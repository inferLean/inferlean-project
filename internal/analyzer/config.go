package analyzer

import "strings"

func loadConfig(path string) (configSnapshot, []string, error) {
	if strings.TrimSpace(path) == "" {
		return configSnapshot{}, nil, nil
	}
	raw, format, err := readStructuredFile(path)
	if err != nil {
		return configSnapshot{}, nil, err
	}
	flat := flattenMap(raw)
	return configSnapshot{
		Path:   path,
		Format: format,
		Raw:    raw,
		Flat:   flat,
	}, nil, nil
}

func LoadConfigFile(path string) (map[string]any, error) {
	cfg, _, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	if len(cfg.Raw) == 0 {
		return nil, nil
	}
	return cfg.Raw, nil
}
