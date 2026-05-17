package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/recovery"
)

// RPMDetector is the seam between RPMDetectPhase and the production
// calibrate.Manager.DetectRPMSensor. Tests substitute stubs instead
// of running the multi-second per-fan heuristic.
type RPMDetector interface {
	// Detect drives the given fan's PWM and watches every fan*_input
	// across the same hwmon chip for correlated RPM movement.
	// Returns the matching fan*_input path or empty when no fan
	// responded (DC fan or operator-disconnected tach).
	Detect(fan *config.Fan) (calibrate.DetectResult, error)
}

// RPMDetectFanResult is one entry in the RPMDetectArtifact. Records
// the per-fan outcome of the RPM-detection heuristic.
type RPMDetectFanResult struct {
	PWMPath     string `json:"pwm_path"`
	ResolvedRPM string `json:"resolved_rpm,omitempty"` // sysfs path the detection picked
	Delta       int    `json:"delta,omitempty"`        // RPM change that identified it
	OriginalRPM string `json:"original_rpm,omitempty"` // what ProbePhase originally paired
	Improved    bool   `json:"improved,omitempty"`     // true when detection found a non-empty path
	Skipped     string `json:"skipped,omitempty"`      // skip reason
	Error       string `json:"error,omitempty"`
}

// RPMDetectArtifact is the structured result of the RPMDetectPhase.
// Consumed by ApplyPhase to override ProbedFan.RPMPath with the
// detected path when detection found a better match.
type RPMDetectArtifact struct {
	Results []RPMDetectFanResult `json:"results"`
}

// RPMDetectPhase runs the calibrate.Manager.DetectRPMSensor heuristic
// for every probed hwmon fan whose same-chip RPM pairing didn't
// produce a useful tach (RPMPath empty, OR fan*_input shows 0 RPM
// despite non-zero PWM). For each candidate fan, briefly ramps the
// PWM and identifies the fan*_input under the same chip with the
// largest correlated RPM delta.
//
// This is the orchestrator's port of the legacy Manager.run Phase 5
// (RPM sensor detection). Without it, fans whose pwmN and fanN_input
// don't follow the kernel's same-index convention (some AMD boards,
// split SuperIO chips) would be marked tach-less and the dashboard
// would show "—" for RPM even though the tach is wired.
//
// Skips phantom-polarity fans (no PWM surface to drive) and nvidia
// fans (no hwmon RPM sensor).
type RPMDetectPhase struct {
	// Detector is the test seam. nil → fail at runtime; production
	// must wire one via the bridge (NewRPMDetector(*calibrate.Manager)).
	Detector RPMDetector
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (RPMDetectPhase) Name() string { return "rpm_detect" }

// Execute walks ProbeArtifact, runs Detector for fans needing it,
// emits RPMDetectArtifact with the per-fan outcome.
func (p RPMDetectPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	if p.Detector == nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "RPMDetectPhase requires a Detector (production wires one via the bridge)",
		}
	}

	probeArt, err := loadProbeArtifact(rc)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "load probe artifact: " + err.Error(),
		}
	}

	polByPath := map[string]string{}
	if polArt, polErr := loadPolarityArtifact(rc); polErr == nil {
		for _, r := range polArt.Results {
			polByPath[r.PWMPath] = r.Polarity
		}
	}

	if len(probeArt.Fans) == 0 {
		rc.Sink().Emit("info", "rpm_detect", "no fans to scan; skipping")
		raw, _ := EncodeArtifact(RPMDetectArtifact{})
		return Outcome{Status: StatusSkipped, Detail: "no fans enumerated", Artifact: raw}
	}

	art := RPMDetectArtifact{Results: make([]RPMDetectFanResult, 0, len(probeArt.Fans))}

	for _, fan := range probeArt.Fans {
		entry := RPMDetectFanResult{
			PWMPath:     fan.PWMPath,
			OriginalRPM: fan.RPMPath,
		}

		// Skip phantom fans — driving them is unsafe.
		if polByPath[fan.PWMPath] == "phantom" {
			entry.Skipped = "polarity=phantom"
			art.Results = append(art.Results, entry)
			continue
		}

		// Skip when ProbePhase already paired a same-index tach —
		// detection won't improve on the kernel's convention.
		if fan.RPMPath != "" {
			entry.Skipped = "already paired by probe (same-index fan_input)"
			art.Results = append(art.Results, entry)
			continue
		}

		rc.Sink().Emit("info", "rpm_detect",
			fmt.Sprintf("scanning RPM tach for %s", fan.LabelHint))

		cfgFan := &config.Fan{
			Name:     fan.LabelHint,
			Type:     "hwmon",
			PWMPath:  fan.PWMPath,
			ChipName: fan.ChipName,
		}
		res, err := p.Detector.Detect(cfgFan)
		if err != nil {
			entry.Error = err.Error()
			rc.Log().Warn("rpm detect failed",
				"fan", fan.LabelHint, "err", err)
		} else if res.RPMPath != "" {
			entry.ResolvedRPM = res.RPMPath
			entry.Delta = res.Delta
			entry.Improved = true
			rc.Log().Info("rpm detect found tach",
				"fan", fan.LabelHint,
				"resolved", res.RPMPath,
				"delta", res.Delta)
		} else {
			entry.Skipped = "no fan_input responded to PWM ramp (DC fan or disconnected tach)"
		}
		art.Results = append(art.Results, entry)
	}

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}

	improved := 0
	for _, r := range art.Results {
		if r.Improved {
			improved++
		}
	}
	rc.Log().Info("rpm_detect phase complete",
		"total", len(art.Results), "improved", improved)
	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// realRPMDetector wraps calibrate.Manager.DetectRPMSensor. Production
// uses this via the bridge.
type realRPMDetector struct {
	mgr *calibrate.Manager
}

// NewRPMDetector wraps a calibrate.Manager for RPMDetectPhase.
// Production wires it from the bridge.
func NewRPMDetector(mgr *calibrate.Manager) RPMDetector {
	return &realRPMDetector{mgr: mgr}
}

func (r *realRPMDetector) Detect(fan *config.Fan) (calibrate.DetectResult, error) {
	if r.mgr == nil {
		return calibrate.DetectResult{}, errors.New("realRPMDetector: nil calibrate.Manager")
	}
	return r.mgr.DetectRPMSensor(fan)
}

// loadRPMDetectArtifact reads the RPMDetectPhase's checkpoint. Best-
// effort: missing/failed → ApplyPhase uses the original ProbeArtifact
// RPMPath without overrides.
func loadRPMDetectArtifact(rc *RunContext) (RPMDetectArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return RPMDetectArtifact{}, err
	}
	prior, ok := state.Outcomes[(RPMDetectPhase{}).Name()]
	if !ok {
		return RPMDetectArtifact{}, errors.New("RPMDetectPhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return RPMDetectArtifact{}, fmt.Errorf(
			"RPMDetectPhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return RPMDetectArtifact{}, nil
	}
	var art RPMDetectArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return RPMDetectArtifact{}, fmt.Errorf("decode RPMDetectArtifact: %w", err)
	}
	return art, nil
}
