package setup

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/setup/orchestrator"
	"github.com/ventd/ventd/internal/state"
)

// TestPersistOrchestratorPolarity_WritesKVStore is the regression guard for
// #1222. The wizard orchestrator computes polarity results into its phase
// checkpoint, but the runtime polarity package reads from the KV store at
// daemon start. Without the bridge call this test exercises, the wizard
// completes "successfully" yet every controller refuses every write on the
// first restart because polarity.Load() returns nothing.
func TestPersistOrchestratorPolarity_WritesKVStore(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	st, err := state.Open(dir, logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	m := &Manager{logger: logger, stateKV: st.KV}

	probeArt := orchestrator.ProbeArtifact{
		Fans: []orchestrator.ProbedFan{
			{Index: 1, PWMPath: "/sys/class/hwmon/hwmon1/pwm1", RPMPath: "/sys/class/hwmon/hwmon1/fan1_input"},
			{Index: 2, PWMPath: "/sys/class/hwmon/hwmon1/pwm2", RPMPath: "/sys/class/hwmon/hwmon1/fan2_input"},
		},
	}
	polArt := orchestrator.PolarityArtifact{
		Results: []orchestrator.PolarityFanResult{
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm1", Polarity: "normal", Baseline: 100, Observed: 1500, Delta: 1400, Unit: "rpm"},
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm2", Polarity: "phantom", PhantomReason: "rpm_delta_below_threshold", Unit: "rpm"},
		},
	}
	probeRaw, err := json.Marshal(probeArt)
	if err != nil {
		t.Fatalf("marshal probe: %v", err)
	}
	polRaw, err := json.Marshal(polArt)
	if err != nil {
		t.Fatalf("marshal polarity: %v", err)
	}

	outs := []orchestrator.Outcome{
		{Phase: (orchestrator.ProbePhase{}).Name(), Status: orchestrator.StatusSuccess, Artifact: probeRaw},
		{Phase: (orchestrator.PolarityPhase{}).Name(), Status: orchestrator.StatusSuccess, Artifact: polRaw},
		{Phase: (orchestrator.ApplyPhase{}).Name(), Status: orchestrator.StatusSuccess},
	}

	m.persistOrchestratorPolarity(outs)

	store, err := polarity.Load(st.KV)
	if err != nil {
		t.Fatalf("polarity.Load: %v", err)
	}
	if store == nil {
		t.Fatal("polarity store is nil after persist — bridge did not write")
	}
	if got, want := len(store.Channels), 2; got != want {
		t.Fatalf("persisted channels: got %d, want %d", got, want)
	}
	byPath := map[string]polarity.ChannelResult{}
	for _, c := range store.Channels {
		byPath[c.Identity.PWMPath] = c
	}
	pwm1, ok := byPath["/sys/class/hwmon/hwmon1/pwm1"]
	if !ok {
		t.Fatal("pwm1 not persisted")
	}
	if pwm1.Polarity != "normal" {
		t.Errorf("pwm1 polarity: got %q, want normal", pwm1.Polarity)
	}
	if pwm1.Identity.TachPath != "/sys/class/hwmon/hwmon1/fan1_input" {
		t.Errorf("pwm1 tach: got %q, want /sys/class/hwmon/hwmon1/fan1_input", pwm1.Identity.TachPath)
	}
	if pwm1.Backend != "hwmon" {
		t.Errorf("pwm1 backend: got %q, want hwmon", pwm1.Backend)
	}
	pwm2, ok := byPath["/sys/class/hwmon/hwmon1/pwm2"]
	if !ok {
		t.Fatal("pwm2 not persisted")
	}
	if pwm2.Polarity != "phantom" {
		t.Errorf("pwm2 polarity: got %q, want phantom", pwm2.Polarity)
	}
	if pwm2.PhantomReason == "" {
		t.Error("pwm2 phantom_reason was dropped")
	}
}

// TestPersistOrchestratorPolarity_SkipsUnknownEntries pins the contract that
// Polarity=="unknown" entries are NOT persisted — the runtime polarity
// package treats resolved-to-unknown as a probe miss that blocks re-probe,
// so we keep those out of the KV store entirely.
func TestPersistOrchestratorPolarity_SkipsUnknownEntries(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	st, err := state.Open(dir, logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	m := &Manager{logger: logger, stateKV: st.KV}

	polArt := orchestrator.PolarityArtifact{
		Results: []orchestrator.PolarityFanResult{
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm1", Polarity: "normal", Unit: "rpm"},
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm2", Polarity: "unknown", ProbeError: "settle timeout"},
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm3", Polarity: ""},
		},
	}
	polRaw, _ := json.Marshal(polArt)
	outs := []orchestrator.Outcome{
		{Phase: (orchestrator.PolarityPhase{}).Name(), Status: orchestrator.StatusSuccess, Artifact: polRaw},
	}

	m.persistOrchestratorPolarity(outs)

	store, err := polarity.Load(st.KV)
	if err != nil {
		t.Fatalf("polarity.Load: %v", err)
	}
	if store == nil {
		t.Fatal("polarity store is nil after persist")
	}
	if got, want := len(store.Channels), 1; got != want {
		t.Fatalf("persisted channels: got %d, want %d (only the resolved entry should land)", got, want)
	}
	if store.Channels[0].Identity.PWMPath != "/sys/class/hwmon/hwmon1/pwm1" {
		t.Errorf("wrong channel persisted: %+v", store.Channels[0])
	}
}

// TestPersistOrchestratorPolarity_NoKVDBIsNonFatal pins the safety: a
// Manager constructed without a KVDB handle must not crash the wizard. The
// wizard logs a warning and proceeds — the apply has already succeeded, the
// user has a working config, only the cross-restart guarantee is lost.
func TestPersistOrchestratorPolarity_NoKVDBIsNonFatal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := &Manager{logger: logger}
	polArt := orchestrator.PolarityArtifact{
		Results: []orchestrator.PolarityFanResult{
			{PWMPath: "/sys/class/hwmon/hwmon1/pwm1", Polarity: "normal"},
		},
	}
	polRaw, _ := json.Marshal(polArt)
	outs := []orchestrator.Outcome{
		{Phase: (orchestrator.PolarityPhase{}).Name(), Status: orchestrator.StatusSuccess, Artifact: polRaw},
	}
	// Must not panic. No assertions on the KV — there isn't one.
	m.persistOrchestratorPolarity(outs)
}
