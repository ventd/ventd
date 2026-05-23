package detectors

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// CalibrationArtifactLoader is the read surface
// CalibrationCurveQualityDetector needs. Production wires the wizard's
// /var/lib/ventd/setup/ checkpoint store via a thin reader that
// extracts the calibrate outcome's artifact bytes; tests pass a stub
// returning canned JSON.
type CalibrationArtifactLoader interface {
	// ReadCalibrateArtifact returns the raw JSON bytes of the
	// orchestrator.CalibrateArtifact, or (nil, nil) when the
	// artifact is absent (wizard not yet run, or running on a
	// monitor-only fallback). Surface I/O errors via the second
	// return so the detector can emit a "couldn't verify" warning.
	ReadCalibrateArtifact() ([]byte, error)
}

// FileCalibrationArtifactLoader is the production loader. It reads the
// orchestrator's State file (one JSON document with an outcomes map
// keyed by phase name) and extracts the CalibratePhase outcome's
// embedded Artifact bytes. The path defaults to
// /var/lib/ventd/setup/state.json (the CheckpointStore's on-disk
// layout) — the daemon's main.go can override via the Path field for
// tests and for cross-StateDir installs.
type FileCalibrationArtifactLoader struct {
	Path string
}

// ReadCalibrateArtifact reads the orchestrator State file and decodes
// the CalibratePhase outcome's Artifact field. A missing file means
// the wizard has not yet run; the detector treats this as "no signal"
// and emits zero facts.
func (f FileCalibrationArtifactLoader) ReadCalibrateArtifact() ([]byte, error) {
	path := f.Path
	if path == "" {
		path = "/var/lib/ventd/setup/state.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read checkpoints: %w", err)
	}
	var envelope struct {
		Outcomes map[string]struct {
			Status   string          `json:"status"`
			Artifact json.RawMessage `json:"artifact"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode checkpoints envelope: %w", err)
	}
	cal, ok := envelope.Outcomes["calibrate"]
	if !ok || len(cal.Artifact) == 0 {
		return nil, nil
	}
	return cal.Artifact, nil
}

// calibrateArtifactShape mirrors the subset of
// orchestrator.CalibrateArtifact + CalibrateFanResult the detector
// needs. We decode locally rather than importing internal/setup to
// avoid a dependency cycle (orchestrator depends on doctor for
// recovery classes, doctor → orchestrator would close the loop).
type calibrateArtifactShape struct {
	Results []calibrateFanResultShape `json:"results"`
}

type calibrateFanResultShape struct {
	PWMPath           string `json:"pwm_path"`
	NonMonotonicCurve bool   `json:"non_monotonic_curve,omitempty"`
	MaxDropRPM        int    `json:"max_drop_rpm,omitempty"`
	MaxRPM            int    `json:"max_rpm,omitempty"`
}

// CalibrationCurveQualityDetector surfaces the calibrate sweep's
// per-fan NonMonotonicCurve quality signal. The flag is set by the
// orchestrator's CalibratePhase when a fan's rising-portion PWM→RPM
// curve drops by more than 15% of MaxRPM between consecutive samples.
// Candidate causes vary by hardware: vendor-EC firmware clamping on
// some laptop OEMs, motherboard super-IO chip tach quantisation, fan
// stall-and-restart bands, or a fan whose tach signal is noisy in
// part of the duty-cycle range. The detector does not name a single
// vendor — the wording previously hardcoded "Dell SMM / ASUS Q-Fan /
// HP Omen" which mis-described the cause on every host using a
// super-IO chip (NCT668x, IT87xx, F71xxx) or a desktop board.
//
// One Fact per affected fan, severity Warning. The remediation
// ventd has already taken is to cap the per-fan max_pwm_pct at the
// saturation knee; the operator can dig in further if they want.
type CalibrationCurveQualityDetector struct {
	Loader CalibrationArtifactLoader
}

// NewCalibrationCurveQualityDetector constructs a detector. A nil
// loader is treated as a no-op (the detector emits zero facts).
func NewCalibrationCurveQualityDetector(loader CalibrationArtifactLoader) *CalibrationCurveQualityDetector {
	return &CalibrationCurveQualityDetector{Loader: loader}
}

// Name returns the stable detector ID.
func (d *CalibrationCurveQualityDetector) Name() string { return "calibration_curve_quality" }

// Probe reads the calibrate artifact, scans for non-monotonic curve
// flags, and emits one Warning per affected fan.
func (d *CalibrationCurveQualityDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Loader == nil {
		return nil, nil
	}
	raw, err := d.Loader.ReadCalibrateArtifact()
	if err != nil {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "Cannot verify calibration curve quality",
			Detail:     fmt.Sprintf("Reading the calibrate artifact failed: %v. Operator's view of vendor-EC clamping is suppressed until this is resolved.", err),
			EntityHash: doctor.HashEntity("calibration_curve_quality_unreadable"),
			Observed:   timeNowFromDeps(deps),
		}}, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var art calibrateArtifactShape
	if err := json.Unmarshal(raw, &art); err != nil {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "Cannot decode calibration artifact",
			Detail:     fmt.Sprintf("Calibrate artifact JSON did not decode: %v. Re-run the wizard's calibration phase to refresh it.", err),
			EntityHash: doctor.HashEntity("calibration_curve_quality_decode"),
			Observed:   timeNowFromDeps(deps),
		}}, nil
	}

	// Filter to fans with the non-monotonic flag set. Sorted by
	// path so multi-fan output is stable across runs (doctor
	// downstream consumers diff Facts by EntityHash but a stable
	// order keeps the operator's eye from chasing).
	flagged := make([]calibrateFanResultShape, 0, len(art.Results))
	for _, r := range art.Results {
		if r.NonMonotonicCurve {
			flagged = append(flagged, r)
		}
	}
	if len(flagged) == 0 {
		return nil, nil
	}
	sort.Slice(flagged, func(i, j int) bool { return flagged[i].PWMPath < flagged[j].PWMPath })

	out := make([]doctor.Fact, 0, len(flagged))
	now := timeNowFromDeps(deps)
	for _, r := range flagged {
		dropPct := 0
		if r.MaxRPM > 0 {
			dropPct = r.MaxDropRPM * 100 / r.MaxRPM
		}
		out = append(out, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityWarning,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("Fan %s shows %d%% RPM drop in rising calibration curve", r.PWMPath, dropPct),
			Detail: fmt.Sprintf(
				"Calibrate sweep recorded MaxRPM=%d and a single-step drop of %d RPM (%d%%) in the rising portion. Duty cycles ventd writes past that point no longer translate into airflow. Common causes (vary by hardware): firmware reasserting its own fan curve above a PWM threshold on some laptops, motherboard super-IO tach quantisation, a fan that stalls and restarts in part of the range, or a noisy tach signal. ventd has already capped this fan's max_pwm_pct at the saturation knee, so the runtime is no longer driving above the inflection. If you override the curve in the web UI, keep max_pwm_pct at or below the wizard-emitted value.",
				r.MaxRPM, r.MaxDropRPM, dropPct),
			EntityHash: doctor.HashEntity("calibration_non_monotonic", r.PWMPath),
			Observed:   now,
		})
	}
	return out, nil
}
