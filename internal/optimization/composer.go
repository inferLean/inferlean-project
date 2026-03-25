package optimization

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

type ComposeOptions struct {
	ID            string
	Status        string
	ObjectiveMode string
	Constraint    *model.ConstraintV2
	AccessTier    string
}

type scenarioCandidate struct {
	Objective  string
	Label      string
	Decision   string
	Mechanism  string
	Rationale  string
	Upside     string
	Tradeoff   string
	Confidence float64
	Basis      *model.RecommendationBasisV2
	Projection *model.OperatingPointProjectionV2
	Knobs      []model.KnobDeltaV2
}

func ComposeAnalysisReportV2(analysis *model.AnalysisReport, opts ComposeOptions) *model.AnalysisReportV2 {
	if analysis == nil {
		return nil
	}
	objective := resolveObjectiveMode(opts.ObjectiveMode, analysis, nil)
	workload := buildWorkloadContext(analysis, nil, objective, opts.Constraint)
	operating := buildOperatingPoint(analysis, nil)
	pressure := buildPressureSummary(analysis)
	multimodalNotes := buildMultimodalNotes(analysis)
	evidenceQuality := buildEvidenceQuality(analysis)
	return &model.AnalysisReportV2{
		Metadata: model.ReportMetadataV2{
			SchemaVersion: model.AnalysisSchemaVersionV2,
			ReportKind:    "analysis",
			GeneratedAt:   analysis.GeneratedAt,
			ToolName:      analysis.ToolName,
			ToolVersion:   analysis.ToolVersion,
			ID:            strings.TrimSpace(opts.ID),
			Status:        strings.TrimSpace(opts.Status),
		},
		Workload:           workload,
		OperatingPoint:     operating,
		PressureSummary:    pressure,
		FrontierAssessment: buildFrontierAssessment(analysis, pressure),
		EvidenceQuality:    evidenceQuality,
		Findings:           analysisFindings(analysis),
		Configuration:      cloneMap(analysis.CurrentVLLMConfigurations),
		MultimodalNotes:    multimodalNotes,
		Evidence:           buildEvidence(analysis, nil, nil),
	}
}

func ComposeRecommendationReportV2(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, opts ComposeOptions) *model.RecommendationReportV2 {
	if recommendation == nil {
		return nil
	}
	objective := resolveObjectiveMode(opts.ObjectiveMode, analysis, recommendation)
	pressure := buildPressureSummary(analysis)
	primary := buildDecision(analysis, recommendation, objective, pressure)
	scenarios := buildScenarios(analysis, recommendation, objective, opts)
	return &model.RecommendationReportV2{
		Metadata: model.ReportMetadataV2{
			SchemaVersion: model.RecommendationSchemaV2,
			ReportKind:    "recommendation",
			GeneratedAt:   recommendation.GeneratedAt,
			ToolName:      recommendation.ToolName,
			ToolVersion:   recommendation.ToolVersion,
			ID:            strings.TrimSpace(opts.ID),
			Status:        strings.TrimSpace(opts.Status),
		},
		AnalysisRef:         recommendation.SourceAnalysis,
		ObjectiveMode:       objective,
		Constraint:          opts.Constraint,
		PrimaryDecision:     primary,
		Frontier:            buildFrontier(analysis, recommendation, objective, opts.Constraint, primary),
		Scenarios:           scenarios,
		RecommendationBasis: buildRecommendationBasis(recommendation),
		Warnings:            append([]string(nil), recommendation.Warnings...),
	}
}

func ComposeOptimizationReportV2(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, opts ComposeOptions) *model.OptimizationReportV2 {
	if analysis == nil {
		return nil
	}
	objective := resolveObjectiveMode(opts.ObjectiveMode, analysis, recommendation)
	workload := buildWorkloadContext(analysis, recommendation, objective, opts.Constraint)
	operating := buildOperatingPoint(analysis, recommendation)
	pressure := buildPressureSummary(analysis)
	primary := buildDecision(analysis, recommendation, objective, pressure)
	frontier := buildFrontier(analysis, recommendation, objective, opts.Constraint, primary)
	scenarios := buildScenarios(analysis, recommendation, objective, opts)
	basis := buildRecommendationBasis(recommendation)
	report := &model.OptimizationReportV2{
		Metadata: model.ReportMetadataV2{
			SchemaVersion: model.OptimizationSchemaVersionV2,
			ReportKind:    "optimization",
			GeneratedAt:   analysis.GeneratedAt,
			ToolName:      analysis.ToolName,
			ToolVersion:   analysis.ToolVersion,
			ID:            strings.TrimSpace(opts.ID),
			Status:        strings.TrimSpace(opts.Status),
		},
		Workload:            workload,
		OperatingPoint:      operating,
		PressureSummary:     pressure,
		Frontier:            frontier,
		PrimaryDecision:     primary,
		Scenarios:           scenarios,
		Configuration:       cloneMap(analysis.CurrentVLLMConfigurations),
		MultimodalNotes:     buildMultimodalNotes(analysis),
		RecommendationBasis: basis,
		Evidence:            buildEvidence(analysis, recommendation, opts.Constraint),
		Access:              accessForTier(opts.AccessTier),
	}
	if report.Access.Tier == model.AccessTierFree {
		redactForFree(report)
	}
	return report
}

func resolveObjectiveMode(explicit string, analysis *model.AnalysisReport, recommendation *model.RecommendationReport) string {
	switch normalizedObjective(explicit) {
	case "throughput_first", "latency_first", "balanced":
		return normalizedObjective(explicit)
	}
	if recommendation != nil {
		if resolved := normalizedObjective(recommendation.Objective); resolved != "" {
			return resolved
		}
	}
	if analysis != nil && analysis.WorkloadProfile != nil {
		if resolved := normalizedObjective(analysis.WorkloadProfile.Objective); resolved != "" {
			return resolved
		}
	}
	if analysis != nil && analysis.AnalysisSummary != nil {
		if resolved := normalizedObjective(analysis.AnalysisSummary.WorkloadIntent); resolved != "" {
			return resolved
		}
	}
	return "balanced"
}

func buildWorkloadContext(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string, constraint *model.ConstraintV2) model.WorkloadContextV2 {
	var declared *model.WorkloadProfile
	var observed *model.ObservedWorkloadProfile
	var alignment *model.WorkloadProfileAlignment
	if analysis != nil {
		declared = analysis.WorkloadProfile
		observed = analysis.ObservedWorkloadProfile
		alignment = analysis.WorkloadProfileAlignment
	}
	servingPattern := ""
	mediaReuse := ""
	multimodal := false
	if observed != nil {
		servingPattern = observed.ServingPattern
		mediaReuse = observed.MediaReuse
	}
	if declared != nil && servingPattern == "" {
		servingPattern = declared.ServingPattern
	}
	if declared != nil && mediaReuse == "" {
		mediaReuse = declared.MediaReuse
	}
	if analysis != nil && analysis.FeatureSummary != nil {
		multimodal = analysis.FeatureSummary.MultimodalLikely
	}
	if !multimodal {
		multimodal = multimodalConfigPresent(analysis)
	}
	return model.WorkloadContextV2{
		DeclaredIntent: declared,
		ObservedIntent: observed,
		Alignment:      alignment,
		ObjectiveMode:  objective,
		Constraint:     constraint,
		ServingPattern: servingPattern,
		Multimodal:     multimodal,
		MediaReuse:     mediaReuse,
	}
}

