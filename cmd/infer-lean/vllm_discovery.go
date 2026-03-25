package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type vllmDiscoveryResult struct {
	ConfigPath    string
	MetricsTarget string
	PID           int
}

func ensureVLLMDiscovery(ctx context.Context, configPath string) (vllmDiscoveryResult, error) {
	result := vllmDiscoveryResult{}
	clean := strings.TrimSpace(configPath)
	if clean != "" {
		debugf("vLLM discovery: using explicit config path %q", clean)
		result.ConfigPath = clean
	}

	debugf("vLLM discovery: detecting running vLLM process")
	pid := detectVLLMPID(ctx)
	if pid <= 0 {
		if strings.TrimSpace(result.ConfigPath) != "" {
			debugf("vLLM discovery: no running vLLM process found; continuing with explicit config path")
			return result, nil
		}
		return result, fmt.Errorf("vLLM was not found on this host; provide the vLLM configuration path using --config-file <path>")
	}
	result.PID = pid
	debugf("vLLM discovery: found pid %d", pid)

	if discovered := discoverVLLMMetricsTargetFromVLLMProcess(pid); discovered != "" {
		result.MetricsTarget = discovered
		debugf("vLLM discovery: metrics target discovered from process args: %s", discovered)
	} else {
		debugf("vLLM discovery: metrics target not found in process args; using default metrics target")
	}

	if strings.TrimSpace(result.ConfigPath) != "" {
		return result, nil
	}
	if discovered := discoverConfigPathFromVLLMProcess(pid); discovered != "" {
		debugf("vLLM discovery: config discovered from process args: %s", discovered)
		result.ConfigPath = discovered
		return result, nil
	}
	if discovered := discoverConfigPathFromCommonLocations(); discovered != "" {
		debugf("vLLM discovery: config discovered from common locations: %s", discovered)
		result.ConfigPath = discovered
		return result, nil
	}
	debugf("vLLM discovery: no config path discovered; continuing with empty config path")
	return result, nil
}

func discoverConfigPathFromVLLMProcess(pid int) string {
	args, err := readProcessArgs(pid)
	if err != nil {
		debugf("vLLM discovery: failed reading process args for pid %d: %v", pid, err)
		return ""
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

func discoverVLLMMetricsTargetFromVLLMProcess(pid int) string {
	args, err := readProcessArgs(pid)
	if err != nil {
		debugf("vLLM discovery: failed reading process args for pid %d: %v", pid, err)
		return ""
	}
	return parseVLLMMetricsTargetFromArgs(args)
}

func parseVLLMMetricsTargetFromArgs(args []string) string {
	host := ""
	port := ""
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch {
		case arg == "--host":
			if i+1 < len(args) {
				host = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--host="):
			host = strings.TrimSpace(strings.TrimPrefix(arg, "--host="))
		case arg == "--port":
			if i+1 < len(args) {
				port = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--port="):
			port = strings.TrimSpace(strings.TrimPrefix(arg, "--port="))
		}
	}
	if strings.TrimSpace(port) == "" {
		return ""
	}
	portNumber, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return ""
	}
	host = normalizeVLLMMetricsHost(host)
	return net.JoinHostPort(host, strconv.Itoa(portNumber))
}

func normalizeVLLMMetricsHost(host string) string {
	host = strings.TrimSpace(strings.Trim(host, `"'`))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
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
