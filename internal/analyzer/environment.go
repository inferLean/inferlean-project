package analyzer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

func (defaultProbe) Collect(ctx context.Context) (model.OSInformation, model.GPUInformation, []string) {
	osInfo := model.OSInformation{
		OSType:       runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPU: model.CPUInformation{
			Model:         "unknown",
			PhysicalCores: runtime.NumCPU(),
			LogicalCores:  runtime.NumCPU(),
		},
	}
	gpuInfo := model.GPUInformation{}
	var warnings []string

	if version, err := readOSVersion(); err == nil {
		osInfo.OSVersion = version
	} else {
		warnings = append(warnings, "os version unavailable: "+err.Error())
	}
	if distro, err := readOSRelease(); err == nil {
		osInfo.Distribution = distro
	} else {
		warnings = append(warnings, "distribution unavailable: "+err.Error())
	}

	if diskTotal, diskAvail, err := probeDisk(); err == nil {
		osInfo.DiskSizeBytes = diskTotal
		osInfo.AvailableDiskBytes = diskAvail
	} else {
		warnings = append(warnings, "disk stats unavailable: "+err.Error())
	}

	if memTotal, memAvail, err := probeMemory(); err == nil {
		osInfo.MemorySizeBytes = memTotal
		osInfo.AvailableMemoryBytes = memAvail
	} else {
		warnings = append(warnings, "memory stats unavailable: "+err.Error())
	}

	if cpuInfo, err := probeCPUInfo(); err == nil {
		osInfo.CPU = cpuInfo
	} else {
		warnings = append(warnings, "cpu info unavailable: "+err.Error())
	}

	if util, err := probeCPUUtilization(ctx); err == nil {
		osInfo.AverageCPUUtilizationPct = util
	} else {
		warnings = append(warnings, "cpu utilization unavailable: "+err.Error())
	}

	if gpu, err := probeNvidiaGPU(ctx); err == nil {
		gpuInfo = gpu
	} else {
		warnings = append(warnings, "gpu probe unavailable: "+err.Error())
	}

	return osInfo, gpuInfo, warnings
}

func readOSVersion() (string, error) {
	if version, err := readFirstNonEmpty("/proc/sys/kernel/osrelease", "/proc/version"); err == nil {
		return version, nil
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				return v, nil
			}
		}
	}
	return "", errors.New("unable to determine OS version")
}

func probeMemory() (uint64, uint64, error) {
	if total, available, err := readLinuxMemInfo(); err == nil {
		return total, available, nil
	}
	if runtime.GOOS == "darwin" {
		return readDarwinMemory()
	}
	return 0, 0, errors.New("unsupported platform for memory probing")
}

func readLinuxMemInfo() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var totalKB uint64
	var availableKB uint64
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			totalKB = value
		case "MemAvailable":
			availableKB = value
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if totalKB == 0 {
		return 0, 0, errors.New("MemTotal not found")
	}
	if availableKB == 0 {
		availableKB = totalKB
	}
	return totalKB * 1024, availableKB * 1024, nil
}

func readDarwinMemory() (uint64, uint64, error) {
	totalOut, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, 0, err
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(totalOut)), 10, 64)
	if err != nil {
		return 0, 0, err
	}

	vmOut, err := exec.Command("vm_stat").Output()
	if err != nil {
		return total, 0, err
	}
	available, err := parseDarwinAvailableMemory(string(vmOut))
	if err != nil {
		return total, 0, err
	}
	return total, available, nil
}

func parseDarwinAvailableMemory(vmStat string) (uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(vmStat))
	pageSize := uint64(4096)
	var availablePages uint64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics:") {
			if n, err := extractFirstUint(line); err == nil && n > 0 {
				pageSize = n
			}
			continue
		}
		if strings.HasPrefix(line, "Pages free:") || strings.HasPrefix(line, "Pages inactive:") || strings.HasPrefix(line, "Pages speculative:") {
			n, err := extractFirstUint(line)
			if err != nil {
				continue
			}
			availablePages += n
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if availablePages == 0 {
		return 0, errors.New("vm_stat did not expose available pages")
	}
	return availablePages * pageSize, nil
}

func probeCPUInfo() (model.CPUInformation, error) {
	if info, err := readLinuxCPUInfo(); err == nil {
		return info, nil
	}
	if runtime.GOOS == "darwin" {
		return readDarwinCPUInfo()
	}
	return model.CPUInformation{Model: "unknown", PhysicalCores: runtime.NumCPU(), LogicalCores: runtime.NumCPU()}, nil
}

func readLinuxCPUInfo() (model.CPUInformation, error) {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return model.CPUInformation{}, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	modelName := ""
	logical := 0
	cpuCores := 0
	physicalIDs := map[string]struct{}{}
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "model name":
			if modelName == "" {
				modelName = value
			}
		case "processor":
			logical++
		case "cpu cores":
			if cpuCores == 0 {
				if parsed, err := strconv.Atoi(value); err == nil {
					cpuCores = parsed
				}
			}
		case "physical id":
			if value != "" {
				physicalIDs[value] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return model.CPUInformation{}, err
	}
	if logical == 0 {
		logical = runtime.NumCPU()
	}
	physical := cpuCores
	if physical > 0 && len(physicalIDs) > 0 {
		physical = cpuCores * len(physicalIDs)
	}
	if physical == 0 {
		physical = logical
	}
	if modelName == "" {
		modelName = "unknown"
	}
	return model.CPUInformation{Model: modelName, PhysicalCores: physical, LogicalCores: logical}, nil
}

