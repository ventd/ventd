package monitor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFile writes data to path, creating the file mode 0o644. Fails the
// test fatally on error — test fixtures that can't be laid down are not
// a soft failure.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mkHwmonDir creates a hwmon chip directory under root with the given
// chip name and returns its path. Every test fixture needs a "name"
// file; the kernel guarantees one per hwmon device.
func mkHwmonDir(t *testing.T, root, hwmonName, chipName string) string {
	t.Helper()
	dir := filepath.Join(root, hwmonName)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeFile(t, filepath.Join(dir, "name"), chipName+"\n")
	return dir
}

// withScanRoot overrides the package scanRoot for the duration of the
// test and restores it via t.Cleanup. Tests that need a fresh fake
// sysfs tree should call this once at the top.
func withScanRoot(t *testing.T, root string) {
	t.Helper()
	prev := scanRoot
	scanRoot = root
	t.Cleanup(func() { scanRoot = prev })
}

// findDevice returns the first Device whose Path matches the hwmon
// directory name (e.g. "hwmon0"), or nil if none match.
func findDevice(devs []Device, path string) *Device {
	for i := range devs {
		if devs[i].Path == path {
			return &devs[i]
		}
	}
	return nil
}

// findReading returns the first Reading with the matching label from d,
// or nil if none match.
func findReading(d *Device, label string) *Reading {
	for i := range d.Readings {
		if d.Readings[i].Label == label {
			return &d.Readings[i]
		}
	}
	return nil
}

// TestScanHwmon_HappyPath — case A from the plan. Two chips with
// temp/fan/pwm entries. Verifies friendlyDeviceName, scaled values,
// and label handling. Tests drive scanHwmon directly rather than the
// public Scan() entry point so the fake-sysfs fixture isn't polluted
// by any real NVIDIA GPU the host happens to have; scanNVML
// coverage is a v0.4 item (needs a GPU mock).
func TestScanHwmon_HappyPath(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d0 := mkHwmonDir(t, root, "hwmon0", "coretemp")
	writeFile(t, filepath.Join(d0, "temp1_input"), "45000\n")
	writeFile(t, filepath.Join(d0, "temp1_label"), "Package id 0\n")

	d1 := mkHwmonDir(t, root, "hwmon1", "nct6798")
	writeFile(t, filepath.Join(d1, "fan1_input"), "1200\n")
	writeFile(t, filepath.Join(d1, "fan2_input"), "800\n")
	writeFile(t, filepath.Join(d1, "pwm1"), "128\n") // should not be picked up — no *_input suffix

	devs := scanHwmon()
	if len(devs) != 2 {
		t.Fatalf("Scan: got %d devices, want 2: %+v", len(devs), devs)
	}

	d := findDevice(devs, "hwmon0")
	if d == nil {
		t.Fatalf("Scan: no hwmon0 device; got %+v", devs)
	}
	if d.Name != "Intel CPU" {
		t.Errorf("hwmon0 Name = %q, want %q", d.Name, "Intel CPU")
	}
	r := findReading(d, "Package id 0")
	if r == nil {
		t.Fatalf("hwmon0: no 'Package id 0' reading; got %+v", d.Readings)
	}
	if r.Value != 45.0 || r.Unit != "°C" || r.SensorType != "hwmon" {
		t.Errorf("hwmon0 temp: value=%v unit=%q type=%q; want 45.0 °C hwmon",
			r.Value, r.Unit, r.SensorType)
	}

	d = findDevice(devs, "hwmon1")
	if d == nil {
		t.Fatalf("Scan: no hwmon1 device; got %+v", devs)
	}
	if d.Name != "Nuvoton NCT6798" {
		t.Errorf("hwmon1 Name = %q, want %q", d.Name, "Nuvoton NCT6798")
	}
	f1 := findReading(d, "fan1")
	if f1 == nil || f1.Value != 1200 || f1.Unit != "RPM" {
		t.Errorf("hwmon1 fan1: %+v, want 1200 RPM", f1)
	}
	f2 := findReading(d, "fan2")
	if f2 == nil || f2.Value != 800 {
		t.Errorf("hwmon1 fan2: %+v, want 800 RPM", f2)
	}
	// pwm1 has no *_input suffix, must not appear as a reading
	for _, rd := range d.Readings {
		if rd.Label == "pwm1" {
			t.Errorf("hwmon1: pwm1 should not appear as a reading; got %+v", rd)
		}
	}
}

