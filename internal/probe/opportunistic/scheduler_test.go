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

// TestScheduler_FirstProbeDelayedBy24h asserts that with a marker
// file aged less than FirstInstallDelay, the scheduler does not fire
// (RULE-OPP-PROBE-07).
func TestScheduler_FirstProbeDelayedBy24h(t *testing.T) {
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
		Now:                    func() time.Time { return now.Add(2 * time.Hour) }, // < 24h
		ProbeDeps:              testProbeDeps(&fired),
		IdleCfg:                openOpportunisticGate(t),
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}

	s.tick(stubContext())

	if fired.Load() != 0 {
		t.Fatal("scheduler fired before first-install delay elapsed")
	}
	if got := string(idle.ReasonOpportunisticBootWindow); !contains(s.Status().LastReason, got) {
		t.Errorf("LastReason: got %q, want substring %q", s.Status().LastReason, got)
	}
	_ = chID
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
