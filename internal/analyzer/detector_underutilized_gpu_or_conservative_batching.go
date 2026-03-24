package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newUnderutilizedGPUOrConservativeBatchingDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorUnderutilizedGPUOrConservativeBatch,
		Category:    "utilization",
		Implemented: true,
		MinDataRequirements: []string{
			"gpu_utilization_pct",
			"vllm:request_success_total",
			"vllm:generation_tokens_total",
			"vllm:num_requests_running",
			"vllm:num_requests_waiting",
		},
	}

	return detectorFunc{
		spec: spec,
		eval: func(features FeatureSet) (model.Finding, error) {
			baseEvidence := []model.EvidenceItem{
				evidence("avg_gpu_utilization_pct", features.AvgGPUUtilizationPct, ""),
				evidence("max_gpu_utilization_pct", features.MaxGPUUtilizationPct, ""),
				evidence("avg_requests_running", features.AvgRequestsRunning, ""),
				evidence("max_requests_waiting", features.MaxRequestsWaiting, ""),
				evidence("request_success_delta", features.RequestSuccessDelta, ""),
				evidence("generation_tokens_delta", features.GenerationTokensDelta, ""),
			}

			if !features.TrafficObserved || features.SnapshotCount < 2 {
				return insufficientFinding(
					spec,
					"Need at least two snapshots with live traffic before diagnosing underutilization or conservative batching.",
					0.3,
					baseEvidence...,
				), nil
			}
			if features.AvgGPUUtilizationPct <= 0 {
				return insufficientFinding(
					spec,
					"Traffic was observed, but GPU utilization telemetry was not available enough to evaluate batching efficiency.",
					0.35,
					baseEvidence...,
				), nil
			}

			lowQueueing := features.MaxRequestsWaiting < 1
			lowConcurrency := features.AvgRequestsRunning <= 1.5
			if features.AvgGPUUtilizationPct < 40 && lowQueueing && lowConcurrency {
				severity := model.SeverityMedium
				confidence := 0.79
				if features.AvgGPUUtilizationPct < 25 {
					severity = model.SeverityHigh
					confidence = 0.86
				}
				return presentFinding(
					spec,
					severity,
					"Traffic was present, but GPU utilization stayed low with little queueing, which usually means batching or concurrency is too conservative for the offered load.",
					confidence,
					baseEvidence...,
				), nil
			}

			return absentFinding(
				spec,
				"Observed traffic either kept the GPU reasonably busy or showed enough concurrency pressure that conservative batching is not the dominant issue.",
				0.76,
				baseEvidence...,
			), nil
		},
	}
}
