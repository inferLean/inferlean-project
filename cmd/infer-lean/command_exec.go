package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCommandCapture(ctx context.Context, timeoutSeconds int, binary string, args ...string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		return "", errors.New("empty command")
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmdLine := strings.Join(append([]string{binary}, args...), " ")
	debugf("exec start (timeout=%s): %s", timeout, cmdLine)
	start := time.Now()

	cmd := exec.CommandContext(runCtx, binary, args...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	elapsed := time.Since(start)
	if err != nil {
		debugf("exec failed (%s): %s | err=%v | output=%s", elapsed, cmdLine, err, debugSnippet(output, 1200))
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return output, fmt.Errorf("command timed out after %s", timeout)
		}
		return output, fmt.Errorf("command failed: %w", err)
	}
	debugf("exec success (%s): %s | output=%s", elapsed, cmdLine, debugSnippet(output, 400))
	return output, nil
}

func runPrivilegedCommandCapture(ctx context.Context, timeoutSeconds int, binary string, args ...string) (string, error) {
	if os.Geteuid() == 0 {
		debugf("exec privileged mode: already running as root")
		return runCommandCapture(ctx, timeoutSeconds, binary, args...)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return "", errors.New("command requires root privileges but sudo is not available")
	}
	debugf("exec privileged mode: using sudo for %s", binary)
	wrappedArgs := append([]string{binary}, args...)
	return runCommandCapture(ctx, timeoutSeconds, sudo, wrappedArgs...)
}
