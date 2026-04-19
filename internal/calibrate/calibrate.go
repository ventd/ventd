// Package calibrate implements fan calibration: ramp PWM from min to max,
// record RPM at each step, determine start_pwm (lowest PWM that spins the fan)
// and max_rpm.
package calibrate

// Direct hwmon/nvidia call checklist (all must remain absent from this file):
//   - hwmon.WritePWM          → b.Write(ch, pwm)
//   - hwmon.WritePWMSafe      → b.Write(ch, pwm)
//   - hwmon.WritePWMEnable    → removed; b.Write ensureManualMode handles it
//   - hwmon.WritePWMEnablePath → removed; same
//   - hwmon.WriteFanTarget    → b.Write(ch, rpmToCalPWM(rpm, maxRPM))
//   - hwmon.ReadRPM           → b.Read(ch).RPM
//   - hwmon.ReadRPMPath       → readSysfsInt(path) or b.Read(ch).RPM
//   - hwmon.ReadPWM           → b.Read(ch).PWM (or readSysfsUint8 for detect)
//   - hwmon.ReadFanMaxRPM     → calReadFanMaxRPM(path)
//   - hwmon.ReadFanMinRPM     → calReadFanMinRPM(path)
//   - hwmon.RPMTargetEnablePath → calRPMTargetEnablePath(path) [unused after removal]
//   - hwmon.RPMTargetInputPath  → calRPMTargetInputPath(path) [unused after removal]
//   - nvidia.WriteFanSpeed    → b.Write(ch, pwm)
//   - nvidia.ResetFanSpeed    → b.Restore(ch)
//   - nvidia.ReadFanSpeed     → b.Read(ch).PWM (proxy for spinning)

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwdiag"
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

// ChannelResolver maps a config.Fan to the HAL backend and channel that
// calibration should use for that fan. Injected from main.go via
// SetChannelResolver; tests inject a local stub. When nil, RunSync / Start
// return an error immediately.
type ChannelResolver func(ctx context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error)

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
	resolver    ChannelResolver    // injected from main.go via SetChannelResolver
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

