package idle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// captureCPUStat reads the first "cpu " line from /proc/stat and returns
// idle and total tick counts. RULE-IDLE-05: reads /proc/loadavg directly —
// getloadavg(3) is never used.
func captureCPUStat(procRoot string) CPUStat {
	path := procStatPath(procRoot)
	f, err := os.Open(path)
	if err != nil {
		return CPUStat{}
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		// Fields: cpu user nice system idle iowait irq softirq steal guest guest_nice
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return CPUStat{}
		}
		var total uint64
		var idle uint64
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				continue
			}
			total += v
			if i == 3 { // idle column
				idle = v
			}
			if i == 4 { // iowait — also counts as idle for our purposes
				idle += v
			}
		}
		return CPUStat{Idle: idle, Total: total}
	}
	return CPUStat{}
}

// procStatPath returns the path to /proc/stat relative to procRoot.
func procStatPath(procRoot string) string {
	if procRoot == "" {
		return "/proc/stat"
	}
	return procRoot + "/stat"
}

// captureLoadAvg reads /proc/loadavg directly (RULE-IDLE-05: no getloadavg(3)).
func captureLoadAvg(procRoot string) [3]float64 {
	path := loadAvgPath(procRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return [3]float64{}
	}
	fields := strings.Fields(string(data))
	var la [3]float64
	for i := 0; i < 3 && i < len(fields); i++ {
		v, err := parseFloat64(fields[i])
		if err == nil {
			la[i] = v
		}
	}
	return la
}

func loadAvgPath(procRoot string) string {
	if procRoot == "" {
		return "/proc/loadavg"
	}
	return procRoot + "/loadavg"
}

// CPUIdleRatio computes the idle ratio from two CPUStat snapshots.
// Returns a value in [0.0, 1.0]; 1.0 means fully idle.
func CPUIdleRatio(before, after CPUStat) float64 {
	deltaTotal := after.Total - before.Total
	if deltaTotal == 0 {
		return 1.0
	}
	deltaIdle := after.Idle - before.Idle
	return float64(deltaIdle) / float64(deltaTotal)
}

// LoadAvgPerCPU returns the 1-minute load average normalised by ncpus.
func LoadAvgPerCPU(la [3]float64) float64 {
	ncpus := runtime.NumCPU()
	if ncpus < 1 {
		ncpus = 1
	}
	return la[0] / float64(ncpus)
}

// captureProcesses reads /proc/[pid]/comm for all running processes and returns
// a map of process name → count for names that appear in the blocklist.
func captureProcesses(procRoot string) map[string]int {
	root := procRoot
	if root == "" {
		root = "/proc"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make(map[string]int)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only numeric directories are PID entries.
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		commPath := filepath.Join(root, e.Name(), "comm")
		data, err := os.ReadFile(commPath)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(data))
		if isBlockedProcess(name) {
			result[name]++
		}
	}
	return result
}

// processBlocklist is the R5 §7.1 base process blocklist.
var processBlocklist = map[string]struct{}{
	"rsync": {}, "restic": {}, "borg": {}, "duplicity": {}, "pbs-backup": {},
	"plex-transcoder": {}, "Plex Media Scan": {}, "jellyfin-ffmpeg": {},
	"ffmpeg": {}, "HandBrakeCLI": {}, "x265": {}, "x264": {},
	"make": {}, "apt": {}, "apt-get": {}, "dpkg": {}, "dnf": {}, "rpm": {},
	"pacman": {}, "yay": {}, "paru": {}, "zypper": {},
	"updatedb": {}, "plocate-updatedb": {}, "mlocate": {},
	"smartctl": {}, "fio": {}, "stress-ng": {}, "sysbench": {},
}

// extraBlocklist is populated from config (operator extension, R5 §9).
var extraBlocklist []string

// SetExtraBlocklist sets the operator-extended process blocklist (from config).
func SetExtraBlocklist(names []string) {
	extraBlocklist = names
}

func isBlockedProcess(name string) bool {
	if _, ok := processBlocklist[name]; ok {
		return true
	}
	for _, extra := range extraBlocklist {
		if name == extra {
			return true
		}
	}
	return false
}

// uptimeSeconds reads /proc/uptime and returns the first field (seconds since boot).
func uptimeSeconds(procRoot string) (float64, error) {
	path := procRoot
	if path == "" {
		path = "/proc"
	}
	path = path + "/uptime"
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty %s", path)
	}
	return parseFloat64(fields[0])
}
