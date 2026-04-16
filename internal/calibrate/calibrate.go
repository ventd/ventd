// Package calibrate implements fan calibration: ramp PWM from min to max,
// record RPM at each step, determine start_pwm (lowest PWM that spins the fan)
// and max_rpm.
package calibrate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
)

// SchemaVersion is the current on-disk format for calibration.json.
// Bump when the structure of Result or its envelope changes incompatibly.
//
//	v1 — PWM-only sweep. Result has no SweepMode field.
//	v2 — adds SweepMode + RPMCurve to Result. v1 records load cleanly; loader
//	     defaults missing SweepMode to "pwm".
const SchemaVersion = 2

// AutoFixRecalibrate is re-exported so existing callers / tests need not
// import hwdiag directly. Tier 5 moved the canonical definition into hwdiag;
// new code should reference hwdiag.AutoFixRecalibrate.
const AutoFixRecalibrate = hwdiag.AutoFixRecalibrate

// onDiskEnvelope wraps the results map with a schema_version. v1 is the
// current format; v0 is the legacy bare map and is migrated transparently.
type onDiskEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Results       map[string]Result `json:"results"`
}

// PWMRPMPoint is a single sample from the up-ramp during calibration.
type PWMRPMPoint struct {
	PWM uint8 `json:"pwm"`
	RPM int   `json:"rpm"`
}

// RPMTargetPoint is a single sample from an RPM-target sweep (pre-RDNA AMD).
// TargetRPM is the value written to fan*_target; ActualRPM is the observed
// reading after settling. Settled is true if ActualRPM stabilised within ±5%
// of TargetRPM within the per-step timeout.
type RPMTargetPoint struct {
	TargetRPM int  `json:"target_rpm"`
	ActualRPM int  `json:"actual_rpm"`
	Settled   bool `json:"settled"`
}

// SweepModePWM is the default mode: write pwm*, record RPM per step.
// SweepModeRPM writes fan*_target, polls fan*_input for settle per step.
const (
	SweepModePWM = "pwm"
	SweepModeRPM = "rpm"
)

// Result holds the outcome of a completed calibration run.
//
// The Partial / CompletedSteps / DownRampPWM fields make calibration crash-safe.
// While a sweep is in progress they are written after every step; on daemon
// restart, runSync resumes from CompletedSteps (or from DownRampPWM if the
// up-ramp had completed). On successful completion they are reset to zero so
// applied results carry no resume state.
type Result struct {
	PWMPath        string        `json:"pwm_path"`
	StartPWM       uint8         `json:"start_pwm"` // lowest PWM that produces non-zero RPM from standstill
	StopPWM        uint8         `json:"stop_pwm"`  // lowest PWM that keeps a spinning fan spinning (0 if not measured)
	MaxRPM         int           `json:"max_rpm"`
	MinRPM         int           `json:"min_rpm"`                   // RPM at StartPWM
	Curve          []PWMRPMPoint `json:"curve,omitempty"`           // full PWM→RPM mapping from up-ramp
	FanType        string        `json:"fan_type,omitempty"`        // "pump" when detected; "" otherwise
	Partial        bool          `json:"partial,omitempty"`         // true while sweep is in progress
	CompletedSteps int           `json:"completed_steps,omitempty"` // up-ramp steps completed (resume anchor)
	DownRampPWM    uint8         `json:"down_ramp_pwm,omitempty"`   // last PWM tested in down-ramp; 0 = down-ramp not started
	// FanFingerprint captures fan identity (type, pwm path, min/max PWM) at
	// checkpoint time. On resume, a mismatch means the underlying hardware or
	// config changed and the partial is discarded rather than blindly applied.
	FanFingerprint string `json:"fan_fingerprint,omitempty"`
	// Aborted is set on the on-disk record when a sweep terminated because the
	// user fired Abort. It tells startup to treat the record as final (do not
	// resume) rather than as an in-flight checkpoint.
	Aborted bool `json:"aborted,omitempty"`
	// SweepMode records how this fan was calibrated. "pwm" (default, empty on
	// v1 records → normalised by load()) or "rpm". Consumers that only care
	// about Curve can ignore this field; consumers that need to control the
	// fan must dispatch on it to pick the correct sysfs attribute.
	SweepMode string `json:"sweep_mode,omitempty"`
	// RPMCurve holds the per-step samples for an RPM-target sweep. Unused in
	// PWM mode.
	RPMCurve []RPMTargetPoint `json:"rpm_curve,omitempty"`
}

// fanFingerprint produces the identity string compared on resume. Any field
// that would change the shape of the sweep belongs here. ControlKind is
// included so a config flip between pwm and rpm_target invalidates old
// checkpoints — the two modes produce incompatible sweep shapes.
func fanFingerprint(fan *config.Fan) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d",
		fan.Type, fan.ControlKind, fan.PWMPath, fan.MinPWM, fan.MaxPWM)
}

// selectSweepMode decides which sweep to run for a given fan. Capability-first:
// a fan configured with ControlKind="rpm_target" (detected at scan time because
// it exposes fan*_target and fan*_min/fan*_max instead of pwm*) sweeps in RPM
// mode; everything else uses PWM. The controller uses the same ControlKind to
// pick its write path at runtime, so calibration and control stay in sync.
func selectSweepMode(fan *config.Fan) string {
	if fan.ControlKind == "rpm_target" {
		return SweepModeRPM
	}
	return SweepModePWM
}

