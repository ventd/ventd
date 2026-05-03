package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
)

type stubCalibLoader struct {
	run *hwdb.CalibrationRun
	ok  bool
	err error
}

func (s *stubCalibLoader) Load(fp, bios string) (*hwdb.CalibrationRun, bool, error) {
	return s.run, s.ok, s.err
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_PresentAndFreshNoFacts(t *testing.T) {
	loader := &stubCalibLoader{
		run: &hwdb.CalibrationRun{
			BIOSVersion:  "0805",
			CalibratedAt: fixedNow().Add(-30 * 24 * time.Hour), // 30 days
		},
		ok: true,
	}
	det := NewCalibrationFreshnessDetector("abc123", "0805", loader, 0)

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("fresh + same BIOS emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_AbsentRecordWarning(t *testing.T) {
	det := NewCalibrationFreshnessDetector("abc123", "0805", &stubCalibLoader{ok: false}, 0)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for absent record, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "No calibration") {
		t.Errorf("Title doesn't say absent: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_BIOSDriftBlocker(t *testing.T) {
	loader := &stubCalibLoader{
		run: &hwdb.CalibrationRun{
			BIOSVersion:  "0805",
			CalibratedAt: fixedNow().Add(-30 * 24 * time.Hour),
		},
		ok: true,
	}
	// Running on BIOS 0806 now — different from stored 0805.
	det := NewCalibrationFreshnessDetector("abc123", "0806", loader, 0)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for BIOS drift, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker (RULE-HWDB-PR2-09)", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "BIOS") {
		t.Errorf("Title doesn't mention BIOS: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_OldButCurrentBIOSWarning(t *testing.T) {
	loader := &stubCalibLoader{
		run: &hwdb.CalibrationRun{
			BIOSVersion:  "0805",
			CalibratedAt: fixedNow().Add(-9 * 30 * 24 * time.Hour), // 9 months
		},
		ok: true,
	}
	det := NewCalibrationFreshnessDetector("abc123", "0805", loader, 0)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for stale record, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "days old") {
		t.Errorf("Title doesn't mention age: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_LoaderErrorWarns(t *testing.T) {
	det := NewCalibrationFreshnessDetector("abc123", "0805",
		&stubCalibLoader{err: errors.New("permission denied")}, 0)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for loader error, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "Cannot verify") {
		t.Errorf("Title doesn't say cannot-verify: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_NilLoaderNoOp(t *testing.T) {
	det := NewCalibrationFreshnessDetector("abc123", "0805", nil, 0)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("nil loader emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_RespectsContextCancel(t *testing.T) {
	det := NewCalibrationFreshnessDetector("abc123", "0805", &stubCalibLoader{}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}
