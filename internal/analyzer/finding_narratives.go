package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

type findingNarrative struct {
	stage     string
	technical string
	impact    string
}

var findingNarratives = map[string]findingNarrative{
	detectorQueueDominatedTTFT: {
		stage:     "queue",
		technical: "Requests spend a disproportionate share of time waiting for scheduler admission before prefill can begin, so the latency problem is upstream of token generation.",
		impact:    "Queue-heavy TTFT makes interactive traffic feel slow even when decode itself is not the dominant limiter.",
	},
	detectorThroughputSaturationWithQueuePressure: {
		stage:     "queue",
		technical: "GPU compute stays busy while requests continue to accumulate in the waiting queue, which indicates sustained serving saturation rather than conservative batching.",
		impact:    "Throughput is capped by current serving capacity, so raising demand mainly increases waiting time and tail latency.",
	},
	detectorUnderutilizedGPUOrConservativeBatch: {
		stage:     "prefill",
		technical: "The scheduler is not feeding enough concurrent work to the GPU, so compute and memory pipelines remain partially idle during live traffic.",
		impact:    "Unused GPU headroom leaves throughput on the table and can keep queue relief or latency gains unrealized.",
	},
	detectorKVCachePressurePreemptions: {
		stage:     "memory",
		technical: "KV-cache occupancy is high enough that requests are being preempted or recomputed, which forces already-processed prompt work back through the system.",
		impact:    "Memory churn increases p95 latency and reduces useful throughput because compute is spent replaying prompt work instead of serving new tokens.",
	},
	detectorPrefixCacheIneffective: {
		stage:     "cache",
		technical: "Repeated prompt prefixes are not being reused efficiently, so prefill work that could be cached is repeatedly recomputed.",
		impact:    "Poor prefix reuse inflates prefill cost, which drags down both TTFT and throughput on repeated prompt patterns.",
	},
	detectorPromptRecomputationThrashing: {
		stage:     "cache",
		technical: "Cached prompt state is being evicted or bypassed often enough that prompt tokens are recomputed instead of reused.",
		impact:    "Recomputation burns capacity on duplicate work, worsening latency and reducing headroom for new requests.",
	},
	detectorPrefillHeavyWorkload: {
		stage:     "prefill",
		technical: "Prompt ingestion and KV-cache construction dominate request compute time, so the workload is bottlenecked before steady-state token generation.",
		impact:    "Prompt-heavy traffic is especially sensitive to chunked-prefill budgets, cache reuse, and fairness between short and long prompts.",
	},
	detectorDecodeBoundGeneration: {
		stage:     "decode",
		technical: "Token-by-token decode consumes more time than prefill, so throughput is limited by steady-state generation rather than prompt ingestion.",
		impact:    "Decode-bound traffic is more sensitive to inter-token latency and output length than to larger prefill batch budgets.",
	},
	detectorCPUOrHostBottleneck: {
		stage:     "host",
		technical: "CPU-side scheduling, tokenization, transport, or input processing appears to be gating GPU work, so the accelerator cannot stay fully occupied.",
		impact:    "Host pressure throttles throughput before the GPU saturates and can also inflate TTFT through scheduler delay.",
	},
	detectorMultimodalPreprocessingCPUBottleneck: {
		stage:     "multimodal",
		technical: "Multimodal request preparation appears to be consuming substantial host CPU time before GPU execution, so image or media preprocessing is likely gating serving throughput.",
		impact:    "CPU-side media preprocessing can inflate end-to-end latency and keep GPU pressure deceptively low even when the node feels slow to operators.",
	},
	detectorMultimodalCacheIneffective: {
		stage:     "multimodal",
		technical: "The multimodal processor cache is seeing enough query volume to matter, but hit rate remains low, which points to repeated image or media work not being reused effectively.",
		impact:    "Repeated decode, resize, and normalization work burns host capacity and adds latency that could often be avoided with stable cache keys or upstream preprocessing reuse.",
	},
	detectorGPUMemorySaturation: {
		stage:     "memory",
		technical: "Framebuffer usage is already high even though useful compute throughput is not, which suggests memory footprint rather than compute is constraining batching efficiency.",
		impact:    "Memory-bound serving reduces safe concurrency headroom and can turn further batching increases into churn instead of higher throughput.",
	},
	detectorGPUHardwareInstability: {
		stage:     "host",
		technical: "GPU XID-like instability indicates the host-driver-hardware stack is not operating normally, so performance data may be distorted by fault handling.",
		impact:    "Tuning conclusions are unreliable until the instability path is resolved, because faults can mimic normal latency or throughput regressions.",
	},
	detectorTextOnlyOnMultimodalStack: {
		stage:     "multimodal",
		technical: "The deployment looks capable of multimodal processing, but the observed window shows only text-style activity and no meaningful multimodal processor usage.",
		impact:    "Leaving multimodal pathways enabled for text-only traffic can add avoidable preprocessing, cache, and memory overhead without improving output quality.",
	},
}

func enrichFindingNarrative(finding model.Finding) model.Finding {
	narrative, ok := findingNarratives[finding.ID]
	if !ok {
		return finding
	}
	finding.PipelineStage = narrative.stage
	finding.TechnicalExplanation = narrative.technical
	finding.ImpactExplanation = narrative.impact
	return finding
}
