package detectors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/asus"
)

func asusTestDeps() doctor.Deps {
	return doctor.Deps{Now: func() time.Time { return time.Unix(0, 0) }}
}

// TestASUSFanCurves_ASUSHostEmitsCard pins the corpus-backed consumer: an ASUS
// DMI yields exactly one OK card naming the g-helper presets and the
// asus_custom_fan_curve kernel facts (RULE-ASUS-DOCTOR-01).
func TestASUSFanCurves_ASUSHostEmitsCard(t *testing.T) {
	cat, err := asus.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	d := &ASUSFanCurvesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{SysVendor: "ASUSTeK COMPUTER INC.", ProductName: "ROG Strix G16"}, nil
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), asusTestDeps())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("severity = %v, want OK", f.Severity)
	}
	// Names the modes read from the corpus + the kernel interface facts.
	for _, want := range []string{"silent", "balanced", "turbo", "asus_custom_fan_curve", "g-helper"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail missing %q, got: %s", want, f.Detail)
		}
	}
}

// TestASUSFanCurves_NonASUSSilent: a non-ASUS host emits no card.
func TestASUSFanCurves_NonASUSSilent(t *testing.T) {
	cat, _ := asus.LoadCatalog()
	d := &ASUSFanCurvesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{SysVendor: "Micro-Star International Co., Ltd.", ProductName: "MS-7D25"}, nil
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), asusTestDeps())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("non-ASUS host: got %d facts, want 0", len(facts))
	}
}

// TestASUSFanCurves_DMIErrorSilent: a DMI read error stays quiet.
func TestASUSFanCurves_DMIErrorSilent(t *testing.T) {
	cat, _ := asus.LoadCatalog()
	d := &ASUSFanCurvesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{}, context.DeadlineExceeded
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), asusTestDeps())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("DMI error: got %d facts, want 0", len(facts))
	}
}
