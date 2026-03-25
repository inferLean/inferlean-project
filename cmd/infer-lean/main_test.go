package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

func TestCollectWritesAbsoluteCollectorReport(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	configPath := filepath.Join(tmp, "config.json")
	workloadProfilePath := filepath.Join(tmp, "workload-profile.json")
	outputPath := filepath.Join("reports", "collector.json")

	mustWriteFile(t, metricsPath, `{
  "collected_metrics": [
    {"time_label": "2026-03-20T10:00:00Z", "metrics": {"request_tps": 6, "latency_ms": 420}},
    {"time_label": "2026-03-20T10:01:00Z", "metrics": {"request_tps": 7, "latency_ms": 390}}
  ],
  "metric_collection_outputs": {
    "prometheus_output": "started"
  }
}`)
	mustWriteFile(t, configPath, `{
  "gpu_memory_utilization": 0.70,
  "max_num_batched_tokens": 8192,
  "max_num_seqs": 8
}`)
	mustWriteFile(t, workloadProfilePath, `{
  "objective": "latency_first",
  "serving_pattern": "realtime_chat",
  "prefix_reuse": "high"
}`)

	cwd := changeDir(t, tmp)
	defer cwd()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"collect", "--output", outputPath, "--vllm-version", "0.17.1", "--deployment-type", "docker", "--metrics-file", metricsPath, "--config-file", configPath, "--workload-profile-file", workloadProfilePath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != absOutput {
		t.Fatalf("expected stdout to print absolute output path %q, got %q", absOutput, got)
	}

	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if payload["schema_version"] != "collector/v1" {
		t.Fatalf("unexpected schema version: %+v", payload["schema_version"])
	}
	if payload["deployment_type"] != "docker" {
		t.Fatalf("unexpected deployment type: %+v", payload["deployment_type"])
	}
	if payload["vllm_version"] != "0.17.1" {
		t.Fatalf("unexpected version: %+v", payload["vllm_version"])
	}
	if _, ok := payload["collected_metrics"]; !ok {
		t.Fatalf("expected collected_metrics to be present")
	}
	currentConfig, ok := payload["current_vllm_configurations"].(map[string]any)
	if !ok || currentConfig["max_num_seqs"] == nil {
		t.Fatalf("expected current_vllm_configurations to include max_num_seqs, got %+v", payload["current_vllm_configurations"])
	}
	workloadProfile, ok := payload["workload_profile"].(map[string]any)
	if !ok || workloadProfile["objective"] != "latency_first" {
		t.Fatalf("expected embedded workload profile, got %+v", payload["workload_profile"])
	}
}

func TestCollectRejectsInvalidDeploymentType(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"collect", "--deployment-type", "baremetal"}, stdout, stderr)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "invalid deployment-type") {
		t.Fatalf("expected deployment validation error, got %q", stderr.String())
	}
}

func TestCollectHelpShowsUpdatedCollectionDefaults(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"collect", "-h"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	help := stderr.String()
	if !strings.Contains(help, "Prometheus collection duration in minutes (default: 1)") {
		t.Fatalf("expected updated duration default in help output, got %q", help)
	}
	if !strings.Contains(help, "Prometheus query range step in seconds (default: 10)") {
		t.Fatalf("expected updated step default in help output, got %q", help)
	}
}

func TestAnalyzeHelpReturnsZero(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"analyze", "-h"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
}

func TestIntentWritesInteractiveWorkloadProfile(t *testing.T) {
	tmp := t.TempDir()
	outputPath := filepath.Join("reports", "intent.json")

	cwd := changeDir(t, tmp)
	defer cwd()

	previousInput := cliInput
	cliInput = strings.NewReader("latency\nrealtime\n")
	defer func() { cliInput = previousInput }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"intent", "--output", outputPath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	if !strings.Contains(stdout.String(), absOutput) {
		t.Fatalf("expected stdout to include absolute output path %q, got %q", absOutput, stdout.String())
	}

	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var profile model.WorkloadProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if profile.SchemaVersion != model.WorkloadProfileSchemaVersion {
		t.Fatalf("expected schema version %q, got %+v", model.WorkloadProfileSchemaVersion, profile)
	}
	if profile.Source != model.WorkloadProfileSourceUserInput {
		t.Fatalf("expected user-input source, got %+v", profile)
	}
	if profile.Objective != "latency_first" || profile.ServingPattern != "realtime_chat" || profile.TaskPattern != "unknown" {
		t.Fatalf("unexpected intent profile: %+v", profile)
	}
	if profile.PrefixReuse != "unknown" || profile.MediaReuse != "unknown" {
		t.Fatalf("unexpected reuse fields: %+v", profile)
	}
	if profile.Notes != "" {
		t.Fatalf("expected notes to stay empty, got %+v", profile)
	}
}

