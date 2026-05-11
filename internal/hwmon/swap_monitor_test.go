package hwmon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// seedHwmonLayout creates a tempdir-rooted /sys-like layout with
// `stableDevice/hwmon/hwmon<n>/pwm1` populated. Returns the live
// pwm1 path and the stable-device directory.
func seedHwmonLayout(t *testing.T, root, devSubpath, hwmonName string) (pwmPath, stableDev string) {
	t.Helper()
	stableDev = filepath.Join(root, devSubpath)
	hwmonDir := filepath.Join(stableDev, "hwmon", hwmonName)
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("seed mkdir: %v", err)
	}
	pwmPath = filepath.Join(hwmonDir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("128\n"), 0o644); err != nil {
		t.Fatalf("seed pwm: %v", err)
	}
	return pwmPath, stableDev
}

// TestReResolveAll_NoSwapReportsUnchanged pins the steady-state
// branch: when stored paths still exist on disk, ReResolveAll
// reports Changed=false and ResolvedPath==StoredPath for every
// input. RULE-HWMON-SWAP-MONITOR.
func TestReResolveAll_NoSwapReportsUnchanged(t *testing.T) {
	root := t.TempDir()
	pwmPath, stableDev := seedHwmonLayout(t, root, "devices/platform/nct6687.2608", "hwmon2")

	got := ReResolveAll([]ChannelInput{
		{StoredPath: pwmPath, StableDevice: stableDev},
	})
	if len(got) != 1 {
		t.Fatalf("len(detections)=%d, want 1", len(got))
	}
	if got[0].Changed {
		t.Errorf("Changed=true for path that still exists: %+v", got[0])
	}
	if got[0].ResolvedPath != pwmPath {
		t.Errorf("ResolvedPath=%q, want %q", got[0].ResolvedPath, pwmPath)
	}
}

// TestReResolveAll_SwapDetectedAndRebased pins the swap path: after
// the hwmonN dir is renamed under the same stable-device anchor,
// ReResolveAll rebases StoredPath onto the new hwmonN dir and
// reports Changed=true. RULE-HWMON-SWAP-MONITOR.
func TestReResolveAll_SwapDetectedAndRebased(t *testing.T) {
	root := t.TempDir()
	pwmPath, stableDev := seedHwmonLayout(t, root, "devices/platform/nct6687.2608", "hwmon2")

	// Simulate a module reload that renumbered the hwmon from
	// hwmon2 → hwmon5 under the same stable device.
	oldHwmon := filepath.Dir(pwmPath)
	newHwmon := filepath.Join(stableDev, "hwmon", "hwmon5")
	if err := os.Rename(oldHwmon, newHwmon); err != nil {
		t.Fatalf("rename hwmon: %v", err)
	}

	got := ReResolveAll([]ChannelInput{
		{StoredPath: pwmPath, StableDevice: stableDev},
	})
	if len(got) != 1 {
		t.Fatalf("len(detections)=%d, want 1", len(got))
	}
	if !got[0].Changed {
		t.Errorf("Changed=false after rename: %+v", got[0])
	}
	wantResolved := filepath.Join(newHwmon, "pwm1")
	if got[0].ResolvedPath != wantResolved {
		t.Errorf("ResolvedPath=%q, want %q", got[0].ResolvedPath, wantResolved)
	}
}

// TestReResolveAll_NoStableDeviceLeavesUnchanged pins that an empty
// StableDevice (caller couldn't capture a stable anchor at startup)
// reports Changed=false without attempting a rebase — the helper
// doesn't make up data.
func TestReResolveAll_NoStableDeviceLeavesUnchanged(t *testing.T) {
	got := ReResolveAll([]ChannelInput{
		{StoredPath: "/nonexistent/pwm1", StableDevice: ""},
	})
	if got[0].Changed {
		t.Errorf("Changed=true with empty StableDevice: %+v", got[0])
	}
}

// TestMonitorSwap_StopsOnContextCancel pins the lifecycle: ctx.Done
// stops the loop within one ticker tick. RULE-HWMON-SWAP-MONITOR.
func TestMonitorSwap_StopsOnContextCancel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())

	root := t.TempDir()
	pwmPath, stableDev := seedHwmonLayout(t, root, "devices/platform/nct6687.2608", "hwmon2")

	done := make(chan struct{})
	go func() {
		MonitorSwap(ctx, []ChannelInput{
			{StoredPath: pwmPath, StableDevice: stableDev},
		}, 50*time.Millisecond, logger, nil)
		close(done)
	}()

	// Let the monitor run for a couple of ticks, then cancel.
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		t.Fatal("MonitorSwap did not return within 1s after ctx cancel")
	}
}

