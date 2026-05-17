package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/recovery"
)

// Installer is the seam between DriverInstallPhase and the kernel-
// module-building production pipeline (hwmon.InstallDriver). Tests
// inject stubs so unit tests don't compile kernel modules.
type Installer interface {
	// Install installs the OOT driver identified by chipKey
	// (e.g. "it8688e", "nct6687d"). logFn is called with each
	// human-readable progress line so the caller can stream into
	// the wizard's activity feed.
	//
	// Return semantics (mirrors hwmon.InstallDriver):
	//   - nil: success, PWM channels visible after modprobe
	//   - *hwmon.ErrRebootRequired: ACPI conflict fix applied,
	//     reboot needed
	//   - any other error: classified by the caller and retried
	//     against the next candidate
	Install(chipKey string, logFn func(string)) error
}

// realInstaller wraps hwmon.InstallDriver for production. Implements
// Installer. hwmon.InstallDriver dereferences the logger to write
// every progress line, so passing nil there crashes the install at
// the first "Checking build tools" log call. We always pass
// slog.Default() — the orchestrator's per-phase logger is on
// RunContext but the Installer interface stays narrow.
type realInstaller struct{}

func (realInstaller) Install(chipKey string, logFn func(string)) error {
	return hwmon.InstallDriver(chipKey, logFn, slog.Default())
}

// DriverInstallArtifact records the per-candidate attempt outcome plus
// the overall result. The wizard UI surfaces this so the operator sees
// what was tried and why each failed before recommending the
// remediation card.
type DriverInstallArtifact struct {
	// InstalledKey is the chip key of the candidate that succeeded.
	// Empty when no candidate succeeded.
	InstalledKey string `json:"installed_key,omitempty"`

	// RebootRequired is true when one of the install attempts
	// applied an ACPI cmdline fix that requires a reboot to take
	// effect. The wizard then surfaces a reboot card; on next boot
	// the orchestrator re-runs the phase and the install completes.
	RebootRequired bool   `json:"reboot_required,omitempty"`
	RebootMessage  string `json:"reboot_message,omitempty"`

	// Attempts is a per-candidate audit log: which key was tried,
	// whether it succeeded, and the error string if it failed.
	Attempts []DriverInstallAttempt `json:"attempts,omitempty"`
}

// DriverInstallAttempt is one element of DriverInstallArtifact.Attempts.
type DriverInstallAttempt struct {
	Key       string   `json:"key"`
	ChipName  string   `json:"chip_name"`
	Succeeded bool     `json:"succeeded"`
	Error     string   `json:"error,omitempty"`
	LogLines  []string `json:"log_lines,omitempty"`
}

