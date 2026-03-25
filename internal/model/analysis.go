package model

const (
	FindingStatusPresent          = "present"
	FindingStatusAbsent           = "absent"
	FindingStatusInsufficientData = "insufficient_data"

	SeverityNone     = "none"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

type AnalysisSummary struct {
	WorkloadIntent               string             `json:"workload_intent"`
	DataQuality                  DataQualitySummary `json:"data_quality"`
	TotalHeuristicImprovementPct float64            `json:"total_heuristic_improvement_pct,omitempty"`
	Findings                     []Finding          `json:"findings,omitempty"`
}

type DataQualitySummary struct {
	SnapshotCount        int     `json:"snapshot_count"`
	IntervalSeconds      float64 `json:"interval_seconds"`
	TrafficObserved      bool    `json:"traffic_observed"`
	EnoughLatencySamples bool    `json:"enough_latency_samples"`
	EnoughKVCacheSamples bool    `json:"enough_kv_cache_samples"`
}

type Finding struct {
	ID                      string         `json:"id"`
	Category                string         `json:"category"`
	Status                  string         `json:"status"`
	Severity                string         `json:"severity"`
	Confidence              float64        `json:"confidence"`
	Rank                    int            `json:"rank,omitempty"`
	ImportanceScore         float64        `json:"importance_score,omitempty"`
	HeuristicImprovementPct float64        `json:"heuristic_improvement_pct,omitempty"`
	PipelineStage           string         `json:"pipeline_stage,omitempty"`
	TechnicalExplanation    string         `json:"technical_explanation,omitempty"`
	ImpactExplanation       string         `json:"impact_explanation,omitempty"`
	Summary                 string         `json:"summary"`
	Evidence                []EvidenceItem `json:"evidence,omitempty"`
}

type EvidenceItem struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Note   string  `json:"note,omitempty"`
}
