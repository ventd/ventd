// Package watchdog: per-syscall deadline helpers.
//
// These helpers wrap the file and NVML primitives that the watchdog's
// restore + register paths consume so a hung driver cannot stall a
// single goroutine past the RestoreCtx budget OR block daemon startup
// indefinitely (issues #1038, #1040, #1041, #1042).
//
// The pattern is a goroutine + ctx-done select: the real syscall runs
// off the caller goroutine; the caller returns either when the syscall
// goroutine signals completion OR when ctx fires (typically driven by
// DefaultRestoreBudget for restore paths or DefaultRegisterDeadline
// for Register-time reads).
//
// On deadline, the inner goroutine is abandoned — neither a Go
// goroutine nor an `os.File` operation is cancellable from outside,
// so the abandoned goroutine continues to run until the kernel
// returns from the underlying syscall. systemd's `KillMode=process`
// reaps it on daemon shutdown. The contract is identical to the
// RestoreCtx-level abandonment in RULE-WD-RESTORE-BUDGET, just
// applied per-syscall instead of per-channel.
//
// Bound: RULE-WD-PER-SYSCALL-DEADLINE.
package watchdog

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// DefaultPerSyscallDeadline is the per-syscall budget used by the
// watchdog's read/write helpers when the caller does not supply an
// explicit ctx deadline. 500 ms is tight enough to fail-fast on a
// wedged driver and loose enough to absorb worst-case sysfs latency
// on slow systems (laptop EC reads can land in the 200-300 ms range
// when the chip is contended).
const DefaultPerSyscallDeadline = 500 * time.Millisecond

// DefaultRegisterDeadline bounds the wait Register imposes on
// Read/Stat probes against a freshly-discovered hwmon path. Daemon
// startup is the only caller; #1042's failure mode is a hot-plug or
// hung chip blocking startup indefinitely.
const DefaultRegisterDeadline = 750 * time.Millisecond

// writeWithDeadline writes data to path using os.WriteFile in a
// short-lived goroutine. Returns when the write completes OR when
// ctx fires, whichever comes first. The goroutine is abandoned on
// ctx fire — the underlying os.WriteFile may keep running inside
// the kernel for an indefinite period, but the daemon proceeds. See
// the package-level rationale on why this is safe.
//
// readWithDeadline / writeWithDeadline are intentionally narrow:
// they preserve the exact production semantics (mode bits, atomic-
// write contracts) of the wrapped primitive. They are NOT generic
// timeouts — callers MUST handle the ctx.Err()-wrapped error and
// log the abandonment so an operator reading the journal knows
// the write may have only partially landed.
func writeWithDeadline(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	done := make(chan error, 1)
	go func() {
		done <- os.WriteFile(path, data, perm)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("watchdog: write %s abandoned: %w", path, ctx.Err())
	}
}

// readWithDeadline reads path's contents via os.ReadFile under a
// per-syscall deadline. Same goroutine-abandonment semantics as
// writeWithDeadline. The returned []byte is non-nil only when the
// read completed within the deadline; on timeout the caller must
// fall back to a safe default.
func readWithDeadline(ctx context.Context, path string) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := os.ReadFile(path)
		done <- result{data, err}
	}()
	select {
	case r := <-done:
		return r.data, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("watchdog: read %s abandoned: %w", path, ctx.Err())
	}
}

// NVML-specific deadline lives in internal/hal/nvml/backend.go (see
// nvmlResetWithDeadline there). The hal/nvml package owns the NVML
// reset primitive; the watchdog package owns only the file-IO
// deadline helpers above + the cross-cutting Restore envelope.
