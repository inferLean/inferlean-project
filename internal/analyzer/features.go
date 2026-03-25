package analyzer

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	computeLoadSourceDCGMSMActive      = "dcgm_sm_active"
	computeLoadSourceDCGMSMOccupancy   = "dcgm_sm_occupancy"
	computeLoadSourceDCGMGREngine      = "dcgm_gr_engine_active"
	computeLoadSourceDCGMPipeActiveMax = "dcgm_pipe_active_max"
	computeLoadSourceGPUUtilProxy      = "gpu_utilization_proxy"

	memoryBandwidthLoadSourceDCGMDRAMActive  = "dcgm_dram_active"
	memoryBandwidthLoadSourceDCGMMemCopyUtil = "dcgm_mem_copy_util"

	saturationSourceMeasured    = "measured"
	saturationSourceApproximate = "approximate"
)

func ExtractFeatures(report *model.AnalysisReport) FeatureSet {
	features := FeatureSet{
		Metrics: map[string]MetricSummary{},
	}
	if report == nil {
		return features
	}
	if report.FeatureSummary != nil && len(report.CollectedMetrics) == 0 {
		return featureSetFromSummary(*report.FeatureSummary)
	}

	features.SnapshotCount = len(report.CollectedMetrics)
	features.IntervalSeconds = metricIntervalSeconds(report.CollectedMetrics)
	features.AverageCPUUtilizationPct = report.OSInformation.AverageCPUUtilizationPct
	features.Metrics = summarizeCollectedMetrics(report.CollectedMetrics)
	if features.AverageCPUUtilizationPct <= 0 {
		if cpuUtil, ok := metricAveragePct(features.Metrics, "node_cpu_utilization_pct"); ok {
			features.AverageCPUUtilizationPct = cpuUtil
		}
	}
	config := flattenMap(report.CurrentVLLMConfigurations)
	features.ModelName = lookupString(config, "model_name", "model", "served_model_name")
	features.MultimodalLikely = containsAnyFold(features.ModelName, "vl", "vision", "llava", "pixtral", "internvl")
	features.MultimodalConfigPresent = hasMultimodalConfig(config)
	if raw, ok := lookupAny(config, "language_model_only"); ok {
		if enabled, ok := coerceBool(raw); ok {
			features.LanguageModelOnlyEnabled = enabled
		}
	}
	if raw, ok := lookupAny(config, "disable_mm_preprocessor_cache"); ok {
		if disabled, ok := coerceBool(raw); ok {
			features.MMPreprocessorCacheDisabled = disabled
		}
	}
	if value, ok := lookupAny(config, "mm_processor_cache_gb"); ok {
		if cacheGB, ok := coerceFloat(value); ok {
			features.MMProcessorCacheGB = cacheGB
		}
	}

	gpuUtil := pickMetric(features.Metrics, "gpu_utilization_pct", "DCGM_FI_DEV_GPU_UTIL")
	if gpuUtil.SampleCount > 0 {
		features.AvgGPUUtilizationPct = gpuUtil.Average
		features.MaxGPUUtilizationPct = gpuUtil.Max
	} else {
		features.AvgGPUUtilizationPct = report.GPUInformation.UtilizationPct
		features.MaxGPUUtilizationPct = report.GPUInformation.UtilizationPct
	}
	if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_PROF_SM_ACTIVE"); ok {
		features.AvgGPUComputeLoadPct = loadPct
		features.ComputeLoadSource = computeLoadSourceDCGMSMActive
	} else if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_PROF_SM_OCCUPANCY"); ok {
		features.AvgGPUComputeLoadPct = loadPct
		features.ComputeLoadSource = computeLoadSourceDCGMSMOccupancy
	} else if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_PROF_GR_ENGINE_ACTIVE"); ok {
		features.AvgGPUComputeLoadPct = loadPct
		features.ComputeLoadSource = computeLoadSourceDCGMGREngine
	} else if loadPct, ok := maxMetricAveragePct(
		features.Metrics,
		"DCGM_FI_PROF_PIPE_TENSOR_ACTIVE",
		"DCGM_FI_PROF_PIPE_FP16_ACTIVE",
		"DCGM_FI_PROF_PIPE_FP32_ACTIVE",
		"DCGM_FI_PROF_PIPE_FP64_ACTIVE",
	); ok {
		features.AvgGPUComputeLoadPct = loadPct
		features.ComputeLoadSource = computeLoadSourceDCGMPipeActiveMax
	} else {
		features.AvgGPUComputeLoadPct = features.AvgGPUUtilizationPct
		features.ComputeLoadSource = computeLoadSourceGPUUtilProxy
	}
	if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_PROF_DRAM_ACTIVE"); ok {
		features.AvgGPUMemoryBandwidthLoadPct = loadPct
		features.MemoryBandwidthLoadSource = memoryBandwidthLoadSourceDCGMDRAMActive
		features.MemoryBandwidthLoadAvailable = true
	} else if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_DEV_MEM_COPY_UTIL"); ok {
		features.AvgGPUMemoryBandwidthLoadPct = loadPct
		features.MemoryBandwidthLoadSource = memoryBandwidthLoadSourceDCGMMemCopyUtil
		features.MemoryBandwidthLoadAvailable = true
	}
	if loadPct, ok := metricAveragePct(features.Metrics, "DCGM_FI_PROF_PIPE_TENSOR_ACTIVE"); ok {
		features.AvgGPUTensorLoadPct = loadPct
		features.TensorLoadAvailable = true
	}

	running := pickMetric(features.Metrics, "vllm:num_requests_running")
	features.AvgRequestsRunning = running.Average
	features.MaxRequestsRunning = running.Max

	waiting := pickMetric(features.Metrics, "vllm:num_requests_waiting")
	features.AvgRequestsWaiting = waiting.Average
	features.MaxRequestsWaiting = waiting.Max

	kvCache := pickMetric(features.Metrics, "vllm:kv_cache_usage_perc")
	features.AvgKVCacheUsagePct = kvCache.Average
	features.MaxKVCacheUsagePct = kvCache.Max
	features.EnoughKVCacheSamples = kvCache.SampleCount >= 2

	ttftSum := pickMetric(features.Metrics, "vllm:time_to_first_token_seconds_sum")
	ttftCount := pickMetric(features.Metrics, "vllm:time_to_first_token_seconds_count")
	features.TTFTCountDelta = positiveDelta(ttftCount.Delta)
	features.AvgTTFTSeconds = averageFromCounters(ttftSum.Delta, ttftCount.Delta)

	queueSum := pickMetric(features.Metrics, "vllm:request_queue_time_seconds_sum")
	queueCount := pickMetric(features.Metrics, "vllm:request_queue_time_seconds_count")
	features.QueueTimeCountDelta = positiveDelta(queueCount.Delta)
	features.AvgQueueTimeSeconds = averageFromCounters(queueSum.Delta, queueCount.Delta)

	requestLatencySum := pickMetric(features.Metrics, "vllm:e2e_request_latency_seconds_sum")
	requestLatencyCount := pickMetric(features.Metrics, "vllm:e2e_request_latency_seconds_count")
	features.RequestLatencyCountDelta = positiveDelta(requestLatencyCount.Delta)
	features.AvgRequestLatencySeconds = averageFromCounters(requestLatencySum.Delta, requestLatencyCount.Delta)

	prefillSum := pickMetric(features.Metrics, "vllm:request_prefill_time_seconds_sum")
	prefillCount := pickMetric(features.Metrics, "vllm:request_prefill_time_seconds_count")
	features.PrefillCountDelta = positiveDelta(prefillCount.Delta)
	features.AvgPrefillTimeSeconds = averageFromCounters(prefillSum.Delta, prefillCount.Delta)

	decodeSum := pickMetric(features.Metrics, "vllm:request_decode_time_seconds_sum")
	decodeCount := pickMetric(features.Metrics, "vllm:request_decode_time_seconds_count")
	features.DecodeCountDelta = positiveDelta(decodeCount.Delta)
	features.AvgDecodeTimeSeconds = averageFromCounters(decodeSum.Delta, decodeCount.Delta)

	features.RequestSuccessDelta = positiveDelta(pickMetric(features.Metrics, "vllm:request_success_total").Delta)
	features.PromptTokensDelta = positiveDelta(pickMetric(features.Metrics, "vllm:prompt_tokens_total").Delta)
	features.GenerationTokensDelta = positiveDelta(pickMetric(features.Metrics, "vllm:generation_tokens_total").Delta)
	features.PreemptionsDelta = positiveDelta(pickMetric(features.Metrics, "vllm:num_preemptions_total").Delta)
	features.PrefixCacheQueriesDelta = positiveDelta(pickMetric(features.Metrics, "vllm:prefix_cache_queries_total").Delta)
	features.PrefixCacheHitsDelta = positiveDelta(pickMetric(features.Metrics, "vllm:prefix_cache_hits_total").Delta)
	features.MMCacheQueriesDelta = positiveDelta(pickMetric(features.Metrics, "vllm:mm_cache_queries", "vllm:mm_cache_queries_total").Delta)
	features.MMCacheHitsDelta = positiveDelta(pickMetric(features.Metrics, "vllm:mm_cache_hits", "vllm:mm_cache_hits_total").Delta)
	features.PromptTokensCachedDelta = positiveDelta(pickMetric(features.Metrics, "vllm:prompt_tokens_cached_total").Delta)
	features.PromptTokensRecomputedDelta = positiveDelta(pickMetric(features.Metrics, "vllm:prompt_tokens_recomputed_total").Delta)
	features.GPUFBUsedBytesAvg = pickMetric(features.Metrics, "gpu_fb_used_bytes", "DCGM_FI_DEV_FB_USED").Average
	features.GPUFBFreeBytesAvg = pickMetric(features.Metrics, "gpu_fb_free_bytes", "DCGM_FI_DEV_FB_FREE").Average
	if total := features.GPUFBUsedBytesAvg + features.GPUFBFreeBytesAvg; total > 0 {
		features.GPUFBUsagePctAvg = (features.GPUFBUsedBytesAvg / total) * 100
	}
	features.XIDErrorsDelta = positiveDelta(pickMetric(features.Metrics, "DCGM_FI_DEV_XID_ERRORS").Delta)

	features.TrafficObserved = features.RequestSuccessDelta > 0 ||
		features.PromptTokensDelta > 0 ||
		features.GenerationTokensDelta > 0 ||
		features.MaxRequestsRunning > 0 ||
		features.MaxRequestsWaiting > 0
	features.EnoughLatencySamples = features.TrafficObserved &&
		features.TTFTCountDelta >= 5 &&
		features.QueueTimeCountDelta >= 5
	features.RealSaturationMetricsAvailable = hasRealSaturationMetrics(features)
	features.SaturationSource = saturationSource(features)

	return features
}