// TestFriendlyDeviceName — case B from the plan. Exhaustive coverage of
// every switch branch, every prefix-match if-branch, and the default
// fallthrough.
func TestFriendlyDeviceName(t *testing.T) {
	cases := []struct {
		name string
		chip string
		want string
	}{
		{"coretemp", "coretemp", "Intel CPU"},
		{"k10temp", "k10temp", "AMD CPU"},
		{"zenpower", "zenpower", "AMD CPU (ZenPower)"},
		{"amdgpu", "amdgpu", "AMD GPU"},
		{"nouveau", "nouveau", "NVIDIA GPU (nouveau)"},
		{"nvme", "nvme", "NVMe Drive"},
		{"drivetemp", "drivetemp", "Drive Temp"},
		{"acpitz", "acpitz", "ACPI Thermal"},
		{"cpu_thermal", "cpu_thermal", "CPU Thermal"},
		{"cpu-thermal", "cpu-thermal", "CPU Thermal"},
		{"iwlwifi_1", "iwlwifi_1", "Wi-Fi"},
		// Case insensitivity — the switch lowers first, so upper-case
		// matches the same bucket.
		{"coretemp_upper", "CORETEMP", "Intel CPU"},
		// Nuvoton prefix family.
		{"nct6687", "nct6687", "Nuvoton NCT6687"},
		{"nct6798", "nct6798", "Nuvoton NCT6798"},
		// ITE prefix family — prefix "it" + digit. Keeps original
		// case wrapped in "ITE <UPPER>".
		{"it8688", "it8688", "ITE IT8688"},
		{"it8792", "it8792", "ITE IT8792"},
		// "itchip" (no digit after "it") must NOT match ITE.
		{"it_nodigit", "itchip", "itchip"},
		// Winbond prefix.
		{"w83627ehf", "w83627ehf", "Winbond W83627EHF"},
		// Fintek prefix — both f71 and f81 branches.
		{"f71882fg", "f71882fg", "Fintek F71882FG"},
		{"f81865f", "f81865f", "Fintek F81865F"},
		// Default fallthrough — unknown chip returned verbatim.
		{"unknown", "mystery_chip", "mystery_chip"},
		// Short name — less than 3 chars — must not crash the ITE
		// prefix length check.
		{"short", "it", "it"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := friendlyDeviceName(tc.chip)
			if got != tc.want {
				t.Errorf("friendlyDeviceName(%q) = %q, want %q", tc.chip, got, tc.want)
			}
		})
	}
}

// TestNaturalSortPaths — case C from the plan. Covers the numeric sort
// and the extractBaseNum helper. The sort mutates in place.
func TestNaturalSortPaths(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "lex_would_reorder",
			in: []string{
				"/sys/class/hwmon/hwmon0/temp10_input",
				"/sys/class/hwmon/hwmon0/temp2_input",
				"/sys/class/hwmon/hwmon0/temp1_input",
			},
			want: []string{
				"/sys/class/hwmon/hwmon0/temp1_input",
				"/sys/class/hwmon/hwmon0/temp2_input",
				"/sys/class/hwmon/hwmon0/temp10_input",
			},
		},
		{
			name: "empty",
			in:   []string{},
			want: []string{},
		},
		{
			name: "single",
			in:   []string{"temp1_input"},
			want: []string{"temp1_input"},
		},
		{
			name: "no_digits_returns_zero",
			in:   []string{"b_input", "a_input"},
			// Both map to 0, so the sort.Slice comparator returns
			// false both ways — stable sort keeps insertion order.
			want: []string{"b_input", "a_input"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]string(nil), tc.in...)
			naturalSortPaths(got)
			// Treat nil and empty as equivalent — a zero-length
			// slice has no ordering to verify, and DeepEqual
			// distinguishes nil from empty in a way that isn't
			// meaningful here.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("naturalSortPaths(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestExtractBaseNum — complements case C. The extract helper is public
