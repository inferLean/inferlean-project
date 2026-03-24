package recommender

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

func normalizeObjective(objective Objective) Objective {
	switch Objective(strings.ToLower(strings.TrimSpace(string(objective)))) {
	case ThroughputFirstObjective:
		return ThroughputFirstObjective
	case LatencyFirstObjective:
		return LatencyFirstObjective
	default:
		return BalancedObjective
	}
}

func clampFloat(v, min, max float64) float64 {
	return math.Min(max, math.Max(min, v))
}

func flattenMap(input map[string]any) map[string]any {
	out := map[string]any{}
	flattenInto(out, "", input)
	return out
}

func flattenInto(dst map[string]any, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}
			flattenInto(dst, nextPrefix, typed[key])
		}
	case []any:
		for i, item := range typed {
			nextPrefix := fmt.Sprintf("%s[%d]", prefix, i)
			flattenInto(dst, nextPrefix, item)
		}
	default:
		if prefix != "" {
			dst[prefix] = typed
		}
	}
}

func lookupAny(flat map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := flat[key]; ok {
			return value, true
		}
	}
	for _, key := range keys {
		for candidate, value := range flat {
			if candidate == key || strings.HasSuffix(candidate, "."+key) {
				return value, true
			}
		}
	}
	return nil, false
}

func lookupString(flat map[string]any, keys ...string) string {
	value, ok := lookupAny(flat, keys...)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case fmt.Stringer:
		return typed.String()
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func lookupFloat(flat map[string]any, keys ...string) (float64, bool) {
	value, ok := lookupAny(flat, keys...)
	if !ok {
		return 0, false
	}
	return coerceFloat(value)
}

func coerceFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		v, err := typed.Float64()
		return v, err == nil
	case string:
		v, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func coerceBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		normalized := strings.TrimSpace(strings.ToLower(typed))
		switch normalized {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		default:
			return false, false
		}
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i != 0, true
		}
		if f, err := typed.Float64(); err == nil {
			return f != 0, true
		}
		return false, false
	default:
		return false, false
	}
}

func loadCorpus(path string) (*corpusDocument, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var corpus corpusDocument
	if err := json.Unmarshal(data, &corpus); err != nil {
		return nil, err
	}
	if len(corpus.Profiles) == 0 {
		return nil, nil
	}
	return &corpus, nil
}

func normalizeModelFamily(modelName string) string {
	value := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(value, "qwen 3.5"), strings.Contains(value, "qwen-3.5"), strings.Contains(value, "qwen3.5"):
		return "qwen3.5"
	case strings.Contains(value, "qwen 3"), strings.Contains(value, "qwen-3"), strings.Contains(value, "qwen3"):
		return "qwen3"
	}
	for _, sep := range []string{"/", ":", "-", " "} {
		if head, _, ok := strings.Cut(value, sep); ok && head != "" {
			return head
		}
	}
	return value
}

func normalizeHardwareClass(gpuModel string) string {
	value := strings.ToLower(strings.TrimSpace(gpuModel))
	switch {
	case strings.Contains(value, "h200"):
		return "h200"
	case strings.Contains(value, "h100"):
		return "h100"
	case strings.Contains(value, "a100"):
		return "a100"
	case strings.Contains(value, "l40s"):
		return "l40s"
	case strings.Contains(value, "l40"):
		return "l40"
	default:
		fields := strings.Fields(value)
		if len(fields) > 0 {
			return fields[0]
		}
		return value
	}
}

func pctDelta(next, base float64) float64 {
	if base == 0 {
		return 0
	}
	return ((next - base) / base) * 100
}

func numericMap(raw map[string]any) map[string]float64 {
	out := map[string]float64{}
	flat := flattenMap(raw)
	for _, key := range []string{"max_num_seqs", "max_num_batched_tokens", "tensor_parallel_size"} {
		if value, ok := lookupFloat(flat, key); ok {
			out[key] = value
		}
	}
	return out
}

func fixedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
