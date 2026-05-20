package detectors

import (
	"context"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// stubCoolingLoader is a fixture loader for table-driven tests.
type stubCoolingLoader struct {
	body []byte
	err  error
	tdp  int
}

func (s stubCoolingLoader) ReadCalibrateArtifact() ([]byte, error) {
	return s.body, s.err
}
func (s stubCoolingLoader) ReadCPUTDPW() int { return s.tdp }

// TestCoolingCapacityDetector_NoCalibrateArtifactIsSilent — pre-
// calibrate hosts must not produce noise on the doctor surface.
func TestCoolingCapacityDetector_NoCalibrateArtifactIsSilent(t *testing.T) {
	d := NewCoolingCapacityDetector(stubCoolingLoader{body: nil, err: nil, tdp: 125})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected zero facts on no-calibrate, got %d", len(facts))
	}
}

// TestCoolingCapacityDetector_NoCPUTDPIsSilent — hosts without RAPL
// (AMD without amd_energy, virtualised) must not warn.
func TestCoolingCapacityDetector_NoCPUTDPIsSilent(t *testing.T) {
	body := []byte(`{"results":[{"pwm_path":"/sys/hwmon0/pwm1","max_rpm":1500}]}`)
	d := NewCoolingCapacityDetector(stubCoolingLoader{body: body, tdp: 0})
	facts, _ := d.Probe(context.Background(), doctor.Deps{})
	if len(facts) != 0 {
		t.Errorf("expected zero facts on no-TDP, got %d", len(facts))
	}
}

// TestCoolingCapacityDetector_AdequateCapacityIsSilent — when
// chassis exceeds CPU TDP with 25%+ margin, no warning.
func TestCoolingCapacityDetector_AdequateCapacityIsSilent(t *testing.T) {
	// Four 120 mm @ 1500 RPM fans = 4 * 30 W = 120 W capacity vs
	// 65 W CPU = comfortable margin.
	body := []byte(`{"results":[
		{"pwm_path":"/sys/hwmon0/pwm1","max_rpm":1500},
		{"pwm_path":"/sys/hwmon0/pwm2","max_rpm":1500},
		{"pwm_path":"/sys/hwmon0/pwm3","max_rpm":1500},
		{"pwm_path":"/sys/hwmon0/pwm4","max_rpm":1500}]}`)
	d := NewCoolingCapacityDetector(stubCoolingLoader{body: body, tdp: 65})
	facts, _ := d.Probe(context.Background(), doctor.Deps{})
	if len(facts) != 0 {
		t.Errorf("expected zero facts when capacity > 1.25 * TDP, got %d", len(facts))
	}
}

// TestCoolingCapacityDetector_TightCapacityWarns — when chassis
// capacity is below CPU TDP × 1.25 the detector emits a Warning.
// This is the issue #1285 acceptance: "doctor warns when measured
// load exceeds estimated capacity".
func TestCoolingCapacityDetector_TightCapacityWarns(t *testing.T) {
	// One 120 mm @ 1500 RPM fan = 30 W capacity vs 125 W CPU =
	// way under margin.
	body := []byte(`{"results":[{"pwm_path":"/sys/hwmon0/pwm1","max_rpm":1500}]}`)
	d := NewCoolingCapacityDetector(stubCoolingLoader{body: body, tdp: 125})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Detector != "cooling_capacity" {
		t.Errorf("detector = %q, want cooling_capacity", facts[0].Detector)
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("severity = %v, want Warning", facts[0].Severity)
	}
}

// TestCoolingCapacityDetector_NilLoaderIsNoOp — a runner that hasn't
// been given a loader must not panic and must emit zero facts.
func TestCoolingCapacityDetector_NilLoaderIsNoOp(t *testing.T) {
	d := NewCoolingCapacityDetector(nil)
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected zero facts on nil loader, got %d", len(facts))
	}
}
