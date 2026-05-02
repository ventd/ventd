package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

type stubAppArmorProfiles struct {
	content string
	err     error
}

func (s stubAppArmorProfiles) ReadAll() ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []byte(s.content), nil
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_AppearedSinceStart(t *testing.T) {
	det := NewAppArmorProfileDriftDetector("ventd", "absent",
		stubAppArmorProfiles{content: "ventd (enforce)\nother-profile (complain)\n"})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for appeared profile, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", f.Severity)
	}
	if !strings.Contains(f.Title, "appeared") {
		t.Errorf("Title doesn't say appeared: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_DisappearedSinceStart(t *testing.T) {
	det := NewAppArmorProfileDriftDetector("ventd", "enforce",
		stubAppArmorProfiles{content: "other-profile (complain)\n"})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for disappeared profile, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "no longer loaded") {
		t.Errorf("Title doesn't say no-longer-loaded: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_ModeChanged(t *testing.T) {
	det := NewAppArmorProfileDriftDetector("ventd", "enforce",
		stubAppArmorProfiles{content: "ventd (complain)\n"})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for mode change, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "mode changed") {
		t.Errorf("Title doesn't say mode-changed: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_StableModeNoFacts(t *testing.T) {
	det := NewAppArmorProfileDriftDetector("ventd", "enforce",
		stubAppArmorProfiles{content: "ventd (enforce)\n"})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("stable mode emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_NoBaselineNoOp(t *testing.T) {
	// Empty ExpectedMode = no baseline pinned → don't fire.
	det := NewAppArmorProfileDriftDetector("ventd", "",
		stubAppArmorProfiles{content: "ventd (enforce)\n"})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("no-baseline emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_NoAppArmorGracefulDegrade(t *testing.T) {
	// AppArmor not loaded → kernel file absent.
	det := NewAppArmorProfileDriftDetector("ventd", "enforce",
		stubAppArmorProfiles{err: errors.New("no such file")})

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Errorf("AppArmor-absent should not propagate error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("AppArmor-absent emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_RespectsContextCancel(t *testing.T) {
	det := NewAppArmorProfileDriftDetector("ventd", "enforce",
		stubAppArmorProfiles{content: "ventd (enforce)\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestLookupAppArmorProfile_ParseFormats(t *testing.T) {
	content := strings.Join([]string{
		"ventd (enforce)",
		"docker-default (complain)",
		"unconfined (unconfined)",
		"",
		"some-malformed-line",
	}, "\n")

	cases := []struct {
		name        string
		wantMode    string
		wantPresent bool
	}{
		{"ventd", "enforce", true},
		{"docker-default", "complain", true},
		{"unconfined", "unconfined", true},
		{"missing", "", false},
	}
	for _, c := range cases {
		mode, present := lookupAppArmorProfile(content, c.name)
		if mode != c.wantMode || present != c.wantPresent {
			t.Errorf("lookup(%q) = (%q, %v), want (%q, %v)", c.name, mode, present, c.wantMode, c.wantPresent)
		}
	}
}
