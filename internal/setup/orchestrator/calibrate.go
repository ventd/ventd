package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/recovery"
)

// Calibrator is the seam between CalibratePhase and the production
// calibrate.Manager so unit tests don't run multi-minute per-fan PWM
// sweeps. Production wires realCalibrator (which wraps
// calibrate.Manager.RunSync); tests inject a stub that returns
// configured Result values.
type Calibrator interface {
	// Calibrate runs a synchronous PWM-RPM sweep on the given fan
	// and returns the measured result. Per fan takes ~3-5 minutes
	// in production. Honours ctx cancellation; an aborted sweep
	// returns a partial Result with Aborted: true.
	Calibrate(ctx context.Context, fan *config.Fan) (calibrate.Result, error)
}

// CalibrateFanResult is one entry in the CalibrateArtifact. Mirrors
// the calibrate.Result fields the downstream ApplyPhase needs to
// build real PWM bounds and curve scaling, plus an audit log of how
// the sweep went.
type CalibrateFanResult struct {
	PWMPath    string `json:"pwm_path"`
	StartPWM   uint8  `json:"start_pwm"`             // lowest PWM that spins the fan from standstill
	StopPWM    uint8  `json:"stop_pwm,omitempty"`    // lowest PWM that keeps a spinning fan spinning
	MaxRPM     int    `json:"max_rpm"`               // RPM at PWM=255
	MinRPM     int    `json:"min_rpm,omitempty"`     // RPM at StartPWM
	IsPump     bool   `json:"is_pump,omitempty"`     // detected pump
	Aborted    bool   `json:"aborted,omitempty"`     // sweep aborted (ctx cancel / operator)
	Error      string `json:"error,omitempty"`       // non-empty on failure
	SweepMode  string `json:"sweep_mode,omitempty"`  // "pwm" (default) or "rpm" (pre-RDNA AMD)
	SkippedWhy string `json:"skipped_why,omitempty"` // non-empty when the fan was deliberately skipped

	// Phantom is true when the post-sweep sustained-RPM check
	// observed all-zero RPM at full-speed PWM AND the calibrate
	// sweep itself produced no evidence of a real fan (MaxRPM == 0).
	// ApplyPhase excludes phantom fans from the applied config.
	//
	// This field absorbs what was previously VerifyPhase: rather
	// than running a separate post-sweep phase that could (and on
	// some vendor-EC laptops did) disagree with the sweep, the
	// sustained check now lives inside sweepOne where it has the
	// full curve context. When the sweep measured a working curve
	// (MaxRPM > 0) but the sustained check sampled zero RPM, the
	// curve evidence wins and DisagreedWithSustainedCheck is set
	// instead of Phantom.
	Phantom bool `json:"phantom,omitempty"`

	// SustainedRPMs is the sample slice from the post-sweep
	// sustained-spin check. Empty when the check was skipped (no
	// RPM tach path, calibrate Error, calibrate SkippedWhy) or when
	// the polarity-effective full-speed write failed.
	SustainedRPMs []int `json:"sustained_rpms,omitempty"`

	// DisagreedWithSustainedCheck is set when the sustained-spin
	// check sampled zero RPM but the preceding sweep measured
	// MaxRPM > 0 for this channel. The fan is admitted (curve
	// evidence wins) but the disagreement is surfaced for the
	// doctor page. Observed on Dell SMM and similar vendor-EC
	// firmwares that reassert Q-Fan-style control after the sweep
	// finishes; the sustained-check write is masked but the
	// curve-time writes weren't.
	DisagreedWithSustainedCheck bool `json:"disagreed_with_sustained_check,omitempty"`

	// NonMonotonicCurve is true when the rising-portion PWM→RPM
	// sweep contains a drop greater than nonMonotonicDropThreshold
	// (15%) of MaxRPM between consecutive samples. The fan is
	// admitted; this field is a quality signal for the smart-mode
	// Layer C marginal-benefit estimator (a curve that goes
	// UP-DOWN-UP across the rising portion confuses the RLS fit)
	// and for the doctor page (operator-visible "your fan's curve
	// looks irregular — check for vendor-EC interference").
	//
	// Observed on Dell SMM and similar vendor-EC firmwares: the
	// sweep peaks at ~PWM=165 and then drops back ~500 RPM through
	// PWM=178-255 because the EC reasserts Q-Fan-style control at
	// high PWM. The reading is real (the curve is the actual
	// observed RPM at each PWM step) but it's not what the operator
	// can control. Surfaced for diagnostics; does not affect Phantom.
	NonMonotonicCurve bool `json:"non_monotonic_curve,omitempty"`

	// MaxDropRPM is the largest RPM drop between consecutive
	// rising-portion samples (max(curve[i].RPM - curve[i+1].RPM) for
	// i in [start_pwm..255-step]). Zero when the curve was strictly
	// non-decreasing. Carried for the doctor surface so an operator
	// can see how severe the irregularity is in absolute terms.
	MaxDropRPM int `json:"max_drop_rpm,omitempty"`

	// Curve is the per-step PWM→RPM map captured during the up-ramp
	// sweep (typically ~20 anchors from PWM=0 to PWM=255). ApplyPhase
	// consumes it for two decisions per fan:
	//
	//   1. Saturation knee — the highest PWM where RPM is still within
	//      5% of MaxRPM. Drives the per-fan curve's max_pwm_pct cap so
	//      the daemon doesn't waste duty cycle (and motor whine) above
	//      the point where the fan stops responding.
	//   2. Anchor placement — uniform-temp anchors with PWM% derived
	//      from the measured PWM→RPM monotonic envelope, so each anchor
	//      delivers a similar airflow delta even when the underlying
	//      curve is non-linear.
	//
	// Empty when calibrate skipped (phantom polarity, sweep error).
	Curve []CalibrateCurvePoint `json:"curve,omitempty"`
}

