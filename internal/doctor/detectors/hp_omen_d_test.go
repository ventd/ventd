// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
)

func TestHPOmenDetector_OmenFamilyEmitsInfoCard(t *testing.T) {
	d := NewHPOmenDetector()
	d.ReadDMIFn = func() (hwdb.DMI, error) {
		return hwdb.DMI{
			SysVendor:   "HP",
			ProductName: "OMEN by HP Laptop 16-wf0xxx",
		}, nil
	}
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("Probe: got %d facts, want 1", len(facts))
	}
	if facts[0].Severity != doctor.SeverityOK {
		t.Errorf("severity = %s, want OK", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "OMEN") {
		t.Errorf("title %q does not name OMEN", facts[0].Title)
	}
	if !strings.Contains(facts[0].Detail, "omen-fan") {
		t.Errorf("detail does not name omen-fan remediation")
	}
}

func TestHPOmenDetector_VictusFamilyEmitsCard(t *testing.T) {
	d := NewHPOmenDetector()
	d.ReadDMIFn = func() (hwdb.DMI, error) {
		return hwdb.DMI{
			SysVendor:   "HP",
			ProductName: "Victus by HP Laptop 16-r0xxx",
		}, nil
	}
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("Probe: got %d facts, want 1", len(facts))
	}
	if !strings.Contains(facts[0].Title, "Victus") {
		t.Errorf("title %q does not name Victus", facts[0].Title)
	}
}

func TestHPOmenDetector_HewlettPackardVendorRecognised(t *testing.T) {
	// Older BIOS revisions used "Hewlett-Packard" rather than "HP".
	d := NewHPOmenDetector()
	d.ReadDMIFn = func() (hwdb.DMI, error) {
		return hwdb.DMI{
			SysVendor:   "Hewlett-Packard",
			ProductName: "OMEN by HP Laptop 15-ce0xxx",
		}, nil
	}
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("Probe: expected exactly 1 fact for Hewlett-Packard vendor, got %d", len(facts))
	}
}

func TestHPOmenDetector_NonHPSilent(t *testing.T) {
	cases := []hwdb.DMI{
		{SysVendor: "Dell Inc.", ProductName: "XPS 13 9370"},
		{SysVendor: "LENOVO", ProductName: "ThinkPad T490"},
		{SysVendor: "ASUSTeK COMPUTER INC.", ProductName: "ROG Strix G15"},
		{SysVendor: "HP", ProductName: "EliteBook 840 G7"}, // HP but not Omen
		{SysVendor: "HP", ProductName: "Pavilion x360"},    // HP but not Omen / Victus
		{}, // empty DMI
	}
	for _, dmi := range cases {
		d := NewHPOmenDetector()
		d.ReadDMIFn = func() (hwdb.DMI, error) { return dmi, nil }
		facts, err := d.Probe(context.Background(), doctor.Deps{})
		if err != nil {
			t.Errorf("Probe(%+v): err=%v", dmi, err)
			continue
		}
		if len(facts) != 0 {
			t.Errorf("Probe(%+v): got %d facts, want 0", dmi, len(facts))
		}
	}
}

func TestHPOmenDetector_DMIReadErrorGracefullyDegrades(t *testing.T) {
	d := NewHPOmenDetector()
	d.ReadDMIFn = func() (hwdb.DMI, error) {
		return hwdb.DMI{}, errors.New("simulated permission denied on /sys/class/dmi/id")
	}
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Errorf("Probe: err=%v, want nil (graceful degrade)", err)
	}
	if len(facts) != 0 {
		t.Errorf("Probe: got %d facts on DMI read error, want 0 (DMI-fingerprint detector covers the system-wide case)", len(facts))
	}
}

func TestHPOmenDetector_RespectsContextCancel(t *testing.T) {
	d := NewHPOmenDetector()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Probe(ctx, doctor.Deps{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Probe(cancelled): err=%v, want context.Canceled", err)
	}
}

func TestHPOmenDetector_EntityHashStableAcrossProbes(t *testing.T) {
	d := NewHPOmenDetector()
	d.ReadDMIFn = func() (hwdb.DMI, error) {
		return hwdb.DMI{SysVendor: "HP", ProductName: "OMEN by HP Laptop 16-wf0xxx"}, nil
	}
	f1, _ := d.Probe(context.Background(), doctor.Deps{})
	f2, _ := d.Probe(context.Background(), doctor.Deps{})
	if f1[0].EntityHash != f2[0].EntityHash {
		t.Errorf("EntityHash drifted across probes: %s vs %s", f1[0].EntityHash, f2[0].EntityHash)
	}
}
