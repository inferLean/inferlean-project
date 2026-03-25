package analyzer

import (
	"context"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

type Options struct {
	ConfigPath          string
	ConfigOverride      map[string]any
	MetricsPath         string
	WorkloadProfilePath string
	OutputPath          string
	Now                 time.Time
	ToolVersion         string
	WorkloadIntent      WorkloadIntent
	LLMEnhance          bool
	Probe               Probe
}

type Probe interface {
	Collect(ctx context.Context) (model.OSInformation, model.GPUInformation, []string)
}

type defaultProbe struct{}

func CollectEnvironment(ctx context.Context) (model.OSInformation, model.GPUInformation, []string) {
	return defaultProbe{}.Collect(ctx)
}

type configSnapshot struct {
	Path   string
	Format string
	Raw    map[string]any
	Flat   map[string]any
}

type metricsSnapshot struct {
	Path                   string
	Format                 string
	DeploymentType         string
	VLLMVersion            string
	GPUName                string
	GPUMemoryMB            int
	EmbeddedOSInformation  *model.OSInformation
	EmbeddedGPUInformation *model.GPUInformation
	EmbeddedConfig         map[string]any
	EmbeddedWorkload       *model.WorkloadProfile
	EmbeddedWarnings       []string
	MetricCollectionLogs   map[string]string
	AdvancedProfiling      *model.AdvancedProfilingInfo
	GPUUtilizationSamples  []float64
	CollectedMetrics       []model.CollectedMetricPoint
}

type workloadProfileSnapshot struct {
	Path    string
	Format  string
	Profile *model.WorkloadProfile
}
