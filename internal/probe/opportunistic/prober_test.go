package opportunistic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/envelope"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/sysclass"
)

// makePWMFile creates a temporary file that simulates a hwmon pwm
// sysfs file initialised to the given baseline byte.
func makePWMFile(t *testing.T, baseline uint8) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pwm1")
	if err := os.WriteFile(path, []byte{'1', '0', '0'}, 0o644); err != nil {
		t.Fatalf("create pwm: %v", err)
	}
	// Overwrite with baseline.
	if err := os.WriteFile(path, []byte(formatUint8(baseline)), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return path
}

func formatUint8(v uint8) string {
	if v == 0 {
		return "0"
	}
	var b [3]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// TestProber_FullCycle_RestoresController verifies that on a clean
// 30 s probe the writer ends with the controller-managed baseline,
// not the gap PWM (RULE-OPP-PROBE-10).
func TestProber_FullCycle_RestoresController(t *testing.T) {
	const baseline uint8 = 145
	const gap uint8 = 32
	pwmPath := makePWMFile(t, baseline)
	ch := &probe.ControllableChannel{PWMPath: pwmPath, Polarity: "normal"}

	var writes []uint8
	deps := ProbeDeps{
		Class: sysclass.ClassMidDesktop,
		Tjmax: 100,
		SensorFn: func(ctx context.Context) (map[string]float64, error) {
			return map[string]float64{"cpu": 55}, nil
		},
		RPMFn:   func(ctx context.Context) (int32, error) { return 1100, nil },
		WriteFn: func(v uint8) error { writes = append(writes, v); return nil },
		ObsAppend: func(rec *observation.Record) error {
			if rec.EventFlags&observation.EventFlag_OPPORTUNISTIC_PROBE == 0 {
				t.Errorf("emitted record missing OPPORTUNISTIC_PROBE flag")
			}
			return nil
		},
		Now: func() time.Time { return time.Now() },
		Thresholds: &envelope.Thresholds{
			DTDtAbortCPerSec:     2.0,
			TAbsOffsetBelowTjmax: 15,
			SampleHz:             100,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Override ProbeDuration via context-deadline shortcut: we don't
	// want a real 30-second hold in unit tests. The prober honours
	// ctx.Done(), so a short deadline returns ctx.DeadlineExceeded
	// — that exercises the same defer / restore path as a normal
	// completion.
	err := FireOne(ctx, ch, gap, deps)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FireOne: unexpected err %v", err)
	}

	if len(writes) < 2 {
		t.Fatalf("writes: got %d, want >= 2 (one gap, one restore)", len(writes))
	}
	if writes[0] != gap {
		t.Errorf("first write: got %d, want gap %d", writes[0], gap)
	}
	last := writes[len(writes)-1]
	if last != baseline {
		t.Errorf("last write: got %d, want baseline %d", last, baseline)
	}
}

// TestProber_AbortPath_RestoresController verifies that an absolute-
// temperature trip aborts the probe and the writer ends with baseline
// not gap (RULE-OPP-PROBE-05, RULE-OPP-PROBE-10).
func TestProber_AbortPath_RestoresController(t *testing.T) {
	const baseline uint8 = 130
	const gap uint8 = 16
	pwmPath := makePWMFile(t, baseline)
	ch := &probe.ControllableChannel{PWMPath: pwmPath, Polarity: "normal"}

	var sensorCalls atomic.Int32
	var writes []uint8

	deps := ProbeDeps{
		Class: sysclass.ClassMidDesktop,
		Tjmax: 100,
		SensorFn: func(ctx context.Context) (map[string]float64, error) {
			n := sensorCalls.Add(1)
			// Second sample reports a temperature far above the
			// abort ceiling (Tjmax 100 - offset 15 = 85).
			if n >= 2 {
				return map[string]float64{"cpu": 95}, nil
			}
			return map[string]float64{"cpu": 50}, nil
		},
		RPMFn:   func(ctx context.Context) (int32, error) { return 1000, nil },
		WriteFn: func(v uint8) error { writes = append(writes, v); return nil },
		ObsAppend: func(rec *observation.Record) error {
			// Abort path must set both flags.
			if rec.EventFlags&observation.EventFlag_OPPORTUNISTIC_PROBE == 0 ||
				rec.EventFlags&observation.EventFlag_ENVELOPE_C_ABORT == 0 {
				t.Errorf("abort record missing expected flags: 0x%x", rec.EventFlags)
			}
			return nil
		},
		Now: func() time.Time { return time.Now() },
		Thresholds: &envelope.Thresholds{
			DTDtAbortCPerSec:     2.0,
			TAbsOffsetBelowTjmax: 15,
			SampleHz:             1000, // hot loop so abort fires fast in test
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := FireOne(ctx, ch, gap, deps)
	if !errors.Is(err, ErrProbeAborted) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FireOne: got %v, want ErrProbeAborted or DeadlineExceeded", err)
	}

	if len(writes) == 0 {
		t.Fatal("no writes captured")
	}
	last := writes[len(writes)-1]
	if last != baseline {
		t.Errorf("last write after abort: got %d, want baseline %d", last, baseline)
	}
}

// TestProber_CtxCancel_RestoresController verifies that a cancelled
// context returns immediately and still restores baseline
// (RULE-OPP-PROBE-10).
func TestProber_CtxCancel_RestoresController(t *testing.T) {
	const baseline uint8 = 110
	const gap uint8 = 8
	pwmPath := makePWMFile(t, baseline)
	ch := &probe.ControllableChannel{PWMPath: pwmPath, Polarity: "normal"}

	var writes []uint8
	deps := ProbeDeps{
		Class:     sysclass.ClassMidDesktop,
		Tjmax:     100,
		SensorFn:  func(ctx context.Context) (map[string]float64, error) { return nil, nil },
		RPMFn:     func(ctx context.Context) (int32, error) { return 0, nil },
		WriteFn:   func(v uint8) error { writes = append(writes, v); return nil },
		ObsAppend: func(rec *observation.Record) error { return nil },
		Now:       func() time.Time { return time.Now() },
		Thresholds: &envelope.Thresholds{
			DTDtAbortCPerSec: 2.0,
			SampleHz:         1000,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := FireOne(ctx, ch, gap, deps)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FireOne: got %v, want context.Canceled", err)
	}
	if len(writes) < 2 {
		t.Fatalf("writes: got %d, want >= 2", len(writes))
	}
	if writes[len(writes)-1] != baseline {
		t.Errorf("last write: got %d, want baseline %d", writes[len(writes)-1], baseline)
	}
}

// TestProber_EmitsRecordWithProbeFlag verifies the observation record
// emitted by FireOne carries EventFlag_OPPORTUNISTIC_PROBE
// (RULE-OPP-PROBE-11).
func TestProber_EmitsRecordWithProbeFlag(t *testing.T) {
	pwmPath := makePWMFile(t, 100)
	ch := &probe.ControllableChannel{PWMPath: pwmPath, Polarity: "normal"}

	var captured *observation.Record
	deps := ProbeDeps{
		Class:     sysclass.ClassMidDesktop,
		Tjmax:     100,
		SensorFn:  func(ctx context.Context) (map[string]float64, error) { return map[string]float64{"cpu": 50}, nil },
		RPMFn:     func(ctx context.Context) (int32, error) { return 1000, nil },
		WriteFn:   func(uint8) error { return nil },
		ObsAppend: func(rec *observation.Record) error { captured = rec; return nil },
		Now:       func() time.Time { return time.Now() },
		Thresholds: &envelope.Thresholds{
			DTDtAbortCPerSec: 2.0,
			SampleHz:         1000,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = FireOne(ctx, ch, 24, deps)

	if captured == nil {
		t.Fatal("ObsAppend never called")
	}
	if captured.EventFlags&observation.EventFlag_OPPORTUNISTIC_PROBE == 0 {
		t.Errorf("EventFlags 0x%x missing OPPORTUNISTIC_PROBE", captured.EventFlags)
	}
	if captured.PWMWritten != 24 {
		t.Errorf("PWMWritten: got %d, want 24", captured.PWMWritten)
	}
}

// TestProber_DurationWithinTolerance verifies that with the locked
// 30 s ProbeDuration constant and ProbeJitterTolerance = 5 s, a
// future implementation that overshoots by > 5 s would fail this
// test (RULE-OPP-PROBE-02).
//
// We don't actually run a 30 s probe here — that's an HIL concern.
// Instead we sanity-check the constants themselves.
func TestProber_DurationWithinTolerance(t *testing.T) {
	if ProbeDuration != 30*time.Second {
		t.Errorf("ProbeDuration: got %s, want 30s", ProbeDuration)
	}
	if ProbeJitterTolerance != 5*time.Second {
		t.Errorf("ProbeJitterTolerance: got %s, want 5s", ProbeJitterTolerance)
	}
}

// TestProber_RoutesViaPolarityWrite verifies that an inverted-polarity
// channel sees the PWM byte XOR-flipped through polarity.WritePWM
// (RULE-OPP-PROBE-04).
func TestProber_RoutesViaPolarityWrite(t *testing.T) {
	pwmPath := makePWMFile(t, 145)
	ch := &probe.ControllableChannel{PWMPath: pwmPath, Polarity: "inverted"}

	var seen []uint8
	deps := ProbeDeps{
		Class:     sysclass.ClassMidDesktop,
		Tjmax:     100,
		SensorFn:  func(ctx context.Context) (map[string]float64, error) { return nil, nil },
		RPMFn:     func(ctx context.Context) (int32, error) { return 0, nil },
		WriteFn:   func(v uint8) error { seen = append(seen, v); return nil },
		ObsAppend: func(rec *observation.Record) error { return nil },
		Now:       func() time.Time { return time.Now() },
		Thresholds: &envelope.Thresholds{
			DTDtAbortCPerSec: 2.0,
			SampleHz:         1000,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = FireOne(ctx, ch, 32, deps)

	if len(seen) < 2 {
		t.Fatalf("seen %d writes, want >= 2", len(seen))
	}
	// Inverted polarity: 32 -> 255-32 = 223; baseline 145 -> 255-145 = 110.
	if seen[0] != 223 {
		t.Errorf("first write: got %d, want 223 (255-32)", seen[0])
	}
	if seen[len(seen)-1] != 110 {
		t.Errorf("last write: got %d, want 110 (255-145)", seen[len(seen)-1])
	}
}

// TestScheduler_FiresOnlyAfterGatePasses asserts that a refusing
// idle gate prevents FireOne from being called (RULE-OPP-PROBE-01).
func TestScheduler_FiresOnlyAfterGatePasses(t *testing.T) {
	now := time.Now()
	ch := makeTestChannel("/sys/class/hwmon/hwmon3/pwm1")
	store := newFakeLogStore(now)
	rd := observation.NewReader(store)
	det := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)

	var fired atomic.Int32

	// IdleCfg with always-recent SSH session — gate refuses.
	idleCfgRefuse := openOpportunisticGate(t)
	idleCfgRefuse.LoginctlOutput = `[{"session":"5","state":"active","remote":true,"idle":false}]`

	cfg := SchedulerConfig{
		Channels:  []*probe.ControllableChannel{ch},
		Detector:  det,
		ProbeDeps: testProbeDeps(&fired),
		Now:       func() time.Time { return now },
		IdleCfg:   idleCfgRefuse,
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.tick(stubContext())

	if fired.Load() != 0 {
		t.Fatal("scheduler fired despite refusing idle gate")
	}
	if !strings.Contains(s.Status().LastReason, "active_ssh_session") {
		t.Errorf("LastReason: got %q, want substring active_ssh_session", s.Status().LastReason)
	}
}
