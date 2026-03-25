package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func discoverRuntimeVLLMConfig(ctx context.Context) map[string]any {
	pid := detectVLLMPID(ctx)
	if pid <= 0 {
		return nil
	}
	args, err := readProcessArgs(pid)
	if err != nil {
		debugf("runtime config discovery: failed reading process args for pid %d: %v", pid, err)
		return nil
	}
	config := parseRuntimeConfigFromArgs(args)
	if len(config) > 0 {
		debugf("runtime config discovery: extracted %d settings from pid %d", len(config), pid)
	}
	return config
}

func readProcessArgs(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return nil, err
	}
	raw := strings.Split(string(data), "\x00")
	args := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			args = append(args, item)
		}
	}
	return args, nil
}

func parseRuntimeConfigFromArgs(args []string) map[string]any {
	if len(args) == 0 {
		return nil
	}
	config := map[string]any{}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "serve" && i+1 < len(args) {
			modelArg := strings.TrimSpace(args[i+1])
			if modelArg != "" && !strings.HasPrefix(modelArg, "-") {
				config["model_name"] = modelArg
			}
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}

		flagName := ""
		rawValue := ""
		if key, value, ok := strings.Cut(arg, "="); ok {
			flagName = key
			rawValue = value
		} else {
			flagName = arg
			if i+1 < len(args) && !strings.HasPrefix(strings.TrimSpace(args[i+1]), "--") {
				rawValue = args[i+1]
				i++
			} else {
				rawValue = "true"
			}
		}

		key := normalizeRuntimeConfigKey(strings.TrimPrefix(flagName, "--"))
		if key == "" {
			continue
		}
		config[key] = parseRuntimeConfigValue(rawValue)
	}

	if model, ok := config["model"]; ok {
		if _, exists := config["model_name"]; !exists {
			config["model_name"] = model
		}
	}
	if served, ok := config["served_model_name"]; ok {
		if _, exists := config["model_name"]; !exists {
			config["model_name"] = served
		}
	}

	return config
}

func normalizeRuntimeConfigKey(key string) string {
	key = strings.TrimSpace(strings.ReplaceAll(key, "-", "_"))
	switch key {
	case "", "help":
		return ""
	default:
		return key
	}
}

func parseRuntimeConfigValue(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	case "null", "none":
		return nil
	}
	if strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return decoded
		}
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	return value
}