func buildOperatingPoint(analysis *model.AnalysisReport, recommendation *model.RecommendationReport) model.OperatingPointV2 {
	point := model.OperatingPointV2{SourceType: model.PressureSourceMeasured}
	if analysis == nil {
		return point
	}
	if analysis.ServiceSummary != nil {
		point.RequestRateRPS = cloneFloat(analysis.ServiceSummary.RequestRateRPS)
		point.Latency = model.OperatingLatencyV2{
			AvgMS:       cloneFloat(analysis.ServiceSummary.RequestLatencyMS.Avg),
			P50MS:       cloneFloat(analysis.ServiceSummary.RequestLatencyMS.P50),
			QueueWaitMS: cloneFloat(analysis.ServiceSummary.Queue.AvgDelayMS),
		}
		if analysis.ServiceSummary.RequestLatencyMS.P99 != nil {
			point.Latency.P95MS = cloneFloat(analysis.ServiceSummary.RequestLatencyMS.P99)
		} else if analysis.ServiceSummary.RequestLatencyMS.P90 != nil {
			point.Latency.P95MS = cloneFloat(analysis.ServiceSummary.RequestLatencyMS.P90)
		}
		point.Concurrency = model.ConcurrencyV2{
			AvgWaiting: cloneFloat(analysis.ServiceSummary.Queue.AvgWaitingRequests),
			MaxWaiting: cloneFloat(analysis.ServiceSummary.Queue.MaxWaitingRequests),
		}
	}
	if analysis.FeatureSummary != nil {
		fs := analysis.FeatureSummary
		point.Multimodal = fs.MultimodalLikely
		point.Host.CPUUtilizationPct = floatPtr(fs.AverageCPUUtilizationPct)
		point.GPU.DeviceUtilPct = floatPtr(fs.AvgGPUUtilizationPct)
		point.GPU.ComputeLoadPct = nullablePct(fs.AvgGPUComputeLoadPct)
		point.GPU.MemoryLoadPct = nullablePct(fs.AvgGPUMemoryBandwidthLoadPct)
		point.GPU.KVCacheUsagePct = pctFromUnitValue(fs.AvgKVCacheUsagePct)
		point.Concurrency.AvgRunning = floatPtr(fs.AvgRequestsRunning)
		point.Concurrency.MaxRunning = floatPtr(fs.MaxRequestsRunning)
		point.Concurrency.AvgWaiting = chooseFloat(point.Concurrency.AvgWaiting, floatPtr(fs.AvgRequestsWaiting))
		point.Concurrency.MaxWaiting = chooseFloat(point.Concurrency.MaxWaiting, floatPtr(fs.MaxRequestsWaiting))
		point.Latency.TTFTMS = secondsToMS(fs.AvgTTFTSeconds)
		if point.Latency.QueueWaitMS == nil {
			point.Latency.QueueWaitMS = secondsToMS(fs.AvgQueueTimeSeconds)
		}
	}
	if analysis.CurrentLoadSummary != nil {
		load := analysis.CurrentLoadSummary
		point.GPU.EffectiveLoadPct = nullablePct(load.CurrentSaturationPct)
		point.GPU.DeviceUtilPct = chooseFloat(point.GPU.DeviceUtilPct, nullablePct(load.CurrentGPULoadPct))
		point.GPU.ComputeLoadPct = chooseFloat(point.GPU.ComputeLoadPct, nullablePct(load.ComputeLoadPct))
		point.GPU.MemoryLoadPct = chooseFloat(point.GPU.MemoryLoadPct, nullablePct(load.MemoryBandwidthLoadPct))
		point.Host.CPUUtilizationPct = chooseFloat(point.Host.CPUUtilizationPct, nullablePct(load.CPULoadPct))
		if load.TotalGPUCount > 0 {
			point.GPU.Count = int(math.Round(load.TotalGPUCount))
		}
	}
	if point.GPU.Count == 0 && recommendation != nil && recommendation.MatchedCorpusProfile != nil && recommendation.MatchedCorpusProfile.GPUCount > 0 {
		point.GPU.Count = recommendation.MatchedCorpusProfile.GPUCount
	}
	if recommendation != nil && recommendation.BaselinePrediction != nil {
		point.ThroughputTokensPerSecond = nullablePositive(recommendation.BaselinePrediction.ThroughputTokensPerSecond)
		point.Latency.TTFTMS = chooseFloat(point.Latency.TTFTMS, nullablePositive(recommendation.BaselinePrediction.TTFTMs))
		point.Latency.P50MS = chooseFloat(point.Latency.P50MS, nullablePositive(recommendation.BaselinePrediction.LatencyP50Ms))
		point.Latency.P95MS = chooseFloat(point.Latency.P95MS, nullablePositive(recommendation.BaselinePrediction.LatencyP95Ms))
		point.GPU.DeviceUtilPct = chooseFloat(point.GPU.DeviceUtilPct, nullablePositive(recommendation.BaselinePrediction.GPUUtilizationPct))
		if point.SourceType == model.PressureSourceMeasured {
			point.SourceType = model.PressureSourceMixed
		}
	}
	return point
}

func buildPressureSummary(analysis *model.AnalysisReport) model.PressureSummaryV2 {
	compute := pressureCompute(analysis)
	memory := pressureMemory(analysis)
	kv := pressureKVCache(analysis)
	queue := pressureQueue(analysis)
	host := pressureHostInput(analysis, compute, memory)
	return model.PressureSummaryV2{
		DominantBottleneck: dominantBottleneck(compute, memory, kv, queue, host),
		Compute:            compute,
		MemoryBandwidth:    memory,
		KVCache:            kv,
		Queue:              queue,
		HostInputPipeline:  host,
	}
}

func pressureCompute(analysis *model.AnalysisReport) model.PressureDimensionV2 {
	value, source, evidence := computeLoadValue(analysis)
	status := statusFromThreshold(value, 60, 85)
	if source == "" {
		status = model.PressureStatusInsufficientEvidence
	}
	return model.PressureDimensionV2{
		PressureStatus: status,
		Confidence:     confidenceFromSource(source, value),
		SourceType:     sourceType(source),
		Evidence:       evidence,
		Summary:        summaryForPressure("compute", status, value, source),
	}
}

func pressureMemory(analysis *model.AnalysisReport) model.PressureDimensionV2 {
	value, available, source, evidence := memoryLoadValue(analysis)
	status := statusFromThreshold(value, 60, 85)
	if !available {
		status = model.PressureStatusInsufficientEvidence
	}
	return model.PressureDimensionV2{
		PressureStatus: status,
		Confidence:     confidenceFromSource(source, value),
		SourceType:     sourceType(source),
		Evidence:       evidence,
		Summary:        summaryForPressure("memory bandwidth", status, value, source),
	}
}

