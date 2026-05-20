package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func stageSensorFixture(t *testing.T, root, chipName string, temps map[int]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "name"), []byte(chipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for idx, label := range temps {
		base := "temp" + itoaInt(idx)
		if err := os.WriteFile(filepath.Join(root, base+"_input"), []byte("45000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Write a deterministic _crit so curve-gen tests aren't
		// host-dependent — sysclass.TjmaxFromCPUInfo() returns
		// different values depending on the test runner's CPU model.
		// Use 100°C (Intel coretemp default) so the per-fan curve's
		// MaxTemp lands at 90°C across all platforms.
		if err := os.WriteFile(filepath.Join(root, base+"_crit"), []byte("100000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if label != "" {
			if err := os.WriteFile(filepath.Join(root, base+"_label"), []byte(label+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDiscoverCPUSensor_CoretempPackageWins(t *testing.T) {
	root := t.TempDir()
	stageSensorFixture(t, filepath.Join(root, "hwmon0"), "coretemp",
		map[int]string{1: "Package id 0", 2: "Core 0"})
	got := DiscoverCPUSensor(root)
	if got.Path == "" {
		t.Fatal("expected a sensor, got none")
	}
	if got.ChipName != "coretemp" {
		t.Errorf("ChipName=%q, want coretemp", got.ChipName)
	}
}

func TestDiscoverCPUSensor_K10TempTctlWins(t *testing.T) {
	root := t.TempDir()
	stageSensorFixture(t, filepath.Join(root, "hwmon0"), "k10temp",
		map[int]string{1: "Tctl", 2: "Tdie"})
	got := DiscoverCPUSensor(root)
	if got.Path == "" || got.ChipName != "k10temp" {
		t.Errorf("k10temp not picked; got %+v", got)
	}
}

func TestDiscoverCPUSensor_AcpitzFallback(t *testing.T) {
	root := t.TempDir()
	stageSensorFixture(t, filepath.Join(root, "hwmon0"), "acpitz", map[int]string{1: ""})
	got := DiscoverCPUSensor(root)
	if got.Path == "" || got.ChipName != "acpitz" {
		t.Errorf("acpitz fallback should pick up; got %+v", got)
	}
}

func TestDiscoverCPUSensor_GPUChipsIgnored(t *testing.T) {
	root := t.TempDir()
	stageSensorFixture(t, filepath.Join(root, "hwmon0"), "amdgpu", map[int]string{1: "edge"})
	got := DiscoverCPUSensor(root)
	if got.Path != "" {
		t.Errorf("GPU-only host should produce no CPU sensor; got %+v", got)
	}
}

func TestDiscoverCPUSensor_EmptyHostReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	if got := DiscoverCPUSensor(root); got.Path != "" {
		t.Errorf("empty host should yield empty sensor; got %+v", got)
	}
}