// TestMonitorSwap_FiresHandlerOnDetection pins the callback wiring:
// when a swap is detected during a tick, the SwapHandler is invoked
// with the SwapDetection. RULE-HWMON-SWAP-MONITOR.
func TestMonitorSwap_FiresHandlerOnDetection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	pwmPath, stableDev := seedHwmonLayout(t, root, "devices/platform/nct6687.2608", "hwmon2")

	var (
		callMu  sync.Mutex
		calls   []SwapDetection
		fireCnt atomic.Int32
	)
	onSwap := func(d SwapDetection) {
		callMu.Lock()
		calls = append(calls, d)
		callMu.Unlock()
		fireCnt.Add(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go MonitorSwap(ctx, []ChannelInput{
		{StoredPath: pwmPath, StableDevice: stableDev},
	}, 30*time.Millisecond, logger, onSwap)

	// Wait briefly to ensure the monitor has run at least one tick
	// (no swap → no callback).
	time.Sleep(80 * time.Millisecond)
	if got := fireCnt.Load(); got != 0 {
		t.Errorf("handler fired %d times before any swap; want 0", got)
	}

	// Trigger the swap.
	oldHwmon := filepath.Dir(pwmPath)
	newHwmon := filepath.Join(stableDev, "hwmon", "hwmon7")
	if err := os.Rename(oldHwmon, newHwmon); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Wait for the handler to fire (deadline: 1s).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && fireCnt.Load() == 0 {
		time.Sleep(15 * time.Millisecond)
	}
	cancel()

	callMu.Lock()
	defer callMu.Unlock()
	if len(calls) == 0 {
		t.Fatal("handler never fired after swap")
	}
	want := filepath.Join(newHwmon, "pwm1")
	if calls[0].ResolvedPath != want {
		t.Errorf("ResolvedPath=%q, want %q", calls[0].ResolvedPath, want)
	}
	if !calls[0].Changed {
		t.Error("Changed=false in handler invocation")
	}
}

// TestMonitorSwap_EmptyInputsExitsCleanly pins the no-channels path:
// the monitor logs an info line and returns immediately rather than
// starting a ticker over an empty slice. RULE-HWMON-SWAP-MONITOR.
func TestMonitorSwap_EmptyInputsExitsCleanly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		MonitorSwap(ctx, nil, 50*time.Millisecond, logger, nil)
		close(done)
	}()

	select {
	case <-done:
		// Expected — empty inputs short-circuit before the ticker.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("MonitorSwap with empty inputs did not return promptly")
	}
}

// TestMonitorSwap_NilHandlerStillLogs pins that a nil SwapHandler
// is a clean no-op for the dispatch side but does NOT silence the
// observability log line — operators still see "swap detected"
// even when no remap dispatch is wired. RULE-HWMON-SWAP-MONITOR.
func TestMonitorSwap_NilHandlerStillLogs(t *testing.T) {
	root := t.TempDir()
	pwmPath, stableDev := seedHwmonLayout(t, root, "devices/platform/nct6687.2608", "hwmon2")

	// Capture log output.
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go MonitorSwap(ctx, []ChannelInput{
		{StoredPath: pwmPath, StableDevice: stableDev},
	}, 30*time.Millisecond, logger, nil)

	// Trigger swap.
	oldHwmon := filepath.Dir(pwmPath)
	newHwmon := filepath.Join(stableDev, "hwmon", "hwmon9")
	if err := os.Rename(oldHwmon, newHwmon); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Wait until the WARN appears or deadline.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if buf.contains("swap detected") {
			cancel()
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	cancel()
	t.Errorf("expected 'swap detected' WARN log; got %q", buf.string())
}

// TestDefaultSwapMonitorInterval_Is10Min pins the locked-constant
// so a future refactor can't silently retune the cadence.
// RULE-HWMON-SWAP-MONITOR.
func TestDefaultSwapMonitorInterval_Is10Min(t *testing.T) {
	if DefaultSwapMonitorInterval != 10*time.Minute {
		t.Errorf("DefaultSwapMonitorInterval=%v, want 10m", DefaultSwapMonitorInterval)
	}
}

// safeBuffer is a goroutine-safe bytes.Buffer-ish helper for
// log-capture tests. The slog handler writes from the monitor
// goroutine; the test reads from the main test goroutine; -race
// would flag the unprotected access.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) contains(needle string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i+len(needle) <= len(b.buf); i++ {
		if string(b.buf[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

func (b *safeBuffer) string() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
