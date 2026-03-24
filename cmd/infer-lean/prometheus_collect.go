package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

type PrometheusCollectionOptions struct {
	DurationMinutes          int
	StepSeconds              int
	WorkDir                  string
	PrometheusBinary         string
	NodeExporterBinary       string
	DCGMExporterBinary       string
	VLLMMetricsTarget        string
	VLLMMetricsPath          string
	NodeExporterPort         int
	DCGMExporterPort         int
	PrometheusPort           int
	CollectBCC               bool
	CollectPySpy             bool
	CollectNSYS              bool
	ProfilingDurationSeconds int
	ProfilingWorkDir         string
	VLLMPID                  int
	BCCBinary                string
	PySpyBinary              string
	NSYSBinary               string
}

type promQuery struct {
	name string
	expr string
}

var defaultPromQueries = []promQuery{
	{name: "node_cpu_utilization_pct", expr: `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[1m])) * 100)`},
	{name: "node_memory_total_bytes", expr: `sum(node_memory_MemTotal_bytes)`},
	{name: "node_memory_available_bytes", expr: `sum(node_memory_MemAvailable_bytes)`},
	{name: "node_disk_total_bytes", expr: `sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})`},
	{name: "node_disk_available_bytes", expr: `sum(node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})`},
	{name: "gpu_utilization_pct", expr: `avg(DCGM_FI_DEV_GPU_UTIL)`},
	{name: "gpu_fb_used_bytes", expr: `sum(DCGM_FI_DEV_FB_USED) * 1048576`},
	{name: "gpu_fb_free_bytes", expr: `sum(DCGM_FI_DEV_FB_FREE) * 1048576`},
}

func promQueriesForCollection(includeDCGM bool) []promQuery {
	if includeDCGM {
		return defaultPromQueries
	}
	filtered := make([]promQuery, 0, len(defaultPromQueries))
	for _, query := range defaultPromQueries {
		if strings.Contains(strings.ToUpper(query.expr), "DCGM_") {
			continue
		}
		filtered = append(filtered, query)
	}
	return filtered
}

type managedProcess struct {
	name    string
	cmd     *exec.Cmd
	logPath string
	logFile *os.File
	done    chan error
}