func SummarizeFeatures(report *model.AnalysisReport) *model.FeatureSummary {
	if report == nil {
		return nil
	}
	summary := featureSummaryFromSet(ExtractFeatures(report))
	return &summary
}

func summarizeCollectedMetrics(points []model.CollectedMetricPoint) map[string]MetricSummary {
	perMetric := map[string][]float64{}

	for _, point := range points {
		type aggregate struct {
			sum        float64
			valueCount int
			counter    bool
		}
		perPoint := map[string]aggregate{}
		for metricName, rawValue := range point.Metrics {
			base := metricBaseName(metricName)
			if base == "" {
				continue
			}
			value := normalizeMetricValue(base, rawValue)
			agg := perPoint[base]
			agg.sum += value
			agg.valueCount++
			agg.counter = isCounterMetric(base)
			perPoint[base] = agg
		}
		for base, agg := range perPoint {
			value := agg.sum
			if !agg.counter && agg.valueCount > 0 {
				value = agg.sum / float64(agg.valueCount)
			}
			perMetric[base] = append(perMetric[base], value)
		}
	}

	out := make(map[string]MetricSummary, len(perMetric))
	for metricName, samples := range perMetric {
		if len(samples) == 0 {
			continue
		}
		minValue := samples[0]
		maxValue := samples[0]
		sum := 0.0
		for _, sample := range samples {
			sum += sample
			minValue = math.Min(minValue, sample)
			maxValue = math.Max(maxValue, sample)
		}
		out[metricName] = MetricSummary{
			Name:        metricName,
			Samples:     append([]float64(nil), samples...),
			SampleCount: len(samples),
			First:       samples[0],
			Last:        samples[len(samples)-1],
			Average:     sum / float64(len(samples)),
			Min:         minValue,
			Max:         maxValue,
			Delta:       samples[len(samples)-1] - samples[0],
		}
	}
	return out
}

