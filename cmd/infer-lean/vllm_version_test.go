package main

import "testing"

func TestExtractVLLMVersion(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "plain semver", text: "0.17.1", want: "0.17.1"},
		{name: "version banner", text: "vLLM API server version 0.8.3.post1", want: "0.8.3.post1"},
		{name: "with prefix", text: "v0.9.0", want: "0.9.0"},
		{name: "no version", text: "hello world", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractVLLMVersion(tc.text)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBuildPythonCandidatesHasAtLeastOneCandidate(t *testing.T) {
	candidates := buildPythonCandidates("/nonexistent/path/vllm")
	if len(candidates) == 0 {
		t.Fatalf("expected at least one python candidate")
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			t.Fatalf("duplicate python candidate: %s", candidate)
		}
		seen[candidate] = struct{}{}
	}
}

func TestEffectiveVLLMVersionProbeTimeout(t *testing.T) {
	if got := effectiveVLLMVersionProbeTimeout(0); got != defaultVLLMVersionProbeTimeoutSeconds {
		t.Fatalf("expected default timeout %d, got %d", defaultVLLMVersionProbeTimeoutSeconds, got)
	}
	if got := effectiveVLLMVersionProbeTimeout(-1); got != defaultVLLMVersionProbeTimeoutSeconds {
		t.Fatalf("expected default timeout %d, got %d", defaultVLLMVersionProbeTimeoutSeconds, got)
	}
	if got := effectiveVLLMVersionProbeTimeout(240); got != 240 {
		t.Fatalf("expected custom timeout 240, got %d", got)
	}
}
