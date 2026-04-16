package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHwmon builds a synthetic /sys/class/hwmon tree inside dir. Each key in
// the layout is a path relative to dir, each value is the file contents.
// Parent directories are created automatically. Returns dir so callers can
// compose further paths.
func fakeHwmon(t *testing.T, dir string, layout map[string]string) {
	t.Helper()
	for rel, content := range layout {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		// Files created with 0o600 so the writability probe exercises a real
		// permission bit (some tests override to 0o400 to simulate read-only).
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// TestHwmonFanName_WithLabel exercises the label-first path. A driver that
// exposes fanN_label (e.g. nct6687d with BIOS-provided names) wins over the
// chip-prefixed fallback.
func TestHwmonFanName_WithLabel(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon3/name":       "nct6687\n",
		"hwmon3/pwm1":       "128\n",
		"hwmon3/fan1_label": "CPU FAN\n",
		"hwmon3/pwm2":       "100\n",
		"hwmon3/fan2_label": "sys fan1\n",
	})

	if got, want := hwmonFanName(filepath.Join(dir, "hwmon3", "pwm1")), "Cpu Fan"; got != want {
		t.Errorf("pwm1 with label: got %q, want %q", got, want)
	}
	if got, want := hwmonFanName(filepath.Join(dir, "hwmon3", "pwm2")), "Sys Fan1"; got != want {
		t.Errorf("pwm2 with label: got %q, want %q", got, want)
	}
}

// TestHwmonFanName_FallbackToChipPrefix covers the branch where no
// fanN_label exists. Must produce a chip-prefixed "<CHIP> Fan N" name so
// two boards with overlapping "Fan 1" entries don't collide.
func TestHwmonFanName_FallbackToChipPrefix(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon5/name": "it8688\n",
		"hwmon5/pwm3": "200\n",
	})

	if got, want := hwmonFanName(filepath.Join(dir, "hwmon5", "pwm3")), "IT8688 Fan 3"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestHwmonFanName_RPMTargetChannel ensures fan*_target paths produce a
// sensible fan name. The channel number is extracted from "fanN_target".
func TestHwmonFanName_RPMTargetChannel(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon9/name":        "amdgpu\n",
		"hwmon9/fan1_target": "1500\n",
		"hwmon9/fan1_label":  "gpu fan\n",
	})

	if got, want := hwmonFanName(filepath.Join(dir, "hwmon9", "fan1_target")), "Gpu Fan"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestHwmonFanName_NoNameFile pins the final fallback — dirname of the
// parent becomes the chip stand-in so the output still disambiguates.
func TestHwmonFanName_NoNameFile(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon7/pwm2": "0\n",
		// intentionally no name file
	})
	// The parent is "hwmon7" — uppercased that becomes "HWMON7".
	if got, want := hwmonFanName(filepath.Join(dir, "hwmon7", "pwm2")), "HWMON7 Fan 2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestReadHwmonCritC_DirectCrit covers the fast path: a temp*_crit file
// matching the sensor file name exists and reports a positive millidegree
// value.
func TestReadHwmonCritC_DirectCrit(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon1/temp1_input": "60000\n",
		"hwmon1/temp1_crit":  "100000\n",
	})
	if got, want := readHwmonCritC(filepath.Join(dir, "hwmon1", "temp1_input")), 100.0; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestReadHwmonCritC_PackageLabelFallback covers pass 2: the direct _crit
// file is absent but a labeled "Package id 0" _crit exists elsewhere on the
// same chip.
func TestReadHwmonCritC_PackageLabelFallback(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon2/temp1_input": "45000\n",
		"hwmon2/temp2_input": "50000\n",
		"hwmon2/temp2_label": "Package id 0\n",
		"hwmon2/temp2_crit":  "100000\n",
	})
	// temp1_input has no _crit, but temp2_crit (labeled "Package") should win.
	if got, want := readHwmonCritC(filepath.Join(dir, "hwmon2", "temp1_input")), 100.0; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestReadHwmonCritC_NoCritReturnsZero pins the "unknown" signal that
// buildConfig uses to fall back to its hardcoded 85°C curve defaults.
func TestReadHwmonCritC_NoCritReturnsZero(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon4/temp1_input": "40000\n",
	})
	if got := readHwmonCritC(filepath.Join(dir, "hwmon4", "temp1_input")); got != 0 {
		t.Errorf("got %v, want 0 (unknown)", got)
	}
}