// to the test (same package) and has clear edge cases worth pinning.
func TestExtractBaseNum(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"temp1_input", 1},
		{"temp10_input", 10},
		{"/a/b/temp42_input", 42},
		{"fan3_input", 3},
		{"no_digits", 0},
		{"", 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := extractBaseNum(tc.in); got != tc.want {
				t.Errorf("extractBaseNum(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestScanHwmon_MissingInputFile — case D from the plan. A temp*_input
// path that vanishes between glob and read must be skipped, not crash.
// Simulated by creating the glob-visible sibling files in one chip
// dir, then the subject file only exists with unreadable content to
// force the same code path — readInt returns an error, scanInputs
// continues.
func TestScanHwmon_MissingInputFile(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "coretemp")
	// Good reading.
	writeFile(t, filepath.Join(d, "temp1_input"), "45000\n")
	// Unreadable — empty content triggers readInt's "empty" error.
	writeFile(t, filepath.Join(d, "temp2_input"), "")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	dev := devs[0]
	if n := len(dev.Readings); n != 1 {
		t.Fatalf("hwmon0: %d readings, want 1 (temp2 should be skipped): %+v",
			n, dev.Readings)
	}
	if dev.Readings[0].Value != 45.0 {
		t.Errorf("hwmon0 temp1 value = %v, want 45.0", dev.Readings[0].Value)
	}
}

// TestScanHwmon_MalformedContent — case E. A non-numeric temp*_input
// must not panic; scanInputs must continue and return the other good
// readings.
func TestScanHwmon_MalformedContent(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "coretemp")
	writeFile(t, filepath.Join(d, "temp1_input"), "not_a_number\n")
	writeFile(t, filepath.Join(d, "temp2_input"), "60000\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	if n := len(devs[0].Readings); n != 1 {
		t.Fatalf("hwmon0: %d readings, want 1 (malformed skipped): %+v",
			n, devs[0].Readings)
	}
	if devs[0].Readings[0].Value != 60.0 {
		t.Errorf("hwmon0 temp2 value = %v, want 60.0", devs[0].Readings[0].Value)
	}
}

// TestScanHwmon_Empty — case F. scanRoot points at an empty directory
// (no hwmon* entries). scanHwmon must return nil, no panic.
func TestScanHwmon_Empty(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	devs := scanHwmon()
	if len(devs) != 0 {
		t.Fatalf("Scan: got %d devices on empty root, want 0: %+v", len(devs), devs)
	}
}

// TestScanHwmon_MissingRoot — scanRoot points at a non-existent path.
// os.ReadDir errors, scanHwmon returns nil, Scan returns empty slice
// (scanNVML is a no-op without the NVML library).
func TestScanHwmon_MissingRoot(t *testing.T) {
	withScanRoot(t, filepath.Join(t.TempDir(), "does_not_exist"))

	devs := scanHwmon()
	if len(devs) != 0 {
		t.Fatalf("Scan: got %d devices on missing root, want 0: %+v", len(devs), devs)
	}
}

// TestScanHwmon_FanRPMZeroSkipped — case G. fan*_input = 0 means the
// fan is stopped / unresponsive; the existing code drops it so the UI
// doesn't show a dead fan. Non-zero fans in the same chip stay visible.
func TestScanHwmon_FanRPMZeroSkipped(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "nct6798")
	writeFile(t, filepath.Join(d, "fan1_input"), "0\n")
	writeFile(t, filepath.Join(d, "fan2_input"), "1500\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	dev := devs[0]
	if n := len(dev.Readings); n != 1 {
		t.Fatalf("hwmon0: %d readings, want 1 (fan1=0 skipped): %+v",
			n, dev.Readings)
	}
	if dev.Readings[0].Label != "fan2" || dev.Readings[0].Value != 1500 {
		t.Errorf("hwmon0 fan: %+v, want fan2=1500", dev.Readings[0])
	}
}

// TestScanHwmon_MirrorFansDeduped — #796: many embedded EC firmwares
// expose the same physical fan's RPM across multiple `fan*_input`
// zones (CPU / system / chassis virtual zones, all reading the
// identical RPM because there's only one tach behind them). Phoenix's
// minipc HIL surfaced four fan tach zones for one physical fan.
// scanHwmon now collapses fans within ±10 RPM on the same hwmon
// device. Distinct fans (>10 RPM apart) are preserved.
func TestScanHwmon_MirrorFansDeduped(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "fake_ec")
	// 4 mirror tachs all reading 1500 RPM (within tolerance of each other).
	writeFile(t, filepath.Join(d, "fan1_input"), "1500\n")
	writeFile(t, filepath.Join(d, "fan2_input"), "1502\n")
	writeFile(t, filepath.Join(d, "fan3_input"), "1497\n")
	writeFile(t, filepath.Join(d, "fan4_input"), "1505\n")
	// One distinct fan at a different speed — must NOT collapse.
	writeFile(t, filepath.Join(d, "fan5_input"), "800\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1", len(devs))
	}
	if got := len(devs[0].Readings); got != 2 {
		t.Fatalf("hwmon0: %d fan readings after dedup, want 2 (one mirror cluster + one distinct): %+v",
			got, devs[0].Readings)
	}
	// First reading wins from the cluster (fan1 = 1500); fan5 distinct.
	rpms := []float64{devs[0].Readings[0].Value, devs[0].Readings[1].Value}
	if !((rpms[0] == 1500 && rpms[1] == 800) || (rpms[0] == 800 && rpms[1] == 1500)) {
		t.Errorf("hwmon0 surviving fan rpms = %v, want {1500, 800}", rpms)
	}
}

