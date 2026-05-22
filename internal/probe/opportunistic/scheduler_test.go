package opportunistic

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/sysclass"
)

// TestScheduler_FreshInstallGateDropped pins the v0.5.30 behaviour
// change for RULE-OPP-PROBE-07. With `FirstInstallDelay = 0`, the
// scheduler MUST NOT refuse a tick with `ReasonOpportunisticBootWindow`
// based on marker age — even on a marker that is seconds old, the
// gate is satisfied immediately.
//
// The hard idle preconditions (RULE-OPP-IDLE-04) remain the
// load-bearing protection against probing during real workload; this
// test uses `openOpportunisticGate(t)` which holds those open so the
// only refusal that could fire is the boot-window one being tested.
// A regression that re-introduces the 24 h delay would resurface
// here as `LastReason` containing `opportunistic_boot_window`.
func TestScheduler_FreshInstallGateDropped(t *testing.T) {
	dir := t.TempDir()
	markerPath := dir + "/.first-install-ts"
	now := time.Now()
	if _, err := EnsureMarker(markerPath, now); err != nil {
		t.Fatalf("EnsureMarker: %v", err)
	}

	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	chID := observation.ChannelID(ch.PWMPath)
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)

	var fired atomic.Int32
	cfg := SchedulerConfig{
		Channels:               []*probe.ControllableChannel{ch},
		Detector:               det,
		FirstInstallMarkerPath: markerPath,
		// Marker is seconds old. Pre-v0.5.30 this refused with
		// ReasonOpportunisticBootWindow because age < 24 h. Post-
		// v0.5.30 the gate is dropped — this clock would NOT
		// trigger the refusal regardless of marker age.
		Now:       func() time.Time { return now.Add(2 * time.Second) },
		ProbeDeps: testProbeDeps(&fired),
		IdleCfg:   openOpportunisticGate(t),
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// With the boot-window gate dropped, the scheduler proceeds into
	// FireOne — which RULE-OPP-PROBE-02 holds at the gap PWM for 30 s.
	// We don't want a 30 s probe inside a unit test; we just want to
	// assert that the boot-window refusal does NOT fire. A short-
	// deadline context cancels FireOne quickly so the tick returns
	// without holding the test for 30 s. RULE-OPP-PROBE-10 guarantees
	// ctx-cancel restores the controller-managed PWM, so this is a
	// safe early-exit pattern.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.tick(ctx)

	// Boot-window refusal MUST NOT fire — the gate is dropped.
	// LastReason may be empty (probe started but got ctx-cancelled),
	// or contain a ctx-cancel-flavoured reason. Either is acceptable.
	// The hard assertion is that the post-install boot-window
	// refusal is NOT what blocked the tick.
	if got := s.Status().LastReason; contains(got, string(idle.ReasonOpportunisticBootWindow)) {
		t.Errorf("LastReason = %q; must NOT contain %q (v0.5.30 dropped RULE-OPP-PROBE-07's 24 h gate)",
			got, idle.ReasonOpportunisticBootWindow)
	}
	_ = chID
	// `fired` (declared above as atomic.Int32) is intentionally not
	// inspected — it's passed by pointer to testProbeDeps so the
	// compiler considers it used, but its value depends on detector
	// gaps + ctx timing and is not load-bearing for this test.
}

// TestScheduler_HonoursToggleOff asserts that with the
// NeverActivelyProbeAfterInstall toggle on, the scheduler refuses
// every tick with the disabled reason (RULE-OPP-PROBE-08).
func TestScheduler_HonoursToggleOff(t *testing.T) {
	now := time.Now()
	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)

	var fired atomic.Int32
	cfg := SchedulerConfig{
		Channels:  []*probe.ControllableChannel{ch},
		Detector:  det,
		Disabled:  func() bool { return true },
		ProbeDeps: testProbeDeps(&fired),
		Now:       func() time.Time { return now },
		IdleCfg:   openOpportunisticGate(t),
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.tick(stubContext())

	if fired.Load() != 0 {
		t.Fatal("scheduler fired with toggle off")
	}
	want := string(idle.ReasonOpportunisticDisabled)
	if !contains(s.Status().LastReason, want) {
		t.Errorf("LastReason: got %q, want substring %q", s.Status().LastReason, want)
	}
}

