package setup

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/setup/orchestrator"
)

// TestSynthesiseOrchestratorFans_BridgesArtifactsToFanState pins the
// #1230 fix: the wizard UI's fan roster + system cards must populate
// during the multi-minute calibrate window. The orchestrator never
// writes Manager.fans directly (the legacy phase 0-7 inline body did);
// instead Progress() falls back to reading the orchestrator's
// checkpoint state and synthesising FanState entries from
// ProbeArtifact + PolarityArtifact + CalibrateArtifact.
func TestSynthesiseOrchestratorFans_BridgesArtifactsToFanState(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("VENTD_SETUP_STATE_DIR", stateDir)

	store := orchestrator.NewCheckpointStore(stateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load empty checkpoint store: %v", err)
	}

	probeArt := orchestrator.ProbeArtifact{
		Fans: []orchestrator.ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", RPMPath: "/sys/hwmon0/fan1_input", ChipName: "nct6687", LabelHint: "Cpu Fan"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", RPMPath: "/sys/hwmon0/fan2_input", ChipName: "nct6687", LabelHint: "Case Fan"},
			{Index: 3, PWMPath: "/sys/hwmon0/pwm3", RPMPath: "/sys/hwmon0/fan3_input", ChipName: "nct6687", LabelHint: "Pump"},
		},
	}
	probeRaw, _ := json.Marshal(probeArt)
	state.Outcomes[(orchestrator.ProbePhase{}).Name()] = orchestrator.Outcome{
		Phase:    (orchestrator.ProbePhase{}).Name(),
		Status:   orchestrator.StatusSuccess,
		Artifact: probeRaw,
	}

	polArt := orchestrator.PolarityArtifact{
		Results: []orchestrator.PolarityFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", Polarity: "normal"},
			{PWMPath: "/sys/hwmon0/pwm2", Polarity: "phantom", PhantomReason: "no_response"},
			// pwm3 omitted — polarity not yet resolved (still probing)
		},
	}
	polRaw, _ := json.Marshal(polArt)
	state.Outcomes[(orchestrator.PolarityPhase{}).Name()] = orchestrator.Outcome{
		Phase:    (orchestrator.PolarityPhase{}).Name(),
		Status:   orchestrator.StatusSuccess,
		Artifact: polRaw,
	}

	calArt := orchestrator.CalibrateArtifact{
		Results: []orchestrator.CalibrateFanResult{
			{PWMPath: "/sys/hwmon0/pwm1", StartPWM: 50, MaxRPM: 2400, SweepMode: "pwm"},
			{PWMPath: "/sys/hwmon0/pwm2", StartPWM: 0, MaxRPM: 0, SkippedWhy: "polarity=phantom — fan does not spin under PWM control"},
			// pwm3 omitted — calibrate not yet completed (in progress or pending)
		},
	}
	calRaw, _ := json.Marshal(calArt)
	state.Outcomes[(orchestrator.CalibratePhase{}).Name()] = orchestrator.Outcome{
		Phase:    (orchestrator.CalibratePhase{}).Name(),
		Status:   orchestrator.StatusSuccess,
		Artifact: calRaw,
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	fans := synthesiseOrchestratorFans()
	if len(fans) != 3 {
		t.Fatalf("synthesised %d fans, want 3", len(fans))
	}
	byPath := map[string]FanState{}
	for _, f := range fans {
		byPath[f.PWMPath] = f
	}

	// pwm1: normal polarity, calibrated → done.
	cpu := byPath["/sys/hwmon0/pwm1"]
	if cpu.Name != "Cpu Fan" || cpu.RPMPath != "/sys/hwmon0/fan1_input" {
		t.Errorf("pwm1: identity wrong: %+v", cpu)
	}
	if cpu.DetectPhase != "found" || cpu.PolarityPhase != "normal" || cpu.CalPhase != "done" {
		t.Errorf("pwm1: phases wrong: detect=%q polarity=%q cal=%q",
			cpu.DetectPhase, cpu.PolarityPhase, cpu.CalPhase)
	}
	if cpu.StartPWM != 50 || cpu.MaxRPM != 2400 {
		t.Errorf("pwm1: curve params lost: StartPWM=%d MaxRPM=%d", cpu.StartPWM, cpu.MaxRPM)
	}

	// pwm2: phantom polarity, calibrate skipped → skipped + error reason.
	cas := byPath["/sys/hwmon0/pwm2"]
	if cas.PolarityPhase != "phantom" || cas.CalPhase != "skipped" {
		t.Errorf("pwm2: phantom phases wrong: polarity=%q cal=%q",
			cas.PolarityPhase, cas.CalPhase)
	}
	if cas.Error == "" {
		t.Errorf("pwm2: phantom skip reason missing: %+v", cas)
	}

	// pwm3: polarity + calibrate not yet resolved → pending.
	pump := byPath["/sys/hwmon0/pwm3"]
	if pump.PolarityPhase != "pending" || pump.CalPhase != "pending" {
		t.Errorf("pwm3: pending phases wrong: polarity=%q cal=%q",
			pump.PolarityPhase, pump.CalPhase)
	}
}

// TestSynthesiseOrchestratorFans_EmptyWhenNoProbeArtifact pins the
// graceful-degrade path: a Manager that has not yet run the
// orchestrator (or has been reset) returns nil so Progress.Fans
// stays at its zero value and the wizard UI falls back to its
// pre-roster placeholder state.
func TestSynthesiseOrchestratorFans_EmptyWhenNoProbeArtifact(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("VENTD_SETUP_STATE_DIR", stateDir)
	// No state.json on disk → store.Load returns an empty State; nil
	// return matches the contract.
	if got := synthesiseOrchestratorFans(); got != nil {
		t.Errorf("expected nil for empty state dir, got %d fans", len(got))
	}

	// Now write a state.json with no probe outcome. Same empty result.
	store := orchestrator.NewCheckpointStore(stateDir)
	state, _ := store.Load()
	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := synthesiseOrchestratorFans(); got != nil {
		t.Errorf("expected nil when probe outcome missing, got %d fans", len(got))
	}

	// Make sure the env var actually pointed somewhere — sanity check
	// the file got written.
	if _, err := store.Load(); err != nil {
		t.Fatalf("state file unreadable in temp dir %s: %v",
			filepath.Join(stateDir, "state.json"), err)
	}
}
