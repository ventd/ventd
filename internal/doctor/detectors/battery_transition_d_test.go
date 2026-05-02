package detectors

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
)

// stubPowerSupplyFS is a minimal in-memory PowerSupplyFS for tests.
// Mounts files under arbitrary paths and a synthetic dir listing.
type stubPowerSupplyFS struct {
	files map[string]string
	dirs  map[string][]string // parent dir → child names
}

func (s *stubPowerSupplyFS) ReadFile(name string) ([]byte, error) {
	v, ok := s.files[name]
	if !ok {
		return nil, errFileNotExist // reuse from modules_load test
	}
	return []byte(v), nil
}

func (s *stubPowerSupplyFS) ReadDir(name string) ([]os.DirEntry, error) {
	children, ok := s.dirs[name]
	if !ok {
		return nil, errors.New("no such dir")
	}
	out := make([]os.DirEntry, 0, len(children))
	for _, c := range children {
		out = append(out, fakeDirEntry{name: c})
	}
	return out, nil
}

// fakeDirEntry implements os.DirEntry for stub use.
type fakeDirEntry struct {
	name string
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return true }
func (f fakeDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not implemented") }

// onlineSupplyFS returns a stub representing a typical desktop on AC.
func onlineSupplyFS() *stubPowerSupplyFS {
	return &stubPowerSupplyFS{
		files: map[string]string{
			powerSupplyRoot + "/AC/online":   "1\n",
			powerSupplyRoot + "/BAT0/status": "Charging\n",
		},
		dirs: map[string][]string{
			powerSupplyRoot: {"AC", "BAT0"},
		},
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_OnACNoFacts(t *testing.T) {
	det := NewBatteryTransitionDetector(onlineSupplyFS())

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("on-AC emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_OnBatteryYieldsWarning(t *testing.T) {
	stub := &stubPowerSupplyFS{
		files: map[string]string{
			powerSupplyRoot + "/AC/online":   "0\n",
			powerSupplyRoot + "/BAT0/status": "Discharging\n",
		},
		dirs: map[string][]string{
			powerSupplyRoot: {"AC", "BAT0"},
		},
	}
	det := NewBatteryTransitionDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for on-battery, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", f.Severity)
	}
	if !strings.Contains(f.Title, "battery") {
		t.Errorf("Title doesn't mention battery: %q", f.Title)
	}
	if !f.Observed.Equal(fixedNow()) {
		t.Errorf("Observed not stamped from deps.Now")
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_DesktopWithEmptyACSlotNoFalsePositive(t *testing.T) {
	// Some desktops expose an AC slot that reports online=0 because
	// the cable's not plugged into an unused header. Without a BAT
	// the AND-gate prevents a false positive.
	stub := &stubPowerSupplyFS{
		files: map[string]string{
			powerSupplyRoot + "/AC/online": "0\n",
		},
		dirs: map[string][]string{
			powerSupplyRoot: {"AC"},
		},
	}
	det := NewBatteryTransitionDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("AC offline + no BAT should not emit fact (desktop empty-slot case); got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_NoPowerSupplyDirIsNoOp(t *testing.T) {
	// Container or bare-metal without ACPI exposes no power_supply/.
	det := NewBatteryTransitionDetector(&stubPowerSupplyFS{
		files: nil,
		dirs:  nil, // ReadDir returns error for any path
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Errorf("missing /sys/class/power_supply should not propagate as error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("missing power_supply emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_ChargingNotDischargingNoFact(t *testing.T) {
	// Battery present + AC offline + status="Not charging" (some
	// laptops report this transient state). Don't treat as battery.
	stub := &stubPowerSupplyFS{
		files: map[string]string{
			powerSupplyRoot + "/AC/online":   "0\n",
			powerSupplyRoot + "/BAT0/status": "Not charging\n",
		},
		dirs: map[string][]string{
			powerSupplyRoot: {"AC", "BAT0"},
		},
	}
	det := NewBatteryTransitionDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("Not charging should not surface as Discharging; got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_BatteryTransition_RespectsContextCancel(t *testing.T) {
	det := NewBatteryTransitionDetector(onlineSupplyFS())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestReadAcOnline_ParseValues(t *testing.T) {
	stub := &stubPowerSupplyFS{
		files: map[string]string{
			powerSupplyRoot + "/AC/online":  "1\n",
			powerSupplyRoot + "/AC2/online": "0\n",
			powerSupplyRoot + "/AC3/online": "garbage\n",
		},
	}
	cases := []struct {
		name           string
		wantOnline, ok bool
	}{
		{"AC", true, true},
		{"AC2", false, true},
		{"AC3", false, false},
		{"NX", false, false},
	}
	for _, c := range cases {
		online, ok := readAcOnline(stub, c.name)
		if online != c.wantOnline || ok != c.ok {
			t.Errorf("readAcOnline(%q) = (%v, %v), want (%v, %v)", c.name, online, ok, c.wantOnline, c.ok)
		}
	}
}

// timing micro-test to ensure no surprising allocations happen on
// the common path (full power_supply walk for a typical 2-supply
// desktop should complete trivially fast).
func TestBatteryTransition_FastPath(t *testing.T) {
	det := NewBatteryTransitionDetector(onlineSupplyFS())
	start := time.Now()
	for i := 0; i < 1000; i++ {
		_, _ = det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("1000 probes took %v; expected <100ms", elapsed)
	}
}
