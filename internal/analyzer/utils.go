package analyzer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type yamlLine struct {
	indent int
	text   string
}

func readStructuredFile(path string) (map[string]any, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	format := strings.ToLower(filepath.Ext(path))
	switch format {
	case ".json":
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, "json", err
		}
		return out, "json", nil
	case ".yaml", ".yml":
		out, err := parseYAMLDocument(data)
		if err != nil {
			return nil, "yaml", err
		}
		return out, "yaml", nil
	default:
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, "json", err
			}
			return out, "json", nil
		}
		out, err := parseYAMLDocument(data)
		if err != nil {
			return nil, "", err
		}
		return out, "yaml", nil
	}
}

func parseYAMLDocument(data []byte) (map[string]any, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var lines []yamlLine
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r\n\t ")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := 0
		for i := 0; i < len(raw); i++ {
			switch raw[i] {
			case ' ':
				indent++
			case '\t':
				indent += 2
			default:
				i = len(raw)
			}
		}
		lines = append(lines, yamlLine{indent: indent, text: strings.TrimSpace(raw)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	value, next, err := parseYAMLBlock(lines, 0, lines[0].indent)
	if err != nil {
		return nil, err
	}
	for next < len(lines) {
		if strings.TrimSpace(lines[next].text) != "" {
			return nil, fmt.Errorf("unexpected trailing content near line %d", next+1)
		}
		next++
	}
	m, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"value": value}, nil
	}
	return m, nil
}

func parseYAMLBlock(lines []yamlLine, start int, indent int) (any, int, error) {
	if start >= len(lines) {
		return map[string]any{}, start, nil
	}
	if lines[start].indent < indent {
		return map[string]any{}, start, nil
	}
	if strings.HasPrefix(lines[start].text, "- ") || strings.TrimSpace(lines[start].text) == "-" {
		return parseYAMLList(lines, start, indent)
	}
	return parseYAMLMap(lines, start, indent)
}

func parseYAMLMap(lines []yamlLine, start int, indent int) (map[string]any, int, error) {
	out := map[string]any{}
	i := start
	for i < len(lines) {
		if lines[i].indent < indent {
			break
		}
		if lines[i].indent > indent {
			return nil, i, fmt.Errorf("invalid indentation near line %d", i+1)
		}
		line := lines[i].text
		if strings.HasPrefix(line, "- ") || line == "-" {
			break
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil, i, fmt.Errorf("invalid yaml map entry near line %d", i+1)
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)
		if key == "" {
			return nil, i, fmt.Errorf("empty yaml key near line %d", i+1)
		}
		if rest == "" {
			if i+1 < len(lines) && lines[i+1].indent > indent {
				nextValue, nextIndex, err := parseYAMLBlock(lines, i+1, lines[i+1].indent)
				if err != nil {
					return nil, i, err
				}
				out[key] = nextValue
				i = nextIndex
				continue
			}
			out[key] = nil
			i++
			continue
		}
		out[key] = parseYAMLScalar(rest)
		i++
	}
	return out, i, nil
}

func parseYAMLList(lines []yamlLine, start int, indent int) ([]any, int, error) {
	var out []any
	i := start
	for i < len(lines) {
		if lines[i].indent < indent {
			break
		}
		if lines[i].indent > indent {
			return nil, i, fmt.Errorf("invalid list indentation near line %d", i+1)
		}
		line := strings.TrimSpace(lines[i].text)
		if !strings.HasPrefix(line, "-") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if item == "" {
			if i+1 < len(lines) && lines[i+1].indent > indent {
				nextValue, nextIndex, err := parseYAMLBlock(lines, i+1, lines[i+1].indent)
				if err != nil {
					return nil, i, err
				}
				out = append(out, nextValue)
				i = nextIndex
				continue
			}
			out = append(out, nil)
			i++
			continue
		}
		if strings.Contains(item, ":") {
			key, rest, _ := strings.Cut(item, ":")
			m := map[string]any{strings.TrimSpace(key): parseYAMLScalar(strings.TrimSpace(rest))}
			if i+1 < len(lines) && lines[i+1].indent > indent {
				nextValue, nextIndex, err := parseYAMLBlock(lines, i+1, lines[i+1].indent)
				if err != nil {
					return nil, i, err
				}
				if nextMap, ok := nextValue.(map[string]any); ok {
					for k, v := range nextMap {
						m[k] = v
					}
				}
				out = append(out, m)
				i = nextIndex
				continue
			}
			out = append(out, m)
			i++
			continue
		}
		out = append(out, parseYAMLScalar(item))
		i++
	}
	return out, i, nil
}

func parseYAMLScalar(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.EqualFold(value, "null") || value == "~" {
		return nil
	}
	if strings.EqualFold(value, "true") {
		return true
	}
	if strings.EqualFold(value, "false") {
		return false
	}
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return strings.Trim(value, "\"'")
		}
		if value[0] == '[' || value[0] == '{' {
			var v any
			if json.Unmarshal([]byte(value), &v) == nil {
				return v
			}
		}
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		if finite, ok := finiteFloat(f); ok {
			return finite
		}
	}
	return value
}

func flattenMap(input map[string]any) map[string]any {
	out := map[string]any{}
	flattenMapInto(out, "", input)
	return out
}

func flattenMapInto(dst map[string]any, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			nextPrefix := k
			if prefix != "" {
				nextPrefix = prefix + "." + k
			}
			flattenMapInto(dst, nextPrefix, typed[k])
		}
	case []any:
		for i, item := range typed {
			nextPrefix := fmt.Sprintf("%s[%d]", prefix, i)
			flattenMapInto(dst, nextPrefix, item)
		}
	default:
		if prefix != "" {
			dst[prefix] = typed
		}
	}
}

func lookupAny(flat map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if v, ok := flat[key]; ok {
			return v, true
		}
	}
	for _, key := range keys {
		for candidate, value := range flat {
			if strings.HasSuffix(candidate, "."+key) || candidate == key || strings.Contains(candidate, "["+key+"]") {
				return value, true
			}
		}
	}
	return nil, false
}

func lookupString(flat map[string]any, keys ...string) string {
	if v, ok := lookupAny(flat, keys...); ok {
		switch typed := v.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		case json.Number:
			return typed.String()
		case int64:
			return strconv.FormatInt(typed, 10)
		case int:
			return strconv.Itoa(typed)
		case float64:
			return trimFloat(typed)
		case bool:
			return strconv.FormatBool(typed)
		}
	}
	return ""
}

func coerceInt(v any) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func coerceFloat(v any) (float64, bool) {
	switch typed := v.(type) {
	case float64:
		return finiteFloat(typed)
	case float32:
		return finiteFloat(float64(typed))
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		f, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return finiteFloat(f)
	case string:
		if typed == "" {
			return 0, false
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return finiteFloat(f)
		}
	}
	return 0, false
}

func finiteFloat(v float64) (float64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func coerceBool(v any) (bool, bool) {
	switch typed := v.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y":
			return true, true
		case "false", "0", "no", "n":
			return false, true
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
	}
	return false, false
}

func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func normalizePercentOrRatio(v float64) float64 {
	if v > 1.5 {
		return v
	}
	if v >= 0 && v <= 1.5 {
		return v * 100
	}
	return v
}

func clampFloat(v, min, max float64) float64 {
	return math.Min(max, math.Max(min, v))
}

func appendUnique(values []string, next ...string) []string {
	seen := map[string]struct{}{}
	for _, v := range values {
		seen[v] = struct{}{}
	}
	for _, v := range next {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		values = append(values, v)
	}
	return values
}

func ensureDir(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
