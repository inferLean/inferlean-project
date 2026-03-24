package analyzer

import (
	"fmt"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

const observedConfidenceThreshold = 0.60

func inferObservedWorkloadProfile(report *model.AnalysisReport, features FeatureSet, findings []model.Finding) *model.ObservedWorkloadProfile {
	observed := &model.ObservedWorkloadProfile{
		ServingPattern: model.ServingPatternUnknown,
		TaskPattern:    model.TaskPatternUnknown,
		Objective:      model.WorkloadObjectiveUnknown,
		PrefixReuse:    model.WorkloadProfileReuseUnknown,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}

	servingPattern, servingConfidence, servingNote := inferObservedServingPattern(features)
	observed.ServingPattern = servingPattern
	observed.Confidence.ServingPattern = servingConfidence
	if servingNote != "" {
		observed.Notes = append(observed.Notes, servingNote)
	}

	// Current metrics do not expose prompt/task diversity directly.
	observed.Notes = append(observed.Notes, "task_pattern remains unknown because current metrics do not capture prompt or task diversity.")

	objective, objectiveConfidence, objectiveNote := inferObservedObjective(features, findings, servingPattern, servingConfidence)
	observed.Objective = objective
	observed.Confidence.Objective = objectiveConfidence
	if objectiveNote != "" {
		observed.Notes = append(observed.Notes, objectiveNote)
	}

	prefixReuse, prefixConfidence, prefixNote := inferObservedPrefixReuse(features)
	observed.PrefixReuse = prefixReuse
	observed.Confidence.PrefixReuse = prefixConfidence
	if prefixNote != "" {
		observed.Notes = append(observed.Notes, prefixNote)
	}

	mediaReuse, mediaConfidence, mediaNote := inferObservedMediaReuse(report, features)
	observed.MediaReuse = mediaReuse
	observed.Confidence.MediaReuse = mediaConfidence
	if mediaNote != "" {
		observed.Notes = append(observed.Notes, mediaNote)
	}

	return observed
}

func inferObservedServingPattern(features FeatureSet) (string, float64, string) {
	if !features.TrafficObserved {
		return model.ServingPatternUnknown, 0, "serving_pattern remains unknown because no live traffic was observed."
	}
	if features.AvgRequestsRunning > 0 && features.AvgRequestsRunning <= 2 && features.MaxRequestsWaiting <= 1 && features.AvgQueueTimeSeconds <= 0.25 {
		return model.ServingPatternRealtimeChat, 0.82, "Observed low concurrency with little queueing, which is more consistent with interactive realtime traffic."
	}
	if features.AvgRequestsRunning >= 4 && features.MaxRequestsWaiting <= 2 {
		return model.ServingPatternOfflineBatch, 0.82, "Observed sustained higher in-flight concurrency with little queueing, which is more consistent with batch-style processing."
	}
	if features.AvgRequestsRunning >= 2 && features.MaxRequestsWaiting >= 2 {
		return model.ServingPatternMixed, 0.80, "Observed concurrent execution mixed with queueing pressure, which suggests a mixed serving pattern."
	}
	return model.ServingPatternUnknown, 0, "serving_pattern remains unknown because current traffic signals are not distinctive enough."
}

func inferObservedObjective(features FeatureSet, findings []model.Finding, servingPattern string, servingConfidence float64) (string, float64, string) {
	if !features.TrafficObserved {
		return model.WorkloadObjectiveUnknown, 0, "objective remains unknown because no live traffic was observed."
	}
	if features.RequestLatencyCountDelta > 0 && features.AvgRequestLatencySeconds > 5 {
		if servingPattern == model.ServingPatternOfflineBatch {
			return string(ThroughputFirstIntent), maxFloat(servingConfidence, 0.82), "Average end-to-end request latency exceeded five seconds, so latency-first is not a credible observed objective for this traffic."
		}
		if servingPattern == model.ServingPatternMixed {
			return string(BalancedIntent), 0.80, "Average end-to-end request latency exceeded five seconds, so balanced is the safest observed objective for this mixed traffic."
		}
		return string(BalancedIntent), maxFloat(servingConfidence, 0.80), "Average end-to-end request latency exceeded five seconds, so latency-first is not a credible observed objective for this traffic."
	}
	if servingPattern == model.ServingPatternRealtimeChat {
		confidence := servingConfidence
		if findingPresent(findings, detectorQueueDominatedTTFT) {
			confidence = 0.84
		} else if confidence > 0 {
			confidence = 0.82
		}
		return string(LatencyFirstIntent), confidence, "Observed traffic shape favors latency sensitivity over throughput saturation."
	}
	if servingPattern == model.ServingPatternOfflineBatch {
		return string(ThroughputFirstIntent), maxFloat(servingConfidence, 0.82), "Observed traffic shape favors throughput-oriented batch processing."
	}
	if servingPattern == model.ServingPatternMixed {
		return string(BalancedIntent), 0.80, "Observed traffic looks mixed, so balanced is the safest inferred objective."
	}
	return model.WorkloadObjectiveUnknown, 0, "objective remains unknown because serving intent could not be inferred confidently."
}

func inferObservedPrefixReuse(features FeatureSet) (string, float64, string) {
	queries := features.PrefixCacheQueriesDelta
	hits := features.PrefixCacheHitsDelta
	if queries < 10 {
		return model.WorkloadProfileReuseUnknown, 0, "prefix_reuse remains unknown because there were too few prefix-cache queries in the observed window."
	}
	hitRate := safeHitRate(hits, queries)
	if hitRate >= 0.40 {
		return model.WorkloadProfileReuseHigh, clampObservedConfidence(0.65 + hitRate*0.30), fmt.Sprintf("Observed prefix-cache hit rate was %.0f%% across %.0f queries.", hitRate*100, queries)
	}
	if hitRate <= 0.10 {
		return model.WorkloadProfileReuseLow, 0.86, fmt.Sprintf("Observed prefix-cache hit rate was only %.0f%% across %.0f queries.", hitRate*100, queries)
	}
	return model.WorkloadProfileReuseUnknown, 0.45, fmt.Sprintf("Observed prefix-cache hit rate was %.0f%% across %.0f queries, which is not decisive enough.", hitRate*100, queries)
}

func inferObservedMediaReuse(report *model.AnalysisReport, features FeatureSet) (string, float64, string) {
	queries := features.MMCacheQueriesDelta
	hits := features.MMCacheHitsDelta
	if queries < 10 {
		if looksMultimodal(report) {
			return model.WorkloadProfileReuseUnknown, 0, "media_reuse remains unknown because there were too few multimodal cache queries in the observed window."
		}
		return model.WorkloadProfileReuseUnknown, 0, ""
	}
	hitRate := safeHitRate(hits, queries)
	if hitRate >= 0.35 {
		return model.WorkloadProfileReuseHigh, clampObservedConfidence(0.60 + hitRate*0.25), fmt.Sprintf("Observed multimodal cache hit rate was %.0f%% across %.0f queries.", hitRate*100, queries)
	}
	if hitRate <= 0.05 && looksMultimodal(report) {
		return model.WorkloadProfileReuseUnknown, 0.40, fmt.Sprintf("Observed multimodal cache hit rate was only %.0f%% across %.0f queries, which may reflect either low media reuse or ineffective caching.", hitRate*100, queries)
	}
	return model.WorkloadProfileReuseUnknown, 0.35, fmt.Sprintf("Observed multimodal cache hit rate was %.0f%% across %.0f queries, which is not decisive enough.", hitRate*100, queries)
}

func buildWorkloadProfileAlignment(declared *model.WorkloadProfile, observed *model.ObservedWorkloadProfile) *model.WorkloadProfileAlignment {
	if declared == nil || declared.Source != model.WorkloadProfileSourceUserInput || observed == nil {
		return nil
	}
	fields := []model.WorkloadProfileAlignmentField{
		alignField("serving_pattern", declared.ServingPattern, observed.ServingPattern, observed.Confidence.ServingPattern),
		alignField("task_pattern", declared.TaskPattern, observed.TaskPattern, observed.Confidence.TaskPattern),
		alignField("objective", declared.Objective, observed.Objective, observed.Confidence.Objective),
		alignField("prefix_reuse", declared.PrefixReuse, observed.PrefixReuse, observed.Confidence.PrefixReuse),
		alignField("media_reuse", declared.MediaReuse, observed.MediaReuse, observed.Confidence.MediaReuse),
	}
	out := &model.WorkloadProfileAlignment{}
	for _, field := range fields {
		out.Fields = append(out.Fields, field)
	}
	return out
}

func alignField(name, declared, observed string, confidence float64) model.WorkloadProfileAlignmentField {
	field := model.WorkloadProfileAlignmentField{
		Field:      name,
		Declared:   declared,
		Observed:   observed,
		Confidence: confidence,
	}
	switch {
	case declared == "" || declared == model.ServingPatternUnknown || declared == model.TaskPatternUnknown || declared == model.WorkloadProfileReuseUnknown:
		field.Status = "not_declared"
		field.Note = "No explicit declared value was provided for this field."
	case observed == "" || observed == model.ServingPatternUnknown || observed == model.TaskPatternUnknown || observed == model.WorkloadProfileReuseUnknown || observed == model.WorkloadObjectiveUnknown || confidence < observedConfidenceThreshold:
		field.Status = "insufficient_evidence"
		field.Note = "Observed data was not strong enough to confirm or contradict the declared value."
	case declared == observed:
		field.Status = "match"
		field.Note = "Declared and observed values align."
	default:
		field.Status = "different"
		field.Note = "Declared value differs from the observed workload shape."
	}
	return field
}

func safeHitRate(hits, queries float64) float64 {
	if queries <= 0 {
		return 0
	}
	rate := hits / queries
	if rate < 0 {
		return 0
	}
	if rate > 1 {
		return 1
	}
	return rate
}

func findingPresent(findings []model.Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id && finding.Status == model.FindingStatusPresent {
			return true
		}
	}
	return false
}

func looksMultimodal(report *model.AnalysisReport) bool {
	if report == nil {
		return false
	}
	name := ""
	if report.CurrentVLLMConfigurations != nil {
		if raw, ok := report.CurrentVLLMConfigurations["model_name"]; ok {
			name = fmt.Sprint(raw)
		}
	}
	return containsAnyFold(name, "vl", "vision", "llava", "pixtral", "internvl")
}

func containsAnyFold(value string, substrings ...string) bool {
	for _, substring := range substrings {
		if substring != "" && strings.Contains(strings.ToLower(value), strings.ToLower(substring)) {
			return true
		}
	}
	return false
}

func clampObservedConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 0.98 {
		return 0.98
	}
	return value
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
