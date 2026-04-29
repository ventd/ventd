package idle

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// captureDiskBytes returns aggregate bytes/s across all block devices by reading
// /proc/diskstats. Two successive reads at ~1s interval would be ideal; here we
// return the raw sectors-read+written field for delta computation by callers.
// For the snapshot this is captured as 0 (requires delta from two samples).
func captureDiskBytes(_ string) uint64 {
	// Disk rate requires two time-separated samples; Snapshot records 0 as the
	// initial capture. The idle predicate uses two Snapshots for delta computation.
	return 0
}

// DiskBytesPerSec computes aggregate disk throughput between two /proc/diskstats
// reads taken deltaSeconds apart.
func DiskBytesPerSec(procRoot string, before, after diskStat) uint64 {
	_ = procRoot // reserved for injection
	delta := after.sectorsTotal - before.sectorsTotal
	// 512 bytes per sector (Linux kernel always reports in 512-byte units).
	bytes := delta * 512
	return bytes
}

type diskStat struct {
	sectorsTotal uint64
}

// ReadDiskStat reads the aggregate sector counts from /proc/diskstats.
func ReadDiskStat(procRoot string) diskStat {
	path := procRoot
	if path == "" {
		path = "/proc"
	}
	path = path + "/diskstats"
	f, err := os.Open(path)
	if err != nil {
		return diskStat{}
	}
	defer func() { _ = f.Close() }()

	var total uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// diskstats format: major minor name reads ... sectors_read ... writes ... sectors_written ...
		// sectors_read = field index 5; sectors_written = field index 9
		if len(fields) < 10 {
			continue
		}
		sr, err1 := strconv.ParseUint(fields[5], 10, 64)
		sw, err2 := strconv.ParseUint(fields[9], 10, 64)
		if err1 == nil && err2 == nil {
			total += sr + sw
		}
	}
	return diskStat{sectorsTotal: total}
}

// captureNetPPS reads /proc/net/dev and returns aggregate packets/s. Like disk,
// this requires two samples; Snapshot records 0 as the initial value.
func captureNetPPS(_ string) uint64 {
	return 0
}

// NetPPS computes aggregate network packets per second between two readings.
func NetPPS(procRoot string, before, after netStat) uint64 {
	_ = procRoot
	return after.pktTotal - before.pktTotal
}

type netStat struct {
	pktTotal uint64
}

// ReadNetStat reads aggregate packet counts from /proc/net/dev.
func ReadNetStat(procRoot string) netStat {
	path := procRoot
	if path == "" {
		path = "/proc"
	}
	path = path + "/net/dev"
	f, err := os.Open(path)
	if err != nil {
		return netStat{}
	}
	defer func() { _ = f.Close() }()

	var total uint64
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		if lineNum <= 2 { // skip header lines
			continue
		}
		line := sc.Text()
		// Format: "  eth0: rx_bytes rx_pkts ... tx_bytes tx_pkts ..."
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 9 {
			continue
		}
		rxPkts, err1 := strconv.ParseUint(fields[1], 10, 64)
		txPkts, err2 := strconv.ParseUint(fields[9], 10, 64)
		if err1 == nil && err2 == nil {
			total += rxPkts + txPkts
		}
	}
	return netStat{pktTotal: total}
}

// captureGPUBusy reads AMD gpu_busy_percent from sysfs DRM paths.
// Returns a map of device name → busy percentage.
func captureGPUBusy(sysRoot string) map[string]float64 {
	result := make(map[string]float64)
	root := sysRoot
	if root == "" {
		root = "/sys"
	}

	// AMD: /sys/class/drm/card*/device/gpu_busy_percent
	amdPattern := root + "/class/drm/card*/device/gpu_busy_percent"
	matches, err := filepath.Glob(amdPattern)
	if err == nil {
		for _, p := range matches {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			v, err := parseFloat64(strings.TrimSpace(string(data)))
			if err != nil {
				continue
			}
			// Use card dir as device ID.
			card := filepath.Base(filepath.Dir(filepath.Dir(p)))
			result["amd:"+card] = v
		}
	}
	return result
}

// captureStructuralFlags reads mdraid, ZFS, and BTRFS maintenance states.
func captureStructuralFlags(procRoot, sysRoot string) StructuralFlags {
	var f StructuralFlags
	f.MDRAIDActive = checkMDRAIDActive(procRoot)
	f.ZFSScrub = checkZFSScrub(procRoot)
	f.BTRFSScrub = checkBTRFSScrub(sysRoot)
	return f
}

func checkMDRAIDActive(procRoot string) bool {
	path := procRoot
	if path == "" {
		path = "/proc"
	}
	path = path + "/mdstat"
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "recovery =") ||
		strings.Contains(lower, "resync =") ||
		strings.Contains(lower, "check =")
}

func checkZFSScrub(procRoot string) bool {
	root := procRoot
	if root == "" {
		root = "/proc"
	}
	statePattern := root + "/spl/kstat/zfs/*/state"
	matches, err := filepath.Glob(statePattern)
	if err != nil {
		return false
	}
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "scrub" {
			return true
		}
	}
	return false
}

func checkBTRFSScrub(sysRoot string) bool {
	root := sysRoot
	if root == "" {
		root = "/sys"
	}
	pattern := root + "/fs/btrfs/*/devinfo/*/scrub_in_progress"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "1" {
			return true
		}
	}
	return false
}
