// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
)

func makeVendorDetector(dmi hwdb.DMI, usbVendors []string) *VendorRemediationDetector {
	usbSet := make(map[string]struct{}, len(usbVendors))
	for _, v := range usbVendors {
		usbSet[v] = struct{}{}
	}
	return &VendorRemediationDetector{
		ReadDMIFn:        func() (hwdb.DMI, error) { return dmi, nil },
		ReadUSBVendorsFn: func() map[string]struct{} { return usbSet },
	}
}

func TestVendorRemediation_AppleIntelMacEmitsCard(t *testing.T) {
	d := makeVendorDetector(hwdb.DMI{
		SysVendor:   "Apple Inc.",
		ProductName: "MacBookPro15,1",
	}, nil)
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(facts))
	}
	if !strings.Contains(facts[0].Detail, "applesmc") {
		t.Errorf("card does not name applesmc")
	}
	if !strings.Contains(facts[0].Detail, "mbpfan") {
		t.Errorf("card does not name mbpfan")
	}
}

func TestVendorRemediation_ClevoFamilyEmitsCard(t *testing.T) {
	cases := []hwdb.DMI{
		{SysVendor: "System76, Inc.", ProductName: "oryx-pro"},
		{SysVendor: "CLEVO", BoardVendor: "Notebook", ProductName: "X170KM-G"},
		{SysVendor: "TongFang", ProductName: "GMxNxxx"},
		{SysVendor: "SCHENKER", ProductName: "XMG NEO 15"},
	}
	for _, dmi := range cases {
		d := makeVendorDetector(dmi, nil)
		facts, err := d.Probe(context.Background(), doctor.Deps{})
		if err != nil {
			t.Fatalf("Probe(%+v): %v", dmi, err)
		}
		if len(facts) != 1 {
			t.Errorf("Probe(%+v): got %d facts, want 1 Clevo card", dmi, len(facts))
			continue
		}
		if !strings.Contains(facts[0].Detail, "clevo-indicator") {
			t.Errorf("Probe(%+v): card does not name clevo-indicator", dmi)
		}
	}
}

func TestVendorRemediation_NZXTUSBVendorEmitsCard(t *testing.T) {
	d := makeVendorDetector(hwdb.DMI{
		SysVendor:   "Generic Motherboard",
		ProductName: "Desktop",
	}, []string{"1e71"})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(facts))
	}
	if !strings.Contains(facts[0].Detail, "liquidctl") {
		t.Errorf("card does not name liquidctl")
	}
	if !strings.Contains(facts[0].Detail, "nzxt-kraken3") {
		t.Errorf("card does not name nzxt-kraken3")
	}
}

func TestVendorRemediation_CorsairUSBVendorEmitsCard(t *testing.T) {
	d := makeVendorDetector(hwdb.DMI{}, []string{"1b1c"})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(facts))
	}
	if !strings.Contains(facts[0].Detail, "Commander Core") {
		t.Errorf("card does not name Commander Core")
	}
}

func TestVendorRemediation_NoMatchesQuiet(t *testing.T) {
	d := makeVendorDetector(hwdb.DMI{
		SysVendor:   "Dell Inc.",
		ProductName: "XPS 13 9370",
	}, []string{"1d6b" /* generic Linux Foundation USB hub */})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("got %d facts on unrecognised host, want 0", len(facts))
	}
}

func TestVendorRemediation_MultipleMatchesEmitMultipleFacts(t *testing.T) {
	// A Mac with NZXT AIO over USB: both detectors should fire and
	// the runner aggregates them into one Report. Pin this so a
	// future "first match wins" regression fails CI.
	d := makeVendorDetector(hwdb.DMI{
		SysVendor:   "Apple Inc.",
		ProductName: "MacPro7,1",
	}, []string{"1e71"})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("got %d facts, want 2 (Apple Intel + NZXT)", len(facts))
	}
}

func TestVendorRemediation_RespectsContextCancel(t *testing.T) {
	d := NewVendorRemediationDetector()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Probe(ctx, doctor.Deps{})
	if err == nil {
		t.Errorf("Probe(cancelled): err=nil, want context.Canceled")
	}
}

func TestVendorRemediation_EntityHashUniquePerVendor(t *testing.T) {
	d := makeVendorDetector(hwdb.DMI{
		SysVendor:   "Apple Inc.",
		ProductName: "iMac20,1",
	}, []string{"1e71", "1b1c"})
	facts, _ := d.Probe(context.Background(), doctor.Deps{})
	seen := make(map[string]struct{}, len(facts))
	for _, f := range facts {
		if _, dup := seen[f.EntityHash]; dup {
			t.Errorf("duplicate EntityHash %q across vendor facts (Apple+NZXT+Corsair must hash distinctly)", f.EntityHash)
		}
		seen[f.EntityHash] = struct{}{}
	}
}

func TestIsAppleIntelMac_PrefixFilter(t *testing.T) {
	tests := []struct {
		dmi  hwdb.DMI
		want bool
	}{
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "MacBookPro15,1"}, true},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "MacBookAir7,2"}, true},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "iMac20,1"}, true},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "Macmini8,1"}, true},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "MacPro7,1"}, true},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: ""}, false},
		{hwdb.DMI{SysVendor: "Apple Inc.", ProductName: "iPad"}, false},
		{hwdb.DMI{SysVendor: "Dell Inc.", ProductName: "MacBookPro15,1"}, false},
	}
	for _, tt := range tests {
		got := isAppleIntelMac(tt.dmi)
		if got != tt.want {
			t.Errorf("isAppleIntelMac(%+v) = %v, want %v", tt.dmi, got, tt.want)
		}
	}
}

func TestIsClevoFamily_VendorVariants(t *testing.T) {
	tests := []struct {
		dmi  hwdb.DMI
		want bool
	}{
		{hwdb.DMI{SysVendor: "System76, Inc."}, true},
		{hwdb.DMI{SysVendor: "system76"}, true}, // case-insensitive
		{hwdb.DMI{SysVendor: "CLEVO"}, true},
		{hwdb.DMI{SysVendor: "Clevo"}, true},
		{hwdb.DMI{SysVendor: "TongFang"}, true},
		{hwdb.DMI{SysVendor: "Eluktronics"}, true},
		{hwdb.DMI{SysVendor: "Origin PC"}, true},
		{hwdb.DMI{SysVendor: "SCHENKER"}, true},
		{hwdb.DMI{SysVendor: "XMG"}, true},
		{hwdb.DMI{BoardVendor: "CLEVO", SysVendor: "Generic"}, true}, // board match
		{hwdb.DMI{SysVendor: "Dell Inc."}, false},
		{hwdb.DMI{SysVendor: "LENOVO"}, false},
		{hwdb.DMI{}, false},
	}
	for _, tt := range tests {
		got := isClevoFamily(tt.dmi)
		if got != tt.want {
			t.Errorf("isClevoFamily(%+v) = %v, want %v", tt.dmi, got, tt.want)
		}
	}
}
