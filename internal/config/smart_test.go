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
