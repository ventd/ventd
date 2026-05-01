package fakeprocsys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdle_BuildsExpectedTree(t *testing.T) {
	r := Idle(t)

	if _, err := os.Stat(r.ProcRoot); err != nil {
		t.Fatalf("ProcRoot: %v", err)
	}
	if _, err := os.Stat(r.SysRoot); err != nil {
		t.Fatalf("SysRoot: %v", err)
	}

	// PSI trio present and zero.
	for _, name := range []string{"pressure/cpu", "pressure/io", "pressure/memory"} {
		path := filepath.Join(r.ProcRoot, name)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(b), "avg60=0.00") {
			t.Errorf("%s should be zero-pressure, got %q", name, b)
		}
	}

	uptime, err := os.ReadFile(filepath.Join(r.ProcRoot, "uptime"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(uptime), "7200.00") {
		t.Errorf("uptime: want age=7200.00, got %q", uptime)
	}

	// Battery & cgroup absent by default.
	if _, err := os.Stat(filepath.Join(r.SysRoot, "class/power_supply/AC0")); !os.IsNotExist(err) {
		t.Errorf("AC0 should not exist by default; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(r.ProcRoot, "1/cgroup")); !os.IsNotExist(err) {
		t.Errorf("/proc/1/cgroup should not exist by default; err=%v", err)
	}
}

func TestWritePSI_RoundTrip(t *testing.T) {
	r := Build(t)
	r.WritePSI(t, PSI{CPUSomeAvg60: 12.34, IOSomeAvg60: 5.67, MemFullAvg60: 0.99})

	b, err := os.ReadFile(filepath.Join(r.ProcRoot, "pressure/cpu"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "avg60=12.34") {
		t.Errorf("cpu psi: want avg60=12.34, got %q", b)
	}

	b, err = os.ReadFile(filepath.Join(r.ProcRoot, "pressure/memory"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "full avg10=0.00 avg60=0.99") {
		t.Errorf("memory psi: want full avg60=0.99, got %q", b)
	}
}

func TestRemovePSI_ForcesLoadavgFallback(t *testing.T) {
	r := Idle(t)
	r.RemovePSI(t)

	if _, err := os.Stat(filepath.Join(r.ProcRoot, "pressure/cpu")); !os.IsNotExist(err) {
		t.Errorf("pressure/cpu still exists after RemovePSI")
	}
}

func TestWriteAC_BatteryDischarging(t *testing.T) {
	r := Build(t)
	r.WriteAC(t, 0, false)
	r.WriteBattery(t, 0, "Discharging")

	online, err := os.ReadFile(filepath.Join(r.SysRoot, "class/power_supply/AC0/online"))
	if err != nil {
		t.Fatal(err)
	}
	if string(online) != "0\n" {
		t.Errorf("AC0/online: want 0, got %q", online)
	}

	stat, err := os.ReadFile(filepath.Join(r.SysRoot, "class/power_supply/BAT0/status"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stat) != "Discharging\n" {
		t.Errorf("BAT0/status: want Discharging, got %q", stat)
	}
}

func TestWriteCgroup_EmptyRemoves(t *testing.T) {
	r := Build(t)
	r.WriteCgroup(t, "0::/docker/abc123\n")
	if _, err := os.Stat(filepath.Join(r.ProcRoot, "1/cgroup")); err != nil {
		t.Fatalf("write: %v", err)
	}

	r.WriteCgroup(t, "")
	if _, err := os.Stat(filepath.Join(r.ProcRoot, "1/cgroup")); !os.IsNotExist(err) {
		t.Errorf("WriteCgroup(\"\") did not remove the file")
	}
}

func TestWriteHypervisor(t *testing.T) {
	r := Build(t)
	r.WriteHypervisor(t, "kvm")

	b, err := os.ReadFile(filepath.Join(r.SysRoot, "hypervisor/type"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "kvm\n" {
		t.Errorf("hypervisor/type: got %q", b)
	}
}
