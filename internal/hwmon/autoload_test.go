package hwmon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// newTestLogger returns a slog.Logger that discards output — the tests
// care about return values, not what the code logs.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// readTestdata reads a file from internal/hwmon/testdata/.
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return string(data)
}

// candidateEq compares two candidate slices order-independently.
func candidateEq(a, b []candidate) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]candidate(nil), a...)
	bs := append([]candidate(nil), b...)
	sort.Slice(as, func(i, j int) bool {
		if as[i].module != as[j].module {
			return as[i].module < as[j].module
		}
		return as[i].options < as[j].options
	})
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].module != bs[j].module {
			return bs[i].module < bs[j].module
		}
		return bs[i].options < bs[j].options
	})
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// --- A. parseSensorsDetectModules -------------------------------------------

func TestParseSensorsDetectModules(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []candidate
	}{
		{
			name:  "ubuntu 24.04 coretemp+nct6775",
			input: readTestdata(t, "sensors-detect-ubuntu2404.txt"),
			want: []candidate{
				{module: "coretemp"},
				{module: "nct6775"},
			},
		},
		{
			name:  "arch it87 with force_id option",
			input: readTestdata(t, "sensors-detect-arch-it87.txt"),
			want: []candidate{
				{module: "coretemp"},
				{module: "it87", options: "force_id=0x8625"},
			},
		},
		{
			name:  "empty output",
			input: "",
			want:  []candidate{},
		},
		{
			name:  "no cut-here markers",
			input: "some output\nwith no cut-here section\nat all\n",
			want:  []candidate{},
		},
		{
			name: "only comments inside cut-here",
			input: "Preamble\n" +
				"#----cut here----\n" +
				"# Chip drivers\n" +
				"# only comments here\n" +
				"#----cut here----\n" +
				"Trailer\n",
			want: []candidate{},
		},
		{
			name: "second cut-here terminates section",
			input: "#----cut here----\n" +
				"coretemp\n" +
				"#----cut here----\n" +
				"# this line is outside the section\n" +
				"should_not_appear\n",
			want: []candidate{{module: "coretemp"}},
		},
		{
			name: "options line with malformed single-token value is ignored",
			input: "#----cut here----\n" +
				"it87\n" +
				"options it87\n" +
				"#----cut here----\n",
			want: []candidate{{module: "it87"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSensorsDetectModules(tc.input)
			if got == nil {
				got = []candidate{}
			}
			if !candidateEq(got, tc.want) {
				t.Fatalf("parseSensorsDetectModules mismatch\nwant: %#v\ngot:  %#v", tc.want, got)
			}
		})
	}
}

// --- B. parseSensorsDetectChips ---------------------------------------------

func TestParseSensorsDetectChips(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name: "it87 with IT8625E chip",
			input: "Driver `it87':\n" +
				"  * ISA bus, address 0xa40\n" +
				"    Chip `IT8625E Super I/O Sensors' (confidence: 9)\n",
			want: map[string]string{"it87": "IT8625E Super I/O Sensors"},
		},
		{
			name: "to-be-written driver entry is captured",
			input: "Driver `to-be-written':\n" +
				"    Chip `IT8688E Super I/O' (confidence: 9)\n",
			want: map[string]string{"to-be-written": "IT8688E Super I/O"},
		},
		{
			name:  "ubuntu 24.04 full output",
			input: readTestdata(t, "sensors-detect-ubuntu2404.txt"),
			want: map[string]string{
				"coretemp": "Intel digital thermal sensor",
				"nct6775":  "Nuvoton NCT6779D Super IO Sensors",
			},
		},
		{
			name:  "empty output",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "no driver lines",
			input: "just some text\nno drivers here\n",
			want:  map[string]string{},
		},
		{
			name: "should-be-inserted variant captures driver name",
			input: "Driver `nct6775' (should be inserted):\n" +
				"  * ISA bus, address 0x290\n" +
				"    Chip `Nuvoton NCT6798D Super IO Sensors' (confidence: 9)\n",
			want: map[string]string{"nct6775": "Nuvoton NCT6798D Super IO Sensors"},
		},
		{
			name: "loaded variant captures driver name",
			input: "Driver `it87' (loaded):\n" +
				"    Chip `ITE IT8625E Super IO Sensors' (confidence: 9)\n",
			want: map[string]string{"it87": "ITE IT8625E Super IO Sensors"},
		},
		{
			name: "bare driver line with no trailing colon",
			input: "Driver `coretemp'\n" +
				"  * Chip `Intel digital thermal sensor' (confidence: 9)\n",
			want: map[string]string{"coretemp": "Intel digital thermal sensor"},
		},
		{
			name:  "should-be-inserted fixture",
			input: readTestdata(t, "sensors-detect-should-be-inserted.txt"),
			want: map[string]string{
				"coretemp": "Intel digital thermal sensor",
				"nct6775":  "Nuvoton NCT6798D Super IO Sensors",
				"it87":     "ITE IT8625E Super IO Sensors",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSensorsDetectChips(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("size mismatch: want %d got %d (%v)", len(tc.want), len(got), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("chips[%q]: want %q got %q", k, v, got[k])
				}
			}
		})
	}
}