func pressureKVCache(analysis *model.AnalysisReport) model.PressureDimensionV2 {
	if analysis == nil || analysis.FeatureSummary == nil {
		return insufficientPressure("KV/cache pressure could not be established from current samples.")
	}
	fs := analysis.FeatureSummary
	avg := fs.AvgKVCacheUsagePct * 100
	max := fs.MaxKVCacheUsagePct * 100
	preemptions := fs.PreemptionsDelta
	status := model.PressureStatusLow
	switch {
	case preemptions > 0 || avg >= 85 || max >= 95:
		status = model.PressureStatusHigh
	case avg >= 60 || max >= 75:
		status = model.PressureStatusModerate
	}
	if !fs.EnoughKVCacheSamples {
		status = model.PressureStatusInsufficientEvidence
	}
	evidence := []model.EvidenceItem{
		{Metric: "avg_kv_cache_usage_pct", Value: avg},
		{Metric: "max_kv_cache_usage_pct", Value: max},
	}
	if preemptions > 0 {
		evidence = append(evidence, model.EvidenceItem{Metric: "preemptions_delta", Value: preemptions})
	}
	return model.PressureDimensionV2{
		PressureStatus: status,
		Confidence:     clampFloat(0.55+normalizePercent(max)*0.35, 0.45, 0.92),
		SourceType:     model.PressureSourceMeasured,
		Evidence:       evidence,
		Summary:        kvSummary(status, preemptions, avg, max),
	}
}

func pressureQueue(analysis *model.AnalysisReport) model.PressureDimensionV2 {
	if analysis == nil || analysis.ServiceSummary == nil {
		return insufficientPressure("Queue pressure could not be established from current samples.")
	}
	queue := analysis.ServiceSummary.Queue
	avgDelay := floatValue(queue.AvgDelayMS)
	pressureRatio := floatValue(queue.PressureRatio)
	status := model.PressureStatusLow
	switch {
	case queue.Health == "severe" || pressureRatio >= 3 || avgDelay >= 250:
		status = model.PressureStatusHigh
	case queue.Health == "elevated" || pressureRatio >= 1.5 || avgDelay >= 75:
		status = model.PressureStatusModerate
	}
	evidence := []model.EvidenceItem{
		{Metric: "avg_delay_ms", Value: avgDelay},
		{Metric: "pressure_ratio", Value: pressureRatio},
	}
	if queue.AvgWaitingRequests != nil {
		evidence = append(evidence, model.EvidenceItem{Metric: "avg_waiting_requests", Value: *queue.AvgWaitingRequests})
	}
	return model.PressureDimensionV2{
		PressureStatus: status,
		Confidence:     clampFloat(0.60+math.Min(pressureRatio/10, 0.22), 0.55, 0.94),
		SourceType:     model.PressureSourceMeasured,
		Evidence:       evidence,
		Summary:        queueSummary(status, avgDelay, pressureRatio),
	}
}

func pressureHostInput(analysis *model.AnalysisReport, compute, memory model.PressureDimensionV2) model.PressureDimensionV2 {
	if analysis == nil {
		return insufficientPressure("Host/input pipeline pressure could not be established from current samples.")
	}
	cpu := hostCPUValue(analysis)
	multimodal := workloadIsMultimodal(analysis)
	mmCacheDisabled := analysis.FeatureSummary != nil && analysis.FeatureSummary.MMPreprocessorCacheDisabled
	lowGPU := compute.PressureStatus != model.PressureStatusHigh && memory.PressureStatus != model.PressureStatusHigh
	hostFinding := findingPresent(analysis, "cpu_or_host_bottleneck") || findingPresent(analysis, "multimodal_preprocessing_cpu_bottleneck")
	status := model.PressureStatusLow
	switch {
	case (multimodal && lowGPU && cpu >= 70) || (hostFinding && multimodal) || (mmCacheDisabled && multimodal):
		status = model.PressureStatusHigh
	case (multimodal && lowGPU && cpu >= 45) || hostFinding || mmCacheDisabled:
		status = model.PressureStatusModerate
	}
	if cpu == 0 && !hostFinding && !multimodal {
		return insufficientPressure("Host/input pipeline pressure needs stronger CPU or multimodal evidence.")
	}
	evidence := []model.EvidenceItem{}
	if cpu > 0 {
		evidence = append(evidence, model.EvidenceItem{Metric: "average_cpu_utilization_pct", Value: cpu})
	}
	if multimodal {
		evidence = append(evidence, model.EvidenceItem{Metric: "multimodal_likely", Value: 1, Note: "bool"})
	}
	if mmCacheDisabled {
		evidence = append(evidence, model.EvidenceItem{Metric: "mm_preprocessor_cache_disabled", Value: 1, Note: "bool"})
	}
	return model.PressureDimensionV2{
		PressureStatus: status,
		Confidence:     clampFloat(0.48+(cpu/200), 0.45, 0.90),
		SourceType:     hostSourceType(multimodal, cpu),
		Evidence:       evidence,
		Summary:        hostSummary(status, multimodal, mmCacheDisabled),
	}
}

func dominantBottleneck(compute, memory, kv, queue, host model.PressureDimensionV2) string {
	if host.PressureStatus == model.PressureStatusHigh {
		return "host_input_pipeline"
	}
	if queue.PressureStatus == model.PressureStatusHigh && compute.PressureStatus != model.PressureStatusHigh && memory.PressureStatus != model.PressureStatusHigh {
		return "queue"
	}
	if kv.PressureStatus == model.PressureStatusHigh {
		return "kv_cache"
	}
	if memory.PressureStatus == model.PressureStatusHigh && memory.Confidence >= compute.Confidence {
		return "memory_bandwidth"
	}
	if compute.PressureStatus == model.PressureStatusHigh {
		return "compute"
	}
	if host.PressureStatus == model.PressureStatusModerate && host.Confidence >= 0.7 {
		return "host_input_pipeline"
	}
	if queue.PressureStatus == model.PressureStatusModerate {
		return "queue"
	}
	if kv.PressureStatus == model.PressureStatusModerate {
		return "kv_cache"
	}
	if memory.PressureStatus == model.PressureStatusModerate && memory.Confidence >= compute.Confidence {
		return "memory_bandwidth"
	}
	if compute.PressureStatus == model.PressureStatusModerate {
		return "compute"
	}
	return "insufficient_evidence"
}

func buildFrontierAssessment(analysis *model.AnalysisReport, pressure model.PressureSummaryV2) model.FrontierAssessmentV2 {
	proximity := model.FrontierProximityUnknown
	reason := "Current evidence is not strong enough to place the workload relative to a practical frontier."
	var near *bool
	switch pressure.DominantBottleneck {
	case "queue", "host_input_pipeline":
		value := false
		near = &value
		proximity = model.FrontierProximityLow
		reason = "The workload is limited before GPU saturation, so there is likely practical headroom on this node."
	case "compute", "memory_bandwidth", "kv_cache":
		value := true
		near = &value
		proximity = model.FrontierProximityHigh
		reason = "The current bottleneck is already on a core serving resource, so software-only headroom is likely narrower."
	}
	if analysis != nil && analysis.AnalysisSummary != nil && !analysis.AnalysisSummary.DataQuality.TrafficObserved {
		near = nil
		proximity = model.FrontierProximityUnknown
		reason = "No live traffic was observed, so frontier proximity is unknown."
	}
	return model.FrontierAssessmentV2{
		IsNearFrontier:    near,
		FrontierProximity: proximity,
		FrontierReason:    reason,
	}
}

