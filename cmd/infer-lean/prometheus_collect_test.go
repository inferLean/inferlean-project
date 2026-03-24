package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildPrometheusConfig(t *testing.T) {
	cfg := buildPrometheusConfig(9100, 9400, "127.0.0.1:8000", "/metrics")
	if !strings.Contains(cfg, "127.0.0.1:9100") {
		t.Fatalf("expected node exporter target in config: %s", cfg)
	}
	if !strings.Contains(cfg, "127.0.0.1:9400") {
		t.Fatalf("expected dcgm exporter target in config: %s", cfg)
	}
	if !strings.Contains(cfg, "job_name: \"vllm\"") {
		t.Fatalf("expected vllm job in config: %s", cfg)
	}
	if !strings.Contains(cfg, "127.0.0.1:8000") {
		t.Fatalf("expected vllm target in config: %s", cfg)
	}
}

func TestPromQueriesForCollectionIncludesDCGMWhenEnabled(t *testing.T) {
	queries := promQueriesForCollection(true)
	if len(queries) != len(defaultPromQueries) {
		t.Fatalf("expected all queries when dcgm is enabled: got=%d want=%d", len(queries), len(defaultPromQueries))
	}
}

func TestPromQueriesForCollectionSkipsDCGMWhenDisabled(t *testing.T) {
	queries := promQueriesForCollection(false)
	for _, query := range queries {
		if strings.Contains(strings.ToUpper(query.expr), "DCGM_") {
			t.Fatalf("unexpected dcgm query when disabled: %s", query.expr)
		}
	}
	if len(queries) >= len(defaultPromQueries) {
		t.Fatalf("expected fewer queries when dcgm is disabled: got=%d total=%d", len(queries), len(defaultPromQueries))
	}
}

func TestQueryPrometheusRangeAveragesSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {"values": [[1710000000, "10"], [1710000060, "20"]]},
      {"values": [[1710000000, "30"], [1710000060, "40"]]}
    ]
  }
}`)
	}))
	defer server.Close()

	start := time.Unix(1710000000, 0)
	end := time.Unix(1710000060, 0)
	values, err := queryPrometheusRange(context.Background(), server.URL, "dummy", start, end, 30*time.Second)
	if err != nil {
		t.Fatalf("queryPrometheusRange returned error: %v", err)
	}

	ts0 := int64(1710000000 * 1000)
	ts1 := int64(1710000060 * 1000)
	if got := values[ts0]; got != 20 {
		t.Fatalf("expected average at ts0 to be 20, got %v", got)
	}
	if got := values[ts1]; got != 30 {
		t.Fatalf("expected average at ts1 to be 30, got %v", got)
	}
}

func TestQueryPrometheusMultiMetricRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {"metric": {"__name__": "vllm:test_gauge"}, "values": [[1710000000, "10"], [1710000060, "20"]]},
      {"metric": {"__name__": "vllm:test_gauge"}, "values": [[1710000000, "30"], [1710000060, "40"]]},
      {"metric": {"__name__": "vllm:requests_total"}, "values": [[1710000000, "5"], [1710000060, "7"]]}
    ]
  }
}`)
	}))
	defer server.Close()

	start := time.Unix(1710000000, 0)
	end := time.Unix(1710000060, 0)
	values, err := queryPrometheusMultiMetricRange(context.Background(), server.URL, `{job="vllm",__name__=~"vllm:.*"}`, start, end, 30*time.Second)
	if err != nil {
		t.Fatalf("queryPrometheusMultiMetricRange returned error: %v", err)
	}

	ts0 := int64(1710000000 * 1000)
	ts1 := int64(1710000060 * 1000)
	if got := values["vllm:test_gauge"][ts0]; got != 20 {
		t.Fatalf("expected vllm:test_gauge average at ts0 to be 20, got %v", got)
	}
	if got := values["vllm:test_gauge"][ts1]; got != 30 {
		t.Fatalf("expected vllm:test_gauge average at ts1 to be 30, got %v", got)
	}
	if got := values["vllm:requests_total"][ts1]; got != 7 {
		t.Fatalf("expected vllm:requests_total at ts1 to be 7, got %v", got)
	}
}

func TestQueryPrometheusLabeledMetricRangePreservesSeriesLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {"metric": {"__name__": "node_cpu_seconds_total", "cpu":"0", "mode":"user", "instance":"127.0.0.1:9100"}, "values": [[1710000000, "10"], [1710000060, "20"]]},
      {"metric": {"__name__": "node_cpu_seconds_total", "cpu":"1", "mode":"user", "instance":"127.0.0.1:9100"}, "values": [[1710000000, "30"], [1710000060, "40"]]}
    ]
  }
}`)
	}))
	defer server.Close()

	start := time.Unix(1710000000, 0)
	end := time.Unix(1710000060, 0)
	values, err := queryPrometheusLabeledMetricRange(context.Background(), server.URL, `{job="node_exporter"}`, start, end, 30*time.Second)
	if err != nil {
		t.Fatalf("queryPrometheusLabeledMetricRange returned error: %v", err)
	}

	keyCPU0 := `node_cpu_seconds_total{cpu="0",instance="127.0.0.1:9100",mode="user"}`
	keyCPU1 := `node_cpu_seconds_total{cpu="1",instance="127.0.0.1:9100",mode="user"}`
	ts0 := int64(1710000000 * 1000)
	ts1 := int64(1710000060 * 1000)
	if got := values[keyCPU0][ts0]; got != 10 {
		t.Fatalf("expected %s at ts0 to be 10, got %v", keyCPU0, got)
	}
	if got := values[keyCPU1][ts1]; got != 40 {
		t.Fatalf("expected %s at ts1 to be 40, got %v", keyCPU1, got)
	}
}
