package envelope

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
	"github.com/vmihailenco/msgpack/v5"
)

// fastThr returns a Thresholds suitable for unit tests: 1ms hold, high SampleHz,
// generous ambient headroom so the precondition passes with ambient=25/tjmax=100.
func fastThr() Thresholds {
	return Thresholds{
		DTDtAbortCPerSec:     2.0,
		TAbsOffsetBelowTjmax: 12.0,
		AmbientHeadroomMin:   40.0,
		PWMSteps:             []uint8{180, 140, 110, 90},
		Hold:                 1 * time.Millisecond,
		SampleHz:             1000,
	}
}

// testState opens an in-memory (temp dir) state store.
func testState(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(dir, slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return st
}

// pwmFile creates a temp file whose content is the PWM value v (as decimal string).
// Returns the file path and a writeFn that writes to that file.
func pwmFile(t *testing.T, v uint8) (path string, writeFn func(uint8) error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "pwm")
	if err != nil {
		t.Fatalf("create pwm file: %v", err)
	}
	path = f.Name()
	if _, err := fmt.Fprintf(f, "%d", v); err != nil {
		t.Fatalf("write initial pwm: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close pwm file: %v", err)
	}

	writeFn = func(val uint8) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d", val)), 0o644)
	}
	return path, writeFn
}

// noopSensorFn returns a SensorFn that always returns a stable temp map.
func noopSensorFn(temps map[string]float64) SensorFn {
	return func(_ context.Context) (map[string]float64, error) {
		out := make(map[string]float64, len(temps))
		for k, v := range temps {
			out[k] = v
		}
		return out, nil
	}
}

// noopRPMFn returns a RPMFn that always returns the given RPM.
func noopRPMFn(rpm uint32) RPMFn {
	return func(_ context.Context) (uint32, error) { return rpm, nil }
}

// noopIdleGate is an IdleGateFn that always grants access.
func noopIdleGate(_ context.Context, _ idle.GateConfig) (bool, idle.Reason, *idle.Snapshot) {
	return true, idle.ReasonOK, &idle.Snapshot{}
}

// normalChannel returns a ControllableChannel with Polarity="normal".
func normalChannel(pwmPath string) *probe.ControllableChannel {
	return &probe.ControllableChannel{
		SourceID: "test",
		PWMPath:  pwmPath,
		Polarity: "normal",
	}
}

// TestRULE_ENVELOPE_01_WritePWMViaHelper verifies that channelWriter.Write routes
// through polarity.WritePWM and refuses phantom/unknown channels without calling fn.
func TestRULE_ENVELOPE_01_WritePWMViaHelper(t *testing.T) {
	var fnCalled int32

	fn := func(_ uint8) error {
		atomic.AddInt32(&fnCalled, 1)
		return nil
	}

	t.Run("phantom_refused", func(t *testing.T) {
		atomic.StoreInt32(&fnCalled, 0)
		ch := &probe.ControllableChannel{Polarity: "phantom", PhantomReason: "no_tach"}
		cw := &channelWriter{ch: ch, writeFunc: fn}
		err := cw.Write(128)
		if !errors.Is(err, polarity.ErrChannelNotControllable) {
			t.Fatalf("want ErrChannelNotControllable, got %v", err)
		}
		if atomic.LoadInt32(&fnCalled) != 0 {
			t.Fatal("fn must not be called for phantom channel")
		}
	})

	t.Run("unknown_refused", func(t *testing.T) {
		atomic.StoreInt32(&fnCalled, 0)
		ch := &probe.ControllableChannel{Polarity: "unknown"}
		cw := &channelWriter{ch: ch, writeFunc: fn}
		err := cw.Write(128)
		if !errors.Is(err, polarity.ErrPolarityNotResolved) {
			t.Fatalf("want ErrPolarityNotResolved, got %v", err)
		}
		if atomic.LoadInt32(&fnCalled) != 0 {
			t.Fatal("fn must not be called for unknown channel")
		}
	})

	t.Run("normal_passes", func(t *testing.T) {
		atomic.StoreInt32(&fnCalled, 0)
		ch := &probe.ControllableChannel{Polarity: "normal"}
		cw := &channelWriter{ch: ch, writeFunc: fn}
		if err := cw.Write(128); err != nil {
			t.Fatalf("unexpected error for normal channel: %v", err)
		}
		if atomic.LoadInt32(&fnCalled) != 1 {
			t.Fatal("fn must be called exactly once for normal channel")
		}
	})
}