// TestReadAMDGPUPowerW covers the AMD GPU power-limit reader. Value is in
// microwatts and must be integer-divided into watts; a missing or zero
// reading yields 0 (the documented "unknown" signal).
func TestReadAMDGPUPowerW(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"250W", "250000000\n", 250},
		{"150W", "150000000\n", 150},
		{"fractional_floor", "150500000\n", 150},
		{"zero", "0\n", 0},
		{"negative", "-1\n", 0},
		{"nonsense", "hello\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "power1_cap"),
				[]byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readAMDGPUPowerW(dir); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReadAMDGPUPowerW_MissingFile pins the no-file branch: hwmon dirs
// without power1_cap (older cards, or an early classifyDevice pass) must
// report 0 so buildConfig falls back to "unknown".
func TestReadAMDGPUPowerW_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if got := readAMDGPUPowerW(dir); got != 0 {
		t.Errorf("missing file: got %v, want 0", got)
	}
}

// TestReadAMDGPUCritC covers the AMD GPU crit-temperature reader. The path
// is a tempN_input; the function inspects tempN_crit. Matches the millidegree
// convention.
func TestReadAMDGPUCritC(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon2/temp2_input": "55000\n",
		"hwmon2/temp2_crit":  "110000\n",
	})
	if got, want := readAMDGPUCritC(filepath.Join(dir, "hwmon2", "temp2_input")), 110.0; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestReadAMDGPUCritC_MissingReturnsZero pins the "unknown" fallback for
// boards whose crit file is missing.
func TestReadAMDGPUCritC_MissingReturnsZero(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon3/temp1_input": "55000\n",
	})
	if got := readAMDGPUCritC(filepath.Join(dir, "hwmon3", "temp1_input")); got != 0 {
		t.Errorf("missing _crit: got %v, want 0", got)
	}
}

// TestTestPWMWritable_WithEnable covers the common path — a chip that
// exposes pwm_enable. The function must flip enable to 1 (manual) and
// restore the original value, and must report true only when both writes
// succeed.
func TestTestPWMWritable_WithEnable(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon1/pwm1":        "100\n",
		"hwmon1/pwm1_enable": "2\n", // auto mode
	})
	pwmPath := filepath.Join(dir, "hwmon1", "pwm1")
	if !testPWMWritable(pwmPath) {
		t.Errorf("expected writable=true")
	}
	// After return, pwm1_enable should be restored to 2 (auto).
	data, _ := os.ReadFile(filepath.Join(dir, "hwmon1", "pwm1_enable"))
	if got := string(data); got != "2\n" {
		t.Errorf("pwm1_enable after test = %q, want restored to %q", got, "2\n")
	}
}

// TestTestPWMWritable_NoEnable covers the driver-without-pwm_enable path.
// Some chips (nct6683 backing NCT6687D) omit pwm_enable entirely; the
// function falls back to writing the existing PWM value directly and
// reports true when that succeeds.
func TestTestPWMWritable_NoEnable(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon2/pwm1": "150\n",
		// intentionally no pwm1_enable
	})
	pwmPath := filepath.Join(dir, "hwmon2", "pwm1")
	if !testPWMWritable(pwmPath) {
		t.Errorf("expected writable=true for driver without pwm_enable")
	}
	// pwm1 value must be the same — we round-tripped the current value.
	data, _ := os.ReadFile(pwmPath)
	if got := string(data); got != "150\n" {
		t.Errorf("pwm1 after test = %q, want %q", got, "150\n")
	}
}

// TestTestPWMWritable_MissingPWM pins the "not even readable" branch.
// The function must report false without panicking.
func TestTestPWMWritable_MissingPWM(t *testing.T) {
	dir := t.TempDir()
	// no pwm file at all
	if got := testPWMWritable(filepath.Join(dir, "hwmon1", "pwm1")); got {
		t.Errorf("missing pwm: got true, want false")
	}
}

// TestTestFanTargetWritable covers the rpm_target writability probe. Must
// preserve the value (write-what-you-read) and return true iff both ends
// of that round-trip succeed.
func TestTestFanTargetWritable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fan1_target")
	if err := os.WriteFile(path, []byte("1500\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !testFanTargetWritable(path) {
		t.Errorf("expected writable=true")
	}
	data, _ := os.ReadFile(path)
	if got := string(data); got != "1500\n" {
		t.Errorf("value changed after writability probe: got %q, want %q", got, "1500\n")
	}
}

// TestTestFanTargetWritable_Missing pins the missing-path branch.
func TestTestFanTargetWritable_Missing(t *testing.T) {
	dir := t.TempDir()
	if got := testFanTargetWritable(filepath.Join(dir, "fan1_target")); got {
		t.Errorf("missing fan_target: got true, want false")
	}
}

// TestTestFanTargetWritable_NonNumeric pins the parse-failure branch — a
// fan_target file that exists but contains garbage must be rejected.
func TestTestFanTargetWritable_NonNumeric(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fan1_target")
	if err := os.WriteFile(path, []byte("not a number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := testFanTargetWritable(path); got {
		t.Errorf("non-numeric: got true, want false")
	}
}
