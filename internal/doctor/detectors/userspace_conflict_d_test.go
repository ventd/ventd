package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

func stubSystemctl(states map[string]string, err error) SystemctlExec {
	return func(ctx context.Context, unit string) (string, error) {
		if err != nil {
			return "", err
		}
		s, ok := states[unit]
		if !ok {
			return "unknown", nil
		}
		return s, nil
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_AllInactiveNoFacts(t *testing.T) {
	det := NewUserspaceConflictDetector(stubSystemctl(map[string]string{
		"fancontrol.service":     "inactive",
		"thinkfan.service":       "inactive",
		"nbfc_service.service":   "inactive",
		"coolercontrold.service": "inactive",
		"liquidctl.service":      "inactive",
	}, nil))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("all-inactive emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_ActiveSurfacesAsBlocker(t *testing.T) {
	det := NewUserspaceConflictDetector(stubSystemctl(map[string]string{
		"fancontrol.service": "active",
	}, nil))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for active conflict, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if !strings.Contains(f.Title, "fancontrol.service") {
		t.Errorf("Title doesn't name the unit: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_MultipleActiveYieldsMultipleFacts(t *testing.T) {
	det := NewUserspaceConflictDetector(stubSystemctl(map[string]string{
		"fancontrol.service":     "active",
		"coolercontrold.service": "active",
	}, nil))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 2 {
		t.Errorf("expected 2 facts for two active conflicts, got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_FailedStateNotTreatedAsConflict(t *testing.T) {
	// "failed" means the unit attempted to start but couldn't —
	// not actively writing to fans. Skip.
	det := NewUserspaceConflictDetector(stubSystemctl(map[string]string{
		"fancontrol.service": "failed",
	}, nil))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("'failed' state should not surface as conflict; got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_NonSystemdGracefullyDegrades(t *testing.T) {
	// systemctl absent → non-systemd host (Alpine OpenRC / Void runit).
	// Per RULE-DOCTOR-04 the detector emits no facts and returns no
	// error — surfacing "systemctl missing" as a Fact would be cross-
	// class noise (not a fan-control problem).
	det := NewUserspaceConflictDetector(stubSystemctl(nil, errors.New("systemctl: not found")))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Errorf("non-systemd should not propagate error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("non-systemd emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_UserspaceConflict_RespectsContextCancel(t *testing.T) {
	det := NewUserspaceConflictDetector(stubSystemctl(map[string]string{"fancontrol.service": "active"}, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestUserspaceConflict_UnitListOverride(t *testing.T) {
	det := &UserspaceConflictDetector{
		Exec:  stubSystemctl(map[string]string{"my-custom-fan.service": "active"}, nil),
		Units: []string{"my-custom-fan.service"},
	}
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("custom unit list didn't fire; got %d facts", len(facts))
	}
}
