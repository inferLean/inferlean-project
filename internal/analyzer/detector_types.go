package analyzer

import (
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

type WorkloadIntent string

const (
	BalancedIntent        WorkloadIntent = "balanced"
	ThroughputFirstIntent WorkloadIntent = "throughput_first"
	LatencyFirstIntent    WorkloadIntent = "latency_first"
)

const (
	detectorQueueDominatedTTFT                    = "queue_dominated_ttft"
	detectorThroughputSaturationWithQueuePressure = "throughput_saturation_with_queue_pressure"
	detectorUnderutilizedGPUOrConservativeBatch   = "underutilized_gpu_or_conservative_batching"
	detectorKVCachePressurePreemptions            = "kv_cache_pressure_preemptions"
	detectorPrefixCacheIneffective                = "prefix_cache_ineffective"
	detectorPromptRecomputationThrashing          = "prompt_recomputation_thrashing"
	detectorPrefillHeavyWorkload                  = "prefill_heavy_workload"
	detectorDecodeBoundGeneration                 = "decode_bound_generation"
	detectorCPUOrHostBottleneck                   = "cpu_or_host_bottleneck"
	detectorGPUMemorySaturation                   = "gpu_memory_saturation_without_throughput"
	detectorGPUHardwareInstability                = "gpu_hardware_instability"
	detectorTextOnlyOnMultimodalStack             = "text_only_workload_on_multimodal_stack"
)

type DetectorSpec struct {
	ID                  string
	Category            string
	Implemented         bool
	MinDataRequirements []string
}

type Detector interface {
	Spec() DetectorSpec
	Evaluate(features FeatureSet) (model.Finding, error)
}

type MetricSummary struct {
	Name        string
	Samples     []float64
	SampleCount int
	First       float64
	Last        float64
	Average     float64
	Min         float64
	Max         float64
	Delta       float64
}

type FeatureSet struct {
	SnapshotCount                  int
	IntervalSeconds                float64
	Metrics                        map[string]MetricSummary
	ModelName                      string
	MultimodalLikely               bool
	MultimodalConfigPresent        bool
	LanguageModelOnlyEnabled       bool
	MMPreprocessorCacheDisabled    bool
	MMProcessorCacheGB             float64
	TrafficObserved                bool
	EnoughLatencySamples           bool
	EnoughKVCacheSamples           bool
	AvgGPUComputeLoadPct           float64
	ComputeLoadSource              string
	AvgGPUMemoryBandwidthLoadPct   float64
	MemoryBandwidthLoadAvailable   bool
	AvgGPUTensorLoadPct            float64
	TensorLoadAvailable            bool
	SaturationSource               string
	RealSaturationMetricsAvailable bool
	AvgGPUUtilizationPct           float64
	MaxGPUUtilizationPct           float64
	AvgRequestsRunning             float64
	MaxRequestsRunning             float64
	AvgRequestsWaiting             float64
	MaxRequestsWaiting             float64
	AvgKVCacheUsagePct             float64
	MaxKVCacheUsagePct             float64
	AvgTTFTSeconds                 float64
	TTFTCountDelta                 float64
	AvgQueueTimeSeconds            float64
	QueueTimeCountDelta            float64
	AvgRequestLatencySeconds       float64
	RequestLatencyCountDelta       float64
	AvgPrefillTimeSeconds          float64
	PrefillCountDelta              float64
	AvgDecodeTimeSeconds           float64
	DecodeCountDelta               float64
	RequestSuccessDelta            float64
	PromptTokensDelta              float64
	GenerationTokensDelta          float64
	PreemptionsDelta               float64
	PrefixCacheQueriesDelta        float64
	PrefixCacheHitsDelta           float64
	MMCacheQueriesDelta            float64
	MMCacheHitsDelta               float64
	PromptTokensCachedDelta        float64
	PromptTokensRecomputedDelta    float64
	GPUFBUsedBytesAvg              float64
	GPUFBFreeBytesAvg              float64
	GPUFBUsagePctAvg               float64
	XIDErrorsDelta                 float64
	AverageCPUUtilizationPct       float64
}

type detectorFunc struct {
	spec DetectorSpec
	eval func(features FeatureSet) (model.Finding, error)
}

func (d detectorFunc) Spec() DetectorSpec {
	return d.spec
}

func (d detectorFunc) Evaluate(features FeatureSet) (model.Finding, error) {
	if d.eval == nil {
		return model.Finding{
			ID:         d.spec.ID,
			Category:   d.spec.Category,
			Status:     model.FindingStatusInsufficientData,
			Severity:   model.SeverityNone,
			Confidence: 0.2,
			Summary:    "Detector did not receive enough evidence to evaluate this symptom.",
		}, nil
	}
	return d.eval(features)
}

func normalizeWorkloadIntent(intent WorkloadIntent) WorkloadIntent {
	switch WorkloadIntent(strings.ToLower(strings.TrimSpace(string(intent)))) {
	case ThroughputFirstIntent:
		return ThroughputFirstIntent
	case LatencyFirstIntent:
		return LatencyFirstIntent
	default:
		return BalancedIntent
	}
}
