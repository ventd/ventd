package detectors

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// SystemctlExec is the shell-out surface UserspaceConflictDetector
// needs. Production wires execSystemctlIsActive (default below);
// tests inject a stub that returns canned per-unit answers.
//
// is-active returns one of: "active", "inactive", "failed",
// "activating", "deactivating", "unknown" (when the unit isn't
// installed). The detector treats "active" as the conflict signal;
// every other state is benign.
type SystemctlExec func(ctx context.Context, unit string) (state string, err error)

// execSystemctlIsActive runs `systemctl is-active <unit>` and returns
// the trimmed stdout. systemctl prints the state on stdout AND exits
// non-zero for non-active units, so we accept the exit-error path
// and use the printed state regardless.
func execSystemctlIsActive(ctx context.Context, unit string) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", fmt.Errorf("systemctl not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", unit)
	out, _ := cmd.Output() // intentionally ignore exit code; state is on stdout
	return strings.TrimSpace(string(out)), nil
}

// ConflictingUnits is the canonical set of userspace daemons whose
// PWM/fan-control activity races ventd. Adding here is non-breaking
// — the detector iterates and the test fixture covers each entry.
//
// Per spec-03 amendment §3.5 ("conflicts_with_userspace") the resolver
// already encodes per-driver conflicts in the catalog. This list is
// the COARSER outer layer that fires on systems where the catalog
// match is unsupported / tier-3-fallback and we don't have a
// specific conflict declaration to consult.
var ConflictingUnits = []string{
	"fancontrol.service",     // lm_sensors PWM daemon
	"thinkfan.service",       // ThinkPad-specific fan curve daemon
	"nbfc_service.service",   // NoteBook FanControl userspace EC daemon
	"coolercontrold.service", // CoolerControl, common on AMD systems
	"liquidctl.service",      // Liquidctl daemonised mode
}

// UserspaceConflictDetector emits a Blocker Fact for every conflicting
// unit currently active. ventd refuses to drive PWM channels that
// another daemon is also writing — concurrent writes corrupt the
// chip's manual-mode state and produce audible fan-thrashing.
//
// On non-systemd hosts (Alpine OpenRC, Void runit) systemctl is
// absent and the detector emits zero facts — RULE-DOCTOR-04
// graceful-degrade. Future work: an OpenRC counterpart that walks
// /run/openrc/started/ for the same unit names (filed as v0.5.10.x
// follow-up).
type UserspaceConflictDetector struct {
	// Exec is the shell-out surface; production uses execSystemctlIsActive.
	Exec SystemctlExec

	// Units overrides ConflictingUnits for tests. Empty means use the
	// package-level default.
	Units []string
}

// NewUserspaceConflictDetector constructs a detector with the
// production shell-out and default unit list. Tests pass an explicit
// SystemctlExec stub.
func NewUserspaceConflictDetector(exec SystemctlExec) *UserspaceConflictDetector {
	if exec == nil {
		exec = execSystemctlIsActive
	}
	return &UserspaceConflictDetector{Exec: exec}
}

// Name returns the stable detector ID.
func (d *UserspaceConflictDetector) Name() string { return "userspace_conflict" }

// Probe queries every unit in ConflictingUnits via systemctl. Emits
// one Blocker Fact per active conflict.
func (d *UserspaceConflictDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	units := d.Units
	if len(units) == 0 {
		units = ConflictingUnits
	}

	now := timeNowFromDeps(deps)
	var facts []doctor.Fact

	for _, unit := range units {
		state, err := d.Exec(ctx, unit)
		if err != nil {
			// systemctl absent → non-systemd host; surface no facts
			// (graceful degrade per RULE-DOCTOR-04). Per-unit error
			// from a present systemctl is also treated as "couldn't
			// determine" — we'd rather miss a conflict than emit a
			// noisy false positive.
			return nil, nil
		}
		if state != "active" {
			continue
		}
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityBlocker,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("%s is running and conflicts with ventd's PWM control", unit),
			Detail: fmt.Sprintf(
				"`systemctl is-active %s` returned 'active'. Concurrent userspace writes to the same hwmon PWM files corrupt the chip's manual-mode state. Stop the conflicting daemon (`sudo systemctl stop %s` + `sudo systemctl disable %s`) before continuing, or use the `conflicts_with_userspace` catalog declaration to formally hand off.",
				unit, unit, unit,
			),
			EntityHash: doctor.HashEntity("userspace_conflict", unit),
			Observed:   now,
		})
	}
	return facts, nil
}
