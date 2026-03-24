package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type toolInstallSpec struct {
	Name                       string
	LookupNames                []string
	APTPackages                [][]string
	APTPackagePrefixes         []string
	DNFPackages                [][]string
	YUMPackages                [][]string
	ZypperPackages             [][]string
	PacmanPackages             [][]string
	FallbackCommands           [][]string
	PrivilegedFallbackCommands [][]string
}

type packageManager string

const (
	pkgApt    packageManager = "apt"
	pkgDnf    packageManager = "dnf"
	pkgYum    packageManager = "yum"
	pkgZypper packageManager = "zypper"
	pkgPacman packageManager = "pacman"
)

var (
	aptUpdateOnce sync.Once
	aptUpdateErr  error
)

func resolveOrInstallTool(ctx context.Context, explicitPath string, spec toolInstallSpec) (string, error) {
	explicitPath = strings.TrimSpace(explicitPath)
	debugf("resolve tool %q explicitPath=%q", spec.Name, explicitPath)
	if explicitPath != "" {
		return resolveExplicitBinary(explicitPath)
	}
	if path := firstExistingBinary(spec.LookupNames); path != "" {
		debugf("tool %q found in PATH: %s", spec.Name, path)
		return path, nil
	}
	debugf("tool %q not found; attempting auto-install", spec.Name)
	if err := autoInstallTool(ctx, spec); err != nil {
		if path := firstExistingBinary(spec.LookupNames); path != "" {
			debugf("tool %q became available after install despite error: %s", spec.Name, path)
			return path, nil
		}
		return "", err
	}
	if path := firstExistingBinary(spec.LookupNames); path != "" {
		debugf("tool %q installed successfully: %s", spec.Name, path)
		return path, nil
	}
	return "", fmt.Errorf("%s binary still not found after install", spec.Name)
}

func resolveExplicitBinary(pathOrCommand string) (string, error) {
	pathOrCommand = strings.TrimSpace(pathOrCommand)
	if pathOrCommand == "" {
		return "", errors.New("empty binary path")
	}
	if strings.Contains(pathOrCommand, "/") {
		if _, err := os.Stat(pathOrCommand); err != nil {
			return "", fmt.Errorf("binary path %q not found: %w", pathOrCommand, err)
		}
		return pathOrCommand, nil
	}
	resolved, err := exec.LookPath(pathOrCommand)
	if err != nil {
		return "", fmt.Errorf("binary %q not found in PATH", pathOrCommand)
	}
	return resolved, nil
}

func firstExistingBinary(candidates []string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "/") {
			if strings.ContainsAny(candidate, "*?[") {
				matches, err := filepath.Glob(candidate)
				if err != nil {
					continue
				}
				sort.Strings(matches)
				for _, match := range matches {
					info, err := os.Stat(match)
					if err == nil && !info.IsDir() {
						return match
					}
				}
				continue
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
			continue
		}
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return resolved
		}
		for _, userPath := range userScopedBinaryCandidates(candidate) {
			if _, err := os.Stat(userPath); err == nil {
				return userPath
			}
		}
	}
	return ""
}

func userScopedBinaryCandidates(name string) []string {
	if strings.Contains(name, "/") || strings.TrimSpace(name) == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		fmt.Sprintf("%s/.local/bin/%s", home, name),
		fmt.Sprintf("%s/go/bin/%s", home, name),
	}
}

