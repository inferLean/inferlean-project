package model

import (
	"encoding/json"
	"time"
)

const (
	AnalysisSchemaVersion       = "v5"
	RecommendationSchemaVersion = "recommendation/v3"
	SchemaVersion               = AnalysisSchemaVersion
	ToolName                    = "InferLean"
	ToolVersion                 = "dev"
)

type AnalysisReport struct {
	SchemaVersion             string                    `json:"schema_version"`
	GeneratedAt               time.Time                 `json:"generated_at"`
	ToolName                  string                    `json:"tool_name"`
	ToolVersion               string                    `json:"tool_version"`
	OSInformation             OSInformation             `json:"os_information"`
	GPUInformation            GPUInformation            `json:"gpu_information"`
	VLLMInformation           VLLMInformation           `json:"vllm_information"`
	WorkloadProfile           *WorkloadProfile          `json:"declared_intent"`
	ObservedWorkloadProfile   *ObservedWorkloadProfile  `json:"observed_intent,omitempty"`
	WorkloadProfileAlignment  *WorkloadProfileAlignment `json:"intent_alignment,omitempty"`
	CollectedMetrics          []CollectedMetricPoint    `json:"telemetry_samples,omitempty"`
	MetricCollectionOutputs   map[string]string         `json:"collector_logs,omitempty"`
	FeatureSummary            *FeatureSummary           `json:"telemetry_summary,omitempty"`
	CurrentLoadSummary        *CurrentLoadSummary       `json:"resource_load_summary,omitempty"`
	ServiceSummary            *ServiceSummary           `json:"service_snapshot,omitempty"`
	CurrentVLLMConfigurations map[string]any            `json:"effective_vllm_configuration"`
	AnalysisSummary           *AnalysisSummary          `json:"diagnosis_summary,omitempty"`
	LLMEnhanced               *LLMEnhancedOutput        `json:"llm_enhanced,omitempty"`
	AdvancedProfiling         *AdvancedProfilingInfo    `json:"advanced_profiling,omitempty"`
	Warnings                  []string                  `json:"warnings,omitempty"`
}

