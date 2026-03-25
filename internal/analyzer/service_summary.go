package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

const serviceSummaryCLIConfidenceThreshold = 0.80

func buildServiceSummary(report *model.AnalysisReport, intent WorkloadIntent) *model.ServiceSummary {
	if report == nil {
		return nil
	}
	features := ExtractFeatures(report)
	if report.AnalysisSummary != nil && strings.TrimSpace(report.AnalysisSummary.WorkloadIntent) != "" {
		intent = WorkloadIntent(report.AnalysisSummary.WorkloadIntent)
	}

	summary := &model.ServiceSummary{
		RequestLatencyMS: model.RequestLatencySummary{},
		Queue:            model.QueueSummary{Health: queueHealth(features, intent)},
		Bottleneck:       model.BottleneckSummary{Kind: "unclear"},
		ObservedMode:     model.ObservedModeSummary{Objective: model.WorkloadObjectiveUnknown, ServingPattern: "unknown"},
		ConfiguredIntent: configuredIntentSummary(report.CurrentVLLMConfigurations),
		TopIssue:         topIssueHeadline(report),
	}

	if features.IntervalSeconds > 0 {
		requestRateRPS := features.RequestSuccessDelta / features.IntervalSeconds
		summary.RequestRateRPS = &requestRateRPS
	}
	if features.RequestLatencyCountDelta > 0 && features.AvgRequestLatencySeconds > 0 {
		avgMS := features.AvgRequestLatencySeconds * 1000
		summary.RequestLatencyMS.Avg = &avgMS
	}
	if p50MS, p90MS, p99MS, available := requestLatencyPercentilesMS(report); available {
		summary.RequestLatencyMS.P50 = p50MS
		summary.RequestLatencyMS.P90 = p90MS
		summary.RequestLatencyMS.P99 = p99MS
		summary.RequestLatencyMS.PercentilesAvailable = true
	}

	queueDelayMS := features.AvgQueueTimeSeconds * 1000
	summary.Queue.AvgDelayMS = floatPtr(queueDelayMS)
	summary.Queue.AvgWaitingRequests = floatPtr(features.AvgRequestsWaiting)
	summary.Queue.MaxWaitingRequests = floatPtr(features.MaxRequestsWaiting)
	targetWaiting := targetQueueSizeForIntent(intent)
	summary.Queue.TargetWaitingRequests = floatPtr(targetWaiting)
	if targetWaiting > 0 {
		pressureRatio := features.AvgRequestsWaiting / targetWaiting
		summary.Queue.PressureRatio = floatPtr(pressureRatio)
	}

	if report.CurrentLoadSummary != nil {
		summary.SaturationPct = floatPtr(clampFloat(report.CurrentLoadSummary.CurrentSaturationPct, 0, 100))
		summary.Bottleneck = serviceBottleneck(report.CurrentLoadSummary)
	} else {
		saturationPct := clampFloat(maxFloatN(
			features.AvgGPUComputeLoadPct,
			features.AvgGPUMemoryBandwidthLoadPct,
			features.AvgGPUTensorLoadPct,
		), 0, 100)
		summary.SaturationPct = floatPtr(saturationPct)
	}

	if observed := observedModeSummary(report, features); observed != nil {
		summary.ObservedMode = *observed
	}
	if summary.RequestRateRPS != nil && summary.SaturationPct != nil && *summary.SaturationPct >= 5 && canEstimateCapacityHeadroom(report) {
		estimatedUpper := *summary.RequestRateRPS * (100 / *summary.SaturationPct)
		summary.EstimatedUpperRequestRateRPS = floatPtr(estimatedUpper)
	}

	return summary
}

func canEstimateCapacityHeadroom(report *model.AnalysisReport) bool {
	if report == nil || report.CurrentLoadSummary == nil {
		return true
	}
	return strings.TrimSpace(report.CurrentLoadSummary.SaturationSource) == saturationSourceMeasured
}

func serviceBottleneck(load *model.CurrentLoadSummary) model.BottleneckSummary {
	if load == nil {
		return model.BottleneckSummary{Kind: "unclear"}
	}
	return model.BottleneckSummary{
		Kind:       normalizeBottleneckKind(load.CurrentLoadBottleneck),
		Confidence: clampFloat(load.CurrentLoadBottleneckConfidence, 0, 1),
	}
}

func normalizeBottleneckKind(value string) string {
	switch strings.TrimSpace(value) {
	case currentLoadBottleneckCPU:
		return "cpu"
	case currentLoadBottleneckGPUCompute:
		return "gpu_compute"
	case currentLoadBottleneckGPUMemory:
		return "gpu_bandwidth"
	case currentLoadBottleneckMixed:
		return "mixed"
	default:
		return "unclear"
	}
}

func observedModeSummary(report *model.AnalysisReport, features FeatureSet) *model.ObservedModeSummary {
	if report == nil || report.ObservedWorkloadProfile == nil {
		return nil
	}
	objective := normalizeObservedObjective(report.ObservedWorkloadProfile.Objective)
	servingPattern := normalizeObservedServingPattern(report.ObservedWorkloadProfile.ServingPattern)
	confidence := report.ObservedWorkloadProfile.Confidence.Objective
	if report.ObservedWorkloadProfile.Confidence.ServingPattern > 0 && (confidence == 0 || report.ObservedWorkloadProfile.Confidence.ServingPattern < confidence) {
		confidence = report.ObservedWorkloadProfile.Confidence.ServingPattern
	}
	if features.RequestLatencyCountDelta > 0 && features.AvgRequestLatencySeconds > 5 && objective == string(LatencyFirstIntent) {
		objective = string(BalancedIntent)
		confidence = clampFloat(confidence, 0, 0.79)
	}
	return &model.ObservedModeSummary{
		Objective:      objective,
		ServingPattern: servingPattern,
		Confidence:     clampFloat(confidence, 0, 1),
	}
}