// --- C. iteForceIDsFromDetection --------------------------------------------

func TestIteForceIDsFromDetection(t *testing.T) {
	logger := newTestLogger()
	tests := []struct {
		name  string
		chips map[string]string
		want  []candidate
	}{
		{
			name:  "it87/IT8625E produces force_id=0x8625",
			chips: map[string]string{"it87": "IT8625E Super I/O"},
			want:  []candidate{{module: "it87", options: "force_id=0x8625"}},
		},
		{
			name:  "to-be-written/IT8792E produces force_id=0x8792",
			chips: map[string]string{"to-be-written": "IT8792E"},
			want:  []candidate{{module: "it87", options: "force_id=0x8792"}},
		},
		{
			name:  "IT8688E is not in iteChipForceIDs map, skipped",
			chips: map[string]string{"it87": "IT8688E Super I/O"},
			want:  nil,
		},
		{
			name:  "non-it87/non-to-be-written driver is skipped",
			chips: map[string]string{"acpi": "something"},
			want:  nil,
		},
		{
			name:  "empty chips yields empty result",
			chips: map[string]string{},
			want:  nil,
		},
		{
			name:  "chip name must start with IT8",
			chips: map[string]string{"it87": "SomeOther Chip"},
			want:  nil,
		},
		{
			name:  "empty chip name is skipped",
			chips: map[string]string{"it87": ""},
			want:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := iteForceIDsFromDetection(tc.chips, logger)
			if !candidateEq(got, tc.want) {
				t.Fatalf("iteForceIDsFromDetection mismatch\nwant: %#v\ngot:  %#v", tc.want, got)
			}
		})
	}
}

// --- D. identifyDriverNeeds -------------------------------------------------

