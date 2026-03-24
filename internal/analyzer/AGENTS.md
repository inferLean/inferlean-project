# Analyzer Module Guide

This module owns deterministic diagnosis only.

## Responsibilities

- Read collected report inputs and normalize signals into stable features.
- Emit evidence-rich findings about likely bottlenecks or workload/config mismatches.
- Emit data-quality notes about whether the observed window is sufficient for diagnosis.
- Optionally add `llm_enhanced` prose, but only as a separate non-canonical block.

## Non-responsibilities

- Do not emit exact parameter fixes.
- Do not depend on benchmark corpus matching.
- Do not let LLM output alter canonical finding IDs, status, severity, confidence, or evidence.

## Output contract

The analyzer emits `AnalysisReport` with:

- environment and deployment metadata
- collected metrics
- current vLLM configuration
- `analysis_summary.data_quality`
- `analysis_summary.findings`
- optional `llm_enhanced`

It must not emit recommendations.

## Synthetic input example

```json
{
  "vllm_version": "0.18.0",
  "deployment_type": "host",
  "collected_metrics": [
    {
      "time_label": "2026-03-21T14:30:00Z",
      "metrics": {
        "gpu_utilization_pct": 89,
        "vllm:num_requests_running": 7,
        "vllm:num_requests_waiting": 8,
        "vllm:request_success_total": 100,
        "vllm:generation_tokens_total": 30000,
        "vllm:time_to_first_token_seconds_sum": 280,
        "vllm:time_to_first_token_seconds_count": 100,
        "vllm:request_queue_time_seconds_sum": 160,
        "vllm:request_queue_time_seconds_count": 100
      }
    },
    {
      "time_label": "2026-03-21T14:31:00Z",
      "metrics": {
        "gpu_utilization_pct": 93,
        "vllm:num_requests_running": 7,
        "vllm:num_requests_waiting": 10,
        "vllm:request_success_total": 118,
        "vllm:generation_tokens_total": 35600,
        "vllm:time_to_first_token_seconds_sum": 340,
        "vllm:time_to_first_token_seconds_count": 118,
        "vllm:request_queue_time_seconds_sum": 220,
        "vllm:request_queue_time_seconds_count": 118
      }
    }
  ]
}
```

## Synthetic output example

```json
{
  "schema_version": "v3",
  "analysis_summary": {
    "workload_intent": "balanced",
    "data_quality": {
      "snapshot_count": 2,
      "interval_seconds": 60,
      "traffic_observed": true,
      "enough_latency_samples": true,
      "enough_kv_cache_samples": false
    },
    "findings": [
      {
        "id": "throughput_saturation_with_queue_pressure",
        "category": "throughput",
        "status": "present",
        "severity": "critical",
        "confidence": 0.94,
        "summary": "GPU utilization stayed high while requests continued to queue, which points to sustained serving saturation rather than conservative batching.",
        "evidence": [
          { "metric": "avg_gpu_utilization_pct", "value": 91 },
          { "metric": "max_requests_waiting", "value": 10 },
          { "metric": "avg_queue_time_seconds", "value": 3.33 }
        ]
      }
    ]
  },
  "llm_enhanced": {
    "summary": "The system appears saturated.",
    "explanation": "The GPU is already busy and queue pressure remains high, so the setup likely needs a higher-level serving change rather than only more aggressive batching."
  }
}
```

## Detector guidance

- New detectors must have a stable ID, explicit thresholds, and tests.
- Prefer findings that explain whether the setup can improve and what the likely wrong workload or bottleneck shape is.
- Keep overlap between detectors intentional and limited; a detector should add a distinct diagnosis, not restate another finding.

## Detector inventory

Current detector catalog and status:

| Detector ID | Category | Status | Notes |
| --- | --- | --- | --- |
| `queue_dominated_ttft` | `latency` | `implemented` | Elevated TTFT where queue time is the dominant contributor. |
| `throughput_saturation_with_queue_pressure` | `throughput` | `implemented` | High GPU utilization plus persistent queue pressure suggests true serving saturation. |
| `underutilized_gpu_or_conservative_batching` | `utilization` | `implemented` | Traffic exists but GPU stays underutilized with little queueing. |
| `kv_cache_pressure_preemptions` | `memory` | `implemented` | High KV-cache pressure with preemptions and recomputation. |
| `prefix_cache_ineffective` | `caching` | `implemented` | Low prefix-cache hit rate despite meaningful query volume. |
| `prompt_recomputation_thrashing` | `memory` | `implemented` | Recomputed prompt tokens indicate wasted cached work under pressure. |
| `prefill_heavy_workload` | `compute` | `implemented` | Prefill dominates compute time, indicating a prompt-heavy workload shape. |
| `decode_bound_generation` | `compute` | `implemented` | Decode dominates compute time, indicating a generation-heavy workload shape. |
| `cpu_or_host_bottleneck` | `host` | `implemented` | Host CPU pressure appears to limit throughput or latency before GPU saturation. |
| `gpu_memory_saturation_without_throughput` | `memory` | `implemented` | GPU memory occupancy is high without corresponding throughput gains. |
| `gpu_hardware_instability` | `reliability` | `implemented` | XID errors indicate hardware or driver instability. |

Analyzer findings are sorted by heuristic importance. Present findings also include per-finding `heuristic_improvement_pct`, and `analysis_summary.total_heuristic_improvement_pct` gives a capped aggregate estimate across present findings.