func collectPrometheusMetrics(ctx context.Context, opts PrometheusCollectionOptions) (string, error) {
	debugf("prometheus collection: started duration_minutes=%d step_seconds=%d", opts.DurationMinutes, opts.StepSeconds)
	duration := time.Duration(opts.DurationMinutes) * time.Minute
	if opts.DurationMinutes <= 0 {
		duration = 10 * time.Minute
	}
	profilingDurationSeconds := opts.ProfilingDurationSeconds
	if profilingDurationSeconds <= 0 {
		profilingDurationSeconds = int(duration.Seconds())
	}
	step := time.Duration(opts.StepSeconds) * time.Second
	if opts.StepSeconds <= 0 {
		step = 30 * time.Second
	}
	promBinary, err := resolveOrInstallTool(ctx, strings.TrimSpace(opts.PrometheusBinary), toolInstallSpec{
		Name:           "prometheus",
		LookupNames:    []string{"prometheus"},
		APTPackages:    [][]string{{"prometheus"}},
		DNFPackages:    [][]string{{"prometheus2"}, {"prometheus"}},
		YUMPackages:    [][]string{{"prometheus2"}, {"prometheus"}},
		PacmanPackages: [][]string{{"prometheus"}},
	})
	if err != nil {
		return "", err
	}
	debugf("prometheus collection: using prometheus binary %s", promBinary)
	nodeBinary, err := resolveOrInstallTool(ctx, strings.TrimSpace(opts.NodeExporterBinary), toolInstallSpec{
		Name:           "node_exporter",
		LookupNames:    []string{"node_exporter", "prometheus-node-exporter"},
		APTPackages:    [][]string{{"prometheus-node-exporter"}},
		DNFPackages:    [][]string{{"prometheus-node-exporter"}},
		YUMPackages:    [][]string{{"prometheus-node-exporter"}},
		PacmanPackages: [][]string{{"prometheus-node-exporter"}},
	})
	if err != nil {
		return "", err
	}
	debugf("prometheus collection: using node_exporter binary %s", nodeBinary)
	nodePort := opts.NodeExporterPort
	if nodePort <= 0 {
		nodePort = 9100
	}
	dcgmPort := opts.DCGMExporterPort
	if dcgmPort <= 0 {
		dcgmPort = 9400
	}
	vllmTarget := strings.TrimSpace(opts.VLLMMetricsTarget)
	if vllmTarget == "" {
		vllmTarget = "127.0.0.1:8000"
	}
	vllmMetricsPath := strings.TrimSpace(opts.VLLMMetricsPath)
	if vllmMetricsPath == "" {
		vllmMetricsPath = "/metrics"
	}
	if !strings.HasPrefix(vllmMetricsPath, "/") {
		vllmMetricsPath = "/" + vllmMetricsPath
	}

	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		tmp, err := os.MkdirTemp("", "InferLean-prometheus-")
		if err != nil {
			return "", fmt.Errorf("create prometheus work dir: %w", err)
		}
		workDir = tmp
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("create prometheus work dir: %w", err)
	}
	debugf("prometheus collection: workdir=%s", workDir)

	promPort := opts.PrometheusPort
	if promPort <= 0 {
		port, err := reserveFreePort()
		if err != nil {
			return "", fmt.Errorf("find free prometheus port: %w", err)
		}
		promPort = port
	}
	debugf("prometheus collection: prometheus port=%d node_port=%d dcgm_port=%d", promPort, nodePort, dcgmPort)

	cfgPath := filepath.Join(workDir, "prometheus.yml")
	cfg := buildPrometheusConfig(nodePort, dcgmPort, vllmTarget, vllmMetricsPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return "", fmt.Errorf("write prometheus config: %w", err)
	}

	var started []*managedProcess
	collectionOutputs := map[string]string{
		"prometheus_binary":    promBinary,
		"node_exporter_binary": nodeBinary,
	}
	setDCGMWarning := func(message string) {
		trimmed := strings.TrimSpace(message)
		if trimmed == "" {
			return
		}
		collectionOutputs["dcgm_exporter_warning"] = trimmed
		debugf("prometheus collection: %s", trimmed)
	}
	stopAll := func() {
		for i := len(started) - 1; i >= 0; i-- {
			started[i].stop()
		}
	}
	defer stopAll()

	nodeURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", nodePort)
	if !endpointReady(ctx, nodeURL) {
		debugf("prometheus collection: starting node_exporter")
		proc, err := startProcess(ctx, workDir, "node_exporter", nodeBinary)
		if err != nil {
			return "", err
		}
		started = append(started, proc)
		if err := waitForEndpoint(ctx, nodeURL, 45*time.Second, proc); err != nil {
			return "", err
		}
		collectionOutputs["node_exporter_start"] = "started by InferLean"
	} else {
		collectionOutputs["node_exporter_start"] = "reused existing process"
		debugf("prometheus collection: reusing existing node_exporter endpoint")
	}

	collectDCGMMetrics := false
	collectionOutputs["dcgm_exporter_start"] = "skipped"
	dcgmURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", dcgmPort)
	if endpointReady(ctx, dcgmURL) {
		collectDCGMMetrics = true
		collectionOutputs["dcgm_exporter_start"] = "reused existing process"
		collectionOutputs["dcgm_exporter_binary"] = "reused existing endpoint"
		debugf("prometheus collection: reusing existing dcgm_exporter endpoint")
	} else {
		dcgmBinary, err := resolveOrInstallTool(ctx, strings.TrimSpace(opts.DCGMExporterBinary), toolInstallSpec{
			Name:           "dcgm-exporter",
			LookupNames:    []string{"dcgm-exporter"},
			APTPackages:    [][]string{{"dcgm-exporter"}, {"nvidia-dcgm-exporter"}},
			DNFPackages:    [][]string{{"dcgm-exporter"}, {"nvidia-dcgm-exporter"}},
			YUMPackages:    [][]string{{"dcgm-exporter"}, {"nvidia-dcgm-exporter"}},
			ZypperPackages: [][]string{{"dcgm-exporter"}},
			FallbackCommands: [][]string{
				{"go", "install", "github.com/NVIDIA/dcgm-exporter/cmd/dcgm-exporter@latest"},
			},
			PrivilegedFallbackCommands: [][]string{
				{"apt-get", "install", "-y", "golang-go", "git"},
				{"sh", "-lc", "rm -rf /tmp/InferLean-dcgm-exporter-src && git clone --depth 1 --branch 3.1.8-3.1.5 https://github.com/NVIDIA/dcgm-exporter.git /tmp/InferLean-dcgm-exporter-src && cd /tmp/InferLean-dcgm-exporter-src && go build -o /usr/local/bin/dcgm-exporter ./cmd/dcgm-exporter"},
			},
		})
		if err != nil {
			collectionOutputs["dcgm_exporter_binary"] = "not available"
			setDCGMWarning(fmt.Sprintf("dcgm exporter unavailable; continuing without DCGM metrics: %v", err))
		} else {
			collectionOutputs["dcgm_exporter_binary"] = dcgmBinary
			debugf("prometheus collection: using dcgm_exporter binary %s", dcgmBinary)
			dcgmMetricsCSVPath, err := resolveDCGMMetricsCSVPath(workDir)
			if err != nil {
				setDCGMWarning(fmt.Sprintf("dcgm exporter metrics file unavailable; continuing without DCGM metrics: %v", err))
			} else {
				debugf("prometheus collection: starting dcgm_exporter with csv=%s", dcgmMetricsCSVPath)
				proc, err := startProcess(ctx, workDir, "dcgm_exporter", dcgmBinary, "-f", dcgmMetricsCSVPath)
				if err != nil {
					setDCGMWarning(fmt.Sprintf("dcgm exporter failed to start; continuing without DCGM metrics: %v", err))
				} else {
					started = append(started, proc)
					if err := waitForEndpoint(ctx, dcgmURL, 60*time.Second, proc); err != nil {
						setDCGMWarning(fmt.Sprintf("dcgm exporter did not become ready; continuing without DCGM metrics: %v", err))
					} else {
						collectDCGMMetrics = true
						collectionOutputs["dcgm_exporter_start"] = "started by InferLean"
					}
				}
			}
		}
	}

	vllmURL := fmt.Sprintf("http://%s%s", vllmTarget, vllmMetricsPath)
	if !endpointReady(ctx, vllmURL) {
		return "", fmt.Errorf("vllm metrics endpoint is not reachable at %s", vllmURL)
	}
	debugf("prometheus collection: vLLM endpoint reachable at %s", vllmURL)

	promURL := fmt.Sprintf("http://127.0.0.1:%d", promPort)
	proc, err := startProcess(
		ctx,
		workDir,
		"prometheus",
		promBinary,
		"--config.file="+cfgPath,
		"--storage.tsdb.path="+filepath.Join(workDir, "tsdb"),
		"--web.listen-address=127.0.0.1:"+strconv.Itoa(promPort),
		"--log.level=error",
	)
	if err != nil {
		return "", err
	}
	debugf("prometheus collection: starting prometheus")
	started = append(started, proc)
	if err := waitForEndpoint(ctx, promURL+"/-/ready", 60*time.Second, proc); err != nil {
		return "", err
	}
	collectionOutputs["prometheus_start"] = "started by InferLean"

	var profilingResult *model.AdvancedProfilingInfo
	var profilingCh chan model.AdvancedProfilingInfo
	if opts.CollectBCC || opts.CollectPySpy || opts.CollectNSYS {
		profilingWorkDir := strings.TrimSpace(opts.ProfilingWorkDir)
		if profilingWorkDir == "" {
			profilingWorkDir = filepath.Join(workDir, "profiling")
		}
		profilingCh = make(chan model.AdvancedProfilingInfo, 1)
		profilingOpts := AdvancedProfilingOptions{
			CollectBCC:      opts.CollectBCC,
			CollectPySpy:    opts.CollectPySpy,
			CollectNSYS:     opts.CollectNSYS,
			DurationSeconds: profilingDurationSeconds,
			VLLMPID:         opts.VLLMPID,
			WorkDir:         profilingWorkDir,
			BCCBinary:       opts.BCCBinary,
			PySpyBinary:     opts.PySpyBinary,
			NSYSBinary:      opts.NSYSBinary,
		}
		go func() {
			profilingCh <- collectAdvancedProfiling(ctx, profilingOpts)
		}()
	}

	start := time.Now().UTC()
	debugf("prometheus collection: sampling started at %s for %s", start.Format(time.RFC3339), duration)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(duration):
	}
	end := time.Now().UTC()
	debugf("prometheus collection: sampling ended at %s", end.Format(time.RFC3339))

	points := map[int64]map[string]float64{}
	collectedQueries := 0
	for _, q := range promQueriesForCollection(collectDCGMMetrics) {
		debugf("prometheus collection: query aggregate metric %s", q.name)
		values, err := queryPrometheusRange(ctx, promURL, q.expr, start, end, step)
		if err != nil {
			continue
		}
		if len(values) == 0 {
			continue
		}
		collectedQueries++
		for ts, value := range values {
			if _, ok := points[ts]; !ok {
				points[ts] = map[string]float64{}
			}
			points[ts][q.name] = value
		}
	}
	vllmSeries, err := queryPrometheusMultiMetricRange(
		ctx,
		promURL,
		`{job="vllm",__name__=~"vllm:.*"}`,
		start,
		end,
		step,
	)
	if err == nil {
		for metricName, series := range vllmSeries {
			for ts, value := range series {
				if _, ok := points[ts]; !ok {
					points[ts] = map[string]float64{}
				}
				points[ts][metricName] = value
			}
		}
		if len(vllmSeries) > 0 {
			collectedQueries++
			debugf("prometheus collection: collected %d vLLM metric series", len(vllmSeries))
		}
	}
	vllmHistogramSeries, err := queryPrometheusLabeledMetricRange(
		ctx,
		promURL,
		`{job="vllm",__name__="vllm:e2e_request_latency_seconds_bucket"}`,
		start,
		end,
		step,
	)
	if err == nil {
		for metricName, series := range vllmHistogramSeries {
			for ts, value := range series {
				if _, ok := points[ts]; !ok {
					points[ts] = map[string]float64{}
				}
				points[ts][metricName] = value
			}
		}
		if len(vllmHistogramSeries) > 0 {
			collectedQueries++
			debugf("prometheus collection: collected %d vLLM histogram series", len(vllmHistogramSeries))
		}
	}
	nodeSeries, err := queryPrometheusLabeledMetricRange(
		ctx,
		promURL,
		`{job="node_exporter"}`,
		start,
		end,
		step,
	)
	if err == nil {
		for metricName, series := range nodeSeries {
			for ts, value := range series {
				if _, ok := points[ts]; !ok {
					points[ts] = map[string]float64{}
				}
				points[ts][metricName] = value
			}
		}
		if len(nodeSeries) > 0 {
			collectedQueries++
			debugf("prometheus collection: collected %d node_exporter metric series", len(nodeSeries))
		}
	}
	if collectDCGMMetrics {
		dcgmSeries, err := queryPrometheusLabeledMetricRange(
			ctx,
			promURL,
			`{job="dcgm_exporter"}`,
			start,
			end,
			step,
		)
		if err == nil {
			for metricName, series := range dcgmSeries {
				for ts, value := range series {
					if _, ok := points[ts]; !ok {
						points[ts] = map[string]float64{}
					}
					points[ts][metricName] = value
				}
			}
			if len(dcgmSeries) > 0 {
				collectedQueries++
				debugf("prometheus collection: collected %d dcgm_exporter metric series", len(dcgmSeries))
			}
		}
	}

	if profilingCh != nil {
		profile := <-profilingCh
		profilingResult = &profile
	}
	for _, proc := range started {
		if strings.TrimSpace(proc.logPath) == "" {
			continue
		}
		logData, readErr := os.ReadFile(proc.logPath)
		if readErr != nil {
			collectionOutputs[proc.name+"_output_error"] = readErr.Error()
			continue
		}
		if len(logData) > 0 {
			collectionOutputs[proc.name+"_output"] = string(logData)
		}
	}

	if collectedQueries == 0 || len(points) == 0 {
		return "", errors.New("prometheus returned no metric values from node exporter/dcgm exporter/vllm")
	}
	debugf("prometheus collection: built %d timestamp points", len(points))

	timestamps := make([]int64, 0, len(points))
	for ts := range points {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })

	collected := make([]model.CollectedMetricPoint, 0, len(timestamps))
	for _, ts := range timestamps {
		metrics := points[ts]
		if len(metrics) == 0 {
			continue
		}
		collected = append(collected, model.CollectedMetricPoint{
			TimeLabel: time.UnixMilli(ts).UTC().Format(time.RFC3339),
			Metrics:   metrics,
		})
	}

	payload := map[string]any{
		"deployment_type":           "host",
		"collected_metrics":         collected,
		"metric_collection_outputs": collectionOutputs,
	}
	if profilingResult != nil {
		payload["advanced_profiling_information"] = profilingResult
	}

	metricsPath := filepath.Join(workDir, "prometheus-metrics.json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal prometheus metrics: %w", err)
	}
	if err := os.WriteFile(metricsPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write prometheus metrics: %w", err)
	}
	debugf("prometheus collection: metrics payload written to %s", metricsPath)
	return metricsPath, nil
}

