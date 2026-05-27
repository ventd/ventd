package calibrate

// DetectRPMSensor coverage against a fake sysfs.
//
// What this file pins:
//
//   * Happy path:       a ramped fan's fan*_input file is chosen over a
//                       sibling that does not move with PWM.
//   * No-correlation:   when every sibling changes by less than minDelta
//                       (50 RPM), DetectResult.RPMPath stays empty and
//                       the error is nil (documented as "not found").
//   * Nvidia rejected:  DetectRPMSensor refuses nvidia fans up-front.
//   * Missing dir:      when no fan*_input files exist, the function
//                       returns an error, not a misleading empty result.
//   * Concurrent call:  a second DetectRPMSensor against the same PWM
//                       path while the first is still running must
//                       return an "already running" error.
//
// Reference for future sessions:
//
//   The real DetectRPMSensor ramps PWM by 60 and sleeps 2s to let the
//   fan settle (calibrate.go:962-1000). That sleep is unconditional.
//   These tests accept the 2-second wall clock cost. If you add more
//   cases, run them under t.Parallel() where safe — each case builds
//   its own t.TempDir() so the fake sysfs trees don't collide.
//
//   To make these tests sub-second you would need a seam that
//   injects a fake clock; that's a refactor outside the scope of the
//   "no unnecessary code changes" rule this suite was built under.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// fakeModeBackend is a minimal hal.FanBackend standing in for a
// non-hwmon mode/level backend (msiec/thinkpad/…). Its channel ID is a
// device-root path, NOT a writable sysfs pwmN file — exactly the shape
// that makes DetectRPMSensor's hwmon assumptions (readSysfsUint8 on the
// PWM path, same-dir tach glob) fail before #1376. Read reports the
// last-written PWM so DetectRPMSensor can capture/restore current state
// via the backend instead of the (non-existent) sysfs file.
type fakeModeBackend struct{ last uint8 }

func (f *fakeModeBackend) Enumerate(context.Context) ([]hal.Channel, error) { return nil, nil }
func (f *fakeModeBackend) Read(hal.Channel) (hal.Reading, error) {
	return hal.Reading{PWM: f.last, OK: true}, nil
}
func (f *fakeModeBackend) Write(_ hal.Channel, pwm uint8) error { f.last = pwm; return nil }
func (f *fakeModeBackend) Restore(hal.Channel) error            { return nil }
func (f *fakeModeBackend) Close() error                         { return nil }
func (f *fakeModeBackend) Name() string                         { return "msiec" }

// TestDetectRPMSensor_HALFanPairsCrossDeviceTach is the calibrate-side
// half of #1376: an msiec-style fan whose channel ID is a device-root
// path (no sibling fan*_input) must still pair the tach that the
// in-kernel msi_wmi_platform hwmon exposes on a SEPARATE chip. The fan
// is driven through the HAL backend; the cross-device scan over
// m.hwmonRoot finds the responding fan1_input.
func TestDetectRPMSensor_HALFanPairsCrossDeviceTach(t *testing.T) {
	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	chip := filepath.Join(hwmonRoot, "hwmon0")
	if err := os.MkdirAll(chip, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	write(filepath.Join(chip, "name"), "msi_wmi_platform\n")
	fan1In := filepath.Join(chip, "fan1_input")
	fan2In := filepath.Join(chip, "fan2_input")
	write(fan1In, "800\n") // ramps with the fan
	write(fan2In, "800\n") // stays flat

	// Same baseline-vs-post-ramp timing cheat as newRampingHwmon: flip
	// fan1_input to the ramped value after the baseline read window.
	var stopped atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 800 && !stopped.Load(); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		if stopped.Load() {
			return
		}
		write(fan1In, "3000\n")
	}()
	defer func() { stopped.Store(true); wg.Wait() }()

	m := newQuietManager(t)
	m.setHwmonRootForTest(hwmonRoot)
	be := &fakeModeBackend{}
	m.SetChannelResolver(func(_ context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error) {
		return be, hal.Channel{
			ID:   fan.PWMPath,
			Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		}, nil
	})

	fan := &config.Fan{Type: "msiec", PWMPath: "/sys/devices/platform/msi-ec", MaxPWM: 255}
	res, err := m.DetectRPMSensor(fan)
	if err != nil {
		t.Fatalf("DetectRPMSensor: %v", err)
	}
	if res.RPMPath != fan1In {
		t.Fatalf("RPMPath = %q, want cross-device tach %q", res.RPMPath, fan1In)
	}
	if res.Delta < 50 {
		t.Fatalf("Delta = %d, want >=50", res.Delta)
	}
}

