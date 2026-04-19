// Package fakedmi provides a deterministic /sys/class/dmi/id sysfs tree
// for unit tests. It creates a t.TempDir()-backed directory populated with
// the fields from Options; each file mirrors real sysfs format (value with
// trailing newline). Empty-string fields still produce a file containing
// a single newline, matching real sysfs behaviour for absent SMBIOS strings.
package fakedmi

import (
	"os"
	"path/filepath"
	"testing"
)

// Options configures the DMI fields written into the fake sysfs tree.
// Each exported field maps to the corresponding file under /sys/class/dmi/id/.
type Options struct {
	BoardVendor    string
	BoardName      string
	BoardVersion   string
	SysVendor      string
	ProductName    string
	ProductVersion string
	// ChassisType is the raw SMBIOS chassis-type integer as a decimal string.
	// Common values: "1"=Other, "3"=Desktop, "10"=Notebook, "11"=Hand held,
	// "17"=Rack mount, "23"=Blade.
	ChassisType string
	BIOSVendor  string
	BIOSVersion string
	BIOSDate    string
}

// Board preset variables — deterministic fingerprints for known hardware,
// matching the testplan §3 fixture spec. Values reflect real-world DMI
// output; use them directly or copy-and-override individual fields.
var (
	BoardMSIMegX570 = Options{
		BoardVendor:    "Micro-Star International Co., Ltd.",
		BoardName:      "MEG X570 UNIFY",
		BoardVersion:   "1.0",
		SysVendor:      "Micro-Star International Co., Ltd.",
		ProductName:    "MS-7C35",
		ProductVersion: "1.0",
		ChassisType:    "3",
		BIOSVendor:     "American Megatrends Inc.",
		BIOSVersion:    "7C35v1A9",
		BIOSDate:       "07/15/2022",
	}

	BoardGigabyteX870E = Options{
		BoardVendor:    "Gigabyte Technology Co., Ltd.",
		BoardName:      "X870E AORUS MASTER",
		BoardVersion:   "x.x",
		SysVendor:      "Gigabyte Technology Co., Ltd.",
		ProductName:    "X870E AORUS MASTER",
		ProductVersion: "",
		ChassisType:    "3",
		BIOSVendor:     "American Megatrends International, LLC.",
		BIOSVersion:    "F5",
		BIOSDate:       "11/08/2024",
	}

	BoardASUSPrimeX670E = Options{
		BoardVendor:    "ASUSTeK COMPUTER INC.",
		BoardName:      "PRIME X670E-PRO WIFI",
		BoardVersion:   "Rev X.0x",
		SysVendor:      "ASUSTeK COMPUTER INC.",
		ProductName:    "PRIME X670E-PRO WIFI",
		ProductVersion: "Rev X.0x",
		ChassisType:    "3",
		BIOSVendor:     "American Megatrends International, LLC.",
		BIOSVersion:    "1404",
		BIOSDate:       "05/22/2024",
	}

	BoardSupermicroX11 = Options{
		BoardVendor:    "Supermicro",
		BoardName:      "X11DPi-N",
		BoardVersion:   "1.10A",
		SysVendor:      "Supermicro",
		ProductName:    "SYS-2028U-TR4+",
		ProductVersion: "0123456789",
		ChassisType:    "17",
		BIOSVendor:     "American Megatrends Inc.",
		BIOSVersion:    "3.5",
		BIOSDate:       "08/30/2022",
	}

	BoardDellPowerEdgeR750 = Options{
		BoardVendor:    "Dell Inc.",
		BoardName:      "0WMJTH",
		BoardVersion:   "A01",
		SysVendor:      "Dell Inc.",
		ProductName:    "PowerEdge R750",
		ProductVersion: "",
		ChassisType:    "17",
		BIOSVendor:     "Dell Inc.",
		BIOSVersion:    "1.6.11",
		BIOSDate:       "10/31/2022",
	}

	BoardFramework13 = Options{
		BoardVendor:    "Framework Computer Inc",
		BoardName:      "Laptop 13 (AMD Ryzen AI 300)",
		BoardVersion:   "A4",
		SysVendor:      "Framework Computer Inc",
		ProductName:    "Laptop 13 (AMD Ryzen AI 300)",
		ProductVersion: "A4",
		ChassisType:    "10",
		BIOSVendor:     "INSYDE Corp.",
		BIOSVersion:    "03.05",
		BIOSDate:       "01/10/2025",
	}

	BoardFramework16 = Options{
		BoardVendor:    "Framework Computer Inc",
		BoardName:      "Laptop 16",
		BoardVersion:   "A2",
		SysVendor:      "Framework Computer Inc",
		ProductName:    "Laptop 16",
		ProductVersion: "A2",
		ChassisType:    "10",
		BIOSVendor:     "INSYDE Corp.",
		BIOSVersion:    "03.05",
		BIOSDate:       "11/04/2024",
	}

	BoardRPi5 = Options{
		BoardVendor:    "Raspberry Pi Ltd",
		BoardName:      "Raspberry Pi 5 Model B Rev 1.0",
		BoardVersion:   "",
		SysVendor:      "Raspberry Pi Ltd",
		ProductName:    "Raspberry Pi 5 Model B Rev 1.0",
		ProductVersion: "",
		ChassisType:    "1",
		BIOSVendor:     "EDK2",
		BIOSVersion:    "UEFI Firmware v0.3",
		BIOSDate:       "09/01/2023",
	}

	BoardMacBookPro14Asahi = Options{
		BoardVendor:    "Apple Inc.",
		BoardName:      "Mac14,9",
		BoardVersion:   "",
		SysVendor:      "Apple Inc.",
		ProductName:    "MacBook Pro (14-inch, 2021)",
		ProductVersion: "1.0",
		ChassisType:    "10",
		BIOSVendor:     "Apple Inc.",
		BIOSVersion:    "1038.100.628.0.0",
		BIOSDate:       "01/01/2021",
	}
)

