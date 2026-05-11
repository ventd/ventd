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

func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_HappyPathNoFacts(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		if path != "/state" || minBytes != 1024 {
			t.Fatalf("Check(%q, %d), want /state, 1024", path, minBytes)
		}
		return nil
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("happy path emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_ConsumesInsufficientFreeSpaceSentinel(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 4096, func(path string, minBytes uint64) error {
		return fmt.Errorf("kv set refused: %w", iox.ErrInsufficientFreeSpace)
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d (%+v)", len(facts), facts)
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if !strings.Contains(f.Title, "free-space floor") {
		t.Errorf("Title does not describe low free space: %q", f.Title)
	}
	if len(f.Journal) != 1 || !strings.Contains(f.Journal[0], iox.ErrInsufficientFreeSpace.Error()) {
		t.Errorf("Journal does not preserve sentinel context: %+v", f.Journal)
	}
}

func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MissingStateDirIsBenign(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		return fmt.Errorf("statfs %s: %w", path, os.ErrNotExist)
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("missing state dir should not surface as fact; got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_MeasurementErrorSurfaces(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		return errors.New("statfs permission denied")
	})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d (%+v)", len(facts), facts)
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "Cannot measure") {
		t.Errorf("Title does not describe measurement failure: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_StateFreeSpace_RespectsContextCancel(t *testing.T) {
	det := NewStateFreeSpaceDetector("/state", 1024, func(path string, minBytes uint64) error {
		t.Fatal("Check should not run after context cancellation")
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}