func autoInstallTool(ctx context.Context, spec toolInstallSpec) error {
	mgr := detectPackageManager()
	debugf("auto-install %q with package manager=%q", spec.Name, mgr)
	var installErrs []string

	tryPackageSets := func(sets [][]string, manager packageManager) bool {
		for _, pkgSet := range sets {
			if len(pkgSet) == 0 {
				continue
			}
			if err := installWithPackageManager(ctx, manager, pkgSet); err == nil {
				debugf("package install for %q succeeded with %q packages=%v", spec.Name, manager, pkgSet)
				if firstExistingBinary(spec.LookupNames) != "" {
					return true
				}
			} else {
				debugf("package install for %q failed with %q packages=%v err=%v", spec.Name, manager, pkgSet, err)
				installErrs = append(installErrs, err.Error())
			}
		}
		return false
	}

	switch mgr {
	case pkgApt:
		if tryPackageSets(spec.APTPackages, mgr) {
			return nil
		}
		for _, prefix := range spec.APTPackagePrefixes {
			prefix = strings.TrimSpace(prefix)
			if prefix == "" {
				continue
			}
			if err := installLatestAptPackageByPrefix(ctx, prefix); err == nil {
				debugf("package install for %q succeeded with apt prefix=%q", spec.Name, prefix)
				if firstExistingBinary(spec.LookupNames) != "" {
					return nil
				}
			} else {
				debugf("package install for %q failed with apt prefix=%q err=%v", spec.Name, prefix, err)
				installErrs = append(installErrs, err.Error())
			}
		}
	case pkgDnf:
		if tryPackageSets(spec.DNFPackages, mgr) {
			return nil
		}
	case pkgYum:
		if tryPackageSets(spec.YUMPackages, mgr) {
			return nil
		}
	case pkgZypper:
		if tryPackageSets(spec.ZypperPackages, mgr) {
			return nil
		}
	case pkgPacman:
		if tryPackageSets(spec.PacmanPackages, mgr) {
			return nil
		}
	default:
		installErrs = append(installErrs, "no supported package manager detected")
	}

	for _, command := range spec.FallbackCommands {
		if len(command) == 0 {
			continue
		}
		if _, err := runCommandCapture(ctx, 4*60, command[0], command[1:]...); err == nil {
			debugf("fallback install command succeeded for %q: %s", spec.Name, strings.Join(command, " "))
			if firstExistingBinary(spec.LookupNames) != "" {
				return nil
			}
		} else {
			debugf("fallback install command failed for %q: %s err=%v", spec.Name, strings.Join(command, " "), err)
			installErrs = append(installErrs, fmt.Sprintf("fallback install %q failed: %v", strings.Join(command, " "), err))
		}
	}
	for _, command := range spec.PrivilegedFallbackCommands {
		if len(command) == 0 {
			continue
		}
		if _, err := runPrivilegedCommandCapture(ctx, 8*60, command[0], command[1:]...); err == nil {
			debugf("privileged fallback install command succeeded for %q: %s", spec.Name, strings.Join(command, " "))
			if firstExistingBinary(spec.LookupNames) != "" {
				return nil
			}
		} else {
			debugf("privileged fallback install command failed for %q: %s err=%v", spec.Name, strings.Join(command, " "), err)
			installErrs = append(installErrs, fmt.Sprintf("privileged fallback install %q failed: %v", strings.Join(command, " "), err))
		}
	}

	if len(installErrs) == 0 {
		return fmt.Errorf("failed to install %s", spec.Name)
	}
	return fmt.Errorf("failed to install %s: %s", spec.Name, strings.Join(installErrs, "; "))
}

func installWithPackageManager(ctx context.Context, mgr packageManager, packages []string) error {
	if len(packages) == 0 {
		return errors.New("no packages provided")
	}
	switch mgr {
	case pkgApt:
		if err := ensureAptUpdated(ctx); err != nil {
			return err
		}
		out, err := runPrivilegedCommandCapture(ctx, 6*60, "apt-get", append([]string{"install", "-y"}, packages...)...)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return nil
	case pkgDnf:
		out, err := runPrivilegedCommandCapture(ctx, 6*60, "dnf", append([]string{"install", "-y"}, packages...)...)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return nil
	case pkgYum:
		out, err := runPrivilegedCommandCapture(ctx, 6*60, "yum", append([]string{"install", "-y"}, packages...)...)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return nil
	case pkgZypper:
		out, err := runPrivilegedCommandCapture(ctx, 6*60, "zypper", append([]string{"--non-interactive", "install"}, packages...)...)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return nil
	case pkgPacman:
		out, err := runPrivilegedCommandCapture(ctx, 6*60, "pacman", append([]string{"-Sy", "--noconfirm"}, packages...)...)
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return nil
	default:
		return fmt.Errorf("unsupported package manager %q", mgr)
	}
}

