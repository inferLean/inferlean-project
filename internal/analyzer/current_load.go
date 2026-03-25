package analyzer

import (
	"fmt"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	dominantGPUResourceUnknown         = "unknown"
	dominantGPUResourceCompute         = "compute"
	dominantGPUResourceMemoryBandwidth = "memory_bandwidth"
	dominantGPUResourceTensor          = "tensor"

	currentLoadBottleneckUnknown    = "unknown"
	currentLoadBottleneckGPUCompute = "gpu_compute_bound"
	currentLoadBottleneckGPUMemory  = "gpu_memory_bound"
	currentLoadBottleneckCPU        = "cpu_bound"
	currentLoadBottleneckMixed      = "mixed"
)

func InferTotalGPUCount(report *model.AnalysisReport) float64 {
	if report == nil {
		return 1
	}

	cfg := collectNumericConfig(report.CurrentVLLMConfigurations)
	tensorParallel := lookupPositive(cfg, "tensor_parallel_size", "tensorparallelsize")
	pipelineParallel := lookupPositive(cfg, "pipeline_parallel_size", "pipelineparallelsize")
	dataParallel := lookupPositive(cfg, "data_parallel_size", "dataparallelsize")
	if tensorParallel <= 0 {
		tensorParallel = 1
	}
	if pipelineParallel <= 0 {
		pipelineParallel = 1
	}
	if dataParallel <= 0 {
		dataParallel = 1
	}

	explicitCount := lookupPositive(cfg, "gpu_count", "num_gpus", "ngpus", "gpucount")
	if explicitCount > 0 {
		return explicitCount
	}

	composed := tensorParallel * pipelineParallel * dataParallel
	if composed > 0 {
		return composed
	}
	return 1
}

func buildCurrentLoadSummary(report *model.AnalysisReport, intent WorkloadIntent) *model.CurrentLoadSummary {
	if report == nil {
		return nil
	}
	if report.AnalysisSummary != nil && strings.TrimSpace(report.AnalysisSummary.WorkloadIntent) != "" {
		intent = WorkloadIntent(report.AnalysisSummary.WorkloadIntent)
	}

	features := ExtractFeatures(report)
	targetQueueSize := targetQueueSizeForIntent(intent)
	queuePressureRatio := 0.0
	if targetQueueSize > 0 {
		queuePressureRatio = features.AvgRequestsWaiting / targetQueueSize
	}

	currentSaturationPct := maxFloatN(
		features.AvgGPUComputeLoadPct,
		features.AvgGPUMemoryBandwidthLoadPct,
		features.AvgGPUTensorLoadPct,
	)
	totalGPUCount := InferTotalGPUCount(report)
	currentGPULoadPct := clampFloat(features.AvgGPUUtilizationPct, 0, 100)

	bottleneck, confidence := classifyCurrentLoadBottleneck(features, currentLoadFindings(report))

	return &model.CurrentLoadSummary{
		CurrentSaturationPct:            clampFloat(currentSaturationPct, 0, 100),
		CurrentGPULoadPct:               currentGPULoadPct,
		CurrentGPULoadEffectiveCount:    totalGPUCount * (currentGPULoadPct / 100),
		TotalGPUCount:                   totalGPUCount,
		DominantGPUResource:             dominantGPUResource(features),
		CurrentLoadBottleneck:           bottleneck,
		CurrentLoadBottleneckConfidence: confidence,
		ComputeLoadPct:                  clampFloat(features.AvgGPUComputeLoadPct, 0, 100),
		ComputeLoadSource:               strings.TrimSpace(features.ComputeLoadSource),
		MemoryBandwidthLoadPct:          clampFloat(features.AvgGPUMemoryBandwidthLoadPct, 0, 100),
		MemoryBandwidthLoadSource:       strings.TrimSpace(features.MemoryBandwidthLoadSource),
		MemoryBandwidthLoadAvailable:    features.MemoryBandwidthLoadAvailable,
		TensorLoadPct:                   clampFloat(features.AvgGPUTensorLoadPct, 0, 100),
		TensorLoadAvailable:             features.TensorLoadAvailable,
		SaturationSource:                strings.TrimSpace(features.SaturationSource),
		RealSaturationMetricsAvailable:  features.RealSaturationMetricsAvailable,
		CPULoadPct:                      clampFloat(features.AverageCPUUtilizationPct, 0, 100),
		QueuePressureRatio:              queuePressureRatio,
		TargetQueueSize:                 targetQueueSize,
	}
}

func targetQueueSizeForIntent(intent WorkloadIntent) float64 {
	switch normalizeWorkloadIntent(intent) {
	case LatencyFirstIntent:
		return 1
	case ThroughputFirstIntent:
		return 4
	default:
		return 2
	}
}