func buildPrometheusConfig(nodePort, dcgmPort int, vllmTarget, vllmMetricsPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `global:
  scrape_interval: 15s

scrape_configs:
  - job_name: "node_exporter"
    static_configs:
      - targets: ["127.0.0.1:%d"]
`, nodePort)
	if dcgmPort > 0 {
		fmt.Fprintf(&b, `
  - job_name: "dcgm_exporter"
    static_configs:
      - targets: ["127.0.0.1:%d"]
`, dcgmPort)
	}
	fmt.Fprintf(&b, `
  - job_name: "vllm"
    metrics_path: "%s"
    static_configs:
      - targets: ["%s"]
`, vllmMetricsPath, vllmTarget)
	return b.String()
}

func defaultDCGMCSVContent() string {
	return strings.Join([]string{
		// Core GPU telemetry
		"DCGM_FI_DEV_GPU_UTIL, gauge, GPU utilization (in %).",
		"DCGM_FI_DEV_FB_USED, gauge, Framebuffer memory used (in MiB).",
		"DCGM_FI_DEV_FB_FREE, gauge, Framebuffer memory free (in MiB).",
		"DCGM_FI_DEV_FB_TOTAL, gauge, Framebuffer memory total (in MiB).",
		"DCGM_FI_DEV_MEM_COPY_UTIL, gauge, Memory utilization (in %).",
		"DCGM_FI_DEV_POWER_USAGE, gauge, Power draw (in W).",
		"DCGM_FI_DEV_POWER_MGMT_LIMIT, gauge, Power management limit (in W).",
		"DCGM_FI_DEV_GPU_TEMP, gauge, GPU temperature (in C).",
		"DCGM_FI_DEV_SM_CLOCK, gauge, SM clock frequency (in MHz).",
		"DCGM_FI_DEV_MEM_CLOCK, gauge, Memory clock frequency (in MHz).",
		"DCGM_FI_DEV_VIDEO_CLOCK, gauge, Video clock frequency (in MHz).",
		"DCGM_FI_DEV_APP_SM_CLOCK, gauge, Application SM clock (in MHz).",
		"DCGM_FI_DEV_APP_MEM_CLOCK, gauge, Application memory clock (in MHz).",
		"DCGM_FI_DEV_PCIE_REPLAY_COUNTER, counter, PCIe replay counter.",
		"DCGM_FI_DEV_PCIE_TX_THROUGHPUT, gauge, PCIe TX throughput.",
		"DCGM_FI_DEV_PCIE_RX_THROUGHPUT, gauge, PCIe RX throughput.",
		"DCGM_FI_DEV_XID_ERRORS, counter, XID errors.",
		"DCGM_FI_DEV_ECC_SBE_VOL_TOTAL, counter, ECC single-bit volatile errors.",
		"DCGM_FI_DEV_ECC_DBE_VOL_TOTAL, counter, ECC double-bit volatile errors.",
		"DCGM_FI_DEV_ECC_SBE_AGG_TOTAL, counter, ECC single-bit aggregate errors.",
		"DCGM_FI_DEV_ECC_DBE_AGG_TOTAL, counter, ECC double-bit aggregate errors.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL, counter, NVLink total bandwidth.",
		"DCGM_FI_DEV_NVLINK_CRC_FLIT_ERROR_COUNT_TOTAL, counter, NVLink CRC FLIT errors.",
		"DCGM_FI_DEV_NVLINK_CRC_DATA_ERROR_COUNT_TOTAL, counter, NVLink CRC data errors.",
		"DCGM_FI_DEV_NVLINK_REPLAY_ERROR_COUNT_TOTAL, counter, NVLink replay errors.",
		"DCGM_FI_DEV_NVLINK_RECOVERY_ERROR_COUNT_TOTAL, counter, NVLink recovery errors.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L0, counter, NVLink lane 0 bandwidth.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L1, counter, NVLink lane 1 bandwidth.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L2, counter, NVLink lane 2 bandwidth.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L3, counter, NVLink lane 3 bandwidth.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L4, counter, NVLink lane 4 bandwidth.",
		"DCGM_FI_DEV_NVLINK_BANDWIDTH_L5, counter, NVLink lane 5 bandwidth.",
		// Profiling counters (may require admin/profiler permissions)
		"DCGM_FI_PROF_GR_ENGINE_ACTIVE, gauge, Graphics/compute engine active ratio.",
		"DCGM_FI_PROF_SM_ACTIVE, gauge, SM active ratio.",
		"DCGM_FI_PROF_SM_OCCUPANCY, gauge, SM occupancy ratio.",
		"DCGM_FI_PROF_PIPE_TENSOR_ACTIVE, gauge, Tensor pipeline active ratio.",
		"DCGM_FI_PROF_PIPE_FP64_ACTIVE, gauge, FP64 pipeline active ratio.",
		"DCGM_FI_PROF_PIPE_FP32_ACTIVE, gauge, FP32 pipeline active ratio.",
		"DCGM_FI_PROF_PIPE_FP16_ACTIVE, gauge, FP16 pipeline active ratio.",
		"DCGM_FI_PROF_DRAM_ACTIVE, gauge, DRAM active ratio.",
		"DCGM_FI_PROF_NVLINK_TX_BYTES, counter, NVLink TX bytes.",
		"DCGM_FI_PROF_NVLINK_RX_BYTES, counter, NVLink RX bytes.",
		"DCGM_FI_PROF_PCIE_TX_BYTES, counter, PCIe TX bytes.",
		"DCGM_FI_PROF_PCIE_RX_BYTES, counter, PCIe RX bytes.",
		"",
	}, "\n")
}

