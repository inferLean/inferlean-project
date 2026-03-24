package recommender

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/model"
)

func TestSelectBestMeasurementLatencyFirstRespectsThroughputGuardrail(t *testing.T) {
	profile := corpusProfile{
		Measurements: []corpusMeasurement{
			{
				Parameters: map[string]float64{"max_num_seqs": 8},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 1000,
					TTFTMs:                    320,
					LatencyP50Ms:              1000,
					LatencyP95Ms:              1300,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 10},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 700,
					TTFTMs:                    240,
					LatencyP50Ms:              720,
					LatencyP95Ms:              910,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 12},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 830,
					TTFTMs:                    265,
					LatencyP50Ms:              790,
					LatencyP95Ms:              980,
				},
			},
		},
	}
	baseline := &measurementSelection{Measurement: profile.Measurements[0], Exact: true}

	best, warning := selectBestMeasurement(profile, LatencyFirstObjective, defaultGuardrailPolicy(LatencyFirstObjective), baseline, nil)
	if warning != "" {
		t.Fatalf("expected viable guarded candidate, got warning %q", warning)
	}
	if best == nil {
		t.Fatalf("expected guarded candidate")
	}
	if got := best.Measurement.Parameters["max_num_seqs"]; got != 12 {
		t.Fatalf("expected guardrail-safe candidate to win, got %+v", best.Measurement.Parameters)
	}
}

func TestSelectBestMeasurementThroughputFirstRespectsLatencyGuardrail(t *testing.T) {
	profile := corpusProfile{
		Measurements: []corpusMeasurement{
			{
				Parameters: map[string]float64{"max_num_seqs": 8},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 1000,
					GPUUtilizationPct:         40,
					LatencyP50Ms:              1000,
					LatencyP95Ms:              1350,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 12},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 1450,
					GPUUtilizationPct:         60,
					LatencyP50Ms:              1400,
					LatencyP95Ms:              1850,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 10},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 1320,
					GPUUtilizationPct:         54,
					LatencyP50Ms:              1230,
					LatencyP95Ms:              1610,
				},
			},
		},
	}
	baseline := &measurementSelection{Measurement: profile.Measurements[0], Exact: true}

	best, warning := selectBestMeasurement(profile, ThroughputFirstObjective, defaultGuardrailPolicy(ThroughputFirstObjective), baseline, nil)
	if warning != "" {
		t.Fatalf("expected viable guarded candidate, got warning %q", warning)
	}
	if best == nil {
		t.Fatalf("expected guarded candidate")
	}
	if got := best.Measurement.Parameters["max_num_seqs"]; got != 10 {
		t.Fatalf("expected latency-safe throughput candidate to win, got %+v", best.Measurement.Parameters)
	}
}

func TestSelectBestMeasurementWarnsWhenNoCandidateSatisfiesGuardrail(t *testing.T) {
	profile := corpusProfile{
		Measurements: []corpusMeasurement{
			{
				Parameters: map[string]float64{"max_num_seqs": 8},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 1000,
					LatencyP50Ms:              1000,
					LatencyP95Ms:              1300,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 10},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 720,
					LatencyP50Ms:              760,
					LatencyP95Ms:              940,
				},
			},
			{
				Parameters: map[string]float64{"max_num_seqs": 12},
				Metrics: corpusMetrics{
					ThroughputTokensPerSecond: 650,
					LatencyP50Ms:              710,
					LatencyP95Ms:              900,
				},
			},
		},
	}
	baseline := &measurementSelection{Measurement: profile.Measurements[0], Exact: true}

	best, warning := selectBestMeasurement(profile, LatencyFirstObjective, defaultGuardrailPolicy(LatencyFirstObjective), baseline, nil)
	if best == nil || best.Measurement.Parameters["max_num_seqs"] != 8 {
		t.Fatalf("expected selector to keep the guarded baseline, got %+v", best)
	}
	if !strings.Contains(warning, "latency-priority guardrail") {
		t.Fatalf("expected guardrail warning, got %q", warning)
	}
}

