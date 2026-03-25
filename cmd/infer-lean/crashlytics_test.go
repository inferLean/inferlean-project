package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAppendCrashlyticsEntryDropsTopLineWhenFileExceedsLimit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	crashPath, err := inferleanCrashlyticsPath()
	if err != nil {
		t.Fatalf("resolve crashlytics path: %v", err)
	}
	if err := os.MkdirAll(filepathDir(crashPath), 0o700); err != nil {
		t.Fatalf("mkdir crashlytics dir: %v", err)
	}

	var builder strings.Builder
	builder.WriteString("drop-me\n")
	for builder.Len() <= int(maxCrashlyticsFileBytes)+32 {
		builder.WriteString(strings.Repeat("x", 1024))
		builder.WriteString("\n")
	}
	if err := os.WriteFile(crashPath, []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write crashlytics file: %v", err)
	}

	if err := appendCrashlyticsEntry(crashlyticsEntry{
		Timestamp: "2026-03-24T12:00:00Z",
		Kind:      "event",
		Event:     "collect.start",
	}); err != nil {
		t.Fatalf("append crashlytics entry: %v", err)
	}

	updated, err := os.ReadFile(crashPath)
	if err != nil {
		t.Fatalf("read crashlytics file: %v", err)
	}
	if strings.HasPrefix(string(updated), "drop-me\n") {
		t.Fatalf("expected top line to be dropped when file exceeds limit")
	}
}

func TestTryPushCrashlyticsFilePurgesOnSuccessfulUpload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(inferleanCrashlyticsDisableEnv, "false")

	previousNewInstallationID := newInstallationID
	newInstallationID = func() string { return "00000000-0000-0000-0000-000000000321" }
	defer func() { newInstallationID = previousNewInstallationID }()

	if err := ensureInferleanLocalConfig(); err != nil {
		t.Fatalf("ensure local config: %v", err)
	}
	crashPath, err := inferleanCrashlyticsPath()
	if err != nil {
		t.Fatalf("resolve crashlytics path: %v", err)
	}
	if err := os.WriteFile(crashPath, []byte("legacy-event\n"), 0o600); err != nil {
		t.Fatalf("write crashlytics: %v", err)
	}

	var received crashlyticsUploadRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != inferleanCrashlyticsUploadPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	t.Setenv(inferleanBaseURLEnv, server.URL)
	tryPushCrashlyticsFile()

	if received.InstallationID != "00000000-0000-0000-0000-000000000321" {
		t.Fatalf("unexpected installation id: %q", received.InstallationID)
	}
	if !strings.Contains(received.Payload, "legacy-event") {
		t.Fatalf("expected uploaded payload to include legacy content, got %q", received.Payload)
	}
	updated, err := os.ReadFile(crashPath)
	if err != nil {
		t.Fatalf("read crashlytics after push: %v", err)
	}
	if strings.TrimSpace(string(updated)) != "" {
		t.Fatalf("expected crashlytics file to be purged, got %q", string(updated))
	}
}

func TestExecutePushesCrashlyticsAtStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(inferleanCrashlyticsDisableEnv, "false")

	previousNewInstallationID := newInstallationID
	newInstallationID = func() string { return "00000000-0000-0000-0000-000000000654" }
	defer func() { newInstallationID = previousNewInstallationID }()

	if err := ensureInferleanLocalConfig(); err != nil {
		t.Fatalf("ensure local config: %v", err)
	}
	crashPath, err := inferleanCrashlyticsPath()
	if err != nil {
		t.Fatalf("resolve crashlytics path: %v", err)
	}
	if err := os.WriteFile(crashPath, []byte("stale-session-line\n"), 0o600); err != nil {
		t.Fatalf("write crashlytics: %v", err)
	}

	var uploadedPayload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != inferleanCrashlyticsUploadPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload crashlyticsUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		uploadedPayload = payload.Payload
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()
	t.Setenv(inferleanBaseURLEnv, server.URL)

	exitCode := Execute([]string{"help"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("expected help exit code 0, got %d", exitCode)
	}
	if !strings.Contains(uploadedPayload, "stale-session-line") {
		t.Fatalf("expected startup upload to include stale line, got %q", uploadedPayload)
	}

	updated, err := os.ReadFile(crashPath)
	if err != nil {
		t.Fatalf("read crashlytics file: %v", err)
	}
	if strings.Contains(string(updated), "stale-session-line") {
		t.Fatalf("expected stale line to be purged after successful upload, got %q", string(updated))
	}
	if strings.TrimSpace(string(updated)) == "" {
		t.Fatalf("expected current-run events to be written after startup upload purge")
	}
}

func filepathDir(path string) string {
	index := strings.LastIndex(path, string(os.PathSeparator))
	if index < 0 {
		return "."
	}
	if index == 0 {
		return string(os.PathSeparator)
	}
	return path[:index]
}
