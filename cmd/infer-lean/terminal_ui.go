package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	analyzerCLIConfidenceThreshold       = 0.80
	recommendationCLIConfidenceThreshold = 0.70
)

type terminalUI struct {
	out     io.Writer
	enabled bool
	color   bool
}

type analysisSnapshot struct {
	Traffic             string
	Queue               string
	QueueTone           string
	Saturation          string
	SaturationTone      string
	SaturationBreakdown *saturationBreakdown
	Bottleneck          string
	ObservedTraffic     string
	ObservedBehavior    string
	ConfiguredFor       string
}

type saturationBreakdown struct {
	Compute float64
	Memory  float64
	CPU     float64
}

type recommendationSnapshot struct {
	TargetGoal          string
	WastedCapacityLabel string
	WastedCapacity      string
	BestAction          string
	ExpectedImpact      string
	Warning             string
}

func newTerminalUI(out io.Writer, plainOutput bool) terminalUI {
	enabled := !plainOutput && isTerminalWriter(out)
	return terminalUI{
		out:     out,
		enabled: enabled,
		color:   enabled && terminalSupportsColor(),
	}
}

func (ui terminalUI) Enabled() bool {
	return ui.enabled
}

func (ui terminalUI) Step(message string) {
	if !ui.enabled {
		return
	}
	ok := "[ok]"
	if ui.color {
		ok = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("44")).Render(ok)
		message = lipgloss.NewStyle().Foreground(lipgloss.Color("44")).Render(message)
	}
	fmt.Fprintf(ui.out, "%s %s\n", ok, message)
}

func (ui terminalUI) Stepf(format string, args ...any) {
	ui.Step(fmt.Sprintf(format, args...))
}

func (ui terminalUI) RenderAnalyzeSummaryCard(report *model.AnalysisReport) {
	snapshot := buildAnalysisSnapshot(report, nil)
	ui.renderAnalysisSummaryCard(snapshot)
}

func (ui terminalUI) RenderRecommendationSummaryCard(report *model.AnalysisReport, recommendation *model.RecommendationReport) {
	snapshot := buildRecommendationSnapshot(report, recommendation)
	ui.renderRecommendationSummaryCard(snapshot)
}

func (ui terminalUI) renderAnalysisSummaryCard(snapshot analysisSnapshot) {
	if !ui.enabled {
		return
	}
	rows := []row{
		{Label: "Saturation", Value: snapshot.Saturation, Tone: snapshot.SaturationTone},
	}
	if snapshot.SaturationBreakdown != nil {
		rows = append(rows, row{Label: "", Value: ui.renderSaturationBreakdown(*snapshot.SaturationBreakdown), Raw: true})
	}
	rows = append(rows, row{Label: "Traffic", Value: snapshot.Traffic})
	if strings.TrimSpace(snapshot.Queue) != "" {
		rows = append(rows, row{Label: "Queue", Value: snapshot.Queue, Tone: snapshot.QueueTone})
	}
	if strings.TrimSpace(snapshot.Bottleneck) != "" {
		rows = append(rows, row{Label: "Bottleneck", Value: snapshot.Bottleneck})
	}
	if strings.TrimSpace(snapshot.ObservedTraffic) != "" {
		rows = append(rows, row{Label: "Observed Traffic", Value: snapshot.ObservedTraffic})
	}
	if strings.TrimSpace(snapshot.ObservedBehavior) != "" {
		rows = append(rows, row{Label: "Observed Behavior", Value: snapshot.ObservedBehavior})
	}
	if strings.TrimSpace(snapshot.ConfiguredFor) != "" {
		rows = append(rows, row{Label: "Configured For", Value: snapshot.ConfiguredFor})
	}
	ui.renderCard(rows, "")
}

