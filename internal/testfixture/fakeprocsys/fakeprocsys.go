// Package fakeprocsys builds deterministic /proc and /sys fixture trees
// rooted at t.TempDir() for unit tests that depend on system-state files.
//
// The fixture is the cross-package shared source of truth for synthetic
// /proc/loadavg, /proc/pressure, /proc/uptime, /proc/1/cgroup,
// /sys/class/power_supply/{AC,BAT}*, /sys/hypervisor, and /proc/mdstat
// content. Inline reimplementations of these helpers used to live in
// internal/idle/idle_test.go; future smart-mode patches that need the same
// fixtures (v0.5.6 opportunistic probing, v0.5.7 thermal coupling, etc.)
// import this package instead of duplicating.
//
// Defaults aim at the "idle, on AC, not in a container, not virtualised"
// state. Override individual files via WriteFile after Build().
package fakeprocsys

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Roots holds the absolute paths to the synthetic /proc and /sys trees.
type Roots struct {
	ProcRoot string
	SysRoot  string
}

// Idle returns Roots that satisfy the idle predicate by default:
//   - PSI all zero (low pressure)
//   - /proc/uptime past the cold-start window (7200s = 2h)
//   - /proc/loadavg low
//   - no /sys/class/power_supply/* (no on-battery signal)
//   - no /proc/1/cgroup (no in-container signal)
//
// Callers add or replace files via r.WriteFile(t, ...) as needed.
func Idle(t *testing.T) Roots {
	t.Helper()
	r := Build(t)
	r.WritePSI(t, PSI{
		CPUSomeAvg10: 0, CPUSomeAvg60: 0, CPUSomeAvg300: 0,
		IOSomeAvg10: 0, IOSomeAvg60: 0, IOSomeAvg300: 0,
		MemFullAvg10: 0, MemFullAvg60: 0, MemFullAvg300: 0,
	})
	r.WriteUptime(t, 7200.00, 14400.00)
	return r
}

// Build creates an empty pair of /proc and /sys roots in t.TempDir().
// Use this when you want full control over which files exist; otherwise
// prefer Idle.
func Build(t *testing.T) Roots {
	t.Helper()
	dir := t.TempDir()
	r := Roots{
		ProcRoot: filepath.Join(dir, "proc"),
		SysRoot:  filepath.Join(dir, "sys"),
	}
	if err := os.MkdirAll(r.ProcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.SysRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

// WriteFile writes content to a path relative to ProcRoot. Parent dirs
// are created automatically.
func (r Roots) WriteFile(t *testing.T, rel, content string) {
	t.Helper()
	writeAt(t, r.ProcRoot, rel, content)
}

// WriteSysFile writes content to a path relative to SysRoot.
func (r Roots) WriteSysFile(t *testing.T, rel, content string) {
	t.Helper()
	writeAt(t, r.SysRoot, rel, content)
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// PSI holds the avgN values that make up the three /proc/pressure files.
// Values are percent (0.00–100.00).
type PSI struct {
	CPUSomeAvg10, CPUSomeAvg60, CPUSomeAvg300 float64
	IOSomeAvg10, IOSomeAvg60, IOSomeAvg300    float64
	MemFullAvg10, MemFullAvg60, MemFullAvg300 float64
}

// WritePSI writes /proc/pressure/{cpu,io,memory} from p. The "some" field
// is used for cpu and io (kernel always emits both "some" and "full" for
// cpu/io but ventd reads "some" per RULE-IDLE-04). Memory uses "full".
func (r Roots) WritePSI(t *testing.T, p PSI) {
	t.Helper()
	r.WriteFile(t, "pressure/cpu",
		"some avg10="+f2(p.CPUSomeAvg10)+" avg60="+f2(p.CPUSomeAvg60)+" avg300="+f2(p.CPUSomeAvg300)+" total=0\n"+
			"full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	r.WriteFile(t, "pressure/io",
		"some avg10="+f2(p.IOSomeAvg10)+" avg60="+f2(p.IOSomeAvg60)+" avg300="+f2(p.IOSomeAvg300)+" total=0\n"+
			"full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	r.WriteFile(t, "pressure/memory",
		"some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"+
			"full avg10="+f2(p.MemFullAvg10)+" avg60="+f2(p.MemFullAvg60)+" avg300="+f2(p.MemFullAvg300)+" total=0\n")
}

// RemovePSI deletes the /proc/pressure tree to simulate a kernel without
// CONFIG_PSI=y (forces ventd's loadavg fallback per RULE-IDLE-04).
func (r Roots) RemovePSI(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(r.ProcRoot, "pressure")); err != nil {
		t.Fatal(err)
	}
}

// WriteLoadavg writes /proc/loadavg with the given 1/5/15-minute averages.
// running and total tasks default to "1/100" and "999".
func (r Roots) WriteLoadavg(t *testing.T, l1, l5, l15 float64) {
	t.Helper()
	r.WriteFile(t, "loadavg",
		f2(l1)+" "+f2(l5)+" "+f2(l15)+" 1/100 999\n")
}

// WriteUptime writes /proc/uptime with the boot age (seconds) and idle.
func (r Roots) WriteUptime(t *testing.T, age, idle float64) {
	t.Helper()
	r.WriteFile(t, "uptime", f2(age)+" "+f2(idle)+"\n")
}

// WriteCgroup writes /proc/1/cgroup with the given content. Pass an empty
// string to remove the file (simulates a host system).
func (r Roots) WriteCgroup(t *testing.T, content string) {
	t.Helper()
	if content == "" {
		_ = os.Remove(filepath.Join(r.ProcRoot, "1", "cgroup"))
		return
	}
	r.WriteFile(t, "1/cgroup", content)
}

// WriteAC writes /sys/class/power_supply/AC<idx>/online with online=1 (on AC)
// or 0 (no mains). Multiple AC sources can coexist.
func (r Roots) WriteAC(t *testing.T, idx int, online bool) {
	t.Helper()
	v := "0\n"
	if online {
		v = "1\n"
	}
	r.WriteSysFile(t, "class/power_supply/AC"+strconv.Itoa(idx)+"/online", v)
}

// WriteBattery writes /sys/class/power_supply/BAT<idx>/status with one of
// "Charging", "Discharging", "Full", "Not charging".
func (r Roots) WriteBattery(t *testing.T, idx int, status string) {
	t.Helper()
	r.WriteSysFile(t, "class/power_supply/BAT"+strconv.Itoa(idx)+"/status", status+"\n")
}

// WriteHypervisor creates /sys/hypervisor/type with the given content.
// Used to simulate /sys/hypervisor presence (a virt detection signal).
func (r Roots) WriteHypervisor(t *testing.T, hvType string) {
	t.Helper()
	r.WriteSysFile(t, "hypervisor/type", hvType+"\n")
}

// WriteMdstat writes /proc/mdstat with the given content. Used to simulate
// an active RAID rebuild for storage-maintenance gating.
func (r Roots) WriteMdstat(t *testing.T, content string) {
	t.Helper()
	r.WriteFile(t, "mdstat", content)
}

// WriteDockerEnv creates /.dockerenv at the synthetic root (NOT under
// ProcRoot — it's a marker file at filesystem root in real systems). For
// tests, callers typically pass r.ProcRoot's parent as the rootFS.
func (r Roots) WriteDockerEnv(t *testing.T) {
	t.Helper()
	parent := filepath.Dir(r.ProcRoot)
	if err := os.WriteFile(filepath.Join(parent, ".dockerenv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// f2 formats a float to 2 decimal places without scientific notation.
func f2(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