// Status describes the current state of a calibration run.
//
// State is the terminal/active label: "" (never started), "running",
// "complete", "error", or "aborted". Running is retained for callers that
// already check it, but new callers should prefer State.
type Status struct {
	PWMPath    string `json:"pwm_path"`
	Running    bool   `json:"running"`
	State      string `json:"state"`
	Progress   int    `json:"progress"`    // 0–100
	CurrentPWM uint8  `json:"current_pwm"` // PWM being tested right now
	Error      string `json:"error,omitempty"`
}

// runState holds mutable state for one in-flight calibration.
type runState struct {
	mu       sync.Mutex
	running  bool
	state    string // "" | "running" | "complete" | "error" | "aborted"
	progress int
	current  uint8
	errMsg   string
	cancel   context.CancelFunc // fired by Abort; nil until Start/RunSync wires it
}

// Manager owns all calibration state. One instance per daemon.
type Manager struct {
	mu          sync.Mutex
	results     map[string]Result // keyed by pwm_path
	runs        map[string]*runState
	path        string // persist file
	logger      *slog.Logger
	wd          *watchdog.Watchdog // optional; when non-nil each sweep registers/deregisters its fan
	diagnostics []hwdiag.Entry     // populated by load() when on-disk schema is unsupported
	store       *hwdiag.Store      // optional; when non-nil emitters also push into the shared store
}

// SetDiagnosticStore attaches the process-wide hwdiag store. Diagnostics
// already captured during load() are backfilled on attach so callers can wire
// the store up at any point after New().
func (m *Manager) SetDiagnosticStore(s *hwdiag.Store) {
	m.mu.Lock()
	m.store = s
	pending := make([]hwdiag.Entry, len(m.diagnostics))
	copy(pending, m.diagnostics)
	m.mu.Unlock()
	if s == nil {
		return
	}
	for _, e := range pending {
		s.Set(e)
	}
}

// New creates a Manager and loads persisted results from path (if it exists).
// wd may be nil — in that case calibration sweeps run without the per-sweep
// watchdog wrap (tests construct managers this way; production main.go always
// passes a real watchdog).
func New(path string, logger *slog.Logger, wd *watchdog.Watchdog) *Manager {
	m := &Manager{
		results: make(map[string]Result),
		runs:    make(map[string]*runState),
		path:    path,
		logger:  logger,
		wd:      wd,
	}
	m.load()
	return m
}

// IsCalibrating returns true if the given pwmPath is currently being calibrated.
func (m *Manager) IsCalibrating(pwmPath string) bool {
	m.mu.Lock()
	rs, ok := m.runs[pwmPath]
	m.mu.Unlock()
	if !ok {
		return false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.running
}

// Start launches a calibration goroutine for the given fan. Returns an error if
// a calibration is already in progress for this fan.
func (m *Manager) Start(fan *config.Fan) error {
	m.mu.Lock()
	rs, exists := m.runs[fan.PWMPath]
	if exists {
		rs.mu.Lock()
		already := rs.running
		rs.mu.Unlock()
		if already {
			m.mu.Unlock()
			return fmt.Errorf("calibrate: already running for %s", fan.PWMPath)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	rs = &runState{cancel: cancel}
	m.runs[fan.PWMPath] = rs
	m.mu.Unlock()

	go m.run(ctx, fan, rs)
	return nil
}

// AllStatus returns a snapshot of all calibration states.
func (m *Manager) AllStatus() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.runs))
	for path, rs := range m.runs {
		rs.mu.Lock()
		out = append(out, Status{
			PWMPath:    path,
			Running:    rs.running,
			State:      rs.state,
			Progress:   rs.progress,
			CurrentPWM: rs.current,
			Error:      rs.errMsg,
		})
		rs.mu.Unlock()
	}
	return out
}

// Abort cancels an in-flight calibration for pwmPath if one exists. Idempotent:
// safe to call when no calibration is running, or to call repeatedly. The
// runSync defer restores the fan's PWM via the existing safety path; this
// method only fires the context.
func (m *Manager) Abort(pwmPath string) {
	m.mu.Lock()
	rs, ok := m.runs[pwmPath]
	m.mu.Unlock()
	if !ok {
		return
	}
	rs.mu.Lock()
	cancel := rs.cancel
	rs.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RemapKey renames a stored result key from oldPath to newPath.
// Called at startup when hwmonX renumbering causes a path to move.
func (m *Manager) RemapKey(oldPath, newPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.results[oldPath]; ok {
		r.PWMPath = newPath
		m.results[newPath] = r
		delete(m.results, oldPath)
	}
}

// AllResults returns a snapshot of all completed calibration results.
// When load() detected a future schema_version, results are withheld so the
// daemon falls back to the safe defaults baked into the live config; the
// matching diagnostic is exposed via Diagnostics().
//
// Curve is deep-copied because in-flight sweeps mutate the stored Result's
// backing array (see snapshot() in runSync); without this copy a web-UI poll
// races with the next append.
func (m *Manager) AllResults() map[string]Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.diagnostics {
		if d.Remediation != nil && d.Remediation.AutoFixID == AutoFixRecalibrate {
			return map[string]Result{}
		}
	}
	out := make(map[string]Result, len(m.results))
	for k, v := range m.results {
		if len(v.Curve) > 0 {
			curveCopy := make([]PWMRPMPoint, len(v.Curve))
			copy(curveCopy, v.Curve)
			v.Curve = curveCopy
		}
		out[k] = v
	}
	return out
}