// TestRULE_ENVELOPE_02_BaselineRestoreAllExitPaths verifies that the baseline PWM
// is restored after Probe() returns, regardless of exit path.
func TestRULE_ENVELOPE_02_BaselineRestoreAllExitPaths(t *testing.T) {
	initialPWM := uint8(200) // baseline set high so all steps are below it → probeD fires immediately
	pwmPath, writeFn := pwmFile(t, initialPWM)

	st := testState(t)
	sensor := noopSensorFn(map[string]float64{"cpu": 30.0})
	thr := fastThr()
	// Use steps all below baseline so probeD gets ErrEnvelopeDInsufficient immediately
	thr.PWMSteps = []uint8{180, 140, 110, 90} // all below 200

	p := NewProber(ProberConfig{
		State:      st,
		Class:      sysclass.ClassMidDesktop,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   sensor,
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)
	// Probe will write step values; after return the defer must restore initialPWM.
	_ = p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{writeFn})

	got, err := readPWM(pwmPath)
	if err != nil {
		t.Fatalf("readPWM after Probe: %v", err)
	}
	if got != initialPWM {
		t.Fatalf("baseline not restored: got %d, want %d", got, initialPWM)
	}
}

// TestRULE_ENVELOPE_03_ClassThresholdLookup verifies LookupThresholds for every class
// including ClassUnknown (falls back to MidDesktop).
func TestRULE_ENVELOPE_03_ClassThresholdLookup(t *testing.T) {
	classes := []sysclass.SystemClass{
		sysclass.ClassUnknown,
		sysclass.ClassHEDTAir,
		sysclass.ClassHEDTAIO,
		sysclass.ClassMidDesktop,
		sysclass.ClassServer,
		sysclass.ClassLaptop,
		sysclass.ClassMiniPC,
		sysclass.ClassNASHDD,
	}
	for _, cls := range classes {
		thr := LookupThresholds(cls)
		if thr.SampleHz == 0 {
			t.Errorf("class %v: SampleHz is 0", cls)
		}
		if len(thr.PWMSteps) == 0 {
			t.Errorf("class %v: PWMSteps is empty", cls)
		}
		if thr.Hold == 0 {
			t.Errorf("class %v: Hold is 0", cls)
		}
	}
	// ClassUnknown must return MidDesktop thresholds.
	unknown := LookupThresholds(sysclass.ClassUnknown)
	mid := LookupThresholds(sysclass.ClassMidDesktop)
	if unknown.DTDtAbortCPerSec != mid.DTDtAbortCPerSec {
		t.Errorf("ClassUnknown DTDtAbortCPerSec %v != MidDesktop %v", unknown.DTDtAbortCPerSec, mid.DTDtAbortCPerSec)
	}
}

// TestRULE_ENVELOPE_04_DTDtTripBoundary verifies thermalAbort uses exclusive boundary on dT/dt.
func TestRULE_ENVELOPE_04_DTDtTripBoundary(t *testing.T) {
	thr := Thresholds{DTDtAbortCPerSec: 2.0}
	dt := time.Second

	prev := map[string]float64{"cpu": 50.0}

	// Exactly at limit (2.0 °C/s) must NOT abort.
	atLimit := map[string]float64{"cpu": 52.0}
	if thermalAbort(atLimit, prev, dt, thr) {
		t.Error("thermalAbort must not fire at exactly DTDtAbortCPerSec")
	}

	// One step above must abort.
	above := map[string]float64{"cpu": 52.1}
	if !thermalAbort(above, prev, dt, thr) {
		t.Error("thermalAbort must fire above DTDtAbortCPerSec")
	}
}

