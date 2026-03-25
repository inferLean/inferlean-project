package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/inferLean/inferlean-project/internal/model"
)

func TestBuildRecommendationSnapshotPromotesPremiumSummary(t *testing.T) {
	reqRPS := 4.2
	p50 := 1800.0
	recommendation := &model.RecommendationReport{
		DeclaredGoal: &model.DeclaredGoalSummary{
			Value:  "latency_first",
			Source: "intent_file",
		},
		Guardrail: &model.GuardrailSummary{
			MinThroughputRetentionPct: ptr(80),
		},
		CurrentServiceState: &model.ServiceSummary{
			RequestRateRPS: &reqRPS,
			RequestLatencyMS: model.RequestLatencySummary{
				P50: &p50,
			},
		},
		WastedCapacity: &model.WastedCapacitySummary{
			Headline:         "18.0pp GPU load recoverable (0.7 GPU)",
			GPUHeadroomPct:   ptr(18),
			GPUHeadroomCount: ptr(0.7),
		},
		PrimaryAction: &model.PrimaryActionSummary{
			Summary:        "Apply benchmark-backed tuning: max_num_batched_tokens=4096, max_num_seqs=256",
			Confidence:     0.92,
			RollbackValues: []model.ParameterChange{{Name: "max_num_batched_tokens", RecommendedValue: 512}, {Name: "max_num_seqs", RecommendedValue: 16}},
		},
		PredictedImpact: &model.PredictedImpactSummary{
			RequestRateRPS: model.NumericImpact{After: ptr(12.77), DeltaPct: ptr(204.1)},
			RequestLatencyMS: model.PredictedLatencyMS{
				P50: model.NumericImpact{After: ptr(1520), DeltaPct: ptr(-15.6)},
			},
			GPUUtilizationPct: model.NumericImpact{After: ptr(44), DeltaPct: ptr(83.3)},
		},
		CapacityOpportunity: &model.CapacityOpportunity{
			RecoverableGPULoadPct: 18,
			RecoverableGPUCount:   0.7,
		},
		MatchSummary: &model.MatchSummary{
			ProfileID:  "qwen35_08b_rtx_pro_6000_latency",
			MatchScore: 0.88,
			Basis:      "exact model match, gpu footprint match, workload class match",
		},
	}

	snapshot := buildRecommendationSnapshot(nil, recommendation)

	if snapshot.WastedCapacityLabel != "GPU Load Headroom" {
		t.Fatalf("expected gpu-led wasted capacity label, got %q", snapshot.WastedCapacityLabel)
	}
	if snapshot.TargetGoal != "Latency-priority | keep throughput >= 80% of current" {
		t.Fatalf("expected target goal summary, got %q", snapshot.TargetGoal)
	}
	if snapshot.WastedCapacity != "18.0pp | 0.7 GPU recoverable" {
		t.Fatalf("expected wasted capacity headline, got %q", snapshot.WastedCapacity)
	}
	if !strings.Contains(snapshot.BestAction, "max_num_batched_tokens=4096") {
		t.Fatalf("expected best action summary, got %q", snapshot.BestAction)
	}
	if !strings.Contains(snapshot.ExpectedImpact, "req/s 12.77 (+204.1%)") {
		t.Fatalf("expected req/s impact, got %q", snapshot.ExpectedImpact)
	}
	if snapshot.Warning != "" {
		t.Fatalf("expected no warning, got %q", snapshot.Warning)
	}
}

func TestBuildRecommendationSnapshotSuppressesLowConfidenceAction(t *testing.T) {
	recommendation := &model.RecommendationReport{
		WastedCapacity: &model.WastedCapacitySummary{
			Headline:         "+3.20 req/s recoverable",
			ThroughputGapRPS: ptr(3.2),
			ThroughputGapPct: ptr(25),
		},
		PrimaryAction: &model.PrimaryActionSummary{
			Summary:    "Increase max_num_seqs to 20",
			Confidence: 0.52,
		},
	}

	snapshot := buildRecommendationSnapshot(nil, recommendation)

	if snapshot.WastedCapacityLabel != "Req/s Headroom" {
		t.Fatalf("expected throughput headroom label, got %q", snapshot.WastedCapacityLabel)
	}
	if !strings.Contains(snapshot.WastedCapacity, "+3.20 req/s recoverable") {
		t.Fatalf("expected throughput headroom value, got %q", snapshot.WastedCapacity)
	}
	if snapshot.BestAction != "" {
		t.Fatalf("expected low-confidence action to be hidden, got %q", snapshot.BestAction)
	}
	if snapshot.Warning != "" {
		t.Fatalf("expected no warning, got %q", snapshot.Warning)
	}
}

