package model

import "time"

const (
	CollectorSchemaVersionV2    = "collector/v2"
	AnalysisSchemaVersionV2     = "analysis/v2"
	RecommendationSchemaV2      = "recommendation/v2"
	OptimizationSchemaVersionV2 = "optimization/v2"

	PressureStatusLow                  = "low"
	PressureStatusModerate             = "moderate"
	PressureStatusHigh                 = "high"
	PressureStatusInsufficientEvidence = "insufficient_evidence"

	PressureSourceMeasured = "measured"
	PressureSourceInferred = "inferred"
	PressureSourceMixed    = "mixed"

	DecisionKindKeepCurrent            = "keep_current"
	DecisionKindApplyConfigChange      = "apply_config_change"
	DecisionKindOptimizeInputPipeline  = "optimize_input_pipeline_first"
	DecisionKindChangeTrafficShape     = "change_traffic_shape"
	DecisionKindConsiderHardwareChange = "consider_hardware_change"
	DecisionKindInsufficientEvidence   = "insufficient_evidence"

	MechanismKeepCurrentOperatingMode = "keep_current_operating_mode"
	MechanismIncreaseUsefulBatching   = "increase_useful_batching"
	MechanismReduceQueueing           = "reduce_queueing"
	MechanismReduceHostPreprocessing  = "reduce_host_preprocessing_overhead"
	MechanismImproveCacheReuse        = "improve_cache_reuse"
	MechanismSplitTrafficClasses      = "split_traffic_classes"
	MechanismAddHardwareCapacity      = "add_hardware_capacity"

	ScenarioEvidenceAvailable            = "available"
	ScenarioEvidencePreview              = "preview"
	ScenarioEvidenceInsufficientEvidence = "insufficient_evidence"

	FrontierProximityLow      = "low"
	FrontierProximityModerate = "moderate"
	FrontierProximityHigh     = "high"
	FrontierProximityUnknown  = "unknown"

	EvidenceQualityStrong  = "strong"
	EvidenceQualityPartial = "partial"
	EvidenceQualityWeak    = "weak"

	ConfidenceSourceMeasuredOnly    = "measured_only"
	ConfidenceSourceRuleBased       = "rule_based"
	ConfidenceSourceBenchmarkCalib  = "benchmark_calibrated"
	ConfidenceSourceHybrid          = "hybrid"
	ConfidenceSourceLimitedEvidence = "limited_evidence"

	AccessTierFree = "free"
	AccessTierPaid = "paid"
)

type ReportMetadataV2 struct {
	SchemaVersion string    `json:"schema_version"`
	ReportKind    string    `json:"report_kind"`
	GeneratedAt   time.Time `json:"generated_at"`
	ToolName      string    `json:"tool_name"`
	ToolVersion   string    `json:"tool_version"`
	ID            string    `json:"id,omitempty"`
	Status        string    `json:"status,omitempty"`
}

type ConstraintV2 struct {
	TargetP95LatencyMS *float64 `json:"target_p95_latency_ms,omitempty"`
	MinThroughput      *float64 `json:"min_throughput,omitempty"`
}

type WorkloadContextV2 struct {
	DeclaredIntent *WorkloadProfile          `json:"declared_intent,omitempty"`
	ObservedIntent *ObservedWorkloadProfile  `json:"observed_intent,omitempty"`
	Alignment      *WorkloadProfileAlignment `json:"intent_alignment,omitempty"`
	ObjectiveMode  string                    `json:"objective_mode"`
	Constraint     *ConstraintV2             `json:"constraint,omitempty"`
	ServingPattern string                    `json:"serving_pattern,omitempty"`
	Multimodal     bool                      `json:"multimodal"`
	MediaReuse     string                    `json:"media_reuse,omitempty"`
}

type OperatingLatencyV2 struct {
	TTFTMS      *float64 `json:"ttft_ms,omitempty"`
	P50MS       *float64 `json:"p50_ms,omitempty"`
	P95MS       *float64 `json:"p95_ms,omitempty"`
	AvgMS       *float64 `json:"avg_ms,omitempty"`
	QueueWaitMS *float64 `json:"queue_wait_ms,omitempty"`
}

type ConcurrencyV2 struct {
	AvgRunning *float64 `json:"avg_running,omitempty"`
	MaxRunning *float64 `json:"max_running,omitempty"`
	AvgWaiting *float64 `json:"avg_waiting,omitempty"`
	MaxWaiting *float64 `json:"max_waiting,omitempty"`
}

type GPUPointV2 struct {
	EffectiveLoadPct *float64 `json:"effective_load_pct,omitempty"`
	DeviceUtilPct    *float64 `json:"device_utilization_pct,omitempty"`
	ComputeLoadPct   *float64 `json:"compute_load_pct,omitempty"`
	MemoryLoadPct    *float64 `json:"memory_bandwidth_load_pct,omitempty"`
	KVCacheUsagePct  *float64 `json:"kv_cache_usage_pct,omitempty"`
	Count            int      `json:"count,omitempty"`
}

