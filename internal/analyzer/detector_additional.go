package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newPrefixCacheIneffectiveDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorPrefixCacheIneffective,
		Category:    "caching",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:prefix_cache_queries_total",
			"vllm:prefix_cache_hits_total",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			hitRate := safeHitRate(features.PrefixCacheHitsDelta, features.PrefixCacheQueriesDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("prefix_cache_queries_delta", features.PrefixCacheQueriesDelta, ""),
				evidence("prefix_cache_hits_delta", features.PrefixCacheHitsDelta, ""),
				evidence("prefix_cache_hit_rate", hitRate, "ratio"),
				evidence("prompt_tokens_delta", features.PromptTokensDelta, ""),
				evidence("avg_prefill_time_seconds", features.AvgPrefillTimeSeconds, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before diagnosing whether prefix cache opportunities are being missed.", 0.25, baseEvidence...), nil
			}
			if features.PrefixCacheQueriesDelta < 20 {
				return insufficientFinding(spec, "Observed window did not include enough prefix-cache queries to judge whether repeated prefixes are benefiting from caching.", 0.35, baseEvidence...), nil
			}

			if hitRate < 0.15 {
				severity := model.SeverityMedium
				confidence := 0.78
				if hitRate < 0.05 || features.PrefixCacheQueriesDelta >= 100 {
					severity = model.SeverityHigh
					confidence = 0.88
				}
				return presentFinding(spec, severity, "Prefix cache queries were present, but hit rate stayed low, which suggests repeated prompt prefixes are not being reused effectively.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Prefix cache hit rate was healthy enough that ineffective prefix reuse does not look like a dominant issue.", 0.8, baseEvidence...), nil
		},
	}
}

func newPromptRecomputationThrashingDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorPromptRecomputationThrashing,
		Category:    "memory",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:prompt_tokens_recomputed_total",
			"vllm:prompt_tokens_cached_total",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			totalCacheWork := features.PromptTokensCachedDelta + features.PromptTokensRecomputedDelta
			recomputeShare := safeHitRate(features.PromptTokensRecomputedDelta, totalCacheWork)
			baseEvidence := []model.EvidenceItem{
				evidence("prompt_tokens_cached_delta", features.PromptTokensCachedDelta, ""),
				evidence("prompt_tokens_recomputed_delta", features.PromptTokensRecomputedDelta, ""),
				evidence("prompt_recompute_share", recomputeShare, "ratio"),
				evidence("preemptions_delta", features.PreemptionsDelta, ""),
				evidence("max_kv_cache_usage_pct", features.MaxKVCacheUsagePct, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before diagnosing prompt recomputation thrashing.", 0.25, baseEvidence...), nil
			}
			if totalCacheWork <= 0 {
				return insufficientFinding(spec, "Observed window did not contain enough cached or recomputed prompt-token activity to judge recomputation behavior.", 0.35, baseEvidence...), nil
			}

			if recomputeShare >= 0.2 && (features.PreemptionsDelta > 0 || features.MaxKVCacheUsagePct >= 85) {
				severity := model.SeverityMedium
				confidence := 0.8
				if recomputeShare >= 0.35 || features.PreemptionsDelta >= 5 {
					severity = model.SeverityHigh
					confidence = 0.9
				}
				return presentFinding(spec, severity, "A meaningful share of cached prompt work is being recomputed, which points to cache thrashing and wasted prefill work.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Prompt-token telemetry does not show substantial recomputation pressure in the observed window.", 0.79, baseEvidence...), nil
		},
	}
}

func newPrefillHeavyWorkloadDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorPrefillHeavyWorkload,
		Category:    "compute",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:request_prefill_time_seconds_sum",
			"vllm:request_prefill_time_seconds_count",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			total := features.AvgPrefillTimeSeconds + features.AvgDecodeTimeSeconds
			prefillShare := safeHitRate(features.AvgPrefillTimeSeconds, total)
			promptToGeneration := safeRatio(features.PromptTokensDelta, features.GenerationTokensDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("avg_prefill_time_seconds", features.AvgPrefillTimeSeconds, ""),
				evidence("avg_decode_time_seconds", features.AvgDecodeTimeSeconds, ""),
				evidence("prefill_share", prefillShare, "ratio"),
				evidence("prompt_to_generation_token_ratio", promptToGeneration, "ratio"),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before deciding whether the workload is prefill-heavy.", 0.25, baseEvidence...), nil
			}
			if features.PrefillCountDelta < 5 || total <= 0 {
				return insufficientFinding(spec, "Observed window did not contain enough prefill/decode timing samples to classify workload shape.", 0.35, baseEvidence...), nil
			}

			if prefillShare >= 0.6 && promptToGeneration >= 1.2 {
				severity := model.SeverityLow
				confidence := 0.76
				if prefillShare >= 0.75 && promptToGeneration >= 2 {
					severity = model.SeverityMedium
					confidence = 0.84
				}
				return presentFinding(spec, severity, "Observed compute time is dominated by prefill rather than decode, which indicates a prompt-heavy workload shape.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Observed timing does not look dominated by prefill work.", 0.77, baseEvidence...), nil
		},
	}
}

func newDecodeBoundGenerationDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorDecodeBoundGeneration,
		Category:    "compute",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:request_decode_time_seconds_sum",
			"vllm:request_decode_time_seconds_count",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			total := features.AvgPrefillTimeSeconds + features.AvgDecodeTimeSeconds
			decodeShare := safeHitRate(features.AvgDecodeTimeSeconds, total)
			generationToPrompt := safeRatio(features.GenerationTokensDelta, features.PromptTokensDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("avg_prefill_time_seconds", features.AvgPrefillTimeSeconds, ""),
				evidence("avg_decode_time_seconds", features.AvgDecodeTimeSeconds, ""),
				evidence("decode_share", decodeShare, "ratio"),
				evidence("generation_to_prompt_token_ratio", generationToPrompt, "ratio"),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before deciding whether decode dominates the workload.", 0.25, baseEvidence...), nil
			}
			if features.DecodeCountDelta < 5 || total <= 0 {
				return insufficientFinding(spec, "Observed window did not contain enough decode timing samples to classify workload shape.", 0.35, baseEvidence...), nil
			}

			if decodeShare >= 0.6 && generationToPrompt >= 1.2 {
				severity := model.SeverityLow
				confidence := 0.75
				if decodeShare >= 0.75 && generationToPrompt >= 2 {
					severity = model.SeverityMedium
					confidence = 0.83
				}
				return presentFinding(spec, severity, "Observed compute time is dominated by decode rather than prefill, which indicates a generation-heavy workload shape.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Observed timing does not look dominated by decode work.", 0.77, baseEvidence...), nil
		},
	}
}

func newCPUOrHostBottleneckDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorCPUOrHostBottleneck,
		Category:    "host",
		Implemented: true,
		MinDataRequirements: []string{
			"node_cpu_utilization_pct",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			mmHitRate := safeHitRate(features.MMCacheHitsDelta, features.MMCacheQueriesDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("avg_cpu_utilization_pct", features.AverageCPUUtilizationPct, ""),
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
				evidence("avg_requests_running", features.AvgRequestsRunning, ""),
				evidence("max_requests_waiting", features.MaxRequestsWaiting, ""),
				evidence("avg_ttft_seconds", features.AvgTTFTSeconds, ""),
				evidence("mm_cache_hit_rate", mmHitRate, "ratio"),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before diagnosing CPU or host-side bottlenecks.", 0.25, baseEvidence...), nil
			}
			if features.AverageCPUUtilizationPct <= 0 {
				return insufficientFinding(spec, "Traffic was observed, but host CPU telemetry was not available enough to diagnose host bottlenecks.", 0.35, baseEvidence...), nil
			}

			cpuHot := features.AverageCPUUtilizationPct >= 75
			gpuSlack := features.AvgGPUUtilizationPct > 0 && features.AvgGPUUtilizationPct < 60
			latencyPressure := features.AvgTTFTSeconds >= 1.5 || features.MaxRequestsWaiting >= 2
			if cpuHot && gpuSlack && latencyPressure {
				severity := model.SeverityMedium
				confidence := 0.8
				if features.AverageCPUUtilizationPct >= 90 && features.AvgGPUUtilizationPct < 40 {
					severity = model.SeverityHigh
					confidence = 0.9
				}
				return presentFinding(spec, severity, "Host CPU stayed busy while GPU utilization remained comparatively low, which points to host-side processing or scheduling bottlenecks.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Observed telemetry does not show host CPU pressure standing out as the dominant limiter.", 0.76, baseEvidence...), nil
		},
	}
}

func newMultimodalPreprocessingCPUBottleneckDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorMultimodalPreprocessingCPUBottleneck,
		Category:    "multimodal",
		Implemented: true,
		MinDataRequirements: []string{
			"node_cpu_utilization_pct",
			"vllm:mm_cache_queries_total",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			mmHitRate := safeHitRate(features.MMCacheHitsDelta, features.MMCacheQueriesDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("multimodal_likely", boolToMetricValue(features.MultimodalLikely), "bool"),
				evidence("avg_cpu_utilization_pct", features.AverageCPUUtilizationPct, ""),
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
				evidence("avg_ttft_seconds", features.AvgTTFTSeconds, ""),
				evidence("avg_queue_time_seconds", features.AvgQueueTimeSeconds, ""),
				evidence("mm_cache_queries_delta", features.MMCacheQueriesDelta, ""),
				evidence("mm_cache_hit_rate", mmHitRate, "ratio"),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before deciding whether multimodal preprocessing is the dominant bottleneck.", 0.25, baseEvidence...), nil
			}
			if !features.MultimodalLikely {
				return absentFinding(spec, "Observed traffic does not look multimodal enough for host-side media preprocessing to be the dominant limiter.", 0.82, baseEvidence...), nil
			}
			if features.AverageCPUUtilizationPct <= 0 {
				return insufficientFinding(spec, "Traffic was observed, but host CPU telemetry was not available enough to diagnose multimodal preprocessing pressure.", 0.35, baseEvidence...), nil
			}
			if features.MMCacheQueriesDelta < 5 && !features.MMPreprocessorCacheDisabled {
				return insufficientFinding(spec, "Observed window did not contain enough multimodal processor activity to judge preprocessing pressure confidently.", 0.4, baseEvidence...), nil
			}

			cpuHot := features.AverageCPUUtilizationPct >= 75
			gpuSlack := features.AvgGPUUtilizationPct > 0 && features.AvgGPUUtilizationPct < 55
			preGPULatencyGap := features.AvgTTFTSeconds >= 1.2 || features.AvgQueueTimeSeconds >= 0.6
			cacheRisk := features.MMPreprocessorCacheDisabled || mmHitRate < 0.2
			if cpuHot && gpuSlack && preGPULatencyGap && cacheRisk {
				severity := model.SeverityMedium
				confidence := 0.84
				if features.AverageCPUUtilizationPct >= 88 && features.AvgGPUUtilizationPct < 40 && (features.MMPreprocessorCacheDisabled || mmHitRate < 0.1) {
					severity = model.SeverityHigh
					confidence = 0.92
				}
				return presentFinding(spec, severity, "Multimodal request preprocessing appears CPU-bound before the GPU saturates, which suggests the input pipeline is limiting end-to-end latency and throughput.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Current multimodal telemetry does not show host-side preprocessing standing out as the dominant limiter.", 0.77, baseEvidence...), nil
		},
	}
}

func newMultimodalCacheIneffectiveDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorMultimodalCacheIneffective,
		Category:    "multimodal",
		Implemented: true,
		MinDataRequirements: []string{
			"vllm:mm_cache_queries_total",
			"vllm:mm_cache_hits_total",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			hitRate := safeHitRate(features.MMCacheHitsDelta, features.MMCacheQueriesDelta)
			baseEvidence := []model.EvidenceItem{
				evidence("multimodal_likely", boolToMetricValue(features.MultimodalLikely), "bool"),
				evidence("mm_cache_queries_delta", features.MMCacheQueriesDelta, ""),
				evidence("mm_cache_hits_delta", features.MMCacheHitsDelta, ""),
				evidence("mm_cache_hit_rate", hitRate, "ratio"),
				evidence("mm_preprocessor_cache_disabled", boolToMetricValue(features.MMPreprocessorCacheDisabled), "bool"),
				evidence("avg_cpu_utilization_pct", features.AverageCPUUtilizationPct, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before deciding whether multimodal cache reuse is ineffective.", 0.25, baseEvidence...), nil
			}
			if !features.MultimodalLikely {
				return absentFinding(spec, "Observed traffic does not look multimodal enough for multimodal cache reuse to be a meaningful limiter.", 0.83, baseEvidence...), nil
			}
			if features.MMCacheQueriesDelta < 20 && !features.MMPreprocessorCacheDisabled {
				return insufficientFinding(spec, "Observed window did not include enough multimodal cache activity to judge reuse quality confidently.", 0.38, baseEvidence...), nil
			}

			if features.MMPreprocessorCacheDisabled || hitRate < 0.2 {
				severity := model.SeverityLow
				confidence := 0.8
				if features.MMPreprocessorCacheDisabled || hitRate < 0.1 || features.MMCacheQueriesDelta >= 80 {
					severity = model.SeverityMedium
					confidence = 0.89
				}
				return presentFinding(spec, severity, "Multimodal processor cache reuse looks weak, so repeated image or media preprocessing work is likely being redone on the host.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Multimodal cache hit rate looks healthy enough that repeated media preprocessing is unlikely to be the dominant issue.", 0.79, baseEvidence...), nil
		},
	}
}

func newGPUMemorySaturationWithoutThroughputDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorGPUMemorySaturation,
		Category:    "memory",
		Implemented: true,
		MinDataRequirements: []string{
			"gpu_fb_used_bytes",
			"gpu_fb_free_bytes",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("avg_gpu_fb_usage_pct", features.GPUFBUsagePctAvg, ""),
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
				evidence("avg_requests_waiting", features.AvgRequestsWaiting, ""),
				evidence("preemptions_delta", features.PreemptionsDelta, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(spec, "Need live traffic before diagnosing whether GPU memory saturation is limiting throughput.", 0.25, baseEvidence...), nil
			}
			if features.GPUFBUsagePctAvg <= 0 {
				return insufficientFinding(spec, "Traffic was observed, but framebuffer usage telemetry was not available enough to judge memory saturation.", 0.35, baseEvidence...), nil
			}

			if features.GPUFBUsagePctAvg >= 90 && features.AvgGPUUtilizationPct < 60 && features.AvgRequestsWaiting < 2 && features.PreemptionsDelta == 0 {
				severity := model.SeverityMedium
				confidence := 0.79
				if features.GPUFBUsagePctAvg >= 96 && features.AvgGPUUtilizationPct < 45 {
					severity = model.SeverityHigh
					confidence = 0.88
				}
				return presentFinding(spec, severity, "GPU memory is mostly occupied without correspondingly high GPU utilization or queue pressure, which suggests memory headroom is limiting useful batching gains.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "Framebuffer usage does not appear to be the main limiter on throughput in the observed window.", 0.75, baseEvidence...), nil
		},
	}
}

func newGPUHardwareInstabilityDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorGPUHardwareInstability,
		Category:    "reliability",
		Implemented: true,
		MinDataRequirements: []string{
			"DCGM_FI_DEV_XID_ERRORS",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("xid_errors_delta", features.XIDErrorsDelta, ""),
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
			}

			if features.XIDErrorsDelta > 0 {
				severity := model.SeverityHigh
				confidence := 0.93
				if features.XIDErrorsDelta >= 3 {
					severity = model.SeverityCritical
					confidence = 0.97
				}
				return presentFinding(spec, severity, "GPU XID errors were observed during the analysis window, which points to hardware or driver instability rather than a normal tuning problem.", confidence, baseEvidence...), nil
			}

			return absentFinding(spec, "No GPU XID errors were observed in the analysis window.", 0.88, baseEvidence...), nil
		},
	}
}

func safeRatio(num, denom float64) float64 {
	if denom <= 0 {
		if num > 0 {
			return num
		}
		return 0
	}
	return num / denom
}