func (ui terminalUI) renderRecommendationSummaryCard(snapshot recommendationSnapshot) {
	if !ui.enabled {
		return
	}
	rows := []row{}
	if strings.TrimSpace(snapshot.WastedCapacity) != "" {
		label := snapshot.WastedCapacityLabel
		if strings.TrimSpace(label) == "" {
			label = "Wasted Capacity"
		}
		rows = append(rows, row{Label: label, Value: snapshot.WastedCapacity, Tone: "healthy", NoTruncate: true})
	}
	if strings.TrimSpace(snapshot.TargetGoal) != "" {
		rows = append(rows, row{Label: "Target Goal", Value: snapshot.TargetGoal, NoTruncate: true})
	}
	if strings.TrimSpace(snapshot.BestAction) != "" {
		rows = append(rows, row{Label: "Best Action", Value: snapshot.BestAction, NoTruncate: true})
	}
	if strings.TrimSpace(snapshot.ExpectedImpact) != "" {
		rows = append(rows, row{Label: "Expected Impact", Value: snapshot.ExpectedImpact, Tone: "healthy", NoTruncate: true})
	}
	if len(rows) == 0 && strings.TrimSpace(snapshot.Warning) != "" {
		rows = append(rows, row{Label: "Warning", Value: snapshot.Warning, NoTruncate: true})
	} else if strings.TrimSpace(snapshot.Warning) != "" {
		rows = append(rows, row{Label: "Warning", Value: snapshot.Warning, NoTruncate: true})
	}
	ui.renderCard(rows, snapshot.Warning)
}

type row struct {
	Label      string
	Value      string
	Tone       string
	Raw        bool
	NoTruncate bool
}

func (ui terminalUI) renderCard(rows []row, warning string) {
	if len(rows) == 0 {
		return
	}
	labelWidth := 18
	valueWidth := 72

	borderColor := lipgloss.Color("240")
	labelColor := lipgloss.Color("103")
	valueColor := lipgloss.Color("252")
	warnColor := lipgloss.Color("214")
	healthyColor := lipgloss.Color("78")
	elevatedColor := lipgloss.Color("214")
	severeColor := lipgloss.Color("203")

	labelStyle := lipgloss.NewStyle().Width(labelWidth).Foreground(labelColor)
	valueStyle := lipgloss.NewStyle().Width(valueWidth).Align(lipgloss.Left).Foreground(valueColor)
	warnStyle := lipgloss.NewStyle().Width(valueWidth).Align(lipgloss.Left).Foreground(warnColor)
	if !ui.color {
		labelStyle = lipgloss.NewStyle().Width(labelWidth)
		valueStyle = lipgloss.NewStyle().Width(valueWidth).Align(lipgloss.Left)
		warnStyle = lipgloss.NewStyle().Width(valueWidth).Align(lipgloss.Left)
	}

	renderedRows := make([]string, 0, len(rows))
	for _, item := range rows {
		style := valueStyle
		if item.Label == "Warning" {
			style = warnStyle
		} else {
			switch item.Tone {
			case "healthy":
				style = style.Foreground(healthyColor)
			case "elevated":
				style = style.Foreground(elevatedColor)
			case "severe":
				style = style.Foreground(severeColor)
			}
		}
		value := item.Value
		if !item.Raw && !item.NoTruncate {
			value = truncateRunes(value, valueWidth)
		}
		if !item.Raw {
			value = style.Render(value)
		}
		renderedRows = append(renderedRows, lipgloss.JoinHorizontal(
			lipgloss.Top,
			labelStyle.Render(item.Label),
			value,
		))
	}

	card := lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(strings.Join(renderedRows, "\n\n"))

	fmt.Fprintln(ui.out)
	fmt.Fprintln(ui.out, card)
	fmt.Fprintln(ui.out)
}