func resolveDCGMMetricsCSVPath(workDir string) (string, error) {
	candidates := []string{
		"/etc/dcgm-exporter/default-counters.csv",
		"/tmp/InferLean-dcgm-exporter-src/etc/default-counters.csv",
		"/tmp/dcgm-exporter-src/etc/default-counters.csv",
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	path := filepath.Join(workDir, "dcgm-metrics.csv")
	if err := os.WriteFile(path, []byte(defaultDCGMCSVContent()), 0o600); err != nil {
		return "", fmt.Errorf("write dcgm metrics file: %w", err)
	}
	return path, nil
}

func startProcess(ctx context.Context, workDir, name, binary string, args ...string) (*managedProcess, error) {
	logPath := filepath.Join(workDir, name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create %s log: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s (%s): %w", name, binary, err)
	}
	proc := &managedProcess{
		name:    name,
		cmd:     cmd,
		logPath: logPath,
		logFile: logFile,
		done:    make(chan error, 1),
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()
	return proc, nil
}

func (p *managedProcess) stop() {
	if p == nil {
		return
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	}
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
}

func waitForEndpoint(ctx context.Context, endpoint string, timeout time.Duration, proc *managedProcess) error {
	deadline := time.Now().Add(timeout)
	for {
		if endpointReady(ctx, endpoint) {
			return nil
		}
		if proc != nil {
			select {
			case err := <-proc.done:
				if err == nil {
					err = errors.New("exited")
				}
				logTail, _ := tailFile(proc.logPath, 30)
				return fmt.Errorf("%s exited before %s became ready: %v\nlog: %s\n%s", proc.name, endpoint, err, proc.logPath, logTail)
			default:
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for endpoint %s", endpoint)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func endpointReady(ctx context.Context, endpoint string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func reserveFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected listener addr type")
	}
	return addr.Port, nil
}

type promMatrixResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func queryPrometheusRange(ctx context.Context, baseURL, query string, start, end time.Time, step time.Duration) (map[int64]float64, error) {
	debugf("prometheus query(range): %s", debugSnippet(query, 240))
	u, err := url.Parse(baseURL + "/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 3, 64))
	q.Set("end", strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 3, 64))
	q.Set("step", strconv.Itoa(int(step.Seconds())))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("prometheus query failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload promMatrixResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return nil, fmt.Errorf("prometheus query error: %s", payload.Error)
		}
		return nil, fmt.Errorf("prometheus query status: %s", payload.Status)
	}
	acc := map[int64][]float64{}
	for _, series := range payload.Data.Result {
		for _, pair := range series.Values {
			if len(pair) != 2 {
				continue
			}
			ts, ok := asFloat64(pair[0])
			if !ok {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(pair[1])), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			key := int64(math.Round(ts * 1000))
			acc[key] = append(acc[key], value)
		}
	}
	out := map[int64]float64{}
	for ts, values := range acc {
		if len(values) == 0 {
			continue
		}
		var sum float64
		for _, value := range values {
			sum += value
		}
		out[ts] = sum / float64(len(values))
	}
	return out, nil
}

func queryPrometheusMultiMetricRange(ctx context.Context, baseURL, query string, start, end time.Time, step time.Duration) (map[string]map[int64]float64, error) {
	debugf("prometheus query(multi): %s", debugSnippet(query, 240))
	u, err := url.Parse(baseURL + "/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 3, 64))
	q.Set("end", strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 3, 64))
	q.Set("step", strconv.Itoa(int(step.Seconds())))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("prometheus query failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload promMatrixResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return nil, fmt.Errorf("prometheus query error: %s", payload.Error)
		}
		return nil, fmt.Errorf("prometheus query status: %s", payload.Status)
	}

	acc := map[string]map[int64][]float64{}
	for _, series := range payload.Data.Result {
		name := strings.TrimSpace(series.Metric["__name__"])
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "_bucket") {
			continue
		}
		if _, ok := acc[name]; !ok {
			acc[name] = map[int64][]float64{}
		}
		for _, pair := range series.Values {
			if len(pair) != 2 {
				continue
			}
			ts, ok := asFloat64(pair[0])
			if !ok {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(pair[1])), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			key := int64(math.Round(ts * 1000))
			acc[name][key] = append(acc[name][key], value)
		}
	}

	out := map[string]map[int64]float64{}
	for metricName, tsMap := range acc {
		if len(tsMap) == 0 {
			continue
		}
		out[metricName] = map[int64]float64{}
		for ts, values := range tsMap {
			if len(values) == 0 {
				continue
			}
			var sum float64
			for _, value := range values {
				sum += value
			}
			out[metricName][ts] = sum / float64(len(values))
		}
		if len(out[metricName]) == 0 {
			delete(out, metricName)
		}
	}
	return out, nil
}