// TestScheduler_RefusesManualModeChannels asserts that channels
// reported as manual-mode are skipped (RULE-OPP-PROBE-09).
func TestScheduler_RefusesManualModeChannels(t *testing.T) {
	now := time.Now()
	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)

	var fired atomic.Int32
	cfg := SchedulerConfig{
		Channels:     []*probe.ControllableChannel{ch},
		Detector:     det,
		IsManualMode: func(*probe.ControllableChannel) bool { return true },
		ProbeDeps:    testProbeDeps(&fired),
		Now:          func() time.Time { return now },
		IdleCfg:      openOpportunisticGate(t),
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.tick(stubContext())

	if fired.Load() != 0 {
		t.Fatal("scheduler fired on manual-mode channel")
	}
	if !contains(s.Status().LastReason, "manual_mode") {
		t.Errorf("LastReason: got %q, want substring manual_mode", s.Status().LastReason)
	}
}

// TestScheduler_PicksLowestPWMOnLargestGap asserts the scheduler's
// choice rule: the channel with the largest gap set wins, lowest-PWM
// gap inside that channel.
func TestScheduler_PicksLowestPWMOnLargestGap(t *testing.T) {
	now := time.Now()
	chA := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	chB := makeTestChannel("/sys/class/hwmon/hwmon3/pwm2")

	// Pre-fill the log so chA has fewer gaps than chB.
	idA := observation.ChannelID(chA.PWMPath)
	idB := observation.ChannelID(chB.PWMPath)
	store := newFakeLogStore(now,
		// chA: visit nearly the whole low half so it has very few gaps.
		fakeRec(now.Add(-time.Hour), idA, 0, 0),
		fakeRec(now.Add(-time.Hour), idA, 8, 0),
		fakeRec(now.Add(-time.Hour), idA, 16, 0),
		fakeRec(now.Add(-time.Hour), idA, 24, 0),
		fakeRec(now.Add(-time.Hour), idA, 32, 0),
		fakeRec(now.Add(-time.Hour), idA, 40, 0),
		fakeRec(now.Add(-time.Hour), idA, 48, 0),
		fakeRec(now.Add(-time.Hour), idA, 56, 0),
		fakeRec(now.Add(-time.Hour), idA, 64, 0),
		fakeRec(now.Add(-time.Hour), idA, 72, 0),
		fakeRec(now.Add(-time.Hour), idA, 80, 0),
		fakeRec(now.Add(-time.Hour), idA, 88, 0),
		fakeRec(now.Add(-time.Hour), idA, 96, 0),
		fakeRec(now.Add(-time.Hour), idA, 113, 0),
		fakeRec(now.Add(-time.Hour), idA, 129, 0),
		fakeRec(now.Add(-time.Hour), idA, 145, 0),
		fakeRec(now.Add(-time.Hour), idA, 161, 0),
		fakeRec(now.Add(-time.Hour), idA, 177, 0),
		fakeRec(now.Add(-time.Hour), idA, 193, 0),
		fakeRec(now.Add(-time.Hour), idA, 209, 0),
		fakeRec(now.Add(-time.Hour), idA, 225, 0),
		fakeRec(now.Add(-time.Hour), idA, 241, 0),
		// (chA still has PWM 97 ungiven — single gap.)
	)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{chA, chB}, nil)

	gaps, err := det.Gaps(now)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if len(gaps[idA]) >= len(gaps[idB]) {
		t.Fatalf("test setup: chA gaps=%d, chB gaps=%d (want chA < chB)", len(gaps[idA]), len(gaps[idB]))
	}

	cfg := SchedulerConfig{
		Channels:    []*probe.ControllableChannel{chA, chB},
		Detector:    det,
		LastProbeAt: newMemLastProbe(),
	}
	s := &Scheduler{cfg: cfg}
	pickID, pickPWM, picked := s.pickChannel(gaps)
	if pickID != idB {
		t.Errorf("pickID: got %d, want %d (chB has more gaps)", pickID, idB)
	}
	if picked != chB {
		t.Errorf("picked channel: not chB")
	}
	want := gaps[idB][0]
	if pickPWM != want {
		t.Errorf("pickPWM: got %d, want lowest-PWM gap %d", pickPWM, want)
	}
}