func metricIntervalSeconds(points []model.CollectedMetricPoint) float64 {
	if len(points) < 2 {
		return 0
	}
	var first time.Time
	var last time.Time
	for _, point := range points {
		parsed, err := time.Parse(time.RFC3339, point.TimeLabel)
		if err != nil {
			continue
		}
		if first.IsZero() || parsed.Before(first) {
			first = parsed
		}
		if last.IsZero() || parsed.After(last) {
			last = parsed
		}
	}
	if first.IsZero() || last.IsZero() || !last.After(first) {
		return 0
	}
	return last.Sub(first).Seconds()
}

func metricBaseName(metricName string) string {
	metricName = strings.TrimSpace(metricName)
	if idx := strings.Index(metricName, "{"); idx >= 0 {
		return metricName[:idx]
	}
	return metricName
}

func normalizeMetricValue(metricName string, value float64) float64 {
	lower := strings.ToLower(metricName)
	switch {
	case strings.Contains(lower, "_pct"),
		strings.Contains(lower, "_perc"),
		strings.Contains(lower, "gpu_util"),
		strings.Contains(lower, "cpu_util"),
		strings.HasSuffix(lower, "_util"):
		return normalizePercentOrRatio(value)
	default:
		return value
	}
}

func isCounterMetric(metricName string) bool {
	lower := strings.ToLower(metricName)
	if strings.HasSuffix(lower, "_total") || strings.HasSuffix(lower, "_count") || strings.HasSuffix(lower, "_sum") {
		return true
	}
	switch metricName {
	case "DCGM_FI_DEV_XID_ERRORS",
		"DCGM_FI_DEV_PCIE_REPLAY_COUNTER",
		"DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION",
		"vllm:mm_cache_queries",
		"vllm:mm_cache_hits":
		return true
	default:
		return false
	}
}

