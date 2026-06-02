package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// TestStuckSensorDetector binds RULE-DOCTOR-DETECTOR-STUCK-SENSOR: the detector
// emits one Warning fact per stuck sensor, stays silent when there are none, and
// a nil status fn is a no-op. The freeze judgement itself lives in the tracker
// (covered by internal/sensorfreeze); this pins the surfacing contract.
func TestStuckSensorDetector(t *testing.T) {
	src := func(s ...StuckSensor) StuckSensorStatusFn {
		return func() []StuckSensor { return s }
	}
	cases := []struct {
		name      string
		fn        StuckSensorStatusFn
		wantFacts int
	}{
		{"nil-source", nil, 0},
		{"none-stuck", src(), 0},
		{"one-stuck", src(StuckSensor{Name: "vrm", ValueC: 42, FrozenSeconds: 600, ReferenceRiseC: 30}), 1},
		{"two-stuck", src(
			StuckSensor{Name: "vrm", ValueC: 42, FrozenSeconds: 600, ReferenceRiseC: 30},
			StuckSensor{Name: "aux", ValueC: 50, FrozenSeconds: 900, ReferenceRiseC: 22},
		), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewStuckSensorDetector(tc.fn)
			facts, err := d.Probe(context.Background(), doctor.Deps{})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if len(facts) != tc.wantFacts {
				t.Fatalf("facts = %d, want %d", len(facts), tc.wantFacts)
			}
			for _, f := range facts {
				if f.Severity != doctor.SeverityWarning {
					t.Errorf("severity = %v, want Warning", f.Severity)
				}
				if f.Detector != "stuck_sensor" {
					t.Errorf("detector = %q, want stuck_sensor", f.Detector)
				}
			}
		})
	}
}

// TestStuckSensorDetector_DetailAndEntityHash pins that each stuck sensor gets a
// distinct EntityHash (so suppressing one card doesn't suppress another's) and
// an actionable body naming the sensor, the frozen value, and the swing seen
// elsewhere.
func TestStuckSensorDetector_DetailAndEntityHash(t *testing.T) {
	d := NewStuckSensorDetector(func() []StuckSensor {
		return []StuckSensor{
			{Name: "vrm", ValueC: 42.0, FrozenSeconds: 600, ReferenceRiseC: 30},
			{Name: "chipset", ValueC: 55.5, FrozenSeconds: 720, ReferenceRiseC: 18},
		}
	})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	if facts[0].EntityHash == facts[1].EntityHash {
		t.Error("per-sensor EntityHash must differ so suppression is sensor-scoped")
	}
	if !strings.Contains(facts[0].Detail, "vrm") || !strings.Contains(facts[0].Detail, "42.0") {
		t.Errorf("detail should name the sensor and frozen value; got: %s", facts[0].Detail)
	}
	if !strings.Contains(facts[0].Detail, "10 minutes") {
		t.Errorf("detail should render frozen duration in minutes; got: %s", facts[0].Detail)
	}
}
