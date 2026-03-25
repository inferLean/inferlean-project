package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	inferleanCrashlyticsFileName         = "crashlytics"
	inferleanCrashlyticsUploadPath       = "/api/v1/cli-crashlytics"
	maxCrashlyticsFileBytes        int64 = 10 << 20
	inferleanCrashlyticsDisableEnv       = "INFERLEAN_DISABLE_CRASHLYTICS_UPLOAD"
)

var crashlyticsFileMu sync.Mutex

type crashlyticsEntry struct {
	Timestamp      string            `json:"ts"`
	Kind           string            `json:"kind"`
	Event          string            `json:"event,omitempty"`
	Command        string            `json:"command,omitempty"`
	Status         string            `json:"status,omitempty"`
	PanicText      string            `json:"panic,omitempty"`
	StackTrace     string            `json:"stack_trace,omitempty"`
	InstallationID string            `json:"installation_id,omitempty"`
	Meta           map[string]string `json:"meta,omitempty"`
}

type crashlyticsUploadRequest struct {
	InstallationID string `json:"installation_id"`
	Payload        string `json:"payload"`
	Source         string `json:"source"`
	ToolVersion    string `json:"tool_version"`
}

func recordCLIEvent(event string, meta map[string]string) {
	event = strings.TrimSpace(event)
	if event == "" {
		return
	}
	trimmedMeta := map[string]string{}
	for key, value := range meta {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		trimmedMeta[k] = v
	}
	entry := crashlyticsEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Kind:      "event",
		Event:     event,
		Meta:      trimmedMeta,
	}
	_ = appendCrashlyticsEntry(entry)
}

func recordRecoveredPanic(panicValue any, stack []byte) {
	if err := ensureInferleanLocalConfig(); err != nil {
		return
	}
	cfg, err := loadInferleanLocalConfig()
	if err != nil {
		return
	}
	entry := crashlyticsEntry{
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		Kind:           "crash",
		Event:          "panic_recovered",
		PanicText:      strings.TrimSpace(fmt.Sprint(panicValue)),
		StackTrace:     strings.TrimSpace(string(stack)),
		InstallationID: strings.TrimSpace(cfg.InstallationID),
	}
	_ = appendCrashlyticsEntry(entry)
	tryPushCrashlyticsFile()
}

func tryPushCrashlyticsFile() {
	if crashlyticsUploadDisabled() {
		return
	}
	cfg, err := loadInferleanLocalConfig()
	if err != nil {
		return
	}
	installationID := strings.TrimSpace(cfg.InstallationID)
	if installationID == "" {
		return
	}
	crashlyticsPath, err := inferleanCrashlyticsPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(crashlyticsPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return
	}
	baseURL, err := resolveCrashlyticsUploadBaseURL(os.Getenv(inferleanBaseURLEnv))
	if err != nil {
		return
	}
	if err := uploadCrashlytics(baseURL, installationID, data); err != nil {
		return
	}
	_ = os.WriteFile(crashlyticsPath, []byte{}, 0o600)
}

func appendCrashlyticsEntry(entry crashlyticsEntry) error {
	crashlyticsPath, err := inferleanCrashlyticsPath()
	if err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal crashlytics entry: %w", err)
	}
	crashlyticsFileMu.Lock()
	defer crashlyticsFileMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(crashlyticsPath), 0o700); err != nil {
		return fmt.Errorf("create crashlytics dir: %w", err)
	}

	data, err := os.ReadFile(crashlyticsPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		data = nil
	case err != nil:
		return fmt.Errorf("read crashlytics file: %w", err)
	}

	if int64(len(data)) > maxCrashlyticsFileBytes || int64(len(data)+len(line)+1) > maxCrashlyticsFileBytes {
		data = dropFirstLine(data)
	}
	data = append(data, line...)
	data = append(data, '\n')
	if err := os.WriteFile(crashlyticsPath, data, 0o600); err != nil {
		return fmt.Errorf("write crashlytics file: %w", err)
	}
	return nil
}

func dropFirstLine(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	index := bytes.IndexByte(data, '\n')
	if index < 0 {
		return []byte{}
	}
	trimmed := make([]byte, len(data[index+1:]))
	copy(trimmed, data[index+1:])
	return trimmed
}

func inferleanCrashlyticsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return filepath.Join(home, inferleanConfigDirName, inferleanCrashlyticsFileName), nil
}

func resolveCrashlyticsUploadBaseURL(raw string) (string, error) {
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

func uploadCrashlytics(baseURL, installationID string, payload []byte) error {
	requestPayload := crashlyticsUploadRequest{
		InstallationID: strings.TrimSpace(installationID),
		Payload:        string(payload),
		Source:         "inferlean-cli",
		ToolVersion:    model.ToolVersion,
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		return fmt.Errorf("marshal crashlytics upload payload: %w", err)
	}
	endpoint := strings.TrimRight(baseURL, "/") + inferleanCrashlyticsUploadPath
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build crashlytics upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	authToken := strings.TrimSpace(os.Getenv(inferleanAuthTokenEnv))
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload crashlytics: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("upload crashlytics failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func crashlyticsUploadDisabled() bool {
	raw := strings.TrimSpace(os.Getenv(inferleanCrashlyticsDisableEnv))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}
