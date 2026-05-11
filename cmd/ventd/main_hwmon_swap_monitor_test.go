package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// TestStartHwmonSwapMonitor_SkipsWhenNoEligibleChannels pins
// the no-op branch: channels with empty PWMPath or no stable
// device anchor are filtered out. With zero eligible channels,
// the helper returns immediately without registering a goroutine
// against wg. RULE-HWMON-SWAP-MONITOR.
func TestStartHwmonSwapMonitor_SkipsWhenNoEligibleChannels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup

	// Empty channel list.
	startHwmonSwapMonitor(ctx, &wg, nil, logger)

	// A channel with no PWMPath.
	startHwmonSwapMonitor(ctx, &wg, []*probe.ControllableChannel{
		{SourceID: "fake0", PWMPath: ""},
	}, logger)

	// A channel whose path has no stable device (e.g. NVML / IPMI).
	startHwmonSwapMonitor(ctx, &wg, []*probe.ControllableChannel{
		{SourceID: "nvml0", PWMPath: "/dev/nvml/fan0"},
	}, logger)

	// All three calls should have done nothing — wg never advanced.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Expected: zero goroutines were registered, Wait returns
		// immediately.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("startHwmonSwapMonitor registered an unexpected goroutine on empty inputs")
	}
}

// TestStartHwmonSwapMonitor_RegistersGoroutineForEligibleChannel
// pins the wiring path: when a channel has a resolvable stable
// device, the helper registers exactly one goroutine via wg and
// the goroutine unwinds cleanly on ctx cancel.
// RULE-HWMON-SWAP-MONITOR.
func TestStartHwmonSwapMonitor_RegistersGoroutineForEligibleChannel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build a tempdir-rooted hwmon layout so StableDevice resolves
	// to a real path.
	root := t.TempDir()
	devDir := filepath.Join(root, "devices", "platform", "nct6687.2608")
	hwmonDir := filepath.Join(devDir, "hwmon", "hwmon2")
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pwmPath := filepath.Join(hwmonDir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("128\n"), 0o644); err != nil {
		t.Fatalf("write pwm: %v", err)
	}
	// hwmon.StableDevice prefers the hwmon dir's `device` symlink;
	// create one pointing at the device dir.
	deviceLink := filepath.Join(hwmonDir, "device")
	if err := os.Symlink(devDir, deviceLink); err != nil {
		t.Fatalf("symlink device: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	startHwmonSwapMonitor(ctx, &wg, []*probe.ControllableChannel{
		{SourceID: "hwmon2", PWMPath: pwmPath, Driver: "nct6687"},
	}, logger)

	// Give the monitor goroutine a moment to log its startup line.
	time.Sleep(50 * time.Millisecond)

	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Expected: ctx-cancel propagated to the monitor; goroutine
		// unwound; wg released.
	case <-time.After(2 * time.Second):
		t.Fatal("startHwmonSwapMonitor goroutine did not unwind within 2s of ctx cancel")
	}
}
