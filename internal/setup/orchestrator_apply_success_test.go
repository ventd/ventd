package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// TestOnApplyPhaseSuccess_FiresReProberBeforeReload pins the #1268
// contract: after ApplyPhase emits Success the v0.8.x orchestrator
// bridge MUST re-run the daemon-level probe (afterFinalize) BEFORE it
// signals the reload trigger. Without this ordering the wizard's
// pre-install probe (typically "monitor_only" because drivers weren't
// loaded yet) stays cached in KV across the reload, every smart-mode
// subsystem remains inert despite controllers actively driving PWM on
// the newly-installed driver, and the dashboard's smart-mode pill is
// permanently "monitor-only" on a host that's in fact controlling fans.
func TestOnApplyPhaseSuccess_FiresReProberBeforeReload(t *testing.T) {
	m := newBridgeTestManager(t)

	// Track invocation order via an atomic counter incremented in both
	// hooks. The contract is that reprobe lands a lower value than
	// reload — i.e. reprobe ran first.
	var seq atomic.Int32
	var reprobeAt, reloadAt int32
	m.SetReProber(func(ctx context.Context) error {
		reprobeAt = seq.Add(1)
		return nil
	})
	m.SetReloadTrigger(func() {
		reloadAt = seq.Add(1)
	})

	// Stage a config the ApplyPhase artifact points at so the
	// onApplyPhaseSuccess back-read step has something to load.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\nweb:\n  listen: 127.0.0.1:0\n"), 0o600); err != nil {
		t.Fatalf("seed config.yaml: %v", err)
	}
	art := orchestrator.ApplyArtifact{ConfigPath: cfgPath}
	artBytes, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	out := orchestrator.Outcome{
		Phase:    (orchestrator.ApplyPhase{}).Name(),
		Status:   orchestrator.StatusSuccess,
		Artifact: artBytes,
	}

	m.onApplyPhaseSuccess(context.Background(), out, []orchestrator.Outcome{out})

	if reprobeAt == 0 {
		t.Fatalf("ReProber did not fire after onApplyPhaseSuccess; reload fired=%v", reloadAt != 0)
	}
	if reloadAt == 0 {
		t.Fatalf("Reload trigger did not fire after onApplyPhaseSuccess")
	}
	if reprobeAt >= reloadAt {
		t.Fatalf("ReProber must fire BEFORE reload (reprobe=%d, reload=%d) — otherwise the reload reads the stale wizard.initial_outcome from KV (#1268)",
			reprobeAt, reloadAt)
	}

	// Sanity: the manager transitioned to the applied state.
	m.mu.Lock()
	applied := m.applied
	phase := m.phase
	m.mu.Unlock()
	if !applied {
		t.Errorf("Manager.applied = false after onApplyPhaseSuccess; want true")
	}
	if phase != "applied" {
		t.Errorf("Manager.phase = %q; want %q", phase, "applied")
	}
}

// TestOnApplyPhaseSuccess_NilReProberStillReloads pins the fallback
// path: when the daemon hasn't wired SetReProber (legacy test
// scaffolding; production always wires it via cmd/ventd/main.go) the
// reload trigger must still fire so the controllers spawn against the
// freshly-applied config. The wizard.initial_outcome stays stale until
// the next probe trigger, which is the existing non-#1268 behaviour.
func TestOnApplyPhaseSuccess_NilReProberStillReloads(t *testing.T) {
	m := newBridgeTestManager(t)

	var reloadFired atomic.Int32
	m.SetReloadTrigger(func() {
		reloadFired.Add(1)
	})

	out := orchestrator.Outcome{
		Phase:    (orchestrator.ApplyPhase{}).Name(),
		Status:   orchestrator.StatusSuccess,
		Artifact: nil, // tolerate empty artifact — back-read just logs
	}

	m.onApplyPhaseSuccess(context.Background(), out, []orchestrator.Outcome{out})

	if reloadFired.Load() != 1 {
		t.Errorf("Reload trigger fired %d times; want 1 (nil ReProber must not block reload)", reloadFired.Load())
	}
}
