package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/recovery"
)

// PolarityFanResult is one entry in the PolarityArtifact. Mirrors
// polarity.ChannelResult's user-visible fields. The full
// polarity.ChannelResult also stores backend-specific Identity but the
// orchestrator's downstream consumers (ApplyPhase, web UI) only need
// the resolved polarity + reason string.
type PolarityFanResult struct {
	PWMPath       string  `json:"pwm_path"`
	Polarity      string  `json:"polarity"` // "normal" | "inverted" | "phantom" | "unknown"
	PhantomReason string  `json:"phantom_reason,omitempty"`
	Baseline      float64 `json:"baseline,omitempty"`
	Observed      float64 `json:"observed,omitempty"`
	Delta         float64 `json:"delta,omitempty"`
	Unit          string  `json:"unit,omitempty"`
	ProbeError    string  `json:"probe_error,omitempty"`
}

// PolarityArtifact is the structured result of the PolarityPhase.
// Consumed by ApplyPhase (skips fans with phantom polarity) and the
// wizard UI (renders per-fan polarity badges + phantom warnings).
type PolarityArtifact struct {
	Results []PolarityFanResult `json:"results"`
}

// PolarityPhase classifies every probed fan as normal / inverted /
// phantom by writing test PWM values and observing the RPM response.
// This is destructive — the phase actually drives fans. Production
// uses polarity.HwmonProber; tests inject a stub via Prober.
type PolarityPhase struct {
	// Prober is the test seam. nil → polarity.HwmonProber{}.
	Prober polarity.Prober
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (PolarityPhase) Name() string { return "polarity" }

// Execute consumes the prior ProbePhase's ProbeArtifact and runs each
// fan through the polarity prober.
func (p PolarityPhase) Execute(ctx context.Context, rc *RunContext) Outcome {
	prober := p.Prober
	if prober == nil {
		prober = &polarity.HwmonProber{}
	}

	probeArt, err := loadProbeArtifact(rc)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "load probe artifact: " + err.Error(),
		}
	}

	if len(probeArt.Fans) == 0 {
		rc.Sink().Emit("info", "polarity", "no fans to probe; skipping")
		raw, _ := EncodeArtifact(PolarityArtifact{})
		return Outcome{Status: StatusSkipped, Detail: "no fans enumerated", Artifact: raw}
	}

	rc.Sink().Emit("info", "polarity",
		fmt.Sprintf("classifying polarity for %d fan(s)", len(probeArt.Fans)))

	art := PolarityArtifact{Results: make([]PolarityFanResult, 0, len(probeArt.Fans))}
	for _, fan := range probeArt.Fans {
		ch := &probe.ControllableChannel{
			SourceID: fan.ChipName,
			PWMPath:  fan.PWMPath,
			TachPath: fan.RPMPath,
			Driver:   fan.ChipName,
			Polarity: "unknown",
		}
		rc.Sink().Emit("info", "polarity",
			fmt.Sprintf("probing %s (%s)", fan.LabelHint, fan.PWMPath))

		result, err := prober.ProbeChannel(ctx, ch)
		entry := PolarityFanResult{PWMPath: fan.PWMPath}
		if err != nil {
			entry.ProbeError = err.Error()
			entry.Polarity = "unknown"
			rc.Log().Warn("polarity probe failed",
				"pwm_path", fan.PWMPath, "err", err)
		} else {
			entry.Polarity = result.Polarity
			entry.PhantomReason = result.PhantomReason
			entry.Baseline = result.Baseline
			entry.Observed = result.Observed
			entry.Delta = result.Delta
			entry.Unit = result.Unit
		}
		art.Results = append(art.Results, entry)

		if err := ctx.Err(); err != nil {
			rc.Log().Warn("polarity phase cancelled mid-run", "err", err)
			break
		}
	}

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}

	// PolarityPhase succeeds even if some fans are phantom — that's
	// information for ApplyPhase, not a phase failure. Only fail on
	// context cancellation when zero fans completed.
	if ctx.Err() != nil && len(art.Results) == 0 {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "polarity phase cancelled before any fan completed",
		}
	}

	rc.Log().Info("polarity phase complete",
		"total", len(art.Results),
		"normal", countPolarity(art, "normal"),
		"inverted", countPolarity(art, "inverted"),
		"phantom", countPolarity(art, "phantom"),
		"unknown", countPolarity(art, "unknown"))

	return Outcome{Status: StatusSuccess, Artifact: raw}
}

func countPolarity(art PolarityArtifact, want string) int {
	n := 0
	for _, r := range art.Results {
		if r.Polarity == want {
			n++
		}
	}
	return n
}

// loadProbeArtifact reads the ProbePhase's checkpoint. Returns error
// when the prior phase didn't run, didn't succeed, or its artifact is
// malformed.
func loadProbeArtifact(rc *RunContext) (ProbeArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return ProbeArtifact{}, err
	}
	prior, ok := state.Outcomes[(ProbePhase{}).Name()]
	if !ok {
		return ProbeArtifact{}, errors.New("ProbePhase has not run; cannot proceed")
	}
	if prior.Status != StatusSuccess {
		return ProbeArtifact{}, fmt.Errorf(
			"ProbePhase did not succeed (status=%q); cannot proceed", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return ProbeArtifact{}, errors.New("ProbePhase produced no artifact")
	}
	var art ProbeArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return ProbeArtifact{}, fmt.Errorf("decode ProbeArtifact: %w", err)
	}
	return art, nil
}