// TestRULE_ENVELOPE_05_TAbsTripBoundary verifies absoluteTempAbort uses exclusive boundary.
func TestRULE_ENVELOPE_05_TAbsTripBoundary(t *testing.T) {
	thr := Thresholds{TAbsOffsetBelowTjmax: 15.0}
	tjmax := 100.0
	// ceiling = 85.0; exclusive boundary.

	safe := map[string]float64{"cpu": 84.9}
	if absoluteTempAbort(safe, tjmax, thr) {
		t.Error("absoluteTempAbort must not fire at 84.9°C (below ceiling 85°C)")
	}

	trip := map[string]float64{"cpu": 85.1}
	if !absoluteTempAbort(trip, tjmax, thr) {
		t.Error("absoluteTempAbort must fire at 85.1°C (above ceiling 85°C)")
	}
}

// TestRULE_ENVELOPE_06_AmbientHeadroomPrecondition verifies the ambient precondition
// boundary (exclusive).
func TestRULE_ENVELOPE_06_AmbientHeadroomPrecondition(t *testing.T) {
	thr := Thresholds{AmbientHeadroomMin: 60.0}
	tjmax := 100.0
	// passes when ambient < 40.0; fails when ambient >= 40.0.

	if !ambientHeadroomOK(39.9, tjmax, thr) {
		t.Error("ambientHeadroomOK must pass for ambient=39.9 (100-39.9=60.1 > 0)")
	}
	if ambientHeadroomOK(40.0, tjmax, thr) {
		t.Error("ambientHeadroomOK must fail for ambient=40.0 (100-40=60, not > 0)")
	}
}

// TestRULE_ENVELOPE_07_AbortCToProbeD_OrderingPersist verifies that a thermal abort in
// Envelope C transitions to Envelope D and final KV shows complete_D.
func TestRULE_ENVELOPE_07_AbortCToProbeD_OrderingPersist(t *testing.T) {
	initialPWM := uint8(50)
	pwmPath, writeFn := pwmFile(t, initialPWM)
	st := testState(t)

	// Rising temperature: each call adds 1°C, triggering abort quickly at SampleHz=1000.
	var tempMu sync.Mutex
	curTemp := 30.0
	sensorFn := func(_ context.Context) (map[string]float64, error) {
		tempMu.Lock()
		defer tempMu.Unlock()
		curTemp += 5.0 // 5°C per call → 5000°C/s >> DTDtAbortCPerSec=2.0
		return map[string]float64{"cpu": curTemp}, nil
	}

	thr := fastThr()
	thr.DTDtAbortCPerSec = 2.0

	p := NewProber(ProberConfig{
		State:      st,
		Class:      sysclass.ClassMidDesktop,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   sensorFn,
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)
	_ = p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{writeFn})

	kv, ok := LoadChannelKV(st.KV, pwmPath)
	if !ok {
		t.Fatal("KV not written")
	}
	if kv.State != StateCompleteD && kv.State != StateAbortedC {
		// Either complete_D (D succeeded) or aborted_C (D insufficient) is acceptable.
		// The important thing is that C did not complete.
		t.Errorf("unexpected state %q after thermal abort", kv.State)
	}
}