func TestIntentDefaultsTrafficShapeToMixed(t *testing.T) {
	tmp := t.TempDir()
	outputPath := filepath.Join("reports", "intent.json")

	cwd := changeDir(t, tmp)
	defer cwd()

	previousInput := cliInput
	cliInput = strings.NewReader("balanced\n\n")
	defer func() { cliInput = previousInput }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"intent", "--output", outputPath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}

	var profile model.WorkloadProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if profile.ServingPattern != model.ServingPatternMixed {
		t.Fatalf("expected traffic shape default to be mixed, got %+v", profile)
	}
}

func TestIntentAdvancedModeCapturesCacheReuse(t *testing.T) {
	tmp := t.TempDir()
	outputPath := filepath.Join("reports", "intent.json")

	cwd := changeDir(t, tmp)
	defer cwd()

	previousInput := cliInput
	cliInput = strings.NewReader("latency\nrealtime\nhigh\nskip\n")
	defer func() { cliInput = previousInput }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"intent", "--advanced", "--output", outputPath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}

	var profile model.WorkloadProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if profile.PrefixReuse != model.WorkloadProfileReuseHigh || profile.MediaReuse != model.WorkloadProfileReuseUnknown {
		t.Fatalf("expected advanced cache fields, got %+v", profile)
	}
}

func TestCollectRejectsInvalidWorkloadProfile(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	configPath := filepath.Join(tmp, "config.json")
	workloadProfilePath := filepath.Join(tmp, "workload-profile.json")

	mustWriteFile(t, metricsPath, `{"collected_metrics":[{"time_label":"2026-03-20T10:00:00Z","metrics":{"request_tps":6}}]}`)
	mustWriteFile(t, configPath, `{"max_num_seqs":8}`)
	mustWriteFile(t, workloadProfilePath, `{"objective":"invalid","bad_key":"x"}`)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"collect", "--vllm-version", "0.17.1", "--deployment-type", "docker", "--metrics-file", metricsPath, "--config-file", configPath, "--workload-profile-file", workloadProfilePath}, stdout, stderr)
	if exitCode == 0 {
		t.Fatalf("expected invalid workload profile to fail")
	}
	if !strings.Contains(stderr.String(), "workload profile") {
		t.Fatalf("expected workload profile error, got %q", stderr.String())
	}
}

func TestAnalyzeWritesSlimReport(t *testing.T) {
	tmp := t.TempDir()
	collectorPath := filepath.Join(tmp, "collector.json")
	outputPath := filepath.Join("reports", "analysis.json")

	mustWriteFile(t, collectorPath, `{
  "schema_version": "collector/v1",
  "tool_name": "InferLean",
  "tool_version": "dev",
  "generated_at": "2026-03-21T14:20:00Z",
  "deployment_type": "host",
  "vllm_version": "0.18.0",
  "collected_metrics": [
    {
      "time_label": "2026-03-21T14:10:00Z",
      "metrics": {
        "gpu_utilization_pct": 18,
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
        "vllm:request_decode_time_seconds_count": 100
      }
    },
    {
      "time_label": "2026-03-21T14:11:00Z",
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
        "vllm:request_decode_time_seconds_count": 140
      }
    }
  ],
  "current_vllm_configurations": {
    "model_name": "Qwen 3 30B A3B",
    "max_num_seqs": 8,
    "max_num_batched_tokens": 8192,
    "tensor_parallel_size": 4
  }
}`)

	cwd := changeDir(t, tmp)
	defer cwd()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"analyze", "--collector-file", collectorPath, "--output", outputPath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != absOutput {
		t.Fatalf("expected stdout to print absolute output path %q, got %q", absOutput, got)
	}

	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if strings.Contains(string(data), "\"telemetry_samples\"") {
		t.Fatalf("expected slim analyzer output without telemetry_samples, got %s", string(data))
	}
	var report model.AnalysisReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal analysis report: %v", err)
	}
	if report.AnalysisSummary == nil {
		t.Fatalf("expected analysis summary")
	}
	if report.FeatureSummary == nil || report.FeatureSummary.GenerationTokensDelta <= 0 {
		t.Fatalf("expected persisted feature summary, got %+v", report.FeatureSummary)
	}
	if report.CurrentLoadSummary == nil {
		t.Fatalf("expected current_load_summary in analyzer output")
	}
	if len(report.CurrentVLLMConfigurations) == 0 {
		t.Fatalf("expected current_vllm_configurations in analyzer output")
	}
}