func TestIdentifyDriverNeeds(t *testing.T) {
	// keys extracts DriverNeed.Key values for stable comparison.
	keys := func(nds []DriverNeed) []string {
		out := make([]string, 0, len(nds))
		for _, n := range nds {
			out = append(out, n.Key)
		}
		sort.Strings(out)
		return out
	}

	tests := []struct {
		name        string
		boardVendor string
		hwmonNames  []string
		wantKeys    []string
	}{
		{
			name:       "nct6687 hwmon name selects nct6687d driver",
			hwmonNames: []string{"nct6687"},
			wantKeys:   []string{"nct6687d"},
		},
		{
			name:       "it8688 in hwmon name selects it8688e",
			hwmonNames: []string{"it8688"},
			wantKeys:   []string{"it8688e"},
		},
		{
			name:       "it8689 in hwmon name selects it8689e",
			hwmonNames: []string{"it8689"},
			wantKeys:   []string{"it8689e"},
		},
		{
			name:        "gigabyte vendor with no chip match falls through to it8688e",
			boardVendor: "Gigabyte Technology Co., Ltd.",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:        "ASUSTeK without asus_ec/asus_ec_sensors falls through to it8688e",
			boardVendor: "ASUSTeK COMPUTER INC.",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:        "ASUSTeK with asus_ec_sensors does not flag it8688e",
			boardVendor: "ASUSTeK COMPUTER INC.",
			hwmonNames:  []string{"asus_ec_sensors"},
			wantKeys:    []string{},
		},
		{
			name:        "ASUSTeK with asus_ec does not flag it8688e",
			boardVendor: "ASUSTeK COMPUTER INC.",
			hwmonNames:  []string{"asus_ec"},
			wantKeys:    []string{},
		},
		{
			name:        "MSI (Micro-Star International) falls through to it8688e",
			boardVendor: "Micro-Star International Co., Ltd.",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:        "ASRock falls through to it8688e",
			boardVendor: "ASRock",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:        "Biostar falls through to it8688e",
			boardVendor: "Biostar",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:        "unknown vendor with no chip match yields empty",
			boardVendor: "Unknown Vendor",
			hwmonNames:  []string{"coretemp"},
			wantKeys:    []string{},
		},
		{
			name:        "chip-match for it8688 takes precedence over vendor fallback",
			boardVendor: "Gigabyte Technology Co., Ltd.",
			hwmonNames:  []string{"it8688"},
			wantKeys:    []string{"it8688e"},
		},
		{
			name:       "nct6687 plus it8688 returns both (dual Super I/O board)",
			hwmonNames: []string{"nct6687", "it8688"},
			wantKeys:   []string{"it8688e", "nct6687d"},
		},
		{
			name:        "empty inputs yield empty result",
			boardVendor: "",
			hwmonNames:  nil,
			wantKeys:    []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(identifyDriverNeeds(tc.boardVendor, tc.hwmonNames))
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("size mismatch: want %v got %v", tc.wantKeys, got)
			}
			for i, k := range tc.wantKeys {
				if got[i] != k {
					t.Errorf("key[%d]: want %q got %q (full: %v)", i, k, got[i], got)
				}
			}
		})
	}
}

// --- E. koBasename ----------------------------------------------------------

func TestKoBasename(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"nct6775.ko.zst", "nct6775"},
		{"it87.ko.xz", "it87"},
		{"coretemp.ko.gz", "coretemp"},
		{"w83627ehf.ko", "w83627ehf"},
		{"README", ""},
		{"", ""},
		{"some.noko.file", ""},
		// zst before ko — longest-match order matters so the .ko.zst case
		// strips the whole suffix rather than leaving ".zst" behind.
		{"f71882fg.ko.zst", "f71882fg"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := koBasename(tc.in); got != tc.want {
				t.Errorf("koBasename(%q): want %q got %q", tc.in, tc.want, got)
			}
		})
	}
}

// --- F. isBusSpecificModule -------------------------------------------------

func TestIsBusSpecificModule(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"pci alias", "pci:v00008086d0000A3A1sv*sd*bc*sc*i*", true},
		{"i2c alias", "i2c:nct6775", true},
		{"spi alias", "spi:adt7310", true},
		{"usb alias", "usb:v*p*", true},
		{"hid alias", "hid:b0003g*v*p*", true},
		{"cpu alias", "cpu:type:x86,ven0VENfam6Fmod158", true},
		{"of (device-tree) alias", "of:N*T*Cacme,something", true},
		{"acpi alias", "acpi*:ACPI0007:*", true},
		{"mdio alias", "mdio:00000101010101010101010101010101", true},
		{"platform alias is NOT bus-specific", "platform:coretemp", false},
		{"empty string", "", false},
		{"plain text", "not a bus alias", false},
		{
			name: "multi-line with one pci alias flags whole module",
			in:   "something\npci:v0000ABCDd*\nother\n",
			want: true,
		},
		{
			name: "multi-line all platform aliases is not bus-specific",
			in:   "platform:coretemp\nplatform:something\n",
			want: false,
		},
		{
			name: "lines with leading whitespace are trimmed before prefix match",
			in:   "   pci:v0000ABCDd*\n",
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBusSpecificModule(tc.in); got != tc.want {
				t.Errorf("isBusSpecificModule(%q): want %v got %v", tc.in, tc.want, got)
			}
		})
	}
}