func dominantGPUResource(features FeatureSet) string {
	compute := clampFloat(features.AvgGPUComputeLoadPct, 0, 100)
	memory := clampFloat(features.AvgGPUMemoryBandwidthLoadPct, 0, 100)
	tensor := clampFloat(features.AvgGPUTensorLoadPct, 0, 100)
	maxLoad := maxFloatN(compute, memory, tensor)
	if maxLoad <= 0 {
		return dominantGPUResourceUnknown
	}
	switch maxLoad {
	case compute:
		return dominantGPUResourceCompute
	case memory:
		return dominantGPUResourceMemoryBandwidth
	case tensor:
		return dominantGPUResourceTensor
	default:
		return dominantGPUResourceUnknown
	}
}

func currentLoadFindings(report *model.AnalysisReport) map[string]model.Finding {
	out := map[string]model.Finding{}
	if report == nil || report.AnalysisSummary == nil {
		return out
	}
	for _, finding := range report.AnalysisSummary.Findings {
		out[finding.ID] = finding
	}
	return out
}

func classifyCurrentLoadBottleneck(features FeatureSet, findings map[string]model.Finding) (string, float64) {
	if finding, ok := findings[detectorCPUOrHostBottleneck]; ok && finding.Status == model.FindingStatusPresent {
		return currentLoadBottleneckCPU, clampFloat(finding.Confidence, 0, 1)
	}

	computeConfidence, computePresent := currentLoadFindingConfidence(findings, detectorThroughputSaturationWithQueuePressure)
	memoryConfidence, memoryPresent := maxFindingConfidence(
		findings,
		detectorKVCachePressurePreemptions,
		detectorGPUMemorySaturation,
	)

	if computePresent && memoryPresent {
		return currentLoadBottleneckMixed, clampFloat(maxFloatN(computeConfidence, memoryConfidence), 0, 1)
	}
	if memoryPresent {
		return currentLoadBottleneckGPUMemory, clampFloat(memoryConfidence, 0, 1)
	}
	if computePresent {
		return currentLoadBottleneckGPUCompute, clampFloat(computeConfidence, 0, 1)
	}

	computeLoad := clampFloat(features.AvgGPUComputeLoadPct, 0, 100)
	memoryLoad := clampFloat(features.AvgGPUMemoryBandwidthLoadPct, 0, 100)
	if computeLoad >= 70 && memoryLoad >= 70 && absFloat(computeLoad-memoryLoad) <= 5 {
		return currentLoadBottleneckMixed, 0.50
	}
	if computeLoad >= 70 && computeLoad >= memoryLoad+5 {
		return currentLoadBottleneckGPUCompute, 0.60
	}
	if memoryLoad >= 70 && memoryLoad >= computeLoad+5 {
		return currentLoadBottleneckGPUMemory, 0.60
	}

	return currentLoadBottleneckUnknown, 0
}

func currentLoadFindingConfidence(findings map[string]model.Finding, id string) (float64, bool) {
	finding, ok := findings[id]
	if !ok || finding.Status != model.FindingStatusPresent {
		return 0, false
	}
	return clampFloat(finding.Confidence, 0, 1), true
}

func maxFindingConfidence(findings map[string]model.Finding, ids ...string) (float64, bool) {
	best := 0.0
	found := false
	for _, id := range ids {
		if confidence, ok := currentLoadFindingConfidence(findings, id); ok {
			best = maxFloatN(best, confidence)
			found = true
		}
	}
	return best, found
}

func collectNumericConfig(config map[string]any) map[string]float64 {
	out := map[string]float64{}
	for key, value := range config {
		storeNumericConfig(out, key, value)
	}
	return out
}

func storeNumericConfig(out map[string]float64, key string, value any) {
	normalized := normalizeConfigKey(key)
	if number, ok := asFloat64ConfigValue(value); ok {
		out[normalized] = number
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		for nestedKey, nestedValue := range typed {
			storeNumericConfig(out, nestedKey, nestedValue)
		}
	case map[any]any:
		for nestedKey, nestedValue := range typed {
			storeNumericConfig(out, fmt.Sprint(nestedKey), nestedValue)
		}
	}
}

func lookupPositive(values map[string]float64, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := values[normalizeConfigKey(key)]; ok && value > 0 {
			return value
		}
	}
	return 0
}

func normalizeConfigKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, ".", "")
	return normalized
}

func asFloat64ConfigValue(value any) (float64, bool) {
	return coerceFloat(value)
}

func maxFloatN(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	best := values[0]
	for _, value := range values[1:] {
		if value > best {
			best = value
		}
	}
	return best
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
