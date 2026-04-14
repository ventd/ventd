package hwmon

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a test helper that creates parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mkHwmonDir creates root/hwmonN with a name file. Returns the dir path.
func mkHwmonDir(t *testing.T, root, name, chip string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, "name"), chip+"\n")
	return dir
}

func TestEnumerateDevices_CapabilityMatrix(t *testing.T) {
	root := t.TempDir()

	// hwmon0 — temperature-only (ClassNoFans).
	temp := mkHwmonDir(t, root, "hwmon0", "coretemp")
	writeFile(t, filepath.Join(temp, "temp1_input"), "45000\n")

	// hwmon1 — fan RPM readable but no controllable PWM (ClassReadOnly).
	// Models nct6683 loaded against an NCT6687D chip: fanN_input present,
	// no pwmN_enable.
	ro := mkHwmonDir(t, root, "hwmon1", "nct6683")
	writeFile(t, filepath.Join(ro, "fan1_input"), "1200\n")
	writeFile(t, filepath.Join(ro, "pwm1"), "128\n") // read-only PWM, no _enable

	// hwmon2 — controllable PWM, no fan RPM input (ClassOpenLoop).
	ol := mkHwmonDir(t, root, "hwmon2", "gpu_openloop")
	writeFile(t, filepath.Join(ol, "pwm1"), "128\n")
	writeFile(t, filepath.Join(ol, "pwm1_enable"), "1\n")

	// hwmon3 — full controllable PWM + RPM readback (ClassPrimary).
	pri := mkHwmonDir(t, root, "hwmon3", "nct6687")
	writeFile(t, filepath.Join(pri, "pwm1"), "128\n")
	writeFile(t, filepath.Join(pri, "pwm1_enable"), "1\n")
	writeFile(t, filepath.Join(pri, "fan1_input"), "1500\n")
	writeFile(t, filepath.Join(pri, "pwm2"), "200\n")
	writeFile(t, filepath.Join(pri, "pwm2_enable"), "1\n")
	writeFile(t, filepath.Join(pri, "fan2_input"), "2000\n")
	writeFile(t, filepath.Join(pri, "temp1_input"), "42000\n")

	// hwmon4 — nvidia GPU, skipped regardless of contents (ClassSkipNVIDIA).
	nv := mkHwmonDir(t, root, "hwmon4", "nvidia")
	writeFile(t, filepath.Join(nv, "pwm1"), "128\n")
	writeFile(t, filepath.Join(nv, "pwm1_enable"), "1\n")
	writeFile(t, filepath.Join(nv, "fan1_input"), "1800\n")

	// hwmon5 — RPM-target only (pre-RDNA AMD shape): fanN_target +
	// pwmN_enable + fanN_input all present. Classifier must still read this
	// as ClassPrimary because the companion pwmN_enable governs control.
	amd := mkHwmonDir(t, root, "hwmon5", "amdgpu")
	writeFile(t, filepath.Join(amd, "pwm1"), "128\n")
	writeFile(t, filepath.Join(amd, "pwm1_enable"), "1\n")
	writeFile(t, filepath.Join(amd, "fan1_input"), "1700\n")
	writeFile(t, filepath.Join(amd, "fan1_target"), "1700\n")

	devices := EnumerateDevices(root)

	wantClasses := map[string]CapabilityClass{
		"hwmon0": ClassNoFans,
		"hwmon1": ClassReadOnly,
		"hwmon2": ClassOpenLoop,
		"hwmon3": ClassPrimary,
		"hwmon4": ClassSkipNVIDIA,
		"hwmon5": ClassPrimary,
	}

	if len(devices) != len(wantClasses) {
		t.Fatalf("got %d devices, want %d", len(devices), len(wantClasses))
	}

	for _, d := range devices {
		base := filepath.Base(d.Dir)
		want, ok := wantClasses[base]
		if !ok {
			t.Errorf("%s: unexpected device in result", base)
			continue
		}
		if d.Class != want {
			t.Errorf("%s: class = %q, want %q", base, d.Class, want)
		}
	}

	// Specific shape assertions on hwmon3 (ClassPrimary).
	var primary *HwmonDevice
	for i := range devices {
		if filepath.Base(devices[i].Dir) == "hwmon3" {
			primary = &devices[i]
		}
	}
	if primary == nil {
		t.Fatal("hwmon3 not returned")
	}
	if len(primary.PWM) != 2 {
		t.Errorf("hwmon3: %d PWM channels, want 2", len(primary.PWM))
	}
	for _, ch := range primary.PWM {
		if ch.EnablePath == "" {
			t.Errorf("hwmon3 pwm%s: missing EnablePath", ch.Index)
		}
		if ch.FanInput == "" {
			t.Errorf("hwmon3 pwm%s: missing FanInput", ch.Index)
		}
	}

	// ClassReadOnly must still surface the fan_input for UI display.
	for _, d := range devices {
		if filepath.Base(d.Dir) == "hwmon1" {
			if len(d.FanInputs) == 0 {
				t.Errorf("hwmon1: readonly device should surface fan_input paths")
			}
		}
	}

	// ClassOpenLoop must have a PWM channel with EnablePath but no FanInput.
	for _, d := range devices {
		if filepath.Base(d.Dir) == "hwmon2" {
			if len(d.PWM) != 1 {
				t.Errorf("hwmon2: %d PWM channels, want 1", len(d.PWM))
				break
			}
			if d.PWM[0].EnablePath == "" {
				t.Error("hwmon2: PWM channel missing EnablePath")
			}
			if d.PWM[0].FanInput != "" {
				t.Error("hwmon2: open-loop PWM should have no FanInput")
			}
		}
	}
}

func TestEnumerateDevices_NumericSort(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"hwmon10", "hwmon2", "hwmon1"} {
		mkHwmonDir(t, root, name, "chip")
	}
	devices := EnumerateDevices(root)
	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices))
	}
	wantOrder := []string{"hwmon1", "hwmon2", "hwmon10"}
	for i, d := range devices {
		if got := filepath.Base(d.Dir); got != wantOrder[i] {
			t.Errorf("index %d: got %s, want %s", i, got, wantOrder[i])
		}
	}
}

func TestEnumerateDevices_MissingRoot(t *testing.T) {
	devices := EnumerateDevices(filepath.Join(t.TempDir(), "does-not-exist"))
	if devices != nil {
		t.Errorf("missing root: want nil, got %v", devices)
	}
}