// Diagnostics returns a snapshot of any hardware-level diagnostics the
// calibration manager has recorded (e.g. unsupported on-disk schema).
func (m *Manager) Diagnostics() []hwdiag.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]hwdiag.Entry, len(m.diagnostics))
	copy(out, m.diagnostics)
	return out
}

// run is the calibration goroutine.
func (m *Manager) run(ctx context.Context, fan *config.Fan, rs *runState) {
	defer func() {
		// Release the cancel func so the context is GC'd. Safe to call after
		// Abort already fired it — context.CancelFunc is idempotent.
		rs.mu.Lock()
		c := rs.cancel
		rs.mu.Unlock()
		if c != nil {
			c()
		}
	}()
	result, _ := m.runSync(ctx, fan, rs)
	if ctx.Err() != nil {
		result = markAborted(result)
	}

	m.mu.Lock()
	m.results[fan.PWMPath] = result
	m.mu.Unlock()

	m.logger.Info("calibrate: done",
		"pwm_path", fan.PWMPath,
		"start_pwm", result.StartPWM,
		"stop_pwm", result.StopPWM,
		"max_rpm", result.MaxRPM,
		"fan_type", result.FanType,
	)
	m.save()
}

// RunSync runs calibration synchronously and returns the result. Intended for
// the setup wizard. Returns an error only for fatal pre-flight failures; partial
// results (e.g. fan stalled mid-ramp) are returned with a nil error.
func (m *Manager) RunSync(ctx context.Context, fan *config.Fan) (Result, error) {
	m.mu.Lock()
	if rs, ok := m.runs[fan.PWMPath]; ok {
		rs.mu.Lock()
		already := rs.running
		rs.mu.Unlock()
		if already {
			m.mu.Unlock()
			return Result{}, fmt.Errorf("calibrate: already running for %s", fan.PWMPath)
		}
	}
	derivedCtx, cancel := context.WithCancel(ctx)
	rs := &runState{cancel: cancel}
	m.runs[fan.PWMPath] = rs
	m.mu.Unlock()

	defer cancel()

	result, err := m.runSync(derivedCtx, fan, rs)
	if err != nil {
		if derivedCtx.Err() != nil {
			aborted := markAborted(result)
			m.mu.Lock()
			m.results[fan.PWMPath] = aborted
			m.mu.Unlock()
			m.save()
		}
		return result, err
	}

	m.mu.Lock()
	m.results[fan.PWMPath] = result
	m.mu.Unlock()
	m.save()
	return result, nil
}

// runSync is the core calibration implementation shared by run (goroutine) and
// RunSync (blocking). It sets rs.running, does the ramp, and clears rs.running.
// Dispatches on sweep mode: RPM-target fans (pre-RDNA AMD) use runSyncRPM; the
// rest use the default PWM up-ramp / down-ramp path.
func (m *Manager) runSync(ctx context.Context, fan *config.Fan, rs *runState) (Result, error) {
	if selectSweepMode(fan) == SweepModeRPM {
		return m.runSyncRPM(ctx, fan, rs)
	}
	return m.runSyncPWM(ctx, fan, rs)
}