func TestAnalyzeAcceptsIntentFileAlias(t *testing.T) {
	tmp := t.TempDir()
	collectorPath := filepath.Join(tmp, "collector.json")
	intentPath := filepath.Join(tmp, "intent.json")
	outputPath := filepath.Join("reports", "analysis.json")

	mustWriteFile(t, collectorPath, `{
  "schema_version": "collector/v1",
  "tool_name": "inferLean",
  "tool_version": "dev",
  "generated_at": "2026-03-21T14:20:00Z",
  "deployment_type": "host",
  "vllm_version": "0.18.0",
  "collected_metrics": [
    {
      "time_label": "2026-03-21T14:10:00Z",
      "metrics": {
        "gpu_utilization_pct": 18,
        "vllm:num_requests_running": 1,
        "vllm:num_requests_waiting": 0,
        "vllm:request_success_total": 100,
        "vllm:generation_tokens_total": 10000,
        "vllm:prompt_tokens_total": 5000
      }
    },
    {
      "time_label": "2026-03-21T14:11:00Z",
      "metrics": {
        "gpu_utilization_pct": 22,
        "vllm:num_requests_running": 1.2,
        "vllm:num_requests_waiting": 0,
        "vllm:request_success_total": 140,
        "vllm:generation_tokens_total": 16000,
        "vllm:prompt_tokens_total": 8000
      }
    }
  ],
  "current_vllm_configurations": {
    "model_name": "Qwen 3 30B A3B",
    "max_num_seqs": 8,
    "max_num_batched_tokens": 8192
  }
}`)
	mustWriteFile(t, intentPath, `{
  "schema_version": "workload-profile/v1",
  "objective": "latency_first",
  "serving_pattern": "realtime_chat"
}`)

	cwd := changeDir(t, tmp)
	defer cwd()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"analyze", "--collector-file", collectorPath, "--intent-file", intentPath, "--output", outputPath}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report model.AnalysisReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal analysis report: %v", err)
	}
	if report.WorkloadProfile == nil || report.WorkloadProfile.Objective != "latency_first" || report.WorkloadProfile.Source != model.WorkloadProfileSourceUserInput {
		t.Fatalf("expected analyze to load intent file as workload profile, got %+v", report.WorkloadProfile)
	}
}

