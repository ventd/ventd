package controller

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/watchdog"
)

// TestRestoreOnUnbind binds RULE-CTRL-RECONCILE-STRANDED for the live-reload
// case: when a controller's fan or curve disappears from the live config (a
// SIGHUP/in-process reload that drops the fan), the controller hands the fan
// back to firmware auto once — rather than skip-ticking forever and leaving it
// frozen in manual mode — and re-arms once the binding returns.
func TestRestoreOnUnbind(t *testing.T) {
	setEnable := func(t *testing.T, ff fakeFan, v string) {
		t.Helper()
		if err := os.WriteFile(ff.enablePath, []byte(v+"\n"), 0o600); err != nil {
			t.Fatalf("seed pwm_enable: %v", err)
		}
	}

	// fanGone is a config that no longer contains the controller's fan;
	// curveGone keeps the fan but drops its curve. Both are unbind scenarios.
	run := func(t *testing.T, unbind func(full *config.Config, ff fakeFan) *config.Config, wantMissing string) {
		ff := newFakeFan(t)
		if err := os.WriteFile(ff.tempPath, []byte("60000\n"), 0o600); err != nil {
			t.Fatalf("seed temp: %v", err)
		}
		full := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)

		logger := silentLogger()
		wd := watchdog.New(logger)
		wd.Register(ff.pwmPath, "hwmon") // captures origEnable=2

		cfgPtr := &atomic.Pointer[config.Config]{}
		cfgPtr.Store(full)
		c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

		// 1. Bound tick: controller acquires manual control (pwm_enable=1).
		c.tick()
		if got := readIntFile(t, ff.enablePath); got != 1 {
			t.Fatalf("%s: pwm_enable after bound tick = %d, want 1 (manual acquired)", wantMissing, got)
		}

		// 2. Binding removed → next tick hands the fan back to firmware (2).
		cfgPtr.Store(unbind(full, ff))
		c.tick()
		if got := readIntFile(t, ff.enablePath); got != 2 {
			t.Fatalf("%s: pwm_enable after unbind tick = %d, want 2 (handed back to firmware)", wantMissing, got)
		}

		// 3. Guard: a second unbound tick must not re-restore. Poke the file to
		//    a sentinel and confirm the controller leaves it untouched.
		setEnable(t, ff, "9")
		c.tick()
		if got := readIntFile(t, ff.enablePath); got != 9 {
			t.Fatalf("%s: pwm_enable after second unbound tick = %d, want 9 (guard suppresses re-restore)", wantMissing, got)
		}

		// 4. Binding returns → a bound tick re-arms the guard (clears the
		//    one-shot flag via markTickCompleted). We don't assert pwm_enable
		//    here: re-acquiring manual mode is governed by the S0.4
		//    reassert-readback throttle, so the marker poked in step 3 may
		//    persist for one tick — irrelevant to the re-arm being exercised.
		cfgPtr.Store(full)
		c.tick()

		// 5. Removed again → the re-armed guard fires the hand-back once more,
		//    overwriting the step-3 marker back to firmware auto (2).
		cfgPtr.Store(unbind(full, ff))
		c.tick()
		if got := readIntFile(t, ff.enablePath); got != 2 {
			t.Fatalf("%s: pwm_enable after second unbind = %d, want 2 (re-armed hand-back fired)", wantMissing, got)
		}
	}

	t.Run("fan removed from live config", func(t *testing.T) {
		run(t, func(full *config.Config, ff fakeFan) *config.Config {
			// Same sensors/curves, but no fan matching the controller's pwmPath.
			return &config.Config{Sensors: full.Sensors, Curves: full.Curves}
		}, "fan")
	})

	t.Run("curve removed from live config", func(t *testing.T) {
		run(t, func(full *config.Config, ff fakeFan) *config.Config {
			// Fan still present, but its curve (and the control) are gone.
			return &config.Config{Sensors: full.Sensors, Fans: full.Fans}
		}, "curve")
	})
}
