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
)

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
		done <- runDaemonInternal(ctx, cfg, cfgPath, "", logger, "", nil, restartCh)
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
		done <- runDaemonInternal(ctx, cfg, cfgPath, "", logger, "", nil, restartCh)
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
