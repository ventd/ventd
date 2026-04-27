package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/experimental"
)

// TestShutdownSequence is the regression guard for SIGTERM / ctx-cancel
// shutdown. It boots the full daemon in-process against a tempdir-backed
// fake sysfs, waits for at least one control-loop tick, cancels the parent
// context, and asserts:
//
//  1. runDaemon returns nil within the shutdown deadline (no hangs).
//  2. The watchdog restored pwm_enable to its pre-boot value (the safety
//     net actually ran on the way out).
//  3. goleak.VerifyNone reports no stray goroutines.
//
// No real hwmon hardware is touched: the fan's pwm/pwm_enable files are
// ordinary files in t.TempDir() that the hwmon package writes to via
// os.WriteFile. The controller code path is exercised end-to-end; only the
// kernel sysfs boundary is replaced.
func TestShutdownSequence(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Fake sysfs: pwm1 (controller duty write target), pwm1_enable (watchdog
	// reads at Register, writes back at Restore), fan1_input + temp1_input
	// (read-only sensor sources the controller and UI poll).
	sysfs := t.TempDir()
	pwmPath := filepath.Join(sysfs, "pwm1")
	enablePath := filepath.Join(sysfs, "pwm1_enable")
	fanInput := filepath.Join(sysfs, "fan1_input")
	tempInput := filepath.Join(sysfs, "temp1_input")

	writeFile(t, pwmPath, "0\n")
	// 2 = auto (BIOS/kernel). The watchdog captures this at Register time
	// and must restore it on shutdown.
	writeFile(t, enablePath, "2\n")
	writeFile(t, fanInput, "1200\n")
	writeFile(t, tempInput, "45000\n")

	// Disable the netlink uevent socket: CI and containers usually can't
	// open one, and we don't need it to test shutdown.
	t.Setenv("VENTD_DISABLE_UEVENT", "1")

	// Use a config dir the daemon can read/write without needing /etc/ventd.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.yaml")

	cfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
		Web: config.Web{
			// Loopback + ephemeral port keeps the test self-contained and
			// satisfies RequireTransportSecurity without a cert.
			Listen: "127.0.0.1:0",
			// Pre-seeded bcrypt hash of "unused" so the first-boot setup-token
			// branch doesn't fire (we don't exercise the wizard here).
			PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
		},
		Sensors: []config.Sensor{{
			Name: "cpu",
			Type: "hwmon",
			Path: tempInput,
		}},
		Fans: []config.Fan{{
			Name:    "test-fan",
			Type:    "hwmon",
			PWMPath: pwmPath,
			RPMPath: fanInput,
			MinPWM:  0,
			MaxPWM:  255,
		}},
		Curves: []config.CurveConfig{{
			Name:  "half",
			Type:  "fixed",
			Value: 128,
		}},
		Controls: []config.Control{{
			Fan:   "test-fan",
			Curve: "half",
		}},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// runDaemon returns via errCh → a single-slot channel we select on below.
	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, cfg, cfgPath, "", logger, "", nil, experimental.Flags{}, nil)
	}()

	// Wait for at least one tick so the watchdog is registered, the
	// controller has written to pwm1, and the web server has bound its
	// listener. 300ms is ample with a 50ms poll interval.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pwmPath); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" && s != "0" {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := readTrim(t, pwmPath); got != "128" {
		t.Fatalf("controller never wrote PWM before shutdown: pwm1=%q (want 128)", got)
	}

	// SIGTERM analogue: cancel the parent context, then wait for clean exit.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemon returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of ctx cancel — shutdown is hung")
	}

	// Watchdog Restore must have written the original enable value back.
	if got := readTrim(t, enablePath); got != "2" {
		t.Fatalf("watchdog did not restore pwm_enable: got %q, want %q", got, "2")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}
