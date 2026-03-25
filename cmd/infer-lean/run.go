package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	model "github.com/inferLean/inferlean-project/cli/contracts"
)

const (
	defaultInferleanBaseURL  = "https://app.inferlean.com"
	inferleanBaseURLEnv      = "INFERLEAN_BASE_URL"
	inferleanAuthTokenEnv    = "INFERLEAN_AUTH_TOKEN"
	defaultAnalysisETA       = 60 * time.Second
	defaultRecommendationETA = 45 * time.Second
)

var (
	openDashboardInBrowser = openBrowserURL
	runPollInterval        = 3 * time.Second
	runPollTimeout         = 15 * time.Minute
	errArtifactWaitTimeout = errors.New("artifact wait timeout")
	runCollectForRun       = runCollectWithContext
	runNotifyInterrupt     = func(ch chan<- os.Signal) { signal.Notify(ch, os.Interrupt) }
	runStopInterruptNotify = func(ch chan<- os.Signal) { signal.Stop(ch) }
)

type triggerJobAPIResponse struct {
	ID      string         `json:"id"`
	JobID   stringOrNumber `json:"job_id"`
	JobUUID string         `json:"job_uuid"`
	Status  string         `json:"status"`
}

type topRecommendationAPIResponse struct {
	JobID               string                     `json:"job_id"`
	ID                  string                     `json:"id"`
	TopIssue            string                     `json:"top_issue"`
	TopRecommendation   string                     `json:"top_recommendation"`
	CurrentLoadSummary  *model.CurrentLoadSummary  `json:"resource_load_summary,omitempty"`
	CapacityOpportunity *model.CapacityOpportunity `json:"gpu_capacity_headroom,omitempty"`
}

type backendErrorPayload struct {
	Error string `json:"error"`
}

type waitStage string

const (
	waitStageAnalysis       waitStage = "analysis"
	waitStageRecommendation waitStage = "recommendation"
)

type waitProgressUpdate struct {
	Stage    waitStage
	Progress float64
	Elapsed  time.Duration
	ETA      time.Duration
	Done     bool
}

type stringOrNumber string

func (s *stringOrNumber) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var out string
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return err
		}
		*s = stringOrNumber(strings.TrimSpace(out))
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return err
	}
	*s = stringOrNumber(strings.TrimSpace(number.String()))
	return nil
}

