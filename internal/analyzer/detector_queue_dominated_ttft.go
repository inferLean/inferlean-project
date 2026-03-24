package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func newQueueDominatedTTFTDetector() Detector {
	spec := DetectorSpec{
		ID:          detectorQueueDominatedTTFT,
		Category:    "latency",
		Implemented: true,
		MinDataRequirements: []string{
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
				evidence("avg_ttft_seconds", features.AvgTTFTSeconds, ""),
				evidence("avg_queue_time_seconds", features.AvgQueueTimeSeconds, ""),
				evidence("max_requests_waiting", features.MaxRequestsWaiting, ""),
				evidence("ttft_sample_count", features.TTFTCountDelta, ""),
				evidence("queue_sample_count", features.QueueTimeCountDelta, ""),
			}

			if !features.TrafficObserved {
				return insufficientFinding(
					spec,
					"Need live request traffic before diagnosing whether queueing dominates TTFT.",
					0.25,
					baseEvidence...,
				), nil
			}
			if !features.EnoughLatencySamples || features.AvgTTFTSeconds <= 0 {
				return insufficientFinding(
					spec,
					"Traffic was observed, but there were not enough TTFT and queue samples to separate queueing from model compute.",
					0.35,
					baseEvidence...,
				), nil
			}

			queueShare := 0.0
			if features.AvgTTFTSeconds > 0 {
				queueShare = features.AvgQueueTimeSeconds / features.AvgTTFTSeconds
			}
			baseEvidence = append(baseEvidence, evidence("queue_share_of_ttft", queueShare, "ratio"))

			if features.AvgTTFTSeconds >= 2.0 && queueShare >= 0.5 && features.MaxRequestsWaiting >= 1 {
				severity := model.SeverityMedium
				confidence := 0.8
				if queueShare >= 0.75 || features.MaxRequestsWaiting >= 4 {
					severity = model.SeverityHigh
					confidence = 0.88
				}
				return presentFinding(
					spec,
					severity,
					"TTFT is elevated and queue time is the dominant contributor, which points to scheduler pressure or insufficient serving headroom.",
					confidence,
					baseEvidence...,
				), nil
			}

			return absentFinding(
				spec,
				"Queue time does not appear to be the dominant contributor to TTFT in the observed window.",
				0.78,
				baseEvidence...,
			), nil
		},
	}
}
