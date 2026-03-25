package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	model "github.com/inferLean/inferlean-project/cli/contracts"
)

type AdvancedProfilingOptions struct {
	CollectBCC      bool
	CollectPySpy    bool
	CollectNSYS     bool
	DurationSeconds int
	VLLMPID         int
	WorkDir         string
	BCCBinary       string
	PySpyBinary     string
	NSYSBinary      string
}

func collectAdvancedProfiling(ctx context.Context, opts AdvancedProfilingOptions) model.AdvancedProfilingInfo {
	info := model.AdvancedProfilingInfo{
		DurationSeconds: effectiveProfilingDuration(opts.DurationSeconds),
		BCC:             model.ProfilingToolResult{Enabled: false, Status: "disabled"},
		PySpy:           model.ProfilingToolResult{Enabled: false, Status: "disabled"},
		NSys:            model.ProfilingToolResult{Enabled: false, Status: "disabled"},
	}

	if !opts.CollectBCC && !opts.CollectPySpy && !opts.CollectNSYS {
		return info
	}

	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		errResult := model.ProfilingToolResult{Enabled: true, Available: false, Status: "failed", Error: fmt.Sprintf("create profiling workdir: %v", err)}
		if opts.CollectBCC {
			info.BCC = errResult
		}
		if opts.CollectPySpy {
			info.PySpy = errResult
		}
		if opts.CollectNSYS {
			info.NSys = errResult
		}
		return info
	}

	pid := opts.VLLMPID
	if pid <= 0 {
		pid = detectVLLMPID(ctx)
	}
	info.TargetPID = pid
	if pid <= 0 {
		errResult := model.ProfilingToolResult{Enabled: true, Available: false, Status: "failed", Error: "vLLM process PID not found; use --vllm-pid"}
		if opts.CollectBCC {
			info.BCC = errResult
		}
		if opts.CollectPySpy {
			info.PySpy = errResult
		}
		if opts.CollectNSYS {
			info.NSys = errResult
		}
		return info
	}

	if opts.CollectBCC {
		info.BCC = runBCCProfile(ctx, strings.TrimSpace(opts.BCCBinary), pid, info.DurationSeconds)
	}
	if opts.CollectPySpy {
		info.PySpy = runPySpyDump(ctx, strings.TrimSpace(opts.PySpyBinary), pid)
	}
	if opts.CollectNSYS {
		info.NSys = runNSYSProfile(ctx, workDir, strings.TrimSpace(opts.NSYSBinary), pid, info.DurationSeconds)
	}

	return info
}

func effectiveProfilingDuration(seconds int) int {
	if seconds <= 0 {
		return 600
	}
	return seconds
}

func detectVLLMPID(ctx context.Context) int {
	patterns := []string{
		"vllm.entrypoints.openai.api_server",
		"python.*vllm",
		"(^|/)vllm($| )",
	}
	candidates := map[int][]string{}
	for _, pattern := range patterns {
		for _, pid := range detectPIDsByPattern(ctx, pattern) {
			if pid <= 0 {
				continue
			}
			if _, exists := candidates[pid]; exists {
				continue
			}
			args, err := readProcessArgs(pid)
			if err != nil {
				debugf("pid detection: failed reading args for pid %d: %v", pid, err)
				continue
			}
			candidates[pid] = args
		}
	}
	return pickBestVLLMPID(candidates)
}

func detectPIDByPattern(ctx context.Context, pattern string) int {
	pids := detectPIDsByPattern(ctx, pattern)
	if len(pids) == 0 {
		return 0
	}
	return pids[0]
}

func detectPIDsByPattern(ctx context.Context, pattern string) []int {
	debugf("pid detection: searching pattern %q", pattern)
	out, err := runCommandCapture(ctx, 10, "pgrep", "-f", pattern)
	if err != nil {
		debugf("pid detection: pattern %q not found (%v)", pattern, err)
		return nil
	}
	var pids []int
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			debugf("pid detection: pattern %q matched pid %d", pattern, pid)
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		debugf("pid detection: pattern %q returned no parseable pid", pattern)
	}
	return pids
}

func pickBestVLLMPID(candidates map[int][]string) int {
	bestPID := 0
	bestScore := 0
	for pid, args := range candidates {
		score := vllmProcessScore(args)
		debugf("pid detection: pid %d scored %d for args=%q", pid, score, strings.Join(args, " "))
		if score > bestScore || (score == bestScore && score > 0 && pid > bestPID) {
			bestPID = pid
			bestScore = score
		}
	}
	if bestPID > 0 {
		debugf("pid detection: selected pid %d with score %d", bestPID, bestScore)
	}
	return bestPID
}