func TestBuildRunRecommendationSnapshotFallsBackToTopRecommendation(t *testing.T) {
	snapshot := buildRunRecommendationSnapshot(nil, nil, &topRecommendationAPIResponse{
		TopRecommendation: "Apply sample-backed queue tuning.",
		CapacityOpportunity: &model.CapacityOpportunity{
			RecoverableGPULoadPct: 18,
			RecoverableGPUCount:   0.7,
		},
	})

	if snapshot.WastedCapacityLabel != "GPU Load Headroom" {
		t.Fatalf("expected gpu headroom fallback label, got %q", snapshot.WastedCapacityLabel)
	}
	if snapshot.WastedCapacity != "18.0pp | 0.7 GPU recoverable" {
		t.Fatalf("expected gpu headroom fallback value, got %q", snapshot.WastedCapacity)
	}
	if snapshot.BestAction != "Apply sample-backed queue tuning." {
		t.Fatalf("expected top recommendation fallback action, got %q", snapshot.BestAction)
	}
}

func TestRenderRecommendationSummaryCardDoesNotTruncateActionRows(t *testing.T) {
	recommendation := recommendationSnapshot{
		TargetGoal:     "Latency-priority | keep throughput >= 80% of current",
		WastedCapacity: "18.0% | 0.7 GPU recoverable",
		BestAction:     "Apply benchmark-backed tuning: max_num_batched_tokens=4096, max_num_seqs=256, max_num_partial_prefills=4, scheduler_delay_factor=0.2",
		ExpectedImpact: "req/s 12.77 (+204.1%), p50 1.52s (-15.6%), GPU util 44.0% (+83.3%)",
	}

	var out bytes.Buffer
	ui := terminalUI{out: &out, enabled: true, color: false}
	ui.renderRecommendationSummaryCard(recommendation)

	rendered := out.String()
	if strings.Contains(rendered, "...") {
		t.Fatalf("expected recommender card to wrap instead of truncating, got %q", rendered)
	}
	if !strings.Contains(rendered, "0.7 GPU recoverable") {
		t.Fatalf("expected wasted-capacity gpu count in output, got %q", rendered)
	}
	if !strings.Contains(rendered, "Target Goal") || !strings.Contains(rendered, "keep throughput >= 80% of current") {
		t.Fatalf("expected target goal row in output, got %q", rendered)
	}
}

func TestRenderAnalyzeSummaryCardUsesCompactPremiumRows(t *testing.T) {
	report := &model.AnalysisReport{
		ServiceSummary: &model.ServiceSummary{
			RequestRateRPS: ptr(7.5),
			RequestLatencyMS: model.RequestLatencySummary{
				Avg:                  ptr(850),
				P50:                  ptr(700),
				P99:                  ptr(1200),
				PercentilesAvailable: true,
			},
			Queue: model.QueueSummary{
				AvgDelayMS:         ptr(420),
				AvgWaitingRequests: ptr(3.1),
				Health:             "elevated",
			},
			SaturationPct:                ptr(84),
			EstimatedUpperRequestRateRPS: ptr(8.93),
			Bottleneck: model.BottleneckSummary{
				Kind:       "gpu_compute",
				Confidence: 0.91,
			},
			ObservedMode: model.ObservedModeSummary{
				Objective:      "balanced",
				ServingPattern: "realtime",
				Confidence:     0.82,
			},
			ConfiguredIntent: model.ConfiguredIntentSummary{
				Value:      "latency_first",
				Confidence: 0.92,
			},
		},
		CurrentLoadSummary: &model.CurrentLoadSummary{
			DominantGPUResource:    "compute",
			ComputeLoadPct:         84,
			MemoryBandwidthLoadPct: 55,
			CPULoadPct:             70,
		},
	}

	var out bytes.Buffer
	ui := terminalUI{out: &out, enabled: true, color: false}
	ui.RenderAnalyzeSummaryCard(report)

	rendered := out.String()
	for _, want := range []string{"Saturation", "Traffic", "Queue", "Bottleneck", "Observed Traffic", "Observed Behavior", "Configured For"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, rendered)
		}
	}
	if !strings.Contains(rendered, "Interactive realtime") {
		t.Fatalf("expected observed traffic wording, got %q", rendered)
	}
	if !strings.Contains(rendered, "Balanced latency/throughput") {
		t.Fatalf("expected observed behavior wording, got %q", rendered)
	}
	if !strings.Contains(rendered, "Latency-focused") {
		t.Fatalf("expected configured intent mismatch wording, got %q", rendered)
	}
	if !strings.Contains(rendered, "7.50 req/s | avg 850ms, p50 700ms, p99 1.20s") {
		t.Fatalf("expected merged traffic line, got %q", rendered)
	}
	if !strings.Contains(rendered, "Elevated: 84% GPU compute (avg) | headroom to ~8.9 req/s") {
		t.Fatalf("expected marketing saturation headline with dominant resource, got %q", rendered)
	}
	if strings.Contains(rendered, "Detail") || !strings.Contains(rendered, "GPU compute 84%, GPU bandwidth 55%, CPU 70%") {
		t.Fatalf("expected indented saturation continuation line, got %q", rendered)
	}
	if strings.Contains(rendered, "Recoverable Capacity") {
		t.Fatalf("expected analyzer card to omit recommender-only capacity, got %q", rendered)
	}
}

func ptr(value float64) *float64 {
	v := value
	return &v
}
