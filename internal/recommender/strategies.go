package recommender

import (
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	strategyThroughputPush   = "throughput_push"
	strategyLatencyGuardrail = "latency_guardrail"
	strategyStabilizeFirst   = "stabilize_first"

	findingQueueDominatedTTFT                    = "queue_dominated_ttft"
	findingThroughputSaturationWithQueuePressure = "throughput_saturation_with_queue_pressure"
	findingPrefillHeavyWorkload                  = "prefill_heavy_workload"
	findingCPUOrHostBottleneck                   = "cpu_or_host_bottleneck"
	findingGPUMemorySaturation                   = "gpu_memory_saturation_without_throughput"
	findingGPUHardwareInstability                = "gpu_hardware_instability"
)

func buildStrategyOptions(report *model.RecommendationReport, analysis *model.AnalysisReport, derived derivedContext, objective Objective, calibration *corpusCalibration) []model.RecommendationStrategy {
	if report == nil || analysis == nil || !derived.TrafficObserved {
		return nil
	}

	present := presentFindings(analysis.AnalysisSummary)
	primaryID := preferredStrategyID(objective, present)
	alternateID := alternateStrategyID(primaryID, present)

	candidates := map[string]model.RecommendationStrategy{
		strategyThroughputPush:   buildObjectiveStrategy(analysis, derived, report.BaselinePrediction, calibration, ThroughputFirstObjective),
		strategyLatencyGuardrail: buildObjectiveStrategy(analysis, derived, report.BaselinePrediction, calibration, LatencyFirstObjective),
		strategyStabilizeFirst:   buildStabilizationStrategy(analysis, derived, report.BaselinePrediction, calibration),
	}

	primary := candidates[primaryID]
	if primary.ID == "" {
		primary = candidates[strategyThroughputPush]
	}
	primary.Recommended = true

	alternate := candidates[alternateID]
	if alternate.ID == "" || alternate.ID == primary.ID {
		for _, fallback := range []string{strategyLatencyGuardrail, strategyThroughputPush, strategyStabilizeFirst} {
			if fallback == primary.ID {
				continue
			}
			if candidate := candidates[fallback]; candidate.ID != "" {
				alternate = candidate
				break
			}
		}
	}
	if alternate.ID != "" {
		alternate.Recommended = false
	}

	out := []model.RecommendationStrategy{primary}
	if alternate.ID != "" && alternate.ID != primary.ID {
		out = append(out, alternate)
	}
	return out
}

func preferredStrategyID(objective Objective, present map[string]bool) string {
	switch normalizeObjective(objective) {
	case ThroughputFirstObjective:
		return strategyThroughputPush
	case LatencyFirstObjective:
		return strategyLatencyGuardrail
	default:
		switch {
		case present[findingCPUOrHostBottleneck], present[findingGPUMemorySaturation], present[findingGPUHardwareInstability], present[findingThroughputSaturationWithQueuePressure]:
			return strategyStabilizeFirst
		case present[findingQueueDominatedTTFT], present[findingPrefillHeavyWorkload]:
			return strategyLatencyGuardrail
		default:
			return strategyThroughputPush
		}
	}
}

func alternateStrategyID(primary string, present map[string]bool) string {
	switch primary {
	case strategyThroughputPush:
		return strategyLatencyGuardrail
	case strategyLatencyGuardrail:
		return strategyThroughputPush
	case strategyStabilizeFirst:
		if present[findingQueueDominatedTTFT] || present[findingPrefillHeavyWorkload] {
			return strategyLatencyGuardrail
		}
		return strategyThroughputPush
	default:
		return strategyLatencyGuardrail
	}
}

