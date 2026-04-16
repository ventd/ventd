package setup

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRootedManager builds a Manager whose hwmonRoot, procRoot, and
// powercapRoot all live under t.TempDir(). The three sub-directories are
// created up-front so callers can populate them with fakeHwmon / fakeProc
// / fakePowercap. cal is nil because these tests only exercise the
// discovery methods; they never call Start or run.
func newRootedManager(t *testing.T) (m *Manager, hwmonRoot, procRoot, powercapRoot string) {
	t.Helper()
	base := t.TempDir()
	hwmonRoot = filepath.Join(base, "hwmon")
	procRoot = filepath.Join(base, "proc")
	powercapRoot = filepath.Join(base, "powercap")
	for _, d := range []string{hwmonRoot, procRoot, powercapRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewWithRoots(nil, logger, hwmonRoot, procRoot, powercapRoot), hwmonRoot, procRoot, powercapRoot
}

// fakeFile writes content to rel (relative to root), creating parent dirs.
// Tests use this for /proc/cpuinfo and /sys/class/powercap layouts where
// fakeHwmon would overconstrain the directory name shape.
func fakeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestDiscoverCPUTempSensor_Fixtures exercises the three-pass discovery
// logic against fixture trees. Each case pins a branch: a known CPU chip
// with a package label, a known chip without labels, a generic chip with
// a CPU-tagged label, and the acpitz last-resort.
func TestDiscoverCPUTempSensor_Fixtures(t *testing.T) {
	cases := []struct {
		name     string
		layout   map[string]string
		wantName string
		wantFile string // relative to hwmonRoot; "" means no match
	}{
		{
			name: "pass1_coretemp_package",
			layout: map[string]string{
				"hwmon0/name":        "coretemp\n",
				"hwmon0/temp1_input": "45000\n",
				"hwmon0/temp1_label": "Package id 0\n",
			},
			wantName: "coretemp Package",
			wantFile: "hwmon0/temp1_input",
		},
		{
			name: "pass1_k10temp_tdie",
			layout: map[string]string{
				"hwmon1/name":        "k10temp\n",
				"hwmon1/temp1_input": "40000\n",
				"hwmon1/temp1_label": "Tdie\n",
			},
			wantName: "k10temp Package",
			wantFile: "hwmon1/temp1_input",
		},
		{
			name: "pass1_coretemp_no_label_returns_first_match",
			layout: map[string]string{
				"hwmon2/name":        "coretemp\n",
				"hwmon2/temp1_input": "42000\n",
				"hwmon2/temp2_input": "43000\n",
			},
			wantName: "coretemp",
			wantFile: "hwmon2/temp1_input",
		},
		{
			name: "pass2_nct6687_cpu_label",
			layout: map[string]string{
				"hwmon3/name":        "nct6687\n",
				"hwmon3/temp1_input": "50000\n",
				"hwmon3/temp1_label": "CPU Temperature\n",
			},
			wantName: "nct6687 cpu temperature",
			wantFile: "hwmon3/temp1_input",
		},
		{
			name: "pass3_acpitz_fallback",
			layout: map[string]string{
				"hwmon4/name":        "acpitz\n",
				"hwmon4/temp1_input": "48000\n",
			},
			wantName: "ACPI Thermal",
			wantFile: "hwmon4/temp1_input",
		},
		{
			name: "pass3_beats_unlabeled_nct",
			layout: map[string]string{
				// nct6687 with no label → pass 2 ignores it; pass 3 picks up acpitz.
				"hwmon3/name":        "nct6687\n",
				"hwmon3/temp1_input": "50000\n",
				"hwmon4/name":        "acpitz\n",
				"hwmon4/temp1_input": "48000\n",
			},
			wantName: "ACPI Thermal",
			wantFile: "hwmon4/temp1_input",
		},
		{
			name: "skip_amdgpu_in_pass2",
			layout: map[string]string{
				// amdgpu with a "cpu" label must NOT be selected by pass 2 — the
				// skip map keeps GPU sensors out of the CPU search.
				"hwmon5/name":        "amdgpu\n",
				"hwmon5/temp1_input": "60000\n",
				"hwmon5/temp1_label": "cpu-shaped lie\n",
			},
			wantName: "",
			wantFile: "",
		},
		{
			name:     "empty_root_no_match",
			layout:   map[string]string{},
			wantName: "",
			wantFile: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, hwmonRoot, _, _ := newRootedManager(t)
			fakeHwmon(t, hwmonRoot, tc.layout)

			name, path := m.discoverCPUTempSensor()
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			wantPath := ""
			if tc.wantFile != "" {
				wantPath = filepath.Join(hwmonRoot, tc.wantFile)
			}
			if path != wantPath {
				t.Errorf("path = %q, want %q", path, wantPath)
			}
		})
	}
}