func buildAnalysisSnapshot(report *model.AnalysisReport, recommendation *model.RecommendationReport) analysisSnapshot {
	if report != nil && report.ServiceSummary == nil {
		report = analyzer.NormalizeReport(report, analyzer.BalancedIntent)
	}

	snapshot := analysisSnapshot{
		Traffic:    "N/A",
		Saturation: "N/A",
	}
	if report == nil || report.ServiceSummary == nil {
		return snapshot
	}
	summary := report.ServiceSummary
	requestRate := "N/A"
	if summary.RequestRateRPS != nil {
		requestRate = fmt.Sprintf("%s req/s", formatRequestRate(*summary.RequestRateRPS))
	}
	latency := requestLatencySummary(summary.RequestLatencyMS)
	switch {
	case requestRate != "N/A" && latency != "N/A":
		snapshot.Traffic = requestRate + " | " + latency
	case requestRate != "N/A":
		snapshot.Traffic = requestRate
	default:
		snapshot.Traffic = latency
	}
	if summary.Queue.AvgDelayMS != nil && summary.Queue.AvgWaitingRequests != nil {
		if *summary.Queue.AvgDelayMS >= 100 || *summary.Queue.AvgWaitingRequests >= 1 {
			snapshot.Queue = fmt.Sprintf(
				"%s: %.0f ms avg wait, %.1f waiting",
				humanizeQueueHealth(summary.Queue.Health),
				*summary.Queue.AvgDelayMS,
				*summary.Queue.AvgWaitingRequests,
			)
			snapshot.QueueTone = summary.Queue.Health
		}
	}
	if summary.SaturationPct != nil {
		label := saturationLabel(*summary.SaturationPct)
		dominant := "GPU"
		if report.CurrentLoadSummary != nil {
			dominant = humanizeDominantResource(report.CurrentLoadSummary.DominantGPUResource)
			if dominant == "" {
				dominant = "GPU"
			}
			if shouldShowSaturationBreakdown(report.CurrentLoadSummary) {
				snapshot.SaturationBreakdown = &saturationBreakdown{
					Compute: report.CurrentLoadSummary.ComputeLoadPct,
					Memory:  report.CurrentLoadSummary.MemoryBandwidthLoadPct,
					CPU:     report.CurrentLoadSummary.CPULoadPct,
				}
			}
		}
		snapshot.Saturation = fmt.Sprintf("%s: %.0f%% %s (avg)", label, *summary.SaturationPct, dominant)
		snapshot.Saturation += saturationHeadroomSuffix(summary)
		snapshot.SaturationTone = saturationTone(*summary.SaturationPct)
	}
	if summary.Bottleneck.Confidence >= analyzerCLIConfidenceThreshold && summary.Bottleneck.Kind != "" && summary.Bottleneck.Kind != "unclear" {
		snapshot.Bottleneck = humanizeCompactBottleneck(summary.Bottleneck.Kind)
	}
	if summary.ObservedMode.Confidence >= analyzerCLIConfidenceThreshold &&
		summary.ObservedMode.Objective != "" && summary.ObservedMode.Objective != model.WorkloadObjectiveUnknown &&
		summary.ObservedMode.ServingPattern != "" && summary.ObservedMode.ServingPattern != "unknown" {
		snapshot.ObservedTraffic = humanizeObservedTraffic(summary.ObservedMode.ServingPattern)
		snapshot.ObservedBehavior = humanizeObservedBehavior(summary.ObservedMode.Objective)
		if summary.ConfiguredIntent.Confidence >= 0.90 &&
			summary.ConfiguredIntent.Value != "" &&
			summary.ConfiguredIntent.Value != model.WorkloadObjectiveUnknown &&
			summary.ConfiguredIntent.Value != summary.ObservedMode.Objective {
			snapshot.ConfiguredFor = humanizeConfiguredIntent(summary.ConfiguredIntent.Value)
		}
	}
	_ = recommendation
	return snapshot
}

func buildRecommendationSnapshot(report *model.AnalysisReport, recommendation *model.RecommendationReport) recommendationSnapshot {
	snapshot := recommendationSnapshot{}
	if report != nil && report.ServiceSummary == nil {
		report = analyzer.NormalizeReport(report, analyzer.BalancedIntent)
	}
	if recommendation == nil {
		return snapshot
	}
	if recommendation.CurrentServiceState == nil && report != nil {
		recommendation.CurrentServiceState = report.ServiceSummary
	}
	snapshot.WastedCapacityLabel, snapshot.WastedCapacity = wastedCapacityCLISummary(recommendation)
	snapshot.TargetGoal = targetGoalSummary(recommendation)
	if recommendation.PrimaryAction != nil && recommendation.PrimaryAction.Confidence >= recommendationCLIConfidenceThreshold {
		snapshot.BestAction = strings.TrimSpace(recommendation.PrimaryAction.Summary)
	}
	if recommendation.PredictedImpact != nil && recommendation.PrimaryAction != nil && recommendation.PrimaryAction.Confidence >= recommendationCLIConfidenceThreshold {
		snapshot.ExpectedImpact = predictedImpactSummary(recommendation.PredictedImpact)
	}
	snapshot.Warning = firstNonEmpty(recommendation.Warnings)
	return snapshot
}

func primaryFinding(report *model.AnalysisReport) (model.Finding, bool) {
	if report == nil || report.AnalysisSummary == nil || len(report.AnalysisSummary.Findings) == 0 {
		return model.Finding{}, false
	}

	findings := append([]model.Finding(nil), report.AnalysisSummary.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		ri := findings[i].Rank
		rj := findings[j].Rank
		if ri <= 0 {
			ri = 1_000_000 + i
		}
		if rj <= 0 {
			rj = 1_000_000 + j
		}
		return ri < rj
	})

	for _, finding := range findings {
		if finding.Status == model.FindingStatusPresent {
			return finding, true
		}
	}
	for _, finding := range findings {
		if finding.Status == model.FindingStatusInsufficientData {
			return finding, true
		}
	}
	return findings[0], true
}

