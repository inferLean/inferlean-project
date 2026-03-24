package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func ensureVLLMDiscovery(ctx context.Context, configPath string) (string, error) {
	clean := strings.TrimSpace(configPath)
	if clean != "" {
		debugf("vLLM discovery: using explicit config path %q", clean)
		return clean, nil
	}

	debugf("vLLM discovery: detecting running vLLM process")
	pid := detectVLLMPID(ctx)
	if pid <= 0 {
		return "", fmt.Errorf("vLLM was not found on this host; provide the vLLM configuration path using --config-file <path>")
	}
	debugf("vLLM discovery: found pid %d", pid)

	if discovered := discoverConfigPathFromVLLMProcess(pid); discovered != "" {
		debugf("vLLM discovery: config discovered from process args: %s", discovered)
		return discovered, nil
	}
	if discovered := discoverConfigPathFromCommonLocations(); discovered != "" {
		debugf("vLLM discovery: config discovered from common locations: %s", discovered)
		return discovered, nil
	}
	debugf("vLLM discovery: no config path discovered; continuing with empty config path")
	return "", nil
}

func discoverConfigPathFromVLLMProcess(pid int) string {
	if pid <= 0 {
		return ""
	}
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		debugf("vLLM discovery: failed reading %s: %v", cmdlinePath, err)
		return ""
	}
	raw := strings.Split(string(data), "\x00")
	args := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			args = append(args, item)
		}
	}
	return parseConfigPathFromArgs(args)
}

func parseConfigPathFromArgs(args []string) string {
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if arg == "--config" || arg == "--config-file" {
			if i+1 < len(args) {
				return toAbsIfPresent(args[i+1])
			}
			continue
		}
		if strings.HasPrefix(arg, "--config=") {
			return toAbsIfPresent(strings.TrimPrefix(arg, "--config="))
		}
		if strings.HasPrefix(arg, "--config-file=") {
			return toAbsIfPresent(strings.TrimPrefix(arg, "--config-file="))
		}
	}
	return ""
}

func discoverConfigPathFromCommonLocations() string {
	debugf("vLLM discovery: checking common config locations")
	candidates := []string{
		"./vllm-config.json",
		"./vllm-config.yaml",
		"./vllm-config.yml",
		"./config.json",
		"./config.yaml",
		"./config.yml",
		"/workspace/vllm/vllm-config.json",
		"/workspace/vllm/vllm-config.yaml",
		"/workspace/vllm/vllm-config.yml",
		"/workspace/vllm/config.json",
		"/workspace/vllm/config.yaml",
		"/workspace/vllm/config.yml",
		"/workspace/inferLean/vllm-config.json",
		"/workspace/inferLean/config.json",
		"/etc/vllm/config.json",
		"/etc/vllm/config.yaml",
		"/etc/vllm/config.yml",
	}
	for _, candidate := range candidates {
		candidate = toAbsIfPresent(candidate)
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		debugf("vLLM discovery: found config at %s", candidate)
		return candidate
	}
	return ""
}
