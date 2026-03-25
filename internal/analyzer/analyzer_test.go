package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

type stubProbe struct {
	os  model.OSInformation
	gpu model.GPUInformation
}

func (s stubProbe) Collect(context.Context) (model.OSInformation, model.GPUInformation, []string) {
	return s.os, s.gpu, nil
}

func TestAnalyzeCollectsRequiredSections(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	metricsPath := filepath.Join(tmp, "metrics.json")

	mustWrite(t, configPath, `{"model_name":"qwen-3.5","max_num_seqs":8}`)
	mustWrite(t, metricsPath, `{
  "vllm_version": "0.17.1",
  "deployment_type": "docker",
  "metric_collection_outputs": {
    "prometheus_start": "started by InferLean"
  },
  "advanced_profiling_information": {
    "target_pid": 1234,
    "duration_seconds": 30,
    "bcc": {"enabled": true, "available": true, "status": "collected", "binary": "profile", "output_path": "/tmp/bcc.log"},
    "py_spy": {"enabled": true, "available": true, "status": "collected", "binary": "py-spy", "output_path": "/tmp/pyspy.log"},
    "nsys": {"enabled": true, "available": false, "status": "failed", "binary": "nsys", "error": "not found"}
  },
  "collected_metrics": [
    {"time_label": "2026-03-20T10:00:00Z", "metrics": {"prompt_tps": 1200}},
    {"time_label": "2026-03-20T10:01:00Z", "metrics": {"prompt_tps": 1220}}
  ]
}`)

	report, err := Analyze(context.Background(), Options{
		ConfigPath:  configPath,
		MetricsPath: metricsPath,
		Now:         time.Date(2026, 3, 20, 10, 2, 0, 0, time.UTC),
		Probe: stubProbe{
			os:  model.OSInformation{OSType: "linux", Architecture: "amd64", OSVersion: "6.8.0", Distribution: "Ubuntu"},
			gpu: model.GPUInformation{GPUModel: "H100", Company: "NVIDIA", VRAMSizeBytes: 80 * 1024 * 1024 * 1024, UtilizationPct: 72.5},
		},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}

	if report.OSInformation.OSType != "linux" {
		t.Fatalf("unexpected os info: %+v", report.OSInformation)
	}
	if report.GPUInformation.GPUModel != "H100" {
		t.Fatalf("unexpected gpu info: %+v", report.GPUInformation)
	}
	if report.VLLMInformation.VLLMVersion != "0.17.1" {
		t.Fatalf("unexpected vllm info: %+v", report.VLLMInformation)
	}
	if report.VLLMInformation.InstallationType != "docker" {
		t.Fatalf("unexpected install type: %+v", report.VLLMInformation)
	}
	if report.VLLMInformation.ConfigurationLocation != configPath {
		t.Fatalf("expected config location %q, got %q", configPath, report.VLLMInformation.ConfigurationLocation)
	}
	if len(report.CollectedMetrics) != 2 {
		t.Fatalf("expected 2 collected metric samples, got %d", len(report.CollectedMetrics))
	}
	if report.CollectedMetrics[0].TimeLabel == "" {
		t.Fatalf("expected time label in collected metrics")
	}
	if _, ok := report.CurrentVLLMConfigurations["model_name"]; !ok {
		t.Fatalf("expected raw config to be embedded, got %+v", report.CurrentVLLMConfigurations)
	}
	if report.AdvancedProfiling == nil {
		t.Fatalf("expected advanced profiling section to be present")
	}
	if report.AdvancedProfiling.TargetPID != 1234 {
		t.Fatalf("expected target pid 1234, got %+v", report.AdvancedProfiling)
	}
	if report.AdvancedProfiling.BCC.Status != "collected" {
		t.Fatalf("expected bcc profiling status collected, got %+v", report.AdvancedProfiling.BCC)
	}
	if report.MetricCollectionOutputs["prometheus_start"] != "started by InferLean" {
		t.Fatalf("expected metric_collection_outputs to be propagated, got %+v", report.MetricCollectionOutputs)
	}
	if report.AnalysisSummary == nil {
		t.Fatalf("expected analysis summary to be populated")
	}
	if report.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary to be populated")
	}
}

