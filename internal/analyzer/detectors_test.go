package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inferLean/inferlean-project/internal/model"
)

func TestDetectorCatalogContainsExpectedIDs(t *testing.T) {
	all := allDetectors()
	if len(all) != 11 {
		t.Fatalf("expected 11 detectors in catalog, got %d", len(all))
	}

	var ids []string
	for _, detector := range all {
		ids = append(ids, detector.Spec().ID)
	}
	slices.Sort(ids)
	want := []string{
		detectorCPUOrHostBottleneck,
		detectorDecodeBoundGeneration,
		detectorGPUHardwareInstability,
		detectorGPUMemorySaturation,
		detectorKVCachePressurePreemptions,
		detectorPrefillHeavyWorkload,
		detectorPrefixCacheIneffective,
		detectorPromptRecomputationThrashing,
		detectorQueueDominatedTTFT,
		detectorThroughputSaturationWithQueuePressure,
		detectorUnderutilizedGPUOrConservativeBatch,
	}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("unexpected detector catalog ids: got %v want %v", ids, want)
	}

	implemented := implementedDetectors()
	if len(implemented) != 11 {
		t.Fatalf("expected 11 implemented detectors, got %d", len(implemented))
	}
	for _, detector := range implemented {
		if !detector.Spec().Implemented {
			t.Fatalf("expected implemented detector, got %+v", detector.Spec())
		}
	}
}

func TestExtractFeaturesComputesCounterDeltas(t *testing.T) {
	report := loadFixtureReport(t, "queue_dominated_ttft_report.json")

	features := ExtractFeatures(report)
	if !features.TrafficObserved {
		t.Fatalf("expected traffic_observed to be true")
	}
	if got := features.RequestSuccessDelta; got != 20 {
		t.Fatalf("expected request_success delta 20, got %v", got)
	}
	if got := features.TTFTCountDelta; got != 20 {
		t.Fatalf("expected ttft count delta 20, got %v", got)
	}
	if got := features.AvgTTFTSeconds; got != 3 {
		t.Fatalf("expected avg ttft 3, got %v", got)
	}
	if got := features.AvgQueueTimeSeconds; got != 2.5 {
		t.Fatalf("expected avg queue time 2.5, got %v", got)
	}
	if got := features.IntervalSeconds; got != 60 {
		t.Fatalf("expected 60 second interval, got %v", got)
	}
}

func TestIdleFixtureProducesNoActionableRecommendations(t *testing.T) {
	report := loadFixtureReport(t, "idle_analysis_report.json")

	summary := SummarizeReport(report, BalancedIntent)
	if summary == nil {
		t.Fatalf("expected analysis summary")
	}
	if summary.DataQuality.TrafficObserved {
		t.Fatalf("expected idle fixture to have no observed traffic")
	}
	if len(summary.Findings) != 11 {
		t.Fatalf("expected 11 implemented findings, got %d", len(summary.Findings))
	}
	for _, finding := range summary.Findings {
		if finding.Status == model.FindingStatusPresent {
			t.Fatalf("expected no present findings on idle fixture, got %+v", finding)
		}
	}
}

func TestQueueDominatedTTFTDetectorAndRecommendation(t *testing.T) {
	report := loadFixtureReport(t, "queue_dominated_ttft_report.json")

	summary := SummarizeReport(report, LatencyFirstIntent)
	finding := mustFindByID(t, summary.Findings, detectorQueueDominatedTTFT)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected queue-dominated TTFT finding to be present, got %+v", finding)
	}
	if finding.Severity != model.SeverityHigh {
		t.Fatalf("expected high severity, got %+v", finding)
	}
}

func TestUnderutilizedDetectorAndRecommendation(t *testing.T) {
	report := loadFixtureReport(t, "underutilized_active_report.json")

	summary := SummarizeReport(report, ThroughputFirstIntent)
	finding := mustFindByID(t, summary.Findings, detectorUnderutilizedGPUOrConservativeBatch)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected underutilized finding to be present, got %+v", finding)
	}
	if finding.Severity != model.SeverityHigh {
		t.Fatalf("expected high severity, got %+v", finding)
	}
}

func TestKVCachePressureDetectorAndRecommendation(t *testing.T) {
	report := loadFixtureReport(t, "kv_cache_pressure_preemptions_report.json")

	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorKVCachePressurePreemptions)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected kv cache pressure finding to be present, got %+v", finding)
	}
	if finding.Severity != model.SeverityCritical {
		t.Fatalf("expected critical severity, got %+v", finding)
	}
}

func TestAnalysisReportSerializationIncludesSummary(t *testing.T) {
	report := loadFixtureReport(t, "underutilized_active_report.json")
	report.AnalysisSummary = SummarizeReport(report, BalancedIntent)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	var decoded model.AnalysisReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded.AnalysisSummary == nil {
		t.Fatalf("expected analysis summary after roundtrip")
	}
	if len(decoded.AnalysisSummary.Findings) != 11 {
		t.Fatalf("expected findings after roundtrip, got %+v", decoded.AnalysisSummary)
	}
}

