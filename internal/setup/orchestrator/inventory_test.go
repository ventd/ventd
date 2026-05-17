package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a t.Helper for staging fixture files.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stageInventoryFixture builds a /sys-like tree under tmp with the
// given chip name, controllable PWM count, and temp-input count. Also
// stages DMI and /proc/sys/kernel/osrelease.
func stageInventoryFixture(t *testing.T, tmp string, chip string, pwms, temps int, vendor, board, kernel string) (hwmonRoot, procRoot string) {
	t.Helper()
	hwmonRoot = filepath.Join(tmp, "sys", "class", "hwmon")
	procRoot = filepath.Join(tmp, "proc")
	hwmonDir := filepath.Join(hwmonRoot, "hwmon0")

	writeFile(t, filepath.Join(hwmonDir, "name"), chip+"\n")
	for i := 1; i <= pwms; i++ {
		writeFile(t, filepath.Join(hwmonDir, "pwm"+itoa(i)), "128\n")
		writeFile(t, filepath.Join(hwmonDir, "pwm"+itoa(i)+"_enable"), "1\n")
	}
	for i := 1; i <= temps; i++ {
		writeFile(t, filepath.Join(hwmonDir, "temp"+itoa(i)+"_input"), "42000\n")
	}
	writeFile(t, filepath.Join(tmp, "sys", "devices", "virtual", "dmi", "id", "board_vendor"), vendor+"\n")
	writeFile(t, filepath.Join(tmp, "sys", "devices", "virtual", "dmi", "id", "board_name"), board+"\n")
	writeFile(t, filepath.Join(procRoot, "sys", "kernel", "osrelease"), kernel+"\n")
	return hwmonRoot, procRoot
}

// itoa avoids the strconv import in tests for single-digit indices.
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+(i/10))) + string(rune('0'+(i%10)))
}

func TestInventoryPhase_Name(t *testing.T) {
	if (InventoryPhase{}).Name() != "inventory" {
		t.Error("Name should be 'inventory'")
	}
}

func TestInventoryPhase_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	hwmonRoot, procRoot := stageInventoryFixture(t, tmp, "nct6687", 3, 5, "Micro-Star International Co., Ltd.", "MAG Z690 TOMAHAWK", "7.0.2-2-pve")

	rc := &RunContext{HwmonRoot: hwmonRoot, ProcRoot: procRoot, Events: noopSink{}}
	out := (InventoryPhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("Status = %q, want Success; detail=%q", out.Status, out.Detail)
	}

	var art InventoryArtifact
	if err := json.Unmarshal(out.Artifact, &art); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	if art.BoardVendor != "Micro-Star International Co., Ltd." {
		t.Errorf("BoardVendor = %q", art.BoardVendor)
	}
	if art.BoardName != "MAG Z690 TOMAHAWK" {
		t.Errorf("BoardName = %q", art.BoardName)
	}
	if len(art.HwmonDevices) != 1 || art.HwmonDevices[0] != "nct6687" {
		t.Errorf("HwmonDevices = %v, want [nct6687]", art.HwmonDevices)
	}
	if art.PWMChannels != 3 {
		t.Errorf("PWMChannels = %d, want 3", art.PWMChannels)
	}
	if art.TempChannels != 5 {
		t.Errorf("TempChannels = %d, want 5", art.TempChannels)
	}
	if art.KernelRelease != "7.0.2-2-pve" {
		t.Errorf("KernelRelease = %q", art.KernelRelease)
	}
}

func TestInventoryPhase_PWMWithoutEnableIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	hwmonRoot := filepath.Join(tmp, "sys", "class", "hwmon")
	hwmonDir := filepath.Join(hwmonRoot, "hwmon1")
	writeFile(t, filepath.Join(hwmonDir, "name"), "nct6683\n")
	// pwm1 with no _enable — read-only monitoring value; must not count.
	writeFile(t, filepath.Join(hwmonDir, "pwm1"), "128\n")
	// pwm2 with _enable — controllable; counts.
	writeFile(t, filepath.Join(hwmonDir, "pwm2"), "128\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm2_enable"), "1\n")

	rc := &RunContext{HwmonRoot: hwmonRoot}
	out := (InventoryPhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("Status = %q, detail=%q", out.Status, out.Detail)
	}
	var art InventoryArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.PWMChannels != 1 {
		t.Errorf("PWMChannels = %d, want 1 (uncontrollable pwm1 must be skipped)", art.PWMChannels)
	}
}

func TestInventoryPhase_EmptyHwmonTreeStillSucceeds(t *testing.T) {
	tmp := t.TempDir()
	hwmonRoot := filepath.Join(tmp, "sys", "class", "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	rc := &RunContext{HwmonRoot: hwmonRoot}
	out := (InventoryPhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Errorf("empty hwmon tree should succeed (DriverPlan resolves it later); got %q detail=%q",
			out.Status, out.Detail)
	}
	var art InventoryArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.PWMChannels != 0 || len(art.HwmonDevices) != 0 {
		t.Errorf("expected empty inventory, got %+v", art)
	}
}

func TestInventoryPhase_PWMSubfieldsNotMiscounted(t *testing.T) {
	// pwm1_mode, pwm1_min, pwm1_max etc. must not be counted as PWM channels.
	tmp := t.TempDir()
	hwmonRoot := filepath.Join(tmp, "sys", "class", "hwmon")
	hwmonDir := filepath.Join(hwmonRoot, "hwmon0")
	writeFile(t, filepath.Join(hwmonDir, "name"), "nct6687\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm1"), "128\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm1_enable"), "1\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm1_mode"), "1\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm1_min"), "0\n")
	writeFile(t, filepath.Join(hwmonDir, "pwm1_max"), "255\n")

	rc := &RunContext{HwmonRoot: hwmonRoot}
	out := (InventoryPhase{}).Execute(context.Background(), rc)
	var art InventoryArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.PWMChannels != 1 {
		t.Errorf("PWMChannels = %d, want 1 (subfields must not count)", art.PWMChannels)
	}
}

func TestInventoryPhase_MissingDMIIsTolerated(t *testing.T) {
	tmp := t.TempDir()
	hwmonRoot := filepath.Join(tmp, "sys", "class", "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// No DMI files staged.
	rc := &RunContext{HwmonRoot: hwmonRoot}
	out := (InventoryPhase{}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Errorf("missing DMI should not fail Inventory; got %q", out.Status)
	}
	var art InventoryArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if art.BoardVendor != "" || art.BoardName != "" {
		t.Errorf("missing DMI fields should be empty strings, got vendor=%q name=%q",
			art.BoardVendor, art.BoardName)
	}
}
