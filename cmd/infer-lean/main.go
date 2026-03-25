package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

var cliInput io.Reader = os.Stdin

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

func runMain(args []string, stdout, stderr io.Writer) (exitCode int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			recordRecoveredPanic(recovered, debug.Stack())
			fmt.Fprintf(stderr, "panic recovered: %v\n", recovered)
			exitCode = 2
		}
	}()
	return Execute(args, stdout, stderr)
}

// Execute runs the CLI and returns a process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	if err := ensureInferleanLocalConfig(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	tryPushCrashlyticsFile()
	commandName := classifyCLICommand(args)
	recordCLIEvent("command.start", map[string]string{"command": commandName})
	exitCode := executeCLIArgs(args, stdout, stderr)
	recordCLIEvent("command.finish", map[string]string{
		"command":   commandName,
		"exit_code": fmt.Sprintf("%d", exitCode),
	})
	return exitCode
}

func executeCLIArgs(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if err := runEndToEnd(nil, stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "collect":
		if err := runCollect(args[1:], stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "analyze":
		if err := runAnalyze(args[1:], stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "intent":
		if err := runIntent(args[1:], stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "recommend":
		if err := runRecommend(args[1:], stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "run":
		if err := runEndToEnd(args[1:], stdout, stderr); err != nil {
			if err == errHelpRequested {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		if strings.HasPrefix(args[0], "-") {
			if err := runEndToEnd(args, stdout, stderr); err != nil {
				if err == errHelpRequested {
					return 0
				}
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func classifyCLICommand(args []string) string {
	if len(args) == 0 {
		return "run"
	}
	first := strings.TrimSpace(args[0])
	switch first {
	case "collect", "analyze", "intent", "recommend", "run", "help", "-h", "--help":
		return first
	default:
		if strings.HasPrefix(first, "-") {
			return "run"
		}
		return "unknown"
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "inferLean")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  inferLean             Run end-to-end workflow (same as inferLean run)")
	fmt.Fprintln(w, "  inferLean collect [flags]")
	fmt.Fprintln(w, "  inferLean analyze [flags]")
	fmt.Fprintln(w, "  inferLean intent [flags]")
	fmt.Fprintln(w, "  inferLean recommend [flags]")
	fmt.Fprintln(w, "  inferLean run [collect flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Collect flags:")
	fmt.Fprintln(w, "  --output <path>           Write the collector JSON to this path (default: collector-report.json)")
	fmt.Fprintln(w, "  --vllm-version <string>   Optional version override (auto-discovered from vLLM binary when omitted)")
	fmt.Fprintln(w, "  --vllm-bin <path>         vLLM binary path (required only when auto-discovery cannot find it)")
	fmt.Fprintf(w, "  --vllm-version-timeout-seconds <int> Timeout for each vLLM version probe command in seconds (default: %d)\n", defaultVLLMVersionProbeTimeoutSeconds)
	fmt.Fprintln(w, "  --deployment-type <type>  host, docker, or k8s")
	fmt.Fprintln(w, "  --metrics-file <path>     Optional JSON metrics input")
	fmt.Fprintln(w, "  --config-file <path>      Optional vLLM config path (auto-discovered when omitted)")
	fmt.Fprintln(w, "  --workload-profile-file <path> Optional workload profile JSON/YAML input")
	fmt.Fprintln(w, "  --intent-file <path>      Optional declared-intent JSON input (same schema as workload-profile)")
	fmt.Fprintln(w, "  --collect-prometheus      Run prometheus/node_exporter/dcgm-exporter when metrics-file is not provided (default: true)")
	fmt.Fprintf(w, "  --duration-minutes <int>  Prometheus collection duration in minutes (default: %d)\n", defaultCollectionDurationMinutes)
	fmt.Fprintf(w, "  --prometheus-step-seconds Prometheus query range step in seconds (default: %d)\n", defaultPrometheusStepSeconds)
	fmt.Fprintln(w, "  --prometheus-bin <path>   Prometheus binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "  --node-exporter-bin <path> node_exporter binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "  --dcgm-exporter-bin <path> dcgm-exporter binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "  --vllm-metrics-target <host:port> vLLM Prometheus target (default: auto-discovered vLLM port, fallback 127.0.0.1:8000)")
	fmt.Fprintln(w, "  --vllm-metrics-path <path> vLLM metrics path (default: /metrics)")
	fmt.Fprintln(w, "  --prometheus-workdir <path> Working directory for temporary Prometheus files (default: temp dir)")
	fmt.Fprintln(w, "  --plain-output            Disable styled terminal output and print only the report path")
	fmt.Fprintln(w, "  --debug                   Enable verbose debug logs")
	fmt.Fprintln(w, "  --enable-profiling        Enable advanced profiling collection (default: true)")
	fmt.Fprintln(w, "  --collect-bcc             Collect bcc profile output for vLLM PID (default: true)")
	fmt.Fprintln(w, "  --collect-py-spy          Collect py-spy stack dump for vLLM PID (default: true)")
	fmt.Fprintln(w, "  --collect-nsys            Collect NVIDIA Nsight Systems profile for vLLM PID (default: true)")
	fmt.Fprintln(w, "  --profiling-workdir <path> Directory to store profiler artifacts/logs (default: prometheus workdir/profiling)")
	fmt.Fprintln(w, "  --vllm-pid <int>          Explicit vLLM PID (default: auto-detect)")
	fmt.Fprintln(w, "  --bcc-bin <path>          bcc profile binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "  --py-spy-bin <path>       py-spy binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "  --nsys-bin <path>         nsys binary path (empty means auto-install/auto-detect)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Intent command:")
	fmt.Fprintln(w, "  interactively capture declared workload goals and save them as workload-profile JSON")
	fmt.Fprintln(w, "  --output <path>           Write the intent JSON to this path (default: workload-intent.json)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Analyze flags:")
	fmt.Fprintln(w, "  --output <path>                Write the analyzer JSON to this path (default: analysis-report.json)")
	fmt.Fprintln(w, "  --collector-file <path>        Collector report JSON to consume (default: collector-report.json)")
	fmt.Fprintln(w, "  --config-file <path>           Optional config override when collector output lacks effective settings")
	fmt.Fprintln(w, "  --workload-profile-file <path> Optional workload profile override")
	fmt.Fprintln(w, "  --intent-file <path>           Optional declared-intent JSON override (same schema as workload-profile)")
	fmt.Fprintln(w, "  --plain-output                 Disable styled terminal output and print only the report path")
	fmt.Fprintln(w, "  --llm-enhance                  Add optional llm_enhanced output when env vars are configured")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Recommend flags:")
	fmt.Fprintln(w, "  --analysis-file <path>    Analyzer report JSON to consume (default: analysis-report.json)")
	fmt.Fprintln(w, "  --corpus-file <path>      Optional local benchmark corpus JSON file used for calibration")
	fmt.Fprintln(w, "  --objective <value>       balanced, throughput_first, or latency_first (default: workload profile or balanced)")
	fmt.Fprintln(w, "  --set key=value           Explicit what-if parameter override (repeatable)")
	fmt.Fprintln(w, "  --plain-output            Disable styled terminal output and print only the report path")
	fmt.Fprintln(w, "  --llm-enhance             Add optional llm_enhanced output when env vars are configured")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run command:")
	fmt.Fprintln(w, "  collect + trigger backend job + wait for analysis/recommendation + print top issue/recommendation")
	fmt.Fprintln(w, "  INFERLEAN_BASE_URL sets backend/dashboard base URL (default: https://app.inferlean.com)")
	fmt.Fprintln(w, "  INFERLEAN_AUTH_TOKEN optionally sets a bearer token for authenticated backend routes")
	fmt.Fprintln(w, "  run accepts the same flags as collect")
}
