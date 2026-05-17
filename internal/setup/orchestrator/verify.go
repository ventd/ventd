package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

// VerifyFanResult is one entry in the VerifyArtifact, recording the
// post-calibration phantom-spin check for one fan.
type VerifyFanResult struct {
	PWMPath          string `json:"pwm_path"`
	Phantom          bool   `json:"phantom"`                     // true → fan failed verify, should be excluded from config
	ReclassifiedFrom string `json:"reclassified_from,omitempty"` // polarity before verify, when verify flipped it to phantom
	SampleRPMs       []int  `json:"sample_rpms,omitempty"`
	Error            string `json:"error,omitempty"`
	Skipped          string `json:"skipped,omitempty"` // non-empty when verify deliberately skipped this fan
}

// VerifyArtifact is the structured result of the VerifyPhase. Consumed
// by ApplyPhase to exclude fans the verify marked as phantom.
type VerifyArtifact struct {
	Results []VerifyFanResult `json:"results"`
}

// VerifyPhase is the post-calibration phantom-spin contract from
// RULE-SETUP-PHANTOM-VERIFY. For each fan that survived
// CalibratePhase, writes the polarity-effective full-speed PWM byte
// (255 for normal, 0 for inverted), waits settleDuration, samples
// RPM sampleCount times; if all samples read 0 RPM the fan is
// reclassified as phantom and excluded from the applied config.
//
// Polarity-aware: an "inverted" channel takes full-speed at raw 0;
// writing raw 255 would leave it stopped and the verify would
// incorrectly mark it phantom. The phase reads PolarityArtifact and
// uses the resolved polarity to choose the right write byte.
//
// The original PWM is captured before write and restored on every
// exit path (including ctx cancellation and read failure) so the
// verify never leaves a fan at full speed.
type VerifyPhase struct {
	// SettleDuration is the time between writing the full-speed PWM
	// and starting RPM sampling. Default 3s (matches the legacy
	// Phase 6b setting that proved out on the NCT6687D / IT8688E HIL
	// fleet). Tests override with a smaller value via the field.
	SettleDuration time.Duration

	// SampleCount is how many RPM reads to take. Default 3.
	SampleCount int

	// SampleInterval is the delay between samples. Default 250ms.
	SampleInterval time.Duration
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (VerifyPhase) Name() string { return "verify" }

// Execute reads ProbeArtifact + PolarityArtifact + CalibrateArtifact,
// runs the phantom check per fan, writes VerifyArtifact.
func (p VerifyPhase) Execute(ctx context.Context, rc *RunContext) Outcome {
	settle := p.SettleDuration
	if settle == 0 {
		settle = 3 * time.Second
	}
	samples := p.SampleCount
	if samples == 0 {
		samples = 3
	}
	interval := p.SampleInterval
	if interval == 0 {
		interval = 250 * time.Millisecond
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

	calSkipped := map[string]string{}
	calFailed := map[string]string{}
	if calArt, calErr := loadCalibrateArtifact(rc); calErr == nil {
		for _, r := range calArt.Results {
			if r.SkippedWhy != "" {
				calSkipped[r.PWMPath] = r.SkippedWhy
			}
			if r.Error != "" {
				calFailed[r.PWMPath] = r.Error
			}
		}
	}

	if len(probeArt.Fans) == 0 {
		rc.Sink().Emit("info", "verify", "no fans to verify; skipping")
		raw, _ := EncodeArtifact(VerifyArtifact{})
		return Outcome{Status: StatusSkipped, Detail: "no fans enumerated", Artifact: raw}
	}

	art := VerifyArtifact{Results: make([]VerifyFanResult, 0, len(probeArt.Fans))}

	for _, fan := range probeArt.Fans {
		entry := VerifyFanResult{PWMPath: fan.PWMPath}

		// Skip fans we already know are phantom or that calibration
		// failed on — verify can't add useful information.
		if reason, ok := calSkipped[fan.PWMPath]; ok {
			entry.Skipped = "calibration skipped: " + reason
			entry.Phantom = true // skipped reasons all mean unusable
			art.Results = append(art.Results, entry)
			continue
		}
		if reason, ok := calFailed[fan.PWMPath]; ok {
			entry.Skipped = "calibration failed: " + reason
			art.Results = append(art.Results, entry)
			continue
		}

		polarity := polByPath[fan.PWMPath]
		if polarity == "phantom" {
			entry.Phantom = true
			entry.Skipped = "polarity=phantom"
			art.Results = append(art.Results, entry)
			continue
		}
		if fan.RPMPath == "" {
			entry.Skipped = "no RPM tach path; cannot verify"
			art.Results = append(art.Results, entry)
			continue
		}

		rc.Sink().Emit("info", "verify",
			fmt.Sprintf("verifying %s (writing full-speed for %s)", fan.LabelHint, settle))

		phantom, samples, err := p.checkOne(ctx, fan.PWMPath, fan.RPMPath, polarity, settle, samples, interval)
		entry.SampleRPMs = samples
		if err != nil {
			entry.Error = err.Error()
			// On error, admit the fan (don't claim phantom) — the
			// safer default is to allow control and let downstream
			// polarity.WritePWM refuse if needed.
			entry.Phantom = false
		} else {
			entry.Phantom = phantom
			if phantom && polarity != "" && polarity != "phantom" {
				entry.ReclassifiedFrom = polarity
			}
		}
		art.Results = append(art.Results, entry)

		if err := ctx.Err(); err != nil {
			rc.Log().Warn("verify phase cancelled mid-run", "err", err)
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

	phantomCount := 0
	for _, r := range art.Results {
		if r.Phantom {
			phantomCount++
		}
	}
	rc.Log().Info("verify phase complete",
		"total", len(art.Results),
		"phantom", phantomCount)
	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// checkOne writes the polarity-effective full-speed byte, settles, and
// samples RPM. Returns (true, samples, nil) when the fan is phantom
// (all samples read 0 RPM); (false, samples, nil) when at least one
// sample shows movement; (false, partial samples, err) on I/O error.
//
// The original PWM is captured and restored on every exit path.
func (p VerifyPhase) checkOne(ctx context.Context, pwmPath, rpmPath, polarity string,
	settle time.Duration, samples int, interval time.Duration,
) (bool, []int, error) {
	writeByte := byte(255)
	if polarity == "inverted" {
		writeByte = 0
	}

	orig, err := readSysfsByte(pwmPath)
	if err != nil {
		return false, nil, fmt.Errorf("read orig pwm: %w", err)
	}
	defer func() {
		_ = writeSysfsByte(pwmPath, orig)
	}()

	if err := writeSysfsByte(pwmPath, writeByte); err != nil {
		return false, nil, fmt.Errorf("write full-speed: %w", err)
	}

	select {
	case <-time.After(settle):
	case <-ctx.Done():
		return false, nil, ctx.Err()
	}

	out := make([]int, 0, samples)
	for i := 0; i < samples; i++ {
		if i > 0 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return false, out, ctx.Err()
			}
		}
		v, err := readSysfsInt2(rpmPath)
		if err != nil {
			return false, out, fmt.Errorf("read rpm: %w", err)
		}
		out = append(out, v)
		if v > 0 {
			return false, out, nil
		}
	}
	return true, out, nil
}

// readSysfsByte / writeSysfsByte / readSysfsInt2 are small local
// helpers so the verify phase doesn't pull in internal/hwmon (which
// would create a cycle through the bridge during early bootstrap).
func readSysfsByte(path string) (byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse byte from %s: %w", path, err)
	}
	if n < 0 || n > 255 {
		return 0, fmt.Errorf("value %d at %s out of byte range", n, path)
	}
	return byte(n), nil
}

func writeSysfsByte(path string, value byte) error {
	return os.WriteFile(path, []byte(strconv.Itoa(int(value))), 0o644)
}

func readSysfsInt2(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse int from %s: %w", path, err)
	}
	return n, nil
}

// loadVerifyArtifact reads the VerifyPhase's checkpoint. Returns an
// empty artifact when the phase didn't run — ApplyPhase tolerates
// this by treating no verify result as "fan is admitted."
func loadVerifyArtifact(rc *RunContext) (VerifyArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return VerifyArtifact{}, err
	}
	prior, ok := state.Outcomes[(VerifyPhase{}).Name()]
	if !ok {
		return VerifyArtifact{}, errors.New("VerifyPhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return VerifyArtifact{}, fmt.Errorf(
			"VerifyPhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return VerifyArtifact{}, nil
	}
	var art VerifyArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return VerifyArtifact{}, fmt.Errorf("decode VerifyArtifact: %w", err)
	}
	return art, nil
}