// runSyncPWM is the original PWM-duty-cycle calibration path.
func (m *Manager) runSyncPWM(ctx context.Context, fan *config.Fan, rs *runState) (Result, error) {
	pwmPath := fan.PWMPath
	minPWM := fan.MinPWM
	maxPWM := fan.MaxPWM
	if maxPWM == 0 {
		maxPWM = 255
	}
	isNvidia := fan.Type == "nvidia"
	var gpuIdx uint64
	if isNvidia {
		gpuIdx, _ = strconv.ParseUint(pwmPath, 10, 32)
	}

	rs.mu.Lock()
	rs.running = true
	rs.state = "running"
	rs.errMsg = ""
	rs.mu.Unlock()

	// Per-sweep watchdog wrap. If the daemon dies (signal, panic) mid-sweep,
	// the deferred Restore on main's side walks all entries and puts each fan
	// back to its captured pwm_enable. The Deregister below pops this sweep's
	// entry on normal exit so post-setup the startup registration remains the
	// authoritative one.
	if m.wd != nil {
		m.wd.Register(pwmPath, fan.Type)
		defer m.wd.Deregister(pwmPath)
	}

	// PWM=0 safety sentinel. Calibration sweeps that probe stop_pwm
	// drive PWM to 0 intentionally; the sentinel escalates to
	// SafePWMFloor if 0 is held longer than ZeroPWMMaxDuration so
	// a hung sweep can never leave a fan stopped under load. See
	// internal/calibrate/safety.go for the full design note.
	sentinel := NewZeroPWMSentinel(m.logger, func() {
		if isNvidia {
			_ = nvidia.WriteFanSpeed(uint(gpuIdx), SafePWMFloor)
		} else {
			_ = hwmon.WritePWM(pwmPath, SafePWMFloor)
		}
	})
	defer sentinel.Stop()

	// Safety: restore fan control when we exit, regardless of outcome.
	// The terminal state ("complete" | "error" | "aborted") is decided here so
	// callers polling Status see the right label after the goroutine exits.
	defer func() {
		if isNvidia {
			_ = nvidia.ResetFanSpeed(uint(gpuIdx))
		} else {
			// Intentionally WritePWM (not WritePWMSafe): this is the cleanup path.
			// If pwm_enable has somehow flipped to 0/2 mid-sweep, WritePWMSafe would
			// refuse and leave the fan at whatever duty the last forward write set —
			// the opposite of safe. The controller or watchdog will reassert mode=2
			// on the next tick; this write is just a best-effort knock-down.
			_ = hwmon.WritePWM(pwmPath, minPWM)
		}
		rs.mu.Lock()
		rs.running = false
		switch {
		case ctx.Err() != nil:
			rs.state = "aborted"
		case rs.errMsg != "":
			rs.state = "error"
		default:
			rs.state = "complete"
		}
		rs.mu.Unlock()
	}()

	// Take manual control (hwmon only; NVML manages its own mode).
	if !isNvidia {
		if err := hwmon.WritePWMEnable(pwmPath, 1); err != nil {
			rs.mu.Lock()
			rs.errMsg = err.Error()
			rs.mu.Unlock()
			m.logger.Warn("calibrate: take manual control failed", "pwm_path", pwmPath, "err", err)
			// Continue anyway — some drivers don't support pwm_enable.
		}
	}

	// ctxSleep waits for d or ctx cancellation. Returns true if cancelled.
	ctxSleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return true
		case <-time.After(d):
			return false
		}
	}

	const steps = 20
	startPWM := uint8(0)
	minRPM := 0 // RPM recorded at startPWM
	maxRPM := 0
	stopPWM := uint8(0)
	var points []PWMRPMPoint

	fingerprint := fanFingerprint(fan)

	// Resume from last checkpoint, if any. After a daemon crash mid-sweep,
	// m.results carries Partial=true with up-ramp/down-ramp progress; load()
	// already rehydrated it from disk. The FanFingerprint guards against
	// resuming onto hardware or config that no longer matches the checkpoint.
	resumeStep := 0
	resumeDownPWM := uint8(0)
	m.mu.Lock()
	prev, hasPrev := m.results[pwmPath]
	m.mu.Unlock()
	switch {
	case hasPrev && prev.Partial && prev.FanFingerprint != "" && prev.FanFingerprint != fingerprint:
		m.logger.Warn("calibrate: checkpoint fingerprint mismatch, starting fresh",
			"pwm_path", pwmPath,
			"checkpoint_fingerprint", prev.FanFingerprint,
			"current_fingerprint", fingerprint,
		)
	case hasPrev && prev.Partial:
		resumeStep = prev.CompletedSteps
		resumeDownPWM = prev.DownRampPWM
		startPWM = prev.StartPWM
		minRPM = prev.MinRPM
		maxRPM = prev.MaxRPM
		points = prev.Curve
		m.logger.Info("calibrate: resuming from checkpoint",
			"pwm_path", pwmPath,
			"completed_steps", resumeStep,
			"down_ramp_pwm", resumeDownPWM,
		)
	}

	// snapshot builds the current partial result for checkpointing or early return.
	// The Curve is deep-copied because `points` continues to grow in subsequent
	// iterations; without the copy, a web-UI poll of /api/calibrate/results
	// would race with the next append on the shared backing array.
	snapshot := func(downPWM uint8, completedSteps int) Result {
		curveCopy := make([]PWMRPMPoint, len(points))
		copy(curveCopy, points)
		return Result{
			PWMPath:        pwmPath,
			StartPWM:       startPWM,
			StopPWM:        stopPWM,
			MaxRPM:         maxRPM,
			MinRPM:         minRPM,
			Curve:          curveCopy,
			Partial:        true,
			CompletedSteps: completedSteps,
			DownRampPWM:    downPWM,
			FanFingerprint: fingerprint,
			SweepMode:      SweepModePWM,
		}
	}

	// Early-exit: if we've been at ≥50% PWM for this many consecutive steps
	// with no response, assume no fan is present and stop.
	const noFanThreshold = 3
	noFanCount := 0

	for step := resumeStep; step <= steps; step++ {
		if err := ctx.Err(); err != nil {
			return snapshot(0, step), err
		}
		// Linearly interpolate from minPWM to maxPWM.
		frac := float64(step) / float64(steps)
		pwm := minPWM + uint8(frac*float64(maxPWM-minPWM))
		if step == steps {
			pwm = maxPWM
		}

		rs.mu.Lock()
		rs.current = pwm
		rs.progress = step * 100 / steps
		rs.mu.Unlock()

		var writeErr error
		if isNvidia {
			writeErr = nvidia.WriteFanSpeed(uint(gpuIdx), pwm)
		} else {
			writeErr = hwmon.WritePWMSafe(pwmPath, pwm)
		}
		if writeErr == nil {
			// Notify the safety sentinel of the new commanded PWM
			// after a successful write. Set(0) arms the 2s
			// escalation timer; Set(non-zero) cancels it.
			sentinel.Set(pwm)
		}
		if writeErr != nil {
			rs.mu.Lock()
			rs.errMsg = writeErr.Error()
			rs.mu.Unlock()
			m.logger.Error("calibrate: write pwm failed", "err", writeErr)
			return snapshot(0, step), writeErr
		}

		// Wait for the fan to respond (fans are slow to spin up).
		if ctxSleep(2 * time.Second) {
			return snapshot(0, step), ctx.Err()
		}

		// For nvidia, ReadFanSpeed returns a 0–255 PWM readback (not RPM).
		// For hwmon, we read actual RPM from the paired sensor.
		var rpm int
		if isNvidia {
			v, _ := nvidia.ReadFanSpeed(uint(gpuIdx))
			rpm = int(v) // PWM readback used as a proxy for "spinning"
		} else if fan.RPMPath != "" {
			rpm, _ = hwmon.ReadRPMPath(fan.RPMPath)
		} else {
			rpm, _ = hwmon.ReadRPM(pwmPath)
		}

		if rpm > 0 && startPWM == 0 {
			startPWM = pwm
			minRPM = rpm
		}
		if rpm > maxRPM {
			maxRPM = rpm
		}
		points = append(points, PWMRPMPoint{PWM: pwm, RPM: rpm})

		m.logger.Debug("calibrate: step", "pwm_path", pwmPath, "pwm", pwm, "rpm", rpm)

		// Checkpoint after every completed step so a daemon restart can resume.
		m.checkpoint(pwmPath, snapshot(0, step+1))

		// Early-exit if no response at high PWM — no fan connected.
		if startPWM == 0 && pwm >= maxPWM/2 {
			noFanCount++
			if noFanCount >= noFanThreshold {
				m.logger.Info("calibrate: no fan detected, aborting early", "pwm_path", pwmPath, "pwm", pwm)
				break
			}
		} else {
			noFanCount = 0
		}
	}

	// Down-ramp: find stop_pwm (the hysteresis point where a spinning fan stalls).
	// This is lower than start_pwm because a fan in motion requires less power to
	// keep spinning than to start from standstill.
	if startPWM > 0 && !isNvidia {
		// Resume mid-down-ramp: continue past the last PWM tested. resumeDownPWM
		// is 0 when down-ramp hadn't started yet (fresh sweep or up-ramp resume).
		startP := int(startPWM)
		if resumeDownPWM > 0 {
			startP = int(resumeDownPWM) - 2
		}
		for p := startP; p > 0; p -= 2 {
			if ctx.Err() != nil {
				return snapshot(uint8(p), steps+1), ctx.Err()
			}
			if err := hwmon.WritePWMSafe(pwmPath, uint8(p)); err != nil {
				break
			}
			if ctxSleep(1500 * time.Millisecond) {
				return snapshot(uint8(p), steps+1), ctx.Err()
			}
			var rpm int
			if fan.RPMPath != "" {
				rpm, _ = hwmon.ReadRPMPath(fan.RPMPath)
			} else {
				rpm, _ = hwmon.ReadRPM(pwmPath)
			}
			m.logger.Debug("calibrate: down-ramp", "pwm_path", pwmPath, "pwm", p, "rpm", rpm)
			if rpm == 0 {
				// Fan stalled at p; last PWM that kept it spinning was p+2.
				stopPWM = uint8(p + 2)
				break
			}
			// Checkpoint after every down-ramp step so a daemon restart can resume.
			m.checkpoint(pwmPath, snapshot(uint8(p), steps+1))
		}
	}

	// Detect pump: high constant RPM with low variance across the PWM range.
	// A fan that can be stopped (startPWM > 0) is never a pump — pumps always spin.
	fanType := ""
	if maxRPM > 2500 && minRPM > 0 && startPWM == 0 {
		variance := maxRPM - minRPM
		if variance*10 < maxRPM { // variance < 10% of maxRPM
			fanType = "pump"
		}
	}

	return Result{
		PWMPath:        pwmPath,
		StartPWM:       startPWM,
		StopPWM:        stopPWM,
		MaxRPM:         maxRPM,
		MinRPM:         minRPM,
		Curve:          points,
		FanType:        fanType,
		FanFingerprint: fingerprint,
		SweepMode:      SweepModePWM,
	}, nil
}

