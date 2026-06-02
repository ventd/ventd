package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/experimental"
)

// newTestCalManager builds a calibrate.Manager backed by a temp file for tests
// that exercise resolveHwmonPaths' RemapKey call.
func newTestCalManager(t *testing.T) *calibrate.Manager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return calibrate.New(filepath.Join(t.TempDir(), "calibration.json"), logger, nil)
}

// TestAnyControlledFanPWMChanged pins the predicate that decides whether a
// runtime hwmon renumber requires respawning controllers
// (RULE-CTRL-REBIND-FOLLOW): true only when a fan whose PWM path actually moved
// is bound to a control. An uncontrolled fan moving, a sensor-only move, an
// rpm-only move, or no move at all must all be false.
func TestAnyControlledFanPWMChanged(t *testing.T) {
	cfg := &config.Config{
		Controls: []config.Control{{Fan: "cpu_fan", Curve: "c"}},
	}
	cases := []struct {
		name  string
		moves []pathMove
		want  bool
	}{
		{"no moves", nil, false},
		{"controlled fan pwm moved", []pathMove{{Kind: "fan", Name: "cpu_fan", OldPWM: "/h9/pwm1", NewPWM: "/h10/pwm1"}}, true},
		{"uncontrolled fan moved", []pathMove{{Kind: "fan", Name: "case_fan", OldPWM: "/h9/pwm2", NewPWM: "/h10/pwm2"}}, false},
		{"sensor moved only", []pathMove{{Kind: "sensor", Name: "cpu_fan", OldPWM: "/h9/temp1", NewPWM: "/h10/temp1"}}, false},
		{"controlled fan rpm-only move", []pathMove{{Kind: "fan", Name: "cpu_fan", OldPWM: "/h9/pwm1", NewPWM: "/h9/pwm1", OldRPM: "/h9/fan1", NewRPM: "/h10/fan1"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anyControlledFanPWMChanged(tc.moves, cfg); got != tc.want {
				t.Fatalf("anyControlledFanPWMChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

// seedRenumberLayout builds a fake sysfs where one fan's hwmon dir lives under a
// stable device anchor, so hwmon.ResolvePath can rebase it after a renumber.
// Returns the stable device dir and the initial pwm path under hwmon9.
func seedRenumberLayout(t *testing.T, root string) (stableDev, pwm9 string) {
	t.Helper()
	stableDev = filepath.Join(root, "devices", "platform", "fake.0")
	hwmon9 := filepath.Join(stableDev, "hwmon", "hwmon9")
	if err := os.MkdirAll(hwmon9, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(hwmon9, "pwm1"), "0\n")
	writeFile(t, filepath.Join(hwmon9, "pwm1_enable"), "2\n")
	writeFile(t, filepath.Join(hwmon9, "fan1_input"), "1200\n")
	writeFile(t, filepath.Join(hwmon9, "temp1_input"), "45000\n")
	return stableDev, filepath.Join(hwmon9, "pwm1")
}

// renumber simulates an hwmonN index shift: the chip's hwmon dir moves from
// hwmon9 to hwmon10 under the same stable device (rmmod+modprobe / hotplug).
func renumber(t *testing.T, stableDev string) (pwm10 string) {
	t.Helper()
	old := filepath.Join(stableDev, "hwmon", "hwmon9")
	neu := filepath.Join(stableDev, "hwmon", "hwmon10")
	if err := os.Rename(old, neu); err != nil {
		t.Fatalf("simulate renumber: %v", err)
	}
	return filepath.Join(neu, "pwm1")
}

// TestResolveHwmonPaths_ReportsFanRenumber checks that resolveHwmonPaths rebases
// a moved fan path against its stable device anchor and reports the move.
func TestResolveHwmonPaths_ReportsFanRenumber(t *testing.T) {
	root := t.TempDir()
	stableDev, pwm9 := seedRenumberLayout(t, root)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cal := newTestCalManager(t)

	cfg := &config.Config{
		Fans: []config.Fan{{Name: "cpu_fan", Type: "hwmon", PWMPath: pwm9, HwmonDevice: stableDev}},
	}

	// No move while the original path still exists.
	if moves := resolveHwmonPaths(cfg, cal, logger); len(moves) != 0 {
		t.Fatalf("unexpected moves before renumber: %+v", moves)
	}

	pwm10 := renumber(t, stableDev)
	moves := resolveHwmonPaths(cfg, cal, logger)
	if len(moves) != 1 || moves[0].Kind != "fan" || moves[0].NewPWM != pwm10 {
		t.Fatalf("expected one fan move to %s, got %+v", pwm10, moves)
	}
	if cfg.Fans[0].PWMPath != pwm10 {
		t.Fatalf("config fan path not rebased: %s (want %s)", cfg.Fans[0].PWMPath, pwm10)
	}
}

// TestResolveHwmonPaths_NoAnchorWarnsCannotFollow pins the edge case where a fan
// has no stable device anchor: the path can't be rebased, so no move is reported
// (the controller will hand the fan back to firmware), and a vanished path is
// surfaced for the operator.
func TestResolveHwmonPaths_NoAnchorWarnsCannotFollow(t *testing.T) {
	root := t.TempDir()
	_, pwm9 := seedRenumberLayout(t, root)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cal := newTestCalManager(t)

	// Fan with NO HwmonDevice anchor.
	cfg := &config.Config{
		Fans: []config.Fan{{Name: "cpu_fan", Type: "hwmon", PWMPath: pwm9}},
	}
	// Drop the whole hwmon9 dir to simulate a renumber we can't follow.
	if err := os.RemoveAll(filepath.Dir(pwm9)); err != nil {
		t.Fatal(err)
	}
	if moves := resolveHwmonPaths(cfg, cal, logger); len(moves) != 0 {
		t.Fatalf("a fan without a stable anchor must report no move; got %+v", moves)
	}
}

// TestRebindFollow_ControllerFollowsRenumber is the end-to-end proof of
// RULE-CTRL-REBIND-FOLLOW: a fan controlled at hwmon9/pwm1 keeps being driven at
// hwmon10/pwm1 after a runtime renumber, the new path is acquired in manual
// mode, and on shutdown the new path (not the vanished old one) is restored.
func TestRebindFollow_ControllerFollowsRenumber(t *testing.T) {
	root := t.TempDir()
	stableDev, pwm9 := seedRenumberLayout(t, root)

	t.Setenv("VENTD_DISABLE_UEVENT", "1")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	mkCfg := func(pwm string) *config.Config {
		return &config.Config{
			Version:      config.CurrentVersion,
			PollInterval: config.Duration{Duration: 50 * time.Millisecond},
			Web: config.Web{
				Listen:       "127.0.0.1:0",
				PasswordHash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq",
			},
			Fans:     []config.Fan{{Name: "fan", Type: "hwmon", PWMPath: pwm, HwmonDevice: stableDev, MinPWM: 0, MaxPWM: 255}},
			Curves:   []config.CurveConfig{{Name: "c", Type: "fixed", Value: 128}},
			Controls: []config.Control{{Fan: "fan", Curve: "c"}},
		}
	}

	// The on-disk config always names the original (hwmon9) path —
	// resolveHwmonPaths is what rebinds it to the live hwmonN on reload.
	orig := configLoader
	configLoader = func(_ string) (*config.Config, error) { return mkCfg(pwm9), nil }
	defer func() { configLoader = orig }()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartCh := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonInternal(ctx, mkCfg(pwm9), cfgPath, "", logger, nil, restartCh, nil, experimental.Flags{}, func() error { return nil }, func() error { return nil }, nil, nil, "")
	}()

	// Controller drives the fan at hwmon9 (fixed curve → pwm=128, manual mode).
	waitForValue(t, pwm9, "128")
	if got := readTrim(t, filepath.Join(filepath.Dir(pwm9), "pwm1_enable")); got != "1" {
		t.Fatalf("pre-renumber: manual mode not acquired; pwm_enable=%q", got)
	}

	// Simulate the renumber, then blank the new path so we can prove the
	// RESPAWNED controller (not a stale write) drives it.
	pwm10 := renumber(t, stableDev)
	enable10 := filepath.Join(filepath.Dir(pwm10), "pwm1_enable")
	writeFile(t, pwm10, "0\n")
	writeFile(t, enable10, "2\n")

	// Trigger the rebind (in production the uevent/swap monitor does this).
	restartCh <- struct{}{}

	// The respawned controller must drive the NEW path and acquire manual mode.
	waitForValue(t, pwm10, "128")
	if got := readTrim(t, enable10); got != "1" {
		t.Fatalf("post-renumber: respawned controller did not acquire manual mode; pwm_enable=%q (want 1)", got)
	}

	// Clean shutdown restores the NEW path to firmware auto.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not return within 5s of ctx cancel")
	}
	if got := readTrim(t, enable10); got != "2" {
		t.Fatalf("post-shutdown: new path not restored; pwm_enable=%q (want 2)", got)
	}
}

// waitForValue polls path until it holds want or controllerFirstTickWait
// elapses (reusing the generous deadline from the #466 regression tests).
func waitForValue(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(controllerFirstTickWait)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s did not reach %q within %v (last=%q)", path, want, controllerFirstTickWait, readTrim(t, path))
}