func runEndToEnd(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch strings.TrimSpace(args[0]) {
		case "-h", "--help", "help":
			recordCLIEvent("run.help", nil)
			printRunUsage(stderr)
			return errHelpRequested
		}
	}
	recordCLIEvent("run.start", nil)

	baseURL, err := resolveInferleanBaseURL(os.Getenv(inferleanBaseURLEnv))
	if err != nil {
		return err
	}
	ui := newTerminalUI(stdout, false)
	authToken := strings.TrimSpace(os.Getenv(inferleanAuthTokenEnv))

	tmpDir, err := os.MkdirTemp("", "inferlean-run-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	collectorPath := filepath.Join(tmpDir, "collector-report.json")
	collectArgs := append([]string{}, args...)
	collectArgs = append(collectArgs, "--output", collectorPath, "--plain-output")
	collectStepSeconds := resolveCollectStepSeconds(args)
	collectCtx, cancelCollect := context.WithCancel(context.Background())
	defer cancelCollect()
	interruptCh := make(chan os.Signal, 1)
	runNotifyInterrupt(interruptCh)
	var collectInterrupted atomic.Bool
	stopInterruptListener := make(chan struct{})
	var interruptCleanupOnce sync.Once
	cleanupCollectionInterrupt := func() {
		interruptCleanupOnce.Do(func() {
			runStopInterruptNotify(interruptCh)
			close(stopInterruptListener)
		})
	}
	defer cleanupCollectionInterrupt()
	go func() {
		select {
		case <-stopInterruptListener:
			return
		case <-interruptCh:
			collectInterrupted.Store(true)
			cancelCollect()
		}
	}()

	var collectionProgress *terminalProgressBar
	if ui.Enabled() {
		ui.Step("Collecting runtime metrics...")
		collectionProgress = ui.StartProgress("Collecting data")
	}
	recordCLIEvent("run.collect.start", nil)
	if err := runCollectForRun(collectCtx, collectArgs, io.Discard, stderr, collectRunOptions{
		progressCallback: func(update CollectionProgressUpdate) {
			if collectionProgress == nil {
				return
			}
			collectionProgress.Update(update.Progress, update.Remaining, fmt.Sprintf("sampling every %ds", collectStepSeconds))
		},
	}); err != nil {
		if collectionProgress != nil {
			collectionProgress.Fail("collection failed")
		}
		return fmt.Errorf("collect failed: %w", err)
	}
	if collectionProgress != nil {
		if collectInterrupted.Load() {
			collectionProgress.Complete("interrupted; using collected data so far")
		} else {
			collectionProgress.Complete("collection complete")
		}
	}
	if collectInterrupted.Load() {
		if ui.Enabled() {
			ui.Step("Collection interrupted by user; continuing with partial data.")
		}
		recordCLIEvent("run.collect.interrupted", nil)
	}
	recordCLIEvent("run.collect.complete", nil)
	cleanupCollectionInterrupt()

	payload, err := os.ReadFile(collectorPath)
	if err != nil {
		return fmt.Errorf("read collector output: %w", err)
	}
	if ui.Enabled() {
		ui.Step("Triggering backend job...")
	}
	recordCLIEvent("run.trigger.start", nil)
	triggerResponse, err := triggerJob(baseURL, payload, authToken)
	if err != nil {
		if isTriggerNetworkError(err) {
			manualUploadPath, persistErr := persistCollectorForManualUpload(payload)
			if persistErr != nil {
				return fmt.Errorf("%w (also failed to save collector JSON for manual upload: %v)", err, persistErr)
			}
			triggerPage := fmt.Sprintf("%s/optimizations/new", strings.TrimRight(baseURL, "/"))
			fmt.Fprintln(stderr, "warning: automatic backend trigger failed due to a network issue.")
			fmt.Fprintf(stderr, "collector JSON saved for manual upload: %s\n", manualUploadPath)
			fmt.Fprintf(stderr, "open %s and upload the saved file manually.\n", triggerPage)
			recordCLIEvent("run.trigger.network_fallback", nil)
			return nil
		}
		return err
	}
	recordCLIEvent("run.trigger.complete", nil)

	dashboardURL := fmt.Sprintf("%s/optimizations/%s", baseURL, url.PathEscape(triggerResponse.ID))
	fmt.Fprintf(stdout, "Job queued: %s\n", triggerResponse.ID)
	if ui.Enabled() {
		ui.Step("Waiting for analysis and recommendation...")
	}
	var analysisProgress *terminalProgressBar
	var recommendationProgress *terminalProgressBar
	recordCLIEvent("run.wait.start", nil)
	analysisReport, optimizationReport, err := waitForOptimizationCompletion(baseURL, triggerResponse.ID, authToken, func(update waitProgressUpdate) {
		if !ui.Enabled() {
			return
		}
		switch update.Stage {
		case waitStageAnalysis:
			if analysisProgress == nil {
				analysisProgress = ui.StartProgress("Analyzing")
			}
			if analysisProgress == nil {
				return
			}
			if update.Done {
				analysisProgress.Complete("analysis complete")
				return
			}
			analysisProgress.Update(update.Progress, update.ETA, fmt.Sprintf("elapsed %s", formatProgressETA(update.Elapsed)))
		case waitStageRecommendation:
			if recommendationProgress == nil {
				recommendationProgress = ui.StartProgress("Generating recommendation")
			}
			if recommendationProgress == nil {
				return
			}
			if update.Done {
				recommendationProgress.Complete("recommendation ready")
				return
			}
			recommendationProgress.Update(update.Progress, update.ETA, fmt.Sprintf("elapsed %s", formatProgressETA(update.Elapsed)))
		}
	})
	if err != nil {
		if recommendationProgress != nil {
			recommendationProgress.Fail("recommendation failed")
		} else if analysisProgress != nil {
			analysisProgress.Fail("analysis failed")
		}
		return fmt.Errorf("wait for job completion: %w", err)
	}
	recordCLIEvent("run.wait.complete", nil)
	if ui.Enabled() {
		renderOptimizationV2Summary(stdout, optimizationReport)
		proxySaturation := "false"
		if analysisReport != nil && analysisReport.CurrentLoadSummary != nil && strings.TrimSpace(analysisReport.CurrentLoadSummary.SaturationSource) == "approximate" {
			proxySaturation = "true"
		}
		recordCLIEvent("run.output.premium_cards", map[string]string{
			"recommendation_report_available": fmt.Sprintf("%t", optimizationReport != nil),
			"proxy_saturation":                proxySaturation,
		})
	}
	if !ui.Enabled() {
		renderOptimizationV2Summary(stdout, optimizationReport)
	}
	fmt.Fprintf(stdout, "For further details, see dashboard: %s\n", dashboardURL)

	if err := openDashboardInBrowser(dashboardURL); err != nil {
		fmt.Fprintf(stderr, "warning: unable to open browser automatically: %v\n", err)
		recordCLIEvent("run.dashboard.open_failed", nil)
		return nil
	}
	if ui.Enabled() {
		ui.Step("Dashboard opened in browser")
	}
	recordCLIEvent("run.complete", nil)
	return nil
}

func printRunUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: inferLean run [collect flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "run performs:")
	fmt.Fprintln(w, "  1) collect locally")
	fmt.Fprintln(w, "  2) POST /api/v1/optimizations to backend")
	fmt.Fprintln(w, "  3) wait for analysis + optimization report completion")
	fmt.Fprintln(w, "  4) print the decision-oriented optimization summary")
	fmt.Fprintln(w, "  5) open dashboard /optimizations/{id} in browser")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Default collection window: %d seconds sampled every %d seconds\n", defaultCollectionDurationSeconds, defaultPrometheusStepSeconds)
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Environment:\n  %s=<base URL> (default: %s)\n", inferleanBaseURLEnv, defaultInferleanBaseURL)
	fmt.Fprintf(w, "  %s=<bearer token> (optional, used for authenticated backend routes)\n", inferleanAuthTokenEnv)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "All collect flags are accepted by run.")
}

func resolveCollectStepSeconds(args []string) int {
	step := defaultPrometheusStepSeconds
	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "" {
			continue
		}
		if arg == "--prometheus-step-seconds" {
			if index+1 >= len(args) {
				continue
			}
			value, err := strconv.Atoi(strings.TrimSpace(args[index+1]))
			if err == nil && value > 0 {
				step = value
			}
			index++
			continue
		}
		if strings.HasPrefix(arg, "--prometheus-step-seconds=") {
			raw := strings.TrimSpace(strings.TrimPrefix(arg, "--prometheus-step-seconds="))
			value, err := strconv.Atoi(raw)
			if err == nil && value > 0 {
				step = value
			}
		}
	}
	if step <= 0 {
		return defaultPrometheusStepSeconds
	}
	return step
}