// runSyncRPM calibrates an RPM-target fan (pre-RDNA AMD, fan*_target channel).
// It reads fan*_min/fan*_max, sweeps fan*_target in ~10 steps across that
// range, and at each step polls fan*_input up to 5 seconds for the actual RPM
// to settle within ±5% of the setpoint. Records per-step samples, achievable
// min/max RPM (first and last settled steps), and emits a diagnostic if the
// fan never reached any setpoint.
//
// Checkpointing, watchdog registration, abort handling, and fingerprint reuse
// the same machinery as the PWM path — the only divergence is the inner write
// path (fan*_target instead of pwm*) and the per-step settle loop.
func (m *Manager) runSyncRPM(ctx context.Context, fan *config.Fan, rs *runState) (Result, error) {
	targetPath := fan.PWMPath

	rs.mu.Lock()
	rs.running = true
	rs.state = "running"
	rs.errMsg = ""
	rs.mu.Unlock()

	if m.wd != nil {
		m.wd.Register(targetPath, fan.Type)
		defer m.wd.Deregister(targetPath)
	}

	// Discover the driver-advertised RPM range. Fall back to a conservative
	// window if fan*_min is absent (ReadFanMinRPM returns 0) — we don't want
	// to spin at 0 RPM for more than a moment.
	minRPMRange := hwmon.ReadFanMinRPM(targetPath)
	maxRPMRange := hwmon.ReadFanMaxRPM(targetPath)
	if minRPMRange <= 0 {
		minRPMRange = maxRPMRange / 4 // safe floor: 25% of max
	}
	if maxRPMRange <= minRPMRange {
		maxRPMRange = minRPMRange + 100 // degenerate; will still emit a diagnostic below
	}

	enablePath := hwmon.RPMTargetEnablePath(targetPath)
	defer func() {
		// On exit, write fan*_min as a safe floor. Watchdog handles the
		// pwm_enable restore on abnormal termination; this is the normal-exit
		// path where we just leave the fan at a low but non-zero RPM.
		_ = hwmon.WriteFanTarget(targetPath, minRPMRange)
		rs.mu.Lock()
		rs.running = false
		switch {
		case ctx.Err() != nil:
			rs.state = "aborted"
		case rs.errMsg != "":
			rs.state = "error"
		default:
			rs.state = "complete"
		}
		rs.mu.Unlock()
	}()

	// Take manual control. Some drivers don't expose pwm_enable for the
	// companion fan channel — log and continue.
	if err := hwmon.WritePWMEnablePath(enablePath, 1); err != nil {
		m.logger.Warn("calibrate: take manual control (rpm_target) failed",
			"target_path", targetPath, "enable_path", enablePath, "err", err)
	}

	ctxSleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return true
		case <-time.After(d):
			return false
		}
	}

	const steps = 10
	const settleTimeout = 5 * time.Second
	const settlePoll = 500 * time.Millisecond

	fingerprint := fanFingerprint(fan)
	var samples []RPMTargetPoint
	resumeStep := 0

	m.mu.Lock()
	prev, hasPrev := m.results[targetPath]
	m.mu.Unlock()
	switch {
	case hasPrev && prev.Partial && prev.FanFingerprint != "" && prev.FanFingerprint != fingerprint:
		m.logger.Warn("calibrate: rpm checkpoint fingerprint mismatch, starting fresh",
			"target_path", targetPath,
			"checkpoint_fingerprint", prev.FanFingerprint,
			"current_fingerprint", fingerprint,
		)
	case hasPrev && prev.Partial && prev.SweepMode == SweepModeRPM:
		resumeStep = prev.CompletedSteps
		samples = prev.RPMCurve
		m.logger.Info("calibrate: resuming rpm sweep from checkpoint",
			"target_path", targetPath, "completed_steps", resumeStep)
	}

	snapshot := func(completedSteps int) Result {
		samplesCopy := make([]RPMTargetPoint, len(samples))
		copy(samplesCopy, samples)
		r := Result{
			PWMPath:        targetPath,
			RPMCurve:       samplesCopy,
			Partial:        true,
			CompletedSteps: completedSteps,
			FanFingerprint: fingerprint,
			SweepMode:      SweepModeRPM,
		}
		// MinRPM / MaxRPM are the achievable extremes: first and last settled samples.
		for _, s := range samplesCopy {
			if s.Settled {
				if r.MinRPM == 0 || s.ActualRPM < r.MinRPM {
					r.MinRPM = s.ActualRPM
				}
				if s.ActualRPM > r.MaxRPM {
					r.MaxRPM = s.ActualRPM
				}
			}
		}
		return r
	}

	rangeSpan := maxRPMRange - minRPMRange
	for step := resumeStep; step < steps; step++ {
		if err := ctx.Err(); err != nil {
			return snapshot(step), err
		}
		frac := float64(step) / float64(steps-1)
		target := minRPMRange + int(frac*float64(rangeSpan))

		rs.mu.Lock()
		rs.progress = step * 100 / steps
		rs.mu.Unlock()

		if err := hwmon.WriteFanTarget(targetPath, target); err != nil {
			rs.mu.Lock()
			rs.errMsg = err.Error()
			rs.mu.Unlock()
			m.logger.Error("calibrate: write fan_target failed",
				"target_path", targetPath, "target_rpm", target, "err", err)
			return snapshot(step), err
		}

		// Settle loop: poll fan*_input every 500ms up to 5s for ±5% match.
		deadline := time.Now().Add(settleTimeout)
		var last int
		settled := false
		for time.Now().Before(deadline) {
			if ctxSleep(settlePoll) {
				return snapshot(step), ctx.Err()
			}
			if fan.RPMPath != "" {
				last, _ = hwmon.ReadRPMPath(fan.RPMPath)
			} else {
				last, _ = hwmon.ReadRPMPath(hwmon.RPMTargetInputPath(targetPath))
			}
			delta := last - target
			if delta < 0 {
				delta = -delta
			}
			// ±5% of target (guard target==0 with a fixed 50 RPM window).
			tolerance := target / 20
			if tolerance < 50 {
				tolerance = 50
			}
			if delta <= tolerance {
				settled = true
				break
			}
		}

		samples = append(samples, RPMTargetPoint{
			TargetRPM: target,
			ActualRPM: last,
			Settled:   settled,
		})
		m.logger.Debug("calibrate: rpm step",
			"target_path", targetPath, "target", target, "actual", last, "settled", settled)

		m.checkpoint(targetPath, snapshot(step+1))
	}

	final := snapshot(steps)
	final.Partial = false
	final.CompletedSteps = 0
	return final, nil
}

