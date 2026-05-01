package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/experimental"
)

// TestRegression_Issue466_FirstBootReloadStartsControllers covers branch 3 of
// the in-process reload path from PR #478: oldCfg.Controls == 0 →
// newCfg.Controls > 0 (first-boot → post-wizard transition).
//
// Branches 1 (reload failure) and 2 (steady-state) are covered by the sibling
// tests below. Branch 3 is the most complex: it exercises watchdog
// registration, controller construction, and goroutine launch under the outer
// WaitGroup.  A regression here is silently invisible — reload succeeds and
// liveCfg updates, but controllers never start.
//
// Four assertions:
//  1. Pre-reload: no controller writes PWM (initial sysfs value preserved).
//  2. Post-reload: controller starts and writes the fixed-curve value (128).
//  3. Post-reload: fan is in manual mode (pwm_enable == 1).
//  4. Post-shutdown: watchdog.Restore fires (pwm_enable back to 2).
func TestRegression_Issue466_FirstBootReloadStartsControllers(t *testing.T) {
	sysfs := t.TempDir()
	pwmPath := filepath.Join(sysfs, "pwm1")
	enablePath := filepath.Join(sysfs, "pwm1_enable")
	fanInput := filepath.Join(sysfs, "fan1_input")
	tempInput := filepath.Join(sysfs, "temp1_input")

	writeFile(t, pwmPath, "0\n")
	writeFile(t, enablePath, "2\n")
	writeFile(t, fanInput, "1200\n")
	writeFile(t, tempInput, "45000\n")

	t.Setenv("VENTD_DISABLE_UEVENT", "1")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	// First-boot in-memory config: no fans, sensors, curves, or controls.
	// Mirrors the state the daemon holds before the setup wizard completes.
	bootCfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
		Web: config.Web{
			Listen:       "127.0.0.1:0",
			PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
		},
	}

	// Post-wizard config returned by the stub configLoader on reload.
	// Uses the same temp-dir sysfs paths as bootCfg — bypassing the /sys
	// prefix guard that config.Parse enforces on YAML loaded from disk.
	wizardCfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
		Web: config.Web{
			Listen:       "127.0.0.1:0",
			PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
		},
		Sensors:  []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans:     []config.Fan{{Name: "fan", Type: "hwmon", PWMPath: pwmPath, RPMPath: fanInput, MinPWM: 0, MaxPWM: 255}},
		Curves:   []config.CurveConfig{{Name: "c", Type: "fixed", Value: 128}},
		Controls: []config.Control{{Fan: "fan", Curve: "c"}},
	}

	// Stub configLoader so the in-process reload returns wizardCfg directly
	// instead of reading and validating a YAML file with /sys prefix guards.
	// Set before the goroutine starts; restored after it exits (safe per the
	// Go memory model: goroutine creation establishes happens-before).
	orig := configLoader
	configLoader = func(_ string) (*config.Config, error) { return wizardCfg, nil }
	defer func() { configLoader = orig }()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartCh := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonInternal(ctx, bootCfg, cfgPath, "", logger, "", nil, restartCh, experimental.Flags{}, nil, nil, nil)
	}()

	// 1. Pre-reload: no controller is running; PWM must stay at its initial value.
	time.Sleep(200 * time.Millisecond)
	if got := readTrim(t, pwmPath); got != "0" {
		t.Fatalf("pre-reload: expected no controller writes (pwm=0), got %q", got)
	}

	// Simulate wizard completion: signal triggers in-process config reload.
	restartCh <- struct{}{}

	// 2. Post-reload: controller starts and writes the fixed-curve value to PWM.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(pwmPath); strings.TrimSpace(string(b)) == "128" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := readTrim(t, pwmPath); got != "128" {
		t.Fatalf("post-reload: controller did not start; pwm=%q (want 128)", got)
	}

	// 3. Post-reload: watchdog acquired manual mode for the fan (pwm_enable=1).
	if got := readTrim(t, enablePath); got != "1" {
		t.Fatalf("post-reload: watchdog did not acquire manual mode; pwm_enable=%q (want 1)", got)
	}

	// 4. Post-shutdown: watchdog.Restore fires and writes the pre-ventd value back.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemon returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of ctx cancel")
	}
	if got := readTrim(t, enablePath); got != "2" {
		t.Fatalf("post-shutdown: watchdog did not restore pwm_enable; got %q (want 2)", got)
	}
}