func TestRecommendWritesAbsoluteReport(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	corpusPath := filepath.Join(tmp, "corpus.json")
	outputPath := filepath.Join("reports", "recommendation.json")

	mustWriteFile(t, analysisPath, `{
  "schema_version": "v3",
  "generated_at": "2026-03-21T14:20:00Z",
  "tool_name": "InferLean",
  "tool_version": "dev",
  "gpu_information": {"gpu_model": "H100", "company": "NVIDIA"},
  "vllm_information": {"vllm_version": "0.18.0", "configuration_location": "/etc/vllm/config.json", "installation_type": "host"},
  "feature_summary": {
    "snapshot_count": 2,
    "interval_seconds": 60,
    "traffic_observed": true,
    "enough_latency_samples": true,
    "avg_gpu_utilization_pct": 20,
    "avg_requests_running": 1.1,
    "request_success_delta": 40,
    "prompt_tokens_delta": 3000,
    "generation_tokens_delta": 6000,
    "avg_ttft_seconds": 0.55,
    "ttft_count_delta": 40,
    "avg_queue_time_seconds": 0.05,
    "queue_time_count_delta": 40,
    "avg_prefill_time_seconds": 0.20,
    "prefill_count_delta": 40,
    "avg_decode_time_seconds": 0.30,
    "decode_count_delta": 40
  },
  "current_vllm_configurations": {
    "model_name": "Qwen 3 30B A3B",
    "max_num_seqs": 8,
    "max_num_batched_tokens": 8192,
    "tensor_parallel_size": 4
  },
  "analysis_summary": {
    "workload_intent": "throughput_first",
    "data_quality": {
      "snapshot_count": 2,
      "interval_seconds": 60,
      "traffic_observed": true,
      "enough_latency_samples": true,
      "enough_kv_cache_samples": false
    },
    "findings": [
      {
        "id": "underutilized_gpu_or_conservative_batching",
        "category": "utilization",
        "status": "present",
        "severity": "high",
        "confidence": 0.86,
        "summary": "Traffic was present, but GPU utilization stayed low with little queueing, which usually means batching or concurrency is too conservative for the offered load."
      }
    ]
  }
}`)
	mustWriteFile(t, corpusPath, `{
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
          "parameters": {"max_num_seqs": 8, "max_num_batched_tokens": 8192},
          "metrics": {"throughput_tokens_per_second": 4200, "ttft_ms": 620, "latency_p50_ms": 1450, "latency_p95_ms": 2100, "gpu_utilization_pct": 24}
        },
        {
          "parameters": {"max_num_seqs": 16, "max_num_batched_tokens": 16384},
          "metrics": {"throughput_tokens_per_second": 6100, "ttft_ms": 760, "latency_p50_ms": 1650, "latency_p95_ms": 2440, "gpu_utilization_pct": 44}
        }
      ]
    }
  ]
}`)

	cwd := changeDir(t, tmp)
	defer cwd()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"recommend", "--analysis-file", analysisPath, "--corpus-file", corpusPath, "--output", outputPath, "--set", "max_num_seqs=16"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatalf("abs output: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != absOutput {
		t.Fatalf("expected stdout to print absolute output path %q, got %q", absOutput, got)
	}

	data, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report model.RecommendationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal recommendation report: %v", err)
	}
	if report.MatchedCorpusProfile == nil {
		t.Fatalf("expected matched corpus profile")
	}
	if report.ScenarioPrediction == nil {
		t.Fatalf("expected scenario prediction in recommendation output")
	}
	if len(report.ScenarioOptions) < 4 {
		t.Fatalf("expected scenario options in recommendation output, got %+v", report.ScenarioOptions)
	}
	if report.CapacityOpportunity == nil {
		t.Fatalf("expected capacity opportunity in recommendation output")
	}
	if len(report.Recommendations) == 0 {
		t.Fatalf("expected recommendation output")
	}
}