func installLatestAptPackageByPrefix(ctx context.Context, prefix string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return errors.New("empty apt package prefix")
	}

	searchExpr := "^" + prefix
	out, err := runCommandCapture(ctx, 60, "apt-cache", "search", searchExpr)
	if err != nil {
		return fmt.Errorf("apt-cache search %q failed: %w", searchExpr, err)
	}
	latest := latestAptPackageByPrefix(out, prefix)
	if latest == "" {
		return fmt.Errorf("no apt package found for prefix %q", prefix)
	}
	if err := installWithPackageManager(ctx, pkgApt, []string{latest}); err != nil {
		return fmt.Errorf("install apt package %q failed: %w", latest, err)
	}
	return nil
}

func latestAptPackageByPrefix(searchOutput, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	var (
		bestName    string
		bestVersion string
	)
	for _, line := range strings.Split(searchOutput, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		versionSuffix := strings.TrimPrefix(name, prefix)
		if !startsWithDigit(versionSuffix) {
			continue
		}
		if bestName == "" || compareVersionLike(versionSuffix, bestVersion) > 0 {
			bestName = name
			bestVersion = versionSuffix
		}
	}
	return bestName
}

func compareVersionLike(a, b string) int {
	aParts := splitVersionParts(a)
	bParts := splitVersionParts(b)
	limit := len(aParts)
	if len(bParts) < limit {
		limit = len(bParts)
	}
	for i := 0; i < limit; i++ {
		aPart := aParts[i]
		bPart := bParts[i]
		aNumeric := isNumericPart(aPart)
		bNumeric := isNumericPart(bPart)
		if aNumeric && bNumeric {
			aPart = trimLeadingZeros(aPart)
			bPart = trimLeadingZeros(bPart)
			if len(aPart) != len(bPart) {
				if len(aPart) > len(bPart) {
					return 1
				}
				return -1
			}
			if aPart != bPart {
				if aPart > bPart {
					return 1
				}
				return -1
			}
			continue
		}
		if aNumeric && !bNumeric {
			return 1
		}
		if !aNumeric && bNumeric {
			return -1
		}
		if aPart != bPart {
			if aPart > bPart {
				return 1
			}
			return -1
		}
	}
	if len(aParts) != len(bParts) {
		if len(aParts) > len(bParts) {
			return 1
		}
		return -1
	}
	return 0
}

func splitVersionParts(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	segments := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '.', '-', '_', '+':
			return true
		default:
			return false
		}
	})
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		start := 0
		for i := 1; i < len(segment); i++ {
			if isDigit(segment[i-1]) != isDigit(segment[i]) {
				parts = append(parts, segment[start:i])
				start = i
			}
		}
		parts = append(parts, segment[start:])
	}
	return parts
}

func startsWithDigit(value string) bool {
	if value == "" {
		return false
	}
	return isDigit(value[0])
}

func isNumericPart(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isDigit(value[i]) {
			return false
		}
	}
	return true
}

func trimLeadingZeros(value string) string {
	trimmed := strings.TrimLeft(value, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func ensureAptUpdated(ctx context.Context) error {
	aptUpdateOnce.Do(func() {
		_, aptUpdateErr = runPrivilegedCommandCapture(ctx, 6*60, "apt-get", "update")
	})
	return aptUpdateErr
}

func detectPackageManager() packageManager {
	if _, err := exec.LookPath("apt-get"); err == nil {
		return pkgApt
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return pkgDnf
	}
	if _, err := exec.LookPath("yum"); err == nil {
		return pkgYum
	}
	if _, err := exec.LookPath("zypper"); err == nil {
		return pkgZypper
	}
	if _, err := exec.LookPath("pacman"); err == nil {
		return pkgPacman
	}
	return ""
}
