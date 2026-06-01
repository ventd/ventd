package controller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/massstall"
	"github.com/ventd/ventd/internal/watchdog"
)

// TestTick_StallDetectedThroughRealBackendFlipsMassStall drives the full
// stall-detection chain end to end: the REAL control tick reads a real
// fan*_input through the REAL hwmon backend, and a commanded-but-not-spinning
// fan (PWM ≥ stiction floor, RPM=0) propagates through the stall reporter into
// a real massstall.Tracker until MassStalled flips. The existing
// TestController_StallReporterFires pins the reporter mechanism but with a
// fake, always-spinning backend (rpm=1234); it never exercises the
// disconnect/stall transition (spinning → 0) through the real backend, nor the
// mass-stall tracker actually flipping. RULE-CTRL-STALL-REPORT-01 + the R11
// mass-stall path. Mirrors the hwmonsim `disconnect` fault model (tach drops to
// 0 while duty stays high).
func TestTick_StallDetectedThroughRealBackendFlipsMassStall(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.pwmPath)
	tachPath := filepath.Join(chipDir, "fan1_input")
	writeTach := func(rpm string) {
		t.Helper()
		if err := os.WriteFile(tachPath, []byte(rpm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 85 °C → linear(40→80 / 0→255) clamps to 255, capped to MaxPWM=200,
	// which is above StallPWMFloor (77) so the tach is read and reported.
	writeTempAttr(t, chipDir, "temp1_input", "85000")
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

	tracker := massstall.New(time.Minute, 1) // one stalled channel is "mass" here
	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithStallReporter(ff.pwmPath, func(ch string, pwm uint8, rpm int32, now time.Time) {
			tracker.Report(ch, pwm, rpm, now)
		}))
	// New defaults c.backend to the real hal/hwmon backend for "hwmon" fans —
	// not overridden here, so reads hit the real fan*_input file.

	// 1) Fan spinning at 1500 RPM under a high command: NOT a stall.
	writeTach("1500")
	c.tick()
	if tracker.MassStalled(time.Now()) {
		t.Fatal("mass-stall flagged while the fan is spinning (RPM=1500)")
	}

	// 2) Fan stalls — tach drops to 0 while PWM stays high (200). The real
	//    backend reads fan1_input=0, the controller reports RPM=0, and the
	//    tracker flips to mass-stalled.
	writeTach("0")
	c.tick()
	if !tracker.MassStalled(time.Now()) {
		t.Error("stalled fan (PWM=200, RPM=0) not detected end-to-end through the real backend + mass-stall tracker")
	}
}
