package analyzer

import (
	"sort"

	"github.com/inferLean/inferlean-project/internal/model"
)

var detectorImprovementCaps = map[string]float64{
	detectorQueueDominatedTTFT:                    22,
	detectorThroughputSaturationWithQueuePressure: 18,
	detectorUnderutilizedGPUOrConservativeBatch:   35,
	detectorKVCachePressurePreemptions:            24,
	detectorPrefixCacheIneffective:                14,
	detectorPromptRecomputationThrashing:          16,
	detectorPrefillHeavyWorkload:                  8,
	detectorDecodeBoundGeneration:                 7,
	detectorCPUOrHostBottleneck:                   26,
	detectorGPUMemorySaturation:                   18,
	detectorGPUHardwareInstability:                6,
}

var detectorImportanceBases = map[string]float64{
	detectorGPUHardwareInstability:                100,
	detectorKVCachePressurePreemptions:            92,
	detectorCPUOrHostBottleneck:                   90,
	detectorThroughputSaturationWithQueuePressure: 88,
	detectorQueueDominatedTTFT:                    84,
	detectorUnderutilizedGPUOrConservativeBatch:   82,
	detectorGPUMemorySaturation:                   76,
	detectorPromptRecomputationThrashing:          72,
	detectorPrefixCacheIneffective:                65,
	detectorPrefillHeavyWorkload:                  52,
	detectorDecodeBoundGeneration:                 48,
}

func prioritizeFindings(findings []model.Finding) ([]model.Finding, float64) {
	if len(findings) == 0 {
		return findings, 0
	}
	out := make([]model.Finding, 0, len(findings))
	for _, finding := range findings {
		finding.HeuristicImprovementPct = heuristicImprovementPct(finding)
		finding.ImportanceScore = importanceScore(finding)
		out = append(out, finding)
	}

	sort.SliceStable(out, func(i, j int) bool {
		left := out[i]
		right := out[j]
		if statusSortRank(left.Status) != statusSortRank(right.Status) {
			return statusSortRank(left.Status) < statusSortRank(right.Status)
		}
		if left.ImportanceScore != right.ImportanceScore {
			return left.ImportanceScore > right.ImportanceScore
		}
		if left.HeuristicImprovementPct != right.HeuristicImprovementPct {
			return left.HeuristicImprovementPct > right.HeuristicImprovementPct
		}
		return left.ID < right.ID
	})

	for i := range out {
		out[i].Rank = i + 1
	}

	return out, combinedImprovementPct(out)
}

func heuristicImprovementPct(finding model.Finding) float64 {
	if finding.Status != model.FindingStatusPresent {
		return 0
	}
	base := detectorImprovementCaps[finding.ID]
	if base <= 0 {
		return 0
	}
	return clampFloat(base*severityWeight(finding.Severity)*(0.55+0.45*clampFloat(finding.Confidence, 0, 1)), 0, 100)
}

func importanceScore(finding model.Finding) float64 {
	base := detectorImportanceBases[finding.ID]
	if base <= 0 {
		base = 40
	}
	switch finding.Status {
	case model.FindingStatusPresent:
		return clampFloat(base*severityWeight(finding.Severity)*(0.6+0.4*clampFloat(finding.Confidence, 0, 1)), 0, 100)
	case model.FindingStatusInsufficientData:
		return clampFloat(base*0.18*(0.4+0.6*clampFloat(finding.Confidence, 0, 1)), 0, 100)
	default:
		return 0
	}
}

func combinedImprovementPct(findings []model.Finding) float64 {
	remaining := 1.0
	for _, finding := range findings {
		if finding.Status != model.FindingStatusPresent || finding.HeuristicImprovementPct <= 0 {
			continue
		}
		remaining *= 1 - clampFloat(finding.HeuristicImprovementPct/100, 0, 0.75)
	}
	return clampFloat((1-remaining)*100, 0, 100)
}

func statusSortRank(status string) int {
	switch status {
	case model.FindingStatusPresent:
		return 0
	case model.FindingStatusInsufficientData:
		return 1
	default:
		return 2
	}
}

func severityWeight(severity string) float64 {
	switch severity {
	case model.SeverityCritical:
		return 1
	case model.SeverityHigh:
		return 0.8
	case model.SeverityMedium:
		return 0.6
	case model.SeverityLow:
		return 0.35
	default:
		return 0
	}
}