// newRampingHwmon builds a minimal hwmon tree with one pwm channel and
// two fan*_input files. fan1_input tracks the PWM via a rising writer
// goroutine; fan2_input stays flat. The function returns everything
// the test needs plus a stop func.
//
// We cheat the multi-second sleeps inside DetectRPMSensor by writing
// the ramped RPM value into fan1_input from a background goroutine
// timed to land AFTER the baseline read but BEFORE the post-ramp
// read. The baseline read captures the flat initial value on both
// fans, and the post-ramp read on fan1 captures rampRPM.
//
// The detect flow timeline (#754 stiction-break + 20→80% sweep):
//
//	t=0       stiction-break pulse (write PWM=maxPWM), 500ms hold
//	t=500ms   write baselinePWM (20% of maxPWM), 2s settle
//	t=2500ms  3×200ms stability sample (baseline reads happen here)
//	t=3100ms  write testPWM (80% of maxPWM), 2s settle
//	t=5100ms  post-ramp read
//
// The goroutine fires the ramp write at ~4000ms — after baseline,
// before post-ramp. The ramp-direction check inside DetectRPMSensor
// (ramped = testPWM > baselinePWM) is always satisfied because the
// new sweep always goes low → high (20% → 80%).
//
// This technique survives because hwmon.ReadRPMPath is a plain file
// read — the sysfs kernel semantics (which would update fan_input on
// the fly) are not part of the code under test.
func newRampingHwmon(t *testing.T, rampRPM int) (dir, pwm, fan1In, fan2In string, stop func()) {
	t.Helper()
	dir = t.TempDir()

	pwm = filepath.Join(dir, "pwm1")
	pwmEnable := pwm + "_enable"
	fan1In = filepath.Join(dir, "fan1_input")
	fan2In = filepath.Join(dir, "fan2_input")

	write := func(path string, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}

	write(pwm, "100\n")
	write(pwmEnable, "1\n") // must be in manual mode for WritePWMSafe
	// Baseline reads happen FIRST inside DetectRPMSensor. Seed them at
	// a flat idle value so the delta is measured against something
	// realistic.
	write(fan1In, "800\n")
	write(fan2In, "800\n")

	// A goroutine flips fan1_input to the ramped value immediately.
	// The DetectRPMSensor baseline read captures 800; the post-sleep
	// read captures rampRPM. Use atomic to avoid a race when stopping.
	var stopped atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Write the ramped value after a delay tuned to land between
		// the baseline read (~t=3.1s) and the post-ramp read (~t=5.1s)
		// in the new #754 detect flow. ~4s puts it comfortably in the
		// middle of the settle-after-testPWM-write window.
		for i := 0; i < 800 && !stopped.Load(); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		if stopped.Load() {
			return
		}
		// Write after baseline; stays until stop.
		write(fan1In, strings.TrimSpace(itoaInt(rampRPM))+"\n")
	}()

	stop = func() {
		stopped.Store(true)
		wg.Wait()
	}
	return
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func newQuietManager(t *testing.T) *Manager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(filepath.Join(t.TempDir(), "cal.json"), logger, nil)
}

// TestDetectRPMSensor_HappyPath — fan1_input ramps with PWM; fan2_input
// stays flat. The result must identify fan1_input and report a delta
// greater than the 50-RPM noise floor.
func TestDetectRPMSensor_HappyPath(t *testing.T) {
	_, pwm, fan1In, _, stop := newRampingHwmon(t, 1600)
	defer stop()

	m := newQuietManager(t)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)
	fan := &config.Fan{
		Type:    "hwmon",
		PWMPath: pwm,
		MinPWM:  30,
		MaxPWM:  255,
	}
	res, err := m.DetectRPMSensor(fan)
	if err != nil {
		t.Fatalf("DetectRPMSensor: %v", err)
	}
	if res.RPMPath != fan1In {
		t.Fatalf("RPMPath = %q, want %q (fan1 ramped, fan2 flat)", res.RPMPath, fan1In)
	}
	if res.Delta < 50 {
		t.Fatalf("Delta = %d, want >=50 (minDelta)", res.Delta)
	}
}

// TestDetectRPMSensor_NoCorrelation — both siblings stay flat, so no
// sensor crosses the 50-RPM noise floor. The contract is (empty, nil):
// "detection ran, no winner" — see calibrate.go:1022-1026.
func TestDetectRPMSensor_NoCorrelation(t *testing.T) {
	fake := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{{
			Name: "testchip",
			PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 100, Enable: 1}},
			Fans: []fakehwmon.FanOptions{
				{Index: 1, RPM: 800},
				{Index: 2, RPM: 820},
			},
		}},
	})
	pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

	m := newQuietManager(t)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)
	fan := &config.Fan{Type: "hwmon", PWMPath: pwm, MaxPWM: 255}
	res, err := m.DetectRPMSensor(fan)
	if err != nil {
		t.Fatalf("DetectRPMSensor unexpected err: %v", err)
	}
	if res.RPMPath != "" {
		t.Fatalf("RPMPath = %q, want empty (no winner)", res.RPMPath)
	}
}