// markAborted transforms a partial result into a terminal aborted record.
// Called by run / RunSync after runSync returns with ctx.Err() != nil: the
// checkpoint that would otherwise be resumed on next startup is flipped to a
// terminal "aborted" state so the user's cancellation is honoured across
// daemon restarts.
func markAborted(r Result) Result {
	r.Partial = false
	r.Aborted = true
	r.CompletedSteps = 0
	r.DownRampPWM = 0
	return r
}

// DetectResult is returned by DetectRPMSensor.
type DetectResult struct {
	RPMPath string `json:"rpm_path"` // winning fan*_input sysfs path, empty if none found
	Delta   int    `json:"delta"`    // RPM change that identified it
}

// DetectRPMSensor identifies which fan*_input file in the same hwmon directory
// tracks the given PWM channel. It briefly ramps the PWM up (or down if already
// near max), waits for the fan to respond, and returns the path with the largest
// RPM delta. The controller yields during detection because IsCalibrating returns
// true for the duration.
//
// Blocks for ~5 seconds. Safe to call from an HTTP handler goroutine.
func (m *Manager) DetectRPMSensor(fan *config.Fan) (DetectResult, error) {
	if fan.Type == "nvidia" {
		return DetectResult{}, fmt.Errorf("detect: nvidia fans do not use hwmon RPM sensors")
	}

	pwmPath := fan.PWMPath

	// Acquire the lock slot so the controller yields.
	m.mu.Lock()
	if rs, ok := m.runs[pwmPath]; ok {
		rs.mu.Lock()
		already := rs.running
		rs.mu.Unlock()
		if already {
			m.mu.Unlock()
			return DetectResult{}, fmt.Errorf("detect: calibration already running for %s", pwmPath)
		}
	}
	rs := &runState{running: true, progress: 0}
	m.runs[pwmPath] = rs
	m.mu.Unlock()

	defer func() {
		rs.mu.Lock()
		rs.running = false
		rs.mu.Unlock()
	}()

	// Per-sweep watchdog wrap (DetectRPMSensor writes PWM too). Symmetric with
	// runSync — Register at start, Deregister on normal exit; the restore at
	// the bottom of this function keeps the captured PWM as the in-function
	// safety net.
	if m.wd != nil {
		m.wd.Register(pwmPath, fan.Type)
		defer m.wd.Deregister(pwmPath)
	}

	// Read current PWM so we can restore it.
	origPWM, err := hwmon.ReadPWM(pwmPath)
	if err != nil {
		return DetectResult{}, fmt.Errorf("detect: read current pwm: %w", err)
	}

	// Choose test PWM: ramp up by 60, or ramp down if already near max.
	// Guard against uint8 wrap.
	var testPWM uint8
	maxPWM := fan.MaxPWM
	if maxPWM == 0 {
		maxPWM = 255
	}
	const delta = 60
	if int(origPWM)+delta <= int(maxPWM) {
		testPWM = origPWM + delta
	} else if int(origPWM)-delta >= int(fan.MinPWM) {
		testPWM = origPWM - delta
	} else {
		// Range too narrow for a meaningful test; use the max instead.
		testPWM = maxPWM
	}

	// Always restore the original PWM when we're done.
	// Intentionally WritePWM (not WritePWMSafe): cleanup path. See the matching
	// comment in runSync — a safety-guarded restore that refuses on mode 0/2 is
	// worse than a best-effort one, because the fan would otherwise stay at the
	// test PWM until the next controller tick.
	defer func() { _ = hwmon.WritePWM(pwmPath, origPWM) }()

	_ = hwmon.WritePWMEnable(pwmPath, 1) // ignore — some drivers don't expose this

	// Find all fan*_input files in the same hwmon directory.
	dir := filepath.Dir(pwmPath)
	matches, _ := filepath.Glob(filepath.Join(dir, "fan*_input"))
	sort.Strings(matches)
	if len(matches) == 0 {
		return DetectResult{}, fmt.Errorf("detect: no fan*_input files in %s", dir)
	}

	// Baseline read.
	baseline := make(map[string]int, len(matches))
	for _, p := range matches {
		v, _ := hwmon.ReadRPMPath(p)
		baseline[p] = v
	}

	// Write test PWM and wait for the fan to settle.
	if err := hwmon.WritePWMSafe(pwmPath, testPWM); err != nil {
		return DetectResult{}, fmt.Errorf("detect: write test pwm: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Post-ramp read. Only count deltas in the expected direction — this filters
	// out ambient fluctuations from other fans on the same chip, which would
	// appear as random positive/negative noise regardless of our PWM change.
	ramped := testPWM > origPWM // true = we increased PWM, expect RPM to increase
	best, bestDelta := "", 0
	for _, p := range matches {
		v, _ := hwmon.ReadRPMPath(p)
		var d int
		if ramped {
			d = v - baseline[p] // positive when fan sped up
		} else {
			d = baseline[p] - v // positive when fan slowed down
		}
		m.logger.Debug("detect: rpm delta", "path", p, "before", baseline[p], "after", v, "delta", d, "ramped_up", ramped)
		if d > bestDelta {
			bestDelta = d
			best = p
		}
	}

	const minDelta = 50 // RPM — below this is noise
	if bestDelta < minDelta {
		m.logger.Info("detect: no sensor correlated", "pwm_path", pwmPath, "best_delta", bestDelta)
		return DetectResult{}, nil
	}

	m.logger.Info("detect: sensor identified", "pwm_path", pwmPath, "rpm_path", best, "delta", bestDelta)
	return DetectResult{RPMPath: best, Delta: bestDelta}, nil
}

func (m *Manager) load() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return // file may not exist yet
	}

	// Probe the envelope: a top-level "schema_version" key marks v1+; its
	// absence marks a legacy bare map. Sysfs path keys never collide with
	// the field name because they always start with "/sys/" or contain "/".
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		m.logger.Warn("calibrate: load results failed", "path", m.path, "err", err)
		return
	}

	if _, hasSchema := probe["schema_version"]; !hasSchema {
		// Legacy bare map → migrate in-memory; next save() rewrites as current envelope.
		var bare map[string]Result
		if err := json.Unmarshal(data, &bare); err != nil {
			m.logger.Warn("calibrate: load legacy results failed", "path", m.path, "err", err)
			return
		}
		for k, r := range bare {
			if r.SweepMode == "" {
				r.SweepMode = SweepModePWM
				bare[k] = r
			}
		}
		m.results = bare
		m.logger.Info("calibrate: migrated legacy bare map to current schema",
			"path", m.path, "fans", len(bare))
		return
	}

	var env onDiskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		m.logger.Warn("calibrate: load envelope failed", "path", m.path, "err", err)
		return
	}
	if env.SchemaVersion > SchemaVersion {
		// Future schema written by a newer daemon. Don't apply the data; surface
		// a diagnostic so the UI can prompt the user to recalibrate. The
		// controller falls back to the live config's MinPWM/MaxPWM as a safe
		// default because AllResults() now returns empty.
		affected := make([]string, 0, len(env.Results))
		for k := range env.Results {
			affected = append(affected, k)
		}
		sort.Strings(affected)
		entry := hwdiag.Entry{
			ID:        hwdiag.IDCalibrationFutureSchema,
			Component: hwdiag.ComponentCalibration,
			Severity:  hwdiag.SeverityWarn,
			Summary:   "Calibration data is from a newer version — recalibrate to apply",
			Detail: fmt.Sprintf(
				"calibration.json was written by a newer daemon (schema_version=%d, this build supports %d). Existing calibration is ignored; recalibrate to apply current data.",
				env.SchemaVersion, SchemaVersion),
			Remediation: &hwdiag.Remediation{
				AutoFixID: hwdiag.AutoFixRecalibrate,
				Label:     "Recalibrate",
				Endpoint:  "/api/setup/start",
			},
			Affected: affected,
		}
		m.diagnostics = append(m.diagnostics, entry)
		if m.store != nil {
			m.store.Set(entry)
		}
		m.logger.Warn("calibrate: future schema_version, results withheld",
			"found", env.SchemaVersion, "supported", SchemaVersion)
		return
	}
	// v1 → v2 migration: v1 records have no SweepMode field; treat them as PWM.
	// JSON unmarshals the missing field to "", so we just fill it in. RPMCurve
	// is already nil which is the correct v1 shape.
	for k, r := range env.Results {
		if r.SweepMode == "" {
			r.SweepMode = SweepModePWM
			env.Results[k] = r
		}
	}
	m.results = env.Results
}