func TestRunCollectsTriggersAndOpensDashboard(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	configPath := filepath.Join(tmp, "config.json")
	mustWriteFile(t, metricsPath, `{
  "collected_metrics": [
    {"time_label": "2026-03-20T10:00:00Z", "metrics": {"request_tps": 6, "latency_ms": 420}},
    {"time_label": "2026-03-20T10:01:00Z", "metrics": {"request_tps": 7, "latency_ms": 390}}
  ]
}`)
	mustWriteFile(t, configPath, `{"max_num_seqs": 8, "max_num_batched_tokens": 8192}`)

	var receivedCollector map[string]any
	analysisCalls := 0
	recommendationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/trigger-job":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&receivedCollector); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":123,"job_uuid":"00000000-0000-0000-0000-000000000123","status":"queued"}`))
		case "/api/v1/jobs/123/analysis":
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			analysisCalls++
			w.Header().Set("Content-Type", "application/json")
			if analysisCalls < 2 {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"job_id":"123","artifact":"analysis","status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"schema_version":"v3",
				"resource_load_summary":{
					"current_saturation_pct": 84,
					"current_gpu_load_pct": 72,
					"current_gpu_load_effective_count": 2.9,
					"total_gpu_count": 4,
					"current_load_bottleneck": "gpu_compute_bound"
				},
				"diagnosis_summary":{
					"findings":[
						{
							"id":"queue_dominated_ttft",
							"status":"present",
							"severity":"high",
							"summary":"Queue-heavy TTFT hurts responsiveness"
						}
					]
				}
			}`))
		case "/api/v1/jobs/123/top-recommendation":
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			recommendationCalls++
			w.Header().Set("Content-Type", "application/json")
			if recommendationCalls < 2 {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"job_id":"123","artifact":"top_recommendation","status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"job_id":"123",
				"id":"123",
				"top_issue":"Queue-heavy TTFT hurts responsiveness",
				"top_recommendation":"Increase max_num_seqs to reduce queueing.",
				"gpu_capacity_headroom":{
					"recoverable_gpu_load_pct": 18,
					"recoverable_gpu_count": 0.7
				}
			}`))
		case "/api/v1/jobs/123/recommendation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"schema_version":"recommendation/v3",
				"strategy_options":[
					{
						"id":"throughput_push",
						"label":"Throughput Push",
						"objective":"throughput_first",
						"recommended":true,
						"summary":"Increase max_num_seqs to reduce queueing."
					},
					{
						"id":"latency_guardrail",
						"label":"Latency Guardrail",
						"objective":"latency_first",
						"recommended":false,
						"summary":"Reduce chunked prefill budget to protect TTFT."
					}
				]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv(inferleanBaseURLEnv, server.URL)

	var opened string
	previousOpen := openDashboardInBrowser
	openDashboardInBrowser = func(target string) error {
		opened = target
		return nil
	}
	defer func() { openDashboardInBrowser = previousOpen }()
	previousPollInterval := runPollInterval
	previousPollTimeout := runPollTimeout
	runPollInterval = 5 * time.Millisecond
	runPollTimeout = 2 * time.Second
	defer func() {
		runPollInterval = previousPollInterval
		runPollTimeout = previousPollTimeout
	}()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{
		"run",
		"--metrics-file", metricsPath,
		"--config-file", configPath,
		"--vllm-version", "0.17.1",
		"--deployment-type", "docker",
	}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}
	if len(receivedCollector) == 0 {
		t.Fatalf("expected collector payload to be sent")
	}
	if got := strings.TrimSpace(fmt.Sprint(receivedCollector["schema_version"])); got != "collector/v1" {
		t.Fatalf("expected collector schema_version collector/v1, got %q", got)
	}

	expectedDashboard := server.URL + "/jobs/123"
	if opened != expectedDashboard {
		t.Fatalf("expected browser open target %q, got %q", expectedDashboard, opened)
	}
	if !strings.Contains(stdout.String(), "Job queued: 123") {
		t.Fatalf("expected stdout to include job id, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Top issue: Queue-heavy TTFT hurts responsiveness") {
		t.Fatalf("expected stdout to include top issue summary, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Top recommendation: Increase max_num_seqs to reduce queueing.") {
		t.Fatalf("expected stdout to include top recommendation summary, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Alternative path: Reduce chunked prefill budget to protect TTFT.") {
		t.Fatalf("expected stdout to include alternative path summary, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Current saturation: 84.0%") {
		t.Fatalf("expected stdout to include current saturation, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Current GPU utilization (sample average): 72.0% (2.9 / 4.0 GPUs)") {
		t.Fatalf("expected stdout to include current gpu load, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Load bottleneck: GPU compute bound") {
		t.Fatalf("expected stdout to include load bottleneck, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Recoverable capacity: 18.0% (0.7 GPUs)") {
		t.Fatalf("expected stdout to include recoverable capacity, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "For further details, see dashboard: "+expectedDashboard) {
		t.Fatalf("expected stdout to include dashboard URL %q, got %q", expectedDashboard, stdout.String())
	}
}

func TestRunContinuesAfterInterruptDuringCollection(t *testing.T) {
	var receivedCollector map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/trigger-job":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&receivedCollector); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"123","status":"queued"}`))
		case "/api/v1/jobs/123/analysis":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"schema_version":"v3","diagnosis_summary":{"findings":[]}}`))
		case "/api/v1/jobs/123/top-recommendation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"job_id":"123","id":"123","top_recommendation":"Use partial sample tuning."}`))
		case "/api/v1/jobs/123/recommendation":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv(inferleanBaseURLEnv, server.URL)

	previousCollect := runCollectForRun
	runCollectForRun = func(ctx context.Context, args []string, stdout, stderr io.Writer, opts collectRunOptions) error {
		outputPath := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--output" && i+1 < len(args) {
				outputPath = strings.TrimSpace(args[i+1])
				break
			}
		}
		if outputPath == "" {
			return fmt.Errorf("missing --output in collect args")
		}
		if opts.progressCallback != nil {
			opts.progressCallback(CollectionProgressUpdate{
				Elapsed:   10 * time.Second,
				Remaining: 50 * time.Second,
				Total:     60 * time.Second,
				Progress:  10.0 / 60.0,
			})
		}
		<-ctx.Done()
		payload := map[string]any{
			"schema_version": "collector/v1",
			"collected_metrics": []map[string]any{
				{
					"time_label": "2026-03-25T10:00:00Z",
					"metrics": map[string]any{
						"request_tps": 5,
						"latency_ms":  410,
					},
				},
			},
			"metric_collection_outputs": map[string]any{
				"collection_interrupted": "true",
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return os.WriteFile(outputPath, data, 0o600)
	}
	defer func() { runCollectForRun = previousCollect }()

	previousNotify := runNotifyInterrupt
	previousStopNotify := runStopInterruptNotify
	runNotifyInterrupt = func(ch chan<- os.Signal) {
		ch <- os.Interrupt
	}
	runStopInterruptNotify = func(ch chan<- os.Signal) {
		_ = ch
	}
	defer func() {
		runNotifyInterrupt = previousNotify
		runStopInterruptNotify = previousStopNotify
	}()

	openCalled := false
	previousOpen := openDashboardInBrowser
	openDashboardInBrowser = func(target string) error {
		openCalled = true
		_ = target
		return nil
	}
	defer func() { openDashboardInBrowser = previousOpen }()

	previousPollInterval := runPollInterval
	previousPollTimeout := runPollTimeout
	runPollInterval = 5 * time.Millisecond
	runPollTimeout = 2 * time.Second
	defer func() {
		runPollInterval = previousPollInterval
		runPollTimeout = previousPollTimeout
	}()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"run"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}
	if len(receivedCollector) == 0 {
		t.Fatalf("expected collector payload to be sent")
	}
	if got := strings.TrimSpace(fmt.Sprint(receivedCollector["schema_version"])); got != "collector/v1" {
		t.Fatalf("expected collector schema_version collector/v1, got %q", got)
	}
	if !strings.Contains(stdout.String(), "Top recommendation: Use partial sample tuning.") {
		t.Fatalf("expected top recommendation in output, got %q", stdout.String())
	}
	if !openCalled {
		t.Fatalf("expected browser open to be attempted")
	}
}

