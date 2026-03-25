package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

func renderAnalysisV2Summary(w io.Writer, report *model.AnalysisReportV2) {
	if report == nil {
		return
	}
	fmt.Fprintln(w, "Current operating point")
	fmt.Fprintf(w, "  Throughput: %s\n", renderThroughput(report.OperatingPoint))
	fmt.Fprintf(w, "  Latency: p50 %s | p95 %s | queue wait %s\n",
		renderMS(report.OperatingPoint.Latency.P50MS),
		renderMS(report.OperatingPoint.Latency.P95MS),
		renderMS(report.OperatingPoint.Latency.QueueWaitMS),
	)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Dominant bottleneck")
	fmt.Fprintf(w, "  %s\n", humanizeToken(report.PressureSummary.DominantBottleneck))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pressure breakdown")
	fmt.Fprintf(w, "  Compute: %s\n", renderPressure(report.PressureSummary.Compute))
	fmt.Fprintf(w, "  Memory bandwidth: %s\n", renderPressure(report.PressureSummary.MemoryBandwidth))
	fmt.Fprintf(w, "  KV/cache: %s\n", renderPressure(report.PressureSummary.KVCache))
	fmt.Fprintf(w, "  Queue: %s\n", renderPressure(report.PressureSummary.Queue))
	fmt.Fprintf(w, "  Host/input pipeline: %s\n", renderPressure(report.PressureSummary.HostInputPipeline))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Frontier proximity")
	fmt.Fprintf(w, "  %s\n", humanizeToken(report.FrontierAssessment.FrontierProximity))
	fmt.Fprintf(w, "  %s\n", strings.TrimSpace(report.FrontierAssessment.FrontierReason))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Evidence quality")
	fmt.Fprintf(w, "  %s\n", humanizeToken(report.EvidenceQuality.Status))
	fmt.Fprintf(w, "  %s\n", strings.TrimSpace(report.EvidenceQuality.Summary))
}

func renderOptimizationV2Summary(w io.Writer, report *model.OptimizationReportV2) {
	if report == nil {
		return
	}
	fmt.Fprintln(w, "Verdict")
	fmt.Fprintf(w, "  %s\n", humanizeDecision(report.PrimaryDecision.Kind))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Why")
	fmt.Fprintf(w, "  %s\n", strings.TrimSpace(report.PrimaryDecision.Reason))
	if effect := strings.TrimSpace(report.PrimaryDecision.ExpectedEffect); effect != "" {
		fmt.Fprintf(w, "  Expected effect: %s\n", effect)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Current operating point")
	fmt.Fprintf(w, "  Throughput: %s\n", renderThroughput(report.OperatingPoint))
	fmt.Fprintf(w, "  Latency: p50 %s | p95 %s | queue wait %s\n",
		renderMS(report.OperatingPoint.Latency.P50MS),
		renderMS(report.OperatingPoint.Latency.P95MS),
		renderMS(report.OperatingPoint.Latency.QueueWaitMS),
	)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Dominant bottleneck")
	fmt.Fprintf(w, "  %s\n", humanizeToken(report.PressureSummary.DominantBottleneck))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Best next move")
	fmt.Fprintf(w, "  Mechanism: %s\n", humanizeToken(report.PrimaryDecision.PrimaryMechanism))
	fmt.Fprintf(w, "  Confidence: %.0f%% (%s)\n", report.PrimaryDecision.Confidence*100, humanizeToken(report.PrimaryDecision.ConfidenceSource))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Alternatives")
	for _, scenario := range []model.ScenarioV2{
		report.Scenarios.ThroughputFirst,
		report.Scenarios.LatencyFirst,
		report.Scenarios.Balanced,
	} {
		fmt.Fprintf(w, "  %s: %s\n", humanizeToken(scenario.ObjectiveMode), humanizeDecision(scenario.DecisionKind))
		if rationale := strings.TrimSpace(scenario.Rationale); rationale != "" {
			fmt.Fprintf(w, "    %s\n", rationale)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Evidence quality")
	if len(report.Access.Redactions) > 0 {
		fmt.Fprintf(w, "  Access: %s (%s)\n", report.Access.Tier, strings.Join(report.Access.Redactions, ", "))
	} else {
		fmt.Fprintf(w, "  Access: %s\n", report.Access.Tier)
	}
	fmt.Fprintf(w, "  Frontier: %s\n", strings.TrimSpace(report.Frontier.FrontierReason))
	if len(report.PrimaryDecision.ExactKnobDeltas) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Exact changes")
		for _, knob := range report.PrimaryDecision.ExactKnobDeltas {
			fmt.Fprintf(w, "  %s: %v -> %v\n", knob.Name, knob.CurrentValue, knob.RecommendedValue)
		}
	}
}

func writeJSONReport(w io.Writer, report any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func renderThroughput(point model.OperatingPointV2) string {
	if point.ThroughputTokensPerSecond != nil {
		return fmt.Sprintf("%.1f tok/s", *point.ThroughputTokensPerSecond)
	}
	if point.RequestRateRPS != nil {
		return fmt.Sprintf("%.1f req/s", *point.RequestRateRPS)
	}
	return "N/A"
}

func renderMS(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.0f ms", *value)
}

func renderPressure(pressure model.PressureDimensionV2) string {
	return fmt.Sprintf("%s (%s, %.0f%% confidence)", humanizeToken(pressure.PressureStatus), pressure.SourceType, pressure.Confidence*100)
}

func humanizeDecision(kind string) string {
	switch strings.TrimSpace(kind) {
	case model.DecisionKindKeepCurrent:
		return "Keep current config"
	case model.DecisionKindApplyConfigChange:
		return "Apply config change"
	case model.DecisionKindOptimizeInputPipeline:
		return "Optimize input pipeline first"
	case model.DecisionKindChangeTrafficShape:
		return "Change traffic shape"
	case model.DecisionKindConsiderHardwareChange:
		return "Consider hardware or architecture change"
	default:
		return "Insufficient evidence"
	}
}

func humanizeToken(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", " ")
	if value == "" {
		return "N/A"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
