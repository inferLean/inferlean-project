package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/analyzer"
	model "github.com/inferLean/inferlean-project/cli/contracts"
	"github.com/inferLean/inferlean-project/optimization"
	"github.com/inferLean/inferlean-project/recommender"
)

type setFlags map[string]float64

func (s *setFlags) String() string {
	if s == nil {
		return ""
	}
	parts := make([]string, 0, len(*s))
	for key, value := range *s {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	return strings.Join(parts, ",")
}

func (s *setFlags) Set(value string) error {
	key, raw, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(raw) == "" {
		return fmt.Errorf("invalid --set %q: expected key=value", value)
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("invalid --set %q: %w", value, err)
	}
	if *s == nil {
		*s = map[string]float64{}
	}
	(*s)[strings.TrimSpace(key)] = parsed
	return nil
}

func runAnalyze(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)

	outputPath := fs.String("output", "analysis-report.json", "")
	collectorPath := fs.String("collector-file", "collector-report.json", "")
	configPath := fs.String("config-file", "", "")
	workloadProfilePath := fs.String("workload-profile-file", "", "")
	intentPath := fs.String("intent-file", "", "")
	plainOutput := fs.Bool("plain-output", false, "")
	llmEnhance := fs.Bool("llm-enhance", false, "")
	format := fs.String("format", "human", "")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: inferLean analyze [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fmt.Fprintln(stderr, "  --output <path>                Write the analyzer JSON to this path (default: analysis-report.json)")
		fmt.Fprintln(stderr, "  --collector-file <path>        Collector report JSON to consume (default: collector-report.json)")
		fmt.Fprintln(stderr, "  --config-file <path>           Optional config override when collector output lacks effective settings")
		fmt.Fprintln(stderr, "  --workload-profile-file <path> Optional workload profile override")
		fmt.Fprintln(stderr, "  --intent-file <path>           Optional declared-intent JSON override (same schema as workload-profile)")
		fmt.Fprintln(stderr, "  --plain-output                 Disable styled terminal output and print only the report path")
		fmt.Fprintln(stderr, "  --format <value>               human or json (default: human)")
		fmt.Fprintln(stderr, "  --llm-enhance                  Add optional llm_enhanced output when env vars are configured")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpRequested
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	recordCLIEvent("analyze.start", nil)
	cleanCollectorPath := toAbsIfPresent(strings.TrimSpace(*collectorPath))
	if cleanCollectorPath == "" {
		return fmt.Errorf("collector-file is required")
	}
	resolvedWorkloadProfilePath, err := resolveWorkloadProfilePath(*workloadProfilePath, *intentPath)
	if err != nil {
		return err
	}
	ui := newTerminalUI(stdout, *plainOutput)

	report, err := analyzer.Analyze(context.Background(), analyzer.Options{
		ConfigPath:          toAbsIfPresent(strings.TrimSpace(*configPath)),
		MetricsPath:         cleanCollectorPath,
		WorkloadProfilePath: resolvedWorkloadProfilePath,
		Now:                 time.Now().UTC(),
		ToolVersion:         model.ToolVersion,
		LLMEnhance:          *llmEnhance,
	})
	if err != nil {
		return err
	}
	slimAnalysisReport(report)
	reportV2 := optimization.ComposeAnalysisReportV2(report, optimization.ComposeOptions{})

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := analyzer.SaveJSON(absOutput, reportV2); err != nil {
		return err
	}
	recordCLIEvent("analyze.complete", nil)
	if strings.EqualFold(strings.TrimSpace(*format), "json") {
		recordCLIEvent("analyze.output.json", nil)
		return writeJSONReport(stdout, reportV2)
	}
	if ui.Enabled() {
		if report.CurrentLoadSummary != nil && strings.TrimSpace(report.CurrentLoadSummary.SaturationSource) == "approximate" {
			recordCLIEvent("analyze.output.proxy_utilization", nil)
		}
		renderAnalysisV2Summary(stdout, reportV2)
	} else {
		fmt.Fprintln(stdout, absOutput)
	}
	return nil
}

func runRecommend(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("recommend", flag.ContinueOnError)
	fs.SetOutput(stderr)

	outputPath := fs.String("output", "recommendation-report.json", "")
	analysisPath := fs.String("analysis-file", "analysis-report.json", "")
	corpusPath := fs.String("corpus-file", "", "")
	objective := fs.String("objective", "", "")
	targetP95Latency := fs.Float64("target-p95-latency", 0, "")
	minThroughput := fs.Float64("min-throughput", 0, "")
	plainOutput := fs.Bool("plain-output", false, "")
	llmEnhance := fs.Bool("llm-enhance", false, "")
	format := fs.String("format", "human", "")
	var scenarioSet setFlags
	fs.Var(&scenarioSet, "set", "")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: inferLean recommend [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fmt.Fprintln(stderr, "  --output <path>         Write the recommendation JSON to this path (default: recommendation-report.json)")
		fmt.Fprintln(stderr, "  --analysis-file <path>  Analyzer report JSON to consume (default: analysis-report.json)")
		fmt.Fprintln(stderr, "  --corpus-file <path>    Optional local benchmark corpus JSON file used for calibration")
		fmt.Fprintln(stderr, "  --objective <value>     throughput, latency, or balanced (default: workload profile or balanced)")
		fmt.Fprintln(stderr, "  --target-p95-latency    Optional p95 latency guardrail in ms")
		fmt.Fprintln(stderr, "  --min-throughput        Optional minimum throughput guardrail")
		fmt.Fprintln(stderr, "  --set key=value         Explicit what-if parameter override (repeatable)")
		fmt.Fprintln(stderr, "  --format <value>        human or json (default: human)")
		fmt.Fprintln(stderr, "  --plain-output          Disable styled terminal output and print only the report path")
		fmt.Fprintln(stderr, "  --llm-enhance           Add optional llm_enhanced output when env vars are configured")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpRequested
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	recordCLIEvent("recommend.start", nil)
	cleanAnalysisPath := toAbsIfPresent(strings.TrimSpace(*analysisPath))
	if cleanAnalysisPath == "" {
		return fmt.Errorf("analysis-file is required")
	}
	ui := newTerminalUI(stdout, *plainOutput)

	report, err := recommender.Recommend(context.Background(), recommender.Options{
		AnalysisPath: cleanAnalysisPath,
		CorpusPath:   toAbsIfPresent(strings.TrimSpace(*corpusPath)),
		Now:          time.Now().UTC(),
		ToolVersion:  model.ToolVersion,
		Objective:    recommender.Objective(strings.TrimSpace(*objective)),
		ScenarioSet:  map[string]float64(scenarioSet),
		LLMEnhance:   *llmEnhance,
	})
	if err != nil {
		return err
	}
	analysisReport, readErr := loadAnalysisReportForUI(cleanAnalysisPath)
	if readErr != nil {
		return readErr
	}
	var constraint *model.ConstraintV2
	if *targetP95Latency > 0 || *minThroughput > 0 {
		constraint = &model.ConstraintV2{}
		if *targetP95Latency > 0 {
			value := *targetP95Latency
			constraint.TargetP95LatencyMS = &value
		}
		if *minThroughput > 0 {
			value := *minThroughput
			constraint.MinThroughput = &value
		}
	}
	reportV2 := optimization.ComposeOptimizationReportV2(analysisReport, report, optimization.ComposeOptions{
		ObjectiveMode: strings.TrimSpace(*objective),
		Constraint:    constraint,
		AccessTier:    model.AccessTierPaid,
	})

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := analyzer.SaveJSON(absOutput, reportV2); err != nil {
		return err
	}
	recordCLIEvent("recommend.complete", nil)
	if strings.EqualFold(strings.TrimSpace(*format), "json") {
		recordCLIEvent("recommend.output.json", nil)
		return writeJSONReport(stdout, reportV2)
	}
	if ui.Enabled() {
		renderOptimizationV2Summary(stdout, reportV2)
	} else {
		fmt.Fprintln(stdout, absOutput)
	}
	return nil
}

func loadAnalysisReportForUI(path string) (*model.AnalysisReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report model.AnalysisReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return analyzer.NormalizeReport(&report, analyzer.BalancedIntent), nil
}

func slimAnalysisReport(report *model.AnalysisReport) {
	if report == nil {
		return
	}
	report.CollectedMetrics = nil
	report.MetricCollectionOutputs = nil
}
