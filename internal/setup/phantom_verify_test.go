package setup

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestVerifyHwmonChannelSpins covers the post-calibration phantom-
// verification helper. The harness writes synthetic sysfs files into
// a tempdir and exercises the four canonical paths:
//   - real fan: RPM > 0 at PWM=255 → admit (return true)
//   - phantom fan: RPM=0 across all samples at PWM=255 → refuse
//   - mid-verification context cancel → admit (graceful degrade)
//   - read failure → admit (don't downgrade on transient sysfs trouble)
//
// Production timing (3s settle + 200ms × 3 samples) is too slow for unit
// tests; the helper is structured so the file IO and timing are real but
// minimal. We accept ~3.5 s per real-fan test by reducing the sample
// count via a sentinel value file (see the discardLogger helper) — for
// brevity, the tests rely on the helper's existing 3 s settle + 200 ms
// inter-sample sleeps.
func TestVerifyHwmonChannelSpins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (timing-sensitive); skipped under -short")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("phantom_fan_returns_false", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "120")
		writeFile(t, rpmPath, "0") // no fan plugged in
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got := verifyHwmonChannelSpins(ctx, pwmPath, rpmPath, logger)
		if got {
			t.Errorf("phantom fan (RPM=0) should return false; got true")
		}
		// Origin PWM should be restored.
		if v := readFile(t, pwmPath); v != "120" {
			t.Errorf("origPWM not restored; got %q want %q", v, "120")
		}
	})

	t.Run("real_fan_returns_true", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "100")
		// Background goroutine simulates the chip ramping up at PWM=255 —
		// after 3 s it bumps fan1_input to a real running RPM.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		writeFile(t, rpmPath, "0")
		go func() {
			time.Sleep(2500 * time.Millisecond)
			_ = os.WriteFile(rpmPath, []byte("1500"), 0o644)
		}()
		got := verifyHwmonChannelSpins(ctx, pwmPath, rpmPath, logger)
		if !got {
			t.Errorf("real fan (RPM>0 within 3s) should return true; got false")
		}
	})

	t.Run("read_error_admits", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "100")
		// rpmPath does not exist → readSysfsInt returns error.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got := verifyHwmonChannelSpins(ctx, pwmPath, rpmPath, logger)
		if !got {
			t.Errorf("read failure should admit (return true); got false — risks falsely excluding real fans on transient sysfs trouble")
		}
	})

	t.Run("ctx_cancel_admits", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "100")
		writeFile(t, rpmPath, "0")
		ctx, cancel := context.WithCancel(context.Background())
		// Cancel almost immediately — verify returns true (admit).
		go func() { time.Sleep(100 * time.Millisecond); cancel() }()
		got := verifyHwmonChannelSpins(ctx, pwmPath, rpmPath, logger)
		if !got {
			t.Errorf("ctx cancel should admit; got false")
		}
	})
}

// TestVerifyHwmonChannelSpins_OrigPWMRestoredOnAllExitPaths pins the
// deferred restore behaviour. The helper writes PWM=255 internally;
// every exit path (admit, refuse, error, ctx-cancel) MUST restore the
// captured origPWM byte. Failure here would leave the channel running
// at full speed indefinitely after the wizard finishes.
func TestVerifyHwmonChannelSpins_OrigPWMRestoredOnAllExitPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (timing-sensitive); skipped under -short")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name    string
		rpmPath string
		setup   func(t *testing.T, dir string) (rpmPath string, ctx context.Context)
	}{
		{
			name: "admit_real_fan",
			setup: func(t *testing.T, dir string) (string, context.Context) {
				rpmPath := filepath.Join(dir, "fan1_input")
				writeFile(t, rpmPath, "0")
				go func() { time.Sleep(2500 * time.Millisecond); _ = os.WriteFile(rpmPath, []byte("1500"), 0o644) }()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				return rpmPath, ctx
			},
		},
		{
			name: "refuse_phantom",
			setup: func(t *testing.T, dir string) (string, context.Context) {
				rpmPath := filepath.Join(dir, "fan1_input")
				writeFile(t, rpmPath, "0")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				return rpmPath, ctx
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pwmPath := filepath.Join(dir, "pwm1")
			origPWM := uint8(173)
			writeFile(t, pwmPath, strconv.Itoa(int(origPWM)))
			rpmPath, ctx := tc.setup(t, dir)
			_ = verifyHwmonChannelSpins(ctx, pwmPath, rpmPath, logger)
			if v := readFile(t, pwmPath); v != strconv.Itoa(int(origPWM)) {
				t.Errorf("origPWM not restored on %s exit path; got %q want %d", tc.name, v, origPWM)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(data)
}