func TestRecommendMatchesCorpusAndBuildsExactRecommendation(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	corpusPath := filepath.Join(tmp, "corpus.json")

	report := throughputHeadroomAnalysisReport()
	mustWriteJSON(t, analysisPath, report)
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
          "parameters": {"max_num_seqs": 12, "max_num_batched_tokens": 12288},
          "metrics": {"throughput_tokens_per_second": 5200, "ttft_ms": 690, "latency_p50_ms": 1520, "latency_p95_ms": 2250, "gpu_utilization_pct": 33}
        },
        {
          "parameters": {"max_num_seqs": 16, "max_num_batched_tokens": 16384},
          "metrics": {"throughput_tokens_per_second": 6100, "ttft_ms": 760, "latency_p50_ms": 1650, "latency_p95_ms": 2440, "gpu_utilization_pct": 44}
        }
      ]
    }
  ]
}`)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		CorpusPath:   corpusPath,
		Now:          time.Date(2026, 3, 21, 15, 0, 0, 0, time.UTC),
		Objective:    ThroughputFirstObjective,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}

	if recommendationReport.MatchedCorpusProfile == nil {
		t.Fatalf("expected matched corpus profile")
	}
	if recommendationReport.BaselinePrediction == nil || recommendationReport.BaselinePrediction.ThroughputTokensPerSecond != 4200 {
		t.Fatalf("expected exact corpus baseline, got %+v", recommendationReport.BaselinePrediction)
	}
	if len(recommendationReport.Recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %+v", recommendationReport.Recommendations)
	}
	rec := recommendationReport.Recommendations[0]
	if !strings.Contains(rec.Summary, "max_num_seqs=16") {
		t.Fatalf("expected concise exact summary, got %q", rec.Summary)
	}
	if len(rec.Changes) != 2 {
		t.Fatalf("expected exact parameter changes, got %+v", rec.Changes)
	}
	if rec.PredictedEffect.ThroughputDeltaPct <= 0 {
		t.Fatalf("expected positive throughput delta, got %+v", rec.PredictedEffect)
	}
	if recommendationReport.CapacityOpportunity == nil {
		t.Fatalf("expected capacity opportunity")
	}
	if recommendationReport.CapacityOpportunity.RecoverableGPULoadPct <= 0 {
		t.Fatalf("expected positive recoverable gpu load, got %+v", recommendationReport.CapacityOpportunity)
	}
	if recommendationReport.WastedCapacity == nil || strings.TrimSpace(recommendationReport.WastedCapacity.Headline) == "" {
		t.Fatalf("expected wasted_capacity summary, got %+v", recommendationReport.WastedCapacity)
	}
	if recommendationReport.PrimaryAction == nil || len(recommendationReport.PrimaryAction.RollbackValues) == 0 {
		t.Fatalf("expected primary_action with rollback values, got %+v", recommendationReport.PrimaryAction)
	}
	if recommendationReport.PredictedImpact == nil || recommendationReport.PredictedImpact.GPUUtilizationPct.After == nil {
		t.Fatalf("expected predicted impact summary, got %+v", recommendationReport.PredictedImpact)
	}
}

func TestRecommendAddsAlternativeBenchmarkBackedRecommendations(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	corpusPath := filepath.Join(tmp, "corpus.json")

	mustWriteJSON(t, analysisPath, throughputHeadroomAnalysisReport())
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
    },
    {
      "id": "qwen3-30b-h100x4-balanced",
      "model_name": "Qwen 3 30B A3B",
      "model_family": "qwen3",
      "gpu_count": 4,
      "hardware_class": "h100",
      "workload_class": "balanced",
      "measurements": [
        {
          "parameters": {"max_num_seqs": 8, "max_num_batched_tokens": 8192},
          "metrics": {"throughput_tokens_per_second": 4200, "ttft_ms": 620, "latency_p50_ms": 1450, "latency_p95_ms": 2100, "gpu_utilization_pct": 24}
        },
        {
          "parameters": {"max_num_seqs": 12, "max_num_batched_tokens": 12288},
          "metrics": {"throughput_tokens_per_second": 5200, "ttft_ms": 690, "latency_p50_ms": 1520, "latency_p95_ms": 2250, "gpu_utilization_pct": 33}
        }
      ]
    }
  ]
}`)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		CorpusPath:   corpusPath,
		Objective:    ThroughputFirstObjective,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if len(recommendationReport.Recommendations) != 2 {
		t.Fatalf("expected two benchmark-backed recommendations, got %+v", recommendationReport.Recommendations)
	}
	if recommendationReport.Recommendations[0].Priority != 1 || recommendationReport.Recommendations[1].Priority != 2 {
		t.Fatalf("expected sequential priorities, got %+v", recommendationReport.Recommendations)
	}
	if recommendationReport.AlternativeActions == nil || len(recommendationReport.AlternativeActions) != 1 {
		t.Fatalf("expected one alternative action summary, got %+v", recommendationReport.AlternativeActions)
	}
	if !strings.Contains(recommendationReport.Recommendations[1].Basis, "qwen3-30b-h100x4-balanced") {
		t.Fatalf("expected alternative recommendation to reference second profile, got %+v", recommendationReport.Recommendations[1])
	}
}