func resolveInferleanBaseURL(raw string) (string, error) {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = defaultInferleanBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("invalid %s value %q", inferleanBaseURLEnv, base)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func triggerJob(baseURL string, collectorPayload []byte, authToken string) (*triggerJobAPIResponse, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/optimizations"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(collectorPayload))
	if err != nil {
		return nil, fmt.Errorf("build trigger request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trigger backend job: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read trigger response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var backendErr backendErrorPayload
		if unmarshalErr := json.Unmarshal(body, &backendErr); unmarshalErr == nil && strings.TrimSpace(backendErr.Error) != "" {
			return nil, fmt.Errorf("trigger backend job failed (%d): %s", resp.StatusCode, backendErr.Error)
		}
		return nil, fmt.Errorf("trigger backend job failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out triggerJobAPIResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode trigger response: %w", err)
	}
	resolvedID := strings.TrimSpace(out.ID)
	if resolvedID == "" {
		resolvedID = strings.TrimSpace(string(out.JobID))
	}
	if resolvedID == "" {
		resolvedID = strings.TrimSpace(out.JobUUID)
	}
	if resolvedID == "" {
		return nil, errors.New("trigger response did not include id/job_id/job_uuid")
	}
	out.ID = resolvedID
	return &out, nil
}

func waitForOptimizationCompletion(baseURL, jobID, authToken string, progressCallback func(waitProgressUpdate)) (*model.AnalysisReport, *model.OptimizationReportV2, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, nil, errors.New("job id is required")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, nil, errors.New("base url is required")
	}
	deadline := time.Now().Add(runPollTimeout)
	analysisURL := fmt.Sprintf("%s/api/v1/jobs/%s/analysis", baseURL, url.PathEscape(jobID))
	reportURL := fmt.Sprintf("%s/api/v1/optimizations/%s/report", baseURL, url.PathEscape(jobID))
	var analysis model.AnalysisReport
	if err := waitForArtifact(
		analysisURL,
		authToken,
		deadline,
		defaultAnalysisETA,
		&analysis,
		func(progress float64, elapsed, eta time.Duration, done bool) {
			if progressCallback == nil {
				return
			}
			progressCallback(waitProgressUpdate{
				Stage:    waitStageAnalysis,
				Progress: progress,
				Elapsed:  elapsed,
				ETA:      eta,
				Done:     done,
			})
		},
	); err != nil {
		if errors.Is(err, errArtifactWaitTimeout) {
			return nil, nil, fmt.Errorf("timed out waiting for optimization report for job %s", jobID)
		}
		return nil, nil, fmt.Errorf("fetch analysis: %w", err)
	}

	var report model.OptimizationReportV2
	if err := waitForArtifact(
		reportURL,
		authToken,
		deadline,
		defaultRecommendationETA,
		&report,
		func(progress float64, elapsed, eta time.Duration, done bool) {
			if progressCallback == nil {
				return
			}
			progressCallback(waitProgressUpdate{
				Stage:    waitStageRecommendation,
				Progress: progress,
				Elapsed:  elapsed,
				ETA:      eta,
				Done:     done,
			})
		},
	); err != nil {
		if errors.Is(err, errArtifactWaitTimeout) {
			return nil, nil, fmt.Errorf("timed out waiting for optimization report for job %s", jobID)
		}
		return nil, nil, fmt.Errorf("fetch optimization report: %w", err)
	}

	return &analysis, &report, nil
}

func waitForJobCompletion(baseURL, jobID, authToken string, progressCallback func(waitProgressUpdate)) (*model.AnalysisReport, *model.RecommendationReport, *topRecommendationAPIResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, nil, nil, errors.New("job id is required")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, nil, nil, errors.New("base url is required")
	}
	deadline := time.Now().Add(runPollTimeout)
	analysisURL := fmt.Sprintf("%s/api/v1/jobs/%s/analysis", baseURL, url.PathEscape(jobID))
	recommendationURL := fmt.Sprintf("%s/api/v1/jobs/%s/recommendation", baseURL, url.PathEscape(jobID))
	topRecommendationURL := fmt.Sprintf("%s/api/v1/jobs/%s/top-recommendation", baseURL, url.PathEscape(jobID))
	var analysis model.AnalysisReport
	if err := waitForArtifact(
		analysisURL,
		authToken,
		deadline,
		defaultAnalysisETA,
		&analysis,
		func(progress float64, elapsed, eta time.Duration, done bool) {
			if progressCallback == nil {
				return
			}
			progressCallback(waitProgressUpdate{
				Stage:    waitStageAnalysis,
				Progress: progress,
				Elapsed:  elapsed,
				ETA:      eta,
				Done:     done,
			})
		},
	); err != nil {
		if errors.Is(err, errArtifactWaitTimeout) {
			return nil, nil, nil, fmt.Errorf("timed out waiting for analysis/recommendation for job %s", jobID)
		}
		return nil, nil, nil, fmt.Errorf("fetch analysis: %w", err)
	}

	var recommendation topRecommendationAPIResponse
	if err := waitForArtifact(
		topRecommendationURL,
		authToken,
		deadline,
		defaultRecommendationETA,
		&recommendation,
		func(progress float64, elapsed, eta time.Duration, done bool) {
			if progressCallback == nil {
				return
			}
			progressCallback(waitProgressUpdate{
				Stage:    waitStageRecommendation,
				Progress: progress,
				Elapsed:  elapsed,
				ETA:      eta,
				Done:     done,
			})
		},
	); err != nil {
		if errors.Is(err, errArtifactWaitTimeout) {
			return nil, nil, nil, fmt.Errorf("timed out waiting for analysis/recommendation for job %s", jobID)
		}
		return nil, nil, nil, fmt.Errorf("fetch recommendation: %w", err)
	}

	fullRecommendation, _ := fetchOptionalRecommendationReport(recommendationURL, authToken)
	return &analysis, fullRecommendation, &recommendation, nil
}

func waitForArtifact(endpoint, authToken string, deadline time.Time, expectedDuration time.Duration, out any, onProgress func(progress float64, elapsed, eta time.Duration, done bool)) error {
	start := time.Now()
	if onProgress != nil {
		progress, eta := estimateStageProgress(0, expectedDuration)
		onProgress(progress, 0, eta, false)
	}
	for {
		pending, err := fetchPendingJSONArtifact(endpoint, authToken, out)
		if err != nil {
			return err
		}
		elapsed := time.Since(start)
		if !pending {
			if onProgress != nil {
				onProgress(1, elapsed, 0, true)
			}
			return nil
		}
		progress, eta := estimateStageProgress(elapsed, expectedDuration)
		if onProgress != nil {
			onProgress(progress, elapsed, eta, false)
		}
		if time.Now().After(deadline) {
			return errArtifactWaitTimeout
		}
		time.Sleep(runPollInterval)
	}
}

