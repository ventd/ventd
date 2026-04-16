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

// TestWatcher_RebindTrigger_FiresOnAdded covers issue #95 Option A: when a
// new hwmon device appears (action=added) and a RebindTrigger is installed,
// the watcher invokes it exactly once with the new device's stable key and
// fingerprint after the debounce elapses.
func TestWatcher_RebindTrigger_FiresOnAdded(t *testing.T) {
	t0 := []HwmonDevice{dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	t1 := []HwmonDevice{
		dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
		dev("nct6687.b", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
	}

	var (
		calls []string
		fps   []DeviceFingerprint
	)
	store := hwdiag.NewStore()
	enum := &mockEnumerator{t: t, snapshots: [][]HwmonDevice{t0, t1, t1}}
	w := NewWatcher(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithEnumerator(enum.next),
		WithoutUevents(),
		WithDebounce(10*time.Millisecond),
		WithRescanPeriod(time.Hour),
		WithRebindMinInterval(time.Millisecond), // fine-grain for the test
		WithRebindTrigger(func(key string, fp DeviceFingerprint) {
			calls = append(calls, key)
			fps = append(fps, fp)
		}),
	)
	w.stable = snapshotFingerprints(enum.next())
	w.pending = make(map[string]pendingChange)

	now := time.Now()
	if !w.check(now) {
		t.Fatalf("expected pending after divergence")
	}
	if len(calls) != 0 {
		t.Fatalf("trigger fired before debounce: %v", calls)
	}

	w.check(now.Add(defaultDebounce))
	if got, want := len(calls), 1; got != want {
		t.Fatalf("trigger call count = %d, want %d (calls=%v)", got, want, calls)
	}
	if got, want := calls[0], "nct6687.b"; got != want {
		t.Fatalf("trigger key = %q, want %q", got, want)
	}
	if got, want := fps[0].ChipName, "nct6687d"; got != want {
		t.Fatalf("trigger chip name = %q, want %q", got, want)
	}
	if w.rebindCalls != 1 || w.rebindDrops != 0 {
		t.Fatalf("rebindCalls=%d rebindDrops=%d, want 1/0", w.rebindCalls, w.rebindDrops)
	}
}

// TestWatcher_RebindTrigger_IgnoresNonAdded confirms removed/changed events
// don't trigger a rebind (scoped to added only; removed has its own tracker).
func TestWatcher_RebindTrigger_IgnoresNonAdded(t *testing.T) {
	t0 := []HwmonDevice{
		dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
		dev("nct6687.b", "nct6687d", ClassPrimary, "pwm1", "fan1_input"),
	}
	// Second device disappears — action=removed.
	t1 := []HwmonDevice{dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1", "fan1_input")}
	// First device reclassifies — action=changed.
	t2 := []HwmonDevice{dev("nct6687.a", "nct6687d", ClassReadOnly, "fan1_input")}

	var calls []string
	store := hwdiag.NewStore()
	enum := &mockEnumerator{t: t, snapshots: [][]HwmonDevice{t0, t1, t1, t2, t2}}
	w := NewWatcher(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithEnumerator(enum.next),
		WithoutUevents(),
		WithDebounce(10*time.Millisecond),
		WithRescanPeriod(time.Hour),
		WithRebindMinInterval(time.Millisecond),
		WithRebindTrigger(func(key string, _ DeviceFingerprint) {
			calls = append(calls, key)
		}),
	)
	w.stable = snapshotFingerprints(enum.next())
	w.pending = make(map[string]pendingChange)

	now := time.Now()
	w.check(now)
	w.check(now.Add(defaultDebounce))
	w.check(now.Add(2 * defaultDebounce))
	w.check(now.Add(3 * defaultDebounce))

	if len(calls) != 0 {
		t.Fatalf("expected no trigger calls for removed/changed, got: %v", calls)
	}
}

// TestWatcher_RebindTrigger_RateLimited confirms that two action=added
// promotions crossing the debounce line within WithRebindMinInterval produce
// only one trigger call — a flapping driver must not ping-pong the daemon.
func TestWatcher_RebindTrigger_RateLimited(t *testing.T) {
	// Start with device A only.
	t0 := []HwmonDevice{dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1")}
	// Add device B.
	t1 := []HwmonDevice{
		dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1"),
		dev("nct6687.b", "nct6687d", ClassPrimary, "pwm1"),
	}
	// Remove B again.
	t2 := t0
	// Re-add B (second add-event within the rate-limit window).
	t3 := t1

	var calls []string
	store := hwdiag.NewStore()
	enum := &mockEnumerator{t: t, snapshots: [][]HwmonDevice{t0, t1, t1, t2, t2, t3, t3}}
	w := NewWatcher(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithEnumerator(enum.next),
		WithoutUevents(),
		WithDebounce(10*time.Millisecond),
		WithRescanPeriod(time.Hour),
		WithRebindMinInterval(time.Hour), // never re-arms inside the test
		WithRebindTrigger(func(key string, _ DeviceFingerprint) {
			calls = append(calls, key)
		}),
	)
	w.stable = snapshotFingerprints(enum.next())
	w.pending = make(map[string]pendingChange)

	now := time.Now()
	for i := 0; i < 6; i++ {
		w.check(now.Add(time.Duration(i) * defaultDebounce))
	}

	if got, want := len(calls), 1; got != want {
		t.Fatalf("trigger call count = %d, want %d (calls=%v)", got, want, calls)
	}
	if w.rebindDrops == 0 {
		t.Fatalf("expected at least one rebindDrop; got %d", w.rebindDrops)
	}
}

// TestWatcher_RebindTrigger_NilHookNoCrash confirms the watcher tolerates a
// nil rebindTrigger — production runs with one installed, but tests and
// older callers should still work without one.
//
// This is also the disabled-flag regression guard for v0.3: when
// cfg.Hwmon.DynamicRebind is false, main.go never passes
// WithRebindTrigger, so the watcher's rebindTrigger field stays nil and
// an action=added promotion must NOT signal anything. If a future
// refactor wires the trigger up unconditionally it will light this test
// and the config gate will still hold.
func TestWatcher_RebindTrigger_NilHookNoCrash(t *testing.T) {
	t0 := []HwmonDevice{dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1")}
	t1 := []HwmonDevice{
		dev("nct6687.a", "nct6687d", ClassPrimary, "pwm1"),
		dev("nct6687.b", "nct6687d", ClassPrimary, "pwm1"),
	}
	w, _, _ := newTestWatcher(t, t0, t1, t1)
	if w.rebindTrigger != nil {
		t.Fatalf("newTestWatcher unexpectedly installed a rebindTrigger; "+
			"disabled-path test is no longer exercising the disabled path (got %T)",
			w.rebindTrigger)
	}
	now := time.Now()
	w.check(now)
	w.check(now.Add(defaultDebounce))
	if w.rebindCalls != 0 {
		t.Fatalf("rebindCalls = %d with nil hook; want 0", w.rebindCalls)
	}
}