// TestDiscoverAMDGPUTemp_Fixtures pins the junction-over-edge preference
// and the label/fallback branches. The three cases cover: labeled junction
// wins, edge-only when junction is absent, and a no-amdgpu tree returns
// empty.
func TestDiscoverAMDGPUTemp_Fixtures(t *testing.T) {
	cases := []struct {
		name     string
		layout   map[string]string
		wantName string
		wantFile string
	}{
		{
			name: "junction_labeled_wins",
			layout: map[string]string{
				"hwmon0/name":        "amdgpu\n",
				"hwmon0/temp1_input": "55000\n",
				"hwmon0/temp2_input": "65000\n",
				"hwmon0/temp2_label": "junction\n",
			},
			wantName: "amdgpu junction",
			wantFile: "hwmon0/temp2_input",
		},
		{
			name: "junction_no_label_uses_gpu_stand_in",
			layout: map[string]string{
				"hwmon0/name":        "amdgpu\n",
				"hwmon0/temp2_input": "65000\n",
			},
			wantName: "amdgpu GPU",
			wantFile: "hwmon0/temp2_input",
		},
		{
			name: "edge_only_when_junction_absent",
			layout: map[string]string{
				"hwmon0/name":        "amdgpu\n",
				"hwmon0/temp1_input": "55000\n",
				"hwmon0/temp1_label": "edge\n",
			},
			wantName: "amdgpu edge",
			wantFile: "hwmon0/temp1_input",
		},
		{
			name: "no_amdgpu_no_match",
			layout: map[string]string{
				"hwmon0/name":        "nouveau\n",
				"hwmon0/temp1_input": "50000\n",
			},
			wantName: "",
			wantFile: "",
		},
		{
			name:     "empty_root_no_match",
			layout:   map[string]string{},
			wantName: "",
			wantFile: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, hwmonRoot, _, _ := newRootedManager(t)
			fakeHwmon(t, hwmonRoot, tc.layout)

			name, path := m.discoverAMDGPUTemp()
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			wantPath := ""
			if tc.wantFile != "" {
				wantPath = filepath.Join(hwmonRoot, tc.wantFile)
			}
			if path != wantPath {
				t.Errorf("path = %q, want %q", path, wantPath)
			}
		})
	}
}

