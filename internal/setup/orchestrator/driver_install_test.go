package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/recovery"
)

// fakeInstaller is the test seam for the production hwmon.InstallDriver
// pipeline. Each Install call is recorded so tests can assert what was
// attempted and in what order; the per-key map drives the return value.
type fakeInstaller struct {
	results  map[string]error // key → error to return (nil = success)
	attempts []string         // ordered keys tried, for assertions
}

func (f *fakeInstaller) Install(chipKey string, logFn func(string)) error {
	f.attempts = append(f.attempts, chipKey)
	logFn("fake install begin " + chipKey)
	if f.results == nil {
		return nil
	}
	err, ok := f.results[chipKey]
	if !ok {
		return errors.New("fakeInstaller: no result configured for " + chipKey)
	}
	return err
}

// seedDriverPlanCheckpoint writes a DriverPlanArtifact under the state
// dir so the DriverInstall phase has something to consume.
func seedDriverPlanCheckpoint(t *testing.T, rc *RunContext, art DriverPlanArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(DriverPlanPhase{}).Name()] = Outcome{
		Phase:    (DriverPlanPhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

func TestDriverInstallPhase_NameStable(t *testing.T) {
	if (DriverInstallPhase{}).Name() != "driver_install" {
		t.Error("Name() must be 'driver_install'")
	}
}

func TestDriverInstallPhase_ReadyPlanSkipsAndSucceeds(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedDriverPlanCheckpoint(t, rc, DriverPlanArtifact{Status: DriverPlanReady, PWMCount: 4})

	inst := &fakeInstaller{}
	out := (DriverInstallPhase{Installer: inst}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Errorf("ready plan should yield Success, got %q", out.Status)
	}
	if len(inst.attempts) != 0 {
		t.Errorf("ready plan should not invoke installer; attempts=%v", inst.attempts)
	}
}

func TestDriverInstallPhase_NoMatchPlanFailsWithDriverWontBind(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedDriverPlanCheckpoint(t, rc, DriverPlanArtifact{Status: DriverPlanNoMatch})

	out := (DriverInstallPhase{Installer: &fakeInstaller{}}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("no-match plan should yield Failed, got %q", out.Status)
	}
	if out.Class != recovery.ClassDriverWontBind {
		t.Errorf("no-match Class = %q, want ClassDriverWontBind", out.Class)
	}
}

func TestDriverInstallPhase_FirstCandidateSucceedsStopsLoop(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	plan := DriverPlanArtifact{
		Status: DriverPlanNeedsInstall,
		Needs: []hwmon.DriverNeed{
			{Key: "it8688e", ChipName: "IT8688E"},
			{Key: "nct6687d", ChipName: "NCT6687D"},
		},
	}
	seedDriverPlanCheckpoint(t, rc, plan)

	inst := &fakeInstaller{results: map[string]error{
		"it8688e":  nil,
		"nct6687d": errors.New("should not be reached"),
	}}
	out := (DriverInstallPhase{Installer: inst}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("expected Success on first-candidate-succeeds, got %q (detail=%q)", out.Status, out.Detail)
	}
	if len(inst.attempts) != 1 || inst.attempts[0] != "it8688e" {
		t.Errorf("expected loop to stop after first success; attempts=%v", inst.attempts)
	}

	var art DriverInstallArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.InstalledKey != "it8688e" {
		t.Errorf("InstalledKey = %q, want it8688e", art.InstalledKey)
	}
}

func TestDriverInstallPhase_TriesSecondCandidateWhenFirstFails(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	plan := DriverPlanArtifact{
		Status: DriverPlanNeedsInstall,
		Needs: []hwmon.DriverNeed{
			{Key: "it8688e", ChipName: "IT8688E"},
			{Key: "nct6687d", ChipName: "NCT6687D"},
		},
	}
	seedDriverPlanCheckpoint(t, rc, plan)

	inst := &fakeInstaller{results: map[string]error{
		"it8688e":  errors.New("chip mismatch — no PWM channels appeared"),
		"nct6687d": nil,
	}}
	out := (DriverInstallPhase{Installer: inst}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("expected Success after second candidate succeeded, got %q (detail=%q)", out.Status, out.Detail)
	}
	if len(inst.attempts) != 2 {
		t.Errorf("expected both candidates tried; attempts=%v", inst.attempts)
	}

	var art DriverInstallArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.InstalledKey != "nct6687d" {
		t.Errorf("InstalledKey = %q, want nct6687d", art.InstalledKey)
	}
	if len(art.Attempts) != 2 {
		t.Errorf("Attempts log len = %d, want 2", len(art.Attempts))
	}
	if art.Attempts[0].Succeeded != false || art.Attempts[1].Succeeded != true {
		t.Errorf("Attempts succeeded mask wrong: %+v", art.Attempts)
	}
}

func TestDriverInstallPhase_AllCandidatesFailedYieldsFailed(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	plan := DriverPlanArtifact{
		Status: DriverPlanNeedsInstall,
		Needs: []hwmon.DriverNeed{
			{Key: "a", ChipName: "A"},
			{Key: "b", ChipName: "B"},
		},
	}
	seedDriverPlanCheckpoint(t, rc, plan)

	inst := &fakeInstaller{results: map[string]error{
		"a": errors.New("compile failed"),
		"b": errors.New("compile failed"),
	}}
	out := (DriverInstallPhase{Installer: inst}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("expected Failed when all candidates fail, got %q", out.Status)
	}
	// Class should be classified or fall back to ClassDriverWontBind.
	if out.Class == recovery.ClassUnknown {
		t.Errorf("Class should not be Unknown when all candidates fail")
	}
}

func TestDriverInstallPhase_ErrRebootRequiredIsTerminal(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	plan := DriverPlanArtifact{
		Status: DriverPlanNeedsInstall,
		Needs: []hwmon.DriverNeed{
			{Key: "a", ChipName: "A"},
			{Key: "b", ChipName: "B"}, // should NOT be tried
		},
	}
	seedDriverPlanCheckpoint(t, rc, plan)

	rebootErr := &hwmon.ErrRebootRequired{Message: "added acpi_enforce_resources=lax — reboot to apply"}
	inst := &fakeInstaller{results: map[string]error{
		"a": rebootErr,
	}}
	out := (DriverInstallPhase{Installer: inst}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Fatalf("ErrRebootRequired should yield Failed, got %q", out.Status)
	}
	if out.Class != recovery.ClassACPIResourceConflict {
		t.Errorf("Class = %q, want ClassACPIResourceConflict", out.Class)
	}
	if len(inst.attempts) != 1 {
		t.Errorf("ErrRebootRequired must stop the loop; attempts=%v", inst.attempts)
	}

	var art DriverInstallArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if !art.RebootRequired {
		t.Error("artifact.RebootRequired should be true")
	}
	if art.RebootMessage == "" {
		t.Error("artifact.RebootMessage should carry the operator-facing detail")
	}
}

func TestDriverInstallPhase_MissingPriorPlanFails(t *testing.T) {
	// No prior DriverPlan checkpoint → the phase can't proceed.
	rc := &RunContext{StateDir: t.TempDir()}
	out := (DriverInstallPhase{Installer: &fakeInstaller{}}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("missing prior plan should yield Failed, got %q", out.Status)
	}
	if out.Class != recovery.ClassUnknown {
		t.Errorf("missing prior plan Class = %q, want ClassUnknown", out.Class)
	}
}

func TestDriverPlanPhase_NameStable(t *testing.T) {
	if (DriverPlanPhase{}).Name() != "driver_plan" {
		t.Error("Name() must be 'driver_plan'")
	}
}
