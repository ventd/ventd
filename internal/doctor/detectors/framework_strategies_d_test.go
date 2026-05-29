package detectors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/framework"
)

func fwTestDeps() doctor.Deps {
	return doctor.Deps{Now: func() time.Time { return time.Unix(0, 0) }}
}

// TestFrameworkStrategies_FrameworkHostEmitsCard pins the corpus-backed
// consumer: a Framework DMI yields exactly one OK card naming the fw-fanctrl
// strategies + the default curve (RULE-FRAMEWORK-DOCTOR-01).
func TestFrameworkStrategies_FrameworkHostEmitsCard(t *testing.T) {
	cat, err := framework.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	d := &FrameworkStrategiesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{SysVendor: "Framework", ProductName: "Laptop 13"}, nil
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), fwTestDeps())
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
	// Names the default strategy and at least the canonical strategy set.
	if !strings.Contains(f.Detail, "lazy") || !strings.Contains(f.Detail, "agile") {
		t.Errorf("detail should name fw-fanctrl strategies, got: %s", f.Detail)
	}
	// Carries the corrected cros_ec_hwmon kernel facts, not the stale "6.7".
	if !strings.Contains(f.Detail, "cros_ec_hwmon") || !strings.Contains(f.Detail, "6.18") {
		t.Errorf("detail should carry cros_ec_hwmon + kernel 6.18 facts, got: %s", f.Detail)
	}
}

// TestFrameworkStrategies_NonFrameworkSilent: a non-Framework host emits no card.
func TestFrameworkStrategies_NonFrameworkSilent(t *testing.T) {
	cat, _ := framework.LoadCatalog()
	d := &FrameworkStrategiesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{SysVendor: "LENOVO", ProductName: "20XYZ"}, nil
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), fwTestDeps())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("non-Framework host: got %d facts, want 0", len(facts))
	}
}

// TestFrameworkStrategies_DMIErrorSilent: a DMI read error stays quiet rather
// than emitting a card that may not apply.
func TestFrameworkStrategies_DMIErrorSilent(t *testing.T) {
	cat, _ := framework.LoadCatalog()
	d := &FrameworkStrategiesDetector{
		ReadDMIFn: func() (hwdb.DMI, error) {
			return hwdb.DMI{}, context.DeadlineExceeded
		},
		Catalog: cat,
	}
	facts, err := d.Probe(context.Background(), fwTestDeps())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("DMI error: got %d facts, want 0", len(facts))
	}
}