// TestReadCPUModel_Fixtures pins the cpuinfo-parsing branches: a typical
// multi-line file, a file with no model_name (ARM/virt), and a missing
// file. The empty-string return is the documented "unknown" signal.
func TestReadCPUModel_Fixtures(t *testing.T) {
	cases := []struct {
		name    string
		cpuinfo string // "" means don't create the file
		want    string
	}{
		{
			name: "intel_i9",
			cpuinfo: "processor\t: 0\n" +
				"vendor_id\t: GenuineIntel\n" +
				"model name\t: 13th Gen Intel(R) Core(TM) i9-13900K\n" +
				"cpu MHz\t\t: 3000.000\n",
			want: "13th Gen Intel(R) Core(TM) i9-13900K",
		},
		{
			name: "amd_ryzen",
			cpuinfo: "processor\t: 0\n" +
				"vendor_id\t: AuthenticAMD\n" +
				"model name\t: AMD Ryzen 9 7950X 16-Core Processor\n",
			want: "AMD Ryzen 9 7950X 16-Core Processor",
		},
		{
			name:    "no_model_name_line",
			cpuinfo: "processor\t: 0\nBogoMIPS\t: 100.00\n",
			want:    "",
		},
		{
			name:    "missing_file",
			cpuinfo: "", // sentinel: skip file creation
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, procRoot, _ := newRootedManager(t)
			if tc.cpuinfo != "" {
				fakeFile(t, procRoot, "cpuinfo", tc.cpuinfo)
			}
			if got := m.readCPUModel(); got != tc.want {
				t.Errorf("readCPUModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadCPUVendor_Fixtures pins vendor_id extraction — the function that
// decides whether the CPU-module-missing diag emits coretemp or k10temp,
// and stays silent on ARM where no vendor_id line exists.
func TestReadCPUVendor_Fixtures(t *testing.T) {
	cases := []struct {
		name    string
		cpuinfo string
		want    string
	}{
		{
			name:    "intel",
			cpuinfo: "vendor_id\t: GenuineIntel\n",
			want:    "GenuineIntel",
		},
		{
			name:    "amd",
			cpuinfo: "vendor_id\t: AuthenticAMD\n",
			want:    "AuthenticAMD",
		},
		{
			name:    "hygon",
			cpuinfo: "vendor_id\t: HygonGenuine\n",
			want:    "HygonGenuine",
		},
		{
			name:    "arm_no_vendor_id",
			cpuinfo: "processor\t: 0\nBogoMIPS\t: 100.00\n",
			want:    "",
		},
		{
			name:    "missing_file",
			cpuinfo: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, procRoot, _ := newRootedManager(t)
			if tc.cpuinfo != "" {
				fakeFile(t, procRoot, "cpuinfo", tc.cpuinfo)
			}
			if got := m.readCPUVendor(); got != tc.want {
				t.Errorf("readCPUVendor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadRAPLTDPW_Fixtures pins both layouts the reader probes and the
// unit conversion from microwatts to watts. The two cases also cover the
// subtle order dependency: the nested layout is checked first, so when
// both exist the nested one wins — matching how the kernel actually
// exposes these on multi-socket boxes.
func TestReadRAPLTDPW_Fixtures(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string // rel→content under powercapRoot; "" skips
		want  int
	}{
		{
			name: "nested_layout_125w",
			files: map[string]string{
				"intel-rapl/intel-rapl:0/constraint_0_power_limit_uw": "125000000\n",
			},
			want: 125,
		},
		{
			name: "flat_layout_95w",
			files: map[string]string{
				"intel-rapl:0/constraint_0_power_limit_uw": "95000000\n",
			},
			want: 95,
		},
		{
			name: "nested_wins_over_flat",
			files: map[string]string{
				"intel-rapl/intel-rapl:0/constraint_0_power_limit_uw": "200000000\n",
				"intel-rapl:0/constraint_0_power_limit_uw":            "150000000\n",
			},
			want: 200,
		},
		{
			name: "zero_is_unknown",
			files: map[string]string{
				"intel-rapl/intel-rapl:0/constraint_0_power_limit_uw": "0\n",
			},
			want: 0,
		},
		{
			name: "non_numeric_is_unknown",
			files: map[string]string{
				"intel-rapl/intel-rapl:0/constraint_0_power_limit_uw": "not-a-number\n",
			},
			want: 0,
		},
		{
			name:  "missing_paths_is_unknown",
			files: map[string]string{},
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _, powercapRoot := newRootedManager(t)
			for rel, content := range tc.files {
				fakeFile(t, powercapRoot, rel, content)
			}
			if got := m.readRAPLTDPW(); got != tc.want {
				t.Errorf("readRAPLTDPW() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGatherProfile_Fixtures pins the assembly layer: the profile must
// weave together CPU model, CPU TDP (from RAPL), and the CPU crit temp
// (from hwmon) — all read through the injected roots. With nvmlOK=false
// and no AMD GPU path, the GPU fields stay zero.
func TestGatherProfile_Fixtures(t *testing.T) {
	m, hwmonRoot, procRoot, powercapRoot := newRootedManager(t)

	fakeFile(t, procRoot, "cpuinfo",
		"processor\t: 0\n"+
			"vendor_id\t: GenuineIntel\n"+
			"model name\t: Test CPU 9000X\n")
	fakeFile(t, powercapRoot,
		"intel-rapl/intel-rapl:0/constraint_0_power_limit_uw",
		"65000000\n")
	fakeHwmon(t, hwmonRoot, map[string]string{
		"hwmon0/name":        "coretemp\n",
		"hwmon0/temp1_input": "55000\n",
		"hwmon0/temp1_label": "Package id 0\n",
		"hwmon0/temp1_crit":  "100000\n",
	})

	sensorPath := filepath.Join(hwmonRoot, "hwmon0", "temp1_input")
	profile := m.gatherProfile(sensorPath, false, "")
	if profile == nil {
		t.Fatal("profile is nil")
	}
	if profile.CPUModel != "Test CPU 9000X" {
		t.Errorf("CPUModel = %q, want %q", profile.CPUModel, "Test CPU 9000X")
	}
	if profile.CPUTDPW != 65 {
		t.Errorf("CPUTDPW = %d, want 65", profile.CPUTDPW)
	}
	if profile.CPUThermalC != 100.0 {
		t.Errorf("CPUThermalC = %v, want 100.0", profile.CPUThermalC)
	}
	// No GPU source: NVML disabled, AMD path empty. All GPU fields must
	// remain zero so the curve builder falls back to its "unknown" logic.
	if profile.GPUModel != "" || profile.GPUPowerW != 0 || profile.GPUThermalC != 0 || len(profile.GPUs) != 0 {
		t.Errorf("GPU fields were populated despite no GPU source: %+v", profile)
	}
}

// TestGatherProfile_AMDGPUBranch pins the AMD GPU fallback: when NVML is
// unavailable but gpuTempPath points at an AMD hwmon sensor, the profile
// must pick up the power limit from the same hwmon dir and the crit
// threshold from the sibling tempN_crit file.
func TestGatherProfile_AMDGPUBranch(t *testing.T) {
	m, hwmonRoot, procRoot, powercapRoot := newRootedManager(t)

	fakeFile(t, procRoot, "cpuinfo",
		"processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: Test Ryzen\n")
	// No RAPL (AMD does not expose it on powercap like Intel).
	_ = powercapRoot

	fakeHwmon(t, hwmonRoot, map[string]string{
		"hwmon2/name":        "amdgpu\n",
		"hwmon2/temp2_input": "70000\n",
		"hwmon2/temp2_crit":  "110000\n",
		"hwmon2/power1_cap":  "250000000\n", // 250 W in microwatts
	})
	gpuPath := filepath.Join(hwmonRoot, "hwmon2", "temp2_input")

	profile := m.gatherProfile("", false, gpuPath)
	if profile == nil {
		t.Fatal("profile is nil")
	}
	if profile.GPUModel != "AMD GPU" {
		t.Errorf("GPUModel = %q, want %q", profile.GPUModel, "AMD GPU")
	}
	if profile.GPUPowerW != 250 {
		t.Errorf("GPUPowerW = %d, want 250", profile.GPUPowerW)
	}
	if profile.GPUThermalC != 110.0 {
		t.Errorf("GPUThermalC = %v, want 110.0", profile.GPUThermalC)
	}
	if len(profile.GPUs) != 1 || profile.GPUs[0].Model != "AMD GPU" {
		t.Errorf("GPUs slice = %+v, want one AMD GPU entry", profile.GPUs)
	}
}

// TestDiscoverHwmonControls_FriendlyNames replaces the #131 skip that
// sat on the orchestration-level fan-name invariant. The wizard must
// surface human-readable names (driver label first, chip-prefixed fan
// index as fallback) — never raw sysfs paths. Running discovery against
// a fixture root and then translating each path through hwmonFanName is
// what run() does downstream, so the pair is the real surface to test.
func TestDiscoverHwmonControls_FriendlyNames(t *testing.T) {
	m, hwmonRoot, _, _ := newRootedManager(t)

	fakeHwmon(t, hwmonRoot, map[string]string{
		// nct6687 with labels — the "CPU Fan"/"Sys Fan 1" case from the
		// usability invariant.
		"hwmon3/name":        "nct6687\n",
		"hwmon3/pwm1":        "128\n",
		"hwmon3/pwm1_enable": "5\n",
		"hwmon3/fan1_label":  "CPU FAN\n",
		"hwmon3/pwm2":        "100\n",
		"hwmon3/pwm2_enable": "5\n",
		"hwmon3/fan2_label":  "sys fan1\n",
		// coretemp is temp-only (no pwm/fan files) — must not contribute
		// anything to control discovery even though it sits in hwmonRoot.
		"hwmon0/name":        "coretemp\n",
		"hwmon0/temp1_input": "45000\n",
	})

	ctrls := m.discoverHwmonControls()

	// Translate each discovered PWM path to its friendly name and check
	// the invariant: no raw path components leak, and the labels the
	// fixture provided survive through to the user-facing name.
	var names []string
	for _, c := range ctrls {
		n := hwmonFanName(c.path)
		names = append(names, n)

		forbidden := []string{"/sys/", hwmonRoot, "hwmon3/pwm", "hwmon3/fan"}
		for _, f := range forbidden {
			if strings.Contains(n, f) {
				t.Errorf("friendly name %q leaks sysfs detail %q", n, f)
			}
		}
	}
	// Expect the two labeled PWM channels; coretemp must not show up.
	if len(names) != 2 {
		t.Fatalf("got %d controls, want 2 (pwm1, pwm2): %v", len(names), names)
	}
	joined := strings.Join(names, "|")
	if !strings.Contains(joined, "Cpu Fan") {
		t.Errorf("names %v missing labeled CPU fan", names)
	}
	if !strings.Contains(joined, "Sys Fan1") {
		t.Errorf("names %v missing labeled sys fan", names)
	}
}

// TestNew_UsesProductionRoots documents the default-path contract. The
// production constructor must route through the exported defaults — any
// future change to New that drops a field would flip one of these three
// assertions.
func TestNew_UsesProductionRoots(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(nil, logger)
	if m.hwmonRoot != defaultHwmonRoot {
		t.Errorf("hwmonRoot = %q, want %q", m.hwmonRoot, defaultHwmonRoot)
	}
	if m.procRoot != defaultProcRoot {
		t.Errorf("procRoot = %q, want %q", m.procRoot, defaultProcRoot)
	}
	if m.powercapRoot != defaultPowercapRoot {
		t.Errorf("powercapRoot = %q, want %q", m.powercapRoot, defaultPowercapRoot)
	}
}
