package setup

import (
	"context"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// orchestratorEnvVar gates the v0.8.x phase-DAG executor.
//
// As of PR#B5, the orchestrator is the DEFAULT wizard path —
// orchestratorEnabled() returns true unless the env var is
// explicitly set to "0". The legacy Manager.run inline phase
// sequence runs only when VENTD_USE_ORCHESTRATOR=0 (emergency
// rollback escape hatch). The legacy code is retired in PR#B6.
const orchestratorEnvVar = "VENTD_USE_ORCHESTRATOR"

// orchestratorStateDir is the production location for orchestrator
// checkpoint + per-phase state. Overridable via VENTD_SETUP_STATE_DIR
// for HIL fixtures and integration tests.
const orchestratorStateDir = "/var/lib/ventd/setup"

// SetUseOrchestrator opts the Manager into the v0.8.x orchestrator
// path. Production main.go calls this with true so the orchestrator
// is the default wizard for installed daemons; tests leave it false
// so the legacy phase sequence runs unchanged. The env var
// VENTD_USE_ORCHESTRATOR=0 can be set by an operator to override
// back to legacy as an emergency rollback.
//
// Removed in PR#B6 along with the legacy phase code.
func (m *Manager) SetUseOrchestrator(v bool) {
	m.mu.Lock()
	m.useOrchestrator = v
	m.mu.Unlock()
}

// orchestratorEnabled returns true when this Manager instance opted
// into the orchestrator AND the operator has not explicitly opted out
// via VENTD_USE_ORCHESTRATOR=0.
func (m *Manager) orchestratorEnabled() bool {
	m.mu.Lock()
	on := m.useOrchestrator
	m.mu.Unlock()
	return on && os.Getenv(orchestratorEnvVar) != "0"
}

// orchestratorStateRoot returns the effective state directory: the
// VENTD_SETUP_STATE_DIR env var when set, otherwise orchestratorStateDir.
// Tests inject a t.TempDir() so the preview never writes to the
// production /var/lib/ventd path.
func orchestratorStateRoot() string {
	if s := os.Getenv("VENTD_SETUP_STATE_DIR"); s != "" {
		return s
	}
	return orchestratorStateDir
}

// managerEventSink adapts Manager.EmitEvent to the orchestrator's
// EventSink interface so orchestrator phases surface activity-feed
// lines through the same SSE ring buffer the legacy phases use.
//
// In addition to the activity-feed pipe, it intercepts the
// orchestrator's "starting phase" / "phase completed" markers and
// maps them onto Manager.phase / phase_msg so the web UI's
// /api/setup/status polling shows progress. Without this hook the
// orchestrator-driven wizard runs to completion correctly but the
// UI status panel stays empty (operator sees "stuck wizard" while
// phases are actually progressing).
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
// terse label the web UI shows in the progress indicator. Mirrors
// the legacy Manager.run setPhase calls so the UI reads identical
// status regardless of which path drove the wizard.
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
// narrower Calibrator interface. Single-sources the production
// calibration engine with the legacy Manager.run Phase 6 path —
// both call the same calibrate.Manager.RunSync underneath.
type managerCalibrator struct{ m *Manager }

func (c managerCalibrator) Calibrate(ctx context.Context, fan *config.Fan) (calibrate.Result, error) {
	if c.m == nil || c.m.cal == nil {
		return calibrate.Result{}, fmt.Errorf("orchestrator: no CalibrationBackend wired on Manager")
	}
	return c.m.cal.RunSync(ctx, fan)
}

// managerRPMDetector adapts the Manager's CalibrationBackend (which
// implements DetectRPMSensor) to the orchestrator's narrower
// RPMDetector interface. Single-sources the production heuristic
// with the legacy Manager.run Phase 5 — both call the same
// calibrate.Manager.DetectRPMSensor underneath.
type managerRPMDetector struct{ m *Manager }

func (r managerRPMDetector) Detect(fan *config.Fan) (calibrate.DetectResult, error) {
	if r.m == nil || r.m.cal == nil {
		return calibrate.DetectResult{}, fmt.Errorf("orchestrator: no CalibrationBackend wired on Manager")
	}
	return r.m.cal.DetectRPMSensor(fan)
}

// runOrchestratorPreview executes the orchestrator phase set ahead of
// the legacy Manager.run body. As of PR#B5 it is the DEFAULT wizard
// path; the legacy sequence runs only when the orchestrator fails or
// when VENTD_USE_ORCHESTRATOR=0 (emergency rollback).
//
// Return contract (used by Manager.run to decide whether to short-
// circuit the legacy phases):
//
//	applied=true,  err=nil:   ApplyPhase succeeded; wizard is done
//	applied=false, err=nil:   ApplyPhase did NOT succeed (a prior phase
//	                          failed or the artifact is missing); the
//	                          legacy fallback runs
//	applied=false, err!=nil:  orchestrator bootstrap failed (e.g. mkdir
//	                          state dir); the legacy fallback runs
//
// The legacy fallback is the v0.8.x safety net. PR#B6 removes it
// once HIL coverage proves the orchestrator handles every supported
// hardware shape.
func (m *Manager) runOrchestratorPreview(ctx context.Context) (applied bool, err error) {
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
		orchestrator.VerifyPhase{},
		orchestrator.ApplyPhase{},
	)
	if oerr != nil {
		return false, fmt.Errorf("orchestrator preview: %w", oerr)
	}
	outs, runErr := o.Run(ctx)
	if runErr != nil {
		return false, fmt.Errorf("orchestrator preview run: %w", runErr)
	}
	// Wizard is "applied" only when ApplyPhase ran AND succeeded.
	// Any earlier-phase failure short-circuits Run, so an ApplyPhase
	// success outcome is the unambiguous signal we can short-circuit
	// the legacy fallback.
	for _, out := range outs {
		if out.Phase == (orchestrator.ApplyPhase{}).Name() &&
			out.Status == orchestrator.StatusSuccess {
			return true, nil
		}
	}
	return false, nil
}
