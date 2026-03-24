package model

const WorkloadProfileSchemaVersion = "workload-profile/v1"

const (
	WorkloadProfileSourceDefault   = "default"
	WorkloadProfileSourceUserInput = "user_input"
)

const WorkloadObjectiveUnknown = "unknown"

const (
	ServingPatternUnknown      = "unknown"
	ServingPatternRealtimeChat = "realtime_chat"
	ServingPatternOfflineBatch = "offline_batch"
	ServingPatternMixed        = "mixed"
)

const (
	TaskPatternUnknown    = "unknown"
	TaskPatternSingleTask = "single_task"
	TaskPatternMultiTask  = "multi_task"
	TaskPatternMixed      = "mixed"
)

const (
	WorkloadProfileReuseUnknown = "unknown"
	WorkloadProfileReuseHigh    = "high"
	WorkloadProfileReuseLow     = "low"
)

const (
	WorkloadProfilePresetChatbot     = "chatbot"
	WorkloadProfilePresetBatchSingle = "batch_single_task"
	WorkloadProfilePresetBatchMulti  = "batch_multi_task"
	WorkloadProfilePresetMixed       = "mixed"
	WorkloadProfilePresetCustom      = "custom"
)

type WorkloadProfile struct {
	SchemaVersion  string `json:"schema_version"`
	Source         string `json:"source"`
	Preset         string `json:"preset,omitempty"`
	ServingPattern string `json:"serving_pattern"`
	TaskPattern    string `json:"task_pattern"`
	Objective      string `json:"objective"`
	PrefixReuse    string `json:"prefix_reuse"`
	MediaReuse     string `json:"media_reuse"`
	Notes          string `json:"notes,omitempty"`
}

type ObservedWorkloadProfile struct {
	ServingPattern string                    `json:"serving_pattern"`
	TaskPattern    string                    `json:"task_pattern"`
	Objective      string                    `json:"objective"`
	PrefixReuse    string                    `json:"prefix_reuse"`
	MediaReuse     string                    `json:"media_reuse"`
	Confidence     WorkloadProfileConfidence `json:"confidence,omitempty"`
	Notes          []string                  `json:"notes,omitempty"`
}

type WorkloadProfileConfidence struct {
	ServingPattern float64 `json:"serving_pattern,omitempty"`
	TaskPattern    float64 `json:"task_pattern,omitempty"`
	Objective      float64 `json:"objective,omitempty"`
	PrefixReuse    float64 `json:"prefix_reuse,omitempty"`
	MediaReuse     float64 `json:"media_reuse,omitempty"`
}

type WorkloadProfileAlignment struct {
	Fields []WorkloadProfileAlignmentField `json:"fields,omitempty"`
}

type WorkloadProfileAlignmentField struct {
	Field      string  `json:"field"`
	Declared   string  `json:"declared,omitempty"`
	Observed   string  `json:"observed,omitempty"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence,omitempty"`
	Note       string  `json:"note,omitempty"`
}