// CalibrateCurvePoint mirrors calibrate.PWMRPMPoint without taking
// the internal/calibrate import dependency on orchestrator's
// consumers (the artifact is JSON-serialised and downstream packages
// — apply.go, doctor detectors — decode it independently).
type CalibrateCurvePoint struct {
	PWM uint8 `json:"pwm"`
	RPM int   `json:"rpm"`
}

// nonMonotonicDropThreshold is the fraction of MaxRPM a single
// PWM-to-PWM RPM drop must exceed before the curve gets flagged as
// non-monotonic. 15% is generous enough that fan-tach noise and
// small overshoot/undershoot between adjacent steady states don't
// trip the flag, but tight enough that the EC-clamping pattern
// (peak 2112 RPM at PWM=165, then 1595 RPM at PWM=191 — a 24% drop
// on the same fan) registers.
const nonMonotonicDropThreshold = 0.15

// CalibrateArtifact is the structured result of the CalibratePhase.
// Consumed by ApplyPhase (uses StartPWM as MinPWM, MaxRPM for curve
// scaling, Phantom to exclude unwired channels).
type CalibrateArtifact struct {
	Results []CalibrateFanResult `json:"results"`
}

// sustainedSpinSettle is how long the post-sweep sustained-RPM check
// holds the polarity-effective full-speed PWM byte before sampling.
// 3 s matches the legacy VerifyPhase setting that proved out on the
// NCT6687D / IT8688E HIL fleet. Declared `var` (not `const`) so
// tests can shrink it to keep their wall-clock manageable; production
// callers never override it.
var sustainedSpinSettle = 3 * time.Second

// sustainedSpinSamples is how many RPM reads to take after settle.
var sustainedSpinSamples = 3

// sustainedSpinInterval is the delay between RPM samples.
var sustainedSpinInterval = 250 * time.Millisecond

