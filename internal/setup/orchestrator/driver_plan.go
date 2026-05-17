package orchestrator

import (
	"context"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/recovery"
)

// DriverPlanStatus enumerates the three plan outcomes the wizard's UI
// needs to render. The status drives the next phase's decision tree:
// nothing-to-do skips DriverInstall entirely; needs-install kicks the
// install pipeline; no-match surfaces the dead-end recovery card.
type DriverPlanStatus string

const (
	// DriverPlanReady — at least one controllable PWM channel is
	// already visible on the host. No driver work is required for
	// the wizard to proceed.
	DriverPlanReady DriverPlanStatus = "ready"

	// DriverPlanNeedsInstall — zero controllable PWMs visible but
	// the hwmon diagnose helper proposed one or more out-of-tree
	// drivers based on chip name + DMI matching. Those candidates
	// are listed in DriverPlanArtifact.Needs, in priority order.
	// The DriverInstall phase walks the list and tries each until
	// one succeeds or all fail.
	DriverPlanNeedsInstall DriverPlanStatus = "needs_install"

	// DriverPlanNoMatch — zero controllable PWMs visible AND no
	// matching driver candidates. This is the dead-end case where
	// the operator needs to either supply hardware info ventd
	// doesn't recognise yet, or fall back to monitor-only mode.
	DriverPlanNoMatch DriverPlanStatus = "no_match"
)

// DriverPlanArtifact is the structured result of the DriverPlan phase.
// Consumed by the DriverInstall phase (next) and the wizard UI (which
// renders the candidate list in the recovery card when install fails).
type DriverPlanArtifact struct {
	Status DriverPlanStatus `json:"status"`

	// PWMCount is the count of controllable PWM channels visible at
	// the time the plan was made. Sanity-check field for the UI; the
	// next phase doesn't use it because it re-checks via stepVerify.
	PWMCount int `json:"pwm_count"`

	// HwmonDevices is the chip-name list captured at plan time.
	// Mirrors InventoryArtifact.HwmonDevices so the DriverPlan
	// artifact is self-contained for inspection without joining
	// across checkpoints.
	HwmonDevices []string `json:"hwmon_devices"`

	// Needs is the ordered candidate list. Each entry carries
	// Key (e.g. "it8688e", "nct6687d"), ChipName, Explanation,
	// RepoURL, Module, MaxSupportedKernel, and HALBackend hints.
	// DriverInstall walks the list and tries each until one
	// succeeds or all return ErrNoPWMChannelsAppeared.
	Needs []hwmon.DriverNeed `json:"needs,omitempty"`
}

// DriverPlanPhase produces a structured driver-install plan from the
// live host. Side-effect-free: only reads /sys/class/hwmon and DMI,
// never modifies the system. The downstream DriverInstall phase is
// the one that actually compiles + loads modules.
//
// Reuses hwmon.Diagnose so the chip-detection + DMI-matching logic
// stays single-sourced with the legacy Manager.run path. When the
// wizard rework completes (v0.8.1), DriverInstall replaces Phase 2 of
// Manager.run entirely; Diagnose remains the canonical detection
// entrypoint for both paths until then.
type DriverPlanPhase struct{}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (DriverPlanPhase) Name() string { return "driver_plan" }

// Execute reads the live host, classifies the situation, and produces
// a structured plan. Never fails on "no driver matches" — that's a
// legitimate plan outcome (DriverPlanNoMatch), not a phase failure.
// Returns StatusFailed only on outright I/O failures during the scan.
func (DriverPlanPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "driver_plan", "classifying drivers needed vs present")

	diag := hwmon.Diagnose()
	art := DriverPlanArtifact{
		PWMCount:     diag.PWMCount,
		HwmonDevices: diag.HwmonDevices,
		Needs:        diag.DriverNeeds,
	}

	switch {
	case diag.PWMCount > 0:
		art.Status = DriverPlanReady
		rc.Log().Info("driver plan: ready",
			"pwm_count", diag.PWMCount,
			"hwmon_devices", len(diag.HwmonDevices))
	case len(diag.DriverNeeds) > 0:
		art.Status = DriverPlanNeedsInstall
		keys := make([]string, 0, len(diag.DriverNeeds))
		for _, n := range diag.DriverNeeds {
			keys = append(keys, n.Key)
		}
		rc.Log().Info("driver plan: install candidates identified",
			"candidates", keys,
			"hwmon_devices", len(diag.HwmonDevices))
	default:
		art.Status = DriverPlanNoMatch
		rc.Log().Warn("driver plan: no controllable PWMs and no matching candidates",
			"board_vendor", diag.BoardVendor,
			"board_name", diag.BoardName,
			"hwmon_devices", diag.HwmonDevices)
	}

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}
	return Outcome{Status: StatusSuccess, Artifact: raw}
}