func (m *Manager) save() {
	m.mu.Lock()
	env := onDiskEnvelope{
		SchemaVersion: SchemaVersion,
		Results:       m.results,
	}
	data, err := json.MarshalIndent(env, "", "  ")
	m.mu.Unlock()
	if err != nil {
		m.logger.Warn("calibrate: marshal results failed", "err", err)
		return
	}
	// Ensure the parent directory exists — on a fresh install or after
	// /etc/ventd is wiped, WriteFile would otherwise fail with ENOENT and the
	// checkpoint would silently vanish.
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.logger.Warn("calibrate: mkdir parent failed", "path", dir,
			"err", fmt.Errorf("mkdir %s: %w", dir, err))
		return
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		m.logger.Warn("calibrate: write tmp failed", "path", tmp,
			"err", fmt.Errorf("write %s: %w", tmp, err))
		return
	}
	if err := os.Rename(tmp, m.path); err != nil {
		m.logger.Warn("calibrate: rename tmp failed", "path", m.path,
			"err", fmt.Errorf("rename %s -> %s: %w", tmp, m.path, err))
	}
}

// checkpoint writes a single fan's partial result to disk atomically. Called
// from runSync after every PWM step so daemon restart can resume from the
// last completed step.
func (m *Manager) checkpoint(pwmPath string, partial Result) {
	m.mu.Lock()
	m.results[pwmPath] = partial
	m.mu.Unlock()
	m.save()
}
