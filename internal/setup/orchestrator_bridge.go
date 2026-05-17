package setup

import (
	"context"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// orchestratorEnvVar gates the v0.8.x phase-DAG executor. While set,
// Manager.run invokes runOrchestratorPreview before the legacy phase
// sequence so we can exercise the new framework on HIL hosts without
// risking a regression to the production wizard path.
//
// When all legacy phases have been migrated (target: v0.8.1 PR#B2),
// this gate flips default-on and the legacy code is deleted.
const orchestratorEnvVar = "VENTD_USE_ORCHESTRATOR"

// orchestratorStateDir is the production location for orchestrator
// checkpoint + per-phase state. Overridable via VENTD_SETUP_STATE_DIR
// for HIL fixtures and integration tests.
const orchestratorStateDir = "/var/lib/ventd/setup"

// orchestratorEnabled returns true when the preview gate is set. Pulled
// out as a helper so tests can stub via t.Setenv without re-implementing
// the trim/equality logic.
func orchestratorEnabled() bool {
	return os.Getenv(orchestratorEnvVar) == "1"
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
type managerEventSink struct{ m *Manager }

func (s managerEventSink) Emit(level, tag, text string) {
	if s.m == nil {
		return
	}
	s.m.EmitEvent(level, tag, text)
}

// runOrchestratorPreview executes the orchestrator phase set ahead of
// the legacy Manager.run body. Currently registers only the Inventory
// phase — a read-only DMI + hwmon scan whose result lands in
// /var/lib/ventd/setup/state.json. The legacy phase sequence runs
// afterwards regardless of orchestrator outcome; the preview's role
// is to prove the framework in the field, not to replace the wizard.
//
// Returns a non-nil error only when the orchestrator itself fails to
// bootstrap (e.g. cannot create the state directory). A failing
// preview phase is logged via the orchestrator's own event sink and
// returns nil so the legacy path still runs.
func (m *Manager) runOrchestratorPreview(ctx context.Context) error {
	rc := &orchestrator.RunContext{
		Logger:    m.logger,
		HwmonRoot: m.hwmonRoot,
		ProcRoot:  m.procRoot,
		StateDir:  orchestratorStateRoot(),
		Events:    managerEventSink{m: m},
	}
	o, err := orchestrator.New(rc,
		orchestrator.InventoryPhase{},
		orchestrator.ConflictHuntPhase{AutoStop: true, AutoStopVendor: false},
		orchestrator.DriverPlanPhase{},
		orchestrator.DriverInstallPhase{},
		orchestrator.ProbePhase{},
		orchestrator.PolarityPhase{},
		orchestrator.ApplyPhase{},
	)
	if err != nil {
		return fmt.Errorf("orchestrator preview: %w", err)
	}
	if _, err := o.Run(ctx); err != nil {
		return fmt.Errorf("orchestrator preview run: %w", err)
	}
	return nil
}