func pickMetric(metrics map[string]MetricSummary, metricNames ...string) MetricSummary {
	for _, metricName := range metricNames {
		if summary, ok := metrics[metricName]; ok {
			return summary
		}
	}
	return MetricSummary{}
}

func averageFromCounters(sumDelta, countDelta float64) float64 {
	if countDelta <= 0 {
		return 0
	}
	return positiveDelta(sumDelta) / countDelta
}

func metricAveragePct(metrics map[string]MetricSummary, metricNames ...string) (float64, bool) {
	summary := pickMetric(metrics, metricNames...)
	if summary.SampleCount == 0 {
		return 0, false
	}
	return clampFloat(normalizePercentOrRatio(summary.Average), 0, 100), true
}

func positiveDelta(delta float64) float64 {
	if delta < 0 {
		return 0
	}
	return delta
}

func hasMultimodalConfig(config map[string]any) bool {
	if len(config) == 0 {
		return false
	}
	for key, value := range config {
		trimmedKey := strings.TrimSpace(strings.ToLower(key))
		switch {
		case strings.HasPrefix(trimmedKey, "mm_"),
			trimmedKey == "disable_mm_preprocessor_cache",
			trimmedKey == "limit_mm_per_prompt",
			trimmedKey == "media_io_kwargs",
			trimmedKey == "interleave_mm_strings":
			return true
		case trimmedKey == "language_model_only":
			if enabled, ok := coerceBool(value); ok && !enabled {
				return true
			}
		}
	}
	return false
}

