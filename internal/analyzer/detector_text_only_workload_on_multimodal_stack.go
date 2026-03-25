package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newTextOnlyWorkloadOnMultimodalStackDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorTextOnlyOnMultimodalStack,
		Category:    "multimodal",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:mm_cache_queries",
			"effective_vllm_configuration",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("multimodal_likely", boolToMetricValue(features.MultimodalLikely), "bool"),
				evidence("multimodal_config_present", boolToMetricValue(features.MultimodalConfigPresent), "bool"),
				evidence("language_model_only_enabled", boolToMetricValue(features.LanguageModelOnlyEnabled), "bool"),
				evidence("mm_cache_queries_delta", features.MMCacheQueriesDelta, ""),
				evidence("mm_cache_hits_delta", features.MMCacheHitsDelta, ""),
				evidence("generation_tokens_delta", features.GenerationTokensDelta, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before deciding whether multimodal paths are unused for this workload.", 0.25, baseEvidence...), nil
			}
			if features.LanguageModelOnlyEnabled {
				return absentFinding(spec, "The deployment already runs in language-model-only mode, so unused multimodal overhead is not the issue.", 0.88, baseEvidence...), nil
			}
			if !features.MultimodalLikely && !features.MultimodalConfigPresent {
				return absentFinding(spec, "The deployment does not look multimodal-capable enough for unused multimodal overhead to be a likely issue.", 0.72, baseEvidence...), nil
			}
			if features.MMCacheQueriesDelta > 0 || features.MMCacheHitsDelta > 0 {
				return absentFinding(spec, "Observed multimodal cache activity indicates the multimodal stack is active during the analysis window.", 0.84, baseEvidence...), nil
			}
			if features.GenerationTokensDelta <= 0 && features.PromptTokensDelta <= 0 {
				return insufficientFinding(spec, "Observed traffic was too weak to decide whether the multimodal stack is unused for text-only serving.", 0.35, baseEvidence...), nil
			}

			confidence := 0.76
			severity := model.SeverityLow
			if features.MultimodalLikely && features.MultimodalConfigPresent {
				confidence = 0.86
				severity = model.SeverityMedium
			}

			return presentFinding(spec, severity, "The deployment appears multimodal-capable, but the observed traffic looks text-only, so unused multimodal paths may be adding avoidable overhead.", confidence, baseEvidence...), nil
		},
	}
}

func boolToMetricValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
