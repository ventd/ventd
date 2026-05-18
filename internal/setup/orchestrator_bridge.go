package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// orchestratorStateDir is the production location for orchestrator
// checkpoint + per-phase state. Overridable via VENTD_SETUP_STATE_DIR
// for HIL fixtures and integration tests.
const orchestratorStateDir = "/var/lib/ventd/setup"

// orchestratorStateRoot returns the effective state directory: the
// VENTD_SETUP_STATE_DIR env var when set, otherwise orchestratorStateDir.
// Tests inject a t.TempDir() so the orchestrator never writes to the
// production /var/lib/ventd path.
func orchestratorStateRoot() string {
	if s := os.Getenv("VENTD_SETUP_STATE_DIR"); s != "" {
		return s
	}
	return orchestratorStateDir
}

// managerEventSink adapts Manager.EmitEvent to the orchestrator's
// EventSink interface so orchestrator phases surface activity-feed
// lines through the same SSE ring buffer.
//
// In addition to the activity-feed pipe, it intercepts the
// orchestrator's "starting phase" markers and maps them onto
// Manager.phase / phase_msg so the web UI's /api/setup/status polling
// shows progress. Without this hook the wizard runs to completion
// correctly but the UI status panel stays empty.
type managerEventSink struct{ m *Manager }

func (s managerEventSink) Emit(level, tag, text string) {
	if s.m == nil {
		return
	}
	if text == "starting phase" {
		s.m.setPhase(orchestratorPhaseLabel(tag), orchestratorPhaseMsg(tag))
	}
	s.m.EmitEvent(level, tag, text)
}

// orchestratorPhaseLabel maps an orchestrator phase Name() to the
// terse label the web UI shows in the progress indicator.
func orchestratorPhaseLabel(phase string) string {
	switch phase {
	case "inventory":
		return "detecting"
	case "conflict_hunt":
		return "detecting_conflicts"
	case "driver_plan", "driver_install":
		return "installing_driver"
	case "nvml":
		return "detecting_gpu"
	case "probe":
		return "scanning_fans"
	case "rpm_detect":
		return "detecting_rpm"
	case "polarity":
		return "probing_polarity"
	case "calibrate":
		return "calibrating"
	case "verify":
		return "verifying"
	case "apply":
		return "finalising"
	default:
		return phase
	}
}

// orchestratorPhaseMsg is the user-facing phase_msg the web UI
// renders alongside the phase label.
func orchestratorPhaseMsg(phase string) string {
	switch phase {
	case "inventory":
		return "Scanning your system for hardware..."
	case "conflict_hunt":
		return "Checking for competing fan-control daemons..."
	case "driver_plan":
		return "Identifying which fan controller driver this board needs..."
	case "driver_install":
		return "Installing the fan controller driver — this may take a minute..."
	case "nvml":
		return "Looking for NVIDIA GPU fans..."
	case "probe":
		return "Enumerating controllable fans..."
	case "rpm_detect":
		return "Detecting RPM tach sensors..."
	case "polarity":
		return "Classifying fan polarity (normal/inverted/phantom)..."
	case "calibrate":
		return "Calibrating each fan — this takes several minutes..."
	case "verify":
		return "Verifying fans respond to full-speed PWM..."
	case "apply":
		return "Writing configuration..."
	default:
		return phase + " in progress..."
	}
}

// managerCalibrator adapts the Manager's CalibrationBackend (which
// has RunSync, AllStatus, DetectRPMSensor) to the orchestrator's
// narrower Calibrator interface.
type managerCalibrator struct{ m *Manager }

func (c managerCalibrator) Calibrate(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
	if c.m == nil || c.m.cal == nil {
		return calibrate.Result{}, fmt.Errorf("orchestrator: no CalibrationBackend wired on Manager")
	}
	return c.m.cal.RunSync(ctx, fan)
}

