package fakedmi_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/testfixture/fakedmi"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// expectedFiles is the complete set of DMI sysfs filenames the fixture must
// create, regardless of Options content.
var expectedFiles = []string{
	"board_vendor",
	"board_name",
	"board_version",
	"sys_vendor",
	"product_name",
	"product_version",
	"chassis_type",
	"bios_vendor",
	"bios_version",
	"bios_date",
}

// readDMIFile reads a single DMI file from the fake root and returns its
// value stripped of the trailing newline, mirroring what real consumers do.
func readDMIFile(root, field string) string {
	data, err := os.ReadFile(filepath.Join(root, field))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}

// ── constructor + cleanup ────────────────────────────────────────────────────

func TestNew_NilOpts(t *testing.T) {
	f := fakedmi.New(t, nil)
	if f.Root() == "" {
		t.Fatal("Root() must not be empty")
	}
}

func TestNew_EmptyOpts(t *testing.T) {
	f := fakedmi.New(t, &fakedmi.Options{})
	if f.Root() == "" {
		t.Fatal("Root() must not be empty")
	}
}

func TestRoot_IsDirectory(t *testing.T) {
	f := fakedmi.New(t, nil)
	info, err := os.Stat(f.Root())
	if err != nil {
		t.Fatalf("Stat(Root()): %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Root() %q is not a directory", f.Root())
	}
}

func TestRoot_ContainsAllExpectedFiles(t *testing.T) {
	f := fakedmi.New(t, nil)
	for _, name := range expectedFiles {
		path := filepath.Join(f.Root(), name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing DMI file %q: %v", name, err)
		}
	}
}

func TestRoot_EmptyFieldsProduceNewlineOnlyFiles(t *testing.T) {
	f := fakedmi.New(t, &fakedmi.Options{}) // all fields empty
	for _, name := range expectedFiles {
		data, err := os.ReadFile(filepath.Join(f.Root(), name))
		if err != nil {
			t.Errorf("read %q: %v", name, err)
			continue
		}
		if string(data) != "\n" {
			t.Errorf("%q: want %q, got %q", name, "\n", string(data))
		}
	}
}

func TestCleanup_RootRemovedAfterTest(t *testing.T) {
	var savedRoot string
	t.Run("inner", func(t *testing.T) {
		f := fakedmi.New(t, nil)
		savedRoot = f.Root()
		if _, err := os.Stat(savedRoot); err != nil {
			t.Fatalf("root must exist during test: %v", err)
		}
	})
	// After the subtest, t.TempDir() cleanup fires and the directory is gone.
	if _, err := os.Stat(savedRoot); !os.IsNotExist(err) {
		t.Errorf("root %q should not exist after test cleanup (err=%v)", savedRoot, err)
	}
}

// ── preset correctness ───────────────────────────────────────────────────────

type presetCase struct {
	name string
	opts fakedmi.Options
	// spot-check: map sysfs filename → expected value
	checks map[string]string
}

