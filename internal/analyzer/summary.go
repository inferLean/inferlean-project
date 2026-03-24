package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func SummarizeReport(report *model.AnalysisReport, intent WorkloadIntent) *model.AnalysisSummary {
	if report == nil {
		return nil
	}

	normalizedIntent := normalizeWorkloadIntent(intent)
	features := ExtractFeatures(report)
	findings, totalImprovementPct := prioritizeFindings(RunDetectors(features))

	return &model.AnalysisSummary{
		WorkloadIntent: string(normalizedIntent),
		DataQuality: model.DataQualitySummary{
			SnapshotCount:        features.SnapshotCount,
			IntervalSeconds:      features.IntervalSeconds,
			TrafficObserved:      features.TrafficObserved,
			EnoughLatencySamples: features.EnoughLatencySamples,
			EnoughKVCacheSamples: features.EnoughKVCacheSamples,
		},
		TotalHeuristicImprovementPct: totalImprovementPct,
		Findings:                     findings,
	}
}