func buildFrontier(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string, constraint *model.ConstraintV2, primary model.DecisionV2) model.FrontierV2 {
	assessment := buildFrontierAssessment(analysis, buildPressureSummary(analysis))
	frontier := model.FrontierV2{
		IsNearFrontier:    assessment.IsNearFrontier,
		FrontierProximity: assessment.FrontierProximity,
		FrontierReason:    assessment.FrontierReason,
	}
	if recommendation == nil {
		return frontier
	}
	best := selectedScenarioCandidate(analysis, recommendation, objective)
	if best != nil && best.Projection != nil {
		if best.Projection.ThroughputTokensPerSecond != nil {
			frontier.SafeMaxThroughputAtConstraint = &model.FrontierValueV2{
				Value:      cloneFloat(best.Projection.ThroughputTokensPerSecond),
				Unit:       "tok/s",
				Confidence: best.Confidence,
				SourceType: confidenceToSource(primary.ConfidenceSource),
			}
		}
		if best.Projection.P95LatencyMS != nil {
			frontier.ExpectedLatencyFloorAtConstraint = &model.FrontierValueV2{
				Value:      cloneFloat(best.Projection.P95LatencyMS),
				Unit:       "ms",
				Confidence: best.Confidence,
				SourceType: confidenceToSource(primary.ConfidenceSource),
			}
		}
	}
	if recommendation.WastedCapacity != nil && recommendation.WastedCapacity.ThroughputGapPct != nil {
		frontier.PracticalNodeHeadroom = &model.FrontierValueV2{
			Value:      cloneFloat(recommendation.WastedCapacity.ThroughputGapPct),
			Unit:       "pct",
			Confidence: recommendation.WastedCapacity.Confidence,
			SourceType: confidenceToSource(primary.ConfidenceSource),
		}
	}
	if constraint != nil && constraint.TargetP95LatencyMS != nil {
		frontier.FrontierReason = fmt.Sprintf("%s Relative to a target p95 latency constraint of %.0f ms.", frontier.FrontierReason, *constraint.TargetP95LatencyMS)
	}
	if constraint != nil && constraint.MinThroughput != nil {
		frontier.FrontierReason = fmt.Sprintf("%s Relative to a minimum throughput target of %.0f.", frontier.FrontierReason, *constraint.MinThroughput)
	}
	if primary.Kind == model.DecisionKindConsiderHardwareChange {
		value := true
		frontier.IsNearFrontier = &value
		frontier.FrontierProximity = model.FrontierProximityHigh
		frontier.FrontierReason = "Software-only gains appear limited relative to the current objective and resource pressure profile."
	}
	return frontier
}

func buildEvidenceQuality(analysis *model.AnalysisReport) model.EvidenceQualityV2 {
	if analysis == nil || analysis.AnalysisSummary == nil {
		return model.EvidenceQualityV2{
			Status:  model.EvidenceQualityWeak,
			Summary: "Analysis evidence is unavailable.",
		}
	}
	quality := model.EvidenceQualityPartial
	reasons := []string{}
	if analysis.AnalysisSummary.DataQuality.TrafficObserved &&
		analysis.AnalysisSummary.DataQuality.EnoughLatencySamples &&
		analysis.AnalysisSummary.DataQuality.EnoughKVCacheSamples {
		quality = model.EvidenceQualityStrong
		reasons = append(reasons, "Live traffic, latency, and KV/cache samples were all sufficient.")
	} else {
		if !analysis.AnalysisSummary.DataQuality.TrafficObserved {
			quality = model.EvidenceQualityWeak
			reasons = append(reasons, "No live traffic was observed.")
		}
		if !analysis.AnalysisSummary.DataQuality.EnoughLatencySamples {
			reasons = append(reasons, "Latency sampling was incomplete.")
		}
		if !analysis.AnalysisSummary.DataQuality.EnoughKVCacheSamples {
			reasons = append(reasons, "KV/cache sampling was incomplete.")
		}
	}
	summary := "Evidence is partially sufficient for diagnosis."
	switch quality {
	case model.EvidenceQualityStrong:
		summary = "Evidence quality is strong enough for diagnosis and recommendation ranking."
	case model.EvidenceQualityWeak:
		summary = "Evidence quality is weak; prefer conservative decisions."
	}
	return model.EvidenceQualityV2{
		Status:            quality,
		Summary:           summary,
		Reasons:           reasons,
		SnapshotCount:     analysis.AnalysisSummary.DataQuality.SnapshotCount,
		LatencySufficient: analysis.AnalysisSummary.DataQuality.EnoughLatencySamples,
		KVCacheSufficient: analysis.AnalysisSummary.DataQuality.EnoughKVCacheSamples,
	}
}

func buildRecommendationBasis(recommendation *model.RecommendationReport) model.RecommendationBasisV2 {
	if recommendation == nil {
		return model.RecommendationBasisV2{Source: "analysis_only"}
	}
	source := "analysis_only"
	if recommendation.MatchedCorpusProfile != nil {
		source = "benchmark"
	}
	if len(recommendation.Recommendations) > 0 {
		switch recommendation.Recommendations[0].RecommendationSource {
		case model.RecommendationSourceHybrid:
			source = "hybrid"
		case model.RecommendationSourceRule:
			source = "rule"
		case model.RecommendationSourceBenchmark:
			source = "benchmark"
		}
	}
	summary := ""
	if recommendation.MatchSummary != nil {
		summary = strings.TrimSpace(recommendation.MatchSummary.Basis)
	}
	if summary == "" && recommendation.PrimaryAction != nil {
		summary = strings.TrimSpace(recommendation.PrimaryAction.Basis)
	}
	var validation []string
	if recommendation.Validation != nil {
		validation = append(validation, recommendation.Validation.Checks...)
	}
	return model.RecommendationBasisV2{
		Source:           source,
		Summary:          summary,
		MatchedBenchmark: recommendation.MatchedCorpusProfile,
		ValidationChecks: validation,
		Warnings:         append([]string(nil), recommendation.Warnings...),
	}
}

