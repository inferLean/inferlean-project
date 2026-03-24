package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z._+-]*`)

const defaultVLLMVersionProbeTimeoutSeconds = 150

func discoverVLLMVersion(ctx context.Context, explicitVersion, explicitBinary string, timeoutSeconds int) (string, error) {
	normalizedOverride := strings.TrimSpace(strings.TrimPrefix(explicitVersion, "v"))
	if normalizedOverride != "" && !strings.EqualFold(normalizedOverride, "unknown") {
		debugf("vLLM version discovery: using explicit override %s", normalizedOverride)
		return normalizedOverride, nil
	}
	timeoutSeconds = effectiveVLLMVersionProbeTimeout(timeoutSeconds)

	binaryPath, err := resolveVLLMBinary(ctx, explicitBinary)
	if err != nil {
		return "", err
	}
	debugf("vLLM version discovery: resolved binary %s (timeout=%ds)", binaryPath, timeoutSeconds)

	commands := [][]string{
		{binaryPath, "--version"},
		{binaryPath, "version"},
	}
	var combinedOutput strings.Builder
	for _, command := range commands {
		if len(command) == 0 {
			continue
		}
		debugf("vLLM version discovery: trying command %s", strings.Join(command, " "))
		output, runErr := runCommandCapture(ctx, timeoutSeconds, command[0], command[1:]...)
		if strings.TrimSpace(output) != "" {
			if combinedOutput.Len() > 0 {
				combinedOutput.WriteString("\n")
			}
			combinedOutput.WriteString(output)
		}
		if version := extractVLLMVersion(output); version != "" {
			debugf("vLLM version discovery: parsed version %s from command output", version)
			return version, nil
		}
		if runErr == nil {
			continue
		}
		debugf("vLLM version discovery: command failed without parseable version: %v", runErr)
	}

	if version := extractVLLMVersion(combinedOutput.String()); version != "" {
		debugf("vLLM version discovery: parsed version %s from combined CLI output", version)
		return version, nil
	}

	if version, output := discoverVLLMVersionViaPython(ctx, binaryPath, timeoutSeconds); version != "" {
		debugf("vLLM version discovery: parsed version %s from python fallback", version)
		return version, nil
	} else if strings.TrimSpace(output) != "" {
		combinedOutput.WriteString("\n")
		combinedOutput.WriteString(output)
	}

	return "", fmt.Errorf("failed to auto-discover vLLM version from %q; provide --vllm-bin <path> to the vLLM binary", binaryPath)
}

func resolveVLLMBinary(ctx context.Context, explicitBinary string) (string, error) {
	explicitBinary = strings.TrimSpace(explicitBinary)
	if explicitBinary != "" {
		debugf("vLLM binary discovery: using explicit path %q", explicitBinary)
		return resolveExplicitBinary(explicitBinary)
	}

	if path := firstExistingBinary([]string{"vllm"}); path != "" {
		debugf("vLLM binary discovery: found in PATH %s", path)
		return path, nil
	}

	debugf("vLLM binary discovery: checking running process")
	pid := detectVLLMPID(ctx)
	if pid > 0 {
		if discovered := discoverVLLMBinaryFromProcess(pid); discovered != "" {
			debugf("vLLM binary discovery: found from process pid=%d path=%s", pid, discovered)
			return discovered, nil
		}
	}
	return "", fmt.Errorf("vLLM binary was not found; provide --vllm-bin <path> to the vLLM executable")
}

func discoverVLLMBinaryFromProcess(pid int) string {
	if pid <= 0 {
		return ""
	}
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}
	raw := strings.Split(string(data), "\x00")
	args := make([]string, 0, len(raw))
	for _, token := range raw {
		token = strings.TrimSpace(token)
		if token != "" {
			args = append(args, token)
		}
	}

	for _, token := range args {
		candidate := strings.TrimSpace(token)
		if candidate == "" {
			continue
		}
		base := filepath.Base(candidate)
		if base != "vllm" {
			continue
		}
		if path := firstExistingBinary([]string{candidate}); path != "" {
			return path
		}
	}
	return ""
}

func extractVLLMVersion(text string) string {
	match := versionPattern.FindString(strings.TrimSpace(text))
	if match == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(match, "v"))
}

func discoverVLLMVersionViaPython(ctx context.Context, vllmBinaryPath string, timeoutSeconds int) (string, string) {
	pythonCandidates := buildPythonCandidates(vllmBinaryPath)
	debugf("vLLM version python fallback: candidates=%v", pythonCandidates)
	var combinedOutput strings.Builder
	for _, pythonBinary := range pythonCandidates {
		debugf("vLLM version python fallback: trying %s -c import vllm", pythonBinary)
		out, err := runCommandCapture(ctx, timeoutSeconds, pythonBinary, "-c", "import vllm; print(getattr(vllm, '__version__', ''))")
		if strings.TrimSpace(out) != "" {
			if combinedOutput.Len() > 0 {
				combinedOutput.WriteString("\n")
			}
			combinedOutput.WriteString(out)
		}
		if version := extractVLLMVersion(out); version != "" {
			return version, combinedOutput.String()
		}
		if err == nil {
			continue
		}

		debugf("vLLM version python fallback: trying %s -m pip show vllm", pythonBinary)
		pipOut, pipErr := runCommandCapture(ctx, timeoutSeconds, pythonBinary, "-m", "pip", "show", "vllm")
		if strings.TrimSpace(pipOut) != "" {
			if combinedOutput.Len() > 0 {
				combinedOutput.WriteString("\n")
			}
			combinedOutput.WriteString(pipOut)
		}
		if version := extractVLLMVersion(pipOut); version != "" {
			return version, combinedOutput.String()
		}
		if pipErr != nil {
			continue
		}
	}
	return "", combinedOutput.String()
}

func effectiveVLLMVersionProbeTimeout(seconds int) int {
	if seconds <= 0 {
		return defaultVLLMVersionProbeTimeoutSeconds
	}
	return seconds
}

func buildPythonCandidates(vllmBinaryPath string) []string {
	candidates := []string{}
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		if path := firstExistingBinary([]string{candidate}); path != "" {
			candidates = append(candidates, path)
		}
	}

	if strings.TrimSpace(vllmBinaryPath) != "" {
		dir := filepath.Dir(vllmBinaryPath)
		add(filepath.Join(dir, "python"))
		add(filepath.Join(dir, "python3"))
	}
	add("python3")
	add("python")

	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