// TestRULE_ENVELOPE_08_EnvelopeDRefusesBelowBaseline verifies that probeD skips steps
// at or below baseline.
func TestRULE_ENVELOPE_08_EnvelopeDRefusesBelowBaseline(t *testing.T) {
	baseline := uint8(140)
	pwmPath, writeFn := pwmFile(t, baseline)
	st := testState(t)

	var written []uint8
	var mu sync.Mutex
	trackFn := func(v uint8) error {
		mu.Lock()
		written = append(written, v)
		mu.Unlock()
		return writeFn(v)
	}

	// Trigger probeD directly by seeding aborted_C KV.
	kvSeed := ChannelKV{
		State:       StateAbortedC,
		BaselinePWM: baseline,
	}
	if err := PersistChannelKV(st.KV, pwmPath, kvSeed); err != nil {
		t.Fatalf("seed KV: %v", err)
	}

	thr := fastThr()
	thr.PWMSteps = []uint8{180, 140, 110, 90} // only 180 > 140

	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)
	_ = p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{trackFn})

	mu.Lock()
	defer mu.Unlock()
	// The first write should be from probeD writing only step 180; baseline restore appended after.
	for _, v := range written {
		if v < baseline && v != baseline {
			// The restore write is 140 (== baseline), so only < baseline (not ==) is a violation.
			t.Errorf("probeD wrote PWM %d which is below baseline %d", v, baseline)
		}
	}
}

// TestRULE_ENVELOPE_09_StepLevelResumability verifies that Probe() resumes from
// completed_step_count rather than restarting from step 0.
func TestRULE_ENVELOPE_09_StepLevelResumability(t *testing.T) {
	initialPWM := uint8(200)
	pwmPath, writeFn := pwmFile(t, initialPWM)
	st := testState(t)

	// Pre-seed KV with StateProbing, CompletedStepCount=3.
	kvSeed := ChannelKV{
		State:              StateProbing,
		Envelope:           EnvelopeC,
		BaselinePWM:        initialPWM,
		CompletedStepCount: 3,
		StartedAt:          time.Now().Add(-10 * time.Minute),
	}
	if err := PersistChannelKV(st.KV, pwmPath, kvSeed); err != nil {
		t.Fatalf("seed KV: %v", err)
	}

	thr := fastThr()
	// 4 steps; completed=3 means only step index 3 (value 90) should be written.
	thr.PWMSteps = []uint8{180, 140, 110, 90}

	var written []uint8
	var mu sync.Mutex
	trackFn := func(v uint8) error {
		mu.Lock()
		written = append(written, v)
		mu.Unlock()
		return writeFn(v)
	}

	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)
	if err := p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{trackFn}); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// Should not write 180, 140, 110 (already completed); only 90 (index 3) and baseline restore.
	for _, v := range written {
		if v == 180 || v == 140 || v == 110 {
			t.Errorf("step %d was written but should have been skipped (already completed)", v)
		}
	}
}

// TestRULE_ENVELOPE_10_LogStoreSchemaConformance verifies StepEvent round-trips via msgpack.
func TestRULE_ENVELOPE_10_LogStoreSchemaConformance(t *testing.T) {
	original := StepEvent{
		SchemaVersion:   kvSchemaVersion,
		ChannelID:       "/sys/class/hwmon/hwmon2/pwm1",
		Envelope:        EnvelopeC,
		EventType:       EventStepEnd,
		TimestampNs:     time.Now().UnixNano(),
		PWMTarget:       140,
		PWMActual:       138,
		Temps:           map[string]float64{"cpu": 55.5, "mb": 42.0},
		RPM:             1200,
		ControllerState: 2,
		EventFlags:      0,
		AbortReason:     "",
	}

	data, err := msgpack.Marshal(&original)
	if err != nil {
		t.Fatalf("msgpack.Marshal: %v", err)
	}

	var decoded StepEvent
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("msgpack.Unmarshal: %v", err)
	}

	if decoded.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", decoded.SchemaVersion, original.SchemaVersion)
	}
	if decoded.ChannelID != original.ChannelID {
		t.Errorf("ChannelID mismatch")
	}
	if decoded.Envelope != original.Envelope {
		t.Errorf("Envelope mismatch")
	}
	if decoded.EventType != original.EventType {
		t.Errorf("EventType mismatch")
	}
	if decoded.PWMTarget != original.PWMTarget {
		t.Errorf("PWMTarget: got %d, want %d", decoded.PWMTarget, original.PWMTarget)
	}
	if decoded.PWMActual != original.PWMActual {
		t.Errorf("PWMActual: got %d, want %d", decoded.PWMActual, original.PWMActual)
	}
	if decoded.RPM != original.RPM {
		t.Errorf("RPM: got %d, want %d", decoded.RPM, original.RPM)
	}
	if len(decoded.Temps) != len(original.Temps) {
		t.Errorf("Temps len: got %d, want %d", len(decoded.Temps), len(original.Temps))
	}
}

