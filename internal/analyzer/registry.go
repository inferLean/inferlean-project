package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func allDetectors() []Detector {
	return []Detector{
		newQueueDominatedTTFTDetector(),
		newThroughputSaturationWithQueuePressureDetector(),
		newUnderutilizedGPUOrConservativeBatchingDetector(),
		newKVCachePressurePreemptionsDetector(),
		newPrefixCacheIneffectiveDetector(),
		newPromptRecomputationThrashingDetector(),
		newPrefillHeavyWorkloadDetector(),
		newDecodeBoundGenerationDetector(),
		newCPUOrHostBottleneckDetector(),
		newMultimodalPreprocessingCPUBottleneckDetector(),
		newMultimodalCacheIneffectiveDetector(),
		newGPUMemorySaturationWithoutThroughputDetector(),
		newGPUHardwareInstabilityDetector(),
		newTextOnlyWorkloadOnMultimodalStackDetector(),
	}
}

func implementedDetectors() []Detector {
	all := allDetectors()
	out := make([]Detector, 0, len(all))
	for _, detector := range all {
		if detector.Spec().Implemented {
			out = append(out, detector)
		}
	}
	return out
}

func RunDetectors(features FeatureSet) []model.Finding {
	detectors := implementedDetectors()
	findings := make([]model.Finding, 0, len(detectors))
	for _, detector := range detectors {
		finding, err := detector.Evaluate(features)
		if err != nil {
			finding = insufficientFinding(
				detector.Spec(),
				"Detector evaluation failed before a stable diagnosis could be produced.",
				0,
				evidence("detector_error", 1, err.Error()),
			)
		}
		findings = append(findings, finding)
	}
	return findings
}
