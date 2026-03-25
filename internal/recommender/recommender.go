package recommender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/llm"
	"github.com/inferLean/inferlean-project/internal/model"
)

func (r *Recommender) Recommend(ctx context.Context, opts Options) (*model.RecommendationReport, error) {
	if strings.TrimSpace(opts.AnalysisPath) == "" {
		return nil, errors.New("analysis path is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	analysis, err := loadAnalysisReport(opts.AnalysisPath)
	if err != nil {
		return nil, err
	}
	missingCurrentConfig := len(analysis.CurrentVLLMConfigurations) == 0
	objective := resolveObjective(opts.Objective, analysis)
	declaredGoal := resolveDeclaredGoal(opts.Objective, analysis)
	guardrail := defaultGuardrailPolicy(objective)

	now := opts.Now
	if now.IsZero() {
		now = r.now().UTC()
	}

	report := &model.RecommendationReport{
		SchemaVersion: model.RecommendationSchemaVersion,
		GeneratedAt:   now,
		ToolName:      model.ToolName,
		ToolVersion:   r.toolVersion,
		SourceAnalysis: model.SourceAnalysisReference{
			SchemaVersion: analysis.SchemaVersion,
			GeneratedAt:   analysis.GeneratedAt,
			ToolVersion:   analysis.ToolVersion,
		},
		Objective: string(objective),
	}
	report.DeclaredGoal = declaredGoal
	report.Guardrail = guardrail.summaryModel()
	report.CurrentServiceState = analysis.ServiceSummary
	if missingCurrentConfig {
		report.Warnings = append(report.Warnings, "analysis report did not include current_vllm_configurations; exact parameter recommendations may be unavailable")
	}

	derived := deriveContext(analysis)
	report.BaselinePrediction = derived.ObservedBaseline
	if !derived.TrafficObserved {
		report.Warnings = append(report.Warnings, "no live traffic was observed; recommendation output is intentionally non-actionable")
		if opts.LLMEnhance {
			if enhanced, warning := llm.EnhanceRecommendationReport(ctx, report); enhanced != nil {
				report.LLMEnhanced = enhanced
			} else if warning != "" {
				report.Warnings = append(report.Warnings, warning)
			}
		}
		return report, nil
	}

	corpus, err := loadCorpus(opts.CorpusPath)
	if err != nil {
		return nil, err
	}
	calibration := nearestCalibrationMatch(corpus, derived)
	if calibration != nil {
		report.MatchedCorpusProfile = matchedCorpusProfileModel(calibration)
		if calibration.BaselinePrediction != nil {
			report.BaselinePrediction = calibration.BaselinePrediction
		}
	}

	report.Recommendations = buildIssueRecommendations(analysis, derived, objective, report.BaselinePrediction)
	applyCorpusCalibration(report, calibration, derived)
	assignRecommendationPriorities(report.Recommendations)

	if len(opts.ScenarioSet) > 0 {
		if calibration != nil {
			if scenario, warning := buildScenarioPrediction(calibration.Match, derived, opts.ScenarioSet); scenario != nil {
				report.ScenarioPrediction = scenario
			} else if warning != "" {
				report.Warnings = append(report.Warnings, warning)
			}
		} else {
			report.ScenarioPrediction = buildFallbackScenarioPrediction(analysis, derived, opts.ScenarioSet, report.BaselinePrediction)
		}
	}

	report.CapacityOpportunity = buildCapacityOpportunity(analysis, report)
	populateRecommendationSummary(report, analysis)

	if opts.LLMEnhance {
		if enhanced, warning := llm.EnhanceRecommendationReport(ctx, report); enhanced != nil {
			report.LLMEnhanced = enhanced
		} else if warning != "" {
			report.Warnings = append(report.Warnings, warning)
		}
	}

	return report, nil
}

func loadAnalysisReport(path string) (*model.AnalysisReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report model.AnalysisReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return analyzer.NormalizeReport(&report, analyzer.BalancedIntent), nil
}

func deriveContext(report *model.AnalysisReport) derivedContext {
	features := analyzer.ExtractFeatures(report)
	current := report.CurrentVLLMConfigurations
	flat := flattenMap(current)
	modelName := lookupString(flat, "model_name", "model", "served_model_name")
	modelFamily := normalizeModelFamily(modelName)
	gpuCount := int(math.Round(analyzer.InferTotalGPUCount(report)))
	if gpuCount <= 0 {
		gpuCount = 1
	}
	workloadClass := inferWorkloadClass(report, features)
	return derivedContext{
		ModelName:        modelName,
		ModelFamily:      modelFamily,
		GPUCount:         gpuCount,
		HardwareClass:    normalizeHardwareClass(report.GPUInformation.GPUModel),
		WorkloadClass:    workloadClass,
		Features:         features,
		CurrentConfig:    current,
		CurrentNumeric:   numericMap(current),
		ObservedBaseline: observedPrediction(features),
		TrafficObserved:  features.TrafficObserved,
	}
}

func buildCapacityOpportunity(analysis *model.AnalysisReport, report *model.RecommendationReport) *model.CapacityOpportunity {
	if analysis == nil || report == nil {
		return nil
	}

	topRecommendation := firstRecommendation(report)
	if topRecommendation == nil {
		return nil
	}

	currentLoadPct, ok := currentGPULoadPctForOpportunity(analysis, report)
	if !ok {
		return nil
	}
	predictedLoadPct, basis, confidence, ok := predictedGPULoadPctForOpportunity(currentLoadPct, topRecommendation)
	if !ok {
		return nil
	}
	recoverableLoadPct := clampFloat(predictedLoadPct-currentLoadPct, 0, 100)
	totalGPUCount := analyzer.InferTotalGPUCount(analysis)
	if totalGPUCount <= 0 {
		totalGPUCount = 1
	}

	estimateMode := "conservative_rule_range"
	pointRecoverablePct := recoverableLoadPct
	pointPredictedLoadPct := predictedLoadPct
	lowRecoverablePct := recoverableLoadPct
	highRecoverablePct := recoverableLoadPct
	if usesPreciseBenchmarkHeadroom(report, topRecommendation) {
		estimateMode = "benchmark_calibrated"
		spread := clampFloat(recoverableLoadPct*(1-clampFloat(confidence, 0, 1))*0.30, 0.5, math.Max(recoverableLoadPct*0.15, 0.5))
		lowRecoverablePct = clampFloat(recoverableLoadPct-spread, 0, recoverableLoadPct)
		highRecoverablePct = clampFloat(recoverableLoadPct+spread, lowRecoverablePct, 100-currentLoadPct)
	} else {
		lowerBoundScale := clampFloat(confidence, 0.45, 0.75)
		pointRecoverablePct = clampFloat(recoverableLoadPct*lowerBoundScale, 0, recoverableLoadPct)
		pointPredictedLoadPct = clampFloat(currentLoadPct+pointRecoverablePct, currentLoadPct, 100)
		lowRecoverablePct = pointRecoverablePct
		highRecoverablePct = recoverableLoadPct
	}

	pointRecoverableCount := totalGPUCount * (pointRecoverablePct / 100)
	lowRecoverableCount := totalGPUCount * (lowRecoverablePct / 100)
	highRecoverableCount := totalGPUCount * (highRecoverablePct / 100)
	predictedLowLoadPct := clampFloat(currentLoadPct+lowRecoverablePct, currentLoadPct, 100)
	predictedHighLoadPct := clampFloat(currentLoadPct+highRecoverablePct, predictedLowLoadPct, 100)

	return &model.CapacityOpportunity{
		CurrentGPULoadPct:              currentLoadPct,
		PredictedOptimalGPULoadPct:     pointPredictedLoadPct,
		RecoverableGPULoadPct:          pointRecoverablePct,
		RecoverableGPUCount:            pointRecoverableCount,
		TotalGPUCount:                  totalGPUCount,
		EstimateMode:                   estimateMode,
		PredictedOptimalGPULoadPctLow:  floatPtr(predictedLowLoadPct),
		PredictedOptimalGPULoadPctHigh: floatPtr(predictedHighLoadPct),
		RecoverableGPULoadPctLow:       floatPtr(lowRecoverablePct),
		RecoverableGPULoadPctHigh:      floatPtr(highRecoverablePct),
		RecoverableGPUCountLow:         floatPtr(lowRecoverableCount),
		RecoverableGPUCountHigh:        floatPtr(highRecoverableCount),
		Basis:                          basis,
		Confidence:                     confidence,
	}
}

func populateRecommendationSummary(report *model.RecommendationReport, analysis *model.AnalysisReport) {
	if report == nil {
		return
	}
	if report.CurrentServiceState == nil && analysis != nil {
		report.CurrentServiceState = analysis.ServiceSummary
	}
	report.MatchSummary = buildMatchSummary(report)
	report.PrimaryAction = buildPrimaryAction(report)
	report.Validation = buildValidationSummary(report.PrimaryAction, report)
	report.PredictedImpact = buildPredictedImpact(report)
	report.WastedCapacity = buildWastedCapacity(report)
	report.AlternativeActions = buildAlternativeActions(report)
}

func buildMatchSummary(report *model.RecommendationReport) *model.MatchSummary {
	if report == nil || report.MatchedCorpusProfile == nil {
		return nil
	}
	match := report.MatchedCorpusProfile
	return &model.MatchSummary{
		ProfileID:  strings.TrimSpace(match.ID),
		MatchScore: clampFloat(match.MatchScore, 0, 1),
		Basis:      strings.TrimSpace(match.Basis),
	}
}

func buildPrimaryAction(report *model.RecommendationReport) *model.PrimaryActionSummary {
	if report == nil || len(report.Recommendations) == 0 {
		return nil
	}
	item := report.Recommendations[0]
	return &model.PrimaryActionSummary{
		Summary:        strings.TrimSpace(item.Summary),
		Changes:        append([]model.ParameterChange(nil), item.Changes...),
		RollbackValues: rollbackChanges(item.Changes),
		Confidence:     clampFloat(item.Confidence, 0, 1),
		Basis:          strings.TrimSpace(item.Basis),
	}
}

func buildAlternativeActions(report *model.RecommendationReport) []model.PrimaryActionSummary {
	if report == nil || len(report.Recommendations) <= 1 {
		return nil
	}
	alternatives := make([]model.PrimaryActionSummary, 0, len(report.Recommendations)-1)
	for _, item := range report.Recommendations[1:] {
		alternatives = append(alternatives, model.PrimaryActionSummary{
			Summary:        strings.TrimSpace(item.Summary),
			Changes:        append([]model.ParameterChange(nil), item.Changes...),
			RollbackValues: rollbackChanges(item.Changes),
			Confidence:     clampFloat(item.Confidence, 0, 1),
			Basis:          strings.TrimSpace(item.Basis),
		})
	}
	return alternatives
}

func rollbackChanges(changes []model.ParameterChange) []model.ParameterChange {
	if len(changes) == 0 {
		return nil
	}
	rollback := make([]model.ParameterChange, 0, len(changes))
	for _, change := range changes {
		rollback = append(rollback, model.ParameterChange{
			Name:             change.Name,
			CurrentValue:     change.RecommendedValue,
			RecommendedValue: change.CurrentValue,
		})
	}
	return rollback
}

func buildValidationSummary(primary *model.PrimaryActionSummary, report *model.RecommendationReport) *model.ValidationSummary {
	if report == nil || len(report.Recommendations) == 0 {
		return nil
	}
	checks := append([]string(nil), report.Recommendations[0].ValidationChecks...)
	if len(checks) == 0 && primary == nil {
		return nil
	}
	return &model.ValidationSummary{Checks: checks}
}

func buildPredictedImpact(report *model.RecommendationReport) *model.PredictedImpactSummary {
	if report == nil {
		return nil
	}
	top := firstRecommendation(report)
	if top == nil && report.ScenarioPrediction == nil {
		return nil
	}

	current := report.CurrentServiceState
	prediction := report.ScenarioPrediction
	if top != nil {
		prediction = &model.Prediction{
			ThroughputTokensPerSecond: top.PredictedEffect.ThroughputTokensPerSecond,
			TTFTMs:                    top.PredictedEffect.TTFTMs,
			LatencyP50Ms:              top.PredictedEffect.LatencyP50Ms,
			LatencyP95Ms:              top.PredictedEffect.LatencyP95Ms,
			GPUUtilizationPct:         top.PredictedEffect.GPUUtilizationPct,
		}
	}

	summary := &model.PredictedImpactSummary{}
	if after, delta := deriveRequestRateImpact(current, top); after != nil || delta != nil {
		summary.RequestRateRPS = model.NumericImpact{After: after, DeltaPct: delta}
	}
	if current != nil {
		if top != nil && top.PredictedEffect.LatencyP50Ms > 0 {
			after := top.PredictedEffect.LatencyP50Ms
			summary.RequestLatencyMS.P50 = model.NumericImpact{After: &after, DeltaPct: deltaFromCurrent(current.RequestLatencyMS.P50, after)}
		}
	}
	if report.CapacityOpportunity != nil && report.CapacityOpportunity.PredictedOptimalGPULoadPct > 0 {
		after := report.CapacityOpportunity.PredictedOptimalGPULoadPct
		value := pctDelta(after, report.CapacityOpportunity.CurrentGPULoadPct)
		summary.GPUUtilizationPct = model.NumericImpact{After: &after, DeltaPct: optionalDelta(value)}
	} else if prediction != nil && prediction.GPUUtilizationPct > 0 {
		after := prediction.GPUUtilizationPct
		summary.GPUUtilizationPct = model.NumericImpact{After: &after, DeltaPct: deltaFromCurrent(currentGPUUtilization(report), after)}
	}
	if summary.RequestRateRPS.After == nil &&
		summary.RequestLatencyMS.Avg.After == nil &&
		summary.RequestLatencyMS.P50.After == nil &&
		summary.RequestLatencyMS.P90.After == nil &&
		summary.SaturationPct.After == nil &&
		summary.GPUUtilizationPct.After == nil {
		return nil
	}
	return summary
}

func buildWastedCapacity(report *model.RecommendationReport) *model.WastedCapacitySummary {
	if report == nil {
		return nil
	}
	afterRPS, deltaPct := deriveRequestRateImpact(report.CurrentServiceState, firstRecommendation(report))
	var gpuHeadroomPct *float64
	var gpuHeadroomCount *float64
	basis := ""
	confidence := 0.0
	if report.CapacityOpportunity != nil {
		value := clampFloat(report.CapacityOpportunity.RecoverableGPULoadPct, 0, 100)
		gpuHeadroomPct = &value
		count := clampFloat(report.CapacityOpportunity.RecoverableGPUCount, 0, report.CapacityOpportunity.TotalGPUCount)
		gpuHeadroomCount = &count
		basis = strings.TrimSpace(report.CapacityOpportunity.Basis)
		confidence = clampFloat(report.CapacityOpportunity.Confidence, 0, 1)
	}
	if preferThroughputHeadroom(report) && afterRPS != nil && report.CurrentServiceState != nil && report.CurrentServiceState.RequestRateRPS != nil {
		gap := *afterRPS - *report.CurrentServiceState.RequestRateRPS
		if gap > 0 {
			headline := fmt.Sprintf("+%.2f req/s recoverable", gap)
			return &model.WastedCapacitySummary{
				Headline:         headline,
				ThroughputGapRPS: &gap,
				ThroughputGapPct: deltaPct,
				GPUHeadroomPct:   gpuHeadroomPct,
				GPUHeadroomCount: gpuHeadroomCount,
				Basis:            firstNonEmpty(strings.TrimSpace(firstRecommendationBasis(report)), basis),
				Confidence:       firstNonZero(firstRecommendationConfidence(report), confidence),
			}
		}
	}
	if gpuHeadroomPct != nil && *gpuHeadroomPct > 0 {
		headline := fmt.Sprintf("%.1fpp GPU load recoverable (%s GPU)", *gpuHeadroomPct, formatRecoverableGPUCount(derefFloat(gpuHeadroomCount)))
		return &model.WastedCapacitySummary{
			Headline:         headline,
			ThroughputGapRPS: nil,
			ThroughputGapPct: deltaPct,
			GPUHeadroomPct:   gpuHeadroomPct,
			GPUHeadroomCount: gpuHeadroomCount,
			Basis:            firstNonEmpty(firstRecommendationBasis(report), basis),
			Confidence:       firstNonZero(firstRecommendationConfidence(report), confidence),
		}
	}
	return nil
}

func firstRecommendation(report *model.RecommendationReport) *model.RecommendationItem {
	if report == nil || len(report.Recommendations) == 0 {
		return nil
	}
	return &report.Recommendations[0]
}

func firstRecommendationBasis(report *model.RecommendationReport) string {
	if top := firstRecommendation(report); top != nil {
		return strings.TrimSpace(top.Basis)
	}
	return ""
}

func firstRecommendationConfidence(report *model.RecommendationReport) float64 {
	if top := firstRecommendation(report); top != nil {
		return clampFloat(top.Confidence, 0, 1)
	}
	return 0
}

func deriveRequestRateImpact(current *model.ServiceSummary, recommendation *model.RecommendationItem) (*float64, *float64) {
	if current == nil || current.RequestRateRPS == nil || recommendation == nil {
		return nil, nil
	}
	if recommendation.PredictedEffect.ThroughputDeltaPct == 0 {
		return nil, nil
	}
	after := applyPctDelta(*current.RequestRateRPS, recommendation.PredictedEffect.ThroughputDeltaPct)
	delta := recommendation.PredictedEffect.ThroughputDeltaPct
	return &after, &delta
}

func applyPctDelta(current, deltaPct float64) float64 {
	return current * (1 + (deltaPct / 100))
}

func derefFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func optionalDelta(value float64) *float64 {
	if value == 0 {
		return nil
	}
	return &value
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func deltaFromCurrent(current *float64, after float64) *float64 {
	if current == nil || *current == 0 {
		return nil
	}
	value := pctDelta(after, *current)
	return optionalDelta(value)
}

func floatPtr(value float64) *float64 {
	v := value
	return &v
}

func currentGPUUtilization(report *model.RecommendationReport) *float64 {
	if report == nil {
		return nil
	}
	if report.CapacityOpportunity != nil && report.CapacityOpportunity.CurrentGPULoadPct > 0 {
		return floatPtr(report.CapacityOpportunity.CurrentGPULoadPct)
	}
	if report.BaselinePrediction != nil && report.BaselinePrediction.GPUUtilizationPct > 0 {
		return floatPtr(report.BaselinePrediction.GPUUtilizationPct)
	}
	return nil
}

func preferThroughputHeadroom(report *model.RecommendationReport) bool {
	if report == nil || report.CapacityOpportunity == nil {
		return true
	}
	return report.CapacityOpportunity.EstimateMode != "benchmark_calibrated"
}

func formatRecoverableGPUCount(value float64) string {
	switch {
	case value >= 0.1:
		return fmt.Sprintf("%.1f", value)
	case value >= 0.01:
		return fmt.Sprintf("%.2f", value)
	case value > 0:
		return "<0.01"
	default:
		return "0.0"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func currentGPULoadPctForOpportunity(analysis *model.AnalysisReport, report *model.RecommendationReport) (float64, bool) {
	if analysis != nil && analysis.CurrentLoadSummary != nil {
		return clampFloat(analysis.CurrentLoadSummary.CurrentGPULoadPct, 0, 100), true
	}
	if report != nil && report.BaselinePrediction != nil && report.BaselinePrediction.GPUUtilizationPct > 0 {
		return clampFloat(report.BaselinePrediction.GPUUtilizationPct, 0, 100), true
	}
	if analysis != nil && analysis.FeatureSummary != nil {
		return clampFloat(analysis.FeatureSummary.AvgGPUUtilizationPct, 0, 100), true
	}
	if analysis != nil && len(analysis.CollectedMetrics) > 0 {
		features := analyzer.ExtractFeatures(analysis)
		if features.AvgGPUUtilizationPct > 0 {
			return clampFloat(features.AvgGPUUtilizationPct, 0, 100), true
		}
	}
	return 0, false
}

func usesPreciseBenchmarkHeadroom(report *model.RecommendationReport, recommendation *model.RecommendationItem) bool {
	if report == nil || recommendation == nil || report.MatchedCorpusProfile == nil || report.BaselinePrediction == nil {
		return false
	}
	if recommendation.RecommendationSource != model.RecommendationSourceHybrid && recommendation.RecommendationSource != model.RecommendationSourceBenchmark {
		return false
	}
	if report.MatchedCorpusProfile.MatchScore < corpusPreciseMinScore {
		return false
	}
	return strings.Contains(strings.ToLower(report.BaselinePrediction.Basis), "benchmark") ||
		strings.Contains(strings.ToLower(report.BaselinePrediction.Basis), "corpus")
}

func predictedGPULoadPctForOpportunity(currentLoadPct float64, recommendation *model.RecommendationItem) (float64, string, float64, bool) {
	if recommendation == nil {
		return 0, "", 0, false
	}
	if recommendation.PredictedEffect.GPUUtilizationPct > 0 {
		return clampFloat(recommendation.PredictedEffect.GPUUtilizationPct, 0, 100), strings.TrimSpace(recommendation.Basis), clampFloat(recommendation.Confidence, 0, 1), true
	}
	if currentLoadPct <= 0 || recommendation.PredictedEffect.ThroughputDeltaPct <= 0 {
		return 0, "", 0, false
	}
	predictedLoadPct := clampFloat(currentLoadPct*(1+(recommendation.PredictedEffect.ThroughputDeltaPct/100)), currentLoadPct, 100)
	basis := strings.TrimSpace(recommendation.Basis)
	if basis != "" {
		basis += " "
	}
	basis += "Estimated GPU utilization from predicted throughput uplift."
	confidence := clampFloat(math.Min(recommendation.Confidence, 0.58), 0.35, 0.58)
	return predictedLoadPct, basis, confidence, true
}

func resolveObjective(explicit Objective, analysis *model.AnalysisReport) Objective {
	if strings.TrimSpace(string(explicit)) != "" {
		return normalizeObjective(explicit)
	}
	if analysis != nil && analysis.WorkloadProfile != nil {
		return normalizeObjective(Objective(analysis.WorkloadProfile.Objective))
	}
	if analysis != nil && analysis.AnalysisSummary != nil {
		return normalizeObjective(Objective(analysis.AnalysisSummary.WorkloadIntent))
	}
	return BalancedObjective
}

func resolveDeclaredGoal(explicit Objective, analysis *model.AnalysisReport) *model.DeclaredGoalSummary {
	if parsed, ok := parseObjective(explicit); ok {
		return &model.DeclaredGoalSummary{
			Value:  string(parsed),
			Source: "objective_flag",
		}
	}
	if analysis == nil || analysis.WorkloadProfile == nil || analysis.WorkloadProfile.Source != model.WorkloadProfileSourceUserInput {
		return nil
	}
	raw := strings.TrimSpace(analysis.WorkloadProfile.Objective)
	if raw == "" || raw == model.WorkloadObjectiveUnknown {
		return nil
	}
	parsed, ok := parseObjective(Objective(raw))
	if !ok {
		return nil
	}
	return &model.DeclaredGoalSummary{
		Value:  string(parsed),
		Source: "intent_file",
	}
}

type guardrailPolicy struct {
	minThroughputRetentionPct float64
	maxLatencyP50IncreasePct  float64
}

func defaultGuardrailPolicy(objective Objective) guardrailPolicy {
	switch normalizeObjective(objective) {
	case LatencyFirstObjective:
		return guardrailPolicy{
			minThroughputRetentionPct: 80,
		}
	case ThroughputFirstObjective:
		return guardrailPolicy{
			maxLatencyP50IncreasePct: 25,
		}
	default:
		return guardrailPolicy{
			minThroughputRetentionPct: 85,
			maxLatencyP50IncreasePct:  15,
		}
	}
}

func (p guardrailPolicy) summaryModel() *model.GuardrailSummary {
	summary := &model.GuardrailSummary{}
	if p.minThroughputRetentionPct > 0 {
		value := clampFloat(p.minThroughputRetentionPct, 0, 100)
		summary.MinThroughputRetentionPct = &value
	}
	if p.maxLatencyP50IncreasePct > 0 {
		value := math.Max(p.maxLatencyP50IncreasePct, 0)
		summary.MaxLatencyP50IncreasePct = &value
	}
	switch {
	case p.minThroughputRetentionPct > 0 && p.maxLatencyP50IncreasePct > 0:
		summary.Summary = fmt.Sprintf(
			"Keep throughput at or above %.0f%% of current while limiting p50 latency growth to +%.0f%%.",
			p.minThroughputRetentionPct,
			p.maxLatencyP50IncreasePct,
		)
	case p.minThroughputRetentionPct > 0:
		summary.Summary = fmt.Sprintf("Keep throughput at or above %.0f%% of current.", p.minThroughputRetentionPct)
	case p.maxLatencyP50IncreasePct > 0:
		summary.Summary = fmt.Sprintf("Keep p50 latency within +%.0f%% of current.", p.maxLatencyP50IncreasePct)
	}
	if summary.Summary == "" {
		return nil
	}
	return summary
}

func parseObjective(value Objective) (Objective, bool) {
	switch Objective(strings.ToLower(strings.TrimSpace(string(value)))) {
	case ThroughputFirstObjective:
		return ThroughputFirstObjective, true
	case LatencyFirstObjective:
		return LatencyFirstObjective, true
	case BalancedObjective:
		return BalancedObjective, true
	default:
		return "", false
	}
}

func inferWorkloadClass(report *model.AnalysisReport, features analyzer.FeatureSet) string {
	if !features.TrafficObserved {
		return "idle"
	}
	if report.AnalysisSummary == nil {
		return "balanced"
	}
	for _, finding := range report.AnalysisSummary.Findings {
		if finding.Status != model.FindingStatusPresent {
			continue
		}
		switch finding.ID {
		case "kv_cache_pressure_preemptions":
			return "memory_pressure"
		case "queue_dominated_ttft":
			return "latency_sensitive"
		case "underutilized_gpu_or_conservative_batching":
			return "throughput_headroom"
		}
	}
	return "balanced"
}

func observedPrediction(features analyzer.FeatureSet) *model.Prediction {
	if !features.TrafficObserved {
		return nil
	}
	totalLatencyMs := (features.AvgQueueTimeSeconds + features.AvgPrefillTimeSeconds + features.AvgDecodeTimeSeconds) * 1000
	confidence := 0.55
	basis := "derived from observed metrics"
	if features.EnoughLatencySamples {
		confidence = 0.72
	}
	if features.IntervalSeconds <= 0 {
		return &model.Prediction{
			TTFTMs:            features.AvgTTFTSeconds * 1000,
			GPUUtilizationPct: features.AvgGPUUtilizationPct,
			Basis:             basis,
			Confidence:        confidence,
		}
	}
	return &model.Prediction{
		ThroughputTokensPerSecond: features.GenerationTokensDelta / features.IntervalSeconds,
		TTFTMs:                    features.AvgTTFTSeconds * 1000,
		LatencyP50Ms:              totalLatencyMs,
		LatencyP95Ms:              totalLatencyMs * 1.35,
		GPUUtilizationPct:         features.AvgGPUUtilizationPct,
		Basis:                     basis,
		Confidence:                confidence,
	}
}

func matchCorpusProfile(corpus *corpusDocument, derived derivedContext) *profileMatch {
	matches := rankCorpusProfiles(corpus, derived)
	if len(matches) == 0 {
		return nil
	}
	return &matches[0]
}

func rankCorpusProfiles(corpus *corpusDocument, derived derivedContext) []profileMatch {
	if corpus == nil {
		return nil
	}
	matches := make([]profileMatch, 0, len(corpus.Profiles))
	for _, profile := range corpus.Profiles {
		score := 0.0
		basisParts := make([]string, 0, 4)
		if strings.EqualFold(strings.TrimSpace(profile.ModelName), strings.TrimSpace(derived.ModelName)) && derived.ModelName != "" {
			score += 0.42
			basisParts = append(basisParts, "exact model match")
		} else if normalizeModelFamily(profile.ModelFamily) != "" && normalizeModelFamily(profile.ModelFamily) == derived.ModelFamily {
			score += 0.30
			basisParts = append(basisParts, "model family match")
		}
		if profile.GPUCount > 0 && profile.GPUCount == derived.GPUCount {
			score += 0.22
			basisParts = append(basisParts, "gpu footprint match")
		}
		if strings.EqualFold(strings.TrimSpace(profile.HardwareClass), strings.TrimSpace(derived.HardwareClass)) && derived.HardwareClass != "" {
			score += 0.18
			basisParts = append(basisParts, "hardware class match")
		}
		if strings.EqualFold(strings.TrimSpace(profile.WorkloadClass), strings.TrimSpace(derived.WorkloadClass)) && derived.WorkloadClass != "" {
			score += 0.18
			basisParts = append(basisParts, "workload class match")
		}
		if score < 0.45 {
			continue
		}
		matches = append(matches, profileMatch{
			Profile:       profile,
			CorpusVersion: corpus.Version,
			Score:         clampFloat(score, 0, 1),
			Basis:         strings.Join(basisParts, ", "),
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Profile.ID < matches[j].Profile.ID
	})
	return matches
}

func buildCorpusRecommendations(matches []profileMatch, derived derivedContext, objective Objective, guardrail guardrailPolicy, baseline *model.Prediction) ([]model.RecommendationItem, []string) {
	if len(matches) == 0 {
		return nil, nil
	}
	const maxCorpusRecommendations = 3
	recommendations := make([]model.RecommendationItem, 0, minInt(len(matches), maxCorpusRecommendations))
	warnings := []string{}
	seenChanges := map[string]struct{}{}

	for index, match := range matches {
		if len(recommendations) >= maxCorpusRecommendations {
			break
		}
		recommendation, warning := buildCorpusRecommendation(match, derived, objective, guardrail, baseline)
		if recommendation == nil {
			if warning != "" && index == 0 {
				warnings = append(warnings, warning)
			}
			continue
		}
		signature := recommendationChangeSignature(recommendation.Changes)
		if signature != "" {
			if _, exists := seenChanges[signature]; exists {
				continue
			}
			seenChanges[signature] = struct{}{}
		}
		recommendation.Priority = len(recommendations) + 1
		if index > 0 {
			recommendation.SafetyNotes = append([]string{
				fmt.Sprintf("Alternative benchmark-backed action from profile %s with a lower profile match score.", match.Profile.ID),
			}, recommendation.SafetyNotes...)
		}
		recommendations = append(recommendations, *recommendation)
	}
	return recommendations, warnings
}

func recommendationChangeSignature(changes []model.ParameterChange) string {
	if len(changes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		parts = append(parts, fmt.Sprintf("%s=%v", change.Name, change.RecommendedValue))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func assignRecommendationPriorities(recommendations []model.RecommendationItem) {
	for index := range recommendations {
		recommendations[index].Priority = index + 1
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func selectNearestMeasurement(profile corpusProfile, current map[string]float64) *measurementSelection {
	if len(profile.Measurements) == 0 {
		return nil
	}
	var best *measurementSelection
	for _, measurement := range profile.Measurements {
		distance := 0.0
		exact := true
		seen := 0
		for key, target := range measurement.Parameters {
			currentValue, ok := current[key]
			if !ok {
				continue
			}
			seen++
			if currentValue != target {
				exact = false
			}
			scale := math.Max(math.Abs(target), 1)
			distance += math.Abs(currentValue-target) / scale
		}
		if seen == 0 {
			exact = false
			distance = 1e9
		}
		candidate := &measurementSelection{Measurement: measurement, Distance: distance, Exact: exact}
		if best == nil || candidate.Distance < best.Distance || (candidate.Distance == best.Distance && candidate.Exact) {
			best = candidate
		}
	}
	if best != nil && best.Distance >= 1e9 {
		return nil
	}
	return best
}

func baselinePredictionBasis(selection measurementSelection) string {
	if selection.Exact {
		return "exact corpus baseline match"
	}
	return "nearest benchmarked baseline approximation"
}

func baselineConfidence(selection measurementSelection) float64 {
	if selection.Exact {
		return 0.96
	}
	return clampFloat(0.82-(selection.Distance*0.18), 0.52, 0.9)
}

func predictionFromMeasurement(measurement corpusMeasurement, basis string, confidence float64) *model.Prediction {
	return &model.Prediction{
		ThroughputTokensPerSecond: measurement.Metrics.ThroughputTokensPerSecond,
		TTFTMs:                    measurement.Metrics.TTFTMs,
		LatencyP50Ms:              measurement.Metrics.LatencyP50Ms,
		LatencyP95Ms:              measurement.Metrics.LatencyP95Ms,
		GPUUtilizationPct:         measurement.Metrics.GPUUtilizationPct,
		Basis:                     basis,
		Confidence:                clampFloat(confidence, 0, 1),
	}
}

func buildCorpusRecommendation(match profileMatch, derived derivedContext, objective Objective, guardrail guardrailPolicy, baseline *model.Prediction) (*model.RecommendationItem, string) {
	baselineSelection := selectNearestMeasurement(match.Profile, derived.CurrentNumeric)
	best, selectionWarning := selectBestMeasurement(match.Profile, objective, guardrail, baselineSelection, baseline)
	if best == nil {
		return nil, selectionWarning
	}
	if baselineSelection != nil && baselineSelection.Exact && measurementsEqual(baselineSelection.Measurement, best.Measurement) {
		return nil, selectionWarning
	}
	changes := buildParameterChanges(derived.CurrentConfig, best.Measurement.Parameters)
	if len(changes) == 0 {
		return nil, selectionWarning
	}
	baseMetrics := baseline
	if baseMetrics == nil && baselineSelection != nil {
		baseMetrics = predictionFromMeasurement(baselineSelection.Measurement, baselinePredictionBasis(*baselineSelection), baselineConfidence(*baselineSelection))
	}
	effect := effectFromMeasurement(best.Measurement, baseMetrics)
	summary := conciseChangeSummary(changes)
	confidence := clampFloat(match.Score*best.confidence, 0.45, 0.98)
	return &model.RecommendationItem{
		ID:              "benchmark_profile_" + match.Profile.ID,
		Priority:        1,
		Objective:       string(objective),
		Summary:         summary,
		Changes:         changes,
		PredictedEffect: effect,
		Confidence:      confidence,
		SafetyNotes: []string{
			"Apply the change set as a single benchmark-backed step, not as independent knob changes.",
			"Keep rollback values for the current configuration before applying the recommendation.",
		},
		ValidationChecks: []string{
			"Replay the same workload and confirm TTFT and p95 stay within the predicted range.",
			"Verify GPU utilization moves in the expected direction without introducing preemptions or queue growth.",
		},
		Basis: fmt.Sprintf("Matched corpus profile %s using %s.", match.Profile.ID, match.Basis),
	}, ""
}

type scoredMeasurement struct {
	Measurement corpusMeasurement
	Score       float64
	confidence  float64
}

type guardrailReference struct {
	throughputTokensPerSecond float64
	latencyP50Ms              float64
}

func selectBestMeasurement(
	profile corpusProfile,
	objective Objective,
	guardrail guardrailPolicy,
	baselineSelection *measurementSelection,
	baseline *model.Prediction,
) (*scoredMeasurement, string) {
	if len(profile.Measurements) == 0 {
		return nil, ""
	}
	maxThroughput := 0.0
	maxGPU := 0.0
	maxTTFT := 0.0
	maxP95 := 0.0
	for _, measurement := range profile.Measurements {
		maxThroughput = math.Max(maxThroughput, measurement.Metrics.ThroughputTokensPerSecond)
		maxGPU = math.Max(maxGPU, measurement.Metrics.GPUUtilizationPct)
		maxTTFT = math.Max(maxTTFT, measurement.Metrics.TTFTMs)
		maxP95 = math.Max(maxP95, measurement.Metrics.LatencyP95Ms)
	}
	reference := buildGuardrailReference(baselineSelection, baseline)
	var best *scoredMeasurement
	var fallback *scoredMeasurement
	blockedAlternative := false
	allowedAlternative := false
	for _, measurement := range profile.Measurements {
		isBaseline := baselineSelection != nil && measurementsEqual(measurement, baselineSelection.Measurement)
		throughputNorm := safeNorm(measurement.Metrics.ThroughputTokensPerSecond, maxThroughput)
		gpuNorm := safeNorm(measurement.Metrics.GPUUtilizationPct, maxGPU)
		ttftPenalty := safeNorm(measurement.Metrics.TTFTMs, maxTTFT)
		p95Penalty := safeNorm(measurement.Metrics.LatencyP95Ms, maxP95)

		score := 0.0
		switch objective {
		case ThroughputFirstObjective:
			score = (throughputNorm * 0.65) + (gpuNorm * 0.25) - (p95Penalty * 0.10)
		case LatencyFirstObjective:
			score = (1-p95Penalty)*0.45 + (1-ttftPenalty)*0.45 + throughputNorm*0.10
		default:
			score = (throughputNorm * 0.45) + (gpuNorm * 0.20) + (1-p95Penalty)*0.20 + (1-ttftPenalty)*0.15
		}
		candidate := &scoredMeasurement{
			Measurement: measurement,
			Score:       score,
			confidence:  0.92,
		}
		if fallback == nil || candidate.Score > fallback.Score {
			fallback = candidate
		}
		if violatesGuardrail(measurement, reference, guardrail) {
			if !isBaseline {
				blockedAlternative = true
			}
			continue
		}
		if !isBaseline {
			allowedAlternative = true
		}
		if best == nil || candidate.Score > best.Score {
			best = candidate
		}
	}
	if best != nil {
		if baselineSelection != nil &&
			measurementsEqual(best.Measurement, baselineSelection.Measurement) &&
			blockedAlternative &&
			!allowedAlternative {
			return best, guardrailSuppressionWarning(objective, guardrail)
		}
		return best, ""
	}
	if fallback == nil {
		return nil, ""
	}
	return nil, guardrailSuppressionWarning(objective, guardrail)
}

func buildGuardrailReference(selection *measurementSelection, baseline *model.Prediction) guardrailReference {
	if baseline != nil && (baseline.ThroughputTokensPerSecond > 0 || baseline.LatencyP50Ms > 0) {
		return guardrailReference{
			throughputTokensPerSecond: baseline.ThroughputTokensPerSecond,
			latencyP50Ms:              baseline.LatencyP50Ms,
		}
	}
	if selection != nil {
		return guardrailReference{
			throughputTokensPerSecond: selection.Measurement.Metrics.ThroughputTokensPerSecond,
			latencyP50Ms:              selection.Measurement.Metrics.LatencyP50Ms,
		}
	}
	return guardrailReference{}
}

func violatesGuardrail(measurement corpusMeasurement, reference guardrailReference, guardrail guardrailPolicy) bool {
	if guardrail.minThroughputRetentionPct > 0 && reference.throughputTokensPerSecond > 0 {
		minThroughput := reference.throughputTokensPerSecond * (guardrail.minThroughputRetentionPct / 100)
		if measurement.Metrics.ThroughputTokensPerSecond < minThroughput {
			return true
		}
	}
	if guardrail.maxLatencyP50IncreasePct > 0 && reference.latencyP50Ms > 0 {
		maxLatency := reference.latencyP50Ms * (1 + (guardrail.maxLatencyP50IncreasePct / 100))
		if measurement.Metrics.LatencyP50Ms > maxLatency {
			return true
		}
	}
	return false
}

func guardrailSuppressionWarning(objective Objective, guardrail guardrailPolicy) string {
	summary := guardrail.summaryModel()
	if summary == nil || strings.TrimSpace(summary.Summary) == "" {
		return ""
	}
	switch normalizeObjective(objective) {
	case LatencyFirstObjective:
		return "no benchmark-backed tuning change satisfied the latency-priority guardrail; current configuration was left unchanged"
	case ThroughputFirstObjective:
		return "no benchmark-backed tuning change satisfied the throughput-priority guardrail; current configuration was left unchanged"
	default:
		return "no benchmark-backed tuning change satisfied the balanced guardrail; current configuration was left unchanged"
	}
}

func safeNorm(value, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return clampFloat(value/max, 0, 1)
}

func buildParameterChanges(current map[string]any, recommended map[string]float64) []model.ParameterChange {
	flat := flattenMap(current)
	keys := fixedKeys(recommended)
	changes := make([]model.ParameterChange, 0, len(keys))
	for _, key := range keys {
		currentValue, _ := lookupAny(flat, key)
		if value, ok := coerceFloat(currentValue); ok && value == recommended[key] {
			continue
		}
		changes = append(changes, model.ParameterChange{
			Name:             key,
			CurrentValue:     currentValue,
			RecommendedValue: recommended[key],
		})
	}
	return changes
}

func conciseChangeSummary(changes []model.ParameterChange) string {
	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		parts = append(parts, fmt.Sprintf("%s=%v", change.Name, change.RecommendedValue))
	}
	return "Apply benchmark-backed tuning: " + strings.Join(parts, ", ")
}

func effectFromMeasurement(measurement corpusMeasurement, baseline *model.Prediction) model.PredictedEffect {
	effect := model.PredictedEffect{
		ThroughputTokensPerSecond: measurement.Metrics.ThroughputTokensPerSecond,
		TTFTMs:                    measurement.Metrics.TTFTMs,
		LatencyP50Ms:              measurement.Metrics.LatencyP50Ms,
		LatencyP95Ms:              measurement.Metrics.LatencyP95Ms,
		GPUUtilizationPct:         measurement.Metrics.GPUUtilizationPct,
	}
	if baseline == nil {
		return effect
	}
	effect.ThroughputDeltaPct = pctDelta(measurement.Metrics.ThroughputTokensPerSecond, baseline.ThroughputTokensPerSecond)
	effect.TTFTDeltaPct = pctDelta(measurement.Metrics.TTFTMs, baseline.TTFTMs)
	effect.LatencyP50DeltaPct = pctDelta(measurement.Metrics.LatencyP50Ms, baseline.LatencyP50Ms)
	effect.LatencyP95DeltaPct = pctDelta(measurement.Metrics.LatencyP95Ms, baseline.LatencyP95Ms)
	effect.GPUUtilizationDeltaPct = pctDelta(measurement.Metrics.GPUUtilizationPct, baseline.GPUUtilizationPct)
	return effect
}

func buildScenarioPrediction(match profileMatch, derived derivedContext, scenario map[string]float64) (*model.Prediction, string) {
	requested := map[string]float64{}
	for key, value := range derived.CurrentNumeric {
		requested[key] = value
	}
	for key, value := range scenario {
		requested[key] = value
	}
	selection := selectNearestMeasurement(match.Profile, requested)
	if selection == nil {
		return nil, "scenario prediction skipped: corpus profile did not contain comparable benchmark points"
	}
	basis := "scenario matched exactly to benchmarked parameters"
	confidence := clampFloat(match.Score*0.95, 0.45, 0.98)
	if !selection.Exact {
		basis = "scenario predicted from nearest benchmarked parameter set"
		confidence = clampFloat(match.Score*0.72, 0.35, 0.85)
	}
	return predictionFromMeasurement(selection.Measurement, basis, confidence), ""
}

func buildFallbackRecommendation(report *model.AnalysisReport, derived derivedContext, objective Objective, baseline *model.Prediction) *model.RecommendationItem {
	if report.AnalysisSummary == nil {
		return nil
	}
	present := map[string]bool{}
	for _, finding := range report.AnalysisSummary.Findings {
		if finding.Status == model.FindingStatusPresent {
			present[finding.ID] = true
		}
	}

	currentSeqs, hasSeqs := derived.CurrentNumeric["max_num_seqs"]
	currentTokens, hasTokens := derived.CurrentNumeric["max_num_batched_tokens"]
	switch {
	case present["underutilized_gpu_or_conservative_batching"] && hasSeqs && hasTokens:
		nextSeqs := math.Ceil(currentSeqs * 1.25)
		nextTokens := math.Ceil(currentTokens * 1.25)
		if objective == LatencyFirstObjective {
			nextSeqs = math.Ceil(currentSeqs * 1.10)
			nextTokens = math.Ceil(currentTokens * 1.10)
		}
		changes := []model.ParameterChange{
			{Name: "max_num_seqs", CurrentValue: currentSeqs, RecommendedValue: nextSeqs},
			{Name: "max_num_batched_tokens", CurrentValue: currentTokens, RecommendedValue: nextTokens},
		}
		return &model.RecommendationItem{
			ID:              "rule_underutilized_gpu",
			Priority:        1,
			Objective:       string(objective),
			Summary:         conciseChangeSummary(changes),
			Changes:         changes,
			PredictedEffect: applyFallbackEffect(baseline, 18, 6, 8, 8, 12),
			Confidence:      0.52,
			SafetyNotes: []string{
				"Fallback recommendation because no close benchmark corpus profile was available.",
			},
			ValidationChecks: []string{
				"Confirm throughput rises without queue time becoming dominant.",
			},
			Basis: "Rule-based fallback from underutilization finding.",
		}
	case present["kv_cache_pressure_preemptions"] && hasSeqs:
		nextSeqs := math.Max(1, math.Floor(currentSeqs*0.85))
		changes := []model.ParameterChange{
			{Name: "max_num_seqs", CurrentValue: currentSeqs, RecommendedValue: nextSeqs},
		}
		return &model.RecommendationItem{
			ID:              "rule_kv_cache_pressure",
			Priority:        1,
			Objective:       string(objective),
			Summary:         conciseChangeSummary(changes),
			Changes:         changes,
			PredictedEffect: applyFallbackEffect(baseline, -8, -12, -10, -14, -6),
			Confidence:      0.48,
			SafetyNotes: []string{
				"Fallback recommendation because no memory-comparable benchmark corpus profile was available.",
			},
			ValidationChecks: []string{
				"Confirm preemptions drop after reducing concurrency.",
			},
			Basis: "Rule-based fallback from KV cache pressure finding.",
		}
	default:
		return nil
	}
}

func buildCacheRecommendations(report *model.AnalysisReport, derived derivedContext, objective Objective) []model.RecommendationItem {
	if report == nil || report.WorkloadProfile == nil || report.WorkloadProfile.Source != model.WorkloadProfileSourceUserInput {
		return nil
	}
	flat := flattenMap(derived.CurrentConfig)
	present := presentFindings(report.AnalysisSummary)
	recommendations := []model.RecommendationItem{}

	if report.WorkloadProfile.PrefixReuse == model.WorkloadProfileReuseHigh && present["prefix_cache_ineffective"] {
		if raw, ok := lookupAny(flat, "enable_prefix_caching"); ok {
			if enabled, ok := coerceBool(raw); ok && !enabled {
				change := model.ParameterChange{
					Name:             "enable_prefix_caching",
					CurrentValue:     raw,
					RecommendedValue: true,
				}
				recommendations = append(recommendations, model.RecommendationItem{
					ID:         "rule_enable_prefix_caching",
					Priority:   2,
					Objective:  string(objective),
					Summary:    conciseRuleSummary("Enable prefix caching for repeated prompt prefixes", []model.ParameterChange{change}),
					Changes:    []model.ParameterChange{change},
					Confidence: 0.82,
					SafetyNotes: []string{
						"Enable the cache first, then replay the same workload before combining it with other tuning changes.",
					},
					ValidationChecks: []string{
						"Confirm prefix-cache hit rate rises and average prefill time drops on the same workload.",
					},
					Basis: "Declared prompt/template reuse is high, the analyzer detected ineffective prefix caching, and the current config has prefix caching disabled.",
				})
			}
		}
	}

	if report.WorkloadProfile.MediaReuse == model.WorkloadProfileReuseHigh && looksMultimodalWorkload(report, derived) {
		if raw, ok := lookupAny(flat, "disable_mm_preprocessor_cache"); ok {
			if disabled, ok := coerceBool(raw); ok && disabled {
				change := model.ParameterChange{
					Name:             "disable_mm_preprocessor_cache",
					CurrentValue:     raw,
					RecommendedValue: false,
				}
				recommendations = append(recommendations, model.RecommendationItem{
					ID:         "rule_enable_mm_preprocessor_cache",
					Priority:   3,
					Objective:  string(objective),
					Summary:    conciseRuleSummary("Enable multimodal preprocessor caching for repeated media inputs", []model.ParameterChange{change}),
					Changes:    []model.ParameterChange{change},
					Confidence: 0.74,
					SafetyNotes: []string{
						"Apply this only for multimodal workloads that actually reuse images or video inputs.",
					},
					ValidationChecks: []string{
						"Confirm multimodal cache hit rate rises and host CPU pressure does not increase on repeated media inputs.",
					},
					Basis: "Declared image/video reuse is high, the workload looks multimodal, and the current config disables multimodal preprocessor caching.",
				})
			}
		}
	}

	return recommendations
}

func buildFallbackScenarioPrediction(report *model.AnalysisReport, derived derivedContext, scenario map[string]float64, baseline *model.Prediction) *model.Prediction {
	if baseline == nil {
		return nil
	}
	seqRatio := 0.0
	tokenRatio := 0.0
	if current, ok := derived.CurrentNumeric["max_num_seqs"]; ok && current > 0 {
		if next, ok := scenario["max_num_seqs"]; ok {
			seqRatio = (next - current) / current
		}
	}
	if current, ok := derived.CurrentNumeric["max_num_batched_tokens"]; ok && current > 0 {
		if next, ok := scenario["max_num_batched_tokens"]; ok {
			tokenRatio = (next - current) / current
		}
	}
	shift := clampFloat((seqRatio+tokenRatio)/2, -0.5, 0.5)
	return &model.Prediction{
		ThroughputTokensPerSecond: baseline.ThroughputTokensPerSecond * (1 + (shift * 0.6)),
		TTFTMs:                    baseline.TTFTMs * (1 + (shift * 0.25)),
		LatencyP50Ms:              baseline.LatencyP50Ms * (1 + (shift * 0.20)),
		LatencyP95Ms:              baseline.LatencyP95Ms * (1 + (shift * 0.28)),
		GPUUtilizationPct:         baseline.GPUUtilizationPct * (1 + (shift * 0.35)),
		Basis:                     "rule-based fallback from observed metrics",
		Confidence:                0.38,
	}
}

func applyFallbackEffect(baseline *model.Prediction, throughputDelta, ttftDelta, p50Delta, p95Delta, gpuDelta float64) model.PredictedEffect {
	if baseline == nil {
		return model.PredictedEffect{
			ThroughputDeltaPct:     throughputDelta,
			TTFTDeltaPct:           ttftDelta,
			LatencyP50DeltaPct:     p50Delta,
			LatencyP95DeltaPct:     p95Delta,
			GPUUtilizationDeltaPct: gpuDelta,
		}
	}
	return model.PredictedEffect{
		ThroughputTokensPerSecond: baseline.ThroughputTokensPerSecond * (1 + throughputDelta/100),
		TTFTMs:                    baseline.TTFTMs * (1 + ttftDelta/100),
		LatencyP50Ms:              baseline.LatencyP50Ms * (1 + p50Delta/100),
		LatencyP95Ms:              baseline.LatencyP95Ms * (1 + p95Delta/100),
		GPUUtilizationPct:         baseline.GPUUtilizationPct * (1 + gpuDelta/100),
		ThroughputDeltaPct:        throughputDelta,
		TTFTDeltaPct:              ttftDelta,
		LatencyP50DeltaPct:        p50Delta,
		LatencyP95DeltaPct:        p95Delta,
		GPUUtilizationDeltaPct:    gpuDelta,
	}
}

func measurementsEqual(a, b corpusMeasurement) bool {
	if len(a.Parameters) != len(b.Parameters) {
		return false
	}
	for key, value := range a.Parameters {
		if b.Parameters[key] != value {
			return false
		}
	}
	return true
}

func presentFindings(summary *model.AnalysisSummary) map[string]bool {
	if summary == nil {
		return nil
	}
	out := map[string]bool{}
	for _, finding := range summary.Findings {
		if finding.Status == model.FindingStatusPresent {
			out[finding.ID] = true
		}
	}
	return out
}

func looksMultimodalWorkload(report *model.AnalysisReport, derived derivedContext) bool {
	if report != nil && report.FeatureSummary != nil && report.FeatureSummary.MultimodalLikely {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(derived.ModelName))
	return strings.Contains(value, "vl") ||
		strings.Contains(value, "vision") ||
		strings.Contains(value, "llava") ||
		strings.Contains(value, "pixtral") ||
		strings.Contains(value, "internvl")
}

func conciseRuleSummary(prefix string, changes []model.ParameterChange) string {
	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		parts = append(parts, fmt.Sprintf("%s=%v", change.Name, change.RecommendedValue))
	}
	return prefix + ": " + strings.Join(parts, ", ")
}