// TestScanHwmon_DistinctFansNotMerged — guards against over-eager
// dedup. Two fans that differ by more than mirrorRPMTolerance must
// both appear.
func TestScanHwmon_DistinctFansNotMerged(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "nct6798")
	writeFile(t, filepath.Join(d, "fan1_input"), "1500\n")
	writeFile(t, filepath.Join(d, "fan2_input"), "1700\n") // 200 RPM apart, distinct

	devs := scanHwmon()
	if got := len(devs[0].Readings); got != 2 {
		t.Errorf("distinct fans collapsed: got %d, want 2: %+v", got, devs[0].Readings)
	}
}

// TestScanHwmon_VoltageAndPower — case H. Voltage divisor is 1000
// (mV → V); power divisor is 1_000_000 (µW → W).
func TestScanHwmon_VoltageAndPower(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "nct6798")
	writeFile(t, filepath.Join(d, "in0_input"), "1200\n")
	writeFile(t, filepath.Join(d, "power1_input"), "45000000\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	dev := &devs[0]

	v := findReading(dev, "in0")
	if v == nil || v.Value != 1.2 || v.Unit != "V" {
		t.Errorf("hwmon0 in0: %+v, want 1.2 V", v)
	}
	p := findReading(dev, "power1")
	if p == nil || p.Value != 45.0 || p.Unit != "W" {
		t.Errorf("hwmon0 power1: %+v, want 45.0 W", p)
	}
}

// TestScanHwmon_LabelFallback — case I. With a *_label file, the
// reading's Label must be the label's content. Without one, it must
// fall back to the sensor's base name (e.g. "temp1").
func TestScanHwmon_LabelFallback(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "coretemp")
	writeFile(t, filepath.Join(d, "temp1_input"), "45000\n")
	writeFile(t, filepath.Join(d, "temp1_label"), "Package id 0\n")
	writeFile(t, filepath.Join(d, "temp2_input"), "50000\n")
	// no temp2_label — must fall back

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	dev := &devs[0]

	r1 := findReading(dev, "Package id 0")
	if r1 == nil {
		t.Errorf("hwmon0: expected reading with label 'Package id 0'; got %+v",
			dev.Readings)
	}
	r2 := findReading(dev, "temp2")
	if r2 == nil {
		t.Errorf("hwmon0: expected reading falling back to 'temp2'; got %+v",
			dev.Readings)
	}
}

// TestScanHwmon_NoNameFile — scanHwmon falls back to the hwmon
// directory name when the driver hasn't exposed a "name" file. The
// chip lookup then runs against e.g. "hwmon0", which the default
// friendlyDeviceName case echoes back unchanged.
func TestScanHwmon_NoNameFile(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	dir := filepath.Join(root, "hwmon0")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No "name" file — the empty string triggers the fallback.
	writeFile(t, filepath.Join(dir, "temp1_input"), "30000\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	if devs[0].Name != "hwmon0" {
		t.Errorf("Name = %q, want fallback to %q", devs[0].Name, "hwmon0")
	}
}

