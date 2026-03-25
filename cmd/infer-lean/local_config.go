package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	yaml "gopkg.in/yaml.v2"
)

const (
	inferleanConfigDirName  = ".inferlean"
	inferleanConfigFileName = "config"
)

type inferleanPreference struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

type inferleanLocalConfig struct {
	InstallationID string                `yaml:"installation_id"`
	Preferences    []inferleanPreference `yaml:"preferences"`
}

var newInstallationID = func() string {
	return uuid.NewString()
}

func ensureInferleanLocalConfig() error {
	configPath, err := inferleanLocalConfigPath()
	if err != nil {
		return err
	}
	return ensureInferleanLocalConfigAtPath(configPath)
}

func ensureInferleanLocalConfigAtPath(configPath string) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return errors.New("local config path is required")
	}

	cfg := inferleanLocalConfig{}
	needsWrite := false

	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("parse local config %s: %w", configPath, err)
			}
		} else {
			needsWrite = true
		}
	case errors.Is(err, os.ErrNotExist):
		needsWrite = true
	default:
		return fmt.Errorf("read local config %s: %w", configPath, err)
	}

	if strings.TrimSpace(cfg.InstallationID) == "" {
		cfg.InstallationID = newInstallationID()
		needsWrite = true
	}
	if cfg.Preferences == nil {
		cfg.Preferences = []inferleanPreference{}
		needsWrite = true
	}

	if !needsWrite {
		return nil
	}

	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create local config dir %s: %w", configDir, err)
	}
	payload, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	if err := os.WriteFile(configPath, payload, 0o600); err != nil {
		return fmt.Errorf("write local config %s: %w", configPath, err)
	}
	return nil
}

func loadInferleanLocalConfig() (*inferleanLocalConfig, error) {
	configPath, err := inferleanLocalConfigPath()
	if err != nil {
		return nil, err
	}
	if err := ensureInferleanLocalConfigAtPath(configPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read local config %s: %w", configPath, err)
	}
	var cfg inferleanLocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse local config %s: %w", configPath, err)
	}
	return &cfg, nil
}

func inferleanLocalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return filepath.Join(home, inferleanConfigDirName, inferleanConfigFileName), nil
}