// --- G. moduleFromPath ------------------------------------------------------

// makeFakeHwmon builds a fake hwmon directory at root/hwmonN with the
// given chip name and optional module symlink target. Returns the pwm
// path (root/hwmonN/pwm1) that moduleFromPath should be called with.
func makeFakeHwmon(t *testing.T, root, hwmon, chipName, modSymTarget string) string {
	t.Helper()
	dir := filepath.Join(root, hwmon)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if chipName != "" {
		if err := os.WriteFile(filepath.Join(dir, "name"), []byte(chipName+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if modSymTarget != "" {
		driverDir := filepath.Join(dir, "device", "driver")
		if err := os.MkdirAll(driverDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Create the symlink target as a real directory so EvalSymlinks
		// resolves cleanly and returns its basename.
		targetAbs := filepath.Join(root, "fakemods", modSymTarget)
		if err := os.MkdirAll(targetAbs, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(targetAbs, filepath.Join(driverDir, "module")); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "pwm1")
}

// regresses #140
func TestModuleFromPath(t *testing.T) {
	tests := []struct {
		name       string
		chipName   string
		symTarget  string // "" = no symlink
		wantModule string
	}{
		{name: "it8688 name → it87 (fallback)", chipName: "it8688", wantModule: "it87"},
		{name: "nct6775 name → nct6775", chipName: "nct6775", wantModule: "nct6775"},
		// HasPrefix("nct6687", "nct678") is FALSE (chars differ at
		// index 4: '6' vs '7'), so the switch falls through to the
		// nct6687 case and returns nct6687d.
		{name: "nct6687 name → nct6687d", chipName: "nct6687", wantModule: "nct6687d"},
		{name: "nct6687d name → nct6687d", chipName: "nct6687d", wantModule: "nct6687d"},
		// nct6683 chip uses the dedicated nct6683 in-tree driver, not
		// nct6775. Matched by an exact-name case so it isn't shadowed
		// by an over-broad HasPrefix later in the switch.
		{name: "nct6683 chip name → nct6683", chipName: "nct6683", wantModule: "nct6683"},
		{name: "w83627ehf prefix → w83627ehf module", chipName: "w836", wantModule: "w83627ehf"},
		{name: "f71882fg prefix → f71882fg module", chipName: "f7182", wantModule: "f71882fg"},
		{name: "asus_ec exact match → asus_ec_sensors", chipName: "asus_ec", wantModule: "asus_ec_sensors"},
		{name: "unknown chip name → empty", chipName: "coretemp", wantModule: ""},
		{name: "symlink wins over name fallback", chipName: "it8688", symTarget: "real_mod_name", wantModule: "real_mod_name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pwm := makeFakeHwmon(t, root, "hwmon0", tc.chipName, tc.symTarget)
			if got := moduleFromPath(pwm); got != tc.wantModule {
				t.Errorf("moduleFromPath: want %q got %q", tc.wantModule, got)
			}
		})
	}

	t.Run("missing name file yields empty", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "hwmon0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// pwm1 exists, name does NOT, no symlink.
		if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := moduleFromPath(filepath.Join(dir, "pwm1")); got != "" {
			t.Errorf("moduleFromPath with no name: want empty got %q", got)
		}
	})
}

// --- H. countControllablePWM ------------------------------------------------

func TestCountControllablePWM(t *testing.T) {
	// Helper: make hwmonN directory with given pwm files and optional
	// pwm*_enable companions. Returns the list of pwm paths.
	makePWMs := func(root, hwmon string, pwms []string, withEnable map[string]bool) []string {
		dir := filepath.Join(root, hwmon)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, p := range pwms {
			full := filepath.Join(dir, p)
			if err := os.WriteFile(full, []byte("128\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if withEnable[p] {
				if err := os.WriteFile(full+"_enable", []byte("1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			paths = append(paths, full)
		}
		return paths
	}

	t.Run("all pwms have _enable companions", func(t *testing.T) {
		root := t.TempDir()
		paths := makePWMs(root, "hwmon0",
			[]string{"pwm1", "pwm2"},
			map[string]bool{"pwm1": true, "pwm2": true},
		)
		if got := countControllablePWM(paths); got != 2 {
			t.Errorf("want 2 controllable, got %d", got)
		}
	})

	t.Run("one pwm missing _enable is skipped", func(t *testing.T) {
		root := t.TempDir()
		paths := makePWMs(root, "hwmon0",
			[]string{"pwm1", "pwm2"},
			map[string]bool{"pwm1": true}, // pwm2 has no _enable
		)
		if got := countControllablePWM(paths); got != 1 {
			t.Errorf("want 1 controllable, got %d", got)
		}
	})

	t.Run("empty paths yields 0", func(t *testing.T) {
		if got := countControllablePWM(nil); got != 0 {
			t.Errorf("want 0, got %d", got)
		}
	})

	t.Run("nonexistent pwm paths yield 0", func(t *testing.T) {
		if got := countControllablePWM([]string{"/nonexistent/pwm1"}); got != 0 {
			t.Errorf("want 0, got %d", got)
		}
	})

	t.Run("none have _enable (read-only PWM)", func(t *testing.T) {
		root := t.TempDir()
		paths := makePWMs(root, "hwmon0",
			[]string{"pwm1", "pwm2", "pwm3"},
			map[string]bool{}, // none enabled
		)
		if got := countControllablePWM(paths); got != 0 {
			t.Errorf("want 0, got %d", got)
		}
	})
}

// --- I. mergeModuleLoadFile / readModuleNames --------------------------------

func TestMergeModuleLoadFile(t *testing.T) {
	t.Run("first_probe_creates_file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "nct6775"); err != nil {
			t.Fatalf("mergeModuleLoadFile: %v", err)
		}

		got := readModuleNames(path)
		if len(got) != 1 || got[0] != "nct6775" {
			t.Fatalf("want [nct6775], got %v", got)
		}
	})

	t.Run("second_probe_appends_dedup_sorted", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "nct6775"); err != nil {
			t.Fatalf("first probe: %v", err)
		}
		if err := mergeModuleLoadFile(path, "it87"); err != nil {
			t.Fatalf("second probe: %v", err)
		}

		got := readModuleNames(path)
		want := []string{"it87", "nct6775"} // sorted lexically
		if len(got) != len(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
		for i, m := range want {
			if got[i] != m {
				t.Errorf("modules[%d]: want %q got %q", i, m, got[i])
			}
		}
	})

	t.Run("idempotent_repeated_probe", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "nct6775"); err != nil {
			t.Fatalf("first probe: %v", err)
		}
		if err := mergeModuleLoadFile(path, "it87"); err != nil {
			t.Fatalf("second probe: %v", err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		if err := mergeModuleLoadFile(path, "it87"); err != nil {
			t.Fatalf("third probe (repeat): %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("idempotency violated\nbefore: %q\nafter:  %q", before, after)
		}
	})

	t.Run("empty_probe_does_not_truncate", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "nct6775"); err != nil {
			t.Fatalf("setup probe: %v", err)
		}
		if err := mergeModuleLoadFile(path, ""); err != nil {
			t.Fatalf("empty probe returned error: %v", err)
		}

		got := readModuleNames(path)
		if len(got) != 1 || got[0] != "nct6775" {
			t.Fatalf("want [nct6775] after empty probe, got %v", got)
		}
	})

	t.Run("header_comment_present", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "coretemp"); err != nil {
			t.Fatalf("probe: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(data), "# Written by ventd") {
			t.Errorf("expected header comment, file content: %q", string(data))
		}
	})

	t.Run("write_is_atomic_tmp_rename", func(t *testing.T) {
		// Verify no .tmp file is left behind after a successful write.
		dir := t.TempDir()
		path := filepath.Join(dir, "ventd.conf")

		if err := mergeModuleLoadFile(path, "nct6775"); err != nil {
			t.Fatalf("probe: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("leftover tmp file after successful write: %s", e.Name())
			}
		}
	})
}
