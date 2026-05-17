package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/setup/conflicts"
)

// ConflictHuntArtifact is the structured result of the ConflictHunt phase.
// One entry per detected competitor. Empty Conflicts is the happy path —
// no competing daemons in sight.
type ConflictHuntArtifact struct {
	Conflicts   []conflicts.Conflict `json:"conflicts"`
	AutoStopped []string             `json:"auto_stopped,omitempty"` // entry names ventd successfully stopped+disabled
	AutoStopErr []string             `json:"auto_stop_errors,omitempty"`
}

// ConflictHuntPhase runs the multi-modal competing-daemon detector and,
// when the headless auto-stop flag is set, attempts to stop+disable
// non-vendor conflicts via systemctl. Vendor daemons (asusd,
// system76-power, fw-fanctrl, …) always require explicit operator
// consent because stopping them disables adjacent functionality.
//
// Phase outcome rules:
//
//   - No conflicts detected → StatusSuccess
//   - Conflicts found, all auto-stopped successfully → StatusSuccess
//     (the artifact lists what was stopped so the wizard can summarise)
//   - Conflicts found, auto-stop not engaged OR some failed →
//     StatusFailed with Class ClassVendorDaemonActive (reuses the
//     existing recovery card; the artifact carries the full conflict
//     list so the UI can render specific buttons per entry)
type ConflictHuntPhase struct {
	// Runner overrides the systemctl shim. nil uses RealSystemctl.
	Runner conflicts.SystemctlRunner

	// AutoStop is the headless gate: when true and the
	// VENTD_AUTO_STOP_CONFLICTS env var is set to "yes", non-vendor
	// conflicts are stopped + disabled automatically. Wizard-driven
	// (web UI) callers leave this false so the operator decides.
	AutoStop bool

	// AutoStopVendor mirrors AutoStop but for vendor daemons. Off by
	// default — production callers only flip this when the operator
	// explicitly sets VENTD_AUTO_STOP_VENDOR_CONFLICTS=yes.
	AutoStopVendor bool
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (ConflictHuntPhase) Name() string { return "conflict_hunt" }

// Execute runs the detector and (optionally) the auto-stop chain.
func (p ConflictHuntPhase) Execute(ctx context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "conflict_hunt", "scanning for competing fan-control daemons")

	runner := p.Runner
	if runner == nil {
		runner = conflicts.RealSystemctl{}
	}

	hits := conflicts.Detect(ctx, conflicts.DetectOptions{
		Systemctl: runner,
		ProcRoot:  defaultStr(rc.ProcRoot, "/proc"),
		HwmonRoot: defaultStr(rc.HwmonRoot, "/sys/class/hwmon"),
	})

	art := ConflictHuntArtifact{Conflicts: hits}

	if len(hits) == 0 {
		rc.Sink().Emit("info", "conflict_hunt", "no competing daemons detected")
		raw, err := EncodeArtifact(art)
		if err != nil {
			return Outcome{Status: StatusFailed, Class: recovery.ClassUnknown, Detail: "encode: " + err.Error()}
		}
		return Outcome{Status: StatusSuccess, Artifact: raw}
	}

	// Auto-stop only when explicitly opted in via env var. The phase
	// flag (p.AutoStop) gates *whether the env var is consulted at
	// all* — the web UI path leaves it false to preserve consent flow.
	autoStopEnv := p.AutoStop && os.Getenv("VENTD_AUTO_STOP_CONFLICTS") == "yes"
	autoStopVendorEnv := p.AutoStopVendor && os.Getenv("VENTD_AUTO_STOP_VENDOR_CONFLICTS") == "yes"

	if autoStopEnv {
		for _, c := range hits {
			if c.Entry.Vendor && !autoStopVendorEnv {
				continue
			}
			if len(c.UnitsActive) == 0 && len(c.UnitsEnabled) == 0 {
				// Nothing to stop via systemctl. Auto-stop only
				// handles unit-driven daemons; ad-hoc scripts
				// still need operator action.
				continue
			}
			if err := autoStopConflict(ctx, c, rc); err != nil {
				rc.Log().Warn("conflict_hunt: auto-stop failed",
					"entry", c.Entry.Name, "err", err)
				art.AutoStopErr = append(art.AutoStopErr,
					fmt.Sprintf("%s: %v", c.Entry.Name, err))
				continue
			}
			art.AutoStopped = append(art.AutoStopped, c.Entry.Name)
		}
	}

	// Re-scan after auto-stop so the artifact reflects post-stop
	// state. The detector is fast (sub-second on a typical host) so
	// the double scan is worth the accuracy.
	if len(art.AutoStopped) > 0 {
		hits = conflicts.Detect(ctx, conflicts.DetectOptions{
			Systemctl: runner,
			ProcRoot:  defaultStr(rc.ProcRoot, "/proc"),
			HwmonRoot: defaultStr(rc.HwmonRoot, "/sys/class/hwmon"),
		})
		art.Conflicts = hits
	}

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{Status: StatusFailed, Class: recovery.ClassUnknown, Detail: "encode: " + err.Error()}
	}

	if len(hits) == 0 {
		// Auto-stop cleared everything.
		rc.Sink().Emit("info", "conflict_hunt",
			fmt.Sprintf("auto-stopped %d competing daemon(s); proceeding", len(art.AutoStopped)))
		return Outcome{Status: StatusSuccess, Artifact: raw}
	}

	// Still conflicts remaining — wizard halts here with the existing
	// vendor-daemon recovery card so the operator decides per-entry.
	names := make([]string, 0, len(hits))
	for _, c := range hits {
		names = append(names, c.Entry.Name)
	}
	detail := fmt.Sprintf(
		"Detected %d competing fan-control daemon(s): %s. "+
			"Stop them (or switch ventd to monitor-only) before ventd can take exclusive PWM control.",
		len(hits), strings.Join(names, ", "))
	rc.Sink().Emit("warn", "conflict_hunt", detail)
	return Outcome{
		Status:   StatusFailed,
		Class:    recovery.ClassVendorDaemonActive,
		Detail:   detail,
		Artifact: raw,
	}
}

// autoStopConflict runs `systemctl disable --now <unit>` for each unit
// declared by the registry entry. Disable is preferred over plain stop
// because disable ALSO prevents re-start on next boot — without it the
// conflict re-appears after a reboot and the wizard loops.
//
// Best-effort: returns the first error encountered. Subsequent units
// in the same entry are NOT attempted because the operator's response
// to one failure is usually "investigate before trying more."
func autoStopConflict(ctx context.Context, c conflicts.Conflict, rc *RunContext) error {
	for _, unit := range c.UnitsActive {
		rc.Sink().Emit("info", "conflict_hunt", "stopping "+unit)
		if err := exec.CommandContext(ctx, "systemctl", "disable", "--now", unit).Run(); err != nil {
			return fmt.Errorf("disable --now %s: %w", unit, err)
		}
	}
	for _, unit := range c.UnitsEnabled {
		// Already iterated by UnitsActive when both. The loop body
		// is a no-op for disable on an already-disabled unit.
		rc.Sink().Emit("info", "conflict_hunt", "disabling "+unit)
		if err := exec.CommandContext(ctx, "systemctl", "disable", unit).Run(); err != nil {
			return fmt.Errorf("disable %s: %w", unit, err)
		}
	}
	return nil
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
