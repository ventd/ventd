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
//   Mark that as a future item in docs/TESTING.md, not here.

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// newRampingHwmon builds a minimal hwmon tree with one pwm channel and
// two fan*_input files. fan1_input tracks the PWM via a rising writer
// goroutine; fan2_input stays flat. The function returns everything
// the test needs plus a stop func.
//
// We cheat the 2-second sleep by pre-writing the post-ramp RPM to
// fan1_input BEFORE DetectRPMSensor starts. The baseline read captures
// the flat initial value on fan2, and by the time the test-PWM write
// lands fan1_input already holds a high RPM. The ramp-direction check
// inside DetectRPMSensor (ramped = testPWM > origPWM) matches because
// origPWM is 100 and testPWM will be 160.
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
		// Write the ramped value after a brief pause so the baseline
		// reads happen first. DetectRPMSensor reads the baseline
		// synchronously at the top, so a short delay is enough.
		for i := 0; i < 200 && !stopped.Load(); i++ {
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
	fan := &config.Fan{Type: "hwmon", PWMPath: pwm, MaxPWM: 255}
	res, err := m.DetectRPMSensor(fan)
	if err != nil {
		t.Fatalf("DetectRPMSensor unexpected err: %v", err)
	}
	if res.RPMPath != "" {
		t.Fatalf("RPMPath = %q, want empty (no winner)", res.RPMPath)
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
