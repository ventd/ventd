package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// minimalSmartConfig builds a Config that passes the rest of validate()
// while exercising the SmartConfig validation paths. Borrows the shape
// from existing tests (sensors=empty + fans=empty + ...).
func minimalSmartConfig() *Config {
	c := Empty()
	c.Version = CurrentVersion
	return c
}

// RULE-CTRL-PRESET-01: SmartConfig.SmartPreset() returns the
// canonical preset name, normalising empty / unrecognised inputs to
// "balanced" with the second return reporting recognition.
func TestSmartPreset_NormalisationAndOK(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"", "balanced", true},
		{"silent", "silent", true},
		{"balanced", "balanced", true},
		{"performance", "performance", true},
		{"chaos", "balanced", false},
		{"Silent", "balanced", false}, // case-sensitive at the config layer
	}
	for _, tc := range cases {
		s := SmartConfig{Preset: tc.in}
		got, ok := s.SmartPreset()
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("SmartConfig{Preset:%q}.SmartPreset() = (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// RULE-CTRL-PRESET-03: SmartConfig.DBATarget validates inside [10, 80]
// dBA. Nil leaves the budget to be resolved from preset defaults at
// runtime; an explicit value overrides the preset.
func TestSmartConfig_DBATargetValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil_dba_target_accepted", func(t *testing.T) {
		c := minimalSmartConfig()
		// Default field is nil → no error.
		if err := validate(c); err != nil {
			t.Fatalf("nil DBATarget: %v", err)
		}
	})

	t.Run("dba_target_below_10_rejected", func(t *testing.T) {
		c := minimalSmartConfig()
		v := 5.0
		c.Smart.DBATarget = &v
		err := validate(c)
		if err == nil || !strings.Contains(err.Error(), "dba_target") {
			t.Fatalf("expected dba_target range error, got %v", err)
		}
	})

	t.Run("dba_target_above_80_rejected", func(t *testing.T) {
		c := minimalSmartConfig()
		v := 90.0
		c.Smart.DBATarget = &v
		err := validate(c)
		if err == nil || !strings.Contains(err.Error(), "dba_target") {
			t.Fatalf("expected dba_target range error, got %v", err)
		}
	})

	t.Run("dba_target_at_boundaries_accepted", func(t *testing.T) {
		for _, v := range []float64{10.0, 80.0, 25.0, 32.0, 45.0} {
			c := minimalSmartConfig()
			vv := v
			c.Smart.DBATarget = &vv
			if err := validate(c); err != nil {
				t.Errorf("DBATarget=%v: %v", v, err)
			}
		}
	})

	t.Run("dba_target_yaml_round_trip", func(t *testing.T) {
		// Operator-set value survives the YAML round-trip and lands
		// at the same numeric value (no truncation, no nil-vs-zero
		// confusion).
		v := 27.5
		in := SmartConfig{Preset: "balanced", DBATarget: &v}
		data, err := yaml.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var out SmartConfig
		if err := yaml.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.DBATarget == nil {
			t.Fatal("DBATarget lost on round-trip (nil after unmarshal)")
		}
		if *out.DBATarget != v {
			t.Errorf("DBATarget round-trip: got %v, want %v", *out.DBATarget, v)
		}
	})

	t.Run("dba_target_omitted_when_nil", func(t *testing.T) {
		// nil DBATarget must not surface in marshalled YAML — the
		// "use preset default" semantic depends on the absence
		// of the field, not on a sentinel zero value.
		in := SmartConfig{Preset: "silent"}
		data, err := yaml.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if strings.Contains(string(data), "dba_target") {
			t.Errorf("nil DBATarget leaked into YAML: %s", data)
		}
	})
}

// RULE-CTRL-PRESET-02: validate() rejects out-of-range Setpoints
// (physical bounds [10, 100] °C) but ACCEPTS unknown preset strings
// — the daemon emits a runtime WARN and falls back to balanced.
func TestSmartConfig_ValidationBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("setpoint below 10°C rejected", func(t *testing.T) {
		c := minimalSmartConfig()
		c.Smart.Setpoints = map[string]float64{"/sys/class/hwmon/hwmon0/pwm1": 5.0}
		err := validate(c)
		if err == nil || !strings.Contains(err.Error(), "out of physical range") {
			t.Fatalf("expected physical-range error, got %v", err)
		}
	})

	t.Run("setpoint above 100°C rejected", func(t *testing.T) {
		c := minimalSmartConfig()
		c.Smart.Setpoints = map[string]float64{"cpu_fan": 150.0}
		err := validate(c)
		if err == nil || !strings.Contains(err.Error(), "out of physical range") {
			t.Fatalf("expected physical-range error, got %v", err)
		}
	})

	t.Run("setpoint at exactly 10°C and 100°C accepted", func(t *testing.T) {
		c := minimalSmartConfig()
		c.Smart.Setpoints = map[string]float64{"a": 10.0, "b": 100.0}
		if err := validate(c); err != nil {
			t.Fatalf("boundary setpoints rejected: %v", err)
		}
	})

	t.Run("PresetWeightVector out of [0,1] rejected", func(t *testing.T) {
		c := minimalSmartConfig()
		c.Smart.PresetWeightVector = &[4]float64{0.5, 1.5, 0.5, 0.5}
		err := validate(c)
		if err == nil || !strings.Contains(err.Error(), "out of [0, 1]") {
			t.Fatalf("expected [0,1] bounds error, got %v", err)
		}
	})

	t.Run("unknown preset string is non-fatal at load", func(t *testing.T) {
		c := minimalSmartConfig()
		c.Smart.Preset = "ludicrous-mode"
		if err := validate(c); err != nil {
			t.Fatalf("unknown preset should not be fatal at load: %v", err)
		}
		// SmartPreset() should signal the fallback for the wiring layer.
		got, ok := c.Smart.SmartPreset()
		if got != "balanced" || ok {
			t.Fatalf("unknown preset: got (%q, %v), want (balanced, false)", got, ok)
		}
	})

	t.Run("known presets accepted at load", func(t *testing.T) {
		for _, p := range []string{"", "silent", "balanced", "performance"} {
			c := minimalSmartConfig()
			c.Smart.Preset = p
			if err := validate(c); err != nil {
				t.Errorf("preset %q rejected at load: %v", p, err)
			}
		}
	})
}

// YAML round-trip: a SmartConfig serialises and deserialises
// without data loss. Pinned because the daemon's web UI saves
// config via this path.
func TestSmartConfig_YAMLRoundTrip(t *testing.T) {
	t.Parallel()
	v := [4]float64{0.4, 0.3, 0.2, 0.1}
	in := SmartConfig{
		Preset:             "performance",
		PresetWeightVector: &v,
		Setpoints: map[string]float64{
			"/sys/class/hwmon/hwmon3/pwm1": 65.0,
			"chassis_fan":                  72.5,
		},
	}
	data, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out SmartConfig
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Preset != in.Preset {
		t.Errorf("Preset: got %q, want %q", out.Preset, in.Preset)
	}
	if out.PresetWeightVector == nil {
		t.Fatalf("PresetWeightVector lost on round-trip")
	}
	if *out.PresetWeightVector != *in.PresetWeightVector {
		t.Errorf("PresetWeightVector mismatch: got %v want %v",
			*out.PresetWeightVector, *in.PresetWeightVector)
	}
	if len(out.Setpoints) != 2 {
		t.Errorf("Setpoints len: got %d, want 2", len(out.Setpoints))
	}
}