func TestRecommendAddsScenarioPredictionForExplicitSet(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	corpusPath := filepath.Join(tmp, "corpus.json")
	mustWriteJSON(t, analysisPath, throughputHeadroomAnalysisReport())
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
          "parameters": {"max_num_seqs": 12, "max_num_batched_tokens": 12288},
          "metrics": {"throughput_tokens_per_second": 5200, "ttft_ms": 690, "latency_p50_ms": 1520, "latency_p95_ms": 2250, "gpu_utilization_pct": 33}
        }
      ]
    }
  ]
}`)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		CorpusPath:   corpusPath,
		Objective:    ThroughputFirstObjective,
		ScenarioSet: map[string]float64{
			"max_num_seqs":           12,
			"max_num_batched_tokens": 12288,
		},
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if recommendationReport.ScenarioPrediction == nil {
		t.Fatalf("expected scenario prediction")
	}
	if recommendationReport.ScenarioPrediction.ThroughputTokensPerSecond != 5200 {
		t.Fatalf("unexpected scenario prediction: %+v", recommendationReport.ScenarioPrediction)
	}
}

func TestRecommendFallsBackWithoutCorpusMatch(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	mustWriteJSON(t, analysisPath, throughputHeadroomAnalysisReport())

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		Objective:    ThroughputFirstObjective,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if len(recommendationReport.Recommendations) != 1 {
		t.Fatalf("expected fallback recommendation, got %+v", recommendationReport.Recommendations)
	}
	if !strings.Contains(recommendationReport.Recommendations[0].Basis, "Rule-based fallback") {
		t.Fatalf("expected fallback basis, got %+v", recommendationReport.Recommendations[0])
	}
	if recommendationReport.PrimaryAction == nil || recommendationReport.PrimaryAction.Confidence <= 0 {
		t.Fatalf("expected summary action even for fallback json output, got %+v", recommendationReport.PrimaryAction)
	}
}

func TestRecommendAddsPrefixCacheToggleWhenDeclaredReuseIsHigh(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")

	report := throughputHeadroomAnalysisReport()
	report.WorkloadProfile = &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		ServingPattern: model.ServingPatternRealtimeChat,
		Objective:      string(LatencyFirstObjective),
		PrefixReuse:    model.WorkloadProfileReuseHigh,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}
	report.CurrentVLLMConfigurations["enable_prefix_caching"] = false
	report.AnalysisSummary.Findings = []model.Finding{
		{
			ID:         "prefix_cache_ineffective",
			Status:     model.FindingStatusPresent,
			Severity:   model.SeverityMedium,
			Confidence: 0.88,
			Summary:    "Prefix cache hit rate stayed low.",
		},
	}
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	found := false
	for _, item := range recommendationReport.Recommendations {
		if item.ID != "rule_enable_prefix_caching" {
			continue
		}
		found = true
		if len(item.Changes) != 1 || item.Changes[0].Name != "enable_prefix_caching" || item.Changes[0].RecommendedValue != true {
			t.Fatalf("expected prefix cache toggle change, got %+v", item.Changes)
		}
	}
	if !found {
		t.Fatalf("expected prefix cache recommendation, got %+v", recommendationReport.Recommendations)
	}
}

func TestRecommendAddsMultimodalCacheToggleWhenDeclaredReuseIsHigh(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")

	report := throughputHeadroomAnalysisReport()
	report.WorkloadProfile = &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		ServingPattern: model.ServingPatternRealtimeChat,
		Objective:      string(BalancedObjective),
		PrefixReuse:    model.WorkloadProfileReuseUnknown,
		MediaReuse:     model.WorkloadProfileReuseHigh,
	}
	report.CurrentVLLMConfigurations["model_name"] = "Qwen2-VL-7B-Instruct"
	report.CurrentVLLMConfigurations["disable_mm_preprocessor_cache"] = true
	report.FeatureSummary = &model.FeatureSummary{
		TrafficObserved:      true,
		MultimodalLikely:     true,
		AvgGPUUtilizationPct: 22,
	}
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	found := false
	for _, item := range recommendationReport.Recommendations {
		if item.ID != "rule_enable_mm_preprocessor_cache" {
			continue
		}
		found = true
		if len(item.Changes) != 1 || item.Changes[0].Name != "disable_mm_preprocessor_cache" || item.Changes[0].RecommendedValue != false {
			t.Fatalf("expected multimodal cache toggle change, got %+v", item.Changes)
		}
	}
	if !found {
		t.Fatalf("expected multimodal cache recommendation, got %+v", recommendationReport.Recommendations)
	}
}

func TestBuildCapacityOpportunityOmitsBlockWithoutPredictedGPUUtilization(t *testing.T) {
	report := throughputHeadroomAnalysisReport()
	recommendationReport := &model.RecommendationReport{
		Recommendations: []model.RecommendationItem{
			{
				ID:       "no_gpu_prediction",
				Priority: 1,
				Basis:    "test",
				PredictedEffect: model.PredictedEffect{
					ThroughputDeltaPct: 10,
				},
				Confidence: 0.7,
			},
		},
	}

	got := buildCapacityOpportunity(report, recommendationReport)
	if got == nil {
		t.Fatalf("expected fallback capacity opportunity when throughput uplift is available")
	}
	if got.RecoverableGPULoadPct <= 0 || got.RecoverableGPUCount <= 0 {
		t.Fatalf("expected positive derived gpu headroom, got %+v", got)
	}
	if !strings.Contains(got.Basis, "Estimated GPU utilization from predicted throughput uplift") {
		t.Fatalf("expected derived basis note, got %+v", got)
	}
}

func TestRecommendPreservesCanonicalOutputWhenLLMEnhancementFails(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	corpusPath := filepath.Join(tmp, "corpus.json")
	mustWriteJSON(t, analysisPath, throughputHeadroomAnalysisReport())
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("INFERLEAN_LLM_BASE_URL", server.URL)
	t.Setenv("INFERLEAN_LLM_API_KEY", "test-key")
	t.Setenv("INFERLEAN_LLM_MODEL", "test-model")

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		CorpusPath:   corpusPath,
		Objective:    ThroughputFirstObjective,
		LLMEnhance:   true,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if len(recommendationReport.Recommendations) != 1 {
		t.Fatalf("expected canonical recommendation to remain present, got %+v", recommendationReport.Recommendations)
	}
	if recommendationReport.LLMEnhanced != nil {
		t.Fatalf("expected llm_enhanced to remain nil on failure, got %+v", recommendationReport.LLMEnhanced)
	}
	if len(recommendationReport.Warnings) == 0 || !strings.Contains(strings.Join(recommendationReport.Warnings, " "), "llm enhancement skipped") {
		t.Fatalf("expected llm warning, got %+v", recommendationReport.Warnings)
	}
}

func TestRecommendReturnsNonActionableForIdleTraffic(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	report := idleAnalysisReport()
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if len(recommendationReport.Recommendations) != 0 {
		t.Fatalf("expected no recommendations for idle traffic, got %+v", recommendationReport.Recommendations)
	}
	if len(recommendationReport.Warnings) == 0 || !strings.Contains(recommendationReport.Warnings[0], "no live traffic") {
		t.Fatalf("expected idle traffic warning, got %+v", recommendationReport.Warnings)
	}
	if recommendationReport.WastedCapacity != nil {
		t.Fatalf("expected no wasted capacity for idle traffic, got %+v", recommendationReport.WastedCapacity)
	}
}

func TestRecommendUsesWorkloadProfileObjectiveWhenExplicitObjectiveMissing(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	report := throughputHeadroomAnalysisReport()
	report.WorkloadProfile = &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		ServingPattern: model.ServingPatternRealtimeChat,
		TaskPattern:    model.TaskPatternMixed,
		Objective:      string(LatencyFirstObjective),
		PrefixReuse:    model.WorkloadProfileReuseHigh,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}
	report.AnalysisSummary.WorkloadIntent = string(ThroughputFirstObjective)
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if recommendationReport.Objective != string(LatencyFirstObjective) {
		t.Fatalf("expected objective to come from workload profile, got %+v", recommendationReport)
	}
	if recommendationReport.DeclaredGoal == nil || recommendationReport.DeclaredGoal.Value != string(LatencyFirstObjective) || recommendationReport.DeclaredGoal.Source != "intent_file" {
		t.Fatalf("expected declared goal from user intent, got %+v", recommendationReport.DeclaredGoal)
	}
	if recommendationReport.Guardrail == nil || recommendationReport.Guardrail.MinThroughputRetentionPct == nil || *recommendationReport.Guardrail.MinThroughputRetentionPct != 80 {
		t.Fatalf("expected latency guardrail summary, got %+v", recommendationReport.Guardrail)
	}
}

func TestRecommendExplicitObjectiveOverridesWorkloadProfile(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	report := throughputHeadroomAnalysisReport()
	report.WorkloadProfile = &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		ServingPattern: model.ServingPatternRealtimeChat,
		TaskPattern:    model.TaskPatternMixed,
		Objective:      string(LatencyFirstObjective),
		PrefixReuse:    model.WorkloadProfileReuseHigh,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
		Objective:    ThroughputFirstObjective,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if recommendationReport.Objective != string(ThroughputFirstObjective) {
		t.Fatalf("expected explicit objective to win, got %+v", recommendationReport)
	}
}

func TestRecommendFallsBackToBalancedWithoutProfileOrSummary(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
	report := throughputHeadroomAnalysisReport()
	report.WorkloadProfile = nil
	report.AnalysisSummary = nil
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if recommendationReport.Objective != string(BalancedObjective) {
		t.Fatalf("expected balanced fallback objective, got %+v", recommendationReport)
	}
}

func TestRecommendNormalizesCollectorOnlyAnalysisReport(t *testing.T) {
	tmp := t.TempDir()
	analysisPath := filepath.Join(tmp, "analysis.json")
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
					"vllm:num_requests_running":               53,
					"vllm:request_success_total":              6786,
					"vllm:prompt_tokens_total":                6190000,
					"vllm:generation_tokens_total":            169000,
					"vllm:time_to_first_token_seconds_count":  2,
					"vllm:time_to_first_token_seconds_sum":    1.4,
					"vllm:request_queue_time_seconds_count":   2,
					"vllm:request_queue_time_seconds_sum":     0.4,
					"vllm:request_prefill_time_seconds_count": 2,
					"vllm:request_prefill_time_seconds_sum":   0.6,
					"vllm:request_decode_time_seconds_count":  2,
					"vllm:request_decode_time_seconds_sum":    0.9,
				},
			},
			{
				TimeLabel: "2026-03-21T15:06:22Z",
				Metrics: map[string]float64{
					"vllm:num_requests_running":               53,
					"vllm:request_success_total":              6790,
					"vllm:prompt_tokens_total":                6196902,
					"vllm:generation_tokens_total":            169969,
					"vllm:time_to_first_token_seconds_count":  4,
					"vllm:time_to_first_token_seconds_sum":    2.9,
					"vllm:request_queue_time_seconds_count":   4,
					"vllm:request_queue_time_seconds_sum":     0.8,
					"vllm:request_prefill_time_seconds_count": 4,
					"vllm:request_prefill_time_seconds_sum":   1.2,
					"vllm:request_decode_time_seconds_count":  4,
					"vllm:request_decode_time_seconds_sum":    1.8,
				},
			},
		},
	}
	mustWriteJSON(t, analysisPath, report)

	recommendationReport, err := Recommend(context.Background(), Options{
		AnalysisPath: analysisPath,
	})
	if err != nil {
		t.Fatalf("recommend returned error: %v", err)
	}
	if recommendationReport.Objective != string(BalancedObjective) {
		t.Fatalf("expected balanced fallback objective, got %+v", recommendationReport)
	}
	if recommendationReport.BaselinePrediction == nil {
		t.Fatalf("expected observed baseline prediction to be synthesized")
	}
	if recommendationReport.BaselinePrediction.ThroughputTokensPerSecond <= 0 {
		t.Fatalf("expected positive synthesized throughput, got %+v", recommendationReport.BaselinePrediction)
	}
	if len(recommendationReport.Warnings) == 0 || !strings.Contains(strings.Join(recommendationReport.Warnings, " "), "current_vllm_configurations") {
		t.Fatalf("expected missing-config compatibility warning, got %+v", recommendationReport.Warnings)
	}
}

func throughputHeadroomAnalysisReport() *model.AnalysisReport {
	report := &model.AnalysisReport{
		SchemaVersion: model.AnalysisSchemaVersion,
		GeneratedAt:   time.Date(2026, 3, 21, 14, 20, 0, 0, time.UTC),
		ToolName:      model.ToolName,
		ToolVersion:   model.ToolVersion,
		GPUInformation: model.GPUInformation{
			GPUModel:       "H100",
			Company:        "NVIDIA",
			VRAMSizeBytes:  80 * 1024 * 1024 * 1024,
			UtilizationPct: 21,
		},
		VLLMInformation: model.VLLMInformation{
			VLLMVersion:           "0.18.0",
			ConfigurationLocation: "/etc/vllm/config.json",
			InstallationType:      "host",
		},
		CollectedMetrics: []model.CollectedMetricPoint{
			{
				TimeLabel: "2026-03-21T14:10:00Z",
				Metrics: map[string]float64{
					"gpu_utilization_pct":                     18,
					"vllm:num_requests_running":               1,
					"vllm:num_requests_waiting":               0,
					"vllm:request_success_total":              100,
					"vllm:generation_tokens_total":            10000,
					"vllm:prompt_tokens_total":                5000,
					"vllm:time_to_first_token_seconds_sum":    50,
					"vllm:time_to_first_token_seconds_count":  100,
					"vllm:request_queue_time_seconds_sum":     5,
					"vllm:request_queue_time_seconds_count":   100,
					"vllm:request_prefill_time_seconds_sum":   20,
					"vllm:request_prefill_time_seconds_count": 100,
					"vllm:request_decode_time_seconds_sum":    30,
					"vllm:request_decode_time_seconds_count":  100,
				},
			},
			{
				TimeLabel: "2026-03-21T14:11:00Z",
				Metrics: map[string]float64{
					"gpu_utilization_pct":                     22,
					"vllm:num_requests_running":               1.2,
					"vllm:num_requests_waiting":               0,
					"vllm:request_success_total":              140,
					"vllm:generation_tokens_total":            16000,
					"vllm:prompt_tokens_total":                8000,
					"vllm:time_to_first_token_seconds_sum":    72,
					"vllm:time_to_first_token_seconds_count":  140,
					"vllm:request_queue_time_seconds_sum":     7,
					"vllm:request_queue_time_seconds_count":   140,
					"vllm:request_prefill_time_seconds_sum":   28,
					"vllm:request_prefill_time_seconds_count": 140,
					"vllm:request_decode_time_seconds_sum":    44,
					"vllm:request_decode_time_seconds_count":  140,
				},
			},
		},
		CurrentVLLMConfigurations: map[string]any{
			"model_name":             "Qwen 3 30B A3B",
			"max_num_seqs":           8,
			"max_num_batched_tokens": 8192,
			"tensor_parallel_size":   4,
		},
	}
	report.AnalysisSummary = analyzer.SummarizeReport(report, analyzer.ThroughputFirstIntent)
	return report
}

func idleAnalysisReport() *model.AnalysisReport {
	report := &model.AnalysisReport{
		SchemaVersion: model.AnalysisSchemaVersion,
		GeneratedAt:   time.Date(2026, 3, 21, 14, 0, 0, 0, time.UTC),
		ToolName:      model.ToolName,
		ToolVersion:   model.ToolVersion,
		GPUInformation: model.GPUInformation{
			GPUModel: "H100",
			Company:  "NVIDIA",
		},
		CollectedMetrics: []model.CollectedMetricPoint{
			{
				TimeLabel: "2026-03-21T14:00:00Z",
				Metrics: map[string]float64{
					"gpu_utilization_pct":                    0,
					"vllm:num_requests_running":              0,
					"vllm:num_requests_waiting":              0,
					"vllm:request_success_total":             0,
					"vllm:generation_tokens_total":           0,
					"vllm:time_to_first_token_seconds_sum":   0,
					"vllm:time_to_first_token_seconds_count": 0,
					"vllm:request_queue_time_seconds_sum":    0,
					"vllm:request_queue_time_seconds_count":  0,
				},
			},
			{
				TimeLabel: "2026-03-21T14:01:00Z",
				Metrics: map[string]float64{
					"gpu_utilization_pct":                    0,
					"vllm:num_requests_running":              0,
					"vllm:num_requests_waiting":              0,
					"vllm:request_success_total":             0,
					"vllm:generation_tokens_total":           0,
					"vllm:time_to_first_token_seconds_sum":   0,
					"vllm:time_to_first_token_seconds_count": 0,
					"vllm:request_queue_time_seconds_sum":    0,
					"vllm:request_queue_time_seconds_count":  0,
				},
			},
		},
		CurrentVLLMConfigurations: map[string]any{
			"model_name": "Qwen 3 30B A3B",
		},
	}
	report.AnalysisSummary = analyzer.SummarizeReport(report, analyzer.BalancedIntent)
	return report
}

func mustWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
