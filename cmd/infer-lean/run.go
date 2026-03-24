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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	defaultInferleanBaseURL = "https://app.inferlean.com"
	inferleanBaseURLEnv     = "INFERLEAN_BASE_URL"
	inferleanAuthTokenEnv   = "INFERLEAN_AUTH_TOKEN"
)

var (
	openDashboardInBrowser = openBrowserURL
	runPollInterval        = 3 * time.Second
	runPollTimeout         = 15 * time.Minute
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
			printRunUsage(stderr)
			return errHelpRequested
		}
	}

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

	if ui.Enabled() {
		ui.Step("Collecting runtime metrics...")
	}
	if err := runCollect(collectArgs, io.Discard, stderr); err != nil {
		return fmt.Errorf("collect failed: %w", err)
	}

	payload, err := os.ReadFile(collectorPath)
	if err != nil {
		return fmt.Errorf("read collector output: %w", err)
	}
	if ui.Enabled() {
		ui.Step("Triggering backend job...")
	}
	triggerResponse, err := triggerJob(baseURL, payload, authToken)
	if err != nil {
		if isTriggerNetworkError(err) {
			manualUploadPath, persistErr := persistCollectorForManualUpload(payload)
			if persistErr != nil {
				return fmt.Errorf("%w (also failed to save collector JSON for manual upload: %v)", err, persistErr)
			}
			triggerPage := fmt.Sprintf("%s/trigger", strings.TrimRight(baseURL, "/"))
			fmt.Fprintln(stderr, "warning: automatic backend trigger failed due to a network issue.")
			fmt.Fprintf(stderr, "collector JSON saved for manual upload: %s\n", manualUploadPath)
			fmt.Fprintf(stderr, "open %s and upload the saved file manually.\n", triggerPage)
			return nil
		}
		return err
	}

	dashboardURL := fmt.Sprintf("%s/jobs/%s", baseURL, url.PathEscape(triggerResponse.ID))
	fmt.Fprintf(stdout, "Job queued: %s\n", triggerResponse.ID)
	if ui.Enabled() {
		ui.Step("Waiting for analysis and recommendation...")
	}
	analysisReport, topRecommendation, err := waitForJobCompletion(baseURL, triggerResponse.ID, authToken)
	if err != nil {
		return fmt.Errorf("wait for job completion: %w", err)
	}

	topIssue := topIssueSummary(analysisReport)
	if summary := strings.TrimSpace(topRecommendation.TopIssue); summary != "" {
		topIssue = summary
	}
	topRecommendationSummaryLine := topRecommendationSummary(nil)
	if summary := strings.TrimSpace(topRecommendation.TopRecommendation); summary != "" {
		topRecommendationSummaryLine = summary
	}
	fmt.Fprintf(stdout, "Top issue: %s\n", topIssue)
	fmt.Fprintf(stdout, "Top recommendation: %s\n", topRecommendationSummaryLine)
	for _, line := range currentLoadSummaryLines(analysisReport, topRecommendation.CapacityOpportunity) {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "For further details, see dashboard: %s\n", dashboardURL)

	if err := openDashboardInBrowser(dashboardURL); err != nil {
		fmt.Fprintf(stderr, "warning: unable to open browser automatically: %v\n", err)
		return nil
	}
	if ui.Enabled() {
		ui.Step("Dashboard opened in browser")
	}
	return nil
}

func printRunUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: inferLean run [collect flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "run performs:")
	fmt.Fprintln(w, "  1) collect locally")
	fmt.Fprintln(w, "  2) POST /api/v1/trigger-job to backend")
	fmt.Fprintln(w, "  3) wait for analysis + recommendation completion")
	fmt.Fprintln(w, "  4) print top issue + top recommendation")
	fmt.Fprintln(w, "  5) open dashboard /jobs/{job_id} in browser")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Environment:\n  %s=<base URL> (default: %s)\n", inferleanBaseURLEnv, defaultInferleanBaseURL)
	fmt.Fprintf(w, "  %s=<bearer token> (optional, used for authenticated backend routes)\n", inferleanAuthTokenEnv)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "All collect flags are accepted by run.")
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
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/trigger-job"
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

func waitForJobCompletion(baseURL, jobID, authToken string) (*model.AnalysisReport, *topRecommendationAPIResponse, error) {
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
	topRecommendationURL := fmt.Sprintf("%s/api/v1/jobs/%s/top-recommendation", baseURL, url.PathEscape(jobID))

	var (
		analysis          *model.AnalysisReport
		topRecommendation *topRecommendationAPIResponse
	)
	for {
		if analysis == nil {
			var report model.AnalysisReport
			pending, err := fetchPendingJSONArtifact(analysisURL, authToken, &report)
			if err != nil {
				return nil, nil, fmt.Errorf("fetch analysis: %w", err)
			}
			if !pending {
				analysis = &report
			}
		}

		if topRecommendation == nil {
			var report topRecommendationAPIResponse
			pending, err := fetchPendingJSONArtifact(topRecommendationURL, authToken, &report)
			if err != nil {
				return nil, nil, fmt.Errorf("fetch recommendation: %w", err)
			}
			if !pending {
				topRecommendation = &report
			}
		}

		if analysis != nil && topRecommendation != nil {
			return analysis, topRecommendation, nil
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("timed out waiting for analysis/recommendation for job %s", jobID)
		}
		time.Sleep(runPollInterval)
	}
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

func topIssueSummary(report *model.AnalysisReport) string {
	primary, ok := primaryFinding(report)
	if !ok {
		return "No clear bottleneck detected from current evidence."
	}
	return findingHeadline(primary)
}

func topRecommendationSummary(report *model.RecommendationReport) string {
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