func readDarwinCPUInfo() (model.CPUInformation, error) {
	modelName := readCommandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	logical := readCommandInt("sysctl", "-n", "hw.logicalcpu")
	physical := readCommandInt("sysctl", "-n", "hw.physicalcpu")
	if logical <= 0 {
		logical = runtime.NumCPU()
	}
	if physical <= 0 {
		physical = logical
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = "unknown"
	}
	return model.CPUInformation{Model: strings.TrimSpace(modelName), PhysicalCores: physical, LogicalCores: logical}, nil
}

func probeCPUUtilization(ctx context.Context) (float64, error) {
	if util, err := readLinuxCPUUtilization(); err == nil {
		return util, nil
	}
	return readPSCPUUtilization(ctx)
}

func readLinuxCPUUtilization() (float64, error) {
	first, err := readLinuxCPUTimes()
	if err != nil {
		return 0, err
	}
	time.Sleep(250 * time.Millisecond)
	second, err := readLinuxCPUTimes()
	if err != nil {
		return 0, err
	}
	deltaTotal := second.total - first.total
	deltaIdle := second.idle - first.idle
	if deltaTotal == 0 {
		return 0, errors.New("cpu sample delta is zero")
	}
	busy := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	return clampFloat(busy, 0, 100), nil
}

type linuxCPUTimes struct {
	total uint64
	idle  uint64
}

func readLinuxCPUTimes() (linuxCPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return linuxCPUTimes{}, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return linuxCPUTimes{}, errors.New("malformed /proc/stat cpu line")
		}
		var total uint64
		values := make([]uint64, 0, len(fields)-1)
		for _, field := range fields[1:] {
			v, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return linuxCPUTimes{}, err
			}
			values = append(values, v)
			total += v
		}
		idle := values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		return linuxCPUTimes{total: total, idle: idle}, nil
	}
	if err := scanner.Err(); err != nil {
		return linuxCPUTimes{}, err
	}
	return linuxCPUTimes{}, errors.New("cpu line not found in /proc/stat")
}

func readPSCPUUtilization(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "%cpu=").Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(out), "\n")
	var sum float64
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		}
		sum += v
	}
	logical := runtime.NumCPU()
	if logical <= 0 {
		logical = 1
	}
	util := sum / float64(logical)
	return clampFloat(util, 0, 100), nil
}

func probeNvidiaGPU(ctx context.Context) (model.GPUInformation, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return model.GPUInformation{}, err
	}
	cmd := exec.CommandContext(ctx, path, "--query-gpu=name,memory.total,utilization.gpu", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return model.GPUInformation{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return model.GPUInformation{}, errors.New("no GPU rows returned")
	}

	models := []string{}
	var totalVRAMMB uint64
	var utilizationTotal float64
	count := 0

	for _, line := range lines {
		fields := splitAndTrim(line, ",")
		if len(fields) == 0 {
			continue
		}
		count++
		models = appendUnique(models, fields[0])
		if len(fields) > 1 {
			if memoryMB, err := parseUint(fields[1]); err == nil {
				totalVRAMMB += memoryMB
			}
		}
		if len(fields) > 2 {
			if util, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64); err == nil {
				utilizationTotal += util
			}
		}
	}
	if count == 0 {
		return model.GPUInformation{}, errors.New("no parseable GPU rows")
	}

	gpu := model.GPUInformation{
		GPUModel:      strings.Join(models, ", "),
		Company:       "NVIDIA",
		VRAMSizeBytes: totalVRAMMB * 1024 * 1024,
	}
	gpu.UtilizationPct = utilizationTotal / float64(count)
	return gpu, nil
}

func readFirstNonEmpty(paths ...string) (string, error) {
	var lastErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		value := strings.TrimSpace(string(data))
		if value != "" {
			if idx := strings.IndexByte(value, '\n'); idx >= 0 {
				value = strings.TrimSpace(value[:idx])
			}
			return value, nil
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", os.ErrNotExist
}

func readOSRelease() (string, error) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var prettyName, id string
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "PRETTY_NAME":
			prettyName = value
		case "ID":
			id = value
		}
	}
	if prettyName != "" {
		return prettyName, nil
	}
	if id != "" {
		return id, nil
	}
	return "", os.ErrNotExist
}

func splitAndTrim(value string, sep string) []string {
	raw := strings.Split(value, sep)
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseUint(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "MiB")
	value = strings.TrimSuffix(value, "MB")
	value = strings.TrimSpace(value)
	return strconv.ParseUint(value, 10, 64)
}

func readCommandOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readCommandInt(name string, args ...string) int {
	text := readCommandOutput(name, args...)
	if text == "" {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0
	}
	return value
}

func extractFirstUint(input string) (uint64, error) {
	var b strings.Builder
	for _, r := range input {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return 0, fmt.Errorf("no integer found in %q", input)
	}
	return strconv.ParseUint(b.String(), 10, 64)
}