func TestAnalyzeDoesNotExtractSeriesIntoCollectedMetrics(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	mustWrite(t, metricsPath, `{"gpu_utilization_samples":[40,42,41]}`)

	report, err := Analyze(context.Background(), Options{MetricsPath: metricsPath, Probe: stubProbe{}})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if len(report.CollectedMetrics) != 0 {
		t.Fatalf("expected no collected_metrics without explicit time-labeled metrics, got %+v", report.CollectedMetrics)
	}
}

func TestAnalyzeEmitsDefaultWorkloadProfileWhenInputMissing(t *testing.T) {
	report, err := Analyze(context.Background(), Options{Probe: stubProbe{}})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.WorkloadProfile == nil {
		t.Fatalf("expected workload profile to be present")
	}
	if report.WorkloadProfile.Source != model.WorkloadProfileSourceDefault {
		t.Fatalf("expected default workload profile source, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.Objective != "balanced" || report.WorkloadProfile.ServingPattern != model.ServingPatternUnknown || report.WorkloadProfile.TaskPattern != model.TaskPatternUnknown {
		t.Fatalf("unexpected default workload profile: %+v", report.WorkloadProfile)
	}
	if report.AnalysisSummary == nil || report.AnalysisSummary.WorkloadIntent != "balanced" {
		t.Fatalf("expected balanced workload intent, got %+v", report.AnalysisSummary)
	}
	if report.ObservedWorkloadProfile == nil {
		t.Fatalf("expected observed workload profile to be present")
	}
	if report.WorkloadProfileAlignment != nil {
		t.Fatalf("expected no alignment block without user-provided workload profile, got %+v", report.WorkloadProfileAlignment)
	}
}

func TestNormalizeReportBackfillsAnalyzerSectionsForCollectorOnlyOutput(t *testing.T) {
	report := &model.AnalysisReport{
		SchemaVersion: "v2",
		GeneratedAt:   time.Date(2026, 3, 21, 15, 4, 22, 0, time.UTC),
		ToolName:      model.ToolName,
		ToolVersion:   model.ToolVersion,
		GPUInformation: model.GPUInformation{
			GPUModel:       "NVIDIA RTX 2000 Ada Generation",
			Company:        "NVIDIA",
			VRAMSizeBytes:  16 * 1024 * 1024 * 1024,
			UtilizationPct: 0,
		},
		VLLMInformation: model.VLLMInformation{
			VLLMVersion:      "0.18.0",
			InstallationType: "host",
		},
		CollectedMetrics: []model.CollectedMetricPoint{
			{
				TimeLabel: "2026-03-21T15:06:02Z",
				Metrics: map[string]float64{
					"up{instance=\"127.0.0.1:9400\",job=\"dcgm_exporter\"}": 1,
				},
			},
			{
				TimeLabel: "2026-03-21T15:06:12Z",
				Metrics: map[string]float64{
					"vllm:num_requests_running":              53,
					"vllm:num_requests_waiting":              0,
					"vllm:request_success_total":             6786,
					"vllm:prompt_tokens_total":               6190000,
					"vllm:generation_tokens_total":           169000,
					"vllm:kv_cache_usage_perc":               72,
					"vllm:time_to_first_token_seconds_count": 2,
					"vllm:time_to_first_token_seconds_sum":   1.4,
				},
			},
			{
				TimeLabel: "2026-03-21T15:06:22Z",
				Metrics: map[string]float64{
					"vllm:num_requests_running":              53,
					"vllm:num_requests_waiting":              0,
					"vllm:request_success_total":             6790,
					"vllm:prompt_tokens_total":               6196902,
					"vllm:generation_tokens_total":           169969,
					"vllm:kv_cache_usage_perc":               74,
					"vllm:time_to_first_token_seconds_count": 4,
					"vllm:time_to_first_token_seconds_sum":   2.9,
				},
			},
		},
	}

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.AnalysisSummary == nil {
		t.Fatalf("expected analysis summary to be synthesized")
	}
	if normalized.AnalysisSummary.WorkloadIntent != string(BalancedIntent) {
		t.Fatalf("expected balanced synthesized intent, got %+v", normalized.AnalysisSummary)
	}
	if normalized.ObservedWorkloadProfile == nil {
		t.Fatalf("expected observed workload profile to be synthesized")
	}
	if normalized.CurrentVLLMConfigurations == nil {
		t.Fatalf("expected current_vllm_configurations to be initialized")
	}
	if normalized.MetricCollectionOutputs == nil {
		t.Fatalf("expected metric_collection_outputs to be initialized")
	}
	if normalized.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary to be synthesized")
	}
}

