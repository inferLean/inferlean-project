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
	report.Warnings = appendSaturationWarnings(report.Warnings, features)
	return report
}

func appendSaturationWarnings(existing []string, features FeatureSet) []string {
	if strings.TrimSpace(features.SaturationSource) != "gpu_utilization_proxy" {
		return existing
	}
	return appendUniqueWarning(
		existing,
		"Real GPU saturation metrics unavailable: missing DCGM compute counters (DCGM_FI_PROF_SM_ACTIVE / DCGM_FI_PROF_GR_ENGINE_ACTIVE), so InferLean is showing GPU utilization as a proxy instead of measured compute saturation.",
	)
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
