package detectors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdiag"
)

type stubHwdiagStore struct {
	entries []hwdiag.Entry
}

func (s *stubHwdiagStore) Snapshot(f hwdiag.Filter) hwdiag.Snapshot {
	out := make([]hwdiag.Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if f.Component != "" && e.Component != f.Component {
			continue
		}
		out = append(out, e)
	}
	return hwdiag.Snapshot{
		GeneratedAt: time.Now(),
		Entries:     out,
	}
}

func TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_NoActiveNoFacts(t *testing.T) {
	det := NewExperimentalFlagsDetector(&stubHwdiagStore{entries: nil})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("no-flags-active emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_ActiveFlagYieldsOK(t *testing.T) {
	store := &stubHwdiagStore{
		entries: []hwdiag.Entry{
			{
				ID:        "experimental.amd_overdrive",
				Component: hwdiag.ComponentExperimental,
				Severity:  hwdiag.SeverityInfo,
				Detail:    "active (ppfeaturemask=0x4000)",
			},
		},
	}
	det := NewExperimentalFlagsDetector(store)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK (operator-opt-in, not nagging)", f.Severity)
	}
	if !strings.Contains(f.Title, "amd_overdrive") {
		t.Errorf("Title doesn't name the flag: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_OnlyExperimentalComponent(t *testing.T) {
	// hwdiag.Store hosts entries from many components. Detector
	// MUST filter on ComponentExperimental — RULE-DOCTOR-10.
	store := &stubHwdiagStore{
		entries: []hwdiag.Entry{
			{ID: "experimental.amd_overdrive", Component: hwdiag.ComponentExperimental, Severity: hwdiag.SeverityInfo},
			{ID: "calibration.future_schema", Component: hwdiag.Component("calibration"), Severity: hwdiag.SeverityWarn},
		},
	}
	det := NewExperimentalFlagsDetector(store)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("expected 1 fact (experimental only), got %d (%+v)", len(facts), facts)
	}
	if !strings.Contains(facts[0].Title, "amd_overdrive") {
		t.Errorf("non-experimental entry leaked into output: %+v", facts[0])
	}
}

func TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_NilStoreNoOp(t *testing.T) {
	det := NewExperimentalFlagsDetector(nil)
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe with nil store: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("nil store emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_RespectsContextCancel(t *testing.T) {
	det := NewExperimentalFlagsDetector(&stubHwdiagStore{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}