func featureSummaryFromSet(features FeatureSet) model.FeatureSummary {
	return model.FeatureSummary{
		SnapshotCount:                  features.SnapshotCount,
		IntervalSeconds:                features.IntervalSeconds,
		ModelName:                      features.ModelName,
		MultimodalLikely:               features.MultimodalLikely,
		MMPreprocessorCacheDisabled:    features.MMPreprocessorCacheDisabled,
		MMProcessorCacheGB:             features.MMProcessorCacheGB,
		TrafficObserved:                features.TrafficObserved,
		EnoughLatencySamples:           features.EnoughLatencySamples,
		EnoughKVCacheSamples:           features.EnoughKVCacheSamples,
		AvgGPUComputeLoadPct:           features.AvgGPUComputeLoadPct,
		ComputeLoadSource:              features.ComputeLoadSource,
		AvgGPUMemoryBandwidthLoadPct:   features.AvgGPUMemoryBandwidthLoadPct,
		MemoryBandwidthLoadSource:      features.MemoryBandwidthLoadSource,
		MemoryBandwidthLoadAvailable:   features.MemoryBandwidthLoadAvailable,
		AvgGPUTensorLoadPct:            features.AvgGPUTensorLoadPct,
		TensorLoadAvailable:            features.TensorLoadAvailable,
		SaturationSource:               features.SaturationSource,
		RealSaturationMetricsAvailable: features.RealSaturationMetricsAvailable,
		AvgGPUUtilizationPct:           features.AvgGPUUtilizationPct,
		MaxGPUUtilizationPct:           features.MaxGPUUtilizationPct,
		AvgRequestsRunning:             features.AvgRequestsRunning,
		MaxRequestsRunning:             features.MaxRequestsRunning,
		AvgRequestsWaiting:             features.AvgRequestsWaiting,
		MaxRequestsWaiting:             features.MaxRequestsWaiting,
		AvgKVCacheUsagePct:             features.AvgKVCacheUsagePct,
		MaxKVCacheUsagePct:             features.MaxKVCacheUsagePct,
		AvgTTFTSeconds:                 features.AvgTTFTSeconds,
		TTFTCountDelta:                 features.TTFTCountDelta,
		AvgQueueTimeSeconds:            features.AvgQueueTimeSeconds,
		QueueTimeCountDelta:            features.QueueTimeCountDelta,
		AvgRequestLatencySeconds:       features.AvgRequestLatencySeconds,
		RequestLatencyCountDelta:       features.RequestLatencyCountDelta,
		AvgPrefillTimeSeconds:          features.AvgPrefillTimeSeconds,
		PrefillCountDelta:              features.PrefillCountDelta,
		AvgDecodeTimeSeconds:           features.AvgDecodeTimeSeconds,
		DecodeCountDelta:               features.DecodeCountDelta,
		RequestSuccessDelta:            features.RequestSuccessDelta,
		PromptTokensDelta:              features.PromptTokensDelta,
		GenerationTokensDelta:          features.GenerationTokensDelta,
		PreemptionsDelta:               features.PreemptionsDelta,
		PrefixCacheQueriesDelta:        features.PrefixCacheQueriesDelta,
		PrefixCacheHitsDelta:           features.PrefixCacheHitsDelta,
		MMCacheQueriesDelta:            features.MMCacheQueriesDelta,
		MMCacheHitsDelta:               features.MMCacheHitsDelta,
		PromptTokensCachedDelta:        features.PromptTokensCachedDelta,
		PromptTokensRecomputedDelta:    features.PromptTokensRecomputedDelta,
		GPUFBUsedBytesAvg:              features.GPUFBUsedBytesAvg,
		GPUFBFreeBytesAvg:              features.GPUFBFreeBytesAvg,
		GPUFBUsagePctAvg:               features.GPUFBUsagePctAvg,
		XIDErrorsDelta:                 features.XIDErrorsDelta,
		AverageCPUUtilizationPct:       features.AverageCPUUtilizationPct,
	}
}

