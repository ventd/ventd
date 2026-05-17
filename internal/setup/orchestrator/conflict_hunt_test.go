package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/setup/conflicts"
)

type fakeRunner struct {
	active  map[string]struct{}
	enabled map[string]struct{}
}

func (f *fakeRunner) IsActive(_ context.Context, unit string) (bool, error) {
	_, ok := f.active[unit]
	return ok, nil
}

func (f *fakeRunner) IsEnabled(_ context.Context, unit string) (bool, error) {
	_, ok := f.enabled[unit]
	return ok, nil
}

func newRunner(active, enabled []string) *fakeRunner {
	a := make(map[string]struct{}, len(active))
	for _, u := range active {
		a[u] = struct{}{}
	}
	e := make(map[string]struct{}, len(enabled))
	for _, u := range enabled {
		e[u] = struct{}{}
	}
	return &fakeRunner{active: a, enabled: e}
}

// stageProcForConflictHunt builds a tiny /proc tree with one matching process.
func stageProcForConflictHunt(t *testing.T, comm, cmdline string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proc")
	pid := strconv.Itoa(1234)
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConflictHuntPhase_NoConflictsYieldsSuccess(t *testing.T) {
	rc := &RunContext{
		StateDir: t.TempDir(),
		ProcRoot: stageProcForConflictHunt(t, "bash", "/bin/bash"),
	}
	phase := ConflictHuntPhase{Runner: newRunner(nil, nil)}
	out := phase.Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("expected Success, got %q (detail=%q)", out.Status, out.Detail)
	}
	var art ConflictHuntArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Conflicts) != 0 {
		t.Errorf("happy path should report 0 conflicts, got %d", len(art.Conflicts))
	}
}

func TestConflictHuntPhase_ConflictFoundYieldsFailedWithVendorClass(t *testing.T) {
	// Use the production registry — fancontrol unit active should
	// surface. We don't need to mock the whole registry; the runner
	// only returns true for the unit we name.
	rc := &RunContext{
		StateDir: t.TempDir(),
		ProcRoot: stageProcForConflictHunt(t, "bash", "/bin/bash"),
	}
	phase := ConflictHuntPhase{Runner: newRunner([]string{"fancontrol.service"}, nil)}
	out := phase.Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Fatalf("expected Failed when a competitor is active, got %q", out.Status)
	}
	if out.Class != recovery.ClassVendorDaemonActive {
		t.Errorf("Class = %q, want ClassVendorDaemonActive", out.Class)
	}
	var art ConflictHuntArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Conflicts) == 0 {
		t.Error("artifact should carry the conflict list for the recovery card")
	}
}

func TestConflictHuntPhase_AutoStopRespectsEnvGate(t *testing.T) {
	// With AutoStop:false (the web-UI default), even with the env var
	// set, no auto-stop happens.
	t.Setenv("VENTD_AUTO_STOP_CONFLICTS", "yes")
	rc := &RunContext{
		StateDir: t.TempDir(),
		ProcRoot: stageProcForConflictHunt(t, "bash", "/bin/bash"),
	}
	phase := ConflictHuntPhase{
		Runner:   newRunner([]string{"fancontrol.service"}, nil),
		AutoStop: false, // web UI default
	}
	out := phase.Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Fatalf("AutoStop:false should NOT auto-stop; expected Failed, got %q", out.Status)
	}
	var art ConflictHuntArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.AutoStopped) > 0 {
		t.Errorf("AutoStop:false should not produce AutoStopped entries, got %v", art.AutoStopped)
	}
}

func TestConflictHuntPhase_NameIsStable(t *testing.T) {
	if (ConflictHuntPhase{}).Name() != "conflict_hunt" {
		t.Error("Name() must be 'conflict_hunt' (used as checkpoint key + UI route segment)")
	}
}

func TestConflictHuntPhase_PhaseRunsThroughOrchestrator(t *testing.T) {
	// End-to-end: orchestrator runs Inventory then ConflictHunt; both
	// land in state.json; the phase failure does not corrupt the prior
	// success outcome.
	rc := &RunContext{StateDir: t.TempDir(), ProcRoot: stageProcForConflictHunt(t, "bash", "/bin/bash")}
	o, err := New(rc,
		InventoryPhase{},
		ConflictHuntPhase{Runner: newRunner([]string{"fancontrol.service"}, nil)},
	)
	if err != nil {
		t.Fatal(err)
	}
	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outs))
	}
	if outs[0].Status != StatusSuccess || outs[0].Phase != "inventory" {
		t.Errorf("outs[0]: phase=%q status=%q (want inventory/Success)", outs[0].Phase, outs[0].Status)
	}
	if outs[1].Status != StatusFailed || outs[1].Phase != "conflict_hunt" {
		t.Errorf("outs[1]: phase=%q status=%q (want conflict_hunt/Failed)", outs[1].Phase, outs[1].Status)
	}
	if outs[1].Class != recovery.ClassVendorDaemonActive {
		t.Errorf("outs[1].Class = %q, want ClassVendorDaemonActive", outs[1].Class)
	}
}

// Smoke: ensure the conflicts.Conflict struct round-trips through
// JSON in the orchestrator's checkpoint without losing the regex
// pointers (which can't marshal — registry deliberately strips them
// at marshal time via Entry having ProcPatterns as unmarshalable).
func TestConflictHuntPhase_ArtifactJSONRoundTrip(t *testing.T) {
	art := ConflictHuntArtifact{
		Conflicts: []conflicts.Conflict{
			{
				Entry: conflicts.Entry{
					Name:           "fancontrol",
					Description:    "lm-sensors",
					Intrusiveness:  conflicts.IntrusivenessLow,
					ConflictReason: "races on hwmon PWM",
					// ProcPatterns deliberately omitted — regex pointers
					// don't marshal. Production-side this is OK because the
					// wizard UI reads the human-facing fields, not the
					// detection regexes.
					ProcPatterns: []*regexp.Regexp{regexp.MustCompile(`^fancontrol$`)},
				},
				UnitsActive: []string{"fancontrol.service"},
			},
		},
	}
	raw, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ConflictHuntArtifact
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Conflicts) != 1 || back.Conflicts[0].Entry.Name != "fancontrol" {
		t.Errorf("round-trip lost data: %+v", back)
	}
}