func queryPrometheusLabeledMetricRange(ctx context.Context, baseURL, query string, start, end time.Time, step time.Duration) (map[string]map[int64]float64, error) {
	debugf("prometheus query(labeled): %s", debugSnippet(query, 240))
	u, err := url.Parse(baseURL + "/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 3, 64))
	q.Set("end", strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 3, 64))
	q.Set("step", strconv.Itoa(int(step.Seconds())))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("prometheus query failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload promMatrixResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return nil, fmt.Errorf("prometheus query error: %s", payload.Error)
		}
		return nil, fmt.Errorf("prometheus query status: %s", payload.Status)
	}

	out := map[string]map[int64]float64{}
	for _, series := range payload.Data.Result {
		seriesKey := buildMetricSeriesKey(series.Metric)
		if seriesKey == "" {
			continue
		}
		if _, ok := out[seriesKey]; !ok {
			out[seriesKey] = map[int64]float64{}
		}
		for _, pair := range series.Values {
			if len(pair) != 2 {
				continue
			}
			ts, ok := asFloat64(pair[0])
			if !ok {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(pair[1])), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			key := int64(math.Round(ts * 1000))
			out[seriesKey][key] = value
		}
		if len(out[seriesKey]) == 0 {
			delete(out, seriesKey)
		}
	}
	return out, nil
}

func buildMetricSeriesKey(metric map[string]string) string {
	if len(metric) == 0 {
		return ""
	}
	name := strings.TrimSpace(metric["__name__"])
	if name == "" {
		return ""
	}
	labelPairs := make([]string, 0, len(metric)-1)
	for key, value := range metric {
		key = strings.TrimSpace(key)
		if key == "" || key == "__name__" {
			continue
		}
		labelPairs = append(labelPairs, fmt.Sprintf(`%s="%s"`, key, escapePromLabelValue(value)))
	}
	if len(labelPairs) == 0 {
		return name
	}
	sort.Strings(labelPairs)
	return name + "{" + strings.Join(labelPairs, ",") + "}"
}

func escapePromLabelValue(value string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`"`, `\"`,
		"\n", `\n`,
	)
	return replacer.Replace(value)
}

func asFloat64(v any) (float64, bool) {
	switch typed := v.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		f, err := typed.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func tailFile(path string, maxLines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}
