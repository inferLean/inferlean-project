package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

func loadMetrics(path string) (metricsSnapshot, []string, error) {
	if strings.TrimSpace(path) == "" {
		return metricsSnapshot{}, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return metricsSnapshot{}, nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return metricsSnapshot{}, nil, err
	}

	snapshot := metricsSnapshot{
		Path:   path,
		Format: "json",
	}
	if v, ok := raw["deployment_type"].(string); ok {
		snapshot.DeploymentType = strings.TrimSpace(v)
	}
	if v, ok := raw["vllm_version"].(string); ok {
		snapshot.VLLMVersion = strings.TrimSpace(v)
	}
	if v, ok := raw["gpu_name"].(string); ok {
		snapshot.GPUName = strings.TrimSpace(v)
	}
	if v, ok := coerceInt(raw["gpu_memory_mb"]); ok {
		snapshot.GPUMemoryMB = v
	}
	if embeddedOSInfo, ok := parseEmbeddedOSInformation(raw["os_information"]); ok {
		snapshot.EmbeddedOSInformation = embeddedOSInfo
	}
	if embeddedGPUInfo, ok := parseEmbeddedGPUInformation(raw["gpu_information"]); ok {
		snapshot.EmbeddedGPUInformation = embeddedGPUInfo
	}
	if embeddedConfig, ok := raw["current_vllm_configurations"].(map[string]any); ok {
		snapshot.EmbeddedConfig = embeddedConfig
	}
	if workloadProfile, ok := parseEmbeddedWorkloadProfile(raw["workload_profile"]); ok {
		snapshot.EmbeddedWorkload = workloadProfile
	}
	if warnings := parseWarnings(raw["warnings"]); len(warnings) > 0 {
		snapshot.EmbeddedWarnings = warnings
	}
	if rawLogs, ok := raw["metric_collection_outputs"].(map[string]any); ok {
		snapshot.MetricCollectionLogs = map[string]string{}
		for k, v := range rawLogs {
			snapshot.MetricCollectionLogs[k] = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if rawProfiling, ok := raw["advanced_profiling_information"]; ok {
		snapshot.AdvancedProfiling = parseAdvancedProfiling(rawProfiling)
	}
	snapshot.GPUUtilizationSamples = normalizeSamples(asFloatSlice(raw["gpu_utilization_samples"]))
	snapshot.CollectedMetrics = parseCollectedMetrics(raw)
	return snapshot, nil, nil
}

func parseEmbeddedOSInformation(raw any) (*model.OSInformation, bool) {
	if raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var info model.OSInformation
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, false
	}
	if strings.TrimSpace(info.OSType) == "" &&
		strings.TrimSpace(info.Architecture) == "" &&
		strings.TrimSpace(info.OSVersion) == "" &&
		strings.TrimSpace(info.Distribution) == "" &&
		info.MemorySizeBytes == 0 &&
		info.DiskSizeBytes == 0 &&
		info.AverageCPUUtilizationPct == 0 {
		return nil, false
	}
	return &info, true
}

func parseEmbeddedGPUInformation(raw any) (*model.GPUInformation, bool) {
	if raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var info model.GPUInformation
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, false
	}
	if strings.TrimSpace(info.GPUModel) == "" &&
		strings.TrimSpace(info.Company) == "" &&
		info.VRAMSizeBytes == 0 &&
		info.UtilizationPct == 0 {
		return nil, false
	}
	return &info, true
}

func parseWarnings(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func parseAdvancedProfiling(raw any) *model.AdvancedProfilingInfo {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var parsed model.AdvancedProfilingInfo
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	if parsed.DurationSeconds <= 0 &&
		!parsed.BCC.Enabled &&
		!parsed.PySpy.Enabled &&
		!parsed.NSys.Enabled &&
		parsed.TargetPID == 0 {
		return nil
	}
	return &parsed
}

func parseEmbeddedWorkloadProfile(raw any) (*model.WorkloadProfile, bool) {
	if raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var profile model.WorkloadProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, false
	}
	if strings.TrimSpace(profile.Objective) == "" &&
		strings.TrimSpace(profile.ServingPattern) == "" &&
		strings.TrimSpace(profile.TaskPattern) == "" &&
		strings.TrimSpace(profile.PrefixReuse) == "" &&
		strings.TrimSpace(profile.MediaReuse) == "" {
		return nil, false
	}
	return &profile, true
}

func parseCollectedMetrics(raw map[string]any) []model.CollectedMetricPoint {
	for _, key := range []string{"collected_metrics", "metrics_over_time"} {
		rawSeries, ok := raw[key]
		if !ok {
			continue
		}
		items, ok := rawSeries.([]any)
		if !ok {
			continue
		}
		out := make([]model.CollectedMetricPoint, 0, len(items))
		for i, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			label := lookupString(entry, "time_label", "timestamp", "time", "label")
			if strings.TrimSpace(label) == "" {
				label = fmt.Sprintf("sample_%d", i+1)
			}
			metrics := map[string]float64{}
			if rawMetrics, ok := entry["metrics"].(map[string]any); ok {
				for metricName, rawValue := range rawMetrics {
					if value, ok := coerceFloat(rawValue); ok {
						metrics[metricName] = value
					}
				}
			} else {
				for metricName, rawValue := range entry {
					if metricName == "time_label" || metricName == "timestamp" || metricName == "time" || metricName == "label" {
						continue
					}
					if value, ok := coerceFloat(rawValue); ok {
						metrics[metricName] = value
					}
				}
			}
			if len(metrics) == 0 {
				continue
			}
			out = append(out, model.CollectedMetricPoint{TimeLabel: label, Metrics: metrics})
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func asFloatSlice(v any) []float64 {
	switch typed := v.(type) {
	case []float64:
		return append([]float64(nil), typed...)
	case []any:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			if f, ok := coerceFloat(item); ok {
				out = append(out, f)
			}
		}
		return out
	case []int:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			out = append(out, float64(item))
		}
		return out
	case []int64:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			out = append(out, float64(item))
		}
		return out
	default:
		return nil
	}
}

func normalizeSamples(samples []float64) []float64 {
	if len(samples) == 0 {
		return nil
	}
	out := make([]float64, 0, len(samples))
	for _, sample := range samples {
		out = append(out, normalizePercentOrRatio(sample))
	}
	return out
}
