package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// KernelReleaseFn returns the running kernel release string (e.g.
// "6.8.0-49-generic"). Production wires the live read of
// /proc/sys/kernel/osrelease; tests inject a stub returning a
// canned release value.
type KernelReleaseFn func() string

// liveKernelRelease reads /proc/sys/kernel/osrelease (always present
// on Linux; uname(2) syscall avoidance per the existing
// hwmon.kernelRelease pattern). Returns "" on read error.
func liveKernelRelease() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// KernelUpdateDetector compares the running kernel release against
// a baseline persisted by the wiring layer at last successful
// daemon-state-load. A mismatch means the host rebooted into a
// different kernel since ventd last ran cleanly — DKMS should have
// auto-rebuilt the OOT module, but the rebuild may have failed
// (caught separately by `dkms_status`). This detector surfaces the
// transition itself so the operator knows recent control-loop
// behaviour is on a freshly-loaded module.
//
// The detector is pure read per RULE-DOCTOR-01. Persisting the new
// LastKernel value is the wiring layer's job — typically right
// after a clean Runner.RunOnce() with no Blocker facts.
//
// Severity: Warning. The kernel update itself isn't a fan-control
// failure; it's a state transition the operator should know about
// so they can correlate any new behaviour with the kernel change.
// dkms_status will fire Blocker if the OOT module didn't rebuild.
type KernelUpdateDetector struct {
	// LastKernel is the persisted "last seen" kernel release. Empty
	// = first daemon run, no comparison; the detector is a no-op.
	LastKernel string

	// Release returns the current running kernel release. Defaults
	// to liveKernelRelease when nil.
	Release KernelReleaseFn
}

// NewKernelUpdateDetector constructs a detector. release nil → live
// /proc read.
func NewKernelUpdateDetector(lastKernel string, release KernelReleaseFn) *KernelUpdateDetector {
	if release == nil {
		release = liveKernelRelease
	}
	return &KernelUpdateDetector{
		LastKernel: lastKernel,
		Release:    release,
	}
}

// Name returns the stable detector ID.
func (d *KernelUpdateDetector) Name() string { return "kernel_update" }

// Probe reads the current kernel and compares to the baseline.
func (d *KernelUpdateDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.LastKernel == "" {
		// First daemon run on this host; nothing to compare.
		return nil, nil
	}
	current := d.Release()
	if current == "" {
		// /proc unavailable (sandboxed). Graceful degrade.
		return nil, nil
	}
	if current == d.LastKernel {
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityWarning,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("Kernel updated since last clean ventd run: %s → %s", d.LastKernel, current),
		Detail: fmt.Sprintf(
			"The running kernel changed since the daemon last persisted state. DKMS should have auto-rebuilt the OOT module against %s — the dkms_status detector reports Blocker if that rebuild failed. Watch the dashboard's smart-mode pill for cold-start re-warmup; calibration carries forward but Layer-A confidence resets to the cold-start hard pin (5 minutes per RULE-AGG-COLDSTART-01).",
			current,
		),
		EntityHash: doctor.HashEntity("kernel_update", d.LastKernel, current),
		Observed:   now,
	}}, nil
}
