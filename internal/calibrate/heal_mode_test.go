package calibrate

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
)

// modeHealBackend is a fake hal.FanBackend + hal.ModeHealer used to
// exercise Manager.HealModeMismatch (#759). It models the canonical
// 3-pin-on-PWM-header signature: in PWM mode the tach is pinned near
// full speed regardless of duty (a flat curve); in DC mode the tach
// tracks the commanded duty (a responsive curve) IFF dcResponsive.
type modeHealBackend struct {
	mu           sync.Mutex
	lastPWM      uint8
	mode         int
	writable     bool
	dcResponsive bool
	setModes     []int
}

func (f *modeHealBackend) Enumerate(context.Context) ([]hal.Channel, error) { return nil, nil }

func (f *modeHealBackend) Read(hal.Channel) (hal.Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rpm := uint16(1500) // flat, stuck near full speed (PWM-mode signature)
	if f.mode == hal.ModeDC && f.dcResponsive {
		rpm = uint16(int(f.lastPWM) * 6) // tracks duty: 0..1530
	}
	return hal.Reading{RPM: rpm, OK: true}, nil
}

func (f *modeHealBackend) Write(_ hal.Channel, pwm uint8) error {
	f.mu.Lock()
	f.lastPWM = pwm
	f.mu.Unlock()
	return nil
}

func (f *modeHealBackend) Restore(hal.Channel) error { return nil }
func (f *modeHealBackend) Close() error              { return nil }
func (f *modeHealBackend) Name() string              { return "hwmon" }

func (f *modeHealBackend) ModeWritable(hal.Channel) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writable
}

func (f *modeHealBackend) Mode(hal.Channel) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode, nil
}

func (f *modeHealBackend) SetMode(_ hal.Channel, mode int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
	f.setModes = append(f.setModes, mode)
	return nil
}

func (f *modeHealBackend) currentMode() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *modeHealBackend) setModeHistory() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.setModes...)
}