func estimateStageProgress(elapsed, expected time.Duration) (float64, time.Duration) {
	if expected <= 0 {
		expected = 30 * time.Second
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > expected {
		return 0.98, runPollInterval
	}
	progress := clampFloat(float64(elapsed)/float64(expected), 0, 0.98)
	eta := expected - elapsed
	if eta < runPollInterval {
		eta = runPollInterval
	}
	return progress, eta
}

func fetchPendingJSONArtifact(endpoint, authToken string, out any) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusAccepted {
		return true, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Errorf("backend returned %d; set %s for authenticated access", resp.StatusCode, inferleanAuthTokenEnv)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var backendErr backendErrorPayload
		if unmarshalErr := json.Unmarshal(body, &backendErr); unmarshalErr == nil && strings.TrimSpace(backendErr.Error) != "" {
			return false, fmt.Errorf("backend request failed (%d): %s", resp.StatusCode, backendErr.Error)
		}
		return false, fmt.Errorf("backend request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	return false, nil
}

func fetchOptionalRecommendationReport(endpoint, authToken string) (*model.RecommendationReport, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		return nil, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil
	}

	var report model.RecommendationReport
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&report); err != nil {
		return nil, err
	}
	return &report, nil
}

func topIssueSummary(report *model.AnalysisReport) string {
	primary, ok := primaryFinding(report)
	if !ok {
		return "No clear bottleneck detected from current evidence."
	}
	return findingHeadline(primary)
}

func topRecommendationSummary(report *model.RecommendationReport) string {
	if report != nil {
		for _, strategy := range report.StrategyOptions {
			if strategy.Recommended && strings.TrimSpace(strategy.Summary) != "" {
				return strings.TrimSpace(strategy.Summary)
			}
		}
	}
	if report == nil || len(report.Recommendations) == 0 {
		return "No recommendation was produced."
	}
	for _, item := range report.Recommendations {
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			return summary
		}
	}
	return "No recommendation was produced."
}

func alternativeStrategySummary(report *model.RecommendationReport) string {
	if report == nil {
		return ""
	}
	for _, strategy := range report.StrategyOptions {
		if !strategy.Recommended && strings.TrimSpace(strategy.Summary) != "" {
			return strings.TrimSpace(strategy.Summary)
		}
	}
	return ""
}

func currentLoadSummaryLines(analysis *model.AnalysisReport, capacity *model.CapacityOpportunity) []string {
	lines := []string{}
	if analysis != nil && analysis.CurrentLoadSummary != nil {
		lines = append(lines, fmt.Sprintf("Current saturation: %.1f%%", analysis.CurrentLoadSummary.CurrentSaturationPct))
		lines = append(lines, fmt.Sprintf(
			"Current GPU utilization (sample average): %.1f%% (%.1f / %.1f GPUs)",
			analysis.CurrentLoadSummary.CurrentGPULoadPct,
			analysis.CurrentLoadSummary.CurrentGPULoadEffectiveCount,
			analysis.CurrentLoadSummary.TotalGPUCount,
		))
		lines = append(lines, fmt.Sprintf("Load bottleneck: %s", humanizeLoadBottleneck(analysis.CurrentLoadSummary.CurrentLoadBottleneck)))
	}
	if capacity != nil {
		lines = append(lines, fmt.Sprintf(
			"Recoverable capacity: %.1f%% (%.1f GPUs)",
			capacity.RecoverableGPULoadPct,
			capacity.RecoverableGPUCount,
		))
	}
	return lines
}

func isTriggerNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func persistCollectorForManualUpload(payload []byte) (string, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return "", errors.New("collector payload is empty")
	}

	fileName := fmt.Sprintf("collector-report-manual-upload-%d.json", time.Now().UTC().UnixNano())

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, fileName)
		if writeErr := os.WriteFile(candidate, payload, 0o600); writeErr == nil {
			absPath, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return absPath, nil
			}
			return candidate, nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "inferlean-manual-upload-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir for manual upload fallback: %w", err)
	}
	fallbackPath := filepath.Join(tmpDir, fileName)
	if err := os.WriteFile(fallbackPath, payload, 0o600); err != nil {
		return "", fmt.Errorf("write collector fallback file: %w", err)
	}
	return fallbackPath, nil
}

func openBrowserURL(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
