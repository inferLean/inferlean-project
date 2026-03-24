package analyzer

import (
	"fmt"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

func loadWorkloadProfile(path string) (workloadProfileSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return workloadProfileSnapshot{
			Profile: defaultWorkloadProfile(model.WorkloadProfileSourceDefault),
		}, nil
	}

	raw, format, err := readStructuredFile(path)
	if err != nil {
		return workloadProfileSnapshot{}, err
	}
	profile, err := normalizeWorkloadProfile(raw, model.WorkloadProfileSourceUserInput)
	if err != nil {
		return workloadProfileSnapshot{}, err
	}
	return workloadProfileSnapshot{
		Path:    path,
		Format:  format,
		Profile: profile,
	}, nil
}

func LoadWorkloadProfileFile(path string) (*model.WorkloadProfile, error) {
	snapshot, err := loadWorkloadProfile(path)
	if err != nil {
		return nil, err
	}
	return snapshot.Profile, nil
}

func defaultWorkloadProfile(source string) *model.WorkloadProfile {
	return &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         source,
		ServingPattern: model.ServingPatternUnknown,
		TaskPattern:    model.TaskPatternUnknown,
		Objective:      string(BalancedIntent),
		PrefixReuse:    model.WorkloadProfileReuseUnknown,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}
}

func normalizeWorkloadProfile(raw map[string]any, source string) (*model.WorkloadProfile, error) {
	profile := defaultWorkloadProfile(source)
	if raw == nil {
		return profile, nil
	}

	allowedKeys := map[string]struct{}{
		"schema_version":  {},
		"source":          {},
		"preset":          {},
		"serving_pattern": {},
		"task_pattern":    {},
		"objective":       {},
		"prefix_reuse":    {},
		"media_reuse":     {},
		"notes":           {},
	}
	for key := range raw {
		if _, ok := allowedKeys[key]; !ok {
			return nil, fmt.Errorf("workload profile contains unsupported key %q", key)
		}
	}

	if schemaVersion, ok, err := workloadProfileString(raw, "schema_version"); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(schemaVersion) != model.WorkloadProfileSchemaVersion {
		return nil, fmt.Errorf("workload profile schema_version must be %q", model.WorkloadProfileSchemaVersion)
	}

	if declaredSource, ok, err := workloadProfileString(raw, "source"); err != nil {
		return nil, err
	} else if ok {
		switch strings.TrimSpace(declaredSource) {
		case "", model.WorkloadProfileSourceDefault, model.WorkloadProfileSourceUserInput:
		default:
			return nil, fmt.Errorf("invalid workload profile source %q", declaredSource)
		}
	}

	if preset, ok, err := workloadProfileString(raw, "preset"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeWorkloadPreset(preset)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile preset %q", preset)
		}
		profile.Preset = normalized
		applyWorkloadPreset(profile, normalized)
	}

	if servingPattern, ok, err := workloadProfileString(raw, "serving_pattern"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeServingPattern(servingPattern)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile serving_pattern %q", servingPattern)
		}
		profile.ServingPattern = normalized
	}

	if taskPattern, ok, err := workloadProfileString(raw, "task_pattern"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeTaskPattern(taskPattern)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile task_pattern %q", taskPattern)
		}
		profile.TaskPattern = normalized
	}

	if objective, ok, err := workloadProfileString(raw, "objective"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeWorkloadProfileObjective(objective)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile objective %q", objective)
		}
		profile.Objective = normalized
	}

	if prefixReuse, ok, err := workloadProfileString(raw, "prefix_reuse"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeReuseLevel(prefixReuse)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile prefix_reuse %q", prefixReuse)
		}
		profile.PrefixReuse = normalized
	}

	if mediaReuse, ok, err := workloadProfileString(raw, "media_reuse"); err != nil {
		return nil, err
	} else if ok {
		normalized, ok := normalizeReuseLevel(mediaReuse)
		if !ok {
			return nil, fmt.Errorf("invalid workload profile media_reuse %q", mediaReuse)
		}
		profile.MediaReuse = normalized
	}

	if notes, ok, err := workloadProfileString(raw, "notes"); err != nil {
		return nil, err
	} else if ok {
		profile.Notes = strings.TrimSpace(notes)
	}

	return profile, nil
}

func workloadProfileString(raw map[string]any, key string) (string, bool, error) {
	value, ok := raw[key]
	if !ok {
		return "", false, nil
	}
	if value == nil {
		return "", false, nil
	}
	typed, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("workload profile field %q must be a string", key)
	}
	return strings.TrimSpace(typed), true, nil
}

