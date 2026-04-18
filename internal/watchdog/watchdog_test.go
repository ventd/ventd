package watchdog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/testfixture/fakehwmon"
)

// TestRestoreOne_MatchesMostRecent asserts that RestoreOne dispatches to the
// LIFO-top (most recently registered) entry for the given pwmPath, not the
// startup entry underneath it. This matters when a per-sweep registration
// layers on top of the daemon-startup registration for the same path.
func TestRestoreOne_MatchesMostRecent(t *testing.T) {
	fake := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{{
			PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 100, Enable: 99}},
		}},
	})
	pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")
	enablePath := pwm + "_enable"

	var buf bytes.Buffer
	w := New(slog.New(slog.NewTextHandler(&buf, nil)))
	// Direct entry injection: startup (origEnable=1) + sweep (origEnable=3).
	// RestoreOne must pick the sweep entry (origEnable=3).
	w.entries = []entry{
		{pwmPath: pwm, fanType: "hwmon", origEnable: 1},
		{pwmPath: pwm, fanType: "hwmon", origEnable: 3},
	}

	if err := os.WriteFile(enablePath, []byte("99\n"), 0o600); err != nil {
		t.Fatalf("perturb enable: %v", err)
	}

	w.RestoreOne(pwm)

	if got := readTrimmed(t, enablePath); got != "3" {
		t.Errorf("pwm_enable after RestoreOne = %q, want %q (LIFO-top entry restored)", got, "3")
	}
}

// TestRestoreOne_NoMatchIsNoOp asserts that RestoreOne on an unregistered
// path does not panic and does not touch any registered entry's sysfs files.
func TestRestoreOne_NoMatchIsNoOp(t *testing.T) {
	fake := fakehwmon.New(t, &fakehwmon.Options{
		Chips: []fakehwmon.ChipOptions{{
			PWMs: []fakehwmon.PWMOptions{{Index: 1, PWM: 77, Enable: 1}},
		}},
	})
	pwm := filepath.Join(fake.Root, "hwmon0", "pwm1")
	enablePath := pwm + "_enable"

	var buf bytes.Buffer
	w := New(slog.New(slog.NewTextHandler(&buf, nil)))
	w.entries = []entry{
		{pwmPath: pwm, fanType: "hwmon", origEnable: 1},
	}

	// Perturb so we can detect any spurious restore write.
	if err := os.WriteFile(enablePath, []byte("2\n"), 0o600); err != nil {
		t.Fatalf("perturb enable: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RestoreOne panicked on unknown path: %v", r)
			}
		}()
		w.RestoreOne("/does-not-exist")
	}()

	// The registered entry must not have been touched.
	if got := readTrimmed(t, enablePath); got != "2" {
		t.Errorf("pwm_enable changed after no-op RestoreOne = %q, want %q", got, "2")
	}
}