// DriverInstallPhase walks the DriverPlan artifact's Needs list and
// runs the production install pipeline (hwmon.InstallDriver) against
// each candidate in order. Stops on the first success.
//
// Reuses the existing multi-candidate retry shape from
// Manager.run's Phase 2 (the loop introduced for issue #1025 and
// extended for #1116 / #1154 / #1159). The retry covers the
// "wrong-driver-installed-and-unloaded" path: if a candidate compiles
// + loads but stepVerify reports no PWM channels appeared, it's a
// chip-mismatch — unload and try the next candidate.
type DriverInstallPhase struct {
	// Installer is the seam used to install drivers. nil → use
	// the production realInstaller (which calls hwmon.InstallDriver).
	// Tests inject a stub.
	Installer Installer
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (DriverInstallPhase) Name() string { return "driver_install" }

// Execute consumes the DriverPlan artifact from the prior phase's
// checkpoint and runs through Needs in order.
func (p DriverInstallPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	installer := p.Installer
	if installer == nil {
		installer = realInstaller{}
	}

	plan, err := loadDriverPlanArtifact(rc)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "load driver plan: " + err.Error(),
		}
	}

	switch plan.Status {
	case DriverPlanReady:
		rc.Sink().Emit("info", "driver_install", "skipping — PWMs already controllable")
		art := DriverInstallArtifact{}
		raw, _ := EncodeArtifact(art)
		return Outcome{Status: StatusSuccess, Artifact: raw}

	case DriverPlanNoMatch:
		rc.Sink().Emit("warn", "driver_install",
			"no candidate driver matches this hardware — recommend monitor-only mode")
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassDriverWontBind,
			Detail: "No fan controller chip on this board is supported by a known driver. Switch ventd to monitor-only mode, or open an issue with your DMI info so we can add it to the catalogue.",
		}

	case DriverPlanNeedsInstall:
		// fall through to install loop below
	}

	art := DriverInstallArtifact{}
	var rebootErr *hwmon.ErrRebootRequired

	for _, need := range plan.Needs {
		rc.Sink().Emit("info", "driver_install",
			fmt.Sprintf("trying %s (%s)", need.ChipName, need.Key))

		var logLines []string
		logFn := func(line string) {
			logLines = append(logLines, line)
			rc.Sink().Emit("info", "driver_install", line)
		}

		err := installer.Install(need.Key, logFn)

		attempt := DriverInstallAttempt{
			Key:      need.Key,
			ChipName: need.ChipName,
			LogLines: logLines,
		}

		if err == nil {
			attempt.Succeeded = true
			art.Attempts = append(art.Attempts, attempt)
			art.InstalledKey = need.Key
			rc.Log().Info("driver install succeeded", "key", need.Key, "chip", need.ChipName)
			raw, _ := EncodeArtifact(art)
			return Outcome{Status: StatusSuccess, Artifact: raw}
		}

		attempt.Error = err.Error()
		art.Attempts = append(art.Attempts, attempt)

		// ErrRebootRequired is terminal: the ACPI cmdline patch is
		// already on disk; further candidates won't help until the
		// host reboots. Surface the reboot card and stop.
		if errors.As(err, &rebootErr) {
			art.RebootRequired = true
			art.RebootMessage = rebootErr.Message
			raw, _ := EncodeArtifact(art)
			return Outcome{
				Status:   StatusFailed,
				Class:    recovery.ClassACPIResourceConflict,
				Detail:   rebootErr.Message,
				Artifact: raw,
			}
		}

		rc.Log().Warn("driver install candidate failed",
			"key", need.Key, "err", err)
	}

	// All candidates exhausted without success. The last attempt's
	// error class is the most likely fix path; the recovery
	// classifier maps the error text.
	lastErr := ""
	if n := len(art.Attempts); n > 0 {
		lastErr = art.Attempts[n-1].Error
	}
	cls := recovery.Classify("driver_install", errors.New(lastErr), nil)
	if cls == recovery.ClassUnknown {
		cls = recovery.ClassDriverWontBind
	}
	raw, _ := EncodeArtifact(art)
	return Outcome{
		Status: StatusFailed,
		Class:  cls,
		Detail: fmt.Sprintf(
			"All %d driver candidate(s) failed to install. Last error: %s",
			len(art.Attempts), lastErr),
		Artifact: raw,
	}
}

// loadDriverPlanArtifact reads the prior DriverPlan phase's checkpoint
// and returns its decoded artifact. Returns an error if the prior
// phase didn't run, didn't succeed, or its artifact is malformed —
// in those cases DriverInstall can't reasonably proceed.
func loadDriverPlanArtifact(rc *RunContext) (DriverPlanArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return DriverPlanArtifact{}, err
	}
	prior, ok := state.Outcomes[(DriverPlanPhase{}).Name()]
	if !ok {
		return DriverPlanArtifact{}, errors.New("DriverPlan phase has not run; cannot install")
	}
	if prior.Status != StatusSuccess {
		return DriverPlanArtifact{}, fmt.Errorf(
			"DriverPlan phase did not succeed (status=%q); cannot install", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return DriverPlanArtifact{}, errors.New("DriverPlan phase produced no artifact")
	}
	var art DriverPlanArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return DriverPlanArtifact{}, fmt.Errorf("decode DriverPlan artifact: %w", err)
	}
	return art, nil
}
