package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

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
}

// CalibrateArtifact is the structured result of the CalibratePhase.
// Consumed by VerifyPhase (post-calibration phantom check) and
// ApplyPhase (uses StartPWM as MinPWM, MaxRPM for curve scaling).
type CalibrateArtifact struct {
	Results []CalibrateFanResult `json:"results"`
}

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
	// different chips sweep concurrently while fans on the same chip
	// stay sequential. Without this, six fans on one Super-I/O chip
	// serialise to ~5+ minutes (52s × 6); the legacy Manager.run
	// Phase 6 path completed the same set in ~1 minute by running one
	// goroutine per chip. Same-chip serialisation is the safety
	// constraint — shared PWM-enable registers on Super-I/O parts can
	// race when two pwmN sweeps happen in parallel.
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

	var wg sync.WaitGroup
	for _, jobs := range chipGroups {
		wg.Add(1)
		go func(jobs []job) {
			defer wg.Done()
			for _, j := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[j.idx] = sweepOne(ctx, p.Calibrator, rc, j.fan, polByPath[j.fan.PWMPath])
			}
		}(jobs)
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
	rc.Log().Info("calibrate success",
		"fan", fan.LabelHint,
		"start_pwm", result.StartPWM,
		"max_rpm", result.MaxRPM,
		"is_pump", entry.IsPump)
	return entry
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