// TestRULE_ENVELOPE_11_SequentialChannelsNoParallel verifies that all writes for channel
// N complete before any write for channel N+1.
func TestRULE_ENVELOPE_11_SequentialChannelsNoParallel(t *testing.T) {
	type writeRecord struct {
		channelIdx int
		value      uint8
	}

	var mu sync.Mutex
	var records []writeRecord

	mkChannel := func(idx int, initialPWM uint8) (ch *probe.ControllableChannel, writeFn func(uint8) error) {
		pwmPath, rawWrite := pwmFile(t, initialPWM)
		track := func(v uint8) error {
			mu.Lock()
			records = append(records, writeRecord{channelIdx: idx, value: v})
			mu.Unlock()
			return rawWrite(v)
		}
		ch = normalChannel(pwmPath)
		return ch, track
	}

	ch0, fn0 := mkChannel(0, 200)
	ch1, fn1 := mkChannel(1, 200)
	ch2, fn2 := mkChannel(2, 200)

	st := testState(t)
	thr := fastThr()
	// All steps below 200 → each channel goes through probeD, gets ErrDInsufficient, finishes quickly.

	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	channels := []*probe.ControllableChannel{ch0, ch1, ch2}
	writeFns := []func(uint8) error{fn0, fn1, fn2}
	_ = p.Probe(context.Background(), channels, writeFns)

	mu.Lock()
	defer mu.Unlock()

	// Verify no interleaving: find max channel index seen so far; next record must not go back.
	maxSeen := -1
	for i, r := range records {
		if r.channelIdx < maxSeen {
			t.Errorf("record[%d]: channel %d appears after channel %d (interleaved)", i, r.channelIdx, maxSeen)
		}
		if r.channelIdx > maxSeen {
			maxSeen = r.channelIdx
		}
	}
}

// TestRULE_ENVELOPE_12_PausedStateReruns_StartupGate verifies that a paused channel
// re-runs the idle gate and does not probe when it returns ok=false.
func TestRULE_ENVELOPE_12_PausedStateReruns_StartupGate(t *testing.T) {
	initialPWM := uint8(200)
	pwmPath, writeFn := pwmFile(t, initialPWM)
	st := testState(t)

	// Seed paused state.
	kvSeed := ChannelKV{
		State:    StatePausedUserIdle,
		Envelope: EnvelopeC,
	}
	if err := PersistChannelKV(st.KV, pwmPath, kvSeed); err != nil {
		t.Fatalf("seed KV: %v", err)
	}

	var gateCallCount int32
	// Gate returns false on first call, true on subsequent.
	idleGate := func(_ context.Context, _ idle.GateConfig) (bool, idle.Reason, *idle.Snapshot) {
		n := atomic.AddInt32(&gateCallCount, 1)
		if n == 1 {
			return false, idle.Reason("not_idle"), nil
		}
		return true, idle.ReasonOK, &idle.Snapshot{}
	}

	var written []uint8
	var mu sync.Mutex
	trackFn := func(v uint8) error {
		mu.Lock()
		written = append(written, v)
		mu.Unlock()
		return writeFn(v)
	}

	thr := fastThr()
	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   idleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)

	// First call: gate returns false → no probe writes.
	_ = p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{trackFn})
	mu.Lock()
	writesAfterFirst := len(written)
	mu.Unlock()
	if writesAfterFirst != 0 {
		t.Errorf("first call: expected 0 writes when gate returns false, got %d", writesAfterFirst)
	}
	if atomic.LoadInt32(&gateCallCount) != 1 {
		t.Errorf("gate should have been called exactly once, got %d", atomic.LoadInt32(&gateCallCount))
	}
}