func (r *AnalysisReport) UnmarshalJSON(data []byte) error {
	type analysisReportAlias AnalysisReport
	aux := struct {
		*analysisReportAlias
		LegacyWorkloadProfile           *WorkloadProfile          `json:"workload_profile"`
		LegacyObservedWorkloadProfile   *ObservedWorkloadProfile  `json:"observed_workload_profile"`
		LegacyWorkloadProfileAlignment  *WorkloadProfileAlignment `json:"workload_profile_alignment"`
		LegacyCollectedMetrics          []CollectedMetricPoint    `json:"collected_metrics"`
		LegacyMetricCollectionOutputs   map[string]string         `json:"metric_collection_outputs"`
		LegacyFeatureSummary            *FeatureSummary           `json:"feature_summary"`
		LegacyCurrentLoadSummary        *CurrentLoadSummary       `json:"current_load_summary"`
		LegacyServiceSummary            *ServiceSummary           `json:"service_summary"`
		LegacyCurrentVLLMConfigurations map[string]any            `json:"current_vllm_configurations"`
		LegacyAnalysisSummary           *AnalysisSummary          `json:"analysis_summary"`
		LegacyAdvancedProfiling         *AdvancedProfilingInfo    `json:"advanced_profiling_information"`
	}{
		analysisReportAlias: (*analysisReportAlias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if r.WorkloadProfile == nil {
		r.WorkloadProfile = aux.LegacyWorkloadProfile
	}
	if r.ObservedWorkloadProfile == nil {
		r.ObservedWorkloadProfile = aux.LegacyObservedWorkloadProfile
	}
	if r.WorkloadProfileAlignment == nil {
		r.WorkloadProfileAlignment = aux.LegacyWorkloadProfileAlignment
	}
	if len(r.CollectedMetrics) == 0 && len(aux.LegacyCollectedMetrics) > 0 {
		r.CollectedMetrics = aux.LegacyCollectedMetrics
	}
	if len(r.MetricCollectionOutputs) == 0 && len(aux.LegacyMetricCollectionOutputs) > 0 {
		r.MetricCollectionOutputs = aux.LegacyMetricCollectionOutputs
	}
	if r.FeatureSummary == nil {
		r.FeatureSummary = aux.LegacyFeatureSummary
	}
	if r.CurrentLoadSummary == nil {
		r.CurrentLoadSummary = aux.LegacyCurrentLoadSummary
	}
	if r.ServiceSummary == nil {
		r.ServiceSummary = aux.LegacyServiceSummary
	}
	if len(r.CurrentVLLMConfigurations) == 0 && len(aux.LegacyCurrentVLLMConfigurations) > 0 {
		r.CurrentVLLMConfigurations = aux.LegacyCurrentVLLMConfigurations
	}
	if r.AnalysisSummary == nil {
		r.AnalysisSummary = aux.LegacyAnalysisSummary
	}
	if r.AdvancedProfiling == nil {
		r.AdvancedProfiling = aux.LegacyAdvancedProfiling
	}
	return nil
}

type OSInformation struct {
	OSType                   string         `json:"os_type"`
	Architecture             string         `json:"architecture"`
	OSVersion                string         `json:"os_version"`
	Distribution             string         `json:"distribution"`
	DiskSizeBytes            uint64         `json:"disk_size_bytes"`
	MemorySizeBytes          uint64         `json:"memory_size_bytes"`
	CPU                      CPUInformation `json:"cpu"`
	AvailableDiskBytes       uint64         `json:"available_disk_bytes"`
	AvailableMemoryBytes     uint64         `json:"available_memory_bytes"`
	AverageCPUUtilizationPct float64        `json:"average_cpu_utilization_pct"`
}

type CPUInformation struct {
	Model         string `json:"model"`
	PhysicalCores int    `json:"physical_cores"`
	LogicalCores  int    `json:"logical_cores"`
}

type GPUInformation struct {
	GPUModel       string  `json:"gpu_model"`
	Company        string  `json:"company"`
	VRAMSizeBytes  uint64  `json:"vram_size_bytes"`
	UtilizationPct float64 `json:"utilization_pct"`
}

type VLLMInformation struct {
	VLLMVersion           string `json:"vllm_version"`
	ConfigurationLocation string `json:"configuration_location"`
	InstallationType      string `json:"installation_type"`
}

type CollectedMetricPoint struct {
	TimeLabel string             `json:"time_label"`
	Metrics   map[string]float64 `json:"metrics"`
}

type FeatureSummary struct {
	SnapshotCount                  int     `json:"snapshot_count,omitempty"`
	IntervalSeconds                float64 `json:"interval_seconds,omitempty"`
	ModelName                      string  `json:"model_name,omitempty"`
	MultimodalLikely               bool    `json:"multimodal_likely,omitempty"`
	MMPreprocessorCacheDisabled    bool    `json:"mm_preprocessor_cache_disabled,omitempty"`
	MMProcessorCacheGB             float64 `json:"mm_processor_cache_gb,omitempty"`
	TrafficObserved                bool    `json:"traffic_observed,omitempty"`
	EnoughLatencySamples           bool    `json:"enough_latency_samples,omitempty"`
	EnoughKVCacheSamples           bool    `json:"enough_kv_cache_samples,omitempty"`
	AvgGPUComputeLoadPct           float64 `json:"avg_gpu_compute_load_pct,omitempty"`
	ComputeLoadSource              string  `json:"compute_load_source,omitempty"`
	AvgGPUMemoryBandwidthLoadPct   float64 `json:"avg_gpu_memory_bandwidth_load_pct,omitempty"`
	MemoryBandwidthLoadAvailable   bool    `json:"memory_bandwidth_load_available,omitempty"`
	AvgGPUTensorLoadPct            float64 `json:"avg_gpu_tensor_load_pct,omitempty"`
	TensorLoadAvailable            bool    `json:"tensor_load_available,omitempty"`
	SaturationSource               string  `json:"saturation_source,omitempty"`
	RealSaturationMetricsAvailable bool    `json:"real_saturation_metrics_available,omitempty"`
	AvgGPUUtilizationPct           float64 `json:"avg_gpu_utilization_pct,omitempty"`
	MaxGPUUtilizationPct           float64 `json:"max_gpu_utilization_pct,omitempty"`
	AvgRequestsRunning             float64 `json:"avg_requests_running,omitempty"`
	MaxRequestsRunning             float64 `json:"max_requests_running,omitempty"`
	AvgRequestsWaiting             float64 `json:"avg_requests_waiting,omitempty"`
	MaxRequestsWaiting             float64 `json:"max_requests_waiting,omitempty"`
	AvgKVCacheUsagePct             float64 `json:"avg_kv_cache_usage_pct,omitempty"`
	MaxKVCacheUsagePct             float64 `json:"max_kv_cache_usage_pct,omitempty"`
	AvgTTFTSeconds                 float64 `json:"avg_ttft_seconds,omitempty"`
	TTFTCountDelta                 float64 `json:"ttft_count_delta,omitempty"`
	AvgQueueTimeSeconds            float64 `json:"avg_queue_time_seconds,omitempty"`
	QueueTimeCountDelta            float64 `json:"queue_time_count_delta,omitempty"`
	AvgRequestLatencySeconds       float64 `json:"avg_request_latency_seconds,omitempty"`
	RequestLatencyCountDelta       float64 `json:"request_latency_count_delta,omitempty"`
	AvgPrefillTimeSeconds          float64 `json:"avg_prefill_time_seconds,omitempty"`
	PrefillCountDelta              float64 `json:"prefill_count_delta,omitempty"`
	AvgDecodeTimeSeconds           float64 `json:"avg_decode_time_seconds,omitempty"`
	DecodeCountDelta               float64 `json:"decode_count_delta,omitempty"`
	RequestSuccessDelta            float64 `json:"request_success_delta,omitempty"`
	PromptTokensDelta              float64 `json:"prompt_tokens_delta,omitempty"`
	GenerationTokensDelta          float64 `json:"generation_tokens_delta,omitempty"`
	PreemptionsDelta               float64 `json:"preemptions_delta,omitempty"`
	PrefixCacheQueriesDelta        float64 `json:"prefix_cache_queries_delta,omitempty"`
	PrefixCacheHitsDelta           float64 `json:"prefix_cache_hits_delta,omitempty"`
	MMCacheQueriesDelta            float64 `json:"mm_cache_queries_delta,omitempty"`
	MMCacheHitsDelta               float64 `json:"mm_cache_hits_delta,omitempty"`
	PromptTokensCachedDelta        float64 `json:"prompt_tokens_cached_delta,omitempty"`
	PromptTokensRecomputedDelta    float64 `json:"prompt_tokens_recomputed_delta,omitempty"`
	GPUFBUsedBytesAvg              float64 `json:"gpu_fb_used_bytes_avg,omitempty"`
	GPUFBFreeBytesAvg              float64 `json:"gpu_fb_free_bytes_avg,omitempty"`
	GPUFBUsagePctAvg               float64 `json:"gpu_fb_usage_pct_avg,omitempty"`
	XIDErrorsDelta                 float64 `json:"xid_errors_delta,omitempty"`
	AverageCPUUtilizationPct       float64 `json:"average_cpu_utilization_pct,omitempty"`
}

type CurrentLoadSummary struct {
	CurrentSaturationPct            float64 `json:"current_saturation_pct,omitempty"`
	CurrentGPULoadPct               float64 `json:"current_gpu_load_pct,omitempty"`
	CurrentGPULoadEffectiveCount    float64 `json:"current_gpu_load_effective_count,omitempty"`
	TotalGPUCount                   float64 `json:"total_gpu_count,omitempty"`
	DominantGPUResource             string  `json:"dominant_gpu_resource,omitempty"`
	CurrentLoadBottleneck           string  `json:"current_load_bottleneck,omitempty"`
	CurrentLoadBottleneckConfidence float64 `json:"current_load_bottleneck_confidence,omitempty"`
	ComputeLoadPct                  float64 `json:"compute_load_pct,omitempty"`
	ComputeLoadSource               string  `json:"compute_load_source,omitempty"`
	MemoryBandwidthLoadPct          float64 `json:"memory_bandwidth_load_pct,omitempty"`
	MemoryBandwidthLoadAvailable    bool    `json:"memory_bandwidth_load_available,omitempty"`
	TensorLoadPct                   float64 `json:"tensor_load_pct,omitempty"`
	TensorLoadAvailable             bool    `json:"tensor_load_available,omitempty"`
	SaturationSource                string  `json:"saturation_source,omitempty"`
	RealSaturationMetricsAvailable  bool    `json:"real_saturation_metrics_available,omitempty"`
	CPULoadPct                      float64 `json:"cpu_load_pct,omitempty"`
	QueuePressureRatio              float64 `json:"queue_pressure_ratio,omitempty"`
	TargetQueueSize                 float64 `json:"target_queue_size,omitempty"`
}

type ServiceSummary struct {
	RequestRateRPS               *float64                `json:"request_rate_rps"`
	RequestLatencyMS             RequestLatencySummary   `json:"request_latency_ms"`
	Queue                        QueueSummary            `json:"queue"`
	SaturationPct                *float64                `json:"saturation_pct"`
	EstimatedUpperRequestRateRPS *float64                `json:"estimated_upper_request_rate_rps"`
	Bottleneck                   BottleneckSummary       `json:"bottleneck"`
	ObservedMode                 ObservedModeSummary     `json:"observed_mode"`
	ConfiguredIntent             ConfiguredIntentSummary `json:"configured_intent"`
	TopIssue                     string                  `json:"top_issue"`
}

type RequestLatencySummary struct {
	Avg                  *float64 `json:"avg"`
	P50                  *float64 `json:"p50"`
	P90                  *float64 `json:"p90"`
	P99                  *float64 `json:"p99"`
	PercentilesAvailable bool     `json:"percentiles_available"`
}

type QueueSummary struct {
	AvgDelayMS            *float64 `json:"avg_delay_ms"`
	AvgWaitingRequests    *float64 `json:"avg_waiting_requests"`
	MaxWaitingRequests    *float64 `json:"max_waiting_requests"`
	PressureRatio         *float64 `json:"pressure_ratio"`
	TargetWaitingRequests *float64 `json:"target_waiting_requests"`
	Health                string   `json:"health"`
}

type BottleneckSummary struct {
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

type ObservedModeSummary struct {
	Objective      string  `json:"objective"`
	ServingPattern string  `json:"serving_pattern"`
	Confidence     float64 `json:"confidence"`
}

type ConfiguredIntentSummary struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

type AdvancedProfilingInfo struct {
	TargetPID       int                 `json:"target_pid,omitempty"`
	DurationSeconds int                 `json:"duration_seconds"`
	BCC             ProfilingToolResult `json:"bcc"`
	PySpy           ProfilingToolResult `json:"py_spy"`
	NSys            ProfilingToolResult `json:"nsys"`
}

type ProfilingToolResult struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Binary    string `json:"binary,omitempty"`
	Command   string `json:"command,omitempty"`
	Output    string `json:"output,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Error     string `json:"error,omitempty"`
}