func TestThroughputSaturationWithQueuePressureDetector(t *testing.T) {
	report := loadFixtureReport(t, "throughput_saturation_with_queue_pressure_report.json")

	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorThroughputSaturationWithQueuePressure)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected saturation finding to be present, got %+v", finding)
	}
	if finding.Severity != model.SeverityCritical {
		t.Fatalf("expected critical severity, got %+v", finding)
	}
}

func TestWorkloadProfileIntentDoesNotSuppressObservedFindings(t *testing.T) {
	report := loadFixtureReport(t, "throughput_saturation_with_queue_pressure_report.json")
	report.WorkloadProfile = &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		Objective:      string(LatencyFirstIntent),
		ServingPattern: model.ServingPatternRealtimeChat,
		TaskPattern:    model.TaskPatternMixed,
		PrefixReuse:    model.WorkloadProfileReuseUnknown,
		MediaReuse:     model.WorkloadProfileReuseHigh,
	}

	summary := SummarizeReport(report, LatencyFirstIntent)
	finding := mustFindByID(t, summary.Findings, detectorThroughputSaturationWithQueuePressure)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected observed finding to remain present, got %+v", finding)
	}
}

func TestPrefixCacheIneffectiveDetector(t *testing.T) {
	report := syntheticReport(40, map[string]float64{
		"gpu_utilization_pct":                     45,
		"vllm:num_requests_running":               2,
		"vllm:num_requests_waiting":               1,
		"vllm:request_success_total":              100,
		"vllm:prompt_tokens_total":                20000,
		"vllm:generation_tokens_total":            12000,
		"vllm:request_prefill_time_seconds_sum":   80,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:prefix_cache_queries_total":         100,
		"vllm:prefix_cache_hits_total":            10,
	}, map[string]float64{
		"gpu_utilization_pct":                     48,
		"vllm:num_requests_running":               2,
		"vllm:num_requests_waiting":               1,
		"vllm:request_success_total":              140,
		"vllm:prompt_tokens_total":                28000,
		"vllm:generation_tokens_total":            17000,
		"vllm:request_prefill_time_seconds_sum":   116,
		"vllm:request_prefill_time_seconds_count": 140,
		"vllm:prefix_cache_queries_total":         160,
		"vllm:prefix_cache_hits_total":            12,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorPrefixCacheIneffective)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected prefix cache finding to be present, got %+v", finding)
	}
}

func TestPromptRecomputationThrashingDetector(t *testing.T) {
	report := loadFixtureReport(t, "kv_cache_pressure_preemptions_report.json")
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorPromptRecomputationThrashing)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected prompt recomputation thrashing finding to be present, got %+v", finding)
	}
}

func TestPrefillHeavyWorkloadDetector(t *testing.T) {
	report := syntheticReport(35, map[string]float64{
		"gpu_utilization_pct":                     52,
		"vllm:num_requests_running":               2,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              100,
		"vllm:prompt_tokens_total":                50000,
		"vllm:generation_tokens_total":            10000,
		"vllm:request_prefill_time_seconds_sum":   100,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:request_decode_time_seconds_sum":    20,
		"vllm:request_decode_time_seconds_count":  100,
	}, map[string]float64{
		"gpu_utilization_pct":                     55,
		"vllm:num_requests_running":               2,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              140,
		"vllm:prompt_tokens_total":                70000,
		"vllm:generation_tokens_total":            14000,
		"vllm:request_prefill_time_seconds_sum":   160,
		"vllm:request_prefill_time_seconds_count": 140,
		"vllm:request_decode_time_seconds_sum":    34,
		"vllm:request_decode_time_seconds_count":  140,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorPrefillHeavyWorkload)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected prefill-heavy finding to be present, got %+v", finding)
	}
}

func TestDecodeBoundGenerationDetector(t *testing.T) {
	report := syntheticReport(35, map[string]float64{
		"gpu_utilization_pct":                     58,
		"vllm:num_requests_running":               3,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              100,
		"vllm:prompt_tokens_total":                10000,
		"vllm:generation_tokens_total":            50000,
		"vllm:request_prefill_time_seconds_sum":   20,
		"vllm:request_prefill_time_seconds_count": 100,
		"vllm:request_decode_time_seconds_sum":    100,
		"vllm:request_decode_time_seconds_count":  100,
	}, map[string]float64{
		"gpu_utilization_pct":                     60,
		"vllm:num_requests_running":               3,
		"vllm:num_requests_waiting":               0,
		"vllm:request_success_total":              140,
		"vllm:prompt_tokens_total":                14000,
		"vllm:generation_tokens_total":            70000,
		"vllm:request_prefill_time_seconds_sum":   30,
		"vllm:request_prefill_time_seconds_count": 140,
		"vllm:request_decode_time_seconds_sum":    160,
		"vllm:request_decode_time_seconds_count":  140,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorDecodeBoundGeneration)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected decode-bound finding to be present, got %+v", finding)
	}
}