func buildObjectiveStrategy(analysis *model.AnalysisReport, derived derivedContext, baseline *model.Prediction, calibration *corpusCalibration, objective Objective) model.RecommendationStrategy {
	items := buildIssueRecommendations(analysis, derived, objective, baseline)
	cacheItems := buildCacheRecommendations(analysis, derived, objective)
	calibrateStrategyItems(items, calibration, derived, baseline)
	calibrateStrategyItems(cacheItems, calibration, derived, baseline)

	candidate := selectStrategyItem(items)
	if (candidate == nil || len(candidate.Changes) == 0) && len(cacheItems) > 0 {
		candidate = selectStrategyItem(cacheItems)
	}

	if candidate == nil {
		return fallbackObjectiveStrategy(objective)
	}

	changes := append([]model.ParameterChange(nil), candidate.Changes...)
	if len(changes) > 0 {
		changes = mergeCompatibleChanges(changes, cacheItems)
	}
	finding := analysisFinding(analysis, candidate.IssueID)
	id := strategyThroughputPush
	label := "Throughput Push"
	if normalizeObjective(objective) == LatencyFirstObjective {
		id = strategyLatencyGuardrail
		label = "Latency Guardrail"
	}
	return model.RecommendationStrategy{
		ID:                 id,
		Label:              label,
		Objective:          string(objective),
		Summary:            strings.TrimSpace(candidate.Summary),
		TechnicalRationale: strategyRationale(id, finding, *candidate),
		Tradeoff:           strategyTradeoff(id, *candidate),
		Changes:            changes,
		PredictedEffect:    candidate.PredictedEffect,
		Confidence:         clampFloat(candidate.Confidence, 0, 1),
		Basis:              strings.TrimSpace(candidate.Basis),
		ValidationChecks:   append([]string(nil), candidate.ValidationChecks...),
	}
}

func buildStabilizationStrategy(analysis *model.AnalysisReport, derived derivedContext, baseline *model.Prediction, calibration *corpusCalibration) model.RecommendationStrategy {
	items := buildIssueRecommendations(analysis, derived, BalancedObjective, baseline)
	calibrateStrategyItems(items, calibration, derived, baseline)
	candidate := selectStabilizationItem(items)
	if candidate == nil {
		return model.RecommendationStrategy{
			ID:                 strategyStabilizeFirst,
			Label:              "Stabilize First",
			Objective:          string(BalancedObjective),
			Summary:            "Clear the blocking host, memory, or capacity constraint before pushing a throughput or latency tuning change.",
			TechnicalRationale: "When the dominant limiter is upstream of scheduler tradeoffs, pushing batching or latency knobs first usually moves the symptom instead of removing the bottleneck.",
			Tradeoff:           "This path prioritizes operational stability over immediate benchmark upside.",
			Confidence:         0.52,
			Basis:              "Deterministic fallback strategy for blocker-led workloads.",
			ValidationChecks: []string{
				"Re-run the same traffic and confirm the blocker finding drops before applying more aggressive throughput or latency tuning.",
			},
		}
	}
	finding := analysisFinding(analysis, candidate.IssueID)
	return model.RecommendationStrategy{
		ID:                 strategyStabilizeFirst,
		Label:              "Stabilize First",
		Objective:          string(BalancedObjective),
		Summary:            strings.TrimSpace(candidate.Summary),
		TechnicalRationale: strategyRationale(strategyStabilizeFirst, finding, *candidate),
		Tradeoff:           strategyTradeoff(strategyStabilizeFirst, *candidate),
		Changes:            append([]model.ParameterChange(nil), candidate.Changes...),
		PredictedEffect:    candidate.PredictedEffect,
		Confidence:         clampFloat(candidate.Confidence, 0, 1),
		Basis:              strings.TrimSpace(candidate.Basis),
		ValidationChecks:   append([]string(nil), candidate.ValidationChecks...),
	}
}

func fallbackObjectiveStrategy(objective Objective) model.RecommendationStrategy {
	id := strategyThroughputPush
	label := "Throughput Push"
	summary := "Run a controlled throughput canary before widening batching or concurrency."
	rationale := "Without a dominant issue-linked knob change, the safest next step is a bounded experiment that measures whether unused headroom actually exists."
	tradeoff := "Throughput experiments can lift queue capacity, but they can also worsen TTFT and tail latency if the workload is already prefill- or queue-sensitive."
	if normalizeObjective(objective) == LatencyFirstObjective {
		id = strategyLatencyGuardrail
		label = "Latency Guardrail"
		summary = "Run a latency canary that protects TTFT and queue delay before chasing more throughput."
		rationale = "When the workload goal is responsiveness, smaller chunked-prefill budgets and fairness checks are safer than immediate batch expansion."
		tradeoff = "Latency protection can preserve interactivity, but it may give up some peak throughput until the workload shape is better characterized."
	}
	return model.RecommendationStrategy{
		ID:                 id,
		Label:              label,
		Objective:          string(objective),
		Summary:            summary,
		TechnicalRationale: rationale,
		Tradeoff:           tradeoff,
		Confidence:         0.42,
		Basis:              "Deterministic fallback strategy because no exact issue-linked change set was available.",
		ValidationChecks: []string{
			"Replay the same workload and compare throughput, TTFT, and queue delay before expanding the change.",
		},
	}
}

