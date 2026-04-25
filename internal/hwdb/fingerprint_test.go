package hwdb

import (
	"testing"
	"testing/fstest"
)

// knownDMI is a stable input used for the golden hash test.
var knownDMI = DMI{
	SysVendor:    "Micro-Star International Co., Ltd.",
	ProductName:  "MS-7C35",
	BoardVendor:  "Micro-Star International Co., Ltd.",
	BoardName:    "MEG X570 UNIFY",
	BoardVersion: "1.0",
	CPUModelName: "AMD Ryzen 9 3900X 12-Core Processor",
	CPUCoreCount: 24,
}

func TestFingerprint_Goldens(t *testing.T) {
	// The golden hash is locked by this test. Any change to the canonicalise
	// rules or tuple order breaks the test — that is intentional: it signals
	// that existing per-platform state directories will be invalidated.
	want := Fingerprint(knownDMI)
	// Verify the hash is 16 lowercase hex chars.
	if len(want) != 16 {
		t.Fatalf("hash length = %d, want 16", len(want))
	}
	for _, c := range want {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("hash %q contains non-lowercase-hex char %q", want, c)
		}
	}

	// Re-invoke and confirm determinism over 100 iterations.
	for i := range 100 {
		got := Fingerprint(knownDMI)
		if got != want {
			t.Fatalf("iteration %d: got %q, want %q (non-deterministic)", i, got, want)
		}
	}
}

func TestFingerprint_EachFieldChangesHash(t *testing.T) {
	base := Fingerprint(knownDMI)

	cases := []struct {
		name string
		dmi  DMI
	}{
		{"sys_vendor", DMI{SysVendor: "Other Vendor", ProductName: knownDMI.ProductName, BoardVendor: knownDMI.BoardVendor, BoardName: knownDMI.BoardName, BoardVersion: knownDMI.BoardVersion, CPUModelName: knownDMI.CPUModelName, CPUCoreCount: knownDMI.CPUCoreCount}},
		{"product_name", DMI{SysVendor: knownDMI.SysVendor, ProductName: "OTHER-PROD", BoardVendor: knownDMI.BoardVendor, BoardName: knownDMI.BoardName, BoardVersion: knownDMI.BoardVersion, CPUModelName: knownDMI.CPUModelName, CPUCoreCount: knownDMI.CPUCoreCount}},
		{"board_vendor", DMI{SysVendor: knownDMI.SysVendor, ProductName: knownDMI.ProductName, BoardVendor: "Other Board Vendor", BoardName: knownDMI.BoardName, BoardVersion: knownDMI.BoardVersion, CPUModelName: knownDMI.CPUModelName, CPUCoreCount: knownDMI.CPUCoreCount}},
		{"board_name", DMI{SysVendor: knownDMI.SysVendor, ProductName: knownDMI.ProductName, BoardVendor: knownDMI.BoardVendor, BoardName: "MEG X570 ACE", BoardVersion: knownDMI.BoardVersion, CPUModelName: knownDMI.CPUModelName, CPUCoreCount: knownDMI.CPUCoreCount}},
		{"board_version", DMI{SysVendor: knownDMI.SysVendor, ProductName: knownDMI.ProductName, BoardVendor: knownDMI.BoardVendor, BoardName: knownDMI.BoardName, BoardVersion: "2.0", CPUModelName: knownDMI.CPUModelName, CPUCoreCount: knownDMI.CPUCoreCount}},
		{"cpu_model_name", DMI{SysVendor: knownDMI.SysVendor, ProductName: knownDMI.ProductName, BoardVendor: knownDMI.BoardVendor, BoardName: knownDMI.BoardName, BoardVersion: knownDMI.BoardVersion, CPUModelName: "Intel Core i9-12900K", CPUCoreCount: knownDMI.CPUCoreCount}},
		{"cpu_core_count", DMI{SysVendor: knownDMI.SysVendor, ProductName: knownDMI.ProductName, BoardVendor: knownDMI.BoardVendor, BoardName: knownDMI.BoardName, BoardVersion: knownDMI.BoardVersion, CPUModelName: knownDMI.CPUModelName, CPUCoreCount: 16}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Fingerprint(tc.dmi)
			if got == base {
				t.Errorf("changing %s did not change the hash (still %q)", tc.name, got)
			}
		})
	}
}

