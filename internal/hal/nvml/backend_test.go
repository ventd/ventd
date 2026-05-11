package nvml

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal"
)

// TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetAbandoned exercises the
// deadline branch of nvmlResetWithDeadline. A blocking fake reset
// holds for longer than the configured deadline; the caller must
// return with a wrapped context.DeadlineExceeded inside the locked
// budget (no leak past the parent). Per RULE-WD-PER-SYSCALL-DEADLINE
// / issue #1040.
func TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetAbandoned(t *testing.T) {
	// Swap the package-level seam to a fake that blocks until
	// released. The release+wait dance is load-bearing under -race:
	// the orphan goroutine spawned by nvmlResetWithDeadline still
	// holds a closure over nvmlResetFn after the parent returns; we
	// must let it complete before the t.Cleanup restores the seam.
	orig := nvmlResetFn
	blockUntil := make(chan struct{})
	fnReturned := make(chan struct{})
	nvmlResetFn = func(idx uint) error {
		<-blockUntil
		close(fnReturned)
		return nil
	}
	t.Cleanup(func() {
		close(blockUntil)
		<-fnReturned
		nvmlResetFn = orig
	})

	start := time.Now()
	err := nvmlResetWithDeadline(0, 50*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("nvmlResetWithDeadline with a blocking fake returned nil err")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "abandoned") {
		t.Errorf("expected wrapped DeadlineExceeded or 'abandoned' in err, got %q", err.Error())
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("nvmlResetWithDeadline took %v despite a 50 ms deadline; abandonment branch is not unblocking the caller", elapsed)
	}
}

// TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetSuccessPassthrough
// asserts the happy path: a fast fake reset completes within budget
// and the deadline branch never fires. Verifies the wrapper is
// transparent for the production case.
func TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetSuccessPassthrough(t *testing.T) {
	orig := nvmlResetFn
	t.Cleanup(func() { nvmlResetFn = orig })
	nvmlResetFn = func(idx uint) error { return nil }

	if err := nvmlResetWithDeadline(0, 1*time.Second); err != nil {
		t.Errorf("nvmlResetWithDeadline with fast fake returned %v, want nil", err)
	}
}

// TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetBackendIntegration
// integrates the deadline wrapper with the Backend.Restore path so a
// future refactor that bypasses nvmlResetWithDeadline is caught at
// CI time.
func TestRULE_WD_PER_SYSCALL_DEADLINE_NVMLResetBackendIntegration(t *testing.T) {
	origFn := nvmlResetFn
	origDeadline := NVMLResetDeadline
	NVMLResetDeadline = 50 * time.Millisecond

	blockUntil := make(chan struct{})
	fnReturned := make(chan struct{})
	nvmlResetFn = func(idx uint) error {
		<-blockUntil
		close(fnReturned)
		return nil
	}
	t.Cleanup(func() {
		close(blockUntil)
		<-fnReturned
		nvmlResetFn = origFn
		NVMLResetDeadline = origDeadline
	})

	var buf bytes.Buffer
	b := NewBackend(slog.New(slog.NewTextHandler(&buf, nil)))
	ch := hal.Channel{ID: "0", Opaque: State{Index: "0"}}

	start := time.Now()
	err := b.Restore(ch)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("Backend.Restore with blocking NVML returned nil err")
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("Backend.Restore took %v despite a 50 ms NVMLResetDeadline; deadline branch did not fire", elapsed)
	}
}

// TestRegression_Issue380_RestoreParseIndexError verifies that Restore
// propagates a parseIndex failure instead of swallowing it with a nil return.
// regresses #380
func TestRegression_Issue380_RestoreParseIndexError(t *testing.T) {
	b := NewBackend(nil)
	ch := hal.Channel{
		ID:     "abc",
		Opaque: State{Index: "abc"},
	}

	err := b.Restore(ch)
	if err == nil {
		t.Fatal("Restore: want non-nil error for invalid gpu index, got nil")
	}

	// Error must use the same sentinel wrap style as Read() / Write().
	const sentinel = "hal/nvml: parse gpu index"
	if !strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Restore: error %q does not contain sentinel %q", err.Error(), sentinel)
	}

	// Confirm Read returns the identical sentinel so both paths stay in sync.
	_, readErr := b.Read(ch)
	if readErr == nil {
		t.Fatal("Read: want non-nil error for invalid gpu index, got nil")
	}
	if !strings.Contains(readErr.Error(), sentinel) {
		t.Fatalf("Read: error %q does not contain sentinel %q", readErr.Error(), sentinel)
	}
}
