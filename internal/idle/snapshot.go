package idle

import (
	"strconv"
	"time"
)

// PSIReadings holds parsed PSI averages from /proc/pressure/*.
type PSIReadings struct {
	CPUSomeAvg10  float64
	CPUSomeAvg60  float64
	CPUSomeAvg300 float64
	IOSomeAvg10   float64
	IOSomeAvg60   float64
	IOSomeAvg300  float64
	MemFullAvg10  float64
	MemFullAvg60  float64
}

// CPUStat holds a /proc/stat CPU line parsed into idle/total ticks.
type CPUStat struct {
	Idle  uint64
	Total uint64
}

// StructuralFlags records active storage-maintenance states.
type StructuralFlags struct {
	MDRAIDActive bool // recovery|resync|check in /proc/mdstat
	ZFSScrub     bool // any ZFS pool state == "scrub"
	BTRFSScrub   bool // any btrfs devinfo scrub_in_progress == 1
}

// Snapshot captures all idle-predicate signals at a single instant in time.
type Snapshot struct {
	Timestamp       time.Time
	PSI             PSIReadings
	CPUStat         CPUStat
	LoadAvg         [3]float64
	DiskBytesPerSec uint64
	NetPPS          uint64
	GPUBusyPercent  map[string]float64 // device → busy %
	Processes       map[string]int     // blocked-process name → count
	StructuralFlags StructuralFlags
}

// Capture reads all idle-predicate signals and returns a Snapshot.
// It never returns an error; individual signal failures are silently zeroed.
func Capture(deps snapshotDeps) *Snapshot {
	s := &Snapshot{
		Timestamp:      deps.clock.Now(),
		GPUBusyPercent: make(map[string]float64),
		Processes:      make(map[string]int),
	}
	s.PSI = capturePSI(deps.procRoot)
	s.CPUStat = captureCPUStat(deps.procRoot)
	s.LoadAvg = captureLoadAvg(deps.procRoot)
	s.DiskBytesPerSec = captureDiskBytes(deps.procRoot)
	s.NetPPS = captureNetPPS(deps.procRoot)
	s.GPUBusyPercent = captureGPUBusy(deps.sysRoot)
	s.Processes = captureProcesses(deps.procRoot)
	s.StructuralFlags = captureStructuralFlags(deps.procRoot, deps.sysRoot)
	return s
}

// snapshotDeps bundles injectable filesystem roots for testing.
type snapshotDeps struct {
	procRoot string
	sysRoot  string
	clock    Clock
}

// parseFloat64 parses a string as a float64.
func parseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
