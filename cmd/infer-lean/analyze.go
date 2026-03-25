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
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/model"
)

var errHelpRequested = errors.New("help requested")

type collectRunOptions struct {
	progressCallback func(CollectionProgressUpdate)
}

func runCollect(args []string, stdout, stderr io.Writer) error {
	return runCollectWithContext(context.Background(), args, stdout, stderr, collectRunOptions{})
}

func runCollectWithOptions(args []string, stdout, stderr io.Writer, opts collectRunOptions) error {
	return runCollectWithContext(context.Background(), args, stdout, stderr, opts)
}

func runCollectWithContext(ctx context.Context, args []string, stdout, stderr io.Writer, opts collectRunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(stderr)

	outputPath := fs.String("output", "collector-report.json", "")
	vllmVersion := fs.String("vllm-version", "", "")
	vllmBin := fs.String("vllm-bin", "", "")
	vllmVersionTimeoutSeconds := fs.Int("vllm-version-timeout-seconds", defaultVLLMVersionProbeTimeoutSeconds, "")
	deploymentType := fs.String("deployment-type", "host", "")
	metricsPath := fs.String("metrics-file", "", "")
	configPath := fs.String("config-file", "", "")
	workloadProfilePath := fs.String("workload-profile-file", "", "")
	intentPath := fs.String("intent-file", "", "")
	collectPrometheus := fs.Bool("collect-prometheus", true, "")
	durationSeconds := fs.Int("duration-seconds", defaultCollectionDurationSeconds, "")
	durationMinutes := fs.Int("duration-minutes", 0, "")
	stepSeconds := fs.Int("prometheus-step-seconds", defaultPrometheusStepSeconds, "")
	prometheusBin := fs.String("prometheus-bin", "", "")
	nodeExporterBin := fs.String("node-exporter-bin", "", "")
	dcgmExporterBin := fs.String("dcgm-exporter-bin", "", "")
	vllmMetricsTarget := fs.String("vllm-metrics-target", "127.0.0.1:8000", "")
	vllmMetricsPath := fs.String("vllm-metrics-path", "/metrics", "")
	prometheusWorkDir := fs.String("prometheus-workdir", "", "")
	plainOutput := fs.Bool("plain-output", false, "")
	debugMode := fs.Bool("debug", false, "")
	enableProfiling := fs.Bool("enable-profiling", true, "")
	collectBCC := fs.Bool("collect-bcc", true, "")
	collectPySpy := fs.Bool("collect-py-spy", true, "")
	collectNSYS := fs.Bool("collect-nsys", true, "")
	profilingWorkDir := fs.String("profiling-workdir", "", "")
	vllmPID := fs.Int("vllm-pid", 0, "")
	bccBin := fs.String("bcc-bin", "", "")
	pySpyBin := fs.String("py-spy-bin", "", "")
	nsysBin := fs.String("nsys-bin", "", "")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: InferLean collect [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fmt.Fprintln(stderr, "  --output <path>           Write the collector JSON to this path (default: collector-report.json)")
		fmt.Fprintln(stderr, "  --vllm-version <string>   Optional version override (auto-discovered from vLLM binary when omitted)")
		fmt.Fprintln(stderr, "  --vllm-bin <path>         vLLM binary path (required only when auto-discovery cannot find it)")
		fmt.Fprintf(stderr, "  --vllm-version-timeout-seconds <int> Timeout for each vLLM version probe command in seconds (default: %d)\n", defaultVLLMVersionProbeTimeoutSeconds)
		fmt.Fprintln(stderr, "  --deployment-type <type>  host, docker, or k8s")
		fmt.Fprintln(stderr, "  --metrics-file <path>     Optional JSON metrics input")
		fmt.Fprintln(stderr, "  --config-file <path>      Optional vLLM config path (auto-discovered when omitted)")
		fmt.Fprintln(stderr, "  --workload-profile-file <path> Optional workload profile JSON/YAML input")
		fmt.Fprintln(stderr, "  --intent-file <path>      Optional declared-intent JSON input (same schema as workload-profile)")
		fmt.Fprintln(stderr, "  --collect-prometheus      Run prometheus/node_exporter/dcgm-exporter for collection when metrics-file is not provided (default: true)")
		fmt.Fprintf(stderr, "  --duration-seconds <int>  Collection duration in seconds (default: %d)\n", defaultCollectionDurationSeconds)
		fmt.Fprintln(stderr, "  --duration-minutes <int>  Deprecated legacy duration override in minutes")
		fmt.Fprintf(stderr, "  --prometheus-step-seconds Prometheus query range step in seconds (default: %d)\n", defaultPrometheusStepSeconds)
		fmt.Fprintln(stderr, "  --prometheus-bin <path>   Prometheus binary path (empty means auto-install/auto-detect)")
		fmt.Fprintln(stderr, "  --node-exporter-bin <path> node_exporter binary path (empty means auto-install/auto-detect)")
		fmt.Fprintln(stderr, "  --dcgm-exporter-bin <path> dcgm-exporter binary path (empty means auto-install/auto-detect)")
		fmt.Fprintln(stderr, "  --vllm-metrics-target <host:port> vLLM Prometheus target (default: auto-discovered vLLM port, fallback 127.0.0.1:8000)")
		fmt.Fprintln(stderr, "  --vllm-metrics-path <path> vLLM metrics path (default: /metrics)")
		fmt.Fprintln(stderr, "  --prometheus-workdir <path> Working directory for temporary Prometheus files (default: temp dir)")
		fmt.Fprintln(stderr, "  --plain-output            Disable styled terminal output and print only the report path")
		fmt.Fprintln(stderr, "  --debug                   Enable verbose debug logs")
		fmt.Fprintln(stderr, "  --enable-profiling        Enable advanced profiling collection (default: true)")
		fmt.Fprintln(stderr, "  --collect-bcc             Collect bcc profile output for vLLM PID (default: true)")
		fmt.Fprintln(stderr, "  --collect-py-spy          Collect py-spy stack dump for vLLM PID (default: true)")
		fmt.Fprintln(stderr, "  --collect-nsys            Collect NVIDIA Nsight Systems profile for vLLM PID (default: true)")
		fmt.Fprintln(stderr, "  --profiling-workdir <path> Directory to store profiler artifacts/logs (default: prometheus workdir/profiling)")
		fmt.Fprintln(stderr, "  --vllm-pid <int>          Explicit vLLM PID (default: auto-detect)")
		fmt.Fprintln(stderr, "  --bcc-bin <path>          bcc profile binary path (empty means auto-install/auto-detect)")
		fmt.Fprintln(stderr, "  --py-spy-bin <path>       py-spy binary path (empty means auto-install/auto-detect)")
		fmt.Fprintln(stderr, "  --nsys-bin <path>         nsys binary path (empty means auto-install/auto-detect)")
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
	vllmMetricsTargetProvided := false
	durationSecondsProvided := false
	durationMinutesProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "vllm-metrics-target" {
			vllmMetricsTargetProvided = true
		}
		if f.Name == "duration-seconds" {
			durationSecondsProvided = true
		}
		if f.Name == "duration-minutes" {
			durationMinutesProvided = true
		}
	})
	configureDebug(*debugMode, stderr)
	debugf("starting collect command")
	collectionDurationSeconds, durationSource, err := resolveCollectionDurationSeconds(*durationSeconds, durationSecondsProvided, *durationMinutes, durationMinutesProvided)
	if err != nil {
		return err
	}
	recordCLIEvent("collect.start", map[string]string{
		"collect_prometheus": fmt.Sprintf("%t", *collectPrometheus),
		"profiling_enabled":  fmt.Sprintf("%t", *enableProfiling),
		"duration_source":    durationSource,
	})
	ui := newTerminalUI(stdout, *plainOutput)

	if err := validateDeploymentType(*deploymentType); err != nil {
		return err
	}
	if *stepSeconds <= 0 {
		return fmt.Errorf("prometheus-step-seconds must be > 0")
	}
	if *vllmVersionTimeoutSeconds <= 0 {
		return fmt.Errorf("vllm-version-timeout-seconds must be > 0")
	}
	if !*enableProfiling {
		*collectBCC = false
		*collectPySpy = false
		*collectNSYS = false
	}

	now := time.Now().UTC()
	recordCLIEvent("collect.discovery.start", nil)
	discovery, err := ensureVLLMDiscovery(ctx, toAbsIfPresent(strings.TrimSpace(*configPath)))
	if err != nil {
		debugf("vLLM discovery failed: %v", err)
		return err
	}
	cleanConfigPath := discovery.ConfigPath
	resolvedVLLMMetricsTarget := strings.TrimSpace(*vllmMetricsTarget)
	if !vllmMetricsTargetProvided && strings.TrimSpace(discovery.MetricsTarget) != "" {
		resolvedVLLMMetricsTarget = strings.TrimSpace(discovery.MetricsTarget)
		debugf("resolved vLLM metrics target from process discovery: %s", resolvedVLLMMetricsTarget)
	} else {
		debugf("resolved vLLM metrics target from flags/defaults: %s", resolvedVLLMMetricsTarget)
	}
	debugf("resolved config path: %q", cleanConfigPath)
	discoveredVersion, err := discoverVLLMVersion(
		ctx,
		strings.TrimSpace(*vllmVersion),
		strings.TrimSpace(*vllmBin),
		*vllmVersionTimeoutSeconds,
	)
	if err != nil {
		debugf("vLLM version discovery failed: %v", err)
		return err
	}
	recordCLIEvent("collect.discovery.complete", nil)
	if ui.Enabled() {
		ui.Stepf("vLLM v%s detected", discoveredVersion)
	}
	debugf("resolved vLLM version: %s", discoveredVersion)
	cleanMetricsPath := toAbsIfPresent(strings.TrimSpace(*metricsPath))
	runtimeConfig := discoverRuntimeVLLMConfig(ctx)
	if cleanMetricsPath == "" && *collectPrometheus {
		recordCLIEvent("collect.prometheus.start", nil)
		if ui.Enabled() {
			ui.Step("Collecting OS, GPU, and vLLM metrics...")
		}
		var collectionProgress *terminalProgressBar
		progressCallback := opts.progressCallback
		if ui.Enabled() {
			collectionProgress = ui.StartProgress("Collecting data")
			localProgress := func(update CollectionProgressUpdate) {
				if collectionProgress == nil {
					return
				}
				detail := fmt.Sprintf("sampling every %ds", *stepSeconds)
				collectionProgress.Update(update.Progress, update.Remaining, detail)
			}
			if progressCallback == nil {
				progressCallback = localProgress
			} else {
				downstream := progressCallback
				progressCallback = func(update CollectionProgressUpdate) {
					localProgress(update)
					downstream(update)
				}
			}
		}
		debugf("collecting Prometheus metrics")
		generatedMetricsPath, err := collectPrometheusMetrics(ctx, PrometheusCollectionOptions{
			DurationSeconds:          collectionDurationSeconds,
			StepSeconds:              *stepSeconds,
			WorkDir:                  strings.TrimSpace(*prometheusWorkDir),
			PrometheusBinary:         strings.TrimSpace(*prometheusBin),
			NodeExporterBinary:       strings.TrimSpace(*nodeExporterBin),
			DCGMExporterBinary:       strings.TrimSpace(*dcgmExporterBin),
			VLLMMetricsTarget:        resolvedVLLMMetricsTarget,
			VLLMMetricsPath:          strings.TrimSpace(*vllmMetricsPath),
			CollectBCC:               *collectBCC,
			CollectPySpy:             *collectPySpy,
			CollectNSYS:              *collectNSYS,
			ProfilingDurationSeconds: collectionDurationSeconds,
			ProfilingWorkDir:         strings.TrimSpace(*profilingWorkDir),
			VLLMPID:                  *vllmPID,
			BCCBinary:                strings.TrimSpace(*bccBin),
			PySpyBinary:              strings.TrimSpace(*pySpyBin),
			NSYSBinary:               strings.TrimSpace(*nsysBin),
			ProgressCallback:         progressCallback,
		})
		if err != nil {
			if collectionProgress != nil {
				collectionProgress.Fail("collection failed")
			}
			debugf("prometheus collection failed: %v", err)
			return fmt.Errorf("prometheus collection failed: %w", err)
		}
		if collectionProgress != nil {
			collectionProgress.Complete("collection complete")
		}
		cleanMetricsPath = generatedMetricsPath
		debugf("prometheus metrics written to %s", cleanMetricsPath)
		recordCLIEvent("collect.prometheus.complete", nil)
	}

	payload, err := loadCollectorPayload(cleanMetricsPath)
	if err != nil {
		return err
	}
	payload["schema_version"] = "collector/v1"
	payload["generated_at"] = now
	payload["tool_name"] = model.ToolName
	payload["tool_version"] = model.ToolVersion
	payload["deployment_type"] = normalizeDeploymentType(*deploymentType)
	if discoveredVersion != "" {
		payload["vllm_version"] = discoveredVersion
	}
	osInfo, gpuInfo, warnings := analyzer.CollectEnvironment(context.Background())
	payload["os_information"] = osInfo
	payload["gpu_information"] = gpuInfo
	if len(warnings) > 0 {
		payload["warnings"] = warnings
	}
	fileConfig, err := analyzer.LoadConfigFile(cleanConfigPath)
	if err != nil {
		return fmt.Errorf("config parse failed: %w", err)
	}
	if effectiveConfig := mergeAnyMaps(payloadMap(payload["current_vllm_configurations"]), fileConfig, runtimeConfig); len(effectiveConfig) > 0 {
		payload["current_vllm_configurations"] = effectiveConfig
	}
	resolvedWorkloadProfilePath, err := resolveWorkloadProfilePath(*workloadProfilePath, *intentPath)
	if err != nil {
		return err
	}
	if resolvedWorkloadProfilePath != "" {
		profile, err := analyzer.LoadWorkloadProfileFile(resolvedWorkloadProfilePath)
		if err != nil {
			return fmt.Errorf("workload profile parse failed: %w", err)
		}
		payload["workload_profile"] = profile
	}

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := saveCollectorJSON(absOutput, payload); err != nil {
		return err
	}
	debugf("collector report written to %s", absOutput)
	recordCLIEvent("collect.complete", nil)

	if ui.Enabled() {
		ui.Stepf("Collector output saved -> %s", filepath.Base(absOutput))
	} else {
		fmt.Fprintln(stdout, absOutput)
	}
	return nil
}