// SetChannelResolver wires up the HAL backend resolver. Must be called before
// any sweep is started. Production code passes a resolver that calls
// hal.Resolve; tests inject a stub.
func (m *Manager) SetChannelResolver(r ChannelResolver) {
	m.mu.Lock()
	m.resolver = r
	m.mu.Unlock()
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

	m.mu.Lock()
	resolver := m.resolver
	m.mu.Unlock()
	if resolver == nil {
		return Result{PWMPath: pwmPath}, fmt.Errorf("calibrate: no channel resolver set for %s", pwmPath)
	}
	b, ch, err := resolver(ctx, fan)
	if err != nil {
		return Result{PWMPath: pwmPath}, fmt.Errorf("calibrate: resolve channel %s: %w", pwmPath, err)
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
		_ = b.Write(ch, SafePWMFloor)
	})
	defer sentinel.Stop()

	// Safety: restore fan control when we exit, regardless of outcome.
	// The terminal state ("complete" | "error" | "aborted") is decided here so
	// callers polling Status see the right label after the goroutine exits.
	defer func() {
		if isNvidia {
			// NVML: hand control back to the driver's autonomous curve.
			_ = b.Restore(ch)
		} else {
			// Intentionally Write (not a safe-mode write): this is the cleanup path.
			// If pwm_enable has somehow flipped to 0/2 mid-sweep, a mode-checking
			// write would refuse and leave the fan at whatever duty the last forward
			// write set — the opposite of safe. The controller or watchdog will
			// reassert mode on the next tick; this write is just a best-effort
			// knock-down.
			_ = b.Write(ch, minPWM)
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

		writeErr := b.Write(ch, pwm)
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

		// For nvidia, Read returns PWM readback in the PWM field (not RPM) as a
		// proxy for "spinning". For hwmon, RPM field is the actual speed.
		var rpm int
		if isNvidia {
			r, _ := b.Read(ch)
			rpm = int(r.PWM) // PWM readback used as a proxy for "spinning"
		} else {
			// Sentinel-guarded read: rejects 0xFFFF and implausible values with
			// up to 3 retries before aborting the calibration with a clear error.
			var rpmErr error
			rpm, rpmErr = readCalRPMWithRetry(ctx, b, ch, fan.RPMPath, 3)
			if rpmErr != nil {
				m.logger.Error("calibrate: RPM sentinel persisted, aborting sweep",
					"pwm_path", pwmPath, "pwm", pwm, "err", rpmErr)
				return snapshot(0, step), rpmErr
			}
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
			if err := b.Write(ch, uint8(p)); err != nil {
				break
			}
			if ctxSleep(1500 * time.Millisecond) {
				return snapshot(uint8(p), steps+1), ctx.Err()
			}
			// Sentinel-guarded read: a sentinel during down-ramp would falsely
			// appear as high RPM and prevent stall detection. Retry up to 3x;
			// if all fail, log and break — the up-ramp result is still valid.
			rpm, _ := readCalRPMWithRetry(ctx, b, ch, fan.RPMPath, 3)
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

	m.mu.Lock()
	resolver := m.resolver
	m.mu.Unlock()
	if resolver == nil {
		return Result{PWMPath: targetPath}, fmt.Errorf("calibrate: no channel resolver set for %s", targetPath)
	}
	b, ch, err := resolver(ctx, fan)
	if err != nil {
		return Result{PWMPath: targetPath}, fmt.Errorf("calibrate: resolve channel %s: %w", targetPath, err)
	}

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
	// window if fan*_min is absent (calReadFanMinRPM returns 0) — we don't
	// want to spin at 0 RPM for more than a moment.
	minRPMRange := calReadFanMinRPM(targetPath)
	maxRPMRange := calReadFanMaxRPM(targetPath)
	if minRPMRange <= 0 {
		minRPMRange = maxRPMRange / 4 // safe floor: 25% of max
	}
	if maxRPMRange <= minRPMRange {
		maxRPMRange = minRPMRange + 100 // degenerate; will still emit a diagnostic below
	}

	defer func() {
		// On exit, write fan*_min as a safe floor via the backend. Watchdog handles
		// the pwm_enable restore on abnormal termination; this is the normal-exit
		// path where we leave the fan at a low but non-zero RPM.
		_ = b.Write(ch, rpmToCalPWM(minRPMRange, maxRPMRange))
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
		targetRPM := minRPMRange + int(frac*float64(rangeSpan))

		rs.mu.Lock()
		rs.progress = step * 100 / steps
		rs.mu.Unlock()

		// Scale the target RPM to 0-255 PWM for the HAL backend. The backend
		// converts it back via pwm/255 * maxRPM, so the round-trip is exact
		// (modulo integer rounding). The settle check uses targetRPM (not the
		// scaled PWM) so the tolerance arithmetic is in RPM units throughout.
		scaledPWM := rpmToCalPWM(targetRPM, maxRPMRange)
		if err := b.Write(ch, scaledPWM); err != nil {
			rs.mu.Lock()
			rs.errMsg = err.Error()
			rs.mu.Unlock()
			m.logger.Error("calibrate: write fan_target failed",
				"target_path", targetPath, "target_rpm", targetRPM, "err", err)
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
				last, _ = readSysfsInt(fan.RPMPath)
			} else {
				r, _ := b.Read(ch)
				last = int(r.RPM)
			}
			delta := last - targetRPM
			if delta < 0 {
				delta = -delta
			}
			// ±5% of target (guard target==0 with a fixed 50 RPM window).
			tolerance := targetRPM / 20
			if tolerance < 50 {
				tolerance = 50
			}
			if delta <= tolerance {
				settled = true
				break
			}
		}

		samples = append(samples, RPMTargetPoint{
			TargetRPM: targetRPM,
			ActualRPM: last,
			Settled:   settled,
		})
		m.logger.Debug("calibrate: rpm step",
			"target_path", targetPath, "target", targetRPM, "actual", last, "settled", settled)

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
	resolver := m.resolver
	rs := &runState{running: true, progress: 0}
	m.runs[pwmPath] = rs
	m.mu.Unlock()

	if resolver == nil {
		rs.mu.Lock()
		rs.running = false
		rs.mu.Unlock()
		return DetectResult{}, fmt.Errorf("detect: no channel resolver set for %s", pwmPath)
	}

	defer func() {
		rs.mu.Lock()
		rs.running = false
		rs.mu.Unlock()
	}()

	b, ch, err := resolver(context.Background(), fan)
	if err != nil {
		return DetectResult{}, fmt.Errorf("detect: resolve channel: %w", err)
	}

	// Per-sweep watchdog wrap (DetectRPMSensor writes PWM too). Symmetric with
	// runSync — Register at start, Deregister on normal exit; the restore at
	// the bottom of this function keeps the captured PWM as the in-function
	// safety net.
	if m.wd != nil {
		m.wd.Register(pwmPath, fan.Type)
		defer m.wd.Deregister(pwmPath)
	}

	// Read current PWM so we can restore it.
	origPWM, err := readSysfsUint8(pwmPath)
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
	// Intentionally Write (not a mode-checked write): cleanup path. See the
	// matching comment in runSync — a safety-guarded restore that refuses on
	// mode 0/2 is worse than a best-effort one, because the fan would otherwise
	// stay at the test PWM until the next controller tick.
	defer func() { _ = b.Write(ch, origPWM) }()

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
		v, _ := readSysfsInt(p)
		baseline[p] = v
	}

	// Write test PWM and wait for the fan to settle.
	if err := b.Write(ch, testPWM); err != nil {
		return DetectResult{}, fmt.Errorf("detect: write test pwm: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Post-ramp read. Only count deltas in the expected direction — this filters
	// out ambient fluctuations from other fans on the same chip, which would
	// appear as random positive/negative noise regardless of our PWM change.
	ramped := testPWM > origPWM // true = we increased PWM, expect RPM to increase
	best, bestDelta := "", 0
	for _, p := range matches {
		v, _ := readSysfsInt(p)
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
	// Marshal while holding the lock so the JSON encoder doesn't race with
	// concurrent map writes (e.g. checkpoint() or run() updating m.results).
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
	// /etc/ventd is wiped, atomicWriteBytes would otherwise fail with ENOENT and the
	// checkpoint would silently vanish.
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.logger.Warn("calibrate: mkdir parent failed", "path", dir,
			"err", fmt.Errorf("mkdir %s: %w", dir, err))
		return
	}
	if err := atomicWriteBytes(m.path, data); err != nil {
		m.logger.Warn("calibrate: persist failed", "path", m.path, "err", err)
	}
}

// atomicWriteJSON marshals data to JSON then writes it to path atomically.
// Intended for tests and callers that own their data exclusively; production
// code that reads from a shared map must marshal under the appropriate lock
// before calling atomicWriteBytes directly.
func atomicWriteJSON(path string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return atomicWriteBytes(path, b)
}

// atomicWriteBytes writes b to path atomically via a uniquely-named temp file
// and rename. Concurrent callers on the same path are safe: the last writer
// wins but no caller ever observes a partial or missing file, and no caller's
// error-path cleanup can delete another caller's in-flight tmp file.
func atomicWriteBytes(path string, b []byte) error {
	var suf [8]byte
	if _, err := rand.Read(suf[:]); err != nil {
		return fmt.Errorf("random suffix: %w", err)
	}
	tmp := path + ".tmp." + hex.EncodeToString(suf[:])

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	// Always remove tmp on exit; no-op if Rename already moved the file.
	defer os.Remove(tmp)

	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	// Best-effort directory fsync for rename durability across power loss.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
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

// ---- Private sysfs helpers ----
// These helpers perform read-only access to sysfs files. They are intentionally
// narrow: only RPM sensor reads and RPM-range queries for rpm_target fans. All
// hardware writes go through FanBackend.Write.

// readSysfsInt reads an integer from a sysfs file. Used for RPM sensor reads
// and rpm_target range queries; never mutates hardware state.
// readCalRPMWithRetry reads RPM from the given channel or explicit rpmPath,
// rejecting sentinel / implausible values. It retries up to maxRetry times
// (with a 500 ms sleep between attempts) before returning an error. This
// prevents a single glitching register from corrupting a calibration sample.
//
// For nvidia channels (no rpmPath, b.Read returns PWM as a spinning proxy),
// sentinel rejection does not apply — callers handle that path directly.
func readCalRPMWithRetry(ctx context.Context, b hal.FanBackend, ch hal.Channel, rpmPath string, maxRetry int) (int, error) {
	for attempt := 0; attempt <= maxRetry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		if rpmPath != "" {
			v, err := readSysfsInt(rpmPath)
			if err != nil {
				continue
			}
			if halhwmon.IsSentinelRPM(v) {
				continue
			}
			return v, nil
		}
		r, _ := b.Read(ch)
		if !r.OK {
			continue
		}
		return int(r.RPM), nil
	}
	target := rpmPath
	if target == "" {
		target = ch.ID
	}
	return 0, fmt.Errorf("calibrate: RPM sensor %q returned sentinel or invalid value %d time(s); check sensor wiring", target, maxRetry+1)
}

func readSysfsInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("calibrate: sysfs read %s: %w", path, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("calibrate: sysfs parse %s: %w", path, err)
	}
	return v, nil
}

// readSysfsUint8 reads a 0-255 integer from a sysfs file. Preserves the error
// semantics of hwmon.ReadPWM: values outside 0-255 are errors.
func readSysfsUint8(path string) (uint8, error) {
	v, err := readSysfsInt(path)
	if err != nil {
		return 0, err
	}
	if v < 0 || v > 255 {
		return 0, fmt.Errorf("calibrate: value %d at %s out of uint8 range", v, path)
	}
	return uint8(v), nil
}

// calReadFanMaxRPM reads the max RPM for a fan*_target channel from its
// companion fan*_max sysfs file. Returns 2000 when absent (matches the default
// in hwmon.ReadFanMaxRPM — a conservative estimate for AMD GPU fans).
func calReadFanMaxRPM(targetPath string) int {
	base := filepath.Base(targetPath)
	num := strings.TrimSuffix(strings.TrimPrefix(base, "fan"), "_target")
	maxPath := filepath.Join(filepath.Dir(targetPath), "fan"+num+"_max")
	v, err := readSysfsInt(maxPath)
	if err != nil || v <= 0 {
		return 2000
	}
	return v
}

// calReadFanMinRPM reads the min RPM for a fan*_target channel from its
// companion fan*_min sysfs file. Returns 0 when absent.
func calReadFanMinRPM(targetPath string) int {
	base := filepath.Base(targetPath)
	num := strings.TrimSuffix(strings.TrimPrefix(base, "fan"), "_target")
	minPath := filepath.Join(filepath.Dir(targetPath), "fan"+num+"_min")
	v, err := readSysfsInt(minPath)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// rpmToCalPWM converts an RPM value to the 0-255 PWM byte accepted by
// FanBackend.Write for an rpm_target channel. The backend reconstructs the RPM
// via pwm/255 * maxRPM, so this is the inverse. Clamps to [0, 255].
func rpmToCalPWM(rpm, maxRPM int) uint8 {
	if maxRPM <= 0 {
		return 0
	}
	v := int(math.Round(float64(rpm) / float64(maxRPM) * 255))
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
