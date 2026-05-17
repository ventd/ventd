package conflicts

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// systemctlTimeout caps each systemctl invocation. systemctl is local IPC
// and normally sub-second; the timeout exists so a wedged systemd doesn't
// hang the wizard. Generous because cold-boot systemd can be slower.
const systemctlTimeout = 5 * time.Second

// SystemctlRunner is the seam tests inject so we don't shell out to a
// real systemctl. Production passes RealSystemctl; tests pass a stub.
type SystemctlRunner interface {
	// IsActive returns true when the unit's ActiveState is "active" or
	// "activating". Returns false (with no error) on "inactive",
	// "failed", "deactivating", etc. — these are not "the daemon is
	// taking control of fans right now."
	IsActive(ctx context.Context, unit string) (bool, error)

	// IsEnabled returns true when the unit will start on next boot
	// (enabled, enabled-runtime, alias, indirect, …). False for
	// disabled, static, masked, transient. This is the signal the
	// wizard uses to warn "fancontrol is configured to run on boot
	// but not running now — did you mean to enable it?"
	IsEnabled(ctx context.Context, unit string) (bool, error)
}

// RealSystemctl shells out to /usr/bin/systemctl. Used in production.
type RealSystemctl struct{}

// IsActive shells out `systemctl is-active --quiet <unit>`. systemctl
// returns 0 for active/activating, non-zero otherwise. We treat the
// non-zero case as "not active" rather than an error, matching the
// existing recovery.DetectVendorDaemon semantics.
func (RealSystemctl) IsActive(ctx context.Context, unit string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, systemctlTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", unit).Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	// Non-exec error (binary missing, ctx deadline, etc.) — surface so
	// the caller can decide whether to skip systemd detection entirely
	// (e.g. running inside a non-systemd container).
	return false, err
}

// IsEnabled shells out `systemctl is-enabled <unit>`. The exit code is
// less reliable than is-active (some valid "enabled-runtime" outputs
// land with non-zero), so we inspect stdout. Output is one of:
// enabled, enabled-runtime, linked, linked-runtime, alias, masked,
// masked-runtime, static, disabled, indirect, generated, transient.
// We treat enabled, enabled-runtime, linked, linked-runtime, alias,
// indirect, and generated as "will start on boot."
func (RealSystemctl) IsEnabled(ctx context.Context, unit string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, systemctlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-enabled", unit).Output()
	state := strings.TrimSpace(string(out))
	switch state {
	case "enabled", "enabled-runtime", "linked", "linked-runtime",
		"alias", "indirect", "generated":
		return true, nil
	case "":
		// No output at all — systemctl couldn't find the unit or
		// systemd isn't running. Treat as "not enabled" rather than
		// erroring; production hosts without systemd should produce
		// an empty conflict report, not crash the wizard.
		if err != nil {
			return false, nil
		}
		return false, nil
	default:
		return false, nil
	}
}

// detectSystemd runs the per-entry unit checks for every registry
// entry. Per-entry skips when Units is empty. Errors from the runner
// (systemctl missing, timeout) collapse to "no detection happened" —
// the caller still receives partial results from the other detectors.
func detectSystemd(ctx context.Context, runner SystemctlRunner, entries []Entry) map[string]*Conflict {
	out := make(map[string]*Conflict, len(entries))
	if runner == nil {
		return out
	}
	for _, e := range entries {
		if len(e.Units) == 0 {
			continue
		}
		var activeFound, enabledFound []string
		for _, unit := range e.Units {
			if active, err := runner.IsActive(ctx, unit); err == nil && active {
				activeFound = append(activeFound, unit)
			}
			if enabled, err := runner.IsEnabled(ctx, unit); err == nil && enabled {
				enabledFound = append(enabledFound, unit)
			}
		}
		if len(activeFound) == 0 && len(enabledFound) == 0 {
			continue
		}
		c := out[e.Name]
		if c == nil {
			c = &Conflict{Entry: e}
			out[e.Name] = c
		}
		c.UnitsActive = activeFound
		c.UnitsEnabled = enabledFound
	}
	return out
}
