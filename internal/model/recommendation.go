package model

import (
	"encoding/json"
	"time"
)

type SourceAnalysisReference struct {
	SchemaVersion string    `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	ToolVersion   string    `json:"tool_version"`
}

type RecommendationReport struct {
	SchemaVersion        string                  `json:"schema_version"`
	GeneratedAt          time.Time               `json:"generated_at"`
	ToolName             string                  `json:"tool_name"`
	ToolVersion          string                  `json:"tool_version"`
	SourceAnalysis       SourceAnalysisReference `json:"source_analysis_report"`
	Objective            string                  `json:"optimization_priority"`
	DeclaredGoal         *DeclaredGoalSummary    `json:"declared_intent,omitempty"`
	Guardrail            *GuardrailSummary       `json:"guardrail_policy,omitempty"`
	CurrentServiceState  *ServiceSummary         `json:"service_snapshot,omitempty"`
	WastedCapacity       *WastedCapacitySummary  `json:"wasted_capacity,omitempty"`
	PrimaryAction        *PrimaryActionSummary   `json:"recommended_action,omitempty"`
	PredictedImpact      *PredictedImpactSummary `json:"expected_impact,omitempty"`
	MatchSummary         *MatchSummary           `json:"benchmark_match_summary,omitempty"`
	Validation           *ValidationSummary      `json:"validation_checks,omitempty"`
	AlternativeActions   []PrimaryActionSummary  `json:"alternative_actions,omitempty"`
	MatchedCorpusProfile *MatchedCorpusProfile   `json:"matched_benchmark_profile,omitempty"`
	BaselinePrediction   *Prediction             `json:"current_baseline_prediction,omitempty"`
	CapacityOpportunity  *CapacityOpportunity    `json:"gpu_capacity_headroom,omitempty"`
	Recommendations      []RecommendationItem    `json:"all_recommendations,omitempty"`
	ScenarioPrediction   *Prediction             `json:"what_if_prediction,omitempty"`
	LLMEnhanced          *LLMEnhancedOutput      `json:"llm_enhanced,omitempty"`
	Warnings             []string                `json:"warnings,omitempty"`
}

func (r *RecommendationReport) UnmarshalJSON(data []byte) error {
	type recommendationReportAlias RecommendationReport
	aux := struct {
		*recommendationReportAlias
		LegacySourceAnalysis       SourceAnalysisReference `json:"source_analysis"`
		LegacyObjective            string                  `json:"objective"`
		LegacyDeclaredGoal         *DeclaredGoalSummary    `json:"declared_goal"`
		LegacyGuardrail            *GuardrailSummary       `json:"guardrail"`
		LegacyCurrentServiceState  *ServiceSummary         `json:"current_service_state"`
		LegacyPrimaryAction        *PrimaryActionSummary   `json:"primary_action"`
		LegacyPredictedImpact      *PredictedImpactSummary `json:"predicted_impact"`
		LegacyMatchSummary         *MatchSummary           `json:"match_summary"`
		LegacyValidation           *ValidationSummary      `json:"validation"`
		LegacyMatchedCorpusProfile *MatchedCorpusProfile   `json:"matched_corpus_profile"`
		LegacyBaselinePrediction   *Prediction             `json:"baseline_prediction"`
		LegacyCapacityOpportunity  *CapacityOpportunity    `json:"capacity_opportunity"`
		LegacyRecommendations      []RecommendationItem    `json:"recommendations"`
		LegacyScenarioPrediction   *Prediction             `json:"scenario_prediction"`
	}{
		recommendationReportAlias: (*recommendationReportAlias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if r.SourceAnalysis == (SourceAnalysisReference{}) {
		r.SourceAnalysis = aux.LegacySourceAnalysis
	}
	if r.Objective == "" {
		r.Objective = aux.LegacyObjective
	}
	if r.DeclaredGoal == nil {
		r.DeclaredGoal = aux.LegacyDeclaredGoal
	}
	if r.Guardrail == nil {
		r.Guardrail = aux.LegacyGuardrail
	}
	if r.CurrentServiceState == nil {
		r.CurrentServiceState = aux.LegacyCurrentServiceState
	}
	if r.PrimaryAction == nil {
		r.PrimaryAction = aux.LegacyPrimaryAction
	}
	if r.PredictedImpact == nil {
		r.PredictedImpact = aux.LegacyPredictedImpact
	}
	if r.MatchSummary == nil {
		r.MatchSummary = aux.LegacyMatchSummary
	}
	if r.Validation == nil {
		r.Validation = aux.LegacyValidation
	}
	if r.MatchedCorpusProfile == nil {
		r.MatchedCorpusProfile = aux.LegacyMatchedCorpusProfile
	}
	if r.BaselinePrediction == nil {
		r.BaselinePrediction = aux.LegacyBaselinePrediction
	}
	if r.CapacityOpportunity == nil {
		r.CapacityOpportunity = aux.LegacyCapacityOpportunity
	}
	if len(r.Recommendations) == 0 && len(aux.LegacyRecommendations) > 0 {
		r.Recommendations = aux.LegacyRecommendations
	}
	if r.ScenarioPrediction == nil {
		r.ScenarioPrediction = aux.LegacyScenarioPrediction
	}
	return nil
}

type MatchedCorpusProfile struct {
	ID            string  `json:"id"`
	CorpusVersion string  `json:"corpus_version,omitempty"`
	ModelName     string  `json:"model_name,omitempty"`
	ModelFamily   string  `json:"model_family,omitempty"`
	GPUCount      int     `json:"gpu_count,omitempty"`
	HardwareClass string  `json:"hardware_class,omitempty"`
	WorkloadClass string  `json:"workload_class,omitempty"`
	MatchScore    float64 `json:"match_score,omitempty"`
	Basis         string  `json:"basis,omitempty"`
}

type Prediction struct {
	ThroughputTokensPerSecond float64 `json:"throughput_tokens_per_second,omitempty"`
	TTFTMs                    float64 `json:"ttft_ms,omitempty"`
	LatencyP50Ms              float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms              float64 `json:"latency_p95_ms,omitempty"`
	GPUUtilizationPct         float64 `json:"gpu_utilization_pct,omitempty"`
	Basis                     string  `json:"basis,omitempty"`
	Confidence                float64 `json:"confidence,omitempty"`
}

type PredictedEffect struct {
	ThroughputTokensPerSecond float64 `json:"throughput_tokens_per_second,omitempty"`
	TTFTMs                    float64 `json:"ttft_ms,omitempty"`
	LatencyP50Ms              float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms              float64 `json:"latency_p95_ms,omitempty"`
	GPUUtilizationPct         float64 `json:"gpu_utilization_pct,omitempty"`
	ThroughputDeltaPct        float64 `json:"throughput_delta_pct,omitempty"`
	TTFTDeltaPct              float64 `json:"ttft_delta_pct,omitempty"`
	LatencyP50DeltaPct        float64 `json:"latency_p50_delta_pct,omitempty"`
	LatencyP95DeltaPct        float64 `json:"latency_p95_delta_pct,omitempty"`
	GPUUtilizationDeltaPct    float64 `json:"gpu_utilization_delta_pct,omitempty"`
}

type RecommendationItem struct {
	ID               string            `json:"id"`
	Priority         int               `json:"priority"`
	Objective        string            `json:"objective"`
	Summary          string            `json:"summary"`
	Changes          []ParameterChange `json:"changes,omitempty"`
	PredictedEffect  PredictedEffect   `json:"predicted_effect"`
	Confidence       float64           `json:"confidence"`
	SafetyNotes      []string          `json:"safety_notes,omitempty"`
	ValidationChecks []string          `json:"validation_checks,omitempty"`
	Basis            string            `json:"basis"`
}

type CapacityOpportunity struct {
	CurrentGPULoadPct          float64 `json:"current_gpu_load_pct,omitempty"`
	PredictedOptimalGPULoadPct float64 `json:"predicted_optimal_gpu_load_pct,omitempty"`
	RecoverableGPULoadPct      float64 `json:"recoverable_gpu_load_pct,omitempty"`
	RecoverableGPUCount        float64 `json:"recoverable_gpu_count,omitempty"`
	TotalGPUCount              float64 `json:"total_gpu_count,omitempty"`
	Basis                      string  `json:"basis,omitempty"`
	Confidence                 float64 `json:"confidence,omitempty"`
}

type ParameterChange struct {
	Name             string `json:"name"`
	CurrentValue     any    `json:"current_value,omitempty"`
	RecommendedValue any    `json:"recommended_value,omitempty"`
}

type WastedCapacitySummary struct {
	Headline         string   `json:"headline"`
	ThroughputGapRPS *float64 `json:"throughput_gap_rps"`
	ThroughputGapPct *float64 `json:"throughput_gap_pct"`
	GPUHeadroomPct   *float64 `json:"gpu_headroom_pct"`
	GPUHeadroomCount *float64 `json:"gpu_headroom_count"`
	Basis            string   `json:"basis,omitempty"`
	Confidence       float64  `json:"confidence,omitempty"`
}

type DeclaredGoalSummary struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
}

type GuardrailSummary struct {
	Summary                   string   `json:"summary,omitempty"`
	MinThroughputRetentionPct *float64 `json:"min_throughput_retention_pct,omitempty"`
	MaxLatencyP50IncreasePct  *float64 `json:"max_latency_p50_increase_pct,omitempty"`
}

type PrimaryActionSummary struct {
	Summary        string            `json:"summary"`
	Changes        []ParameterChange `json:"changes,omitempty"`
	RollbackValues []ParameterChange `json:"rollback_values,omitempty"`
	Confidence     float64           `json:"confidence,omitempty"`
	Basis          string            `json:"basis,omitempty"`
}

type PredictedImpactSummary struct {
	RequestRateRPS    NumericImpact      `json:"request_rate_rps"`
	RequestLatencyMS  PredictedLatencyMS `json:"request_latency_ms"`
	SaturationPct     NumericImpact      `json:"saturation_pct"`
	GPUUtilizationPct NumericImpact      `json:"gpu_utilization_pct"`
}

type NumericImpact struct {
	After    *float64 `json:"after"`
	DeltaPct *float64 `json:"delta_pct"`
}

type PredictedLatencyMS struct {
	Avg NumericImpact `json:"avg"`
	P50 NumericImpact `json:"p50"`
	P90 NumericImpact `json:"p90"`
}

type MatchSummary struct {
	ProfileID  string  `json:"profile_id,omitempty"`
	MatchScore float64 `json:"match_score,omitempty"`
	Basis      string  `json:"basis,omitempty"`
}

type ValidationSummary struct {
	Checks []string `json:"checks,omitempty"`
}

type LLMEnhancedOutput struct {
	Summary          string   `json:"summary,omitempty"`
	Explanation      string   `json:"explanation,omitempty"`
	ActionHighlights []string `json:"action_highlights,omitempty"`
}