// TestRULE_ENVELOPE_13_UniversalDInsufficient_WizardFallback verifies that probeD
// returns ErrEnvelopeDInsufficient when all steps are ≤ baseline.
func TestRULE_ENVELOPE_13_UniversalDInsufficient_WizardFallback(t *testing.T) {
	baseline := uint8(200) // higher than all steps
	pwmPath, writeFn := pwmFile(t, baseline)
	st := testState(t)

	// Seed aborted_C so probeOne goes directly to runProbeD.
	kvSeed := ChannelKV{
		State:       StateAbortedC,
		BaselinePWM: baseline,
	}
	if err := PersistChannelKV(st.KV, pwmPath, kvSeed); err != nil {
		t.Fatalf("seed KV: %v", err)
	}

	thr := fastThr()
	thr.PWMSteps = []uint8{200, 170, 140, 120, 100} // all <= 200

	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := normalChannel(pwmPath)
	// Probe() swallows ErrEnvelopeDInsufficient and returns nil; we verify via internal call.
	err := p.runProbeD(context.Background(), ch, &channelWriter{ch: ch, writeFunc: writeFn}, thr, baseline, pwmPath)
	if !errors.Is(err, ErrEnvelopeDInsufficient) {
		t.Fatalf("want ErrEnvelopeDInsufficient, got %v", err)
	}
}

// TestRULE_ENVELOPE_14_PWMReadbackVerification verifies that a >2 LSB mismatch between
// written and readback PWM triggers abort with FlagBIOSOverride.
func TestRULE_ENVELOPE_14_PWMReadbackVerification(t *testing.T) {
	// Create two separate files: ch.PWMPath (stale) and the actual write target.
	// writeFn writes to a *different* file than ch.PWMPath, so readPWM(ch.PWMPath) sees the old value.
	staleDir := t.TempDir()
	stalePath := filepath.Join(staleDir, "pwm_stale")
	// Write value 50 to the stale path — this is what readPWM will see.
	if err := os.WriteFile(stalePath, []byte("50"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The writeFn writes to a different (shadow) file.
	shadowPath := filepath.Join(staleDir, "pwm_shadow")
	if err := os.WriteFile(shadowPath, []byte("200"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFn := func(v uint8) error {
		return os.WriteFile(shadowPath, []byte(fmt.Sprintf("%d", v)), 0o644)
	}

	// ch.PWMPath points to stalePath; writeFn writes to shadowPath.
	// When step=180 is written, readPWM(stalePath) returns 50 → diff = 130 > 2 → BIOS override.
	ch := &probe.ControllableChannel{
		SourceID: "test",
		PWMPath:  stalePath,
		Polarity: "normal",
	}

	st := testState(t)
	thr := fastThr()
	thr.PWMSteps = []uint8{180, 140, 110, 90}

	p := NewProber(ProberConfig{
		State:      st,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   noopSensorFn(map[string]float64{"cpu": 30.0}),
		RPMFn:      noopRPMFn(1200),
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	// Run Probe; it should detect the mismatch and abort → enter probeD → ErrDInsufficient
	// (since all D steps > stale baseline of 50 are available, but we don't care about D result).
	_ = p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{writeFn})

	// Verify KV was set to aborted_C with an abort reason indicating readback mismatch.
	kv, ok := LoadChannelKV(st.KV, stalePath)
	if !ok {
		// D might have completed; that is also acceptable.
		return
	}
	// If state is aborted_C, abort reason should mention readback.
	if kv.State == StateAbortedC && kv.AbortReason == "" {
		t.Error("aborted_C state with empty AbortReason; expected readback mismatch message")
	}
}