func resolveCollectionDurationSeconds(durationSeconds int, durationSecondsProvided bool, durationMinutes int, durationMinutesProvided bool) (int, string, error) {
	switch {
	case durationSecondsProvided:
		if durationSeconds <= 0 {
			return 0, "", fmt.Errorf("duration-seconds must be > 0")
		}
		return durationSeconds, "seconds", nil
	case durationMinutesProvided:
		if durationMinutes <= 0 {
			return 0, "", fmt.Errorf("duration-minutes must be > 0")
		}
		return durationMinutes * 60, "minutes_legacy", nil
	default:
		if durationSeconds <= 0 {
			return 0, "", fmt.Errorf("duration-seconds must be > 0")
		}
		return durationSeconds, "default", nil
	}
}

func validateDeploymentType(value string) error {
	switch normalizeDeploymentType(value) {
	case "host", "docker", "k8s":
		return nil
	default:
		return fmt.Errorf("invalid deployment-type %q: must be host, docker, or k8s", value)
	}
}

func normalizeDeploymentType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "kubernetes":
		return "k8s"
	default:
		return normalized
	}
}

func applyVersionOverride(report *model.AnalysisReport, version string) {
	normalized := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if normalized == "" || strings.EqualFold(normalized, "unknown") {
		return
	}
	report.VLLMInformation.VLLMVersion = normalized
}

func loadCollectorPayload(path string) (map[string]any, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]any{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func saveCollectorJSON(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func mergeAnyMaps(maps ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, source := range maps {
		for key, value := range source {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func payloadMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func toAbsIfPresent(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