func vllmProcessScore(args []string) int {
	if len(args) == 0 {
		return 0
	}
	score := 0
	if isVLLMBenchServeProcess(args) {
		score -= 100
	}
	if isVLLMServeProcess(args) {
		score += 120
	}
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "vllm.entrypoints.openai.api_server") {
			score += 150
		}
		if filepath.Base(trimmed) == "vllm" {
			score += 20
		}
		if strings.Contains(trimmed, "vllm") {
			score += 5
		}
	}
	return score
}

func isVLLMBenchServeProcess(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if strings.TrimSpace(args[i]) == "bench" && strings.TrimSpace(args[i+1]) == "serve" {
			return true
		}
	}
	return false
}

func isVLLMServeProcess(args []string) bool {
	for i, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if strings.Contains(trimmed, "vllm.entrypoints.openai.api_server") {
			return true
		}
		if trimmed != "serve" {
			continue
		}
		if i > 0 && strings.TrimSpace(args[i-1]) == "bench" {
			continue
		}
		return true
	}
	return false
}

func runBCCProfile(ctx context.Context, binary string, pid, durationSeconds int) model.ProfilingToolResult {
	result := model.ProfilingToolResult{Enabled: true, Status: "failed"}
	spec := toolInstallSpec{
		Name:           "bcc profile",
		LookupNames:    []string{"profile", "profile-bpfcc", "/usr/share/bcc/tools/profile"},
		APTPackages:    [][]string{{"bpfcc-tools"}, {"bcc-tools"}},
		DNFPackages:    [][]string{{"bcc-tools"}},
		YUMPackages:    [][]string{{"bcc-tools"}},
		PacmanPackages: [][]string{{"bcc"}},
	}
	resolvedBinary, err := resolveOrInstallTool(ctx, binary, spec)
	if err != nil {
		result.Available = false
		result.Error = err.Error()
		return result
	}

	result.Available = true
	result.Binary = resolvedBinary
	args := []string{"-p", strconv.Itoa(pid), strconv.Itoa(durationSeconds)}
	result.Command = strings.Join(append([]string{resolvedBinary}, args...), " ")
	output, runErr := runPrivilegedCommandCapture(ctx, durationSeconds+90, resolvedBinary, args...)
	result.Output = output
	if runErr != nil {
		result.Error = runErr.Error()
		return result
	}
	result.Status = "collected"
	return result
}

func runPySpyDump(ctx context.Context, binary string, pid int) model.ProfilingToolResult {
	result := model.ProfilingToolResult{Enabled: true, Status: "failed"}
	spec := toolInstallSpec{
		Name:           "py-spy",
		LookupNames:    []string{"py-spy"},
		APTPackages:    [][]string{{"py-spy"}},
		DNFPackages:    [][]string{{"py-spy"}},
		YUMPackages:    [][]string{{"py-spy"}},
		PacmanPackages: [][]string{{"python-py-spy"}, {"py-spy"}},
		FallbackCommands: [][]string{
			{"python3", "-m", "pip", "install", "--user", "py-spy"},
			{"pip3", "install", "--user", "py-spy"},
		},
	}
	resolvedBinary, err := resolveOrInstallTool(ctx, binary, spec)
	if err != nil {
		result.Available = false
		result.Error = err.Error()
		return result
	}

	result.Available = true
	result.Binary = resolvedBinary
	args := []string{"dump", "--pid", strconv.Itoa(pid), "--native"}
	result.Command = strings.Join(append([]string{resolvedBinary}, args...), " ")
	output, runErr := runCommandCapture(ctx, 60, resolvedBinary, args...)
	if runErr != nil {
		fallbackOutput, fallbackErr := runPrivilegedCommandCapture(ctx, 60, resolvedBinary, args...)
		if fallbackOutput != "" {
			if output != "" {
				output += "\n"
			}
			output += fallbackOutput
		}
		if fallbackErr == nil {
			runErr = nil
		} else {
			runErr = fmt.Errorf("%v; privileged fallback: %v", runErr, fallbackErr)
		}
	}
	result.Output = output
	if runErr != nil {
		result.Error = runErr.Error()
		return result
	}
	result.Status = "collected"
	return result
}

