package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/nbfc"
)

// stubDMI returns a hwdb.DMI reader bound to a fixed tuple.
func stubDMI(d hwdb.DMI, err error) func() (hwdb.DMI, error) {
	return func() (hwdb.DMI, error) { return d, err }
}

// testNBFCCatalog returns a tiny in-memory catalogue covering one
// register-only, one ACPI, and one Lua entry — exercising the three
// severity / detail branches of the detector.
func testNBFCCatalog(t *testing.T) *nbfc.Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"configs/HP Pavilion 17-ab240nd.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"HP Pavilion 17-ab240nd","FanConfigurations":[{"ReadRegister":88,"WriteRegister":88}]}`),
		},
		"configs/HP 250 G8 Notebook PC.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"HP 250 G8 Notebook PC","FanConfigurations":[{"ReadAcpiMethod":"\\_SB.PCI0.SFNV","WriteAcpiMethod":"\\_SB.PCI0.SFNW"}]}`),
		},
		"configs/Synthetic Lua Box.json": &fstest.MapFile{
			Data: []byte(`{"NotebookModel":"Synthetic Lua Box","FanConfigurations":[{"WriteLuaCode":"return 0"}]}`),
		},
	}
	cat, err := nbfc.LoadCatalogFS(fsys, "configs")
	if err != nil {
		t.Fatalf("LoadCatalogFS: %v", err)
	}
	return cat
}

// RULE-NBFC-DOCTOR-01 — register-only match emits OK with the model
// name + "deferred to v0.8.0" framing.
func TestRULE_NBFC_DOCTOR_01_RegisterMatchEmitsOK(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "HP Pavilion 17-ab240nd"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK", f.Severity)
	}
	for _, want := range []string{"HP Pavilion 17-ab240nd", "register-only", "v0.8.0"} {
		if !strings.Contains(f.Title+f.Detail, want) {
			t.Errorf("Fact missing %q: title=%q detail=%q", want, f.Title, f.Detail)
		}
	}
}

// RULE-NBFC-DOCTOR-01 — ACPI-mode match emits OK and names the
// "requires acpi_call DKMS" deferral.
func TestRULE_NBFC_DOCTOR_01_ACPIMatchEmitsOKWithACPIDetail(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "HP 250 G8 Notebook PC"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "ACPI") {
		t.Errorf("Title doesn't name the ACPI mode: %q", facts[0].Title)
	}
	if !strings.Contains(facts[0].Detail, "acpi_call") {
		t.Errorf("Detail doesn't surface the acpi_call DKMS dependency: %q", facts[0].Detail)
	}
}

// RULE-NBFC-DOCTOR-01 — Lua-mode match emits Warning (refused in
// v0.8.0, slot reserved).
func TestRULE_NBFC_DOCTOR_01_LuaMatchEmitsWarning(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "Synthetic Lua Box"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning (Lua refused)", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Detail, "Lua") {
		t.Errorf("Detail doesn't name the Lua refusal: %q", facts[0].Detail)
	}
}

// RULE-NBFC-DOCTOR-01 — no match emits Warning with the upstream-
// contribution URL.
func TestRULE_NBFC_DOCTOR_01_NoMatchEmitsContributionInvite(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "Some Random Tablet 9000"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Detail, "nbfc-linux") {
		t.Errorf("Detail doesn't name the upstream repo: %q", facts[0].Detail)
	}
	if !strings.Contains(facts[0].Detail, "Some Random Tablet 9000") {
		t.Errorf("Detail doesn't echo the unmatched ProductName: %q", facts[0].Detail)
	}
}

// Detector skips silently on hosts with controllable channels —
// smart-mode applies via other detectors. RULE-NBFC-DOCTOR-01's
// quiet-when-irrelevant arm.
func TestNBFCMatch_DesktopWithChannelsNoFact(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 4,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "Anything"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected zero facts on a host with controllable channels; got %d", len(facts))
	}
}

// DMI read failure → Warning fact, no crash. RULE-DOCTOR-04 graceful
// degradation.
func TestNBFCMatch_DMIReadErrorGracefullyDegrades(t *testing.T) {
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{}, errors.New("synthetic /sys read error")),
		Catalog:                  testNBFCCatalog(t),
	}
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe should not return error; got %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 Warning fact on DMI read error; got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Detail, "synthetic /sys read error") {
		t.Errorf("Detail should wrap the underlying err: %q", facts[0].Detail)
	}
}

// Detector honours context cancellation.
func TestNBFCMatch_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	det := &NBFCMatchDetector{
		ControllableChannelCount: 0,
		ReadDMIFn:                stubDMI(hwdb.DMI{ProductName: "X"}, nil),
		Catalog:                  testNBFCCatalog(t),
	}
	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Error("expected ctx.Err() to propagate")
	}
}