func TestRunRendersPremiumCardsWhenTerminalUIEnabled(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	configPath := filepath.Join(tmp, "config.json")
	mustWriteFile(t, metricsPath, `{
  "collected_metrics": [
    {"time_label": "2026-03-20T10:00:00Z", "metrics": {"request_tps": 6, "latency_ms": 420}},
    {"time_label": "2026-03-20T10:01:00Z", "metrics": {"request_tps": 7, "latency_ms": 390}}
  ]
}`)
	mustWriteFile(t, configPath, `{"max_num_seqs": 8, "max_num_batched_tokens": 8192}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/trigger-job":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"123","status":"queued"}`))
		case "/api/v1/jobs/123/analysis":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "schema_version":"v3",
  "service_summary":{
    "request_rate_rps":7.5,
    "request_latency_ms":{"avg":850,"p50":700,"p99":1200,"percentiles_available":true},
    "queue":{"avg_delay_ms":420,"avg_waiting_requests":3.1,"health":"elevated"},
    "saturation_pct":84,
    "estimated_upper_request_rate_rps":8.93,
    "bottleneck":{"kind":"gpu_compute","confidence":0.91},
    "observed_mode":{"objective":"balanced","serving_pattern":"realtime","confidence":0.82},
    "configured_intent":{"value":"latency_first","confidence":0.92}
  },
  "current_load_summary":{
    "current_saturation_pct":84,
    "current_gpu_load_pct":72,
    "current_gpu_load_effective_count":2.9,
    "total_gpu_count":4,
    "current_load_bottleneck":"gpu_compute_bound",
    "dominant_gpu_resource":"compute",
    "compute_load_pct":84,
    "memory_bandwidth_load_pct":55,
    "cpu_load_pct":70
  },
  "diagnosis_summary":{
    "findings":[
      {"id":"queue_dominated_ttft","summary":"Queue-heavy TTFT hurts responsiveness","status":"present","rank":1}
    ]
  }
}`))
		case "/api/v1/jobs/123/top-recommendation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "job_id":"123",
  "id":"123",
  "top_issue":"Queue-heavy TTFT hurts responsiveness",
  "top_recommendation":"Increase max_num_seqs to reduce queueing.",
  "gpu_capacity_headroom":{"recoverable_gpu_load_pct":18,"recoverable_gpu_count":0.7}
}`))
		case "/api/v1/jobs/123/recommendation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "schema_version":"recommendation/v1",
  "declared_intent":{"value":"latency_first","source":"intent_file"},
  "guardrail_policy":{"min_throughput_retention_pct":80},
  "gpu_capacity_headroom":{"recoverable_gpu_load_pct":18,"recoverable_gpu_count":0.7},
  "recommended_action":{"summary":"Apply benchmark-backed tuning: max_num_batched_tokens=4096, max_num_seqs=256","confidence":0.92},
  "expected_impact":{
    "request_rate_rps":{"after":12.77,"delta_pct":204.1},
    "request_latency_ms":{"p50":{"after":1520,"delta_pct":-15.6}},
    "gpu_utilization_pct":{"after":44,"delta_pct":83.3}
  },
  "strategy_options":[
    {"id":"primary","recommended":true,"summary":"Increase max_num_seqs to reduce queueing."}
  ]
}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv(inferleanBaseURLEnv, server.URL)

	previousOpen := openDashboardInBrowser
	openDashboardInBrowser = func(target string) error {
		_ = target
		return nil
	}
	defer func() { openDashboardInBrowser = previousOpen }()

	previousPollInterval := runPollInterval
	previousPollTimeout := runPollTimeout
	runPollInterval = 5 * time.Millisecond
	runPollTimeout = 2 * time.Second
	defer func() {
		runPollInterval = previousPollInterval
		runPollTimeout = previousPollTimeout
	}()

	previousTerminalWriterChecker := terminalWriterChecker
	previousTerminalColorChecker := terminalColorChecker
	terminalWriterChecker = func(w io.Writer) bool {
		_ = w
		return true
	}
	terminalColorChecker = func() bool { return false }
	defer func() {
		terminalWriterChecker = previousTerminalWriterChecker
		terminalColorChecker = previousTerminalColorChecker
	}()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{
		"run",
		"--metrics-file", metricsPath,
		"--config-file", configPath,
		"--vllm-version", "0.17.1",
		"--deployment-type", "docker",
	}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	rendered := stdout.String()
	for _, want := range []string{
		"Saturation",
		"Observed Traffic",
		"GPU compute 84%, GPU bandwidth 55%, CPU 70%",
		"GPU Load Headroom",
		"Target Goal",
		"Best Action",
		"Expected Impact",
		"Increase max_num_seqs to reduce queueing.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected premium run output to include %q, got %q", want, rendered)
		}
	}
}