func findingHeadline(finding model.Finding) string {
	if headline := findingHeadlineByID(finding.ID); headline != "" {
		return headline
	}
	if text := strings.TrimSpace(finding.Summary); text != "" {
		return text
	}
	return humanizeFindingID(finding.ID)
}

func findingHeadlineByID(id string) string {
	switch id {
	case "queue_dominated_ttft":
		return "Queue-heavy TTFT hurts responsiveness"
	case "throughput_saturation_with_queue_pressure":
		return "Queue pressure is limiting throughput"
	case "underutilized_gpu_or_conservative_batching":
		return "Conservative batching leaves GPU headroom unused"
	case "kv_cache_pressure_preemptions":
		return "KV cache preemptions increase tail latency"
	case "prefix_cache_ineffective":
		return "Low prefix-cache hit rate inflates prefill cost"
	case "prompt_recomputation_thrashing":
		return "Prompt recomputation adds avoidable latency"
	case "prefill_heavy_workload":
		return "Prefill-heavy traffic dominates end-to-end latency"
	case "decode_bound_generation":
		return "Decode path is the dominant generation bottleneck"
	case "cpu_or_host_bottleneck":
		return "CPU or host constraints throttle GPU throughput"
	case "gpu_memory_saturation_without_throughput":
		return "GPU memory saturation caps throughput gains"
	case "gpu_hardware_instability":
		return "GPU hardware instability signals were detected"
	default:
		return ""
	}
}

func humanizeFindingID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "Unknown"
	}
	text := strings.ReplaceAll(trimmed, "_", " ")
	return strings.ToUpper(text[:1]) + text[1:]
}

func humanizeLoadBottleneck(value string) string {
	switch strings.TrimSpace(value) {
	case "gpu_compute_bound", "gpu_compute":
		return "GPU compute bound"
	case "gpu_memory_bound", "gpu_bandwidth":
		return "GPU bandwidth bound"
	case "cpu_bound", "cpu":
		return "CPU bound"
	case "mixed":
		return "Mixed"
	default:
		return "Unknown"
	}
}

func humanizeCompactBottleneck(value string) string {
	switch strings.TrimSpace(value) {
	case "gpu_compute":
		return "GPU Compute"
	case "gpu_bandwidth":
		return "GPU Bandwidth"
	case "cpu":
		return "CPU"
	case "mixed":
		return "Mixed"
	default:
		return ""
	}
}

func humanizeDominantResource(value string) string {
	switch strings.TrimSpace(value) {
	case "compute":
		return "GPU compute"
	case "memory_bandwidth":
		return "GPU bandwidth"
	case "tensor":
		return "Tensor"
	default:
		return ""
	}
}

func humanizeObjective(value string) string {
	switch strings.TrimSpace(value) {
	case "throughput_first":
		return "Throughput First"
	case "latency_first":
		return "Latency First"
	case "balanced":
		return "Balanced"
	default:
		return humanizeFindingID(value)
	}
}

func humanizeObservedBehavior(value string) string {
	switch strings.TrimSpace(value) {
	case "throughput_first":
		return "Throughput-focused"
	case "latency_first":
		return "Latency-focused"
	case "balanced":
		return "Balanced latency/throughput"
	default:
		return humanizeFindingID(value)
	}
}

func humanizeConfiguredIntent(value string) string {
	switch strings.TrimSpace(value) {
	case "throughput_first":
		return "Throughput-focused"
	case "latency_first":
		return "Latency-focused"
	case "balanced":
		return "Balanced latency/throughput"
	default:
		return humanizeFindingID(value)
	}
}

func humanizeServingPattern(value string) string {
	switch strings.TrimSpace(value) {
	case "realtime":
		return "Realtime"
	case "batch":
		return "Batch"
	case "mixed":
		return "Mixed"
	default:
		return ""
	}
}

func humanizeObservedTraffic(value string) string {
	switch strings.TrimSpace(value) {
	case "realtime":
		return "Interactive realtime"
	case "batch":
		return "Batch processing"
	case "mixed":
		return "Shared realtime + batch"
	default:
		return ""
	}
}

func humanizeQueueHealth(value string) string {
	switch strings.TrimSpace(value) {
	case "severe":
		return "Severe"
	case "elevated":
		return "Elevated"
	default:
		return "Healthy"
	}
}

func saturationLabel(value float64) string {
	switch {
	case value >= 85:
		return "High"
	case value >= 60:
		return "Elevated"
	default:
		return "Healthy"
	}
}

