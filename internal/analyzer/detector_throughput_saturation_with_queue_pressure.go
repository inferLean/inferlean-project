package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newThroughputSaturationWithQueuePressureDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorThroughputSaturationWithQueuePressure,
		Category:    "throughput",
		Implemented: true,
		MinDataRequirements: []string{
			"gpu_utilization_pct",
			"vllm:num_requests_waiting",
			"vllm:time_to_first_token_seconds_sum",
			"vllm:time_to_first_token_seconds_count",
			"vllm:request_queue_time_seconds_sum",
			"vllm:request_queue_time_seconds_count",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
				evidence("max_gpu_utilization_pct", features.MaxGPUUtilizationPct, ""),
				evidence("avg_requests_waiting", features.AvgRequestsWaiting, ""),
				evidence("max_requests_waiting", features.MaxRequestsWaiting, ""),
				evidence("avg_ttft_seconds", features.AvgTTFTSeconds, ""),
				evidence("avg_queue_time_seconds", features.AvgQueueTimeSeconds, ""),
				evidence("generation_tokens_delta", features.GenerationTokensDelta, ""),
			}

			if !features.TrafficObserved || features.SnapshotCount < 2 {
				return insufficientFinding(
					spec,
					"Need at least two snapshots with live traffic before deciding whether queue growth reflects true serving saturation.",
					0.3,
					baseEvidence...,
				), nil
			}
			if features.AvgGPUUtilizationPct <= 0 {
				return insufficientFinding(
					spec,
					"Traffic was observed, but GPU utilization telemetry was not available enough to judge saturation.",
					0.35,
					baseEvidence...,
				), nil
			}
			if !features.EnoughLatencySamples {
				return insufficientFinding(
					spec,
					"Traffic was observed, but there were not enough TTFT and queue samples to separate transient bursts from sustained saturation.",
					0.4,
					baseEvidence...,
				), nil
			}

			queueShare := 0.0
			if features.AvgTTFTSeconds > 0 {
				queueShare = features.AvgQueueTimeSeconds / features.AvgTTFTSeconds
			}
			baseEvidence = append(baseEvidence, evidence("queue_share_of_ttft", queueShare, "ratio"))

			highGPU := features.AvgGPUUtilizationPct >= 75
			persistentQueue := features.AvgRequestsWaiting >= 2 || features.MaxRequestsWaiting >= 4
			queueDominantEnough := features.AvgQueueTimeSeconds >= 0.75 || queueShare >= 0.35

			if highGPU && persistentQueue && queueDominantEnough {
				severity := model.SeverityMedium
				confidence := 0.8
				if features.AvgGPUUtilizationPct >= 85 && features.MaxRequestsWaiting >= 6 && queueShare >= 0.45 {
					severity = model.SeverityHigh
					confidence = 0.89
				}
				if features.AvgGPUUtilizationPct >= 90 && features.MaxRequestsWaiting >= 8 && queueShare >= 0.55 {
					severity = model.SeverityCritical
					confidence = 0.94
				}
				return presentFinding(
					spec,
					severity,
					"GPU utilization stayed high while requests continued to queue, which points to sustained serving saturation rather than conservative batching.",
					confidence,
					baseEvidence...,
				), nil
			}

			return absentFinding(
				spec,
				"Observed traffic did not show sustained high GPU utilization together with enough queue pressure to call this serving saturation.",
				0.77,
				baseEvidence...,
			), nil
		},
	}
}
