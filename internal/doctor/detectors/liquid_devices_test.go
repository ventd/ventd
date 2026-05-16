// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

import (
	"regexp"
	"testing"
)

var hex4Pattern = regexp.MustCompile(`^[0-9a-f]{4}$`)

func TestLiquidDeviceCatalog_AllEntriesWellFormed(t *testing.T) {
	if len(liquidDeviceCatalog) == 0 {
		t.Fatalf("liquidDeviceCatalog is empty — vendor_remediation depends on at least one entry per supported vendor")
	}
	for i, d := range liquidDeviceCatalog {
		if !hex4Pattern.MatchString(d.VID) {
			t.Errorf("entry %d (%q): VID %q is not 4-char lowercase hex", i, d.Name, d.VID)
		}
		if !hex4Pattern.MatchString(d.PID) {
			t.Errorf("entry %d (%q): PID %q is not 4-char lowercase hex", i, d.Name, d.PID)
		}
		if d.Name == "" {
			t.Errorf("entry %d: Name is empty", i)
		}
		// Either KernelDriver OR UserspaceTool must be non-empty —
		// an entry with neither populated is ghost data.
		if d.KernelDriver == "" && d.UserspaceTool == "" {
			t.Errorf("entry %d (%q): both KernelDriver and UserspaceTool are empty — no actionable remediation", i, d.Name)
		}
	}
}

func TestLookupLiquidDeviceByVID_ReturnsAllVendorEntries(t *testing.T) {
	cases := []struct {
		vid       string
		atLeastN  int
	}{
		{"1e71", 4},  // NZXT — multiple Kraken + Smart Device entries
		{"1b1c", 3},  // Corsair — Commander family
		{"0c70", 2},  // Aquacomputer — D5 Next + Octo + Quadro
		{"1044", 1},  // Gigabyte — Waterforce
		{"dead", 0},  // unknown vendor
	}
	for _, tt := range cases {
		got := LookupLiquidDeviceByVID(tt.vid)
		if len(got) < tt.atLeastN {
			t.Errorf("LookupLiquidDeviceByVID(%q) returned %d entries, want >= %d", tt.vid, len(got), tt.atLeastN)
		}
		for _, d := range got {
			if d.VID != tt.vid {
				t.Errorf("LookupLiquidDeviceByVID(%q) returned entry with mismatched VID %q", tt.vid, d.VID)
			}
		}
	}
}

func TestLookupLiquidDevice_ExactMatchAndMiss(t *testing.T) {
	// Exact match: NZXT Kraken X3 series (1e71:2007).
	d := LookupLiquidDevice("1e71", "2007")
	if d == nil {
		t.Fatalf("LookupLiquidDevice(1e71, 2007) returned nil; expected NZXT Kraken entry")
	}
	if d.KernelDriver != "nzxt-kraken3" {
		t.Errorf("LookupLiquidDevice(1e71, 2007).KernelDriver = %q, want nzxt-kraken3", d.KernelDriver)
	}

	// Miss on a real vendor with wrong PID.
	if d := LookupLiquidDevice("1e71", "ffff"); d != nil {
		t.Errorf("LookupLiquidDevice(1e71, ffff) returned non-nil; expected miss")
	}

	// Miss on unknown vendor.
	if d := LookupLiquidDevice("dead", "beef"); d != nil {
		t.Errorf("LookupLiquidDevice(dead, beef) returned non-nil; expected miss")
	}
}

func TestRenderLiquidDeviceList_PopulatesDetailWhenVendorKnown(t *testing.T) {
	out := renderLiquidDeviceList("1e71")
	if out == "" {
		t.Fatalf("renderLiquidDeviceList(1e71) returned empty; NZXT catalog should produce a non-empty list")
	}
	// Must contain the canonical Kraken X3 device name.
	if !regexp.MustCompile(`Kraken X53 / X63 / X73`).MatchString(out) {
		t.Errorf("renderLiquidDeviceList(1e71) does not name the Kraken X3 family:\n%s", out)
	}
}

func TestRenderLiquidDeviceList_EmptyForUnknownVendor(t *testing.T) {
	if out := renderLiquidDeviceList("dead"); out != "" {
		t.Errorf("renderLiquidDeviceList(dead) = %q, want empty", out)
	}
}