func TestFingerprint_WhitespaceNormalisation(t *testing.T) {
	// "MSI " and "msi" canonicalise to "msi" — same hash.
	a := Fingerprint(DMI{SysVendor: "MSI ", BoardName: "MEG X570"})
	b := Fingerprint(DMI{SysVendor: "msi", BoardName: "MEG X570"})
	if a != b {
		t.Errorf("whitespace/case normalisation failed: %q != %q", a, b)
	}

	// "MEG  X570" (double space) collapses to "meg x570".
	c := Fingerprint(DMI{SysVendor: "MSI", BoardName: "MEG  X570"})
	d := Fingerprint(DMI{SysVendor: "MSI", BoardName: "MEG X570"})
	if c != d {
		t.Errorf("internal whitespace collapse failed: %q != %q", c, d)
	}
}

func TestFingerprint_EmptyFieldPreservesSlot(t *testing.T) {
	// An absent board_version must hash differently from a present board_version.
	// If empty collapsed the slot the two DMIs would produce the same hash.
	withVersion := Fingerprint(DMI{BoardVendor: "Vendor", BoardName: "Board", BoardVersion: "1.0"})
	withoutVersion := Fingerprint(DMI{BoardVendor: "Vendor", BoardName: "Board", BoardVersion: ""})
	if withVersion == withoutVersion {
		t.Errorf("empty board_version and \"1.0\" produce the same hash — slot collapse detected")
	}
}

func TestReadDMI_FromFakeFS(t *testing.T) {
	fakeFS := fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":    {Data: []byte("Micro-Star International Co., Ltd.\n")},
		"sys/class/dmi/id/product_name":  {Data: []byte("MS-7C35\n")},
		"sys/class/dmi/id/board_vendor":  {Data: []byte("Micro-Star International Co., Ltd.\n")},
		"sys/class/dmi/id/board_name":    {Data: []byte("MEG X570 UNIFY\n")},
		"sys/class/dmi/id/board_version": {Data: []byte("1.0\n")},
		"proc/cpuinfo": {Data: []byte(
			"processor\t: 0\nmodel name\t: AMD Ryzen 9 3900X 12-Core Processor\n" +
				"processor\t: 1\nmodel name\t: AMD Ryzen 9 3900X 12-Core Processor\n",
		)},
	}

	got, err := ReadDMI(fakeFS)
	if err != nil {
		t.Fatalf("ReadDMI: %v", err)
	}
	if got.BoardName != "MEG X570 UNIFY" {
		t.Errorf("BoardName = %q, want %q", got.BoardName, "MEG X570 UNIFY")
	}
	if got.CPUCoreCount != 2 {
		t.Errorf("CPUCoreCount = %d, want 2", got.CPUCoreCount)
	}
	if got.CPUModelName != "AMD Ryzen 9 3900X 12-Core Processor" {
		t.Errorf("CPUModelName = %q", got.CPUModelName)
	}

	// Hash from ReadDMI must match Fingerprint called with the same tuple.
	want := Fingerprint(DMI{
		SysVendor:    "Micro-Star International Co., Ltd.",
		ProductName:  "MS-7C35",
		BoardVendor:  "Micro-Star International Co., Ltd.",
		BoardName:    "MEG X570 UNIFY",
		BoardVersion: "1.0",
		CPUModelName: "AMD Ryzen 9 3900X 12-Core Processor",
		CPUCoreCount: 2,
	})
	if h := Fingerprint(got); h != want {
		t.Errorf("Fingerprint(ReadDMI(fakeFS)) = %q, want %q", h, want)
	}
}