func runNSYSProfile(ctx context.Context, workDir, binary string, pid, durationSeconds int) model.ProfilingToolResult {
	result := model.ProfilingToolResult{Enabled: true, Status: "failed"}
	spec := toolInstallSpec{
		Name:        "nsys",
		LookupNames: []string{"nsys", "/usr/local/cuda/bin/nsys", "/opt/nvidia/nsight-systems/*/bin/nsys"},
		APTPackages: [][]string{{"nsight-systems-cli"}, {"cuda-nsight-systems"}},
		APTPackagePrefixes: []string{
			"nsight-systems-",
			"cuda-nsight-systems-",
		},
		DNFPackages:    [][]string{{"nsight-systems-cli"}, {"cuda-nsight-systems"}},
		YUMPackages:    [][]string{{"nsight-systems-cli"}, {"cuda-nsight-systems"}},
		PacmanPackages: [][]string{{"nsight-systems"}},
	}
	resolvedBinary, err := resolveOrInstallTool(ctx, binary, spec)
	if err != nil {
		result.Available = false
		result.Error = err.Error()
		return result
	}

	result.Available = true
	result.Binary = resolvedBinary
	prefix := filepath.Join(workDir, "nsys-profile")

	baseArgs := []string{
		"profile",
		"--force-overwrite=true",
		"--duration=" + strconv.Itoa(durationSeconds),
		"--sample=none",
		"--trace=cuda,nvtx,osrt",
		"--output=" + prefix,
	}
	attachArgsVariants := [][]string{
		{"--attach-pid=" + strconv.Itoa(pid)},
		{"--pid=" + strconv.Itoa(pid)},
	}
	if helpOutput, helpErr := runCommandCapture(ctx, 30, resolvedBinary, "profile", "--help"); helpErr == nil {
		parsedAttachArgs := nsysAttachArgsFromHelp(helpOutput, pid)
		if len(parsedAttachArgs) == 0 {
			result.Status = "unsupported"
			result.Error = "installed nsys does not support profiling by attaching to an existing process PID"
			return result
		}
		attachArgsVariants = parsedAttachArgs
	}
	argsVariants := make([][]string, 0, len(attachArgsVariants))
	for _, attachArgs := range attachArgsVariants {
		args := append([]string{}, baseArgs...)
		args = append(args, attachArgs...)
		argsVariants = append(argsVariants, args)
	}

	var (
		combinedOutput strings.Builder
		lastErr        error
	)
	for _, args := range argsVariants {
		result.Command = strings.Join(append([]string{resolvedBinary}, args...), " ")
		output, runErr := runCommandCapture(ctx, durationSeconds+150, resolvedBinary, args...)
		if output != "" {
			if combinedOutput.Len() > 0 {
				combinedOutput.WriteString("\n")
			}
			combinedOutput.WriteString(output)
		}
		if runErr != nil {
			fallbackOutput, fallbackErr := runPrivilegedCommandCapture(ctx, durationSeconds+150, resolvedBinary, args...)
			if fallbackOutput != "" {
				if combinedOutput.Len() > 0 {
					combinedOutput.WriteString("\n")
				}
				combinedOutput.WriteString(fallbackOutput)
			}
			if fallbackErr == nil {
				runErr = nil
			} else {
				runErr = fmt.Errorf("%v; privileged fallback: %v", runErr, fallbackErr)
			}
		}
		if runErr == nil {
			lastErr = nil
			break
		}
		lastErr = runErr
	}

	result.Output = combinedOutput.String()
	if lastErr != nil {
		result.Error = lastErr.Error()
		return result
	}

	artifact := firstExistingPath(prefix+".nsys-rep", prefix+".qdrep")
	if artifact == "" {
		result.Error = "nsys profile finished but no report artifact was generated"
		return result
	}
	statsOutput, statsErr := runCommandCapture(ctx, 120, resolvedBinary, "stats", "--report", "summary,gpukernsum", artifact)
	if statsErr != nil {
		fallbackStatsOutput, fallbackStatsErr := runPrivilegedCommandCapture(ctx, 120, resolvedBinary, "stats", "--report", "summary,gpukernsum", artifact)
		if fallbackStatsOutput != "" {
			if statsOutput != "" {
				statsOutput += "\n"
			}
			statsOutput += fallbackStatsOutput
		}
		if fallbackStatsErr == nil {
			statsErr = nil
		} else {
			statsErr = fmt.Errorf("%v; privileged fallback: %v", statsErr, fallbackStatsErr)
		}
	}
	result.Summary = statsOutput
	if statsErr != nil {
		result.Error = statsErr.Error()
		return result
	}

	result.Status = "collected"
	return result
}

func nsysAttachArgsFromHelp(helpText string, pid int) [][]string {
	pidText := strconv.Itoa(pid)
	normalized := strings.ToLower(helpText)
	var variants [][]string
	if strings.Contains(normalized, "--attach-pid") {
		variants = append(variants, []string{"--attach-pid=" + pidText})
	}
	if strings.Contains(normalized, "--pid") {
		variants = append(variants, []string{"--pid=" + pidText})
	}
	return variants
}

func firstExistingPath(candidates ...string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