func featureSetFromSummary(summary model.FeatureSummary) FeatureSet {
	return FeatureSet{
		Metrics:                        map[string]MetricSummary{},
		SnapshotCount:                  summary.SnapshotCount,
		IntervalSeconds:                summary.IntervalSeconds,
		ModelName:                      summary.ModelName,
		MultimodalLikely:               summary.MultimodalLikely,
		MMPreprocessorCacheDisabled:    summary.MMPreprocessorCacheDisabled,
		MMProcessorCacheGB:             summary.MMProcessorCacheGB,
		TrafficObserved:                summary.TrafficObserved,
		EnoughLatencySamples:           summary.EnoughLatencySamples,
		EnoughKVCacheSamples:           summary.EnoughKVCacheSamples,
		AvgGPUComputeLoadPct:           summary.AvgGPUComputeLoadPct,
		ComputeLoadSource:              summary.ComputeLoadSource,
		AvgGPUMemoryBandwidthLoadPct:   summary.AvgGPUMemoryBandwidthLoadPct,
		MemoryBandwidthLoadSource:      summary.MemoryBandwidthLoadSource,
		MemoryBandwidthLoadAvailable:   summary.MemoryBandwidthLoadAvailable,
		AvgGPUTensorLoadPct:            summary.AvgGPUTensorLoadPct,
		TensorLoadAvailable:            summary.TensorLoadAvailable,
		SaturationSource:               summary.SaturationSource,
		RealSaturationMetricsAvailable: summary.RealSaturationMetricsAvailable,
		AvgGPUUtilizationPct:           summary.AvgGPUUtilizationPct,
		MaxGPUUtilizationPct:           summary.MaxGPUUtilizationPct,
		AvgRequestsRunning:             summary.AvgRequestsRunning,
		MaxRequestsRunning:             summary.MaxRequestsRunning,
		AvgRequestsWaiting:             summary.AvgRequestsWaiting,
		MaxRequestsWaiting:             summary.MaxRequestsWaiting,
		AvgKVCacheUsagePct:             summary.AvgKVCacheUsagePct,
		MaxKVCacheUsagePct:             summary.MaxKVCacheUsagePct,
		AvgTTFTSeconds:                 summary.AvgTTFTSeconds,
		TTFTCountDelta:                 summary.TTFTCountDelta,
		AvgQueueTimeSeconds:            summary.AvgQueueTimeSeconds,
		QueueTimeCountDelta:            summary.QueueTimeCountDelta,
		AvgRequestLatencySeconds:       summary.AvgRequestLatencySeconds,
		RequestLatencyCountDelta:       summary.RequestLatencyCountDelta,
		AvgPrefillTimeSeconds:          summary.AvgPrefillTimeSeconds,
		PrefillCountDelta:              summary.PrefillCountDelta,
		AvgDecodeTimeSeconds:           summary.AvgDecodeTimeSeconds,
		DecodeCountDelta:               summary.DecodeCountDelta,
		RequestSuccessDelta:            summary.RequestSuccessDelta,
		PromptTokensDelta:              summary.PromptTokensDelta,
		GenerationTokensDelta:          summary.GenerationTokensDelta,
		PreemptionsDelta:               summary.PreemptionsDelta,
		PrefixCacheQueriesDelta:        summary.PrefixCacheQueriesDelta,
		PrefixCacheHitsDelta:           summary.PrefixCacheHitsDelta,
		MMCacheQueriesDelta:            summary.MMCacheQueriesDelta,
		MMCacheHitsDelta:               summary.MMCacheHitsDelta,
		PromptTokensCachedDelta:        summary.PromptTokensCachedDelta,
		PromptTokensRecomputedDelta:    summary.PromptTokensRecomputedDelta,
		GPUFBUsedBytesAvg:              summary.GPUFBUsedBytesAvg,
		GPUFBFreeBytesAvg:              summary.GPUFBFreeBytesAvg,
		GPUFBUsagePctAvg:               summary.GPUFBUsagePctAvg,
		XIDErrorsDelta:                 summary.XIDErrorsDelta,
		AverageCPUUtilizationPct:       summary.AverageCPUUtilizationPct,
	}
}

func hasMeasuredComputeLoad(features FeatureSet) bool {
	switch strings.TrimSpace(features.ComputeLoadSource) {
	case computeLoadSourceDCGMSMActive, computeLoadSourceDCGMSMOccupancy, computeLoadSourceDCGMGREngine, computeLoadSourceDCGMPipeActiveMax:
		return true
	default:
		return false
	}
}

func hasMeasuredMemoryBandwidthLoad(features FeatureSet) bool {
	return strings.TrimSpace(features.MemoryBandwidthLoadSource) == memoryBandwidthLoadSourceDCGMDRAMActive
}

func hasApproximateSaturationSignals(features FeatureSet) bool {
	return strings.TrimSpace(features.ComputeLoadSource) == computeLoadSourceGPUUtilProxy ||
		strings.TrimSpace(features.MemoryBandwidthLoadSource) == memoryBandwidthLoadSourceDCGMMemCopyUtil
}

func hasRealSaturationMetrics(features FeatureSet) bool {
	return hasMeasuredComputeLoad(features) || hasMeasuredMemoryBandwidthLoad(features) || features.TensorLoadAvailable
}

func saturationSource(features FeatureSet) string {
	if hasRealSaturationMetrics(features) {
		return saturationSourceMeasured
	}
	if hasApproximateSaturationSignals(features) {
		return saturationSourceApproximate
	}
	return ""
}

