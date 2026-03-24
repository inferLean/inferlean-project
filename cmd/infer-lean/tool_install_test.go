package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLatestAptPackageByPrefixPrefersNewestVersion(t *testing.T) {
	searchOutput := `
nsight-systems-target - NVIDIA Nsight Systems (target specific libraries)
nsight-systems-2024.6.2 - Nsight Systems profiler
nsight-systems-2025.1.3 - Nsight Systems profiler
nsight-systems-2025.6.3 - Nsight Systems profiler
`

	got := latestAptPackageByPrefix(searchOutput, "nsight-systems-")
	want := "nsight-systems-2025.6.3"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLatestAptPackageByPrefixNoMatch(t *testing.T) {
	searchOutput := `
python3-vllm - vllm python package
dcgm-exporter - NVIDIA exporter
`
	got := latestAptPackageByPrefix(searchOutput, "nsight-systems-")
	if got != "" {
		t.Fatalf("expected empty package, got %q", got)
	}
}

func TestFirstExistingBinarySupportsGlobCandidates(t *testing.T) {
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "opt", "nvidia", "nsight-systems", "2025.6.3", "bin", "nsys")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho nsys\n"), 0o755); err != nil {
		t.Fatalf("write nsys: %v", err)
	}

	pattern := filepath.Join(tmp, "opt", "nvidia", "nsight-systems", "*", "bin", "nsys")
	got := firstExistingBinary([]string{pattern})
	if got != binPath {
		t.Fatalf("expected %q, got %q", binPath, got)
	}
}