// TestScanHwmon_DeviceWithoutReadings — a chip with no *_input files at
// all must be excluded from the result (matches the `len(dev.Readings)
// > 0` guard in scanHwmon).
func TestScanHwmon_DeviceWithoutReadings(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	mkHwmonDir(t, root, "hwmon0", "coretemp") // only "name", no inputs

	devs := scanHwmon()
	if len(devs) != 0 {
		t.Fatalf("Scan: got %d devices, want 0 (no readings): %+v", len(devs), devs)
	}
}

// TestReadStrReadInt — small helpers, easy to pin. readStr returns ""
// on error and trims surrounding whitespace. readInt returns
// ParseInt's error on non-numeric input and an "empty" error on
// missing/empty content.
func TestReadStrReadInt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "trim"), "  42  \n")
	writeFile(t, filepath.Join(root, "blank"), "")
	writeFile(t, filepath.Join(root, "junk"), "oops\n")

	if got := readStr(filepath.Join(root, "trim")); got != "42" {
		t.Errorf("readStr trim = %q, want %q", got, "42")
	}
	if got := readStr(filepath.Join(root, "missing")); got != "" {
		t.Errorf("readStr missing = %q, want empty", got)
	}

	if n, err := readInt(filepath.Join(root, "trim")); err != nil || n != 42 {
		t.Errorf("readInt trim: n=%d err=%v, want 42 nil", n, err)
	}
	if _, err := readInt(filepath.Join(root, "blank")); err == nil {
		t.Errorf("readInt blank: want error on empty file")
	}
	if _, err := readInt(filepath.Join(root, "junk")); err == nil {
		t.Errorf("readInt junk: want ParseInt error on non-numeric input")
	}
}

// TestScan_IncludesHwmonDevices — exercises the public Scan() entry
// point over a fake sysfs tree and asserts the hwmon half of the
// result is present. scanNVML() runs too; on a host with no NVIDIA
// driver it returns nil, and on a host with one the real GPU entry
// appears alongside the fake hwmon entry. Either way, our fake
// device must be in the output — that's what this test pins.
func TestScan_IncludesHwmonDevices(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "coretemp")
	writeFile(t, filepath.Join(d, "temp1_input"), "42000\n")

	devs := Scan()
	got := findDevice(devs, "hwmon0")
	if got == nil {
		t.Fatalf("Scan: fake hwmon0 not present in result %+v", devs)
	}
	if got.Name != "Intel CPU" {
		t.Errorf("hwmon0 Name = %q, want %q", got.Name, "Intel CPU")
	}
	r := findReading(got, "temp1")
	if r == nil || r.Value != 42.0 {
		t.Errorf("hwmon0 temp1: %+v, want value 42.0", r)
	}
}

// TestScanHwmon_MultipleReadingTypes — cross-cuts case A and H: a
// single chip with a temp, two fans, a voltage, and a power reading
// must report all five in a stable, natural-sorted order.
func TestScanHwmon_MultipleReadingTypes(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d := mkHwmonDir(t, root, "hwmon0", "nct6798")
	writeFile(t, filepath.Join(d, "temp1_input"), "40000\n")
	writeFile(t, filepath.Join(d, "fan1_input"), "1200\n")
	writeFile(t, filepath.Join(d, "fan10_input"), "900\n") // natural-sort check
	writeFile(t, filepath.Join(d, "in0_input"), "1000\n")
	writeFile(t, filepath.Join(d, "power1_input"), "10000000\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1: %+v", len(devs), devs)
	}
	if n := len(devs[0].Readings); n != 5 {
		t.Fatalf("hwmon0: %d readings, want 5: %+v", n, devs[0].Readings)
	}

	// scanHwmon appends temps, then fans, then voltages, then powers.
	// Within a prefix, natural-sort by index: fan1 before fan10.
	wantLabels := []string{"temp1", "fan1", "fan10", "in0", "power1"}
	for i, want := range wantLabels {
		if got := devs[0].Readings[i].Label; got != want {
			t.Errorf("reading[%d].Label = %q, want %q", i, got, want)
		}
	}
}

