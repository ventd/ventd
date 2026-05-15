package detectors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/iox"
)

// TestRULE_DOCTOR_DETECTOR_StateFreeSpace_HappyPathNoFacts pins the
// nil-probe path: a healthy filesystem produces zero facts so doctor
// stays quiet when there's nothing to report.
func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_HappyPathNoFacts(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		if path != "/state" || minBytes != 1024 {
			t.Fatalf("probe called with (%q, %d), want (/state, 1024)", path, minBytes)
		}
		return nil
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected zero facts on healthy probe; got %+v", facts)
	}
}

// TestRULE_DOCTOR_DETECTOR_StateFreeSpace_ConsumesInsufficientFreeSpaceSentinel
// is the load-bearing test for audit-doc S5: a wrapped
// iox.ErrInsufficientFreeSpace MUST surface as a Blocker fact reached
// via errors.Is, not string-matching, so RULE-WIZARD-RECOVERY-06's
// no-string-match principle stays intact.
func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_ConsumesInsufficientFreeSpaceSentinel(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 4096, func(path string, minBytes uint64) error {
		return fmt.Errorf("kv set refused: %w", iox.ErrInsufficientFreeSpace)
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected exactly 1 fact, got %d (%+v)", len(facts), facts)
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want SeverityBlocker", f.Severity)
	}
	if !strings.Contains(f.Title, "free-space floor") {
		t.Errorf("Title does not describe low free space: %q", f.Title)
	}
	if !strings.Contains(f.Detail, "RULE-STATE-12") {
		t.Errorf("Detail does not reference RULE-STATE-12: %q", f.Detail)
	}
	if len(f.Journal) != 1 || !strings.Contains(f.Journal[0], iox.ErrInsufficientFreeSpace.Error()) {
		t.Errorf("Journal does not preserve sentinel context: %+v", f.Journal)
	}
}

// TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MissingStateDirIsBenign pins
// the RULE-STATE-10 interaction: state.Open creates the directory at
// first start, so a missing path during doctor's pre-startup window
// must NOT surface as a fact.
func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MissingStateDirIsBenign(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		return fmt.Errorf("statfs %s: %w", path, os.ErrNotExist)
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("missing state dir must not surface as fact; got %+v", facts)
	}
}

// TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MeasurementErrorSurfaces pins
// the graceful-degrade path (RULE-DOCTOR-04): a statfs failure that is
// neither ENOENT nor the free-space sentinel surfaces as Warning so the
// operator knows the gate could not be evaluated.
func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MeasurementErrorSurfaces(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		return errors.New("statfs: permission denied")
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected exactly 1 fact, got %d (%+v)", len(facts), facts)
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want SeverityWarning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "Cannot measure") {
		t.Errorf("Title does not describe measurement failure: %q", facts[0].Title)
	}
}

// TestRULE_DOCTOR_DETECTOR_StateFreeSpace_RespectsContextCancel pins
// the RULE-DOCTOR-RUNNER-02 ctx-cancel contract: a cancelled ctx
// MUST short-circuit before the probe runs.
func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_RespectsContextCancel(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		t.Fatal("probe must not run after context cancellation")
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}
