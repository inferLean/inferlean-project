# Recommender Module Guide

This module owns exact tuning proposals and prediction output.

## Responsibilities

- Consume `AnalysisReport` and optionally a local benchmark corpus.
- Generate deterministic, issue-linked recommendations from analyzer findings first.
- Use a near benchmark profile only to calibrate confidence, predicted impact, and headroom when available.
- Emit concise, exact parameter changes when safe, or operational actions when no safe deterministic knob exists.
- Support explicit what-if requests via scenario prediction.
- Optionally add `llm_enhanced` prose, but only as a separate non-canonical block.

## Non-responsibilities

- Do not collect raw metrics directly.
- Do not replace analyzer diagnosis logic.
- Do not let LLM output alter canonical parameter values, predicted metrics, or confidence.

## Output contract

The recommender emits `RecommendationReport` with:

- source analysis reference
- objective
- matched corpus profile when a near benchmark calibration exists
- baseline prediction
- issue-linked recommendation items
- optional scenario prediction
- optional `llm_enhanced`
- warnings when calibration is unavailable, when traffic is insufficient, or when compatibility data is missing

## Synthetic input example

Analysis input:

```json
{
  "schema_version": "v3",
  "current_vllm_configurations": {
    "model_name": "Qwen 3 30B A3B",
    "max_num_seqs": 8,
    "max_num_batched_tokens": 8192,
    "tensor_parallel_size": 4
  },
  "analysis_summary": {
    "workload_intent": "throughput_first",
    "data_quality": {
      "traffic_observed": true
    },
    "findings": [
      {
        "id": "underutilized_gpu_or_conservative_batching",
        "status": "present",
        "severity": "high",
        "confidence": 0.86,
        "summary": "Traffic was present, but GPU utilization stayed low with little queueing."
      }
    ]
  }
}
```

Corpus input:

```json
{
  "version": "2026-03-21",
  "profiles": [
    {
      "id": "qwen3-30b-h100x4-throughput",
      "model_name": "Qwen 3 30B A3B",
      "model_family": "qwen3",
      "gpu_count": 4,
      "hardware_class": "h100",
      "workload_class": "throughput_headroom",
      "measurements": [
        {
          "parameters": {
            "max_num_seqs": 8,
            "max_num_batched_tokens": 8192
          },
          "metrics": {
            "throughput_tokens_per_second": 4200,
            "ttft_ms": 620,
            "latency_p50_ms": 1450,
            "latency_p95_ms": 2100,
            "gpu_utilization_pct": 24
          }
        },
        {
          "parameters": {
            "max_num_seqs": 16,
            "max_num_batched_tokens": 16384
          },
          "metrics": {
            "throughput_tokens_per_second": 6100,
            "ttft_ms": 760,
            "latency_p50_ms": 1650,
            "latency_p95_ms": 2440,
            "gpu_utilization_pct": 44
          }
        }
      ]
    }
  ]
}
```

## Synthetic output example

```json
{
  "schema_version": "recommendation/v1",
  "objective": "throughput_first",
  "matched_corpus_profile": {
    "id": "qwen3-30b-h100x4-throughput",
    "match_score": 0.92,
    "basis": "exact model match, gpu footprint match, hardware class match, workload class match"
  },
  "baseline_prediction": {
    "throughput_tokens_per_second": 4200,
    "ttft_ms": 620,
    "latency_p50_ms": 1450,
    "latency_p95_ms": 2100,
    "gpu_utilization_pct": 24,
    "basis": "exact corpus baseline match",
    "confidence": 0.96
  },
  "recommendations": [
    {
      "id": "benchmark_profile_qwen3-30b-h100x4-throughput",
      "priority": 1,
      "objective": "throughput_first",
      "summary": "Apply benchmark-backed tuning: max_num_batched_tokens=16384, max_num_seqs=16",
      "changes": [
        {
          "name": "max_num_batched_tokens",
          "current_value": 8192,
          "recommended_value": 16384
        },
        {
          "name": "max_num_seqs",
          "current_value": 8,
          "recommended_value": 16
        }
      ],
      "predicted_effect": {
        "throughput_tokens_per_second": 6100,
        "ttft_ms": 760,
        "latency_p50_ms": 1650,
        "latency_p95_ms": 2440,
        "gpu_utilization_pct": 44,
        "throughput_delta_pct": 45.2,
        "ttft_delta_pct": 22.6,
        "latency_p50_delta_pct": 13.8,
        "latency_p95_delta_pct": 16.2,
        "gpu_utilization_delta_pct": 83.3
      },
      "confidence": 0.88,
      "basis": "Matched corpus profile qwen3-30b-h100x4-throughput using exact model match, gpu footprint match, hardware class match, workload class match."
    }
  ]
}
```

## Recommendation guidance

- Exact fixes belong here, not in analyzer output.
- Keep canonical recommendation fields concise and operational.
- Emit one recommendation per present analyzer finding.
- Keep corpus optional; do not treat a missing corpus as a failure mode.
- If no near corpus match exists, keep the rule-based recommendation and lower confidence instead of suppressing output.