var presetCases = []presetCase{
	{
		name: "BoardMSIMegX570",
		opts: fakedmi.BoardMSIMegX570,
		checks: map[string]string{
			"board_vendor": "Micro-Star International Co., Ltd.",
			"board_name":   "MEG X570 UNIFY",
			"chassis_type": "3",
			"sys_vendor":   "Micro-Star International Co., Ltd.",
		},
	},
	{
		name: "BoardGigabyteX870E",
		opts: fakedmi.BoardGigabyteX870E,
		checks: map[string]string{
			"board_vendor": "Gigabyte Technology Co., Ltd.",
			"board_name":   "X870E AORUS MASTER",
			"chassis_type": "3",
		},
	},
	{
		name: "BoardASUSPrimeX670E",
		opts: fakedmi.BoardASUSPrimeX670E,
		checks: map[string]string{
			"board_vendor": "ASUSTeK COMPUTER INC.",
			"board_name":   "PRIME X670E-PRO WIFI",
			"chassis_type": "3",
		},
	},
	{
		name: "BoardSupermicroX11",
		opts: fakedmi.BoardSupermicroX11,
		checks: map[string]string{
			"board_vendor": "Supermicro",
			"board_name":   "X11DPi-N",
			"chassis_type": "17",
			"product_name": "SYS-2028U-TR4+",
		},
	},
	{
		name: "BoardDellPowerEdgeR750",
		opts: fakedmi.BoardDellPowerEdgeR750,
		checks: map[string]string{
			"board_vendor": "Dell Inc.",
			"product_name": "PowerEdge R750",
			"chassis_type": "17",
			"bios_vendor":  "Dell Inc.",
		},
	},
	{
		name: "BoardFramework13",
		opts: fakedmi.BoardFramework13,
		checks: map[string]string{
			"board_vendor": "Framework Computer Inc",
			"board_name":   "Laptop 13 (AMD Ryzen AI 300)",
			"chassis_type": "10",
			"sys_vendor":   "Framework Computer Inc",
		},
	},
	{
		name: "BoardFramework16",
		opts: fakedmi.BoardFramework16,
		checks: map[string]string{
			"board_vendor": "Framework Computer Inc",
			"board_name":   "Laptop 16",
			"chassis_type": "10",
		},
	},
	{
		name: "BoardRPi5",
		opts: fakedmi.BoardRPi5,
		checks: map[string]string{
			"board_vendor": "Raspberry Pi Ltd",
			"board_name":   "Raspberry Pi 5 Model B Rev 1.0",
			"chassis_type": "1",
			"sys_vendor":   "Raspberry Pi Ltd",
		},
	},
	{
		name: "BoardMacBookPro14Asahi",
		opts: fakedmi.BoardMacBookPro14Asahi,
		checks: map[string]string{
			"board_vendor": "Apple Inc.",
			"product_name": "MacBook Pro (14-inch, 2021)",
			"chassis_type": "10",
			"bios_vendor":  "Apple Inc.",
		},
	},
}

func TestPresets_AllFilesPresent(t *testing.T) {
	for _, tc := range presetCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := fakedmi.New(t, &tc.opts)
			for _, name := range expectedFiles {
				if _, err := os.Stat(filepath.Join(f.Root(), name)); err != nil {
					t.Errorf("missing %q: %v", name, err)
				}
			}
		})
	}
}

func TestPresets_FieldValues(t *testing.T) {
	for _, tc := range presetCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := fakedmi.New(t, &tc.opts)
			for field, want := range tc.checks {
				got := readDMIFile(f.Root(), field)
				if got != want {
					t.Errorf("%q = %q, want %q", field, got, want)
				}
			}
		})
	}
}

// TestPresets_AllHaveChassisType verifies every preset declares a non-empty
// ChassisType so consumers can classify board form factor.
func TestPresets_AllHaveChassisType(t *testing.T) {
	for _, tc := range presetCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := fakedmi.New(t, &tc.opts)
			got := readDMIFile(f.Root(), "chassis_type")
			if got == "" {
				t.Errorf("%s: chassis_type is empty", tc.name)
			}
		})
	}
}

// TestConsumer_ReadsDMIRoot exercises a simple consumer that reads board_vendor
// and board_name from the fake root — the pattern T-FP-01 will use.
func TestConsumer_ReadsDMIRoot(t *testing.T) {
	opts := fakedmi.BoardMSIMegX570
	f := fakedmi.New(t, &opts)

	vendor := readDMIFile(f.Root(), "board_vendor")
	name := readDMIFile(f.Root(), "board_name")

	if !strings.Contains(vendor, "Micro-Star") {
		t.Errorf("board_vendor %q does not contain 'Micro-Star'", vendor)
	}
	if name != opts.BoardName {
		t.Errorf("board_name = %q, want %q", name, opts.BoardName)
	}
}

// TestFileContent_TrailingNewline verifies files end with exactly one newline,
// matching real sysfs file format.
func TestFileContent_TrailingNewline(t *testing.T) {
	opts := fakedmi.BoardFramework13
	f := fakedmi.New(t, &opts)

	for _, name := range expectedFiles {
		data, err := os.ReadFile(filepath.Join(f.Root(), name))
		if err != nil {
			t.Errorf("read %q: %v", name, err)
			continue
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Errorf("%q: file does not end with newline (content=%q)", name, string(data))
		}
	}
}
