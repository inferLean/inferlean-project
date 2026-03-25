package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/llm"
	"github.com/inferLean/inferlean-project/internal/model"
)

type Analyzer struct {
	now         func() time.Time
	probe       Probe
	toolVersion string
}

func New(opts Options) *Analyzer {
	a := &Analyzer{
		now:         time.Now,
		probe:       opts.Probe,
		toolVersion: opts.ToolVersion,
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.probe == nil {
		a.probe = defaultProbe{}
	}
	if strings.TrimSpace(a.toolVersion) == "" {
		a.toolVersion = model.ToolVersion
	}
	return a
}

func Analyze(ctx context.Context, opts Options) (*model.AnalysisReport, error) {
	return New(opts).Analyze(ctx, opts)
}

func (a *Analyzer) Analyze(ctx context.Context, opts Options) (*model.AnalysisReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := opts.Now
	if now.IsZero() {
		now = a.now().UTC()
	}

	report := &model.AnalysisReport{
		SchemaVersion:             model.AnalysisSchemaVersion,
		GeneratedAt:               now,
		ToolName:                  model.ToolName,
		ToolVersion:               chooseString(opts.ToolVersion, a.toolVersion, model.ToolVersion),
		CurrentVLLMConfigurations: map[string]any{},
	}

	workloadProfile, err := loadWorkloadProfile(opts.WorkloadProfilePath)
	if err != nil {
		return nil, fmt.Errorf("workload profile parse failed: %w", err)
	}
	report.WorkloadProfile = workloadProfile.Profile

	osInfo, gpuInfo, envWarnings := a.probe.Collect(ctx)
	report.OSInformation = osInfo
	report.GPUInformation = gpuInfo
	report.Warnings = append(report.Warnings, envWarnings...)

	cfg, cfgWarnings, cfgErr := loadConfig(opts.ConfigPath)
	report.Warnings = append(report.Warnings, cfgWarnings...)
	if cfgErr != nil {
		report.Warnings = append(report.Warnings, "config parse failed: "+cfgErr.Error())
	}

	metrics, metricsWarnings, metricsErr := loadMetrics(opts.MetricsPath)
	report.Warnings = append(report.Warnings, metricsWarnings...)
	if metricsErr != nil {
		report.Warnings = append(report.Warnings, "metrics parse failed: "+metricsErr.Error())
	} else {
		report.CollectedMetrics = metrics.CollectedMetrics
		report.MetricCollectionOutputs = metrics.MetricCollectionLogs
		report.AdvancedProfiling = metrics.AdvancedProfiling
		report.Warnings = append(report.Warnings, metrics.EmbeddedWarnings...)
		if report.WorkloadProfile == nil && metrics.EmbeddedWorkload != nil {
			report.WorkloadProfile = metrics.EmbeddedWorkload
		}
	}
	if effectiveConfig := mergeConfigMaps(metrics.EmbeddedConfig, cfg.Raw, opts.ConfigOverride); len(effectiveConfig) > 0 {
		report.CurrentVLLMConfigurations = effectiveConfig
	}

	vllmVersion := chooseString(metrics.VLLMVersion, cfgFromFlat(cfg.Flat, "vllm_version"), "unknown")
	installType := chooseString(metrics.DeploymentType, cfgFromFlat(cfg.Flat, "deployment_type"), "host")
	configLocation := chooseString(cfg.Path, nonEmpty(opts.ConfigPath))
	report.VLLMInformation = model.VLLMInformation{
		VLLMVersion:           vllmVersion,
		ConfigurationLocation: configLocation,
		InstallationType:      installType,
	}

	// Backfill GPU values from metrics when host probe is unavailable.
	if report.GPUInformation.GPUModel == "" && metrics.GPUName != "" {
		report.GPUInformation.GPUModel = metrics.GPUName
	}
	if report.GPUInformation.VRAMSizeBytes == 0 && metrics.GPUMemoryMB > 0 {
		report.GPUInformation.VRAMSizeBytes = uint64(metrics.GPUMemoryMB) * 1024 * 1024
	}
	if report.GPUInformation.UtilizationPct == 0 && len(metrics.GPUUtilizationSamples) > 0 {
		report.GPUInformation.UtilizationPct = mean(metrics.GPUUtilizationSamples)
	}
	if metrics.EmbeddedOSInformation != nil {
		report.OSInformation = mergeOSInformation(report.OSInformation, *metrics.EmbeddedOSInformation)
	}
	if metrics.EmbeddedGPUInformation != nil {
		report.GPUInformation = mergeGPUInformation(report.GPUInformation, *metrics.EmbeddedGPUInformation)
	}

	if report.CollectedMetrics == nil {
		report.CollectedMetrics = []model.CollectedMetricPoint{}
	}
	if report.CurrentVLLMConfigurations == nil {
		report.CurrentVLLMConfigurations = map[string]any{}
	}
	if report.MetricCollectionOutputs == nil {
		report.MetricCollectionOutputs = map[string]string{}
	}
	NormalizeReport(report, opts.WorkloadIntent)
	report.FeatureSummary = SummarizeFeatures(report)
	report.ServiceSummary = buildServiceSummary(report, resolveSummaryIntent(report.WorkloadProfile, opts.WorkloadIntent))
	if opts.LLMEnhance {
		if enhanced, warning := llm.EnhanceAnalysisReport(ctx, report); enhanced != nil {
			report.LLMEnhanced = enhanced
		} else if warning != "" {
			report.Warnings = append(report.Warnings, warning)
		}
	}

	return report, nil
}

func (a *Analyzer) AnalyzeToFile(ctx context.Context, opts Options) (string, error) {
	report, err := a.Analyze(ctx, opts)
	if err != nil {
		return "", err
	}
	outputPath := strings.TrimSpace(opts.OutputPath)
	if outputPath == "" {
		return "", errors.New("output path is required")
	}
	if err := SaveJSON(outputPath, report); err != nil {
		return "", err
	}
	return outputPath, nil
}

func SaveJSON(path string, report any) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func chooseString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cfgFromFlat(flat map[string]any, key string) string {
	if flat == nil {
		return ""
	}
	if v, ok := lookupAny(flat, key); ok {
		if s, ok := v.(string); ok {
			return s
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func nonEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func mergeConfigMaps(maps ...map[string]any) map[string]any {
	if len(maps) == 0 {
		return nil
	}
	merged := map[string]any{}
	for _, source := range maps {
		for key, value := range source {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func mergeOSInformation(base, override model.OSInformation) model.OSInformation {
	if strings.TrimSpace(override.OSType) != "" {
		base.OSType = override.OSType
	}
	if strings.TrimSpace(override.Architecture) != "" {
		base.Architecture = override.Architecture
	}
	if strings.TrimSpace(override.OSVersion) != "" {
		base.OSVersion = override.OSVersion
	}
	if strings.TrimSpace(override.Distribution) != "" {
		base.Distribution = override.Distribution
	}
	if override.DiskSizeBytes > 0 {
		base.DiskSizeBytes = override.DiskSizeBytes
	}
	if override.MemorySizeBytes > 0 {
		base.MemorySizeBytes = override.MemorySizeBytes
	}
	if override.AvailableDiskBytes > 0 {
		base.AvailableDiskBytes = override.AvailableDiskBytes
	}
	if override.AvailableMemoryBytes > 0 {
		base.AvailableMemoryBytes = override.AvailableMemoryBytes
	}
	if override.AverageCPUUtilizationPct > 0 {
		base.AverageCPUUtilizationPct = override.AverageCPUUtilizationPct
	}
	if strings.TrimSpace(override.CPU.Model) != "" {
		base.CPU.Model = override.CPU.Model
	}
	if override.CPU.PhysicalCores > 0 {
		base.CPU.PhysicalCores = override.CPU.PhysicalCores
	}
	if override.CPU.LogicalCores > 0 {
		base.CPU.LogicalCores = override.CPU.LogicalCores
	}
	return base
}

func mergeGPUInformation(base, override model.GPUInformation) model.GPUInformation {
	if strings.TrimSpace(override.GPUModel) != "" {
		base.GPUModel = override.GPUModel
	}
	if strings.TrimSpace(override.Company) != "" {
		base.Company = override.Company
	}
	if override.VRAMSizeBytes > 0 {
		base.VRAMSizeBytes = override.VRAMSizeBytes
	}
	if override.UtilizationPct > 0 {
		base.UtilizationPct = override.UtilizationPct
	}
	return base
}
