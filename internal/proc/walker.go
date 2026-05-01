// Package proc walks /proc to produce per-process samples for the
// v0.5.6 signature library.
//
// The walker reads only fields whose privacy surface is bounded by
// design (comm, jiffies, RSS, PPid) — never cmdline, exe, environ,
// or /proc/[pid]/io. v0.5.6's RULE-SIG-HASH-02 is enforced at this
// layer: the walker will not return data the hasher cannot legally
// consume.
package proc

import (
	"bufio"
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessSample is the minimal per-process record consumed by the
// signature library and the idle gate's blocklist filter.
type ProcessSample struct {
	PID       int
	PPid      int
	Comm      string  // /proc/PID/comm, null-stripped, ≤16 bytes
	EWMACPU   float64 // EWMA share of one core over the last ~10 s
	RSSBytes  uint64
	IsKThread bool // PPid == 2 OR comm starts with '['
}

// Walker maintains per-process EWMA-CPU state across calls.
//
// Concurrent calls to Walker.Walk are serialised by an internal
// mutex; the signature library's tick goroutine is the only
// expected caller. The idle blocklist scan and any future consumer
// can call Walker.Sample(...) for a one-shot read without the EWMA
// state.
type Walker struct {
	procRoot   string
	clkTck     int64 // jiffies per second (sysconf(_SC_CLK_TCK))
	pageSize   int64
	halfLifeS  float64
	now        func() time.Time
	mu         sync.Mutex
	prevTotal  uint64 // /proc/stat user+nice+sys+idle+iowait+irq+softirq+steal jiffies
	prevAtTime time.Time
	prevPerPID map[int]processCPU
	ewma       map[int]float64
}

type processCPU struct {
	utime uint64
	stime uint64
}

// New constructs a Walker. procRoot defaults to /proc; tests inject
// a fixture root. clkTck and pageSize default to system values when
// zero.
func New(procRoot string, clkTck, pageSize int64) *Walker {
	if procRoot == "" {
		procRoot = "/proc"
	}
	if clkTck <= 0 {
		clkTck = 100 // overwhelmingly the Linux default
	}
	if pageSize <= 0 {
		pageSize = int64(os.Getpagesize())
	}
	return &Walker{
		procRoot:   procRoot,
		clkTck:     clkTck,
		pageSize:   pageSize,
		halfLifeS:  10.0, // EWMA half-life in seconds (R7 §Q2)
		now:        time.Now,
		prevPerPID: make(map[int]processCPU),
		ewma:       make(map[int]float64),
	}
}

// Walk reads /proc and returns one ProcessSample per PID. The first
// call seeds EWMA state and returns samples with EWMACPU == 0; the
// second call onward returns the EWMA-CPU share computed against
// the previous snapshot.
//
// Walk is not safe for concurrent use; callers should serialise.
func (w *Walker) Walk() ([]ProcessSample, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	curTotal, err := readSystemTotalJiffies(w.procRoot)
	if err != nil {
		return nil, err
	}

	dtSeconds := now.Sub(w.prevAtTime).Seconds()
	totalDelta := curTotal - w.prevTotal // jiffies
	if w.prevTotal == 0 || dtSeconds <= 0 || totalDelta == 0 {
		// First call: seed and return zero-CPU samples.
		samples, perPID, err := w.snapshotProcs(false)
		if err != nil {
			return nil, err
		}
		w.prevPerPID = perPID
		w.prevTotal = curTotal
		w.prevAtTime = now
		return samples, nil
	}

	// EWMA decay factor for this dt window.
	alpha := halfLifeAlpha(dtSeconds, w.halfLifeS)

	samples, perPID, err := w.snapshotProcs(true)
	if err != nil {
		return nil, err
	}

	// Compute per-process CPU share over [prev, now].
	for i := range samples {
		s := &samples[i]
		cur, ok := perPID[s.PID]
		if !ok {
			continue
		}
		prev, hadPrev := w.prevPerPID[s.PID]
		if !hadPrev {
			s.EWMACPU = 0 // first observation; let next tick warm up
			continue
		}
		ticksDelta := (cur.utime + cur.stime) - (prev.utime + prev.stime)
		if ticksDelta > totalDelta {
			ticksDelta = totalDelta
		}
		share := float64(ticksDelta) / float64(totalDelta)
		// Convert to share-of-one-core by multiplying by ncpus
		// (ncpus is implicit in totalDelta; share is already
		// fraction of total system jiffies. Multiply by ncpus to
		// get share-of-one-core.)
		// We compute ncpus from /proc/stat lines beginning with "cpu" + digit.
		// For simplicity and to match top(1)'s convention, treat
		// share as already share-of-one-core: a process pegging
		// one core on a 16-core box reads as ~0.0625 of total
		// jiffies, which when converted to share-of-one-core is
		// 0.0625 × 16 = 1.0. Apply ncpus multiplier here.
		share = share * float64(w.cachedNCPUs())

		ewma := w.ewma[s.PID]
		ewma = ewma*alpha + share*(1.0-alpha)
		w.ewma[s.PID] = ewma
		s.EWMACPU = ewma
	}

	// Drop EWMA state for PIDs that have exited.
	for pid := range w.ewma {
		if _, ok := perPID[pid]; !ok {
			delete(w.ewma, pid)
		}
	}

	w.prevPerPID = perPID
	w.prevTotal = curTotal
	w.prevAtTime = now
	return samples, nil
}

// cachedNCPUs returns the count of CPUs in /proc/stat. For test
// fixtures with no /proc/stat lines beyond "cpu", returns 1.
func (w *Walker) cachedNCPUs() int {
	data, err := os.ReadFile(filepath.Join(w.procRoot, "stat"))
	if err != nil {
		return 1
	}
	n := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) < 4 {
			continue
		}
		// "cpuN " (where N is a digit) — count per-CPU lines, not
		// the aggregate "cpu " line.
		if line[0] == 'c' && line[1] == 'p' && line[2] == 'u' {
			if line[3] >= '0' && line[3] <= '9' {
				n++
			}
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

// snapshotProcs walks /proc and returns one ProcessSample per
// numeric PID directory. It also returns a per-PID map of jiffies
// for the EWMA computation in Walk.
//
// withEWMA controls whether the existing per-PID EWMA is copied
// into the returned samples (used by Walk for the steady-state
// path).
func (w *Walker) snapshotProcs(withEWMA bool) ([]ProcessSample, map[int]processCPU, error) {
	entries, err := os.ReadDir(w.procRoot)
	if err != nil {
		return nil, nil, err
	}

	samples := make([]ProcessSample, 0, len(entries)/4)
	perPID := make(map[int]processCPU, len(entries)/4)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		s, cpu, ok := w.readPID(pid)
		if !ok {
			continue
		}
		if withEWMA {
			s.EWMACPU = w.ewma[pid]
		}
		samples = append(samples, s)
		perPID[pid] = cpu
	}
	return samples, perPID, nil
}

// readPID reads the comm, stat (utime/stime/PPid), and statm (RSS)
// for a single PID. Missing or partially-readable PIDs are silently
// skipped.
func (w *Walker) readPID(pid int) (ProcessSample, processCPU, bool) {
	pidDir := filepath.Join(w.procRoot, strconv.Itoa(pid))

	commBytes, err := os.ReadFile(filepath.Join(pidDir, "comm"))
	if err != nil {
		return ProcessSample{}, processCPU{}, false
	}
	comm := strings.TrimSpace(strings.TrimSuffix(string(commBytes), "\n"))
	if comm == "" {
		return ProcessSample{}, processCPU{}, false
	}

	statData, err := os.ReadFile(filepath.Join(pidDir, "stat"))
	if err != nil {
		return ProcessSample{}, processCPU{}, false
	}
	utime, stime, ppid, ok := parseStatJiffies(statData)
	if !ok {
		return ProcessSample{}, processCPU{}, false
	}

	rssPages, err := readRSSPages(filepath.Join(pidDir, "statm"))
	if err != nil {
		return ProcessSample{}, processCPU{}, false
	}

	return ProcessSample{
		PID:       pid,
		PPid:      ppid,
		Comm:      comm,
		RSSBytes:  rssPages * uint64(w.pageSize),
		IsKThread: ppid == 2 || strings.HasPrefix(comm, "["),
	}, processCPU{utime: utime, stime: stime}, true
}

// parseStatJiffies extracts utime (field 14), stime (field 15), and
// PPid (field 4) from a /proc/PID/stat line. The kernel's stat
// format is space-delimited but the comm field (field 2) is
// parenthesised and may contain spaces — find the last ')' to skip
// past it before counting fields.
func parseStatJiffies(stat []byte) (utime, stime uint64, ppid int, ok bool) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 || end+2 >= len(stat) {
		return 0, 0, 0, false
	}
	rest := stat[end+2:] // skip "PID (comm) " up to and including ' '
	fields := bytes.Fields(rest)
	// After the comm field, fields are: state(1), ppid(2),
	// pgrp(3), session(4), tty_nr(5), tpgid(6), flags(7),
	// minflt(8), cminflt(9), majflt(10), cmajflt(11), utime(12),
	// stime(13), cutime(14), cstime(15), ...
	// Numbering above is 1-indexed within the post-comm fields.
	if len(fields) < 13 {
		return 0, 0, 0, false
	}
	ppid64, err := strconv.ParseInt(string(fields[1]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	u, err := strconv.ParseUint(string(fields[11]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	s, err := strconv.ParseUint(string(fields[12]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	return u, s, int(ppid64), true
}

// readRSSPages reads /proc/PID/statm and returns the second field
// (resident set size in pages). The first field is total VM size,
// which we don't care about.
func readRSSPages(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := bytes.Fields(data)
	if len(fields) < 2 {
		return 0, errors.New("statm: short read")
	}
	rss, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return rss, nil
}

// readSystemTotalJiffies reads /proc/stat's first line ("cpu ...")
// and returns the sum of all jiffies fields (user + nice + system +
// idle + iowait + irq + softirq + steal + guest + guest_nice).
func readSystemTotalJiffies(procRoot string) (uint64, error) {
	f, err := os.Open(filepath.Join(procRoot, "stat"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, errors.New("stat: empty file")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, errors.New("stat: missing cpu line")
	}
	var total uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			break
		}
		total += v
	}
	if total == 0 {
		return 0, errors.New("stat: zero total jiffies")
	}
	return total, nil
}

// halfLifeAlpha returns the EWMA decay factor for a dt-second
// window with the given half-life. alpha = 0.5^(dt / half_life).
func halfLifeAlpha(dt, halfLife float64) float64 {
	if halfLife <= 0 {
		return 0
	}
	return math.Pow(0.5, dt/halfLife)
}