func buildDecision(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string, pressure model.PressureSummaryV2) model.DecisionV2 {
	basis := buildRecommendationBasis(recommendation)
	best := selectedScenarioCandidate(analysis, recommendation, objective)
	if analysis == nil || analysis.AnalysisSummary == nil || !analysis.AnalysisSummary.DataQuality.TrafficObserved {
		return model.DecisionV2{
			Kind:             model.DecisionKindInsufficientEvidence,
			Reason:           "No live traffic was observed or evidence quality was too weak for a stronger recommendation.",
			Confidence:       0.35,
			ConfidenceSource: model.ConfidenceSourceLimitedEvidence,
			PrimaryMechanism: model.MechanismKeepCurrentOperatingMode,
		}
	}

	hostDominant := pressure.DominantBottleneck == "host_input_pipeline"
	queueDominant := pressure.DominantBottleneck == "queue"
	resourceDominant := pressure.DominantBottleneck == "compute" || pressure.DominantBottleneck == "memory_bandwidth" || pressure.DominantBottleneck == "kv_cache"
	changeTraffic := shouldChangeTrafficShape(analysis, pressure)
	confidenceSource := model.ConfidenceSourceMeasuredOnly
	if basis.Source == "benchmark" {
		confidenceSource = model.ConfidenceSourceBenchmarkCalib
	} else if basis.Source == "hybrid" {
		confidenceSource = model.ConfidenceSourceHybrid
	} else if basis.Source == "rule" {
		confidenceSource = model.ConfidenceSourceRuleBased
	}

	if hostDominant {
		return model.DecisionV2{
			Kind:             model.DecisionKindOptimizeInputPipeline,
			Reason:           "Host-side preprocessing or CPU-side work appears to dominate before GPU saturation.",
			Confidence:       clampFloat(pressure.HostInputPipeline.Confidence, 0.55, 0.90),
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: model.MechanismReduceHostPreprocessing,
			ExpectedEffect:   "Reduce end-to-end latency before widening serving-side batching or concurrency.",
		}
	}
	if changeTraffic {
		return model.DecisionV2{
			Kind:             model.DecisionKindChangeTrafficShape,
			Reason:           "The observed workload mix suggests traffic classes should be split before another knob-only tuning pass.",
			Confidence:       0.68,
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: model.MechanismSplitTrafficClasses,
			ExpectedEffect:   "Reduce interference between traffic classes and make frontier estimates more reliable.",
		}
	}
	if best == nil {
		return model.DecisionV2{
			Kind:             model.DecisionKindKeepCurrent,
			Reason:           "No stronger deterministic action was synthesized from the current evidence.",
			Confidence:       0.48,
			ConfidenceSource: model.ConfidenceSourceLimitedEvidence,
			PrimaryMechanism: model.MechanismKeepCurrentOperatingMode,
		}
	}
	if resourceDominant && !scenarioShowsMaterialGain(best) {
		return model.DecisionV2{
			Kind:             model.DecisionKindConsiderHardwareChange,
			Reason:           "The node already appears close to a core serving bottleneck, and the best software path does not show material upside.",
			Confidence:       clampFloat(best.Confidence, 0.60, 0.88),
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: model.MechanismAddHardwareCapacity,
			ExpectedEffect:   "Meaningful additional throughput or latency improvement likely requires more capacity or a different serving shape.",
		}
	}
	if len(best.Knobs) > 0 && allowExactKnobs(best, pressure) {
		return model.DecisionV2{
			Kind:             model.DecisionKindApplyConfigChange,
			Reason:           best.Rationale,
			Confidence:       clampFloat(best.Confidence, 0.55, 0.95),
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: best.Mechanism,
			ExpectedEffect:   best.Upside,
			ExactKnobDeltas:  append([]model.KnobDeltaV2(nil), best.Knobs...),
		}
	}
	if queueDominant && len(best.Knobs) == 0 {
		return model.DecisionV2{
			Kind:             model.DecisionKindApplyConfigChange,
			Reason:           "Queue delay is the dominant limiter and the safest next move is to reduce scheduler pressure.",
			Confidence:       clampFloat(best.Confidence, 0.55, 0.85),
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: model.MechanismReduceQueueing,
			ExpectedEffect:   best.Upside,
		}
	}
	if !scenarioShowsMaterialGain(best) {
		return model.DecisionV2{
			Kind:             model.DecisionKindKeepCurrent,
			Reason:           "The current setup already looks close to the practical frontier for the selected objective.",
			Confidence:       clampFloat(best.Confidence, 0.55, 0.88),
			ConfidenceSource: confidenceSource,
			PrimaryMechanism: model.MechanismKeepCurrentOperatingMode,
			ExpectedEffect:   "Keep the current operating mode and validate with another replay before widening changes.",
		}
	}
	return model.DecisionV2{
		Kind:             model.DecisionKindApplyConfigChange,
		Reason:           best.Rationale,
		Confidence:       clampFloat(best.Confidence, 0.55, 0.95),
		ConfidenceSource: confidenceSource,
		PrimaryMechanism: best.Mechanism,
		ExpectedEffect:   best.Upside,
	}
}

func buildScenarios(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string, opts ComposeOptions) model.ScenarioSetV2 {
	throughput := scenarioForObjective(analysis, recommendation, "throughput_first", opts)
	latency := scenarioForObjective(analysis, recommendation, "latency_first", opts)
	balanced := scenarioForObjective(analysis, recommendation, "balanced", opts)
	recommended := scenarioForObjective(analysis, recommendation, objective, opts)
	recommended.Slot = "recommended_decision"
	recommended.ObjectiveMode = objective
	return model.ScenarioSetV2{
		RecommendedDecision: recommended,
		ThroughputFirst:     throughput,
		LatencyFirst:        latency,
		Balanced:            balanced,
	}
}

func scenarioForObjective(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string, opts ComposeOptions) model.ScenarioV2 {
	slot := objective
	candidate := selectedScenarioCandidate(analysis, recommendation, objective)
	pressure := buildPressureSummary(analysis)
	decision := buildDecision(analysis, recommendation, objective, pressure)
	if candidate == nil {
		return model.ScenarioV2{
			Slot:           slot,
			ObjectiveMode:  objective,
			EvidenceState:  fallbackEvidenceState(analysis),
			DecisionKind:   decision.Kind,
			Mechanism:      decision.PrimaryMechanism,
			Rationale:      decision.Reason,
			ExpectedUpside: decision.ExpectedEffect,
			Confidence:     decision.Confidence,
		}
	}
	evidenceState := model.ScenarioEvidenceAvailable
	if opts.AccessTier == model.AccessTierFree {
		evidenceState = model.ScenarioEvidencePreview
	}
	knobs := candidate.Knobs
	if !allowExactKnobs(candidate, pressure) {
		knobs = nil
	}
	scenario := model.ScenarioV2{
		Slot:                    slot,
		ObjectiveMode:           objective,
		EvidenceState:           evidenceState,
		DecisionKind:            candidate.Decision,
		Mechanism:               candidate.Mechanism,
		Rationale:               candidate.Rationale,
		ExpectedUpside:          candidate.Upside,
		Tradeoff:                candidate.Tradeoff,
		Confidence:              candidate.Confidence,
		RecommendationBasis:     candidate.Basis,
		ProjectedOperatingPoint: candidate.Projection,
		ExactKnobDeltas:         knobs,
	}
	if opts.AccessTier == model.AccessTierFree {
		scenario.ProjectedOperatingPoint = nil
		scenario.ExactKnobDeltas = nil
		if scenario.RecommendationBasis != nil {
			scenario.RecommendationBasis.MatchedBenchmark = nil
		}
	}
	return scenario
}

func selectedScenarioCandidate(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, objective string) *scenarioCandidate {
	if recommendation == nil {
		return fallbackScenarioCandidate(analysis, objective)
	}
	candidates := scenarioCandidatesFromRecommendation(analysis, recommendation)
	for i := range candidates {
		if candidates[i].Objective == objective {
			return &candidates[i]
		}
	}
	for i := range candidates {
		if candidates[i].Label == "Recommended Decision" || strings.EqualFold(candidates[i].Objective, recommendation.Objective) {
			return &candidates[i]
		}
	}
	if len(candidates) > 0 {
		return &candidates[0]
	}
	return fallbackScenarioCandidate(analysis, objective)
}