type HostPointV2 struct {
	CPUUtilizationPct *float64 `json:"cpu_utilization_pct,omitempty"`
}

type OperatingPointV2 struct {
	RequestRateRPS            *float64           `json:"request_rate_rps,omitempty"`
	ThroughputTokensPerSecond *float64           `json:"throughput_tokens_per_second,omitempty"`
	Latency                   OperatingLatencyV2 `json:"latency"`
	Concurrency               ConcurrencyV2      `json:"concurrency"`
	GPU                       GPUPointV2         `json:"gpu"`
	Host                      HostPointV2        `json:"host"`
	Multimodal                bool               `json:"multimodal"`
	SourceType                string             `json:"source_type"`
}

type PressureDimensionV2 struct {
	PressureStatus string         `json:"pressure_status"`
	Confidence     float64        `json:"confidence"`
	SourceType     string         `json:"source_type"`
	Evidence       []EvidenceItem `json:"evidence,omitempty"`
	Summary        string         `json:"summary"`
}

type PressureSummaryV2 struct {
	DominantBottleneck string              `json:"dominant_bottleneck"`
	Compute            PressureDimensionV2 `json:"compute"`
	MemoryBandwidth    PressureDimensionV2 `json:"memory_bandwidth"`
	KVCache            PressureDimensionV2 `json:"kv_cache"`
	Queue              PressureDimensionV2 `json:"queue"`
	HostInputPipeline  PressureDimensionV2 `json:"host_input_pipeline"`
}

type FrontierAssessmentV2 struct {
	IsNearFrontier    *bool  `json:"is_near_frontier,omitempty"`
	FrontierProximity string `json:"frontier_proximity"`
	FrontierReason    string `json:"frontier_reason"`
}

