package main

import "testing"

func TestParseConfigPathFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "config equals form",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--config=/tmp/vllm-config.json"},
			want: "/tmp/vllm-config.json",
		},
		{
			name: "config-file split form",
			args: []string{"python", "-m", "vllm.entrypoints.openai.api_server", "--config-file", "/etc/vllm/config.json"},
			want: "/etc/vllm/config.json",
		},
		{
			name: "missing config",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseConfigPathFromArgs(tc.args)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParseRuntimeConfigFromArgs(t *testing.T) {
	args := []string{
		"vllm", "serve", "Qwen/Qwen3.5-2B-Instruct",
		"--tensor-parallel-size", "4",
		"--max-num-seqs=64",
		"--gpu-memory-utilization", "0.92",
		"--enable-prefix-caching",
		"--served-model-name", "chat-prod",
	}

	got := parseRuntimeConfigFromArgs(args)
	if got["model_name"] != "Qwen/Qwen3.5-2B-Instruct" {
		t.Fatalf("expected positional serve model to be captured, got %+v", got)
	}
	if got["tensor_parallel_size"] != int64(4) {
		t.Fatalf("expected tensor_parallel_size=4, got %+v", got)
	}
	if got["max_num_seqs"] != int64(64) {
		t.Fatalf("expected max_num_seqs=64, got %+v", got)
	}
	if got["gpu_memory_utilization"] != 0.92 {
		t.Fatalf("expected gpu_memory_utilization=0.92, got %+v", got)
	}
	if got["enable_prefix_caching"] != true {
		t.Fatalf("expected boolean flag capture, got %+v", got)
	}
	if got["served_model_name"] != "chat-prod" {
		t.Fatalf("expected served_model_name, got %+v", got)
	}
}

func TestParseVLLMMetricsTargetFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "port only",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--port", "8012"},
			want: "127.0.0.1:8012",
		},
		{
			name: "port equals with wildcard host",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--host=0.0.0.0", "--port=9001"},
			want: "127.0.0.1:9001",
		},
		{
			name: "explicit host and port",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--host", "10.42.0.7", "--port", "8100"},
			want: "10.42.0.7:8100",
		},
		{
			name: "missing port",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--host", "127.0.0.1"},
			want: "",
		},
		{
			name: "invalid port",
			args: []string{"vllm", "serve", "Qwen/Qwen3.5-2B", "--port", "bad"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseVLLMMetricsTargetFromArgs(tc.args)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
