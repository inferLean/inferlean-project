package analyzer

import (
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

// NormalizeReport backfills analyzer-derived sections for reports that only
// contain collected telemetry, keeping downstream consumers compatible with
// older and newer CLI output shapes.
func NormalizeReport(report *model.AnalysisReport, fallbackIntent WorkloadIntent) *model.AnalysisReport {
	if report == nil {
		return nil
	}
	if report.CollectedMetrics == nil {
		report.CollectedMetrics = []model.CollectedMetricPoint{}
	}
	if report.CurrentVLLMConfigurations == nil {
		report.CurrentVLLMConfigurations = map[string]any{}
	}
	if report.MetricCollectionOutputs == nil {
		report.MetricCollectionOutputs = map[string]string{}
	}

	intent := resolveSummaryIntent(report.WorkloadProfile, fallbackIntent)
	if report.AnalysisSummary == nil {
		report.AnalysisSummary = SummarizeReport(report, intent)
	}
	if report.FeatureSummary == nil {
		report.FeatureSummary = SummarizeFeatures(report)
	}
	features := ExtractFeatures(report)
	if report.ObservedWorkloadProfile == nil && report.AnalysisSummary != nil {
		report.ObservedWorkloadProfile = inferObservedWorkloadProfile(report, features, report.AnalysisSummary.Findings)
	}
	if report.WorkloadProfileAlignment == nil {
		report.WorkloadProfileAlignment = buildWorkloadProfileAlignment(report.WorkloadProfile, report.ObservedWorkloadProfile)
	}
	if report.CurrentLoadSummary == nil {
		report.CurrentLoadSummary = buildCurrentLoadSummary(report, intent)
	}
	if report.ServiceSummary == nil {
		report.ServiceSummary = buildServiceSummary(report, intent)
	}
	report.Warnings = appendSaturationWarnings(report.Warnings, report, features)
	return report
}

func appendSaturationWarnings(existing []string, report *model.AnalysisReport, features FeatureSet) []string {
	if strings.TrimSpace(features.SaturationSource) != "gpu_utilization_proxy" {
		return existing
	}
	reason := "DCGM compute counters were not present in the collected telemetry."
	if detailed := saturationWarningReason(report); detailed != "" {
		reason = detailed
	}
	return appendUniqueWarning(
		existing,
		"Real GPU saturation metrics unavailable: "+reason+" InferLean is showing GPU utilization as a proxy instead of measured compute saturation.",
	)
}

func saturationWarningReason(report *model.AnalysisReport) string {
	if report == nil {
		return ""
	}
	outputs := report.MetricCollectionOutputs
	switch {
	case strings.TrimSpace(outputs["dcgm_profiler_warning"]) != "":
		return strings.TrimSpace(outputs["dcgm_profiler_warning"])
	case strings.TrimSpace(outputs["dcgm_exporter_warning"]) != "":
		return strings.TrimSpace(outputs["dcgm_exporter_warning"])
	case strings.TrimSpace(outputs["dcgm_exporter_start"]) == "skipped":
		return "DCGM exporter was unavailable during collection."
	case strings.TrimSpace(outputs["dcgm_profiler_metrics_available"]) == "false":
		missing := strings.TrimSpace(outputs["dcgm_profiler_metrics_missing"])
		if missing != "" {
			return "DCGM exporter was reachable but did not expose profiler counters: " + missing + "."
		}
		return "DCGM exporter was reachable but did not expose the requested profiler counters."
	default:
		return ""
	}
}

func appendUniqueWarning(existing []string, warning string) []string {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return existing
	}
	for _, item := range existing {
		if strings.TrimSpace(item) == warning {
			return existing
		}
	}
	return append(existing, warning)
}