// TestRegression_Issue754_StictionBreakPulse verifies that
// DetectRPMSensor writes maxPWM at the top of its sequence before any
// other PWM write — the 500ms stiction-break pulse that breaks a
// stalled rotor on a fan whose previous calibration sweep parked it
// at PWM=0. Before #754, the routine ramped origPWM±60, which on a
// fan parked at PWM=0 landed at PWM=60 — below the start-from-
// standstill threshold for many fans. The sweep returned delta≈0 and
// the working sensor was misclassified as "no correlation".
//
// We assert the pulse by capturing PWM-write history through the
// fake hwmon's `pwm` file: the first write must be PWM=255 (or
// maxPWM if non-default). We don't need to assert RPM correlation
// here — the happy-path test already covers that the wider 20→80%
// sweep produces measurable deltas.
func TestRegression_Issue754_StictionBreakPulse(t *testing.T) {
	dir, pwm, _, _, stop := newRampingHwmon(t, 1600)
	defer stop()

	m := newQuietManager(t)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)
	// Park PWM at 0 so the pre-#754 origPWM±60 picker would have
	// produced a too-low test PWM. The stiction-break pulse must
	// dominate this and write 255 anyway.
	if err := os.WriteFile(pwm, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("park pwm at 0: %v", err)
	}

	fan := &config.Fan{
		Type:    "hwmon",
		PWMPath: pwm,
		MinPWM:  30,
		MaxPWM:  255,
	}
	// Probe the post-pulse PWM by sampling the pwm file 200ms after
	// DetectRPMSensor starts — well inside the 500ms pulse hold and
	// before the baseline-write at t=500ms.
	type sample struct {
		t   time.Duration
		pwm string
	}
	samplesCh := make(chan sample, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		data, _ := os.ReadFile(pwm)
		samplesCh <- sample{t: 200 * time.Millisecond, pwm: strings.TrimSpace(string(data))}
	}()

	_, err := m.DetectRPMSensor(fan)
	if err != nil {
		t.Fatalf("DetectRPMSensor: %v", err)
	}

	s := <-samplesCh
	if s.pwm != "255" {
		t.Fatalf("at t=%s expected stiction-break pulse PWM=255, got pwm=%q (dir=%s)",
			s.t, s.pwm, dir)
	}
}

// TestDetectRPMSensor_RejectsNvidia — the nvidia branch in
// DetectRPMSensor (calibrate.go:913-915) refuses up-front because NVML
// fans have no hwmon RPM sensor to probe. The error message identifies
// the branch; pin the substring so a future diagnostic message change
// doesn't silently drop the guard.
func TestDetectRPMSensor_RejectsNvidia(t *testing.T) {
	m := newQuietManager(t)
	fan := &config.Fan{Type: "nvidia", PWMPath: "0"}
	_, err := m.DetectRPMSensor(fan)
	if err == nil {
		t.Fatal("DetectRPMSensor on nvidia fan: err = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "nvidia fans do not use hwmon") {
		t.Fatalf("nvidia refusal message changed: %v", err)
	}
}

// TestDetectRPMSensor_NoFanInputFiles — empty hwmon dir. The function
// must surface "no fan*_input files" instead of returning a misleading
// empty-result success. Regression target for calibrate.go:985-987.
func TestDetectRPMSensor_NoFanInputFiles(t *testing.T) {
	fake := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{{
			Name: "testchip",
			PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 100, Enable: 1}},
			// No Fans: no fan*_input files in the chip directory.
		}},
	})
	pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")

	m := newQuietManager(t)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)
	fan := &config.Fan{Type: "hwmon", PWMPath: pwm, MaxPWM: 255}
	_, err := m.DetectRPMSensor(fan)
	if err == nil {
		t.Fatal("DetectRPMSensor on fan-input-less dir: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "no fan") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDetectRPMSensor_ConcurrentCall_Rejected — two DetectRPMSensor
// calls race against the same PWM path. Only one may hold the run
// slot; the other must return "already running".
//
// This is the concurrency shape that production hits when a user
// double-clicks a "Detect sensor" button in the wizard UI. The guard
// lives at calibrate.go:920-929.
func TestDetectRPMSensor_ConcurrentCall_Rejected(t *testing.T) {
	_, pwm, _, _, stop := newRampingHwmon(t, 1600)
	defer stop()

	m := newQuietManager(t)
	resolver, _ := makeHwmonResolver(t)
	m.SetChannelResolver(resolver)
	fan := &config.Fan{Type: "hwmon", PWMPath: pwm, MaxPWM: 255}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range errs {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.DetectRPMSensor(fan)
		}(i)
	}
	wg.Wait()

	// Exactly one caller should see "already running"; the other gets
	// a normal result. The order is non-deterministic under the race
	// detector, so check the set.
	gotAlready := 0
	for _, e := range errs {
		if e != nil && strings.Contains(e.Error(), "already running") {
			gotAlready++
		}
	}
	if gotAlready != 1 {
		t.Fatalf("concurrent DetectRPMSensor: got %d \"already running\" errors, want 1 (errs=%v)", gotAlready, errs)
	}
}