## Source-backed complementary detector backlog

The following backlog is based on recurring vLLM operational issues documented in official vLLM docs. These are the highest-signal complementary detectors to add next.

### Priority 1

| Proposed detector | Why it matters | Candidate signals | Source basis |
| --- | --- | --- | --- |
| `multimodal_preprocessing_cpu_bottleneck` | vLLM documents that media loading uses CPU threads, multimodal processor caching exists to avoid repeated processing, and cache/thread settings materially affect performance. This is a narrower high-value specialization of the host bottleneck case. | High CPU, low GPU, multimodal model, image/video traffic, low `mm_cache_hits/mm_cache_queries`, low request running, queue growth. | [Optimization and Tuning: Parallel Processing and Multi-Modal Caching](https://docs.vllm.ai/en/stable/configuration/optimization/), [Production Metrics](https://docs.vllm.ai/en/stable/usage/metrics/) |

### Priority 2

| Proposed detector | Why it matters | Candidate signals | Source basis |
| --- | --- | --- | --- |
| `chunked_prefill_tradeoff_misaligned` | vLLM documents that `max_num_batched_tokens` tunes a real latency/throughput tradeoff under chunked prefill. Wrong values can hurt TTFT or ITL depending on workload intent. | High TTFT with throughput-first or poor decode latency with latency-first, long prompts, `max_num_batched_tokens`, prefill/decode timing split. | [Optimization and Tuning: Chunked Prefill](https://docs.vllm.ai/en/stable/configuration/optimization/), [Engine Arguments](https://docs.vllm.ai/en/latest/configuration/engine_args/) |
| `long_prefill_starvation_or_unfairness` | vLLM exposes `max_num_partial_prefills`, `max_long_partial_prefills`, and `long_prefill_token_threshold` specifically to let shorter prompts jump ahead of long prompts. This suggests a detector for mixed workloads where long prefills hurt latency fairness. | Large prefill times, long prompts, short-request latency inflation, queue growth, chunked-prefill scheduler settings. | [Engine Arguments](https://docs.vllm.ai/en/latest/configuration/engine_args/) |
| `multimodal_cache_ineffective` | vLLM exposes multimodal cache hit/query metrics and configurable multimodal processor cache size/type. Poor cache effectiveness causes repeated CPU-side work on repeated media. | `mm_cache_queries`, `mm_cache_hits`, multimodal model, repeated media-heavy traffic, CPU pressure. | [Optimization and Tuning: Multi-Modal Caching](https://docs.vllm.ai/en/stable/configuration/optimization/), [Production Metrics](https://docs.vllm.ai/en/stable/usage/metrics/) |
| `mm_prompt_limits_too_permissive` | vLLM allows per-modality limits per prompt and multimodal processor kwargs like image crop settings. Overly permissive multimodal input limits can create CPU and memory spikes. | `limit_mm_per_prompt`, `mm_processor_kwargs`, multimodal traffic, CPU pressure, queue growth, high TTFT. | [Engine Arguments](https://docs.vllm.ai/en/latest/configuration/engine_args/) |

### Priority 3

| Proposed detector | Why it matters | Candidate signals | Source basis |
| --- | --- | --- | --- |
| `cpu_offload_penalty` | vLLM documents that CPU offload requires a fast CPU-GPU interconnect because weights are loaded on the fly during forwards. This can trade capacity for latency/throughput penalties. | `cpu_offload_gb`, host CPU load, GPU underutilization, inference latency inflation. | [Engine Arguments: OffloadConfig](https://docs.vllm.ai/en/latest/configuration/engine_args/) |
| `quantization_method_misaligned_with_goal` | Official vLLM quantization docs note that AWQ is currently more suitable for low-latency low-concurrency use and can have lower throughput than unquantized models. That makes it a useful config-risk detector for throughput-first serving. | Quantization config indicates AWQ, throughput-first intent, high queueing or lower-than-expected throughput. | [AutoAWQ](https://docs.vllm.ai/en/v0.6.1/quantization/auto_awq.html), [Quantization](https://docs.vllm.ai/en/stable/features/quantization/) |
| `cuda_graph_fallback_risk` | vLLM documents that long sequences beyond `max_seq_len_to_capture` fall back to eager mode, which can become a performance issue for long-context workloads. | `max_seq_len_to_capture`, long prompt/context evidence, latency regression on long contexts. | [OpenAI-Compatible Server](https://docs.vllm.ai/en/v0.7.0/serving/openai_compatible_server.html) |

## Research notes

The most usual recurring issue families, based on official vLLM docs, are:

- KV-cache pressure and preemption from insufficient memory headroom.
- Mis-tuned chunked-prefill batch budgets that trade TTFT against decode latency or throughput poorly.
- CPU underprovisioning for tokenization, scheduling, streaming, and multimodal input processing.
- Missed cache opportunities from ineffective prefix caching or multimodal processor caching.
- Multimodal-specific CPU exhaustion from media loading threads, processor kwargs, and repeated image/video preprocessing.
- Configuration choices that trade memory for speed but may harm throughput, such as CPU offload or certain quantization modes.

Inference from sources:

- The highest-priority remaining detector gap is `multimodal_preprocessing_cpu_bottleneck`, because it specializes a common real-world host bottleneck pattern for multimodal serving.
- The best next scheduler/config detector after the now-implemented core set is `chunked_prefill_tradeoff_misaligned`, because vLLM explicitly documents the tradeoff around `max_num_batched_tokens`.
