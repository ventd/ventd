package smartmode_test

// Cross-spec integration test for spec-smart-mode.md §16 success criterion #2:
//
//   "ventd never reduces cooling on a channel during first contact without
//    thermal guard active."
//
// "Thermal guard" is the dT/dt + T_abs gate that runs alongside every Envelope
// C step (RULE-ENVELOPE-04, RULE-ENVELOPE-05). The contract has two parts:
//   (i)  during Envelope C, the guard is consulted for every step (sensorFn
//        is called, abort is possible);
//   (ii) when the guard fires, control transitions to Envelope D, which only
//        ramps UP from baseline — RULE-ENVELOPE-08 already pins this for the
//        unit; this test pins the C→D handoff and end-to-end invariant.
//
// RULE-ENVELOPE-07 covers the C-to-D state transition; RULE-ENVELOPE-08 covers
// D's refusal to write below baseline. This test stitches the pieces together
// and asserts the §16 invariant that a future refactor cannot violate while
// keeping individual rule subtests green.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/envelope"
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
	"github.com/vmihailenco/msgpack/v5"
)

// pwmTempFile creates a temp file containing the initial PWM value as decimal
// text and returns (path, writeFn). The writeFn writes a new uint8 value to
// the same file. Mirrors the helper in internal/envelope/envelope_test.go but
// duplicated here because internal/envelope test helpers are package-private.
func pwmTempFile(t *testing.T, v uint8) (string, func(uint8) error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pwm")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", v)), 0o644); err != nil {
		t.Fatalf("write pwm: %v", err)
	}
	return path, func(val uint8) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d", val)), 0o644)
	}
}

func openTestState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// noopIdleGate satisfies envelope.IdleGateFn unconditionally.
func noopIdleGate(_ context.Context, _ idle.GateConfig) (bool, idle.Reason, *idle.Snapshot) {
	return true, idle.ReasonOK, &idle.Snapshot{}
}

