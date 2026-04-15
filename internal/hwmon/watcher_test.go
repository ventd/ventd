package hwmon

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hwdiag"
)

// mockEnumerator returns the next queued snapshot on each call. Tests queue
// a sequence of snapshots; the watcher's check() drives the playback.
type mockEnumerator struct {
	t         *testing.T
	snapshots [][]HwmonDevice
	calls     int
}

func (m *mockEnumerator) next() []HwmonDevice {
	if m.calls >= len(m.snapshots) {
		// Repeat the last snapshot indefinitely so tests don't have to
		// enumerate every future rescan.
		m.calls++
		return m.snapshots[len(m.snapshots)-1]
	}
	s := m.snapshots[m.calls]
	m.calls++
	return s
}

func dev(stable, chip string, class CapabilityClass, bases ...string) HwmonDevice {
	d := HwmonDevice{
		Dir:          "/sys/class/hwmon/" + stable,
		StableDevice: stable,
		ChipName:     chip,
		Class:        class,
	}
	// Simulate bases as fan inputs / pwm / temp based on prefix so
	// Fingerprint() produces the expected sorted dedup list.
	for _, b := range bases {
		switch {
		case len(b) >= 3 && b[:3] == "pwm":
			d.PWM = append(d.PWM, PWMChannel{Index: b[3:], Path: "/sys/class/hwmon/" + stable + "/" + b})
		case len(b) >= 4 && b[:4] == "fan" && b[len(b)-6:] == "_input":
			d.FanInputs = append(d.FanInputs, "/sys/class/hwmon/"+stable+"/"+b)
		case len(b) >= 4 && b[:4] == "temp":
			d.TempInputs = append(d.TempInputs, "/sys/class/hwmon/"+stable+"/"+b)
		}
	}
	return d
}

// TestDefaultRescanPeriod_TenSecondPromise locks the watcher's
// production rescan ticker to <= 10 seconds so the README's "ventd
// notices a new fan or GPU within ten seconds" claim is true even on
// hosts where AF_NETLINK uevents are filtered.
func TestDefaultRescanPeriod_TenSecondPromise(t *testing.T) {
	if defaultRescanPeriod > 10*time.Second {
		t.Errorf("defaultRescanPeriod = %v; README promises detection within 10s",
			defaultRescanPeriod)
	}
	if defaultRescanPeriod < time.Second {
		t.Errorf("defaultRescanPeriod = %v; under 1s wastes CPU on a sysfs scan",
			defaultRescanPeriod)
	}
}

func newTestWatcher(t *testing.T, snapshots ...[]HwmonDevice) (*Watcher, *hwdiag.Store, *mockEnumerator) {
	t.Helper()
	store := hwdiag.NewStore()
	enum := &mockEnumerator{t: t, snapshots: snapshots}
	w := NewWatcher(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithEnumerator(enum.next),
		WithoutUevents(),
		WithDebounce(10*time.Millisecond),
		WithRescanPeriod(time.Hour), // never fires inside the test
	)
	// Prime the stable map from the first snapshot exactly as Run() would.
	w.stable = snapshotFingerprints(enum.next())
	w.pending = make(map[string]pendingChange)
	return w, store, enum
}

func TestWatcher_NoChange_NoEmission(t *testing.T) {
	base := []HwmonDevice{dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	w, store, _ := newTestWatcher(t, base, base, base)

	pending := w.check(time.Now())
	if pending {
		t.Fatalf("no change expected, got pending=%v", pending)
	}
	if got := len(store.Snapshot(hwdiag.Filter{}).Entries); got != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", got)
	}
}