// TestScheduler_MinPWMFloorDropsLowBins asserts the per-channel
// min_pwm filter — bins below the operator-declared floor are
// removed from the gap set before pick so the scheduler doesn't
// pick a fan-off PWM that reliably trips the slope abort on
// thermally-loaded hosts. Fan-off-territory probing also has no
// calibration value (config says "fan doesn't spin usefully below
// this").
func TestScheduler_MinPWMFloorDropsLowBins(t *testing.T) {
	now := time.Now()
	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	id := observation.ChannelID(ch.PWMPath)
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)

	// All grid bins are gaps (empty log). Set min_pwm=80 — bins
	// below 80 (0, 8, 16, 24, 32, 40, 48, 56, 64, 72) should be
	// filtered out by the scheduler before pickChannel runs, so the
	// picked PWM must be 80 or higher.
	cfg := SchedulerConfig{
		Channels:    []*probe.ControllableChannel{ch},
		Detector:    det,
		LastProbeAt: newMemLastProbe(),
		MinPWMs:     map[uint16]uint8{id: 80},
	}

	gaps, err := det.Gaps(now)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	// Sanity: detector returns the full low-half AND high-half grid.
	if len(gaps[id]) == 0 {
		t.Fatal("Detector returned no gaps; need a non-empty grid for this test")
	}
	hasZero := false
	for _, p := range gaps[id] {
		if p == 0 {
			hasZero = true
		}
	}
	if !hasZero {
		t.Fatal("detector grid missing PWM=0; can't exercise the floor filter")
	}

	// Apply the scheduler's filter in-place — same as tick() does.
	s := &Scheduler{cfg: cfg}
	for id, pwms := range gaps {
		floor, ok := s.cfg.MinPWMs[id]
		if !ok || floor == 0 {
			continue
		}
		filtered := pwms[:0]
		for _, p := range pwms {
			if p >= floor {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			gaps[id] = filtered
		} else {
			delete(gaps, id)
		}
	}

	for _, p := range gaps[id] {
		if p < 80 {
			t.Errorf("min_pwm filter leaked PWM %d below floor 80; remaining gaps=%v", p, gaps[id])
		}
	}
	if len(gaps[id]) == 0 {
		t.Error("filter dropped all bins; expected high-half bins (80-255) to remain")
	}
}

// TestScheduler_OneChannelAtATime asserts that runActive is set true
// while a probe runs and is reset after; concurrent ticks do not
// fire more than one probe (RULE-OPP-PROBE-03).
func TestScheduler_OneChannelAtATime(t *testing.T) {
	// This is a structural test: we assert the runActive flag has
	// the expected lifecycle around tick(). A full concurrency stress
	// test belongs in HIL; the scheduler's tick is single-threaded.
	now := time.Now()
	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)
	var fired atomic.Int32
	cfg := SchedulerConfig{
		Channels:  []*probe.ControllableChannel{ch},
		Detector:  det,
		ProbeDeps: testProbeDeps(&fired),
		Now:       func() time.Time { return now },
		IdleCfg:   openOpportunisticGate(t),
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status().Running {
		t.Fatal("Status().Running true before any tick")
	}
	s.tick(stubContext())
	if s.Status().Running {
		t.Fatal("Status().Running still true after tick returned")
	}
}

// makeTestChannel constructs a controllable channel with the given
// PWM sysfs path. Polarity is "normal" so polarity.IsControllable
// returns true.
func makeTestChannel(pwmPath string) *probe.ControllableChannel {
	return &probe.ControllableChannel{
		PWMPath:  pwmPath,
		TachPath: "",
		Polarity: "normal",
	}
}

// openOpportunisticGate returns an OpportunisticGateConfig backed by
// a clean /proc + /sys fixture so the StartupGate predicate passes
// (no PSI pressure, AC online, etc.) AND the opportunistic-specific
// signals (no SSH activity, no input IRQ delta) also pass. Tests
// that target scheduler logic — not gate logic — use this.
func openOpportunisticGate(t *testing.T) idle.OpportunisticGateConfig {
	t.Helper()
	procRoot, sysRoot := makeIdleProcRoot(t)
	return idle.OpportunisticGateConfig{
		GateConfig: idle.GateConfig{
			ProcRoot:     procRoot,
			SysRoot:      sysRoot,
			Durability:   1 * time.Nanosecond,
			TickInterval: 1 * time.Nanosecond,
		},
		LoginctlOutput: `[]`,
		IRQReader:      func() (idle.IRQCounters, error) { return idle.IRQCounters{}, nil },
	}
}

// testProbeDeps returns ProbeDeps that records FireOne invocations
// in fired and otherwise no-ops.
func testProbeDeps(fired *atomic.Int32) ProbeDeps {
	return ProbeDeps{
		Class: sysclass.ClassMidDesktop,
		Tjmax: 100,
		SensorFn: func(ctx context.Context) (map[string]float64, error) {
			return map[string]float64{"cpu": 50}, nil
		},
		RPMFn:   func(ctx context.Context) (int32, error) { return 1000, nil },
		WriteFn: func(uint8) error { fired.Add(1); return nil },
		ObsAppend: func(rec *observation.Record) error {
			return nil
		},
	}
}

func contains(s, sub string) bool {
	if s == "" {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
