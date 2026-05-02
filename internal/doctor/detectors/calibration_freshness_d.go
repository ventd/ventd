package detectors

import (
	"context"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/recovery"
)

// CalibrationLoader is the read-only surface
// CalibrationFreshnessDetector needs. Production wires the daemon's
// calibration.Store; tests pass a stub returning a synthetic run.
type CalibrationLoader interface {
	// Load returns the persisted CalibrationRun for the given
	// (fingerprint, biosVersion) pair, or nil + ok=false if absent.
	Load(fingerprint, biosVersion string) (run *hwdb.CalibrationRun, ok bool, err error)
}

// CalibrationFreshnessDetector verifies a calibration record exists
// for the running (DMI fingerprint, BIOS version) pair AND is fresh.
// Three failure modes:
//
//  1. **Absent** — no record for the current fingerprint/BIOS;
//     fan curves run on defaults. Warning.
//  2. **BIOS-stale** — record exists but for a different BIOS version
//     than the live one. RULE-HWDB-PR2-09 requires recalibration on
//     BIOS change because polarity/stall/min-responsive-PWM can flip.
//     Blocker.
//  3. **Old-but-current-BIOS** — record older than 6 months on the
//     same BIOS. Hardware can drift (fan bearings wear, pump-impeller
//     accumulation in AIOs). Warning.
//
// The 6-month freshness threshold is conservative; operators on
// gaming desktops with new fans rarely see drift this fast, but
// 24/7 NAS workloads under high RPM do.
type CalibrationFreshnessDetector struct {
	// Fingerprint is the live DMI fingerprint from probe.
	Fingerprint string

	// BIOSVersion is the live /sys/class/dmi/id/bios_version.
	BIOSVersion string

	// Loader is the calibration-store reader.
	Loader CalibrationLoader

	// FreshnessWindow is the max age before the detector emits a
	// freshness warning. Defaults to 6 months when zero.
	FreshnessWindow time.Duration
}

// NewCalibrationFreshnessDetector constructs a detector. nil loader
// → detector is a no-op.
func NewCalibrationFreshnessDetector(fingerprint, biosVersion string, loader CalibrationLoader, freshnessWindow time.Duration) *CalibrationFreshnessDetector {
	if freshnessWindow <= 0 {
		freshnessWindow = 6 * 30 * 24 * time.Hour // ~6 months
	}
	return &CalibrationFreshnessDetector{
		Fingerprint:     fingerprint,
		BIOSVersion:     biosVersion,
		Loader:          loader,
		FreshnessWindow: freshnessWindow,
	}
}

// Name returns the stable detector ID.
func (d *CalibrationFreshnessDetector) Name() string { return "calibration_freshness" }

// Probe checks the calibration record state.
func (d *CalibrationFreshnessDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Loader == nil || d.Fingerprint == "" {
		return nil, nil
	}

	now := timeNowFromDeps(deps)

	run, ok, err := d.Loader.Load(d.Fingerprint, d.BIOSVersion)
	if err != nil {
		// Loader I/O error — surface as Warning so the operator
		// knows we couldn't verify.
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "Cannot verify calibration freshness",
			Detail:     fmt.Sprintf("Loading calibration store failed: %v", err),
			EntityHash: doctor.HashEntity("calibration_unreadable", d.Fingerprint),
			Observed:   now,
		}}, nil
	}

	if !ok || run == nil {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "No calibration record for this fingerprint+BIOS",
			Detail:     fmt.Sprintf("DMI fingerprint %q + BIOS %q has no calibration record. Fan control runs on defaults — re-run the wizard's calibration sweep for fingerprint-specific stall/min-responsive PWM values.", d.Fingerprint, d.BIOSVersion),
			EntityHash: doctor.HashEntity("calibration_missing", d.Fingerprint, d.BIOSVersion),
			Observed:   now,
		}}, nil
	}

	// BIOS-version mismatch — Blocker.
	if hwdb.NeedsRecalibration(run, d.BIOSVersion) {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityBlocker,
			Class:      recovery.ClassUnknown,
			Title:      "Calibration record's BIOS version does not match the running BIOS",
			Detail:     fmt.Sprintf("Stored: %q. Running: %q. RULE-HWDB-PR2-09 requires recalibration on BIOS change because polarity / stall_pwm / min_responsive_pwm can flip. Re-run the wizard's calibration sweep.", run.BIOSVersion, d.BIOSVersion),
			EntityHash: doctor.HashEntity("calibration_bios_drift", d.Fingerprint, run.BIOSVersion, d.BIOSVersion),
			Observed:   now,
		}}, nil
	}

	// Old record — Warning.
	if !run.CalibratedAt.IsZero() && now.Sub(run.CalibratedAt) > d.FreshnessWindow {
		ageDays := int(now.Sub(run.CalibratedAt).Hours() / 24)
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      fmt.Sprintf("Calibration record is %d days old (>%d-day freshness window)", ageDays, int(d.FreshnessWindow.Hours()/24)),
			Detail:     fmt.Sprintf("Calibrated %s. BIOS unchanged so the curves still apply, but hardware can drift over time (fan bearings wear, AIO pump-impeller accumulation). Consider re-running calibration if you've noticed audible behaviour changes.", run.CalibratedAt.Format(time.RFC3339)),
			EntityHash: doctor.HashEntity("calibration_stale", d.Fingerprint),
			Observed:   now,
		}}, nil
	}

	return nil, nil
}
