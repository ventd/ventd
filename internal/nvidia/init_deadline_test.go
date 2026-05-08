//go:build !nonvidia

package nvidia

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// RULE-GPU-PR2D-09: nvidia.InitWithDeadline returns wrapped
// ErrLibraryUnavailable on timeout; the in-flight dlopen goroutine
// is orphaned (uncancellable). These subtests exercise the inner
// initWithDeadline so every branch is covered without touching
// the package-level loadOnce state.

func TestInitWithDeadline_Rules(t *testing.T) {
	t.Run("RULE-GPU-PR2D-09_timeout_returns_wrapped_unavailable", func(t *testing.T) {
		// A loader that never returns within the deadline must surface
		// as wrapped ErrLibraryUnavailable with "timed out" in the
		// message so operators can distinguish a hung dlopen from a
		// genuine "library not present" error in the journal.
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		hanging := func(_ *slog.Logger) error {
			select {
			case <-stop:
				return nil
			case <-time.After(5 * time.Second):
				return nil
			}
		}
		err := initWithDeadline(context.Background(), nil, 50*time.Millisecond, hanging)
		if err == nil {
			t.Fatal("expected error on timeout, got nil")
		}
		if !errors.Is(err, ErrLibraryUnavailable) {
			t.Errorf("expected errors.Is(err, ErrLibraryUnavailable), got %v", err)
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("expected 'timed out' in message, got %q", err.Error())
		}
	})

	t.Run("RULE-GPU-PR2D-09_fast_path_passes_through", func(t *testing.T) {
		// A loader that completes within the deadline must return
		// whatever the loader returned (nil or its own error) without
		// the timeout wrapper.
		ok := func(_ *slog.Logger) error { return nil }
		if err := initWithDeadline(context.Background(), nil, time.Second, ok); err != nil {
			t.Errorf("expected nil on fast path, got %v", err)
		}

		sentinel := errors.New("loader said no")
		erroring := func(_ *slog.Logger) error { return sentinel }
		err := initWithDeadline(context.Background(), nil, time.Second, erroring)
		if !errors.Is(err, sentinel) {
			t.Errorf("expected loader error verbatim, got %v", err)
		}
	})

	t.Run("RULE-GPU-PR2D-09_ctx_cancel_returns_wrapped_unavailable", func(t *testing.T) {
		// A pre-cancelled ctx must short-circuit and surface as wrapped
		// ErrLibraryUnavailable with the cancellation cause embedded.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		hanging := func(_ *slog.Logger) error {
			time.Sleep(5 * time.Second)
			return nil
		}
		err := initWithDeadline(ctx, nil, time.Second, hanging)
		if err == nil {
			t.Fatal("expected error on ctx cancel, got nil")
		}
		if !errors.Is(err, ErrLibraryUnavailable) {
			t.Errorf("expected errors.Is(err, ErrLibraryUnavailable), got %v", err)
		}
		if !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("expected 'context canceled' in message, got %q", err.Error())
		}
	})

	t.Run("RULE-GPU-PR2D-09_zero_timeout_disables_deadline", func(t *testing.T) {
		// A timeout <= 0 disables the timeout entirely (equivalent
		// to plain Init), but a pre-cancelled ctx still short-
		// circuits before any loader call.
		ok := func(_ *slog.Logger) error { return nil }
		if err := initWithDeadline(context.Background(), nil, 0, ok); err != nil {
			t.Errorf("expected nil with zero timeout, got %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := initWithDeadline(ctx, nil, 0, ok)
		if err == nil {
			t.Fatal("expected error on pre-cancelled ctx with zero timeout")
		}
		if !errors.Is(err, ErrLibraryUnavailable) {
			t.Errorf("expected errors.Is(err, ErrLibraryUnavailable), got %v", err)
		}
	})

	t.Run("RULE-GPU-PR2D-09_nil_logger_uses_default", func(t *testing.T) {
		// A nil logger must not panic; the function falls back to
		// slog.Default() so the daemon's startup path can call
		// InitWithDeadline before the structured logger is built.
		ok := func(_ *slog.Logger) error { return nil }
		if err := initWithDeadline(context.Background(), nil, time.Second, ok); err != nil {
			t.Errorf("expected nil with nil logger, got %v", err)
		}
	})
}
