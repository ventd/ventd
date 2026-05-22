package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/watchdog"
)

// fakePolarityProber simulates the side effects of a real HwmonProber's
// per-channel pwm/pwm_enable writes without exercising the real bipolar
// pulse + RPM-mean loop (which sleeps for ~14 s per channel). Each call
// writes pwm_enable=1 (manual mode) and a probe-residual pwm value so
// the test can verify that wd.Restore on daemon exit writes pwm_enable
// back to the pre-probe value.
type fakePolarityProber struct {
	pwmResidual string
}

func (p *fakePolarityProber) ProbeAll(
	ctx context.Context,
	channels []*probe.ControllableChannel,
) ([]polarity.ChannelResult, error) {
	for _, ch := range channels {
		// Mirror the kernel's auto-flip when userspace writes a pwm value
		// without first setting pwm_enable: manual mode + the test's
		// residual PWM value.
		enablePath := ch.PWMPath + "_enable"
		_ = os.WriteFile(enablePath, []byte("1\n"), 0o644)
		_ = os.WriteFile(ch.PWMPath, []byte(p.pwmResidual+"\n"), 0o644)
	}
	results := make([]polarity.ChannelResult, 0, len(channels))
	for _, ch := range channels {
		results = append(results, polarity.ChannelResult{
			Backend:  "hwmon",
			Identity: polarity.Identity{PWMPath: ch.PWMPath, TachPath: ch.TachPath},
			Polarity: polarity.PolarityNormal,
		})
	}
	return results, nil
}

// TestPolarityAutoProbe_RestoresPWMEnable_OnDaemonExit_Issue1312 covers
// the safety property: when the first-boot polarity goroutine writes to
// pwm channels (leaving pwm_enable=1 / manual mode) and the daemon
// then exits, the watchdog must restore pwm_enable to its pre-probe
// value. Before the fix the polarity goroutine ran with an empty
// watchdog (cfg.Fans empty in first-boot mode), so wd.Restore() was a
// no-op and pwm_enable=1 leaked across daemon exit.
func TestPolarityAutoProbe_RestoresPWMEnable_OnDaemonExit_Issue1312(t *testing.T) {
	sysfs := t.TempDir()
	pwm1 := filepath.Join(sysfs, "pwm1")
	enable1 := filepath.Join(sysfs, "pwm1_enable")
	pwm2 := filepath.Join(sysfs, "pwm2")
	enable2 := filepath.Join(sysfs, "pwm2_enable")

	if err := os.WriteFile(pwm1, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seed pwm1: %v", err)
	}
	if err := os.WriteFile(enable1, []byte("2\n"), 0o644); err != nil {
		t.Fatalf("seed pwm1_enable: %v", err)
	}
	if err := os.WriteFile(pwm2, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seed pwm2: %v", err)
	}
	if err := os.WriteFile(enable2, []byte("2\n"), 0o644); err != nil {
		t.Fatalf("seed pwm2_enable: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)

	channels := []*probe.ControllableChannel{
		{PWMPath: pwm1, TachPath: filepath.Join(sysfs, "fan1_input"), Driver: "nct6687"},
		{PWMPath: pwm2, TachPath: filepath.Join(sysfs, "fan2_input"), Driver: "nct6687"},
	}

	st, err := state.Open(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	prober := &fakePolarityProber{pwmResidual: "128"}

	// Simulate a probe that COMPLETES — runPolarityAutoProbe's own
	// per-channel RestoreOne should bring pwm_enable back to its pre-
	// probe value before deregistering, leaving the watchdog with no
	// entries when subsequent controllers register.
	runPolarityAutoProbe(context.Background(), wd, st.KV, channels, prober, logger)

	for _, p := range []string{enable1, enable2} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if v := strings.TrimSpace(string(got)); v != "2" {
			t.Fatalf("after probe completion: %s = %q, want %q (probe leaked pwm_enable)", p, v, "2")
		}
	}

	// Now exercise the daemon-exit-mid-probe scenario: register the same
	// channels with the watchdog (mirroring what runPolarityAutoProbe
	// does first), have the fake prober write pwm_enable=1 + a PWM
	// residual to simulate the probe mid-flight, and then call
	// wd.Restore directly (mirroring run()'s deferred Restore on
	// daemon exit). pwm_enable must come back to 2 — proving that any
	// daemon-exit path during the probe is safe.
	if err := os.WriteFile(enable1, []byte("2\n"), 0o644); err != nil {
		t.Fatalf("reset pwm1_enable: %v", err)
	}
	if err := os.WriteFile(enable2, []byte("2\n"), 0o644); err != nil {
		t.Fatalf("reset pwm2_enable: %v", err)
	}
	wd2 := watchdog.New(logger)
	for _, ch := range channels {
		wd2.Register(ch.PWMPath, "hwmon")
	}
	if _, err := prober.ProbeAll(context.Background(), channels); err != nil {
		t.Fatalf("fake ProbeAll: %v", err)
	}
	// Sanity check: probe leaked pwm_enable=1.
	for _, p := range []string{enable1, enable2} {
		got, _ := os.ReadFile(p)
		if v := strings.TrimSpace(string(got)); v != "1" {
			t.Fatalf("mid-probe sanity: %s = %q, want %q", p, v, "1")
		}
	}
	// Daemon-exit Restore must clean it up.
	wd2.Restore()
	for _, p := range []string{enable1, enable2} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s post-restore: %v", p, err)
		}
		if v := strings.TrimSpace(string(got)); v != "2" {
			t.Fatalf("daemon-exit Restore: %s = %q, want %q (#1312 regression)", p, v, "2")
		}
	}
}