// TestRegression_Issue466_NoSelfRestart verifies that when the wizard
// completion handler signals restartCh, runDaemon performs an in-process
// config reload and keeps running — it must NOT exit.
//
// Regression guard for #466: the former code path called syscall.Exec to
// replace the process image; under the systemd sandbox (ProtectSystem=strict,
// User=ventd) that Exec failed with EPERM, causing the daemon to exit with
// status 1 on every successful calibration.
func TestRegression_Issue466_NoSelfRestart(t *testing.T) {
	sysfs := t.TempDir()
	pwmPath := filepath.Join(sysfs, "pwm1")
	enablePath := filepath.Join(sysfs, "pwm1_enable")
	fanInput := filepath.Join(sysfs, "fan1_input")
	tempInput := filepath.Join(sysfs, "temp1_input")

	writeFile(t, pwmPath, "0\n")
	writeFile(t, enablePath, "2\n")
	writeFile(t, fanInput, "1200\n")
	writeFile(t, tempInput, "45000\n")

	t.Setenv("VENTD_DISABLE_UEVENT", "1")

	// cfgPath points to a non-existent file: reload will fail gracefully.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	cfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
		Web: config.Web{
			Listen:       "127.0.0.1:0",
			PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
		},
		Sensors:  []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans:     []config.Fan{{Name: "test-fan", Type: "hwmon", PWMPath: pwmPath, RPMPath: fanInput, MinPWM: 0, MaxPWM: 255}},
		Curves:   []config.CurveConfig{{Name: "half", Type: "fixed", Value: 128}},
		Controls: []config.Control{{Fan: "test-fan", Curve: "half"}},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartCh := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonInternal(ctx, cfg, cfgPath, "", logger, "", nil, restartCh, experimental.Flags{}, nil, nil, nil)
	}()

	// Wait for at least one controller tick so we know the daemon is live.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(pwmPath); strings.TrimSpace(string(b)) == "128" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := readTrim(t, pwmPath); got != "128" {
		t.Fatalf("daemon did not start controlling fan: pwm1=%q (want 128)", got)
	}

	// Simulate wizard completion: signal restartCh.
	// The config file doesn't exist so Load will fail — daemon must keep running.
	restartCh <- struct{}{}

	// Give the reload attempt time to complete.
	time.Sleep(200 * time.Millisecond)

	// Assert: daemon is still running; the reload signal must NOT cause an exit.
	select {
	case err := <-done:
		t.Fatalf("daemon exited after reload signal — self-restart regression (#466): err=%v", err)
	default:
		// Good — daemon kept running.
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemon returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of ctx cancel")
	}
}

// TestRegression_Issue466_ReloadFailureIsNonFatal verifies that a failed
// config reload (config file deleted or unreadable) is non-fatal: the daemon
// keeps running with the previous config and does not exit.
func TestRegression_Issue466_ReloadFailureIsNonFatal(t *testing.T) {
	sysfs := t.TempDir()
	pwmPath := filepath.Join(sysfs, "pwm1")
	enablePath := filepath.Join(sysfs, "pwm1_enable")
	fanInput := filepath.Join(sysfs, "fan1_input")
	tempInput := filepath.Join(sysfs, "temp1_input")

	writeFile(t, pwmPath, "0\n")
	writeFile(t, enablePath, "2\n")
	writeFile(t, fanInput, "1200\n")
	writeFile(t, tempInput, "45000\n")

	t.Setenv("VENTD_DISABLE_UEVENT", "1")

	// cfgPath is deliberately absent — any reload attempt will return an error.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	cfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
		Web: config.Web{
			Listen:       "127.0.0.1:0",
			PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
		},
		Sensors:  []config.Sensor{{Name: "cpu", Type: "hwmon", Path: tempInput}},
		Fans:     []config.Fan{{Name: "fan", Type: "hwmon", PWMPath: pwmPath, RPMPath: fanInput, MinPWM: 0, MaxPWM: 255}},
		Curves:   []config.CurveConfig{{Name: "c", Type: "fixed", Value: 64}},
		Controls: []config.Control{{Fan: "fan", Curve: "c"}},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartCh := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonInternal(ctx, cfg, cfgPath, "", logger, "", nil, restartCh, experimental.Flags{}, nil, nil, nil)
	}()

	// Wait for the controller to start (daemon is running normally).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(pwmPath); strings.TrimSpace(string(b)) == "64" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := readTrim(t, pwmPath); got != "64" {
		t.Fatalf("daemon did not start controlling fan: pwm1=%q (want 64)", got)
	}

	// Trigger a reload that will fail (config file absent).
	restartCh <- struct{}{}
	time.Sleep(200 * time.Millisecond)

	// Assert: daemon is still running despite the failed reload.
	select {
	case err := <-done:
		t.Fatalf("daemon exited after failed reload (want: non-fatal): err=%v", err)
	default:
		// Good — daemon kept running with the old config.
	}

	// Assert: fan is still being controlled at the original curve value.
	// Poll rather than a single read: os.WriteFile is O_TRUNC then write, so a
	// readFile that lands between those two steps returns "".  The polling window
	// is long enough to guarantee ≥2 controller ticks at the 50ms poll interval.
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(pwmPath); strings.TrimSpace(string(b)) == "64" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := readTrim(t, pwmPath); got != "64" {
		t.Fatalf("daemon stopped controlling fan after failed reload: pwm1=%q (want 64)", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemon returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of ctx cancel")
	}
}
