package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/recovery"
)

// polaritySafeRestorePWM is the PWM byte written to non-normal
// channels at the end of PolarityPhase to defeat the
// pwm_enable-restore-is-a-no-op trap (#1241). 64 ≈ 25% duty: low
// enough to be quiet on every fan the wizard has seen, high enough to
// avoid the stall band of cheap 3-pin tach reads, and conservative for
// inverted-polarity channels where 64 mapped through .Polarity = 191
// still keeps the fan moving (RPM signal preserved for downstream
// diagnostics). Chips where pwm_enable=InitialEnable already returns
// the fan to firmware control (BIOS auto = 2 pre-wizard) ignore this
// write — the firmware curve drives the channel.
const polaritySafeRestorePWM = 64

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

	// Restore pwm<N>_enable on every channel the bipolar probe drove
	// that won't be revisited by CalibratePhase. Without this the
	// probe-end pwm_enable+PWM combination leaves phantom-classified
	// channels at whatever PWM the prober last wrote — typically the
	// bipolar high-pulse value (255) — for the full CalibratePhase
	// wall-clock (5+ min on an 8-fan box). Audibly stuck at max RPM
	// for the entire wizard window with no UI indication anything is
	// wrong (#1220). The pattern mirrors ApplyPhase's EnableRestored
	// loop and uses the same probe-time InitialEnable byte. "normal"
	// channels are skipped because CalibratePhase will assert
	// pwm_enable on them within seconds; "inverted" and "unknown" are
	// included alongside "phantom" because the calibrate sweep gates
	// on polarity and never touches them either.
	enableRestored := 0
	for i, fan := range probeArt.Fans {
		if i >= len(art.Results) {
			break // ctx cancellation truncated the results slice
		}
		switch art.Results[i].Polarity {
		case polarity.PolarityNormal, polarity.PolarityProbational:
			// CalibratePhase will assert pwm_enable on these within
			// seconds. Probational channels go through the calibrate
			// sweep just like normal fans — the EC-cold reclassification
			// only affects the apply-phase admission rules, not the
			// downstream phase chain.
			continue
		case polarity.PolarityInverted, polarity.PolarityPhantom, polarity.PolarityUnknown:
			// fall through to restore.
		default:
			continue
		}
		if fan.EnablePath == "" || fan.InitialEnable == 0 {
			continue
		}
		// Step 1: drive a safe-mid PWM byte BEFORE flipping enable
		// state. Ordering matters: writing pwm_enable to manual mode
		// *first* could latch whatever the chip already had in the
		// pwm<N> register; setting pwm<N> before pwm_enable commits
		// keeps the byte the chip uses once the manual-mode flip
		// lands. Phantom + inverted channels can't be driven usefully
		// anyway, so polaritySafeRestorePWM (64 / 25%) is a quiet
		// floor instead of leaving the probe-end value (often 0 for
		// LOW or 255 for HIGH bipolar pulse). Best-effort: a write
		// error is logged WARN and the loop continues to the enable
		// restore so the operator-facing state still improves on
		// chips where the byte writes are partial. (#1241: NCT6687D
		// holds the probe-end PWM=153 across pwm_enable=1 restore
		// because the chip's manual mode latches the prior pwm<N>
		// register value.)
		if fan.PWMPath != "" {
			if err := os.WriteFile(fan.PWMPath,
				[]byte(strconv.Itoa(polaritySafeRestorePWM)), 0o644); err != nil {
				rc.Log().Warn("polarity: safe-PWM write failed",
					"pwm_path", fan.PWMPath,
					"polarity", art.Results[i].Polarity,
					"err", err)
				// don't skip the enable restore — the byte may have
				// been partially-written and pwm_enable still wants
				// restoring.
			}
		}
		// Step 2: restore pwm_enable to the probe-time InitialEnable.
		if err := os.WriteFile(fan.EnablePath,
			[]byte(strconv.Itoa(int(fan.InitialEnable))), 0o644); err != nil {
			rc.Log().Warn("polarity: restore pwm_enable failed",
				"enable_path", fan.EnablePath,
				"target", fan.InitialEnable,
				"polarity", art.Results[i].Polarity,
				"err", err)
			continue
		}
		enableRestored++
	}

	rc.Log().Info("polarity phase complete",
		"total", len(art.Results),
		"normal", countPolarity(art, polarity.PolarityNormal),
		"inverted", countPolarity(art, polarity.PolarityInverted),
		"phantom", countPolarity(art, polarity.PolarityPhantom),
		"probational", countPolarity(art, polarity.PolarityProbational),
		"unknown", countPolarity(art, polarity.PolarityUnknown),
		"pwm_enable_restored", enableRestored)

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