func selectStrategyItem(items []model.RecommendationItem) *model.RecommendationItem {
	for index := range items {
		if len(items[index].Changes) > 0 {
			return &items[index]
		}
	}
	for index := range items {
		if strings.TrimSpace(items[index].Summary) != "" {
			return &items[index]
		}
	}
	return nil
}

func selectStabilizationItem(items []model.RecommendationItem) *model.RecommendationItem {
	blockers := map[string]bool{
		findingCPUOrHostBottleneck:                   true,
		findingGPUMemorySaturation:                   true,
		findingGPUHardwareInstability:                true,
		findingThroughputSaturationWithQueuePressure: true,
	}
	for index := range items {
		if blockers[items[index].IssueID] {
			return &items[index]
		}
	}
	return selectStrategyItem(items)
}

func calibrateStrategyItems(items []model.RecommendationItem, calibration *corpusCalibration, derived derivedContext, baseline *model.Prediction) {
	if calibration == nil {
		return
	}
	for index := range items {
		calibrateRecommendationItem(&items[index], calibration, derived, baseline)
	}
}

func mergeCompatibleChanges(changes []model.ParameterChange, additional []model.RecommendationItem) []model.ParameterChange {
	seen := map[string]bool{}
	out := append([]model.ParameterChange(nil), changes...)
	for _, change := range out {
		seen[change.Name] = true
	}
	for _, item := range additional {
		for _, change := range item.Changes {
			if seen[change.Name] {
				continue
			}
			seen[change.Name] = true
			out = append(out, change)
		}
	}
	return out
}

func analysisFinding(report *model.AnalysisReport, id string) *model.Finding {
	if report == nil || report.AnalysisSummary == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	for index := range report.AnalysisSummary.Findings {
		if report.AnalysisSummary.Findings[index].ID == id {
			return &report.AnalysisSummary.Findings[index]
		}
	}
	return nil
}

func strategyRationale(strategyID string, finding *model.Finding, item model.RecommendationItem) string {
	if finding != nil && strings.TrimSpace(finding.TechnicalExplanation) != "" {
		return strings.TrimSpace(finding.TechnicalExplanation)
	}
	switch strategyID {
	case strategyLatencyGuardrail:
		return "Protecting TTFT and queue delay usually means constraining prefill budgets and scheduler behavior before widening throughput-oriented batching."
	case strategyStabilizeFirst:
		return "The dominant issue is operational rather than purely scheduler-tunable, so the safest path is to remove the blocker before pushing throughput or latency knobs."
	default:
		if strings.TrimSpace(item.Basis) != "" {
			return strings.TrimSpace(item.Basis)
		}
		return "The current workload shows room to push more useful work through the serving path if the change stays inside latency guardrails."
	}
}

func strategyTradeoff(strategyID string, item model.RecommendationItem) string {
	if len(item.SafetyNotes) > 0 && strings.TrimSpace(item.SafetyNotes[0]) != "" {
		return strings.TrimSpace(item.SafetyNotes[0])
	}
	switch strategyID {
	case strategyLatencyGuardrail:
		return "This path trades some peak throughput upside for lower queue delay, TTFT, and safer tail-latency behavior."
	case strategyStabilizeFirst:
		return "This path delays aggressive tuning until the blocking system issue is cleared."
	default:
		return "This path favors higher throughput and GPU occupancy, but it can increase TTFT or tail latency if the workload is already latency-sensitive."
	}
}