// managerRPMDetector adapts the Manager's CalibrationBackend (which
// implements DetectRPMSensor) to the orchestrator's narrower
// RPMDetector interface.
type managerRPMDetector struct{ m *Manager }

func (r managerRPMDetector) Detect(fan *config.Fan) (calibrate.DetectResult, error) {
	if r.m == nil || r.m.cal == nil {
		return calibrate.DetectResult{}, fmt.Errorf("orchestrator: no CalibrationBackend wired on Manager")
	}
	return r.m.cal.DetectRPMSensor(fan)
}

// runOrchestrator runs the full v0.8.x phase set. On ApplyPhase success
// the wizard transitions to the "applied" state; on any phase failure
// (or orchestrator bootstrap failure) m.errMsg + m.failureClass are
// populated from the failing outcome so the recovery card surfaces the
// right remediation.
//
// This replaces the legacy Manager.run inline phase 0-7 sequence; the
// orchestrator is now the only wizard path.
func (m *Manager) runOrchestrator(ctx context.Context) {
	rc := &orchestrator.RunContext{
		Logger:    m.logger,
		HwmonRoot: m.hwmonRoot,
		ProcRoot:  m.procRoot,
		StateDir:  orchestratorStateRoot(),
		Events:    managerEventSink{m: m},
	}
	o, oerr := orchestrator.New(rc,
		orchestrator.InventoryPhase{},
		orchestrator.ConflictHuntPhase{AutoStop: true, AutoStopVendor: false},
		orchestrator.DriverPlanPhase{},
		orchestrator.DriverInstallPhase{},
		orchestrator.NVMLPhase{},
		orchestrator.ProbePhase{},
		orchestrator.RPMDetectPhase{Detector: managerRPMDetector{m: m}},
		orchestrator.PolarityPhase{},
		orchestrator.CalibratePhase{Calibrator: managerCalibrator{m: m}},
		orchestrator.ApplyPhase{},
	)
	if oerr != nil {
		m.mu.Lock()
		m.errMsg = fmt.Sprintf("orchestrator bootstrap failed: %v", oerr)
		m.failureClass = recovery.ClassUnknown
		m.phase = "failed"
		m.phaseMsg = "Setup could not start. Send a diagnostic bundle so we can investigate."
		m.mu.Unlock()
		return
	}
	outs, runErr := o.Run(ctx)
	if runErr != nil {
		m.mu.Lock()
		m.errMsg = fmt.Sprintf("orchestrator run failed: %v", runErr)
		m.failureClass = recovery.ClassUnknown
		m.phase = "failed"
		m.phaseMsg = "Setup crashed unexpectedly. Send a diagnostic bundle so we can investigate."
		m.mu.Unlock()
		return
	}
	// ApplyPhase Success is the unambiguous "wizard is applied" signal.
	// Any earlier-phase failure short-circuits Run; o.Run returns the
	// outcomes-so-far with the failing one last.
	for _, out := range outs {
		if out.Phase == (orchestrator.ApplyPhase{}).Name() &&
			out.Status == orchestrator.StatusSuccess {
			m.persistOrchestratorPolarity(outs)
			m.mu.Lock()
			m.phase = "applied"
			m.phaseMsg = "Wizard complete"
			m.applied = true
			m.mu.Unlock()
			return
		}
	}
	// Wizard failed before ApplyPhase succeeded. The last outcome
	// carries the recovery class + operator-facing detail that the
	// failing phase populated; surface them so the web UI's recovery
	// card has the right shape.
	if len(outs) == 0 {
		m.mu.Lock()
		m.errMsg = "orchestrator returned no outcomes"
		m.failureClass = recovery.ClassUnknown
		m.phase = "failed"
		m.phaseMsg = "Setup produced no result. Send a diagnostic bundle so we can investigate."
		m.mu.Unlock()
		return
	}
	last := outs[len(outs)-1]
	detail := last.Detail
	if detail == "" {
		detail = fmt.Sprintf("phase %q ended with status %q", last.Phase, last.Status)
	}
	cls := last.Class
	if cls == "" {
		cls = recovery.ClassUnknown
	}
	m.mu.Lock()
	m.errMsg = detail
	m.failureClass = cls
	// Don't overwrite phase here — managerEventSink already set it to
	// the phase that was running when failure landed; that's what the
	// recovery card classifier wants to see.
	m.mu.Unlock()
}

