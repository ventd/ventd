package curve

import (
	"testing"
)

// Note: TestLinearEvaluate's "missing sensor returns MaxPWM" case is a
// SAFETY INVARIANT — fail-open to full speed when sensor data is
// absent. Do not "optimise" Linear.Evaluate to fall back to a last
// known value; that would silently weaken the safety contract.

func TestLinearEvaluate(t *testing.T) {
	c := &Linear{
		SensorName: "cpu",
		MinTemp:    40,
		MaxTemp:    80,
		MinPWM:     50,
		MaxPWM:     200,
	}

	cases := []struct {
		name    string
		sensors map[string]float64
		want    uint8
	}{
		{"below min clamps to MinPWM", map[string]float64{"cpu": 20}, 50},
		{"at min returns MinPWM", map[string]float64{"cpu": 40}, 50},
		{"at max returns MaxPWM", map[string]float64{"cpu": 80}, 200},
		{"above max clamps to MaxPWM", map[string]float64{"cpu": 100}, 200},
		{"midpoint interpolates", map[string]float64{"cpu": 60}, 125},
		{"quarter interpolates rounded", map[string]float64{"cpu": 50}, 88},         // 50 + 0.25*150 = 87.5 → 88
		{"three-quarters interpolates rounded", map[string]float64{"cpu": 70}, 163}, // 50 + 0.75*150 = 162.5 → 163 (round half away from zero)
		{"missing sensor returns MaxPWM", map[string]float64{"gpu": 75}, 200},
		{"empty sensors returns MaxPWM", map[string]float64{}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Evaluate(tc.sensors); got != tc.want {
				t.Errorf("Evaluate(%v) = %d, want %d", tc.sensors, got, tc.want)
			}
		})
	}
}

func TestLinearDegenerateRange(t *testing.T) {
	// MinPWM == MaxPWM: every in-range input must return the same value.
	c := &Linear{SensorName: "cpu", MinTemp: 30, MaxTemp: 70, MinPWM: 128, MaxPWM: 128}
	for _, tempC := range []float64{10, 30, 50, 70, 100} {
		if got := c.Evaluate(map[string]float64{"cpu": tempC}); got != 128 {
			t.Errorf("Evaluate(cpu=%v) = %d, want 128", tempC, got)
		}
	}
}

func TestFixedEvaluate(t *testing.T) {
	c := &Fixed{Value: 128}
	// Fixed ignores its input entirely.
	inputs := []map[string]float64{
		nil,
		{},
		{"cpu": 20},
		{"cpu": 80, "gpu": 95},
	}
	for i, in := range inputs {
		if got := c.Evaluate(in); got != 128 {
			t.Errorf("case %d: Evaluate(%v) = %d, want 128", i, in, got)
		}
	}

	// Boundary values round-trip unchanged.
	for _, v := range []uint8{0, 1, 127, 255} {
		if got := (&Fixed{Value: v}).Evaluate(nil); got != v {
			t.Errorf("Fixed{%d}.Evaluate(nil) = %d", v, got)
		}
	}
}

func TestParseMixFunc(t *testing.T) {
	cases := []struct {
		in      string
		want    MixFunc
		wantErr bool
	}{
		{"max", MixMax, false},
		{"min", MixMin, false},
		{"average", MixAverage, false},
		{"MAX", 0, true},  // case-sensitive
		{"mean", 0, true}, // not an alias
		{"", 0, true},
		{"something else", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseMixFunc(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseMixFunc(%q) err = %v, wantErr = %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("ParseMixFunc(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestMixEvaluate(t *testing.T) {
	// Three fixed sources with distinct values so we can eyeball the aggregation.
	lo := &Fixed{Value: 40}
	mid := &Fixed{Value: 120}
	hi := &Fixed{Value: 200}

	cases := []struct {
		name    string
		fn      MixFunc
		sources []Curve
		want    uint8
	}{
		{"max of three", MixMax, []Curve{lo, mid, hi}, 200},
		{"max single source", MixMax, []Curve{mid}, 120},
		{"min of three", MixMin, []Curve{lo, mid, hi}, 40},
		{"min finds later-smaller value", MixMin, []Curve{hi, mid, lo}, 40},
		{"min single source", MixMin, []Curve{hi}, 200},
		{"average of three", MixAverage, []Curve{lo, mid, hi}, 120},                          // (40+120+200)/3
		{"average rounds up", MixAverage, []Curve{&Fixed{Value: 10}, &Fixed{Value: 11}}, 11}, // (10+11)/2 = 10.5 → 11
		{"average rounds down", MixAverage, []Curve{&Fixed{Value: 10}, &Fixed{Value: 12}}, 11},
		{"unknown func falls back to first source", MixFunc(999), []Curve{hi, lo}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Mix{Sources: tc.sources, Function: tc.fn}
			if got := c.Evaluate(nil); got != tc.want {
				t.Errorf("Evaluate = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMixEmptySources(t *testing.T) {
	// Any function with zero sources must not panic — contract is "return 0".
	for _, fn := range []MixFunc{MixMax, MixMin, MixAverage} {
		c := &Mix{Sources: nil, Function: fn}
		if got := c.Evaluate(nil); got != 0 {
			t.Errorf("empty Mix with fn=%d returned %d, want 0", fn, got)
		}
	}
}

func TestMixPropagatesSensors(t *testing.T) {
	// Mix must pass the sensor map unchanged to each source so nested curves
	// see the same readings.
	cpu := &Linear{SensorName: "cpu", MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255}
	gpu := &Linear{SensorName: "gpu", MinTemp: 40, MaxTemp: 80, MinPWM: 0, MaxPWM: 255}
	c := &Mix{Sources: []Curve{cpu, gpu}, Function: MixMax}

	sensors := map[string]float64{"cpu": 40, "gpu": 80}
	if got := c.Evaluate(sensors); got != 255 {
		t.Errorf("expected GPU at max to win: got %d, want 255", got)
	}
	sensors = map[string]float64{"cpu": 80, "gpu": 40}
	if got := c.Evaluate(sensors); got != 255 {
		t.Errorf("expected CPU at max to win: got %d, want 255", got)
	}
}