func scenarioCandidatesFromRecommendation(analysis *model.AnalysisReport, recommendation *model.RecommendationReport) []scenarioCandidate {
	candidates := []scenarioCandidate{}
	basis := buildRecommendationBasis(recommendation)
	for _, strategy := range recommendation.StrategyOptions {
		mechanism := inferMechanismFromChanges(strategy.Changes, strategy.Summary, strategy.Objective, analysis)
		decision := inferDecisionKind(strategy.Changes, mechanism, buildPressureSummary(analysis), analysis, summaryBasis(strategy.Summary, strategy.Basis))
		candidates = append(candidates, scenarioCandidate{
			Objective:  normalizedObjective(strategy.Objective),
			Label:      strategy.Label,
			Decision:   decision,
			Mechanism:  mechanism,
			Rationale:  firstNonEmpty(strategy.TechnicalRationale, strategy.Summary, strategy.Basis),
			Upside:     summarizePredictedEffect(strategy.PredictedEffect),
			Tradeoff:   strategy.Tradeoff,
			Confidence: strategy.Confidence,
			Basis:      cloneBasis(&basis),
			Projection: projectionFromEffect(strategy.PredictedEffect, recommendation.CurrentServiceState),
			Knobs:      knobDeltas(strategy.Changes),
		})
	}
	if len(candidates) == 0 && len(recommendation.Recommendations) > 0 {
		item := recommendation.Recommendations[0]
		mechanism := inferMechanismFromChanges(item.Changes, item.Summary, item.Objective, analysis)
		candidates = append(candidates, scenarioCandidate{
			Objective:  normalizedObjective(item.Objective),
			Label:      "Recommended Decision",
			Decision:   inferDecisionKind(item.Changes, mechanism, buildPressureSummary(analysis), analysis, item.Summary),
			Mechanism:  mechanism,
			Rationale:  firstNonEmpty(item.IssueSummary, item.Summary, item.Basis),
			Upside:     summarizePredictedEffect(item.PredictedEffect),
			Tradeoff:   firstString(item.SafetyNotes),
			Confidence: item.Confidence,
			Basis:      cloneBasis(&basis),
			Projection: projectionFromEffect(item.PredictedEffect, recommendation.CurrentServiceState),
			Knobs:      knobDeltas(item.Changes),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Confidence > candidates[j].Confidence })
	return candidates
}

func fallbackScenarioCandidate(analysis *model.AnalysisReport, objective string) *scenarioCandidate {
	if analysis == nil {
		return nil
	}
	pressure := buildPressureSummary(analysis)
	decision := buildDecision(analysis, nil, objective, pressure)
	return &scenarioCandidate{
		Objective:  objective,
		Label:      objective,
		Decision:   decision.Kind,
		Mechanism:  decision.PrimaryMechanism,
		Rationale:  decision.Reason,
		Upside:     decision.ExpectedEffect,
		Confidence: decision.Confidence,
	}
}

func buildMultimodalNotes(analysis *model.AnalysisReport) []model.MultimodalNoteV2 {
	if !workloadIsMultimodal(analysis) {
		return nil
	}
	notes := []model.MultimodalNoteV2{}
	host := pressureHostInput(analysis, pressureCompute(analysis), pressureMemory(analysis))
	if host.PressureStatus == model.PressureStatusHigh || host.PressureStatus == model.PressureStatusModerate {
		notes = append(notes, model.MultimodalNoteV2{
			Summary:    "Host-side image or media preprocessing may be limiting end-to-end performance before GPU serving saturates.",
			Confidence: host.Confidence,
			Recommendations: []string{
				"Switch to a faster image processor path if available for this model family.",
				"Move image normalization and resize upstream or onto GPU-backed preprocessing when supported.",
				"Introduce image preprocessing cache keyed by stable asset ID; if stable asset IDs are unavailable, use content hash for cache reuse.",
			},
			Evidence: host.Evidence,
		})
	}
	return notes
}

func buildEvidence(analysis *model.AnalysisReport, recommendation *model.RecommendationReport, constraint *model.ConstraintV2) model.OptimizationEvidenceV2 {
	ev := model.OptimizationEvidenceV2{}
	if analysis != nil {
		ev.Findings = analysisFindings(analysis)
		ev.Warnings = append(ev.Warnings, analysis.Warnings...)
		ev.SystemInformation = &analysis.OSInformation
		ev.GPUInformation = &analysis.GPUInformation
		workload := buildWorkloadContext(analysis, recommendation, resolveObjectiveMode("", analysis, recommendation), constraint)
		ev.Workload = &workload
		ev.Configuration = cloneMap(analysis.CurrentVLLMConfigurations)
		ev.Profiling = analysis.AdvancedProfiling
		ev.CollectorLogs = cloneStringMap(analysis.MetricCollectionOutputs)
		ev.CollectedMetrics = append([]model.CollectedMetricPoint(nil), analysis.CollectedMetrics...)
	}
	if recommendation != nil {
		ev.Warnings = append(ev.Warnings, recommendation.Warnings...)
		ev.MatchedBenchmark = recommendation.MatchedCorpusProfile
	}
	return ev
}

func accessForTier(tier string) model.AccessV2 {
	if strings.TrimSpace(tier) == model.AccessTierPaid {
		return model.AccessV2{Tier: model.AccessTierPaid}
	}
	return model.AccessV2{
		Tier:       model.AccessTierFree,
		Redactions: []string{"scenario_projections", "exact_knob_deltas", "benchmark_details"},
	}
}

func redactForFree(report *model.OptimizationReportV2) {
	if report == nil {
		return
	}
	report.PrimaryDecision.ExactKnobDeltas = nil
	report.RecommendationBasis.MatchedBenchmark = nil
	report.Scenarios.RecommendedDecision.ProjectedOperatingPoint = nil
	report.Scenarios.RecommendedDecision.ExactKnobDeltas = nil
	report.Scenarios.ThroughputFirst.ProjectedOperatingPoint = nil
	report.Scenarios.ThroughputFirst.ExactKnobDeltas = nil
	report.Scenarios.LatencyFirst.ProjectedOperatingPoint = nil
	report.Scenarios.LatencyFirst.ExactKnobDeltas = nil
	report.Scenarios.Balanced.ProjectedOperatingPoint = nil
	report.Scenarios.Balanced.ExactKnobDeltas = nil
	report.Evidence.MatchedBenchmark = nil
}

func inferDecisionKind(changes []model.ParameterChange, mechanism string, pressure model.PressureSummaryV2, analysis *model.AnalysisReport, summary string) string {
	if strings.Contains(strings.ToLower(summary), "traffic") || shouldChangeTrafficShape(analysis, pressure) {
		return model.DecisionKindChangeTrafficShape
	}
	if pressure.DominantBottleneck == "host_input_pipeline" {
		return model.DecisionKindOptimizeInputPipeline
	}
	if pressure.DominantBottleneck == "compute" || pressure.DominantBottleneck == "memory_bandwidth" || pressure.DominantBottleneck == "kv_cache" {
		if len(changes) == 0 {
			return model.DecisionKindConsiderHardwareChange
		}
	}
	if len(changes) == 0 {
		return model.DecisionKindKeepCurrent
	}
	if mechanism == model.MechanismReduceHostPreprocessing {
		return model.DecisionKindOptimizeInputPipeline
	}
	return model.DecisionKindApplyConfigChange
}

func inferMechanismFromChanges(changes []model.ParameterChange, summary, objective string, analysis *model.AnalysisReport) string {
	if shouldChangeTrafficShape(analysis, buildPressureSummary(analysis)) {
		return model.MechanismSplitTrafficClasses
	}
	if containsKnob(changes, "mm_preprocessor_cache", "mm_processor_cache", "media_io_kwargs", "disable_mm_preprocessor_cache") {
		return model.MechanismImproveCacheReuse
	}
	if containsKnob(changes, "max_num_seqs", "max_num_batched_tokens", "tensor_parallel_size", "gpu_memory_utilization") {
		if strings.Contains(summary, "queue") || normalizedObjective(objective) == "latency_first" {
			return model.MechanismReduceQueueing
		}
		return model.MechanismIncreaseUsefulBatching
	}
	if findingPresent(analysis, "cpu_or_host_bottleneck") || findingPresent(analysis, "multimodal_preprocessing_cpu_bottleneck") {
		return model.MechanismReduceHostPreprocessing
	}
	return model.MechanismKeepCurrentOperatingMode
}

func allowExactKnobs(candidate *scenarioCandidate, pressure model.PressureSummaryV2) bool {
	if candidate == nil || len(candidate.Knobs) == 0 {
		return false
	}
	if candidate.Confidence < 0.75 {
		return false
	}
	if !scenarioShowsMaterialGain(candidate) {
		return false
	}
	if pressure.DominantBottleneck == "host_input_pipeline" || pressure.DominantBottleneck == "compute" && candidate.Mechanism == model.MechanismAddHardwareCapacity {
		return false
	}
	return true
}

func shouldChangeTrafficShape(analysis *model.AnalysisReport, pressure model.PressureSummaryV2) bool {
	if analysis == nil || analysis.ServiceSummary == nil {
		return false
	}
	mixed := analysis.ServiceSummary.ObservedMode.ServingPattern == model.ServingPatternMixed
	if !mixed {
		return false
	}
	return findingPresent(analysis, "prefill_heavy_workload") || findingPresent(analysis, "queue_dominated_ttft") || pressure.DominantBottleneck == "host_input_pipeline"
}

func scenarioShowsMaterialGain(candidate *scenarioCandidate) bool {
	if candidate == nil {
		return false
	}
	text := strings.ToLower(candidate.Upside)
	return strings.Contains(text, "%") || candidate.Projection != nil
}

func projectionFromEffect(effect model.PredictedEffect, current *model.ServiceSummary) *model.OperatingPointProjectionV2 {
	projection := &model.OperatingPointProjectionV2{
		ThroughputTokensPerSecond: nullablePositive(effect.ThroughputTokensPerSecond),
		P95LatencyMS:              nullablePositive(effect.LatencyP95Ms),
		P50LatencyMS:              nullablePositive(effect.LatencyP50Ms),
		TTFTMS:                    nullablePositive(effect.TTFTMs),
	}
	if current != nil && current.RequestRateRPS != nil && effect.ThroughputDeltaPct != 0 {
		projection.RequestRateRPS = floatPtr(*current.RequestRateRPS * (1 + (effect.ThroughputDeltaPct / 100)))
	}
	if projection.ThroughputTokensPerSecond == nil && projection.RequestRateRPS == nil && projection.P95LatencyMS == nil && projection.P50LatencyMS == nil && projection.TTFTMS == nil {
		return nil
	}
	return projection
}

func summarizePredictedEffect(effect model.PredictedEffect) string {
	parts := []string{}
	if effect.ThroughputDeltaPct != 0 {
		parts = append(parts, fmt.Sprintf("%+.1f%% throughput", effect.ThroughputDeltaPct))
	}
	if effect.TTFTDeltaPct != 0 {
		parts = append(parts, fmt.Sprintf("%+.1f%% TTFT", effect.TTFTDeltaPct))
	}
	if effect.LatencyP95DeltaPct != 0 {
		parts = append(parts, fmt.Sprintf("%+.1f%% p95 latency", effect.LatencyP95DeltaPct))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func knobDeltas(changes []model.ParameterChange) []model.KnobDeltaV2 {
	if len(changes) == 0 {
		return nil
	}
	out := make([]model.KnobDeltaV2, 0, len(changes))
	for _, change := range changes {
		out = append(out, model.KnobDeltaV2{
			Name:             change.Name,
			CurrentValue:     change.CurrentValue,
			RecommendedValue: change.RecommendedValue,
		})
	}
	return out
}

func analysisFindings(analysis *model.AnalysisReport) []model.Finding {
	if analysis == nil || analysis.AnalysisSummary == nil {
		return nil
	}
	return append([]model.Finding(nil), analysis.AnalysisSummary.Findings...)
}

func hostCPUValue(analysis *model.AnalysisReport) float64 {
	if analysis == nil {
		return 0
	}
	if analysis.CurrentLoadSummary != nil && analysis.CurrentLoadSummary.CPULoadPct > 0 {
		return analysis.CurrentLoadSummary.CPULoadPct
	}
	if analysis.FeatureSummary != nil && analysis.FeatureSummary.AverageCPUUtilizationPct > 0 {
		return analysis.FeatureSummary.AverageCPUUtilizationPct
	}
	if analysis.OSInformation.AverageCPUUtilizationPct > 0 {
		return analysis.OSInformation.AverageCPUUtilizationPct
	}
	return 0
}

func computeLoadValue(analysis *model.AnalysisReport) (float64, string, []model.EvidenceItem) {
	if analysis != nil && analysis.CurrentLoadSummary != nil && analysis.CurrentLoadSummary.ComputeLoadPct > 0 {
		return analysis.CurrentLoadSummary.ComputeLoadPct, firstNonEmpty(analysis.CurrentLoadSummary.ComputeLoadSource, model.PressureSourceMeasured), []model.EvidenceItem{
			{Metric: "compute_load_pct", Value: analysis.CurrentLoadSummary.ComputeLoadPct},
		}
	}
	if analysis != nil && analysis.FeatureSummary != nil && analysis.FeatureSummary.AvgGPUComputeLoadPct > 0 {
		return analysis.FeatureSummary.AvgGPUComputeLoadPct, firstNonEmpty(analysis.FeatureSummary.ComputeLoadSource, model.PressureSourceMeasured), []model.EvidenceItem{
			{Metric: "avg_gpu_compute_load_pct", Value: analysis.FeatureSummary.AvgGPUComputeLoadPct},
		}
	}
	return 0, "", nil
}

func memoryLoadValue(analysis *model.AnalysisReport) (float64, bool, string, []model.EvidenceItem) {
	if analysis != nil && analysis.CurrentLoadSummary != nil && analysis.CurrentLoadSummary.MemoryBandwidthLoadAvailable && analysis.CurrentLoadSummary.MemoryBandwidthLoadPct > 0 {
		return analysis.CurrentLoadSummary.MemoryBandwidthLoadPct, true, firstNonEmpty(analysis.CurrentLoadSummary.MemoryBandwidthLoadSource, model.PressureSourceMeasured), []model.EvidenceItem{
			{Metric: "memory_bandwidth_load_pct", Value: analysis.CurrentLoadSummary.MemoryBandwidthLoadPct},
		}
	}
	if analysis != nil && analysis.FeatureSummary != nil && analysis.FeatureSummary.MemoryBandwidthLoadAvailable && analysis.FeatureSummary.AvgGPUMemoryBandwidthLoadPct > 0 {
		return analysis.FeatureSummary.AvgGPUMemoryBandwidthLoadPct, true, firstNonEmpty(analysis.FeatureSummary.MemoryBandwidthLoadSource, model.PressureSourceMeasured), []model.EvidenceItem{
			{Metric: "avg_gpu_memory_bandwidth_load_pct", Value: analysis.FeatureSummary.AvgGPUMemoryBandwidthLoadPct},
		}
	}
	return 0, false, "", nil
}

func workloadIsMultimodal(analysis *model.AnalysisReport) bool {
	if analysis == nil {
		return false
	}
	if analysis.FeatureSummary != nil && analysis.FeatureSummary.MultimodalLikely {
		return true
	}
	return multimodalConfigPresent(analysis)
}

func multimodalConfigPresent(analysis *model.AnalysisReport) bool {
	if analysis == nil {
		return false
	}
	flat := cloneMap(analysis.CurrentVLLMConfigurations)
	if len(flat) == 0 {
		return false
	}
	for key := range flat {
		if strings.Contains(key, "mm_") || strings.Contains(key, "multimodal") || strings.Contains(key, "media_io") {
			return true
		}
	}
	return false
}

func findingPresent(analysis *model.AnalysisReport, id string) bool {
	if analysis == nil || analysis.AnalysisSummary == nil {
		return false
	}
	for _, finding := range analysis.AnalysisSummary.Findings {
		if finding.ID == id && finding.Status == model.FindingStatusPresent {
			return true
		}
	}
	return false
}

func statusFromThreshold(value float64, moderate, high float64) string {
	switch {
	case value >= high:
		return model.PressureStatusHigh
	case value >= moderate:
		return model.PressureStatusModerate
	case value > 0:
		return model.PressureStatusLow
	default:
		return model.PressureStatusInsufficientEvidence
	}
}

func summaryForPressure(label, status string, value float64, source string) string {
	switch status {
	case model.PressureStatusHigh:
		return fmt.Sprintf("%s pressure is high at roughly %.0f%% of observed load.", strings.Title(label), value)
	case model.PressureStatusModerate:
		return fmt.Sprintf("%s pressure is moderate at roughly %.0f%% of observed load.", strings.Title(label), value)
	case model.PressureStatusLow:
		return fmt.Sprintf("%s pressure is low at roughly %.0f%% of observed load.", strings.Title(label), value)
	default:
		if source != "" {
			return fmt.Sprintf("%s pressure is not conclusive from the current %s signal.", strings.Title(label), source)
		}
		return fmt.Sprintf("%s pressure is not conclusive from the current evidence.", strings.Title(label))
	}
}

func kvSummary(status string, preemptions, avg, max float64) string {
	switch status {
	case model.PressureStatusHigh:
		if preemptions > 0 {
			return "KV/cache pressure is high, with preemptions or very high occupancy likely harming tail latency."
		}
		return "KV/cache pressure is high, with occupancy close to practical limits."
	case model.PressureStatusModerate:
		return "KV/cache pressure is moderate and worth watching if concurrency increases further."
	case model.PressureStatusLow:
		return "KV/cache pressure is low for the current traffic window."
	default:
		return fmt.Sprintf("KV/cache pressure is inconclusive; avg %.0f%%, max %.0f%%.", avg, max)
	}
}

func queueSummary(status string, avgDelay, pressureRatio float64) string {
	switch status {
	case model.PressureStatusHigh:
		return fmt.Sprintf("Queue delay is a dominant contributor to latency (avg %.0f ms, pressure ratio %.1f).", avgDelay, pressureRatio)
	case model.PressureStatusModerate:
		return fmt.Sprintf("Queueing is present and starting to influence latency (avg %.0f ms).", avgDelay)
	case model.PressureStatusLow:
		return "Queue delay is not the main limiter in the current window."
	default:
		return "Queue pressure is inconclusive from the available latency samples."
	}
}

func hostSummary(status string, multimodal, cacheDisabled bool) string {
	switch status {
	case model.PressureStatusHigh:
		if multimodal {
			return "Host/input pipeline pressure is high; multimodal preprocessing likely dominates before GPU serving saturates."
		}
		return "Host/input pipeline pressure is high relative to current GPU saturation."
	case model.PressureStatusModerate:
		if cacheDisabled {
			return "Host/input pipeline pressure is moderate and repeated preprocessing work may be avoidable."
		}
		return "Host/input pipeline pressure is moderate and worth resolving before aggressive serving-side tuning."
	default:
		return "Host/input pipeline pressure is not clearly dominant in the current window."
	}
}

func sourceType(source string) string {
	switch {
	case source == "":
		return model.PressureSourceInferred
	case strings.Contains(source, "proxy") || strings.Contains(source, "approx"):
		return model.PressureSourceMixed
	default:
		return model.PressureSourceMeasured
	}
}

func hostSourceType(multimodal bool, cpu float64) string {
	switch {
	case multimodal && cpu > 0:
		return model.PressureSourceMixed
	case cpu > 0:
		return model.PressureSourceMeasured
	default:
		return model.PressureSourceInferred
	}
}

func confidenceFromSource(source string, value float64) float64 {
	if source == "" {
		return 0.35
	}
	conf := 0.55 + normalizePercent(value)*0.30
	if strings.Contains(source, "proxy") || strings.Contains(source, "approx") {
		conf -= 0.10
	}
	return clampFloat(conf, 0.35, 0.92)
}

func normalizePercent(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return clampFloat(value/100, 0, 1)
}

func normalizedObjective(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "throughput", "throughput_first":
		return "throughput_first"
	case "latency", "latency_first":
		return "latency_first"
	case "balanced":
		return "balanced"
	default:
		return ""
	}
}

func fallbackEvidenceState(analysis *model.AnalysisReport) string {
	if analysis == nil || analysis.AnalysisSummary == nil || !analysis.AnalysisSummary.DataQuality.TrafficObserved {
		return model.ScenarioEvidenceInsufficientEvidence
	}
	return model.ScenarioEvidencePreview
}

func confidenceToSource(source string) string {
	switch source {
	case model.ConfidenceSourceBenchmarkCalib:
		return model.PressureSourceMeasured
	case model.ConfidenceSourceHybrid:
		return model.PressureSourceMixed
	default:
		return model.PressureSourceInferred
	}
}

func nullablePositive(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return floatPtr(value)
}

func nullablePct(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return floatPtr(value)
}

func pctFromUnitValue(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	if value <= 1 {
		return floatPtr(value * 100)
	}
	return floatPtr(value)
}

func secondsToMS(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return floatPtr(value * 1000)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func chooseFloat(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil && *value > 0 {
			return value
		}
	}
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func floatPtr(value float64) *float64 {
	return &value
}

func floatValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func cloneMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneBasis(basis *model.RecommendationBasisV2) *model.RecommendationBasisV2 {
	if basis == nil {
		return nil
	}
	copy := *basis
	if basis.ValidationChecks != nil {
		copy.ValidationChecks = append([]string(nil), basis.ValidationChecks...)
	}
	if basis.Warnings != nil {
		copy.Warnings = append([]string(nil), basis.Warnings...)
	}
	return &copy
}

func insufficientPressure(summary string) model.PressureDimensionV2 {
	return model.PressureDimensionV2{
		PressureStatus: model.PressureStatusInsufficientEvidence,
		Confidence:     0.30,
		SourceType:     model.PressureSourceInferred,
		Summary:        summary,
	}
}

func containsKnob(changes []model.ParameterChange, names ...string) bool {
	nameSet := map[string]struct{}{}
	for _, name := range names {
		nameSet[name] = struct{}{}
	}
	for _, change := range changes {
		if _, ok := nameSet[change.Name]; ok {
			return true
		}
	}
	return false
}

func summaryBasis(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstString(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