func TestNormalizeReportBuildsComputeBoundCurrentLoadSummary(t *testing.T) {
	report := loadFixtureReport(t, "throughput_saturation_with_queue_pressure_report.json")

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary")
	}
	if normalized.CurrentLoadSummary.DominantGPUResource != "compute" {
		t.Fatalf("expected compute dominant resource, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.CurrentLoadSummary.CurrentLoadBottleneck != "gpu_compute_bound" {
		t.Fatalf("expected gpu_compute_bound, got %+v", normalized.CurrentLoadSummary)
	}
}

func TestNormalizeReportWarnsWhenSaturationUsesUtilizationProxy(t *testing.T) {
	report := syntheticReport(12, map[string]float64{
		"gpu_utilization_pct":                     95,
		"vllm:num_requests_running":               4,
		"vllm:num_requests_waiting":               2,
		"vllm:request_success_total":              100,
		"vllm:generation_tokens_total":            8000,
		"vllm:prompt_tokens_total":                4000,
		"vllm:time_to_first_token_seconds_sum":    50,
		"vllm:time_to_first_token_seconds_count":  100,
		"vllm:request_queue_time_seconds_sum":     30,
		"vllm:request_queue_time_seconds_count":   100,
		"vllm:request_prefill_time_seconds_sum":   20,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:request_decode_time_seconds_sum":    35,
		"vllm:request_decode_time_seconds_count":  100,
	}, map[string]float64{
		"gpu_utilization_pct":                     97,
		"vllm:num_requests_running":               4.2,
		"vllm:num_requests_waiting":               3,
		"vllm:request_success_total":              150,
		"vllm:generation_tokens_total":            12000,
		"vllm:prompt_tokens_total":                6000,
		"vllm:time_to_first_token_seconds_sum":    80,
		"vllm:time_to_first_token_seconds_count":  150,
		"vllm:request_queue_time_seconds_sum":     55,
		"vllm:request_queue_time_seconds_count":   150,
		"vllm:request_prefill_time_seconds_sum":   33,
		"vllm:request_prefill_time_seconds_count": 150,
		"vllm:request_decode_time_seconds_sum":    50,
		"vllm:request_decode_time_seconds_count":  150,
	})

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary")
	}
	if normalized.CurrentLoadSummary.SaturationSource != "gpu_utilization_proxy" {
		t.Fatalf("expected proxy saturation source, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.CurrentLoadSummary.RealSaturationMetricsAvailable {
		t.Fatalf("expected real saturation metrics to be unavailable, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.ServiceSummary == nil {
		t.Fatalf("expected service summary")
	}
	if normalized.ServiceSummary.EstimatedUpperRequestRateRPS != nil {
		t.Fatalf("expected headroom estimate to be suppressed for proxy saturation, got %+v", normalized.ServiceSummary)
	}
	if !containsAll(strings.Join(normalized.Warnings, "\n"), "Real GPU saturation metrics unavailable", "GPU utilization as a proxy") {
		t.Fatalf("expected proxy saturation warning, got %+v", normalized.Warnings)
	}
}

func TestNormalizeReportUsesMeasuredPipeActivityWhenSMCountersMissing(t *testing.T) {
	report := syntheticReport(8, map[string]float64{
		"DCGM_FI_PROF_PIPE_TENSOR_ACTIVE":         62,
		"DCGM_FI_PROF_PIPE_FP16_ACTIVE":           48,
		"vllm:num_requests_running":               3,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              100,
		"vllm:generation_tokens_total":            8000,
		"vllm:prompt_tokens_total":                4000,
		"vllm:time_to_first_token_seconds_sum":    20,
		"vllm:time_to_first_token_seconds_count":  100,
		"vllm:request_queue_time_seconds_sum":     4,
		"vllm:request_queue_time_seconds_count":   100,
		"vllm:request_prefill_time_seconds_sum":   12,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:request_decode_time_seconds_sum":    18,
		"vllm:request_decode_time_seconds_count":  100,
	}, map[string]float64{
		"DCGM_FI_PROF_PIPE_TENSOR_ACTIVE":         68,
		"DCGM_FI_PROF_PIPE_FP16_ACTIVE":           44,
		"vllm:num_requests_running":               3.2,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              140,
		"vllm:generation_tokens_total":            11000,
		"vllm:prompt_tokens_total":                6000,
		"vllm:time_to_first_token_seconds_sum":    28,
		"vllm:time_to_first_token_seconds_count":  140,
		"vllm:request_queue_time_seconds_sum":     6,
		"vllm:request_queue_time_seconds_count":   140,
		"vllm:request_prefill_time_seconds_sum":   18,
		"vllm:request_prefill_time_seconds_count": 140,
		"vllm:request_decode_time_seconds_sum":    24,
		"vllm:request_decode_time_seconds_count":  140,
	})

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary")
	}
	if normalized.CurrentLoadSummary.ComputeLoadSource != "dcgm_pipe_active_max" {
		t.Fatalf("expected pipe-activity compute source, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.CurrentLoadSummary.SaturationSource != "measured" {
		t.Fatalf("expected measured saturation source, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.CurrentLoadSummary.RealSaturationMetricsAvailable != true {
		t.Fatalf("expected real saturation metrics to be available, got %+v", normalized.CurrentLoadSummary)
	}
	if containsAll(strings.Join(normalized.Warnings, "\n"), "GPU utilization as a proxy") {
		t.Fatalf("did not expect proxy warning, got %+v", normalized.Warnings)
	}
}

func TestBuildCurrentLoadSummaryClassifiesCPUBound(t *testing.T) {
	report := syntheticReport(92, map[string]float64{
		"gpu_utilization_pct":                     22,
		"vllm:num_requests_running":               1.5,
		"vllm:num_requests_waiting":               2,
		"vllm:request_success_total":              100,
		"vllm:generation_tokens_total":            8000,
		"vllm:prompt_tokens_total":                4000,
		"vllm:time_to_first_token_seconds_sum":    220,
		"vllm:time_to_first_token_seconds_count":  100,
		"vllm:request_queue_time_seconds_sum":     40,
		"vllm:request_queue_time_seconds_count":   100,
		"vllm:request_prefill_time_seconds_sum":   60,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:request_decode_time_seconds_sum":    80,
		"vllm:request_decode_time_seconds_count":  100,
	}, map[string]float64{
		"gpu_utilization_pct":                     24,
		"vllm:num_requests_running":               1.8,
		"vllm:num_requests_waiting":               3,
		"vllm:request_success_total":              140,
		"vllm:generation_tokens_total":            11000,
		"vllm:prompt_tokens_total":                6000,
		"vllm:time_to_first_token_seconds_sum":    330,
		"vllm:time_to_first_token_seconds_count":  140,
		"vllm:request_queue_time_seconds_sum":     60,
		"vllm:request_queue_time_seconds_count":   140,
		"vllm:request_prefill_time_seconds_sum":   88,
		"vllm:request_prefill_time_seconds_count": 140,
		"vllm:request_decode_time_seconds_sum":    114,
		"vllm:request_decode_time_seconds_count":  140,
	})
	report.AnalysisSummary = SummarizeReport(report, BalancedIntent)

	summary := buildCurrentLoadSummary(report, BalancedIntent)
	if summary == nil {
		t.Fatalf("expected current load summary")
	}
	if summary.CurrentLoadBottleneck != "cpu_bound" {
		t.Fatalf("expected cpu_bound, got %+v", summary)
	}
}

func TestBuildCurrentLoadSummaryClassifiesMixedWhenComputeAndMemoryTie(t *testing.T) {
	report := syntheticReport(30, map[string]float64{
		"gpu_utilization_pct":                   74,
		"DCGM_FI_PROF_SM_ACTIVE":                0.74,
		"DCGM_FI_PROF_DRAM_ACTIVE":              0.72,
		"vllm:num_requests_running":             2,
		"vllm:num_requests_waiting":             0,
		"vllm:request_success_total":            100,
		"vllm:generation_tokens_total":          10000,
		"vllm:prompt_tokens_total":              4000,
		"vllm:request_queue_time_seconds_sum":   2,
		"vllm:request_queue_time_seconds_count": 100,
	}, map[string]float64{
		"gpu_utilization_pct":                   75,
		"DCGM_FI_PROF_SM_ACTIVE":                0.75,
		"DCGM_FI_PROF_DRAM_ACTIVE":              0.73,
		"vllm:num_requests_running":             2,
		"vllm:num_requests_waiting":             0,
		"vllm:request_success_total":            130,
		"vllm:generation_tokens_total":          13000,
		"vllm:prompt_tokens_total":              5200,
		"vllm:request_queue_time_seconds_sum":   3,
		"vllm:request_queue_time_seconds_count": 130,
	})
	report.AnalysisSummary = SummarizeReport(report, BalancedIntent)

	summary := buildCurrentLoadSummary(report, BalancedIntent)
	if summary == nil {
		t.Fatalf("expected current load summary")
	}
	if summary.CurrentLoadBottleneck != "mixed" {
		t.Fatalf("expected mixed bottleneck, got %+v", summary)
	}
}

func TestNormalizeReportBackfillsCurrentLoadSummaryFromFeatureSummary(t *testing.T) {
	report := &model.AnalysisReport{
		FeatureSummary: &model.FeatureSummary{
			TrafficObserved:              true,
			EnoughLatencySamples:         true,
			AvgGPUComputeLoadPct:         84,
			AvgGPUMemoryBandwidthLoadPct: 41,
			AvgGPUTensorLoadPct:          12,
			AvgGPUUtilizationPct:         62,
			AvgRequestsWaiting:           1,
			RequestSuccessDelta:          10,
			GenerationTokensDelta:        1000,
		},
		AnalysisSummary: &model.AnalysisSummary{
			WorkloadIntent: string(BalancedIntent),
		},
		CurrentVLLMConfigurations: map[string]any{
			"tensor_parallel_size": 4,
		},
	}

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.CurrentLoadSummary == nil {
		t.Fatalf("expected current load summary")
	}
	if normalized.CurrentLoadSummary.CurrentSaturationPct != 84 {
		t.Fatalf("expected saturation 84, got %+v", normalized.CurrentLoadSummary)
	}
	if normalized.CurrentLoadSummary.CurrentLoadBottleneck != "gpu_compute_bound" {
		t.Fatalf("expected raw compute fallback, got %+v", normalized.CurrentLoadSummary)
	}
}

func TestNormalizeReportBuildsServiceSummaryWithExactLatencyPercentiles(t *testing.T) {
	report := syntheticReport(18, map[string]float64{
		"gpu_utilization_pct":                                32,
		"DCGM_FI_PROF_SM_ACTIVE":                             0.61,
		"vllm:num_requests_running":                          1.2,
		"vllm:num_requests_waiting":                          2.4,
		"vllm:request_success_total":                         100,
		"vllm:e2e_request_latency_seconds_sum":               120,
		"vllm:e2e_request_latency_seconds_count":             100,
		`vllm:e2e_request_latency_seconds_bucket{le="0.5"}`:  10,
		`vllm:e2e_request_latency_seconds_bucket{le="1"}`:    40,
		`vllm:e2e_request_latency_seconds_bucket{le="2"}`:    80,
		`vllm:e2e_request_latency_seconds_bucket{le="+Inf"}`: 100,
		"vllm:request_queue_time_seconds_sum":                15,
		"vllm:request_queue_time_seconds_count":              100,
	}, map[string]float64{
		"gpu_utilization_pct":                                35,
		"DCGM_FI_PROF_SM_ACTIVE":                             0.64,
		"vllm:num_requests_running":                          1.1,
		"vllm:num_requests_waiting":                          3.1,
		"vllm:request_success_total":                         160,
		"vllm:e2e_request_latency_seconds_sum":               210,
		"vllm:e2e_request_latency_seconds_count":             160,
		`vllm:e2e_request_latency_seconds_bucket{le="0.5"}`:  18,
		`vllm:e2e_request_latency_seconds_bucket{le="1"}`:    70,
		`vllm:e2e_request_latency_seconds_bucket{le="2"}`:    130,
		`vllm:e2e_request_latency_seconds_bucket{le="+Inf"}`: 160,
		"vllm:request_queue_time_seconds_sum":                42,
		"vllm:request_queue_time_seconds_count":              160,
	})
	report.AnalysisSummary = SummarizeReport(report, BalancedIntent)

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.ServiceSummary == nil {
		t.Fatalf("expected service summary")
	}
	if normalized.ServiceSummary.RequestRateRPS == nil || *normalized.ServiceSummary.RequestRateRPS != 1 {
		t.Fatalf("expected request rate 1 rps, got %+v", normalized.ServiceSummary.RequestRateRPS)
	}
	if normalized.ServiceSummary.RequestLatencyMS.Avg == nil || *normalized.ServiceSummary.RequestLatencyMS.Avg <= 0 {
		t.Fatalf("expected avg request latency, got %+v", normalized.ServiceSummary.RequestLatencyMS)
	}
	if !normalized.ServiceSummary.RequestLatencyMS.PercentilesAvailable {
		t.Fatalf("expected exact latency percentiles to be available")
	}
	if normalized.ServiceSummary.RequestLatencyMS.P50 == nil || normalized.ServiceSummary.RequestLatencyMS.P90 == nil {
		t.Fatalf("expected p50 and p90 percentiles, got %+v", normalized.ServiceSummary.RequestLatencyMS)
	}
	if normalized.ServiceSummary.RequestLatencyMS.P99 == nil {
		t.Fatalf("expected p99 percentile, got %+v", normalized.ServiceSummary.RequestLatencyMS)
	}
	if normalized.ServiceSummary.EstimatedUpperRequestRateRPS == nil || *normalized.ServiceSummary.EstimatedUpperRequestRateRPS <= 0 {
		t.Fatalf("expected estimated upper request rate, got %+v", normalized.ServiceSummary)
	}
	if normalized.ServiceSummary.Queue.Health != "elevated" {
		t.Fatalf("expected elevated queue health, got %+v", normalized.ServiceSummary.Queue)
	}
}

func TestObservedObjectiveDoesNotClassifyLatencyFirstAboveFiveSeconds(t *testing.T) {
	report := syntheticReport(25, map[string]float64{
		"gpu_utilization_pct":                    18,
		"vllm:num_requests_running":              1,
		"vllm:num_requests_waiting":              0,
		"vllm:request_success_total":             100,
		"vllm:e2e_request_latency_seconds_sum":   600,
		"vllm:e2e_request_latency_seconds_count": 100,
		"vllm:request_queue_time_seconds_sum":    10,
		"vllm:request_queue_time_seconds_count":  100,
		"vllm:time_to_first_token_seconds_sum":   40,
		"vllm:time_to_first_token_seconds_count": 100,
	}, map[string]float64{
		"gpu_utilization_pct":                    20,
		"vllm:num_requests_running":              1,
		"vllm:num_requests_waiting":              0,
		"vllm:request_success_total":             160,
		"vllm:e2e_request_latency_seconds_sum":   1020,
		"vllm:e2e_request_latency_seconds_count": 160,
		"vllm:request_queue_time_seconds_sum":    18,
		"vllm:request_queue_time_seconds_count":  160,
		"vllm:time_to_first_token_seconds_sum":   64,
		"vllm:time_to_first_token_seconds_count": 160,
	})
	report.AnalysisSummary = SummarizeReport(report, BalancedIntent)

	normalized := NormalizeReport(report, BalancedIntent)
	if normalized.ObservedWorkloadProfile == nil {
		t.Fatalf("expected observed workload profile")
	}
	if normalized.ObservedWorkloadProfile.Objective == string(LatencyFirstIntent) {
		t.Fatalf("expected latency_first to be suppressed for >5s avg latency, got %+v", normalized.ObservedWorkloadProfile)
	}
}

func TestAnalyzeLoadsUserWorkloadProfileAndDefaultsMissingFields(t *testing.T) {
	tmp := t.TempDir()
	profilePath := filepath.Join(tmp, "workload-profile.json")
	mustWrite(t, profilePath, `{
  "objective": "throughput_first",
  "media_reuse": "high"
}`)

	report, err := Analyze(context.Background(), Options{
		WorkloadProfilePath: profilePath,
		Probe:               stubProbe{},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.WorkloadProfile == nil {
		t.Fatalf("expected workload profile")
	}
	if report.WorkloadProfile.Source != model.WorkloadProfileSourceUserInput {
		t.Fatalf("expected user workload profile source, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.Objective != "throughput_first" {
		t.Fatalf("expected throughput_first objective, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.ServingPattern != model.ServingPatternUnknown || report.WorkloadProfile.TaskPattern != model.TaskPatternUnknown || report.WorkloadProfile.PrefixReuse != model.WorkloadProfileReuseUnknown {
		t.Fatalf("expected missing workload profile fields to default, got %+v", report.WorkloadProfile)
	}
	if report.AnalysisSummary == nil || report.AnalysisSummary.WorkloadIntent != "throughput_first" {
		t.Fatalf("expected throughput-first analysis intent, got %+v", report.AnalysisSummary)
	}
}

func TestAnalyzeUsesRuntimeConfigOverrideWhenConfigFileIsMissing(t *testing.T) {
	report, err := Analyze(context.Background(), Options{
		ConfigOverride: map[string]any{
			"model_name":           "Qwen/Qwen3.5-2B-Instruct",
			"tensor_parallel_size": int64(2),
			"max_num_seqs":         int64(32),
		},
		Probe: stubProbe{},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.CurrentVLLMConfigurations["model_name"] != "Qwen/Qwen3.5-2B-Instruct" {
		t.Fatalf("expected runtime config override to populate current_vllm_configurations, got %+v", report.CurrentVLLMConfigurations)
	}
	if report.CurrentVLLMConfigurations["max_num_seqs"] != int64(32) {
		t.Fatalf("expected max_num_seqs from override, got %+v", report.CurrentVLLMConfigurations)
	}
}

func TestAnalyzeExpandsWorkloadPresetAndAllowsExplicitOverrides(t *testing.T) {
	tmp := t.TempDir()
	profilePath := filepath.Join(tmp, "workload-profile.json")
	mustWrite(t, profilePath, `{
  "preset": "chatbot",
  "task_pattern": "multi_task",
  "media_reuse": "high"
}`)

	report, err := Analyze(context.Background(), Options{
		WorkloadProfilePath: profilePath,
		Probe:               stubProbe{},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.WorkloadProfile == nil {
		t.Fatalf("expected workload profile")
	}
	if report.WorkloadProfile.Preset != model.WorkloadProfilePresetChatbot {
		t.Fatalf("expected chatbot preset, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.ServingPattern != model.ServingPatternRealtimeChat {
		t.Fatalf("expected preset serving pattern, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.Objective != string(LatencyFirstIntent) {
		t.Fatalf("expected preset objective, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.PrefixReuse != model.WorkloadProfileReuseHigh {
		t.Fatalf("expected preset prefix reuse, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.TaskPattern != model.TaskPatternMultiTask {
		t.Fatalf("expected explicit task_pattern override, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.MediaReuse != model.WorkloadProfileReuseHigh {
		t.Fatalf("expected explicit media_reuse override, got %+v", report.WorkloadProfile)
	}
}

func TestAnalyzeAcceptsCanonicalWorkloadProfileSourceField(t *testing.T) {
	tmp := t.TempDir()
	profilePath := filepath.Join(tmp, "workload-profile.json")
	mustWrite(t, profilePath, `{
  "schema_version": "workload-profile/v1",
  "source": "user_input",
  "objective": "latency_first",
  "serving_pattern": "realtime_chat"
}`)

	report, err := Analyze(context.Background(), Options{
		WorkloadProfilePath: profilePath,
		Probe:               stubProbe{},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.WorkloadProfile == nil {
		t.Fatalf("expected workload profile")
	}
	if report.WorkloadProfile.Source != model.WorkloadProfileSourceUserInput {
		t.Fatalf("expected loader source to remain user_input, got %+v", report.WorkloadProfile)
	}
	if report.WorkloadProfile.Objective != string(LatencyFirstIntent) || report.WorkloadProfile.ServingPattern != model.ServingPatternRealtimeChat {
		t.Fatalf("expected canonical workload profile fields to load, got %+v", report.WorkloadProfile)
	}
}

func TestAnalyzeBuildsObservedWorkloadProfileAndAlignment(t *testing.T) {
	tmp := t.TempDir()
	profilePath := filepath.Join(tmp, "workload-profile.json")
	metricsPath := filepath.Join(tmp, "metrics.json")
	mustWrite(t, profilePath, `{
  "serving_pattern": "realtime_chat",
  "objective": "latency_first",
  "prefix_reuse": "high",
  "media_reuse": "high"
}`)
	mustWrite(t, metricsPath, `{
  "collected_metrics": [
    {
      "time_label": "2026-03-21T10:00:00Z",
      "metrics": {
        "gpu_utilization_pct": 20,
        "vllm:num_requests_running": 1,
        "vllm:num_requests_waiting": 0,
        "vllm:request_success_total": 100,
        "vllm:generation_tokens_total": 10000,
        "vllm:prompt_tokens_total": 5000,
        "vllm:time_to_first_token_seconds_sum": 50,
        "vllm:time_to_first_token_seconds_count": 100,
        "vllm:request_queue_time_seconds_sum": 5,
        "vllm:request_queue_time_seconds_count": 100,
        "vllm:request_prefill_time_seconds_sum": 20,
        "vllm:request_prefill_time_seconds_count": 100,
        "vllm:request_decode_time_seconds_sum": 30,
        "vllm:request_decode_time_seconds_count": 100,
        "vllm:prefix_cache_queries_total": 100,
        "vllm:prefix_cache_hits_total": 10,
        "vllm:mm_cache_queries": 100,
        "vllm:mm_cache_hits": 5
      }
    },
    {
      "time_label": "2026-03-21T10:01:00Z",
      "metrics": {
        "gpu_utilization_pct": 22,
        "vllm:num_requests_running": 1.2,
        "vllm:num_requests_waiting": 0,
        "vllm:request_success_total": 140,
        "vllm:generation_tokens_total": 16000,
        "vllm:prompt_tokens_total": 8000,
        "vllm:time_to_first_token_seconds_sum": 72,
        "vllm:time_to_first_token_seconds_count": 140,
        "vllm:request_queue_time_seconds_sum": 7,
        "vllm:request_queue_time_seconds_count": 140,
        "vllm:request_prefill_time_seconds_sum": 28,
        "vllm:request_prefill_time_seconds_count": 140,
        "vllm:request_decode_time_seconds_sum": 44,
        "vllm:request_decode_time_seconds_count": 140,
        "vllm:prefix_cache_queries_total": 140,
        "vllm:prefix_cache_hits_total": 12,
        "vllm:mm_cache_queries": 140,
        "vllm:mm_cache_hits": 8
      }
    }
  ]
}`)

	report, err := Analyze(context.Background(), Options{
		WorkloadProfilePath: profilePath,
		MetricsPath:         metricsPath,
		Probe:               stubProbe{},
	})
	if err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}
	if report.ObservedWorkloadProfile == nil {
		t.Fatalf("expected observed workload profile")
	}
	if report.ObservedWorkloadProfile.ServingPattern != model.ServingPatternRealtimeChat {
		t.Fatalf("expected observed serving pattern, got %+v", report.ObservedWorkloadProfile)
	}
	if report.ObservedWorkloadProfile.PrefixReuse != model.WorkloadProfileReuseLow {
		t.Fatalf("expected low observed prefix reuse, got %+v", report.ObservedWorkloadProfile)
	}
	if report.ObservedWorkloadProfile.MediaReuse != model.WorkloadProfileReuseUnknown {
		t.Fatalf("expected unknown observed media reuse under weak evidence, got %+v", report.ObservedWorkloadProfile)
	}
	if report.WorkloadProfileAlignment == nil {
		t.Fatalf("expected alignment block")
	}
	if field := alignmentField(report.WorkloadProfileAlignment, "prefix_reuse"); field.Status != "different" {
		t.Fatalf("expected prefix_reuse mismatch, got %+v", field)
	}
	if field := alignmentField(report.WorkloadProfileAlignment, "media_reuse"); field.Status != "insufficient_evidence" {
		t.Fatalf("expected media_reuse insufficient evidence, got %+v", field)
	}
}

func TestAnalyzeRejectsInvalidWorkloadProfile(t *testing.T) {
	tmp := t.TempDir()
	profilePath := filepath.Join(tmp, "workload-profile.json")
	mustWrite(t, profilePath, `{
  "objective": "fastest",
  "unknown_field": "bad"
}`)

	_, err := Analyze(context.Background(), Options{
		WorkloadProfilePath: profilePath,
		Probe:               stubProbe{},
	})
	if err == nil {
		t.Fatalf("expected workload profile validation error")
	}
	if got := err.Error(); got == "" || (!containsAll(got, "workload profile", "unsupported key") && !containsAll(got, "workload profile", "objective")) {
		t.Fatalf("expected workload profile validation error, got %q", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func alignmentField(alignment *model.WorkloadProfileAlignment, name string) model.WorkloadProfileAlignmentField {
	for _, field := range alignment.Fields {
		if field.Field == name {
			return field
		}
	}
	return model.WorkloadProfileAlignmentField{}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
