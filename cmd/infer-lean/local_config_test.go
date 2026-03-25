package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	yaml "gopkg.in/yaml.v2"
)

func TestExecuteCreatesLocalConfigDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"help"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	configPath := filepath.Join(home, inferleanConfigDirName, inferleanConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(data), "installation_id:") {
		t.Fatalf("expected installation_id in config, got %q", string(data))
	}
	if !strings.Contains(string(data), "preferences: []") {
		t.Fatalf("expected preferences empty array in config, got %q", string(data))
	}

	var cfg inferleanLocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal local config: %v", err)
	}
	if cfg.InstallationID == "" {
		t.Fatalf("expected generated installation_id")
	}
	if _, err := uuid.Parse(cfg.InstallationID); err != nil {
		t.Fatalf("expected installation_id to be uuid, got %q (%v)", cfg.InstallationID, err)
	}
	if len(cfg.Preferences) != 0 {
		t.Fatalf("expected empty preferences, got %+v", cfg.Preferences)
	}
}

func TestExecuteBackfillsMissingInstallationID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configDir := filepath.Join(home, inferleanConfigDirName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, inferleanConfigFileName)
	mustWriteFile(t, configPath, `preferences:
  - key: output_format
    value: compact
`)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"help"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}

	var cfg inferleanLocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal local config: %v", err)
	}
	if cfg.InstallationID == "" {
		t.Fatalf("expected generated installation_id")
	}
	if _, err := uuid.Parse(cfg.InstallationID); err != nil {
		t.Fatalf("expected installation_id to be uuid, got %q (%v)", cfg.InstallationID, err)
	}
	if len(cfg.Preferences) != 1 {
		t.Fatalf("expected one preference entry, got %+v", cfg.Preferences)
	}
	if cfg.Preferences[0].Key != "output_format" || cfg.Preferences[0].Value != "compact" {
		t.Fatalf("expected preference key/value to be preserved, got %+v", cfg.Preferences[0])
	}
}

func TestExecuteBackfillsMissingPreferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existingID := uuid.NewString()
	configDir := filepath.Join(home, inferleanConfigDirName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, inferleanConfigFileName)
	mustWriteFile(t, configPath, "installation_id: "+existingID+"\n")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Execute([]string{"help"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%s", exitCode, stderr.String())
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}

	var cfg inferleanLocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal local config: %v", err)
	}
	if cfg.InstallationID != existingID {
		t.Fatalf("expected installation_id to be preserved, got %q want %q", cfg.InstallationID, existingID)
	}
	if len(cfg.Preferences) != 0 {
		t.Fatalf("expected empty preferences, got %+v", cfg.Preferences)
	}
}
