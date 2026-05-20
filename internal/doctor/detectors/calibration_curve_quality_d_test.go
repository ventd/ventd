package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

type stubArtifactLoader struct {
	raw []byte
	err error
}

func (s stubArtifactLoader) ReadCalibrateArtifact() ([]byte, error) {
	return s.raw, s.err
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_NoArtifact_NoFacts(t *testing.T) {
	det := NewCalibrationCurveQualityDetector(stubArtifactLoader{raw: nil})
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("absent artifact must produce zero facts; got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_NilLoader_NoFacts(t *testing.T) {
	det := NewCalibrationCurveQualityDetector(nil)
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("nil loader must produce zero facts; got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_NoFlaggedFans_NoFacts(t *testing.T) {
	raw := []byte(`{
		"results": [
			{"pwm_path": "/sys/class/hwmon/hwmon0/pwm1", "max_rpm": 2000},
			{"pwm_path": "/sys/class/hwmon/hwmon0/pwm2", "max_rpm": 2200}
		]
	}`)
	det := NewCalibrationCurveQualityDetector(stubArtifactLoader{raw: raw})
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("zero non-monotonic flags must produce zero facts; got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_TwoFlagged_TwoFactsSorted(t *testing.T) {
	// pwm3 alphabetically after pwm1; the detector must sort by path
	// so output is stable across runs.
	raw := []byte(`{
		"results": [
			{"pwm_path": "/sys/class/hwmon/hwmon0/pwm3", "max_rpm": 2000, "non_monotonic_curve": true, "max_drop_rpm": 500},
			{"pwm_path": "/sys/class/hwmon/hwmon0/pwm1", "max_rpm": 2400, "non_monotonic_curve": true, "max_drop_rpm": 600},
			{"pwm_path": "/sys/class/hwmon/hwmon0/pwm2", "max_rpm": 2200}
		]
	}`)
	det := NewCalibrationCurveQualityDetector(stubArtifactLoader{raw: raw})
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts (pwm1 + pwm3), got %d", len(facts))
	}
	// Sorted: pwm1 first.
	if !strings.Contains(facts[0].Title, "pwm1") {
		t.Errorf("fact[0] title should reference pwm1; got %q", facts[0].Title)
	}
	if !strings.Contains(facts[1].Title, "pwm3") {
		t.Errorf("fact[1] title should reference pwm3; got %q", facts[1].Title)
	}
	for i, f := range facts {
		if f.Severity != doctor.SeverityWarning {
			t.Errorf("fact[%d] severity = %v, want Warning", i, f.Severity)
		}
		if f.Detector != "calibration_curve_quality" {
			t.Errorf("fact[%d] detector = %q", i, f.Detector)
		}
		if f.EntityHash == "" {
			t.Errorf("fact[%d] EntityHash empty", i)
		}
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_LoaderError_WarningFact(t *testing.T) {
	det := NewCalibrationCurveQualityDetector(stubArtifactLoader{err: errors.New("io read failed")})
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 1 || facts[0].Severity != doctor.SeverityWarning {
		t.Fatalf("loader error must produce one Warning fact; got %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_CalibrationCurveQuality_MalformedJSON_WarningFact(t *testing.T) {
	det := NewCalibrationCurveQualityDetector(stubArtifactLoader{raw: []byte(`{not json`)})
	facts, err := det.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe err = %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Title, "decode") {
		t.Fatalf("malformed JSON must produce one decode-warning Fact; got %+v", facts)
	}
}