// TestRegression_Issue460v2_SentinelSuppressedAtScanBoundary pins
// RULE-HWMON-SENTINEL-STATUS-BOUNDARY: the monitor.Scan path must reject
// sentinel / implausible values (0xFFFF temp, 0xFFFF RPM, 0xFFFF voltage)
// before they appear in the /api/hardware JSON payload. This test
// reproduces the v2 failure mode: raw sysfs contains the sentinel but the
// scan happens at the moment of mid-latch, so the UI shows 255.5°C even
// though a second-later direct sysfs read looks normal.
func TestRegression_Issue460v2_SentinelSuppressedAtScanBoundary(t *testing.T) {
	root := t.TempDir()
	withScanRoot(t, root)

	d0 := mkHwmonDir(t, root, "hwmon0", "nct6687")

	// Sentinel temp (255500 millidegrees → 255.5°C after ÷1000). Must be suppressed.
	writeFile(t, filepath.Join(d0, "temp6_input"), "255500\n")
	writeFile(t, filepath.Join(d0, "temp6_label"), "PCIe x1\n")

	// Valid temp alongside sentinel — must still appear.
	writeFile(t, filepath.Join(d0, "temp1_input"), "42000\n")
	writeFile(t, filepath.Join(d0, "temp1_label"), "CPU Temp\n")

	// Sentinel RPM (65535 — 0xFFFF raw). Must be suppressed.
	writeFile(t, filepath.Join(d0, "fan3_input"), "65535\n")

	// Valid fan alongside sentinel — must still appear.
	writeFile(t, filepath.Join(d0, "fan1_input"), "1200\n")

	// Sentinel voltage (65535 millivolts → 65.535 V after ÷1000). Must be suppressed.
	writeFile(t, filepath.Join(d0, "in5_input"), "65535\n")

	// Valid voltage alongside sentinel — must still appear.
	writeFile(t, filepath.Join(d0, "in0_input"), "12000\n")

	devs := scanHwmon()
	if len(devs) != 1 {
		t.Fatalf("Scan: got %d devices, want 1", len(devs))
	}
	dev := devs[0]

	// Sentinel values must be absent from readings.
	for _, rd := range dev.Readings {
		if rd.Value >= 150.0 && rd.Unit == "°C" {
			t.Errorf("sentinel temp %.1f°C escaped suppression (label=%q)", rd.Value, rd.Label)
		}
		if rd.Value >= 65535 && rd.Unit == "RPM" {
			t.Errorf("sentinel RPM %.0f escaped suppression (label=%q)", rd.Value, rd.Label)
		}
		if rd.Value > 20.0 && rd.Unit == "V" {
			t.Errorf("sentinel voltage %.3f V escaped suppression (label=%q)", rd.Value, rd.Label)
		}
	}

	// Valid readings must be present.
	if r := findReading(&dev, "CPU Temp"); r == nil || r.Value != 42.0 {
		t.Errorf("valid temp 42°C missing from readings: %+v", dev.Readings)
	}
	if r := findReading(&dev, "fan1"); r == nil || r.Value != 1200 {
		t.Errorf("valid fan 1200 RPM missing from readings: %+v", dev.Readings)
	}
	if r := findReading(&dev, "in0"); r == nil || r.Value != 12.0 {
		t.Errorf("valid voltage 12 V missing from readings: %+v", dev.Readings)
	}
}

// TestSentinelMonitorVal_AllBranches exercises isSentinelMonitorVal directly
// to confirm the threshold table matches the constants in
// internal/hal/hwmon/sentinel.go.
func TestSentinelMonitorVal_AllBranches(t *testing.T) {
	cases := []struct {
		prefix string
		val    float64
		want   bool
		desc   string
	}{
		// temp
		{"temp", 149.9, false, "temp below cap"},
		{"temp", 150.0, true, "temp at cap (PlausibleTempMaxCelsius=150)"},
		{"temp", 255.5, true, "temp 0xFFFF sentinel"},
		// fan
		{"fan", 10000, false, "fan at PlausibleRPMMax"},
		{"fan", 10001, true, "fan just above PlausibleRPMMax"},
		{"fan", 65535, true, "fan 0xFFFF sentinel"},
		// in (voltage)
		{"in", 20.0, false, "voltage at cap"},
		{"in", 20.001, true, "voltage just above PlausibleVoltageMaxVolts"},
		{"in", 65.535, true, "voltage 0xFFFF sentinel"},
		// unknown prefix
		{"power", 9999, false, "power prefix not checked"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if got := isSentinelMonitorVal(tc.prefix, tc.val); got != tc.want {
				t.Errorf("isSentinelMonitorVal(%q, %v) = %v, want %v", tc.prefix, tc.val, got, tc.want)
			}
		})
	}
}