func saturationHeadroomSuffix(summary *model.ServiceSummary) string {
	if summary == nil || summary.SaturationPct == nil {
		return ""
	}
	if *summary.SaturationPct >= 95 {
		return " | near current limit"
	}
	if summary.EstimatedUpperRequestRateRPS == nil {
		return ""
	}
	return fmt.Sprintf(" | headroom to ~%s req/s", formatApproxRequestRate(*summary.EstimatedUpperRequestRateRPS))
}

func requestLatencySummary(latency model.RequestLatencySummary) string {
	parts := []string{}
	if latency.Avg != nil {
		parts = append(parts, fmt.Sprintf("avg %s", formatMS(*latency.Avg)))
	}
	if latency.P50 != nil {
		parts = append(parts, fmt.Sprintf("p50 %s", formatMS(*latency.P50)))
	}
	if latency.P99 != nil {
		parts = append(parts, fmt.Sprintf("p99 %s", formatMS(*latency.P99)))
	} else if latency.PercentilesAvailable && latency.P90 != nil {
		parts = append(parts, fmt.Sprintf("p90 %s", formatMS(*latency.P90)))
	}
	if len(parts) == 0 {
		return "N/A"
	}
	return strings.Join(parts, ", ")
}

func formatPct(value float64) string {
	return fmt.Sprintf("%.1f%%", clampFloat(value, 0, 100))
}

func formatSignedPct(value float64) string {
	return fmt.Sprintf("%+.1f%%", value)
}

func formatMS(value float64) string {
	switch {
	case value >= 1000:
		return fmt.Sprintf("%.2fs", value/1000)
	case value >= 100:
		return fmt.Sprintf("%.0fms", value)
	default:
		return fmt.Sprintf("%.1fms", value)
	}
}

func predictedImpactSummary(summary *model.PredictedImpactSummary) string {
	if summary == nil {
		return ""
	}
	parts := []string{}
	if summary.RequestRateRPS.After != nil {
		value := fmt.Sprintf("req/s %.2f", *summary.RequestRateRPS.After)
		if summary.RequestRateRPS.DeltaPct != nil {
			value += " (" + formatSignedPct(*summary.RequestRateRPS.DeltaPct) + ")"
		}
		parts = append(parts, value)
	}
	if summary.RequestLatencyMS.P50.After != nil {
		value := fmt.Sprintf("p50 %s", formatMS(*summary.RequestLatencyMS.P50.After))
		if summary.RequestLatencyMS.P50.DeltaPct != nil {
			value += " (" + formatSignedPct(*summary.RequestLatencyMS.P50.DeltaPct) + ")"
		}
		parts = append(parts, value)
	}
	if summary.GPUUtilizationPct.After != nil {
		value := fmt.Sprintf("GPU util %s", formatPct(*summary.GPUUtilizationPct.After))
		if summary.GPUUtilizationPct.DeltaPct != nil {
			value += " (" + formatSignedPct(*summary.GPUUtilizationPct.DeltaPct) + ")"
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, ", ")
}

func llmSummary(output *model.LLMEnhancedOutput) string {
	if output == nil {
		return ""
	}
	if summary := strings.TrimSpace(output.Summary); summary != "" {
		return summary
	}
	if explanation := strings.TrimSpace(output.Explanation); explanation != "" {
		return explanation
	}
	return firstNonEmpty(output.ActionHighlights)
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func terminalSupportsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CLICOLOR") == "0" {
		return false
	}
	if os.Getenv("CLICOLOR_FORCE") == "1" {
		return true
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	return true
}

func truncateRunes(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	return string(runes[:width-3]) + "..."
}

func saturationTone(value float64) string {
	switch {
	case value >= 85:
		return "severe"
	case value >= 60:
		return "elevated"
	default:
		return "healthy"
	}
}

func shouldShowSaturationBreakdown(load *model.CurrentLoadSummary) bool {
	if load == nil {
		return false
	}
	values := []float64{
		clampFloat(load.ComputeLoadPct, 0, 100),
		clampFloat(load.MemoryBandwidthLoadPct, 0, 100),
		clampFloat(load.CPULoadPct, 0, 100),
	}
	maxValue := values[0]
	minValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
		if value < minValue {
			minValue = value
		}
	}
	return maxValue >= 85 || (maxValue-minValue) >= 20
}