func TestCPUOrHostBottleneckDetector(t *testing.T) {
	report := syntheticReport(88, map[string]float64{
		"gpu_utilization_pct":                    32,
		"vllm:num_requests_running":              1,
		"vllm:num_requests_waiting":              2,
		"vllm:request_success_total":             100,
		"vllm:prompt_tokens_total":               12000,
		"vllm:generation_tokens_total":           10000,
		"vllm:time_to_first_token_seconds_sum":   200,
		"vllm:time_to_first_token_seconds_count": 100,
	}, map[string]float64{
		"gpu_utilization_pct":                    36,
		"vllm:num_requests_running":              1,
		"vllm:num_requests_waiting":              4,
		"vllm:request_success_total":             130,
		"vllm:prompt_tokens_total":               18000,
		"vllm:generation_tokens_total":           14500,
		"vllm:time_to_first_token_seconds_sum":   290,
		"vllm:time_to_first_token_seconds_count": 130,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorCPUOrHostBottleneck)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected cpu bottleneck finding to be present, got %+v", finding)
	}
}

func TestGPUMemorySaturationWithoutThroughputDetector(t *testing.T) {
	report := syntheticReport(35, map[string]float64{
		"gpu_utilization_pct":        40,
		"vllm:num_requests_running":  1,
		"vllm:num_requests_waiting":  0,
		"vllm:request_success_total": 100,
		"gpu_fb_used_bytes":          96,
		"gpu_fb_free_bytes":          4,
	}, map[string]float64{
		"gpu_utilization_pct":        42,
		"vllm:num_requests_running":  1,
		"vllm:num_requests_waiting":  0,
		"vllm:request_success_total": 130,
		"gpu_fb_used_bytes":          97,
		"gpu_fb_free_bytes":          3,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorGPUMemorySaturation)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected gpu memory saturation finding to be present, got %+v", finding)
	}
}

func TestGPUHardwareInstabilityDetector(t *testing.T) {
	report := syntheticReport(40, map[string]float64{
		"gpu_utilization_pct":        70,
		"vllm:request_success_total": 100,
		"DCGM_FI_DEV_XID_ERRORS":     0,
	}, map[string]float64{
		"gpu_utilization_pct":        72,
		"vllm:request_success_total": 130,
		"DCGM_FI_DEV_XID_ERRORS":     2,
	})
	summary := SummarizeReport(report, BalancedIntent)
	finding := mustFindByID(t, summary.Findings, detectorGPUHardwareInstability)
	if finding.Status != model.FindingStatusPresent {
		t.Fatalf("expected gpu hardware instability finding to be present, got %+v", finding)
	}
}

func TestFindingsAreRankedWithHeuristicImprovement(t *testing.T) {
	report := loadFixtureReport(t, "throughput_saturation_with_queue_pressure_report.json")
	summary := SummarizeReport(report, BalancedIntent)
	if summary.TotalHeuristicImprovementPct <= 0 {
		t.Fatalf("expected total heuristic improvement pct, got %+v", summary)
	}
	if len(summary.Findings) == 0 || summary.Findings[0].Rank != 1 {
		t.Fatalf("expected ranked findings, got %+v", summary.Findings)
	}
	if summary.Findings[0].Status != model.FindingStatusPresent {
		t.Fatalf("expected present findings to sort first, got %+v", summary.Findings[0])
	}
	if summary.Findings[0].HeuristicImprovementPct <= 0 || summary.Findings[0].ImportanceScore <= 0 {
		t.Fatalf("expected heuristic metrics on ranked finding, got %+v", summary.Findings[0])
	}
}

func loadFixtureReport(t *testing.T, name string) *model.AnalysisReport {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var report model.AnalysisReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return &report
}

func mustFindByID(t *testing.T, findings []model.Finding, id string) model.Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.ID == id {
			return finding
		}
	}
	t.Fatalf("finding %q not found in %+v", id, findings)
	return model.Finding{}
}

func syntheticReport(cpuUtil float64, first, second map[string]float64) *model.AnalysisReport {
	return &model.AnalysisReport{
		OSInformation: model.OSInformation{
			AverageCPUUtilizationPct: cpuUtil,
		},
		GPUInformation: model.GPUInformation{
			GPUModel:       "H100",
			UtilizationPct: first["gpu_utilization_pct"],
		},
		CollectedMetrics: []model.CollectedMetricPoint{
			{TimeLabel: "2026-03-21T10:00:00Z", Metrics: first},
			{TimeLabel: "2026-03-21T10:01:00Z", Metrics: second},
		},
	}
}