func normalizeObservedObjective(value string) string {
	switch strings.TrimSpace(value) {
	case string(LatencyFirstIntent):
		return string(LatencyFirstIntent)
	case string(ThroughputFirstIntent):
		return string(ThroughputFirstIntent)
	case string(BalancedIntent):
		return string(BalancedIntent)
	default:
		return model.WorkloadObjectiveUnknown
	}
}

func normalizeObservedServingPattern(value string) string {
	switch strings.TrimSpace(value) {
	case model.ServingPatternRealtimeChat:
		return "realtime"
	case model.ServingPatternOfflineBatch:
		return "batch"
	case model.ServingPatternMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

func configuredIntentSummary(config map[string]any) model.ConfiguredIntentSummary {
	numeric := collectNumericConfig(config)
	maxSeqs := lookupPositive(numeric, "max_num_seqs")
	maxTokens := lookupPositive(numeric, "max_num_batched_tokens")
	value := string(BalancedIntent)
	switch {
	case maxSeqs > 0 && maxSeqs <= 16 && maxTokens > 0 && maxTokens <= 2048:
		value = string(LatencyFirstIntent)
	case maxSeqs >= 128 || maxTokens >= 8192:
		value = string(ThroughputFirstIntent)
	}

	confidence := 0.0
	switch {
	case maxSeqs > 0 && maxTokens > 0:
		confidence = 0.92
	case maxSeqs > 0 || maxTokens > 0:
		confidence = 0.68
	default:
		value = model.WorkloadObjectiveUnknown
	}
	return model.ConfiguredIntentSummary{
		Value:      value,
		Confidence: confidence,
	}
}

func queueHealth(features FeatureSet, intent WorkloadIntent) string {
	avgDelayMS := features.AvgQueueTimeSeconds * 1000
	targetWaiting := targetQueueSizeForIntent(intent)
	pressureRatio := 0.0
	if targetWaiting > 0 {
		pressureRatio = features.AvgRequestsWaiting / targetWaiting
	}
	switch {
	case avgDelayMS > 1000 || pressureRatio > 3:
		return "severe"
	case avgDelayMS >= 100 || pressureRatio >= 1 || features.AvgRequestsWaiting >= 1:
		return "elevated"
	default:
		return "healthy"
	}
}

func topIssueHeadline(report *model.AnalysisReport) string {
	primary, ok := primaryFindingForSummary(report)
	if !ok {
		return "No clear bottleneck detected from current evidence."
	}
	if headline := findingHeadlineForSummary(primary.ID); headline != "" {
		return headline
	}
	if text := strings.TrimSpace(primary.Summary); text != "" {
		return text
	}
	return humanizeIdentifier(primary.ID)
}

func primaryFindingForSummary(report *model.AnalysisReport) (model.Finding, bool) {
	if report == nil || report.AnalysisSummary == nil || len(report.AnalysisSummary.Findings) == 0 {
		return model.Finding{}, false
	}
	findings := append([]model.Finding(nil), report.AnalysisSummary.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		ri := findings[i].Rank
		rj := findings[j].Rank
		if ri <= 0 {
			ri = 1_000_000 + i
		}
		if rj <= 0 {
			rj = 1_000_000 + j
		}
		return ri < rj
	})
	for _, finding := range findings {
		if finding.Status == model.FindingStatusPresent {
			return finding, true
		}
	}
	for _, finding := range findings {
		if finding.Status == model.FindingStatusInsufficientData {
			return finding, true
		}
	}
	return findings[0], true
}

func findingHeadlineForSummary(id string) string {
	switch id {
	case detectorQueueDominatedTTFT:
		return "Queue-heavy TTFT hurts responsiveness"
	case detectorThroughputSaturationWithQueuePressure:
		return "Queue pressure is limiting throughput"
	case detectorUnderutilizedGPUOrConservativeBatch:
		return "Conservative batching leaves GPU headroom unused"
	case detectorKVCachePressurePreemptions:
		return "KV cache preemptions increase tail latency"
	case detectorPrefixCacheIneffective:
		return "Low prefix-cache hit rate inflates prefill cost"
	case detectorPromptRecomputationThrashing:
		return "Prompt recomputation adds avoidable latency"
	case detectorPrefillHeavyWorkload:
		return "Prefill-heavy traffic dominates end-to-end latency"
	case detectorDecodeBoundGeneration:
		return "Decode path is the dominant generation bottleneck"
	case detectorCPUOrHostBottleneck:
		return "CPU or host constraints throttle GPU throughput"
	case detectorGPUMemorySaturation:
		return "GPU memory saturation caps throughput gains"
	case detectorGPUHardwareInstability:
		return "GPU hardware instability signals were detected"
	default:
		return ""
	}
}

func humanizeIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "Unknown"
	}
	trimmed = strings.ReplaceAll(trimmed, "_", " ")
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}

func floatPtr(value float64) *float64 {
	v := value
	return &v
}

func serviceSummaryQueueLine(summary *model.ServiceSummary) string {
	if summary == nil || summary.Queue.AvgDelayMS == nil || summary.Queue.AvgWaitingRequests == nil {
		return ""
	}
	if *summary.Queue.AvgDelayMS < 100 && *summary.Queue.AvgWaitingRequests < 1 {
		return ""
	}
	return fmt.Sprintf("%s: %.0f ms avg wait, %.1f waiting", strings.Title(summary.Queue.Health), *summary.Queue.AvgDelayMS, *summary.Queue.AvgWaitingRequests)
}