// TestSmartmode_NeverReduceCooling_C_Aborts_To_D_Above_Baseline drives the
// full Envelope C → D pipeline against a synthetic baseline-100 channel,
// triggers a thermal abort partway through C, and asserts:
//
//   - sensorFn was invoked at least once during C (guard active),
//   - the StepEvent log contains a C-phase abort,
//   - every PWM write recorded by D had PWMTarget >= baseline (no reduce),
//   - the actual write callback never received a value < baseline during D.
//
// The combination of these assertions is the §16 #2 contract end-to-end.
func TestSmartmode_NeverReduceCooling_C_Aborts_To_D_Above_Baseline(t *testing.T) {
	const baseline uint8 = 100

	pwmPath, writeFn := pwmTempFile(t, baseline)
	st := openTestState(t)

	// Sensor model: ramp temperature aggressively so dT/dt aborts quickly.
	// 5°C per call at SampleHz=1000 ⇒ effective rise rate >> any per-second
	// threshold, which guarantees probeC trips on the first sampled step
	// rather than the test wall-clocking through the whole sweep.
	var (
		sensorMu    sync.Mutex
		sensorCalls int
		curTemp     = 30.0
	)
	sensorFn := func(_ context.Context) (map[string]float64, error) {
		sensorMu.Lock()
		defer sensorMu.Unlock()
		sensorCalls++
		curTemp += 5.0
		return map[string]float64{"cpu": curTemp}, nil
	}

	// Track every PWM write seen by the bottom-of-stack writer. We tag
	// writes with the phase by snapshotting the per-channel KV state at
	// write time — phase=C while StateProbing/AbortedC, phase=D while
	// StateProbingD/CompleteD. Restoration of baseline at exit is also
	// captured but excluded from the "D writes" set via timestamp.
	type writeLog struct {
		val   uint8
		phase string // "C", "D", or "restore"
		ts    time.Time
	}
	var (
		writesMu sync.Mutex
		writes   []writeLog
	)
	wrappedWrite := func(v uint8) error {
		writesMu.Lock()
		phase := "unknown"
		kv, ok := envelope.LoadChannelKV(st.KV, pwmPath)
		if ok {
			switch kv.State {
			case envelope.StateProbing:
				phase = "C"
			case envelope.StateAbortedC:
				phase = "C-final"
			case envelope.StateProbingD, envelope.StateCompleteD:
				phase = "D"
			case envelope.StateCompleteC:
				phase = "C-final"
			}
		}
		writes = append(writes, writeLog{val: v, phase: phase, ts: time.Now()})
		writesMu.Unlock()
		return writeFn(v)
	}

	// Custom thresholds: short hold for fast tests, ascending PWM steps that
	// straddle the baseline so D has a non-empty step set above 100.
	thr := envelope.Thresholds{
		DTDtAbortCPerSec:     2.0,
		TAbsOffsetBelowTjmax: 12.0,
		AmbientHeadroomMin:   40.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70, 55, 40},
		Hold:                 1 * time.Millisecond,
		SampleHz:             1000,
	}

	p := envelope.NewProber(envelope.ProberConfig{
		State:      st,
		Class:      sysclass.ClassMidDesktop,
		Tjmax:      100.0,
		Ambient:    25.0,
		SensorFn:   sensorFn,
		RPMFn:      func(_ context.Context) (uint32, error) { return 1200, nil },
		IdleGate:   noopIdleGate,
		Logger:     slog.Default(),
		Thresholds: &thr,
	})

	ch := &probe.ControllableChannel{
		SourceID: "smartmode-§16-2",
		PWMPath:  pwmPath,
		Polarity: "normal",
	}

	// We accept whatever Probe returns — the success-vs-error split is
	// outside the §16 #2 contract; what matters is "no D write below baseline".
	// In practice this returns nil or ErrEnvelopeDInsufficient; both are fine.
	err := p.Probe(context.Background(), []*probe.ControllableChannel{ch}, []func(uint8) error{wrappedWrite})
	if err != nil && !errors.Is(err, envelope.ErrEnvelopeDInsufficient) {
		t.Fatalf("Probe returned unexpected error: %v", err)
	}

	// (i) Guard was active: sensorFn was consulted during C.
	sensorMu.Lock()
	calls := sensorCalls
	sensorMu.Unlock()
	if calls == 0 {
		t.Fatal("sensorFn was never called — thermal guard inactive during Envelope C, §16 #2 violated")
	}

	// (ii) StepEvent log contains a C-phase abort.
	abortSeenInC := false
	dStepWrites := []uint16{}
	cStepWrites := []uint16{}
	if err := st.Log.Iterate("envelope", time.Time{}, func(payload []byte) error {
		var ev envelope.StepEvent
		if err := msgpack.Unmarshal(payload, &ev); err != nil {
			return nil // skip torn / unparseable
		}
		if ev.EventType == envelope.EventProbeAbort && ev.Envelope == envelope.EnvelopeC {
			abortSeenInC = true
		}
		if ev.EventType == envelope.EventStepBegin {
			switch ev.Envelope {
			case envelope.EnvelopeC:
				cStepWrites = append(cStepWrites, ev.PWMTarget)
			case envelope.EnvelopeD:
				dStepWrites = append(dStepWrites, ev.PWMTarget)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("log iterate: %v", err)
	}
	if !abortSeenInC {
		t.Errorf("no Envelope-C probe_abort event in StepEvent log; thermal guard never tripped")
	}
	if len(cStepWrites) == 0 {
		t.Errorf("no Envelope-C step_begin events recorded; C never executed")
	}

	// (iii) Every D-phase write target ≥ baseline. RULE-ENVELOPE-08 unit-tests
	// this for probeD in isolation; here we re-verify across the full C→D
	// handoff so a regression that lets D inherit C's descending steps would
	// fail this assertion even if RULE-ENVELOPE-08's narrow subtest still
	// passed.
	for _, v := range dStepWrites {
		if v < uint16(baseline) {
			t.Errorf("Envelope D wrote PWM=%d, below baseline %d — §16 #2 violated", v, baseline)
		}
	}

	// (iv) The bottom-of-stack writer never received a sub-baseline value
	// while KV reported StateProbingD / StateCompleteD. The KV-snapshot
	// phase tag is best-effort (KV updates lag the actual write by a few
	// nanoseconds) but covers writes that didn't make it into the StepEvent
	// log (e.g. the final restore is captured under "C-final").
	writesMu.Lock()
	defer writesMu.Unlock()
	for _, w := range writes {
		if w.phase == "D" && w.val < baseline {
			t.Errorf("D-phase write hit the bottom of the stack with value %d < baseline %d", w.val, baseline)
		}
	}
}
