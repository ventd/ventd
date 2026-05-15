package setup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
)

// TestReProber_FiresAfterLoadModule asserts that a successful LoadModule call
// invokes the registered ReProber callback, so the persisted
// wizard.initial_outcome is updated against the post-modprobe kernel state
// (#766). Without this, the wizard's monitor-only outcome from the pre-load
// probe stays in KV until the next daemon restart.
func TestReProber_FiresAfterLoadModule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())
	m.settleAfterModprobe = 0 // skip the 2s wait in tests

	// Stub modprobe so we don't shell out.
	cleanup := SetModprobeCmd(func(ctx context.Context, module string) ([]byte, error) {
		return []byte("loaded"), nil
	})
	t.Cleanup(func() { SetModprobeCmd(cleanup) })

	// Stub the modules-load.d write so we don't need root.
	cleanupW := SetModulesLoadWrite(func(path string, data []byte) error { return nil })
	t.Cleanup(func() { SetModulesLoadWrite(cleanupW) })

	var fired atomic.Int32
	m.SetReProber(func(ctx context.Context) error {
		fired.Add(1)
		return nil
	})

	if _, err := m.LoadModule(context.Background(), "nct6683"); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("ReProber fired %d times, want 1", got)
	}
}

// TestReProber_NotFiredOnFailedLoadModule asserts that the ReProber is NOT
// invoked when modprobe itself fails — there's nothing new to probe in the
// kernel, so persisting a fresh outcome would be wasted work and could
// overwrite a valid earlier persistence with a worse one if the probe
// transiently flakes.
func TestReProber_NotFiredOnFailedLoadModule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())
	m.settleAfterModprobe = 0

	cleanup := SetModprobeCmd(func(ctx context.Context, module string) ([]byte, error) {
		return []byte("modprobe: ERROR: could not insert module"), errors.New("exit status 1")
	})
	t.Cleanup(func() { SetModprobeCmd(cleanup) })

	var fired atomic.Int32
	m.SetReProber(func(ctx context.Context) error {
		fired.Add(1)
		return nil
	})

	if _, err := m.LoadModule(context.Background(), "nct6683"); err == nil {
		t.Fatal("LoadModule succeeded; want error from stub")
	}
	if got := fired.Load(); got != 0 {
		t.Errorf("ReProber fired %d times after failed modprobe, want 0", got)
	}
}

// TestReProber_NilIsNoOp asserts that LoadModule succeeds even when no
// ReProber is wired (the pre-#766 behaviour for callers that don't care
// about the persisted KV outcome — e.g. unit tests that don't touch state).
func TestReProber_NilIsNoOp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())
	m.settleAfterModprobe = 0

	cleanup := SetModprobeCmd(func(ctx context.Context, module string) ([]byte, error) {
		return nil, nil
	})
	t.Cleanup(func() { SetModprobeCmd(cleanup) })
	cleanupW := SetModulesLoadWrite(func(path string, data []byte) error { return nil })
	t.Cleanup(func() { SetModulesLoadWrite(cleanupW) })

	// No SetReProber call — m.reprobeFn stays nil.
	if _, err := m.LoadModule(context.Background(), "nct6683"); err != nil {
		t.Fatalf("LoadModule with nil ReProber: %v", err)
	}
}

// TestReProber_ErrorLoggedDoesNotBlockSuccess asserts that a ReProber that
// returns an error is logged at WARN but does not propagate to the caller
// — the modprobe itself succeeded, so LoadModule's return must reflect that.
func TestReProber_ErrorLoggedDoesNotBlockSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())
	m.settleAfterModprobe = 0

	cleanup := SetModprobeCmd(func(ctx context.Context, module string) ([]byte, error) {
		return nil, nil
	})
	t.Cleanup(func() { SetModprobeCmd(cleanup) })
	cleanupW := SetModulesLoadWrite(func(path string, data []byte) error { return nil })
	t.Cleanup(func() { SetModulesLoadWrite(cleanupW) })

	m.SetReProber(func(ctx context.Context) error {
		return errors.New("simulated probe error")
	})

	if _, err := m.LoadModule(context.Background(), "nct6683"); err != nil {
		t.Fatalf("LoadModule wrapped a non-fatal ReProber error: %v", err)
	}
}

// TestReProber_FiresAfterFinalize asserts that the wizard's finalize phase
// invokes the registered ReProber callback so wizard.initial_outcome reflects
// the post-calibration kernel state regardless of whether the installing_driver
// phase ran (#1108). Hosts whose driver was already loaded at first boot skip
// installing_driver entirely; without this the persisted KV outcome stays at
// a stale "monitor_only" value forever, leaving smart-mode subsystems inert
// despite controllers actively driving PWM.
func TestReProber_FiresAfterFinalize(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())

	var fired atomic.Int32
	m.SetReProber(func(ctx context.Context) error {
		fired.Add(1)
		return nil
	})

	m.afterFinalize(context.Background(), "FinalizeWithChannels")
	if got := fired.Load(); got != 1 {
		t.Errorf("ReProber fired %d times after afterFinalize, want 1", got)
	}
}

// TestReProber_FinalizeNilIsNoOp asserts that afterFinalize is safe when no
// ReProber is wired (the test scaffolding default — production always wires
// it via cmd/ventd/main.go).
func TestReProber_FinalizeNilIsNoOp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())

	// No SetReProber call — m.reprobeFn stays nil.
	m.afterFinalize(context.Background(), "FinalizeWithChannels")
}

// TestReProber_FinalizeErrorLoggedDoesNotPanic asserts that a ReProber
// returning an error during finalize is logged at WARN but does not panic
// or otherwise disrupt the wizard goroutine. The wizard's generated config
// is unaffected by a stale KV outcome — only smart-mode subsystem activation
// is delayed until the next reprobe trigger.
func TestReProber_FinalizeErrorLoggedDoesNotPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	m := NewWithRoots(cal, logger, t.TempDir(), t.TempDir(), t.TempDir())

	m.SetReProber(func(ctx context.Context) error {
		return errors.New("simulated probe error")
	})

	// Bare call: a panic here would fail the test via the runtime's
	// goroutine-panic surface rather than via t.Fatal — the assertion is
	// "no panic", which is the absence of an outcome we can't directly
	// observe other than by reaching the next line.
	m.afterFinalize(context.Background(), "FinalizeWithChannels")
}
