package recommender

import (
	"context"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

type Objective string

const (
	BalancedObjective        Objective = "balanced"
	ThroughputFirstObjective Objective = "throughput_first"
	LatencyFirstObjective    Objective = "latency_first"
)

type Options struct {
	AnalysisPath string
	CorpusPath   string
	OutputPath   string
	Now          time.Time
	ToolVersion  string
	Objective    Objective
	ScenarioSet  map[string]float64
	LLMEnhance   bool
}

type Recommender struct {
	now         func() time.Time
	toolVersion string
}

type corpusDocument struct {
	Version  string          `json:"version"`
	Profiles []corpusProfile `json:"profiles"`
}

type corpusProfile struct {
	ID            string              `json:"id"`
	ModelName     string              `json:"model_name"`
	ModelFamily   string              `json:"model_family"`
	GPUCount      int                 `json:"gpu_count"`
	HardwareClass string              `json:"hardware_class"`
	WorkloadClass string              `json:"workload_class"`
	Measurements  []corpusMeasurement `json:"measurements"`
}

type corpusMeasurement struct {
	Parameters map[string]float64 `json:"parameters"`
	Metrics    corpusMetrics      `json:"metrics"`
}

type corpusMetrics struct {
	ThroughputTokensPerSecond float64 `json:"throughput_tokens_per_second"`
	TTFTMs                    float64 `json:"ttft_ms"`
	LatencyP50Ms              float64 `json:"latency_p50_ms"`
	LatencyP95Ms              float64 `json:"latency_p95_ms"`
	GPUUtilizationPct         float64 `json:"gpu_utilization_pct"`
}

type profileMatch struct {
	Profile       corpusProfile
	CorpusVersion string
	Score         float64
	Basis         string
}

type measurementSelection struct {
	Measurement corpusMeasurement
	Distance    float64
	Exact       bool
}

type derivedContext struct {
	ModelName        string
	ModelFamily      string
	GPUCount         int
	HardwareClass    string
	WorkloadClass    string
	CurrentConfig    map[string]any
	CurrentNumeric   map[string]float64
	ObservedBaseline *model.Prediction
	TrafficObserved  bool
}

func New(opts Options) *Recommender {
	r := &Recommender{
		now:         time.Now,
		toolVersion: opts.ToolVersion,
	}
	if r.toolVersion == "" {
		r.toolVersion = model.ToolVersion
	}
	return r
}

func Recommend(ctx context.Context, opts Options) (*model.RecommendationReport, error) {
	return New(opts).Recommend(ctx, opts)
}