func TestRunRejectsInvalidBaseURLFromEnv(t *testing.T) {
	t.Setenv(inferleanBaseURLEnv, "://bad-url")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"run"}, stdout, stderr)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code for invalid base url")
	}
	if !strings.Contains(stderr.String(), "invalid INFERLEAN_BASE_URL") {
		t.Fatalf("expected INFERLEAN_BASE_URL validation error, got %q", stderr.String())
	}
}

func TestExecuteWithoutArgsDefaultsToRun(t *testing.T) {
	t.Setenv(inferleanBaseURLEnv, "://bad-url")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute(nil, stdout, stderr)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code when default run sees invalid base url")
	}
	if !strings.Contains(stderr.String(), "invalid INFERLEAN_BASE_URL") {
		t.Fatalf("expected default command path to validate INFERLEAN_BASE_URL, got %q", stderr.String())
	}
}

func TestExecuteWithRootFlagsDefaultsToRun(t *testing.T) {
	t.Setenv(inferleanBaseURLEnv, "://bad-url")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"--output", "collector-report.json"}, stdout, stderr)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code when default run sees invalid base url")
	}
	if !strings.Contains(stderr.String(), "invalid INFERLEAN_BASE_URL") {
		t.Fatalf("expected root flags without subcommand to route to run, got %q", stderr.String())
	}
}

