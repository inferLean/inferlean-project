package main

import (
	"fmt"
	"strings"
)

func resolveWorkloadProfilePath(workloadProfilePath, intentPath string) (string, error) {
	cleanWorkloadProfilePath := toAbsIfPresent(strings.TrimSpace(workloadProfilePath))
	cleanIntentPath := toAbsIfPresent(strings.TrimSpace(intentPath))
	switch {
	case cleanWorkloadProfilePath == "":
		return cleanIntentPath, nil
	case cleanIntentPath == "":
		return cleanWorkloadProfilePath, nil
	case cleanWorkloadProfilePath == cleanIntentPath:
		return cleanWorkloadProfilePath, nil
	default:
		return "", fmt.Errorf("use either --workload-profile-file or --intent-file, not both")
	}
}