func TestWatcher_DeviceAdded_EmitsAfterDebounce(t *testing.T) {
	t0 := []HwmonDevice{dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	t1 := []HwmonDevice{
		dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
		dev("amdgpu", "amdgpu", ClassPrimary, "pwm1", "fan1_input", "temp1_input"),
	}
	w, store, _ := newTestWatcher(t, t0, t1, t1)

	now := time.Now()
	if !w.check(now) {
		t.Fatalf("expected pending after first divergence")
	}
	if got := len(store.Snapshot(hwdiag.Filter{}).Entries); got != 0 {
		t.Fatalf("should not emit before debounce; got %d", got)
	}

	// Second check after debounce elapses — promotes pending into stable.
	if w.check(now.Add(defaultDebounce)) {
		t.Fatalf("did not expect lingering pending after promotion")
	}
	entries := store.Snapshot(hwdiag.Filter{}).Entries
	if len(entries) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(entries))
	}
	e := entries[0]
	if e.ID != hwdiag.IDHardwareChanged {
		t.Fatalf("id = %q", e.ID)
	}
	if e.Severity != hwdiag.SeverityInfo {
		t.Fatalf("severity = %q", e.Severity)
	}
	if e.Remediation == nil || e.Remediation.AutoFixID != hwdiag.AutoFixReRunSetup {
		t.Fatalf("bad remediation: %+v", e.Remediation)
	}
	if e.Context["action"] != "added" {
		t.Fatalf("action = %v", e.Context["action"])
	}
}

func TestWatcher_TransientFlap_NoEmission(t *testing.T) {
	// Stable set.
	t0 := []HwmonDevice{dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	// Device briefly disappears.
	t1 := []HwmonDevice{}
	// Back within the debounce window.
	t2 := t0

	w, store, _ := newTestWatcher(t, t0, t1, t2)

	now := time.Now()
	if !w.check(now) {
		t.Fatalf("expected pending after removal")
	}
	// Reappear before debounce elapses: pending must clear, no emission.
	if w.check(now.Add(time.Millisecond)) {
		t.Fatalf("expected pending cleared after flap resolution")
	}
	if got := len(store.Snapshot(hwdiag.Filter{}).Entries); got != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", got)
	}
}

func TestWatcher_DeviceRemoved_EmitsAfterDebounce(t *testing.T) {
	t0 := []HwmonDevice{
		dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
		dev("amdgpu", "amdgpu", ClassPrimary, "pwm1", "fan1_input"),
	}
	t1 := []HwmonDevice{
		dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
	}
	w, store, _ := newTestWatcher(t, t0, t1, t1)

	now := time.Now()
	w.check(now)
	w.check(now.Add(defaultDebounce))

	entries := store.Snapshot(hwdiag.Filter{}).Entries
	if len(entries) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(entries))
	}
	if entries[0].Context["action"] != "removed" {
		t.Fatalf("action = %v", entries[0].Context["action"])
	}
}

func TestWatcher_ClassChange_EmitsChanged(t *testing.T) {
	// Same device reclassifies from ReadOnly to Primary (driver upgrade,
	// pwm_enable suddenly available). That's a "changed" fingerprint.
	t0 := []HwmonDevice{dev("nct6687", "nct6687d", ClassReadOnly, "fan1_input")}
	t1 := []HwmonDevice{dev("nct6687", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	w, store, _ := newTestWatcher(t, t0, t1, t1)

	now := time.Now()
	w.check(now)
	w.check(now.Add(defaultDebounce))

	entries := store.Snapshot(hwdiag.Filter{}).Entries
	if len(entries) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(entries))
	}
	if entries[0].Context["action"] != "changed" {
		t.Fatalf("action = %v", entries[0].Context["action"])
	}
}

func TestFingerprint_StableAcrossPathChurn(t *testing.T) {
	// Same stable device, different hwmonX dir. Fingerprint must be equal.
	a := HwmonDevice{
		Dir: "/sys/class/hwmon/hwmon3", StableDevice: "/sys/devices/platform/nct6687.2608",
		ChipName: "nct6687d", Class: ClassPrimary,
		PWM: []PWMChannel{{Index: "1", Path: "/sys/class/hwmon/hwmon3/pwm1", EnablePath: "/sys/class/hwmon/hwmon3/pwm1_enable", FanInput: "/sys/class/hwmon/hwmon3/fan1_input"}},
	}
	b := a
	b.Dir = "/sys/class/hwmon/hwmon7"
	b.PWM = []PWMChannel{{Index: "1", Path: "/sys/class/hwmon/hwmon7/pwm1", EnablePath: "/sys/class/hwmon/hwmon7/pwm1_enable", FanInput: "/sys/class/hwmon/hwmon7/fan1_input"}}

	fa, fb := Fingerprint(a), Fingerprint(b)
	if fa.ChipName != fb.ChipName || fa.Class != fb.Class || !stringSliceEqual(fa.Bases, fb.Bases) {
		t.Fatalf("fingerprints differ across hwmonX renumber:\n  a=%+v\n  b=%+v", fa, fb)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
