package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newKVCachePressurePreemptionsDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorKVCachePressurePreemptions,
		Category:    "memory",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:kv_cache_usage_perc",
			"vllm:num_preemptions_total",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("avg_kv_cache_usage_pct", features.AvgKVCacheUsagePct, ""),
				evidence("max_kv_cache_usage_pct", features.MaxKVCacheUsagePct, ""),
				evidence("preemptions_delta", features.PreemptionsDelta, ""),
				evidence("prompt_tokens_recomputed_delta", features.PromptTokensRecomputedDelta, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(
					spec,
					"Need live traffic before diagnosing KV cache pressure or memory-driven preemptions.",
					0.25,
					baseEvidence...,
				), nil
			}
			if !features.EnoughKVCacheSamples {
				return insufficientFinding(
					spec,
					"Traffic was observed, but KV cache telemetry did not contain enough samples to judge pressure confidently.",
					0.35,
					baseEvidence...,
				), nil
			}

			if features.MaxKVCacheUsagePct >= 85 && features.PreemptionsDelta > 0 {
				severity := model.SeverityHigh
				confidence := 0.84
				if features.MaxKVCacheUsagePct >= 95 || features.PreemptionsDelta >= 5 {
					severity = model.SeverityCritical
					confidence = 0.92
				}
				return presentFinding(
					spec,
					severity,
					"KV cache usage is running hot and preemptions are occurring, which points to memory oversubscription and unstable goodput.",
					confidence,
					baseEvidence...,
				), nil
			}

			return absentFinding(
				spec,
				"KV cache telemetry does not show sustained saturation with preemptions in the observed window.",
				0.8,
				baseEvidence...,
			), nil
		},
	}
}