// CalibratePhase runs a synchronous PWM-RPM sweep on every probed,
// non-phantom fan. This is the most operator-visible long-running
// phase in the wizard — each fan takes minutes. Polarity-aware
// (skips phantom fans; doesn't try to drive them).
//
// Reuses the existing calibrate.Manager.RunSync via the injectable
// Calibrator interface so the production path stays single-sourced
// with the legacy Manager.run Phase 6 — no duplication of the sweep
// algorithm. Tests substitute a stub.
type CalibratePhase struct {
	// Calibrator is the test seam. nil → fail at runtime (production
	// must wire a real Calibrator via the bridge — there is no
	// global default because constructing a calibrate.Manager
	// requires path + logger + watchdog dependencies the orchestrator
	// doesn't own).
	Calibrator Calibrator

	// WithinChipParallel reports whether a given chip name is verified
	// parallel-safe for within-chip-group calibrate sweeps (#1219).
	// nil → conservative serial-within-chip default (pre-#1219
	// behaviour). Production wires this to
	// hwdb.IsChipCalibrateWithinChipParallel against the loaded catalog;
	// tests inject a stub returning the desired flag.
	WithinChipParallel func(chipName string) bool
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (CalibratePhase) Name() string { return "calibrate" }

// Execute reads ProbeArtifact + PolarityArtifact and runs Calibrator
// against every non-phantom fan. Per-fan failures land in
// CalibrateFanResult.Error without failing the phase — partial
// calibration is more useful than none.
func (p CalibratePhase) Execute(ctx context.Context, rc *RunContext) Outcome {
	if p.Calibrator == nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "CalibratePhase requires a Calibrator (production must wire one via the bridge)",
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
		rc.Sink().Emit("info", "calibrate", "no fans to calibrate; skipping")
		raw, _ := EncodeArtifact(CalibrateArtifact{})
		return Outcome{Status: StatusSkipped, Detail: "no fans enumerated", Artifact: raw}
	}

	// Fan-out fanout: group probed fans by ChipName so fans on
	// different chips sweep concurrently. Within a chip group, the
	// inner loop runs serial OR parallel depending on
	// p.WithinChipParallel(chipName) (#1219). Without within-chip
	// parallelism, six fans on one Super-I/O chip serialise to
	// ~5+ minutes (50 s × 6); the cross-chip path alone completed
	// the same set in ~1 minute by running one goroutine per chip.
	// Within-chip parallelism takes the 8-fan single-chip case from
	// ~5 minutes to ~1 minute by stacking N goroutines on one chip
	// when the chip's pwm_enable register layout permits it.
	//
	// Same-chip serialisation is the legacy safety posture — some
	// Super-I/O parts (early NCT6775 / shared pwm_enable designs)
	// race the chip's fan-control state machine when two pwmN sweeps
	// overlap. Others (NCT6687-class chips with per-channel
	// pwm_enable registers) are independently addressable and
	// parallel-safe. Per-chip-family opt-in encoded in
	// internal/hwdb/catalog/chips/*.yaml as calibrate_within_chip_parallel
	// and consulted via p.WithinChipParallel.
	//
	// Each slot in `results` is written by exactly one goroutine, so
	// no mutex is needed on the slice. rc.Sink().Emit and rc.Log()
	// are thread-safe in production (Manager.EmitEvent locks; slog is
	// concurrent-safe by spec) and no-op in tests.
	type job struct {
		idx int
		fan ProbedFan
	}
	results := make([]CalibrateFanResult, len(probeArt.Fans))
	chipGroups := map[string][]job{}
	for i, fan := range probeArt.Fans {
		chipGroups[fan.ChipName] = append(chipGroups[fan.ChipName], job{idx: i, fan: fan})
	}

	withinParallel := p.WithinChipParallel
	if withinParallel == nil {
		// Conservative default: pre-#1219 serial-within-chip behaviour.
		withinParallel = func(string) bool { return false }
	}

	var wg sync.WaitGroup
	for chipName, jobs := range chipGroups {
		wg.Add(1)
		parallelInner := withinParallel(chipName)
		if parallelInner {
			rc.Log().Info("calibrate: parallelising within chip group",
				"chip", chipName, "fan_count", len(jobs))
		}
		go func(chipName string, jobs []job, parallelInner bool) {
			defer wg.Done()
			if !parallelInner {
				for _, j := range jobs {
					if ctx.Err() != nil {
						return
					}
					results[j.idx] = sweepOne(ctx, p.Calibrator, rc, j.fan, polByPath[j.fan.PWMPath])
				}
				return
			}
			var inner sync.WaitGroup
			for _, j := range jobs {
				if ctx.Err() != nil {
					return
				}
				inner.Add(1)
				go func(j job) {
					defer inner.Done()
					results[j.idx] = sweepOne(ctx, p.Calibrator, rc, j.fan, polByPath[j.fan.PWMPath])
				}(j)
			}
			inner.Wait()
		}(chipName, jobs, parallelInner)
	}
	wg.Wait()

	if ctx.Err() != nil {
		rc.Log().Warn("calibrate phase cancelled mid-run", "err", ctx.Err())
	}

	// Drop slots whose goroutine bailed before sweepOne ran (ctx
	// cancelled before its turn) — those carry an empty PWMPath that
	// would propagate as a "phantom-shaped" stub in the artifact.
	art := CalibrateArtifact{Results: make([]CalibrateFanResult, 0, len(results))}
	for _, r := range results {
		if r.PWMPath != "" {
			art.Results = append(art.Results, r)
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

	if ctx.Err() != nil && len(art.Results) == 0 {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "calibrate phase cancelled before any fan completed",
		}
	}

	successCount := 0
	for _, r := range art.Results {
		if r.Error == "" && r.SkippedWhy == "" && !r.Aborted {
			successCount++
		}
	}
	rc.Log().Info("calibrate phase complete",
		"total_fans", len(art.Results),
		"successful", successCount,
		"skipped_phantom", countSkipped(art),
		"failed", countFailed(art))

	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// sweepOne calibrates a single fan and returns the populated result
// entry. Extracted so the per-chip goroutines in Execute can call it
// without duplicating the polarity-skip / error-wrap / log shape.
// Pure of shared mutable state: the only mutation is into the returned
// value.
func sweepOne(
	ctx context.Context,
	cal Calibrator,
	rc *RunContext,
	fan ProbedFan,
	polarity string,
) CalibrateFanResult {
	entry := CalibrateFanResult{PWMPath: fan.PWMPath}

	if polarity == "phantom" {
		entry.SkippedWhy = "polarity=phantom — fan does not spin under PWM control"
		rc.Sink().Emit("info", "calibrate",
			fmt.Sprintf("skipping %s (phantom)", fan.LabelHint))
		return entry
	}

	cfgFan := &config.Fan{
		Name:     fan.LabelHint,
		Type:     "hwmon",
		PWMPath:  fan.PWMPath,
		RPMPath:  fan.RPMPath,
		ChipName: fan.ChipName,
		MinPWM:   0,   // calibrate determines this
		MaxPWM:   255, // sweep ceiling
	}

	rc.Sink().Emit("info", "calibrate",
		fmt.Sprintf("sweeping %s (this takes a few minutes)", fan.LabelHint))

	result, err := cal.Calibrate(ctx, cfgFan)
	if err != nil {
		entry.Error = err.Error()
		rc.Log().Warn("calibrate failed",
			"fan", fan.LabelHint, "pwm_path", fan.PWMPath, "err", err)
		return entry
	}

	entry.StartPWM = result.StartPWM
	entry.StopPWM = result.StopPWM
	entry.MaxRPM = result.MaxRPM
	entry.MinRPM = result.MinRPM
	entry.IsPump = result.FanType == "pump"
	entry.Aborted = result.Aborted
	entry.SweepMode = result.SweepMode
	if len(result.Curve) > 0 {
		entry.Curve = make([]CalibrateCurvePoint, len(result.Curve))
		for i, p := range result.Curve {
			entry.Curve[i] = CalibrateCurvePoint{PWM: p.PWM, RPM: p.RPM}
		}
	}
	rc.Log().Info("calibrate success",
		"fan", fan.LabelHint,
		"start_pwm", result.StartPWM,
		"max_rpm", result.MaxRPM,
		"is_pump", entry.IsPump)

	// Post-sweep sustained-spin check — formerly VerifyPhase, now
	// folded into the sweep so the curve data and the phantom verdict
	// live in the same artifact (RULE-SETUP-PHANTOM-VERIFY).
	//
	// Gated on: not aborted, have an RPM tach path, polarity is
	// resolved (skipping "phantom" polarity, which the sweep already
	// short-circuited above). When the sweep itself measured a real
	// curve (MaxRPM > 0) but the sustained check sees zero RPM, the
	// curve wins and DisagreedWithSustainedCheck is set — the
	// fresh-Fedora wizard regression where a Dell SMM fan with a
	// 2112-RPM measured curve was reclassified as phantom by a
	// 3.75 s post-sweep sample.
	if !entry.Aborted && fan.RPMPath != "" {
		samples, sErr := sustainedSpinSamplesAt(ctx, fan.PWMPath, fan.RPMPath, polarity)
		if sErr != nil {
			// Sysfs IO failed (write blocked, EC offline, synthetic
			// test path). Don't fail the phase — calibrate's curve
			// is still the authoritative signal. Admit the fan.
			rc.Log().Warn("calibrate: sustained-spin check skipped",
				"fan", fan.LabelHint,
				"pwm_path", fan.PWMPath,
				"err", sErr)
		} else {
			entry.SustainedRPMs = samples
			zero := allZero(samples)
			switch {
			case zero && entry.MaxRPM > 0:
				// Verify saw zero but the sweep measured a real curve.
				// Trust the curve.
				entry.DisagreedWithSustainedCheck = true
				rc.Log().Warn("calibrate: sustained-spin disagreed with curve; admitting fan",
					"fan", fan.LabelHint,
					"pwm_path", fan.PWMPath,
					"curve_max_rpm", entry.MaxRPM,
					"sustained_samples", samples)
			case zero:
				// No evidence in either direction → phantom.
				entry.Phantom = true
				rc.Log().Info("calibrate: phantom channel (zero RPM in sweep + sustained check)",
					"fan", fan.LabelHint,
					"pwm_path", fan.PWMPath)
			}
		}
	}

	// Monotonicity quality flag. Walk the rising-portion curve and
	// record the largest RPM drop between consecutive samples.
	// Doesn't affect Phantom — the curve is still real measurements;
	// this is a "your fan's response is irregular" signal for the
	// doctor surface and a quality input for smart-mode Layer C.
	//
	// Observed on Dell SMM (i7-6600U / Latitude 7280): sweep peaks
	// at PWM=165/2112 RPM then drops to ~1595 RPM through PWM=255
	// because the EC reasserts Q-Fan-style control. Recyclable for
	// any vendor-EC laptop whose firmware fights the wizard's PWM
	// writes above some threshold.
	if entry.MaxRPM > 0 && !entry.Phantom {
		maxDrop := largestRPMDrop(result.Curve, entry.StartPWM)
		entry.MaxDropRPM = maxDrop
		if float64(maxDrop) > nonMonotonicDropThreshold*float64(entry.MaxRPM) {
			entry.NonMonotonicCurve = true
			rc.Log().Warn("calibrate: non-monotonic curve detected",
				"fan", fan.LabelHint,
				"pwm_path", fan.PWMPath,
				"max_rpm", entry.MaxRPM,
				"max_drop_rpm", maxDrop,
				"threshold_pct", int(nonMonotonicDropThreshold*100))
		}
	}

	return entry
}

// largestRPMDrop walks the rising-portion of the curve (samples at
// pwm >= startPWM) and returns the largest RPM[i] - RPM[i+1] gap.
// Zero when the curve is strictly non-decreasing or empty. Used to
// flag NonMonotonicCurve for vendor-EC interference patterns.
func largestRPMDrop(curve []calibrate.PWMRPMPoint, startPWM uint8) int {
	worst := 0
	prev := -1
	for _, p := range curve {
		if p.PWM < startPWM {
			continue
		}
		if prev >= 0 && prev > p.RPM {
			if drop := prev - p.RPM; drop > worst {
				worst = drop
			}
		}
		prev = p.RPM
	}
	return worst
}

// sustainedSpinSamplesAt writes the polarity-effective full-speed PWM
// byte, settles for sustainedSpinSettle, and samples RPM
// sustainedSpinSamples times spaced sustainedSpinInterval apart. The
// original PWM byte is captured and restored on every exit path
// (including ctx cancellation and read failure) so the check never
// leaves a fan stranded at full speed.
//
// Returns the RPM sample slice. Early return on the first non-zero
// reading: a fan that's clearly spinning doesn't need three samples
// to prove it. Errors from sysfs (read/write failure on a synthetic
// test path or an offline EC) propagate to the caller, which admits
// the fan rather than failing the phase.
func sustainedSpinSamplesAt(ctx context.Context, pwmPath, rpmPath, polarity string) ([]int, error) {
	writeByte := byte(255)
	if polarity == "inverted" {
		writeByte = 0
	}

	orig, err := readPWMByteAt(pwmPath)
	if err != nil {
		return nil, fmt.Errorf("read orig pwm: %w", err)
	}
	defer func() {
		_ = os.WriteFile(pwmPath, []byte(strconv.Itoa(int(orig))), 0o644)
	}()

	if err := os.WriteFile(pwmPath, []byte(strconv.Itoa(int(writeByte))), 0o644); err != nil {
		return nil, fmt.Errorf("write full-speed: %w", err)
	}

	select {
	case <-time.After(sustainedSpinSettle):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	out := make([]int, 0, sustainedSpinSamples)
	for i := 0; i < sustainedSpinSamples; i++ {
		if i > 0 {
			select {
			case <-time.After(sustainedSpinInterval):
			case <-ctx.Done():
				return out, ctx.Err()
			}
		}
		v, err := readRPMIntAt(rpmPath)
		if err != nil {
			return out, fmt.Errorf("read rpm: %w", err)
		}
		out = append(out, v)
		if v > 0 {
			return out, nil
		}
	}
	return out, nil
}

// allZero reports whether every entry in xs is zero (and there is at
// least one entry). Used by the sustained-spin check to detect a
// truly-stationary fan vs. an empty sample slice (which means the
// check couldn't run).
func allZero(xs []int) bool {
	if len(xs) == 0 {
		return false
	}
	for _, v := range xs {
		if v != 0 {
			return false
		}
	}
	return true
}

// readPWMByteAt reads a sysfs pwm<N> file and parses a 0-255 byte.
// Local copy of the helper formerly in verify.go.
func readPWMByteAt(path string) (byte, error) {
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

// readRPMIntAt reads a sysfs fan<N>_input file and parses an int.
// Local copy of the helper formerly in verify.go.
func readRPMIntAt(path string) (int, error) {
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

func countSkipped(art CalibrateArtifact) int {
	n := 0
	for _, r := range art.Results {
		if r.SkippedWhy != "" {
			n++
		}
	}
	return n
}

func countFailed(art CalibrateArtifact) int {
	n := 0
	for _, r := range art.Results {
		if r.Error != "" {
			n++
		}
	}
	return n
}

// realCalibrator wraps calibrate.Manager.RunSync. Production uses
// this via the bridge; tests use their own Calibrator.
type realCalibrator struct {
	mgr *calibrate.Manager
}

// NewCalibrator wraps a calibrate.Manager into a Calibrator suitable
// for CalibratePhase.Calibrator. Production callers (the bridge) use
// this so the orchestrator path drives the same calibration engine
// the legacy Manager.run Phase 6 does — single-sourced.
func NewCalibrator(mgr *calibrate.Manager) Calibrator {
	return &realCalibrator{mgr: mgr}
}

func (c *realCalibrator) Calibrate(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
	if c.mgr == nil {
		return calibrate.Result{}, errors.New("realCalibrator: nil calibrate.Manager")
	}
	return c.mgr.RunSync(ctx, fan)
}

// loadCalibrateArtifact reads the CalibratePhase's checkpoint. Returns
// error when missing/failed; ApplyPhase tolerates an empty artifact
// (treats every fan as un-calibrated and uses safe defaults).
func loadCalibrateArtifact(rc *RunContext) (CalibrateArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return CalibrateArtifact{}, err
	}
	prior, ok := state.Outcomes[(CalibratePhase{}).Name()]
	if !ok {
		return CalibrateArtifact{}, errors.New("CalibratePhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return CalibrateArtifact{}, fmt.Errorf(
			"CalibratePhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return CalibrateArtifact{}, nil
	}
	var art CalibrateArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return CalibrateArtifact{}, fmt.Errorf("decode CalibrateArtifact: %w", err)
	}
	return art, nil
}