// persistOrchestratorPolarity bridges the wizard's PolarityArtifact
// checkpoint to the runtime polarity KV store (RULE-POLARITY-08).
// Called immediately before the wizard transitions to "applied".
//
// Without this call the daemon on next restart sees no persisted
// polarity, every channel reads Polarity="unknown",
// controller.RestoreCtx refuses every write, and smart-mode reports
// enabled=false / channels=0 despite a successful wizard run. The
// legacy Manager.run Phase 5b made this call inline; that path was
// removed in #1197 without the orchestrator picking up the
// responsibility. (#1222.)
//
// Best-effort: a Persist failure does NOT roll back the wizard's
// applied state. The user has a working config; losing the polarity
// shard means the next restart will need a re-probe at startup,
// which is recoverable without operator intervention.
func (m *Manager) persistOrchestratorPolarity(outs []orchestrator.Outcome) {
	if m.stateKV == nil {
		m.logger.Warn("polarity persist skipped: no KVDB wired on Manager")
		return
	}

	var polArt *orchestrator.PolarityArtifact
	var probeArt *orchestrator.ProbeArtifact
	for i := range outs {
		out := &outs[i]
		if out.Status != orchestrator.StatusSuccess || len(out.Artifact) == 0 {
			continue
		}
		switch out.Phase {
		case (orchestrator.PolarityPhase{}).Name():
			var a orchestrator.PolarityArtifact
			if err := json.Unmarshal(out.Artifact, &a); err != nil {
				m.logger.Warn("polarity persist: decode PolarityArtifact failed", "err", err)
				continue
			}
			polArt = &a
		case (orchestrator.ProbePhase{}).Name():
			var a orchestrator.ProbeArtifact
			if err := json.Unmarshal(out.Artifact, &a); err != nil {
				m.logger.Warn("polarity persist: decode ProbeArtifact failed", "err", err)
				continue
			}
			probeArt = &a
		}
	}

	if polArt == nil || len(polArt.Results) == 0 {
		return
	}

	// PWMPath → RPMPath lookup from the probe artifact. Tach path
	// is not part of the polarity hwmon match key
	// (MatchKey = "hwmon:<PWMPath>") but the runtime polarity
	// package surfaces TachPath in diagnostics; populate when known.
	tachByPWM := map[string]string{}
	if probeArt != nil {
		for _, f := range probeArt.Fans {
			tachByPWM[f.PWMPath] = f.RPMPath
		}
	}

	now := time.Now()
	results := make([]polarity.ChannelResult, 0, len(polArt.Results))
	for _, r := range polArt.Results {
		if r.Polarity == "" || r.Polarity == "unknown" {
			// Don't persist unresolved entries — Load treats them as
			// resolved and would block the daemon's re-probe path.
			continue
		}
		results = append(results, polarity.ChannelResult{
			Backend: "hwmon",
			Identity: polarity.Identity{
				PWMPath:  r.PWMPath,
				TachPath: tachByPWM[r.PWMPath],
			},
			Polarity:      r.Polarity,
			PhantomReason: r.PhantomReason,
			Baseline:      r.Baseline,
			Observed:      r.Observed,
			Delta:         r.Delta,
			Unit:          r.Unit,
			ProbedAt:      now,
		})
	}

	if len(results) == 0 {
		return
	}

	if err := polarity.Persist(m.stateKV, results); err != nil {
		m.logger.Warn("polarity persist: KVDB write failed",
			"err", err, "channels", len(results))
		return
	}
	m.logger.Info("polarity persisted to KV store", "channels", len(results))
}