type FrontierValueV2 struct {
	Value      *float64 `json:"value,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	SourceType string   `json:"source_type,omitempty"`
}

type FrontierV2 struct {
	IsNearFrontier                   *bool            `json:"is_near_frontier,omitempty"`
	FrontierProximity                string           `json:"frontier_proximity"`
	FrontierReason                   string           `json:"frontier_reason"`
	PracticalNodeHeadroom            *FrontierValueV2 `json:"practical_node_headroom,omitempty"`
	SafeMaxThroughputAtConstraint    *FrontierValueV2 `json:"safe_max_throughput_at_constraint,omitempty"`
	ExpectedLatencyFloorAtConstraint *FrontierValueV2 `json:"expected_latency_floor_at_constraint,omitempty"`
}

type EvidenceQualityV2 struct {
	Status            string   `json:"status"`
	Summary           string   `json:"summary"`
	Reasons           []string `json:"reasons,omitempty"`
	SnapshotCount     int      `json:"snapshot_count,omitempty"`
	LatencySufficient bool     `json:"latency_sufficient"`
	KVCacheSufficient bool     `json:"kv_cache_sufficient"`
}

type KnobDeltaV2 struct {
	Name             string `json:"name"`
	CurrentValue     any    `json:"current_value,omitempty"`
	RecommendedValue any    `json:"recommended_value,omitempty"`
}

type RecommendationBasisV2 struct {
	Source           string                `json:"source"`
	Summary          string                `json:"summary,omitempty"`
	MatchedBenchmark *MatchedCorpusProfile `json:"matched_benchmark,omitempty"`
	ValidationChecks []string              `json:"validation_checks,omitempty"`
	Warnings         []string              `json:"warnings,omitempty"`
}

type DecisionV2 struct {
	Kind             string        `json:"kind"`
	Reason           string        `json:"reason"`
	Confidence       float64       `json:"confidence"`
	ConfidenceSource string        `json:"confidence_source"`
	PrimaryMechanism string        `json:"primary_mechanism"`
	ExpectedEffect   string        `json:"expected_effect,omitempty"`
	ExactKnobDeltas  []KnobDeltaV2 `json:"exact_knob_deltas,omitempty"`
}

type OperatingPointProjectionV2 struct {
	ThroughputTokensPerSecond *float64 `json:"throughput_tokens_per_second,omitempty"`
	RequestRateRPS            *float64 `json:"request_rate_rps,omitempty"`
	P95LatencyMS              *float64 `json:"p95_latency_ms,omitempty"`
	P50LatencyMS              *float64 `json:"p50_latency_ms,omitempty"`
	TTFTMS                    *float64 `json:"ttft_ms,omitempty"`
}

type ScenarioV2 struct {
	Slot                    string                      `json:"slot"`
	ObjectiveMode           string                      `json:"objective_mode"`
	EvidenceState           string                      `json:"evidence_state"`
	DecisionKind            string                      `json:"decision_kind"`
	Mechanism               string                      `json:"mechanism"`
	Rationale               string                      `json:"rationale"`
	ExpectedUpside          string                      `json:"expected_upside,omitempty"`
	Tradeoff                string                      `json:"tradeoff,omitempty"`
	Confidence              float64                     `json:"confidence"`
	RecommendationBasis     *RecommendationBasisV2      `json:"recommendation_basis,omitempty"`
	ProjectedOperatingPoint *OperatingPointProjectionV2 `json:"projected_operating_point,omitempty"`
	ExactKnobDeltas         []KnobDeltaV2               `json:"exact_knob_deltas,omitempty"`
}

type ScenarioSetV2 struct {
	RecommendedDecision ScenarioV2 `json:"recommended_decision"`
	ThroughputFirst     ScenarioV2 `json:"throughput_first"`
	LatencyFirst        ScenarioV2 `json:"latency_first"`
	Balanced            ScenarioV2 `json:"balanced"`
}

type MultimodalNoteV2 struct {
	Summary         string         `json:"summary"`
	Confidence      float64        `json:"confidence"`
	Recommendations []string       `json:"recommendations,omitempty"`
	Evidence        []EvidenceItem `json:"evidence,omitempty"`
}

type OptimizationEvidenceV2 struct {
	Findings          []Finding              `json:"findings,omitempty"`
	Warnings          []string               `json:"warnings,omitempty"`
	SystemInformation *OSInformation         `json:"system_information,omitempty"`
	GPUInformation    *GPUInformation        `json:"gpu_information,omitempty"`
	Workload          *WorkloadContextV2     `json:"workload,omitempty"`
	Configuration     map[string]any         `json:"configuration,omitempty"`
	Profiling         *AdvancedProfilingInfo `json:"profiling,omitempty"`
	CollectorLogs     map[string]string      `json:"collector_logs,omitempty"`
	CollectedMetrics  []CollectedMetricPoint `json:"collected_metrics,omitempty"`
	MatchedBenchmark  *MatchedCorpusProfile  `json:"matched_benchmark,omitempty"`
}

type AccessV2 struct {
	Tier       string   `json:"tier"`
	Redactions []string `json:"redactions,omitempty"`
}

type CollectorReportV2 struct {
	Metadata          ReportMetadataV2       `json:"metadata"`
	Environment       map[string]any         `json:"environment,omitempty"`
	RuntimeConfig     map[string]any         `json:"runtime_config,omitempty"`
	MetricsWindow     []CollectedMetricPoint `json:"metrics_window,omitempty"`
	Profiling         *AdvancedProfilingInfo `json:"profiling,omitempty"`
	CollectionQuality EvidenceQualityV2      `json:"collection_quality"`
	Warnings          []string               `json:"warnings,omitempty"`
	RawMetrics        []CollectedMetricPoint `json:"raw_metrics,omitempty"`
}

type AnalysisReportV2 struct {
	Metadata           ReportMetadataV2       `json:"metadata"`
	Workload           WorkloadContextV2      `json:"workload"`
	OperatingPoint     OperatingPointV2       `json:"operating_point"`
	PressureSummary    PressureSummaryV2      `json:"pressure_summary"`
	FrontierAssessment FrontierAssessmentV2   `json:"frontier_assessment"`
	EvidenceQuality    EvidenceQualityV2      `json:"evidence_quality"`
	Findings           []Finding              `json:"findings,omitempty"`
	Configuration      map[string]any         `json:"configuration,omitempty"`
	MultimodalNotes    []MultimodalNoteV2     `json:"multimodal_notes,omitempty"`
	Evidence           OptimizationEvidenceV2 `json:"evidence"`
}

type RecommendationReportV2 struct {
	Metadata            ReportMetadataV2        `json:"metadata"`
	AnalysisRef         SourceAnalysisReference `json:"analysis_ref"`
	ObjectiveMode       string                  `json:"objective_mode"`
	Constraint          *ConstraintV2           `json:"constraint,omitempty"`
	PrimaryDecision     DecisionV2              `json:"primary_decision"`
	Frontier            FrontierV2              `json:"frontier"`
	Scenarios           ScenarioSetV2           `json:"scenarios"`
	RecommendationBasis RecommendationBasisV2   `json:"recommendation_basis"`
	Warnings            []string                `json:"warnings,omitempty"`
}

type OptimizationReportV2 struct {
	Metadata            ReportMetadataV2       `json:"metadata"`
	Workload            WorkloadContextV2      `json:"workload"`
	OperatingPoint      OperatingPointV2       `json:"operating_point"`
	PressureSummary     PressureSummaryV2      `json:"pressure_summary"`
	Frontier            FrontierV2             `json:"frontier"`
	PrimaryDecision     DecisionV2             `json:"primary_decision"`
	Scenarios           ScenarioSetV2          `json:"scenarios"`
	Configuration       map[string]any         `json:"configuration,omitempty"`
	MultimodalNotes     []MultimodalNoteV2     `json:"multimodal_notes,omitempty"`
	RecommendationBasis RecommendationBasisV2  `json:"recommendation_basis"`
	Evidence            OptimizationEvidenceV2 `json:"evidence"`
	Access              AccessV2               `json:"access"`
}

type OptimizationStatusV2 struct {
	ID         string           `json:"id"`
	Status     string           `json:"status"`
	Objective  string           `json:"objective_mode,omitempty"`
	Constraint *ConstraintV2    `json:"constraint,omitempty"`
	Metadata   ReportMetadataV2 `json:"metadata"`
}
