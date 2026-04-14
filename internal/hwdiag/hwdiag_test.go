package hwdiag

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestSetReplacesByID(t *testing.T) {
	s := NewStore()
	s.Set(Entry{ID: "x", Component: ComponentCalibration, Severity: SeverityWarn, Summary: "first"})
	s.Set(Entry{ID: "x", Component: ComponentCalibration, Severity: SeverityError, Summary: "second"})

	snap := s.Snapshot(Filter{})
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry after replace, got %d", len(snap.Entries))
	}
	if snap.Entries[0].Summary != "second" {
		t.Errorf("replace did not take; summary=%q", snap.Entries[0].Summary)
	}
	if snap.Revision < 2 {
		t.Errorf("expected revision >= 2 after two writes, got %d", snap.Revision)
	}
}

func TestSetEmptyIDPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic on empty ID")
		}
	}()
	NewStore().Set(Entry{})
}

func TestRemoveAndClearComponent(t *testing.T) {
	s := NewStore()
	s.Set(Entry{ID: "a", Component: ComponentCalibration, Severity: SeverityInfo, Summary: "a"})
	s.Set(Entry{ID: "b", Component: ComponentCalibration, Severity: SeverityInfo, Summary: "b"})
	s.Set(Entry{ID: "c", Component: ComponentOOT, Severity: SeverityInfo, Summary: "c"})
	revBefore := s.Revision()

	s.Remove("missing") // idempotent, no bump
	if s.Revision() != revBefore {
		t.Errorf("Remove(missing) bumped revision")
	}

	s.ClearComponent(ComponentCalibration)
	snap := s.Snapshot(Filter{})
	if len(snap.Entries) != 1 || snap.Entries[0].ID != "c" {
		t.Fatalf("expected only c after ClearComponent, got %+v", snap.Entries)
	}

	// No-op ClearComponent does not bump revision.
	rev := s.Revision()
	s.ClearComponent(ComponentBoot)
	if s.Revision() != rev {
		t.Errorf("ClearComponent(empty) bumped revision")
	}
}

func TestSnapshotFilterAndOrder(t *testing.T) {
	s := NewStore()
	s.now = fixedClock(time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC))
	s.Set(Entry{ID: "calibration.z", Component: ComponentCalibration, Severity: SeverityInfo, Summary: "z"})
	s.Set(Entry{ID: "calibration.a", Component: ComponentCalibration, Severity: SeverityError, Summary: "a"})
	s.Set(Entry{ID: "oot.x", Component: ComponentOOT, Severity: SeverityWarn, Summary: "x"})

	// Component filter
	snap := s.Snapshot(Filter{Component: ComponentCalibration})
	if len(snap.Entries) != 2 {
		t.Fatalf("component filter: expected 2, got %d", len(snap.Entries))
	}
	// Ordered: component asc, severity desc, id asc
	if snap.Entries[0].ID != "calibration.a" || snap.Entries[1].ID != "calibration.z" {
		t.Errorf("sort order wrong: %+v", snap.Entries)
	}

	// Severity filter
	warnOnly := s.Snapshot(Filter{Severity: SeverityWarn})
	if len(warnOnly.Entries) != 1 || warnOnly.Entries[0].ID != "oot.x" {
		t.Errorf("severity filter: got %+v", warnOnly.Entries)
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	s := NewStore()
	fixed := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	s.now = fixedClock(fixed)
	s.Set(Entry{
		ID:        IDCalibrationFutureSchema,
		Component: ComponentCalibration,
		Severity:  SeverityWarn,
		Summary:   "calibration.json is newer than this build supports",
		Remediation: &Remediation{
			AutoFixID: AutoFixRecalibrate,
			Label:     "Recalibrate",
			Endpoint:  "/api/setup/start",
		},
		Affected: []string{"/sys/class/hwmon/hwmon0/pwm1"},
	})

	snap := s.Snapshot(Filter{})
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed struct {
		GeneratedAt time.Time `json:"generated_at"`
		Revision    uint64    `json:"revision"`
		Entries     []struct {
			ID          string   `json:"id"`
			Component   string   `json:"component"`
			Severity    string   `json:"severity"`
			Summary     string   `json:"summary"`
			Affected    []string `json:"affected"`
			Remediation *struct {
				AutoFixID string `json:"auto_fix_id"`
				Label     string `json:"label"`
				Endpoint  string `json:"endpoint"`
			} `json:"remediation"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !parsed.GeneratedAt.Equal(fixed) {
		t.Errorf("generated_at mismatch: %v", parsed.GeneratedAt)
	}
	if len(parsed.Entries) != 1 {
		t.Fatalf("entries: %d", len(parsed.Entries))
	}
	e := parsed.Entries[0]
	if e.ID != IDCalibrationFutureSchema || e.Component != "calibration" || e.Severity != "warn" {
		t.Errorf("entry shape wrong: %+v", e)
	}
	if e.Remediation == nil || e.Remediation.AutoFixID != string(AutoFixRecalibrate) {
		t.Errorf("remediation missing or wrong: %+v", e.Remediation)
	}
}

func TestStoreConcurrentSafety(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	const writers, reads = 8, 200
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < reads; i++ {
				s.Set(Entry{
					ID:        "w.entry",
					Component: ComponentHwmon,
					Severity:  SeverityInfo,
					Summary:   "x",
				})
			}
		}(w)
	}
	for r := 0; r < writers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < reads; i++ {
				_ = s.Snapshot(Filter{})
				_ = s.Revision()
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot(Filter{})
	if len(snap.Entries) != 1 {
		t.Errorf("concurrent Set with same ID should yield 1 entry, got %d", len(snap.Entries))
	}
}