func TestRunFallsBackToManualUploadOnNetworkTriggerFailure(t *testing.T) {
	tmp := t.TempDir()
	metricsPath := filepath.Join(tmp, "metrics.json")
	configPath := filepath.Join(tmp, "config.json")
	mustWriteFile(t, metricsPath, `{
  "collected_metrics": [
    {"time_label": "2026-03-20T10:00:00Z", "metrics": {"request_tps": 6, "latency_ms": 420}},
    {"time_label": "2026-03-20T10:01:00Z", "metrics": {"request_tps": 7, "latency_ms": 390}}
  ]
}`)
	mustWriteFile(t, configPath, `{"max_num_seqs": 8, "max_num_batched_tokens": 8192}`)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate listener: %v", err)
	}
	unreachableBaseURL := "http://" + ln.Addr().String()
	_ = ln.Close()
	t.Setenv(inferleanBaseURLEnv, unreachableBaseURL)

	cwd := changeDir(t, tmp)
	defer cwd()

	openCalled := false
	previousOpen := openDashboardInBrowser
	openDashboardInBrowser = func(target string) error {
		openCalled = true
		_ = target
		return nil
	}
	defer func() { openDashboardInBrowser = previousOpen }()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{
		"run",
		"--metrics-file", metricsPath,
		"--config-file", configPath,
		"--vllm-version", "0.17.1",
		"--deployment-type", "docker",
	}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 fallback flow, got %d, stderr=%s", exitCode, stderr.String())
	}
	if openCalled {
		t.Fatalf("did not expect browser open on manual upload fallback")
	}

	errOutput := stderr.String()
	if !strings.Contains(errOutput, "automatic backend trigger failed due to a network issue") {
		t.Fatalf("expected network fallback warning, got %q", errOutput)
	}
	expectedTriggerURL := unreachableBaseURL + "/trigger"
	if !strings.Contains(errOutput, expectedTriggerURL) {
		t.Fatalf("expected manual trigger URL %q in stderr, got %q", expectedTriggerURL, errOutput)
	}

	const savedPrefix = "collector JSON saved for manual upload: "
	var savedPath string
	for _, line := range strings.Split(errOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, savedPrefix) {
			savedPath = strings.TrimSpace(strings.TrimPrefix(line, savedPrefix))
			break
		}
	}
	if savedPath == "" {
		t.Fatalf("expected saved collector path in stderr, got %q", errOutput)
	}
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved collector file: %v", err)
	}
	var collector map[string]any
	if err := json.Unmarshal(data, &collector); err != nil {
		t.Fatalf("unmarshal saved collector JSON: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(collector["schema_version"])); got != "collector/v1" {
		t.Fatalf("expected saved collector schema_version collector/v1, got %q", got)
	}
}

func TestWaitForJobCompletionReportsStagedProgress(t *testing.T) {
	analysisCalls := 0
	recommendationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs/123/analysis":
			analysisCalls++
			w.Header().Set("Content-Type", "application/json")
			if analysisCalls < 2 {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"job_id":"123","artifact":"analysis","status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"schema_version":"v3","diagnosis_summary":{"findings":[]}}`))
		case "/api/v1/jobs/123/top-recommendation":
			recommendationCalls++
			w.Header().Set("Content-Type", "application/json")
			if recommendationCalls < 2 {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"job_id":"123","artifact":"top_recommendation","status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"job_id":"123","id":"123","top_recommendation":"Tune max_num_seqs."}`))
		case "/api/v1/jobs/123/recommendation":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previousPollInterval := runPollInterval
	previousPollTimeout := runPollTimeout
	runPollInterval = 5 * time.Millisecond
	runPollTimeout = 2 * time.Second
	defer func() {
		runPollInterval = previousPollInterval
		runPollTimeout = previousPollTimeout
	}()

	updates := make([]waitProgressUpdate, 0, 6)
	analysis, _, recommendation, err := waitForJobCompletion(server.URL, "123", "", func(update waitProgressUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatalf("waitForJobCompletion failed: %v", err)
	}
	if analysis == nil {
		t.Fatalf("expected analysis report")
	}
	if recommendation == nil {
		t.Fatalf("expected recommendation report")
	}

	stageDoneOrder := make([]waitStage, 0, 2)
	for _, update := range updates {
		if update.Done {
			stageDoneOrder = append(stageDoneOrder, update.Stage)
		}
	}
	if len(stageDoneOrder) < 2 {
		t.Fatalf("expected done updates for both stages, got %+v", updates)
	}
	if stageDoneOrder[0] != waitStageAnalysis || stageDoneOrder[1] != waitStageRecommendation {
		t.Fatalf("expected analysis then recommendation completion order, got %+v", stageDoneOrder)
	}
}

func TestResolveCollectStepSecondsDefaultsAndOverrides(t *testing.T) {
	if got := resolveCollectStepSeconds(nil); got != defaultPrometheusStepSeconds {
		t.Fatalf("expected default step %d, got %d", defaultPrometheusStepSeconds, got)
	}
	if got := resolveCollectStepSeconds([]string{"--prometheus-step-seconds", "12"}); got != 12 {
		t.Fatalf("expected step override 12, got %d", got)
	}
	if got := resolveCollectStepSeconds([]string{"--prometheus-step-seconds=14"}); got != 14 {
		t.Fatalf("expected step override 14, got %d", got)
	}
	if got := resolveCollectStepSeconds([]string{"--prometheus-step-seconds", "0"}); got != defaultPrometheusStepSeconds {
		t.Fatalf("expected fallback step %d for invalid override, got %d", defaultPrometheusStepSeconds, got)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func changeDir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() {
		_ = os.Chdir(prev)
	}
}