// dmiFiles maps Options field values to their sysfs filenames, in
// declaration order for deterministic iteration in tests.
var dmiFiles = []struct {
	filename string
	value    func(*Options) string
}{
	{"board_vendor", func(o *Options) string { return o.BoardVendor }},
	{"board_name", func(o *Options) string { return o.BoardName }},
	{"board_version", func(o *Options) string { return o.BoardVersion }},
	{"sys_vendor", func(o *Options) string { return o.SysVendor }},
	{"product_name", func(o *Options) string { return o.ProductName }},
	{"product_version", func(o *Options) string { return o.ProductVersion }},
	{"chassis_type", func(o *Options) string { return o.ChassisType }},
	{"bios_vendor", func(o *Options) string { return o.BIOSVendor }},
	{"bios_version", func(o *Options) string { return o.BIOSVersion }},
	{"bios_date", func(o *Options) string { return o.BIOSDate }},
}

// Fake is a deterministic /sys/class/dmi/id sysfs tree for unit tests.
// Construct with New; do not copy after creation.
type Fake struct {
	root string
}

// New creates a Fake DMI sysfs tree rooted at a t.TempDir() directory.
// All ten DMI files are written at construction time; the tree is removed
// automatically when the test ends. opts may be nil, which is equivalent
// to &Options{} — all files are created containing only a newline.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	dir := t.TempDir()
	for _, entry := range dmiFiles {
		content := entry.value(opts) + "\n"
		path := filepath.Join(dir, entry.filename)
		if err := os.WriteFile(path, []byte(content), 0444); err != nil {
			t.Fatalf("fakedmi: write %s: %v", path, err)
		}
	}
	return &Fake{root: dir}
}

// Root returns the directory path that contains the DMI id files.
// Pass this to any function that accepts an injectable DMI root, such as
// hwmon.ReadDMI(f.Root()), setup's DMI reader, or autoload's sysfs root.
func (f *Fake) Root() string {
	return f.root
}
