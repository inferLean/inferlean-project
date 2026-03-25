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

	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/model"
	"github.com/inferLean/inferlean-project/internal/recommender"
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

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := analyzer.SaveJSON(absOutput, report); err != nil {
		return err
	}
	recordCLIEvent("analyze.complete", nil)
	if ui.Enabled() {
		if report.CurrentLoadSummary != nil && strings.TrimSpace(report.CurrentLoadSummary.SaturationSource) == "approximate" {
			recordCLIEvent("analyze.output.proxy_utilization", nil)
		}
		ui.RenderAnalyzeSummaryCard(report)
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
	plainOutput := fs.Bool("plain-output", false, "")
	llmEnhance := fs.Bool("llm-enhance", false, "")
	var scenarioSet setFlags
	fs.Var(&scenarioSet, "set", "")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: inferLean recommend [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fmt.Fprintln(stderr, "  --output <path>         Write the recommendation JSON to this path (default: recommendation-report.json)")
		fmt.Fprintln(stderr, "  --analysis-file <path>  Analyzer report JSON to consume (default: analysis-report.json)")
		fmt.Fprintln(stderr, "  --corpus-file <path>    Optional local benchmark corpus JSON file used for calibration")
		fmt.Fprintln(stderr, "  --objective <value>     balanced, throughput_first, or latency_first (default: workload profile or balanced)")
		fmt.Fprintln(stderr, "  --set key=value         Explicit what-if parameter override (repeatable)")
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

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := analyzer.SaveJSON(absOutput, report); err != nil {
		return err
	}
	recordCLIEvent("recommend.complete", nil)
	if ui.Enabled() {
		analysisReport, readErr := loadAnalysisReportForUI(cleanAnalysisPath)
		if readErr == nil {
			snapshot := buildRecommendationSnapshot(analysisReport, report)
			if !recommendationSnapshotEmpty(snapshot) {
				ui.renderRecommendationSummaryCard(snapshot)
			} else {
				recordCLIEvent("recommend.output.path_fallback", nil)
				fmt.Fprintln(stdout, absOutput)
			}
		} else {
			recordCLIEvent("recommend.output.path_fallback", nil)
			fmt.Fprintln(stdout, absOutput)
		}
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
