package setup

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// TestOrchestratorBridge_RunWritesCheckpoint exercises the bridge
// directly: runOrchestrator drives the full phase set against a
// minimal hwmon fixture and the inventory outcome lands in state.json
// so a future resume can pick up where the wizard left off.
func TestOrchestratorBridge_RunWritesCheckpoint(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("VENTD_SETUP_STATE_DIR", stateDir)

	// Stage a minimal /sys/class/hwmon fixture under another temp.
	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonRoot, "name"), []byte("coretemp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newBridgeTestManager(t)
	m.hwmonRoot = filepath.Dir(hwmonRoot) // = .../sys/class/hwmon

	m.runOrchestrator(context.Background())

	statePath := filepath.Join(stateDir, "state.json")
	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("expected state.json at %s: %v", statePath, err)
	}

	var st orchestrator.State
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("parse state.json: %v", err)
	}
	inv, ok := st.Outcomes["inventory"]
	if !ok {
		t.Fatalf("inventory outcome missing from state.json; got keys %v", outcomeKeys(st))
	}
	if inv.Status != orchestrator.StatusSuccess {
		t.Errorf("inventory Status = %q, want Success", inv.Status)
	}
}

// TestOrchestratorBridge_EventsFlowToManagerRing confirms phases
// emit through Manager.EmitEvent so the SSE ring buffer picks them up
// (no separate channel plumbing required).
func TestOrchestratorBridge_EventsFlowToManagerRing(t *testing.T) {
	t.Setenv("VENTD_SETUP_STATE_DIR", t.TempDir())

	hwmonRoot := filepath.Join(t.TempDir(), "sys", "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonRoot, "name"), []byte("coretemp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newBridgeTestManager(t)
	m.hwmonRoot = filepath.Dir(hwmonRoot)

	m.runOrchestrator(context.Background())

	events, _ := m.EventsSince(0)
	if len(events) == 0 {
		t.Fatal("expected at least one orchestrator event in the ring buffer, got 0")
	}
	// Look for the inventory phase's "starting phase" + "phase completed" pair.
	var sawInventoryStart, sawInventoryDone bool
	for _, e := range events {
		if e.Tag == "inventory" {
			if e.Text == "starting phase" {
				sawInventoryStart = true
			}
			if e.Text == "phase completed" {
				sawInventoryDone = true
			}
		}
	}
	if !sawInventoryStart || !sawInventoryDone {
		t.Errorf("expected inventory start+complete events; got events=%+v", events)
	}
}

// TestSetReloadTrigger_FireRoundTrip pins the SetReloadTrigger /
// fireReloadTrigger contract: a registered callback fires exactly once
// per invocation; a nil callback (no SetReloadTrigger call) is a clean
// no-op. The bridge's call site (runOrchestrator on ApplyPhase Success)
// is wired in orchestrator_bridge.go via m.fireReloadTrigger(); the
// thermal-safety contract (#1229 / #1232) hinges on this dispatch
// firing exactly once per wizard run.
func TestSetReloadTrigger_FireRoundTrip(t *testing.T) {
	m := newBridgeTestManager(t)

	// nil callback before SetReloadTrigger → no-op, no panic.
	m.fireReloadTrigger()

	var fired int
	m.SetReloadTrigger(func() { fired++ })
	m.fireReloadTrigger()
	if fired != 1 {
		t.Errorf("after SetReloadTrigger, fireReloadTrigger() expected 1 invocation; got %d", fired)
	}
	m.fireReloadTrigger()
	if fired != 2 {
		t.Errorf("second fireReloadTrigger() expected 2 invocations total; got %d", fired)
	}

	// Re-setting to nil disables the hook cleanly.
	m.SetReloadTrigger(nil)
	m.fireReloadTrigger()
	if fired != 2 {
		t.Errorf("after SetReloadTrigger(nil), fireReloadTrigger() must be a no-op; got %d", fired)
	}
}

// outcomeKeys lists the keys present in a State.Outcomes map (for
// readable test failure messages).
func outcomeKeys(st orchestrator.State) []string {
	keys := make([]string, 0, len(st.Outcomes))
	for k := range st.Outcomes {
		keys = append(keys, k)
	}
	return keys
}

// newBridgeTestManager builds a Manager with the minimum wiring needed
// for the bridge tests: a stub CalibrationBackend (unused by orchestrator
// for inventory) and a discard logger. ApplyPhase's writeConfigAtomic
// target is redirected to a per-test temp directory so a root-owned dev
// host (e.g. the homelab proxmox box that ventd is developed on) never
// has its live /etc/ventd/config.yaml stomped by a unit-test run.
func newBridgeTestManager(t *testing.T) *Manager {
	t.Helper()
	cb := calibrate.New(t.TempDir(), slog.Default(), nil)
	m := New(cb, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// Override roots so the test never reads /sys or /proc.
	m.hwmonRoot = filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	m.procRoot = filepath.Join(t.TempDir(), "proc")
	// Isolate ApplyPhase's write target. Without this, a root-uid test
	// run lands writeConfigAtomic on /etc/ventd/config.yaml and any
	// already-applied operator config gets clobbered.
	m.SetApplyConfigPathOverride(filepath.Join(t.TempDir(), "config.yaml"))
	return m
}