// healFan is the RPMPath-less fan used by the heal tests so the sweep
// reads RPM through the backend's Read (no sysfs fan*_input).
func healFan() *config.Fan {
	return &config.Fan{Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon0/pwm1", MinPWM: 0, MaxPWM: 255}
}

func newHealManager(t *testing.T, be *modeHealBackend) *Manager {
	t.Helper()
	m := newQuietManager(t)
	m.SetChannelResolver(func(_ context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error) {
		return be, hal.Channel{ID: fan.PWMPath, Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore}, nil
	})
	return m
}

// TestMain shrinks the per-step PWM ramp holds so the two full sweeps a
// heal runs don't dominate wall-clock. The whole package shares this and
// no sweep test depends on the production 2s/1.5s values, so the override
// is global and unrestored.
func TestMain(m *testing.M) {
	pwmUpStepSettle = time.Millisecond
	pwmDownStepSettle = time.Millisecond
	os.Exit(m.Run())
}

// TestHealModeMismatch_RecoversViaDCMode: a flat PWM-mode curve on a
// writable driver whose fan responds in DC mode must heal — the header
// is left in DC mode and the result reports the recovered curve.
func TestHealModeMismatch_RecoversViaDCMode(t *testing.T) {
	be := &modeHealBackend{mode: hal.ModePWM, writable: true, dcResponsive: true}
	m := newHealManager(t, be)
	ctx := context.Background()
	fan := healFan()

	// First sweep (PWM mode) flags the mismatch.
	res1, err := m.RunSync(ctx, fan)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if !res1.ModeMismatchSuspected {
		t.Fatalf("expected ModeMismatchSuspected on the flat PWM-mode sweep, got %+v", res1)
	}

	healed, ok, err := m.HealModeMismatch(ctx, fan)
	if err != nil {
		t.Fatalf("HealModeMismatch: %v", err)
	}
	if !ok {
		t.Fatal("expected heal to succeed")
	}
	if !healed.ModeHealed || healed.ResolvedPWMMode != "dc" {
		t.Fatalf("healed result missing flags: ModeHealed=%v ResolvedPWMMode=%q", healed.ModeHealed, healed.ResolvedPWMMode)
	}
	if healed.ModeMismatchSuspected {
		t.Fatal("healed result must clear ModeMismatchSuspected")
	}
	if healed.ModeMismatchEvidence != "self_healed_dc_mode" {
		t.Fatalf("ModeMismatchEvidence = %q, want self_healed_dc_mode", healed.ModeMismatchEvidence)
	}
	if healed.MaxRPM <= healed.MinRPM {
		t.Fatalf("expected a responsive DC-mode curve, got max=%d min=%d", healed.MaxRPM, healed.MinRPM)
	}
	if be.currentMode() != hal.ModeDC {
		t.Fatalf("header must be LEFT in DC mode after a successful heal, got %d", be.currentMode())
	}
	if hist := be.setModeHistory(); len(hist) != 1 || hist[0] != hal.ModeDC {
		t.Fatalf("expected exactly one SetMode(DC), got %v", hist)
	}
}

// TestHealModeMismatch_RevertsWhenDCDoesNotHelp: a fan that stays flat
// even in DC mode (dead fan / stiction) must NOT be left in DC mode —
// the heal reverts to the original mode and reports failure.
func TestHealModeMismatch_RevertsWhenDCDoesNotHelp(t *testing.T) {
	be := &modeHealBackend{mode: hal.ModePWM, writable: true, dcResponsive: false}
	m := newHealManager(t, be)
	ctx := context.Background()
	fan := healFan()

	healed, ok, err := m.HealModeMismatch(ctx, fan)
	if err != nil {
		t.Fatalf("HealModeMismatch: %v", err)
	}
	if ok {
		t.Fatalf("expected heal to fail (still flat in DC), got healed=%+v", healed)
	}
	if be.currentMode() != hal.ModePWM {
		t.Fatalf("header mode must be reverted to PWM, got %d", be.currentMode())
	}
	if hist := be.setModeHistory(); len(hist) != 2 || hist[0] != hal.ModeDC || hist[1] != hal.ModePWM {
		t.Fatalf("expected SetMode(DC) then SetMode(PWM) revert, got %v", hist)
	}
}

// TestHealModeMismatch_NoWritableModeIsNoop: a backend whose channel
// has no writable mode must short-circuit without touching the mode.
func TestHealModeMismatch_NoWritableModeIsNoop(t *testing.T) {
	be := &modeHealBackend{mode: hal.ModePWM, writable: false, dcResponsive: true}
	m := newHealManager(t, be)

	_, ok, err := m.HealModeMismatch(context.Background(), healFan())
	if err != nil {
		t.Fatalf("HealModeMismatch: %v", err)
	}
	if ok {
		t.Fatal("expected no heal when the channel mode is not writable")
	}
	if hist := be.setModeHistory(); len(hist) != 0 {
		t.Fatalf("expected no SetMode calls on a non-writable channel, got %v", hist)
	}
}

// TestHealModeMismatch_AlreadyDCIsNoop: when the header is already in DC
// mode a flat curve is not a PWM-mode mismatch — don't churn the mode.
func TestHealModeMismatch_AlreadyDCIsNoop(t *testing.T) {
	be := &modeHealBackend{mode: hal.ModeDC, writable: true, dcResponsive: true}
	m := newHealManager(t, be)

	_, ok, err := m.HealModeMismatch(context.Background(), healFan())
	if err != nil {
		t.Fatalf("HealModeMismatch: %v", err)
	}
	if ok {
		t.Fatal("expected no heal when the header is already DC-driven")
	}
	if hist := be.setModeHistory(); len(hist) != 0 {
		t.Fatalf("expected no SetMode calls when already DC, got %v", hist)
	}
}