func (ui terminalUI) renderSaturationBreakdown(b saturationBreakdown) string {
	parts := []string{
		ui.renderSeveritySegment(fmt.Sprintf("GPU compute %.0f%%", b.Compute), saturationTone(b.Compute)),
		ui.renderSeveritySegment(fmt.Sprintf("GPU bandwidth %.0f%%", b.Memory), saturationTone(b.Memory)),
		ui.renderSeveritySegment(fmt.Sprintf("CPU %.0f%%", b.CPU), saturationTone(b.CPU)),
	}
	return strings.Join(parts, ", ")
}

func (ui terminalUI) renderSeveritySegment(text, tone string) string {
	if !ui.color {
		return text
	}
	style := lipgloss.NewStyle()
	switch tone {
	case "healthy":
		style = style.Foreground(lipgloss.Color("78"))
	case "elevated":
		style = style.Foreground(lipgloss.Color("214"))
	case "severe":
		style = style.Foreground(lipgloss.Color("203"))
	}
	return style.Render(text)
}

func wastedCapacityCLISummary(report *model.RecommendationReport) (string, string) {
	if report == nil {
		return "", ""
	}
	if report.CapacityOpportunity != nil {
		return "Wasted Capacity", fmt.Sprintf("%.1f%% | %.1f GPU recoverable", report.CapacityOpportunity.RecoverableGPULoadPct, report.CapacityOpportunity.RecoverableGPUCount)
	}
	if report.WastedCapacity != nil {
		if report.WastedCapacity.GPUHeadroomPct != nil && report.WastedCapacity.GPUHeadroomCount != nil {
			return "Wasted Capacity", fmt.Sprintf("%.1f%% | %.1f GPU recoverable", *report.WastedCapacity.GPUHeadroomPct, *report.WastedCapacity.GPUHeadroomCount)
		}
		if report.WastedCapacity.ThroughputGapRPS != nil {
			if report.WastedCapacity.ThroughputGapPct != nil {
				return "Req/s Headroom", fmt.Sprintf("+%s req/s recoverable (%+.1f%%)", formatRequestRate(*report.WastedCapacity.ThroughputGapRPS), *report.WastedCapacity.ThroughputGapPct)
			}
			return "Req/s Headroom", fmt.Sprintf("+%s req/s recoverable", formatRequestRate(*report.WastedCapacity.ThroughputGapRPS))
		}
		return "Wasted Capacity", strings.TrimSpace(report.WastedCapacity.Headline)
	}
	return "", ""
}

func targetGoalSummary(report *model.RecommendationReport) string {
	if report == nil || report.DeclaredGoal == nil {
		return ""
	}
	parts := []string{humanizeTargetGoal(report.DeclaredGoal.Value)}
	if report.Guardrail != nil {
		guardrail := humanizeGuardrailSummary(report.Guardrail)
		if guardrail != "" {
			parts = append(parts, guardrail)
		}
	}
	return strings.Join(parts, " | ")
}

func humanizeTargetGoal(value string) string {
	switch strings.TrimSpace(value) {
	case "latency_first":
		return "Latency-priority"
	case "throughput_first":
		return "Throughput-priority"
	case "balanced":
		return "Balanced"
	default:
		return humanizeFindingID(value)
	}
}

func humanizeGuardrailSummary(summary *model.GuardrailSummary) string {
	if summary == nil {
		return ""
	}
	if summary.MinThroughputRetentionPct != nil && summary.MaxLatencyP50IncreasePct != nil {
		return fmt.Sprintf(
			"keep throughput >= %.0f%% and p50 latency growth <= +%.0f%%",
			*summary.MinThroughputRetentionPct,
			*summary.MaxLatencyP50IncreasePct,
		)
	}
	if summary.MinThroughputRetentionPct != nil {
		return fmt.Sprintf("keep throughput >= %.0f%% of current", *summary.MinThroughputRetentionPct)
	}
	if summary.MaxLatencyP50IncreasePct != nil {
		return fmt.Sprintf("keep p50 latency growth <= +%.0f%%", *summary.MaxLatencyP50IncreasePct)
	}
	return strings.TrimSpace(summary.Summary)
}

func formatApproxRequestRate(value float64) string {
	switch {
	case value >= 10:
		return fmt.Sprintf("%.0f", value)
	default:
		return fmt.Sprintf("%.1f", value)
	}
}

func formatRequestRate(value float64) string {
	switch {
	case value >= 100:
		return fmt.Sprintf("%.0f", value)
	case value >= 10:
		return fmt.Sprintf("%.1f", value)
	default:
		return fmt.Sprintf("%.2f", value)
	}
}
