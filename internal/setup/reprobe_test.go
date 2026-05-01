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
