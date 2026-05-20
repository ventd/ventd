// SPDX-License-Identifier: GPL-3.0-or-later
//
// pwmconfig_parity_test.go is the T4.2 deliverable from the
// absorption plan at /root/.claude/plans/squishy-sparking-pearl.md.
//
// It pins ventd's pwm↔fan-tach pairing detection against the
// canonical lm-sensors `pwmconfig` reference algorithm.
//
// pwmconfig (in lm-sensors:prog/pwm/pwmconfig.in) implements
// fan-detection as follows:
//
//	for each pwm* file:
//	    set pwm = 0 (full stop)
//	    sleep 5s
//	    read every fan*_input — these are the post-stop RPMs
//	    set pwm = 255 (full speed)
//	    sleep 5s
//	    read every fan*_input — these are the post-spin RPMs
//	    set pwm = original_pwm
//	    Δ_per_fan = post_spin - post_stop
//	    pair pwm with the fan whose Δ is greatest (the largest
//	    positive correlation wins).
//
// The pairing is determined PURELY by RPM correlation — pwmconfig
// never assumes pwm<N> drives fan<N>_input. A board where pwm1
// physically drives fan2_input + fan3_input (e.g. dual-fan
// chassis where the OEM wired the tachs to header 2/3 but the
// PWM signal to header 1) is paired correctly.
//
// ventd's `Manager.DetectRPMSensor` implements an equivalent
// algorithm (calibrate.go) but ramps PWM by +60 from baseline
// rather than the full 0→255 sweep — a cheaper variant that the
// existing `TestDetectRPMSensor_HappyPath` test (with the
// ramp-fan1 fixture) covers head-on. This parity test extends
// that coverage with the harder case: the wrong index alignment.
package calibrate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// TestPwmconfigParity_RPMCorrelationBeatsIndexAlignment seeds a
// hwmon fixture where pwm1 drives fan2_input (the +60 PWM ramp
// produces a measurable RPM rise on fan2) and fan1_input stays
// flat. The pwmconfig algorithm pairs pwm1 with fan2 by RPM
// correlation; ventd's DetectRPMSensor MUST reach the same
// pairing. A naive "pair by trailing-digit match" implementation
// would mis-pair pwm1 with fan1_input and silently produce wrong
// fan-speed data on every board with non-aligned wiring.
//
// This test is the pwmconfig-parity load-bearing assertion. A
// regression that changes DetectRPMSensor to break this pairing
// would surface as a test failure here BEFORE the daemon ships
// to a board with non-aligned tach wiring.
func TestPwmconfigParity_RPMCorrelationBeatsIndexAlignment(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	pwmEnable := pwm + "_enable"
	fan1In := filepath.Join(dir, "fan1_input")
	fan2In := filepath.Join(dir, "fan2_input")

	write := func(path, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	write(pwm, "100\n")
	write(pwmEnable, "1\n")
	// Baseline: both fans at the same idle reading. Without the
	// rampGoroutine below, both would stay there and ventd's
	// detector would return (empty, nil) per RULE-CAL-DETECT-NO-WINNER.
	write(fan1In, "800\n")
	write(fan2In, "800\n")

	// The wiring quirk: when the daemon writes the higher PWM,
	// fan2_input rises by ~600 RPM while fan1_input stays at
	// baseline. This is the "physical wiring doesn't match the
	// trailing-digit convention" case pwmconfig handles correctly.
	var stopped atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Sleep until the post-#754 baseline read has captured the
		// 800 RPM starting value; then write the ramped reading so
		// the post-ramp read sees 1500. The new flow's baseline read
		// lands at ~t=3.1s and the post-ramp read at ~t=5.1s; ~t=4s
		// puts the ramped write between them.
		for i := 0; i < 800 && !stopped.Load(); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		if stopped.Load() {
			return
		}
		// pwm1 physically drives fan2 — fan2_input is the channel
		// that responds.
		write(fan2In, "1500\n")
	}()
	t.Cleanup(func() {
		stopped.Store(true)
		wg.Wait()
	})

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
	if res.RPMPath == "" {
		t.Fatalf("DetectRPMSensor returned (empty, nil) — pwmconfig would have paired pwm1 with fan2_input via RPM correlation")
	}
	if !strings.HasSuffix(res.RPMPath, "/fan2_input") {
		t.Errorf("DetectRPMSensor paired pwm1 with %q, want fan2_input (RPM correlation pairing, not index alignment)", res.RPMPath)
	}
	if res.Delta < 50 {
		t.Errorf("Delta = %d, want >= 50 (minDelta noise floor)", res.Delta)
	}
}

// TestPwmconfigParity_NoCorrelationReportsEmptyResult pins the
// reverse condition: pwmconfig's "no fan responded" outcome must
// map cleanly to ventd's "empty RPMPath, nil error" contract
// (RULE-CAL-DETECT-NO-WINNER). pwmconfig saves an empty pairing
// when every Δ is below its noise threshold; ventd's
// DetectRPMSensor returns (empty, nil) when every fan stayed
// below the 50-RPM noise floor.
//
// This complements TestDetectRPMSensor_NoCorrelation by binding
// the contract specifically to the pwmconfig parity claim — a
// future change that makes DetectRPMSensor return an error on
// no-winner would break pwmconfig-shaped integrations + the
// detector's documented contract.
func TestPwmconfigParity_NoCorrelationReportsEmptyResult(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	pwmEnable := pwm + "_enable"
	fan1In := filepath.Join(dir, "fan1_input")
	fan2In := filepath.Join(dir, "fan2_input")

	write := func(path, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	write(pwm, "100\n")
	write(pwmEnable, "1\n")
	// Both fans stuck at flat readings — no PWM change can produce
	// a correlation signal.
	write(fan1In, "800\n")
	write(fan2In, "800\n")

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
	if res.RPMPath != "" {
		t.Errorf("DetectRPMSensor returned %q, want empty (no fan crossed the noise floor — pwmconfig's no-winner case)", res.RPMPath)
	}
}