func maxMetricAveragePct(metrics map[string]MetricSummary, names ...string) (float64, bool) {
	best := 0.0
	found := false
	for _, name := range names {
		if value, ok := metricAveragePct(metrics, name); ok {
			if !found || value > best {
				best = value
			}
			found = true
		}
	}
	return best, found
}

func requestLatencyPercentilesMS(report *model.AnalysisReport) (p50MS, p90MS, p99MS *float64, available bool) {
	if report == nil || len(report.CollectedMetrics) < 2 {
		return nil, nil, nil, false
	}
	buckets := histogramBucketDeltas(report.CollectedMetrics, "vllm:e2e_request_latency_seconds_bucket")
	if len(buckets) == 0 {
		return nil, nil, nil, false
	}
	p50, ok50 := histogramQuantile(0.50, buckets)
	p90, ok90 := histogramQuantile(0.90, buckets)
	p99, ok99 := histogramQuantile(0.99, buckets)
	if ok50 {
		value := p50 * 1000
		p50MS = &value
	}
	if ok90 {
		value := p90 * 1000
		p90MS = &value
	}
	if ok99 {
		value := p99 * 1000
		p99MS = &value
	}
	return p50MS, p90MS, p99MS, ok50 || ok90 || ok99
}

func histogramBucketDeltas(points []model.CollectedMetricPoint, metricBase string) map[float64]float64 {
	if len(points) < 2 {
		return nil
	}
	ordered := append([]model.CollectedMetricPoint(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].TimeLabel < ordered[j].TimeLabel
	})

	perLE := map[float64][]float64{}
	for _, point := range ordered {
		perPoint := map[float64]float64{}
		for metricName, value := range point.Metrics {
			if metricBaseName(metricName) != metricBase {
				continue
			}
			le, ok := promLabelValue(metricName, "le")
			if !ok {
				continue
			}
			upperBound, ok := parsePromLE(le)
			if !ok {
				continue
			}
			perPoint[upperBound] += value
		}
		for upperBound, total := range perPoint {
			perLE[upperBound] = append(perLE[upperBound], total)
		}
	}

	deltas := map[float64]float64{}
	for upperBound, samples := range perLE {
		if len(samples) < 2 {
			continue
		}
		delta := positiveDelta(samples[len(samples)-1] - samples[0])
		if delta <= 0 {
			continue
		}
		deltas[upperBound] = delta
	}
	return deltas
}

func histogramQuantile(q float64, buckets map[float64]float64) (float64, bool) {
	if q <= 0 || q > 1 || len(buckets) == 0 {
		return 0, false
	}
	bounds := make([]float64, 0, len(buckets))
	for upperBound := range buckets {
		bounds = append(bounds, upperBound)
	}
	sort.Float64s(bounds)

	total := buckets[bounds[len(bounds)-1]]
	if total <= 0 {
		return 0, false
	}
	target := total * q
	prevCount := 0.0
	prevBound := 0.0
	for _, upperBound := range bounds {
		count := buckets[upperBound]
		if count < target {
			prevCount = count
			prevBound = upperBound
			continue
		}
		if math.IsInf(upperBound, 1) {
			return prevBound, true
		}
		if count <= prevCount {
			return upperBound, true
		}
		fraction := (target - prevCount) / (count - prevCount)
		if fraction < 0 {
			fraction = 0
		}
		if fraction > 1 {
			fraction = 1
		}
		return prevBound + ((upperBound - prevBound) * fraction), true
	}
	return 0, false
}

func promLabelValue(metricName, label string) (string, bool) {
	start := strings.Index(metricName, "{")
	end := strings.LastIndex(metricName, "}")
	if start < 0 || end <= start {
		return "", false
	}
	labels := strings.Split(metricName[start+1:end], ",")
	prefix := label + "="
	for _, item := range labels {
		item = strings.TrimSpace(item)
		if !strings.HasPrefix(item, prefix) {
			continue
		}
		value := strings.TrimPrefix(item, prefix)
		value = strings.Trim(value, `"`)
		return value, true
	}
	return "", false
}

func parsePromLE(value string) (float64, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	switch strings.ToLower(trimmed) {
	case "+inf", "inf":
		return math.Inf(1), true
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(parsed) {
		return 0, false
	}
	return parsed, true
}