func normalizeServingPattern(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case model.ServingPatternRealtimeChat:
		return model.ServingPatternRealtimeChat, true
	case model.ServingPatternOfflineBatch:
		return model.ServingPatternOfflineBatch, true
	case model.ServingPatternMixed:
		return model.ServingPatternMixed, true
	case model.ServingPatternUnknown, "":
		return model.ServingPatternUnknown, true
	default:
		return "", false
	}
}

func normalizeTaskPattern(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case model.TaskPatternSingleTask:
		return model.TaskPatternSingleTask, true
	case model.TaskPatternMultiTask:
		return model.TaskPatternMultiTask, true
	case model.TaskPatternMixed:
		return model.TaskPatternMixed, true
	case model.TaskPatternUnknown, "":
		return model.TaskPatternUnknown, true
	default:
		return "", false
	}
}

func normalizeWorkloadProfileObjective(value string) (string, bool) {
	switch normalizeWorkloadIntent(WorkloadIntent(value)) {
	case ThroughputFirstIntent:
		return string(ThroughputFirstIntent), true
	case LatencyFirstIntent:
		return string(LatencyFirstIntent), true
	case BalancedIntent:
		if strings.TrimSpace(value) == "" || strings.EqualFold(strings.TrimSpace(value), string(BalancedIntent)) {
			return string(BalancedIntent), true
		}
		return "", false
	default:
		return "", false
	}
}

func normalizeReuseLevel(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case model.WorkloadProfileReuseHigh:
		return model.WorkloadProfileReuseHigh, true
	case model.WorkloadProfileReuseLow:
		return model.WorkloadProfileReuseLow, true
	case model.WorkloadProfileReuseUnknown, "":
		return model.WorkloadProfileReuseUnknown, true
	default:
		return "", false
	}
}

func normalizeWorkloadPreset(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case model.WorkloadProfilePresetChatbot:
		return model.WorkloadProfilePresetChatbot, true
	case model.WorkloadProfilePresetBatchSingle:
		return model.WorkloadProfilePresetBatchSingle, true
	case model.WorkloadProfilePresetBatchMulti:
		return model.WorkloadProfilePresetBatchMulti, true
	case model.WorkloadProfilePresetMixed:
		return model.WorkloadProfilePresetMixed, true
	case model.WorkloadProfilePresetCustom:
		return model.WorkloadProfilePresetCustom, true
	default:
		return "", false
	}
}

func applyWorkloadPreset(profile *model.WorkloadProfile, preset string) {
	if profile == nil {
		return
	}
	switch preset {
	case model.WorkloadProfilePresetChatbot:
		profile.ServingPattern = model.ServingPatternRealtimeChat
		profile.TaskPattern = model.TaskPatternMixed
		profile.Objective = string(LatencyFirstIntent)
		profile.PrefixReuse = model.WorkloadProfileReuseHigh
		profile.MediaReuse = model.WorkloadProfileReuseUnknown
	case model.WorkloadProfilePresetBatchSingle:
		profile.ServingPattern = model.ServingPatternOfflineBatch
		profile.TaskPattern = model.TaskPatternSingleTask
		profile.Objective = string(ThroughputFirstIntent)
		profile.PrefixReuse = model.WorkloadProfileReuseHigh
		profile.MediaReuse = model.WorkloadProfileReuseUnknown
	case model.WorkloadProfilePresetBatchMulti:
		profile.ServingPattern = model.ServingPatternOfflineBatch
		profile.TaskPattern = model.TaskPatternMultiTask
		profile.Objective = string(ThroughputFirstIntent)
		profile.PrefixReuse = model.WorkloadProfileReuseLow
		profile.MediaReuse = model.WorkloadProfileReuseUnknown
	case model.WorkloadProfilePresetMixed:
		profile.ServingPattern = model.ServingPatternMixed
		profile.TaskPattern = model.TaskPatternMixed
		profile.Objective = string(BalancedIntent)
		profile.PrefixReuse = model.WorkloadProfileReuseUnknown
		profile.MediaReuse = model.WorkloadProfileReuseUnknown
	case model.WorkloadProfilePresetCustom:
	}
}

func resolveSummaryIntent(profile *model.WorkloadProfile, fallback WorkloadIntent) WorkloadIntent {
	if profile != nil && profile.Source == model.WorkloadProfileSourceUserInput {
		return normalizeWorkloadIntent(WorkloadIntent(profile.Objective))
	}
	return normalizeWorkloadIntent(fallback)
}
