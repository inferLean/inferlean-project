package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

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
	return report
}
