package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateRejectsPWMPathOutsideSysfs(t *testing.T) {
	cases := []struct {
		name, path, want string
	}{
		{"etc_passwd", "/etc/passwd", "must start with"},
		{"tmp_file", "/tmp/pwm1", "must start with"},
		{"traversal_escape", "/sys/class/hwmon/../../../etc/passwd", "escapes sysfs"},
		{"bad_basename", "/sys/class/hwmon/hwmon0/in1_input", "basename"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Version: CurrentVersion,
				Fans: []Fan{
					{Name: "x", Type: "hwmon", PWMPath: tc.path},
				},
			}
			err := validate(cfg)
			if err == nil {
				t.Fatalf("expected error for %q", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsRealHwmonPWMPaths(t *testing.T) {
	cases := []string{
		"/sys/class/hwmon/hwmon0/pwm1",
		"/sys/devices/platform/nct6687.2608/hwmon/hwmon3/pwm2",
	}
	for _, p := range cases {
		cfg := &Config{
			Version: CurrentVersion,
			Fans: []Fan{
				{Name: "x", Type: "hwmon", PWMPath: p, MinPWM: 10, MaxPWM: 255},
			},
		}
		if err := validate(cfg); err != nil {
			t.Errorf("valid path %q rejected: %v", p, err)
		}
	}
}

func TestValidateLeavesNvidiaFanAlone(t *testing.T) {
	// nvidia fans use PWMPath as a GPU index, not a sysfs path.
	cfg := &Config{
		Version: CurrentVersion,
		Fans: []Fan{
			{Name: "gpu", Type: "nvidia", PWMPath: "0", MinPWM: 10, MaxPWM: 255},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("nvidia fan index rejected: %v", err)
	}
}

func TestValidateRejectsBadRPMPath(t *testing.T) {
	cfg := &Config{
		Version: CurrentVersion,
		Fans: []Fan{
			{
				Name:    "x",
				Type:    "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				RPMPath: "/etc/shadow",
				MinPWM:  10, MaxPWM: 255,
			},
		},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected rejection for bad rpm_path")
	}
}

// TestHwmonDynamicRebindDefaultsFalse pins the v0.3 escape-hatch default.
// A config with no `hwmon:` key (i.e. every v0.2.x on-disk config) must
// parse with Hwmon.DynamicRebind == false so the rebind path stays opt-
// in. If this breaks, existing deployments that upgrade to v0.3 would
// silently enable the re-exec behaviour — the whole point of the gate
// is to prevent exactly that.
func TestHwmonDynamicRebindDefaultsFalse(t *testing.T) {
	v02Config := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors: []
curves: []
controls: []
`)
	cfg, err := Parse(v02Config)
	if err != nil {
		t.Fatalf("parse v0.2.x config: %v", err)
	}
	if cfg.Hwmon.DynamicRebind {
		t.Fatalf("Hwmon.DynamicRebind = true on v0.2.x config; want false")
	}
}

// TestHwmonRoundTripPreservesNothingForZeroValue verifies the yaml
// `omitempty` on the Hwmon field — a config loaded from a v0.2.x YAML
// must marshal back to YAML without gaining a new `hwmon:` section.
// This keeps diffs quiet for upgraders who haven't opted in yet: the
// first `Save` after upgrade should not mutate the file shape.
func TestHwmonRoundTripPreservesNothingForZeroValue(t *testing.T) {
	v02Config := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors: []
curves: []
controls: []
`)
	cfg, err := Parse(v02Config)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "hwmon:") {
		t.Fatalf("round-trip emitted hwmon: key on zero-value Hwmon;\n---\n%s\n---", out)
	}
}

// TestConfigEmptyJSONShape pins the JSON contract of config.Empty().
// Every collection field must marshal as [] not null, because the web UI
// iterates these lists without null-guard code — a JSON null would abort
// the render pass with a TypeError. Regression guard for #135.
func TestConfigEmptyJSONShape(t *testing.T) {
	data, err := json.Marshal(Empty())
	if err != nil {
		t.Fatalf("marshal empty config: %v", err)
	}
	for _, field := range []string{"sensors", "fans", "curves", "controls"} {
		nullForm := []byte(`"` + field + `":null`)
		if bytes.Contains(data, nullForm) {
			t.Errorf("Empty() JSON contains %q; collection fields must marshal as []\n---\n%s\n---", nullForm, data)
		}
		arrayForm := []byte(`"` + field + `":[]`)
		if !bytes.Contains(data, arrayForm) {
			t.Errorf("Empty() JSON missing %q\n---\n%s\n---", arrayForm, data)
		}
	}
}

// TestConfigDefaultJSONShape covers the realistic first-boot path that
// goes through SavePasswordHash: Empty() is YAML-marshalled to disk, then
// re-read via Parse on the next boot, then served as JSON by /api/config.
// The full round-trip must preserve the [] shape so the UI sees arrays,
// not nulls, on the first dashboard load after setup. No Default() or
// New() constructor exists today; when one is added it must pass this
// same check.
func TestConfigDefaultJSONShape(t *testing.T) {
	yamlBytes, err := yaml.Marshal(Empty())
	if err != nil {
		t.Fatalf("marshal empty config to YAML: %v", err)
	}
	cfg, err := Parse(yamlBytes)
	if err != nil {
		t.Fatalf("re-parse YAML-marshalled Empty(): %v\n---\n%s\n---", err, yamlBytes)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal re-parsed config to JSON: %v", err)
	}
	for _, field := range []string{"sensors", "fans", "curves", "controls"} {
		nullForm := []byte(`"` + field + `":null`)
		if bytes.Contains(data, nullForm) {
			t.Errorf("round-tripped Empty() JSON contains %q\n---\n%s\n---", nullForm, data)
		}
	}
}

// TestHwmonDynamicRebindRoundTripEnabled covers the opt-in path: a
// config with `hwmon.dynamic_rebind: true` on disk must parse with
// the flag set, and re-marshal must emit the key so an operator who
// has opted in does not silently lose the setting on the next Save.
func TestHwmonDynamicRebindRoundTripEnabled(t *testing.T) {
	enabled := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
hwmon:
  dynamic_rebind: true
fans: []
sensors: []
curves: []
controls: []
`)
	cfg, err := Parse(enabled)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.Hwmon.DynamicRebind {
		t.Fatalf("Hwmon.DynamicRebind = false after parse; want true")
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "dynamic_rebind: true") {
		t.Fatalf("round-trip dropped dynamic_rebind=true;\n---\n%s\n---", out)
	}
	cfg2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !cfg2.Hwmon.DynamicRebind {
		t.Fatalf("re-parse lost Hwmon.DynamicRebind; got %+v", cfg2.Hwmon)
	}
}

// TestProfilesRoundTripPreservesNothingForZeroValue mirrors the
// Hwmon.DynamicRebind omitempty guard (#125) for the Session C 2e
// Profiles addition. A v0.2.x YAML must round-trip without gaining
// either `profiles:` or `active_profile:` keys so upgraders see a
// zero-diff first Save.
func TestProfilesRoundTripPreservesNothingForZeroValue(t *testing.T) {
	v02Config := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors: []
curves: []
controls: []
`)
	cfg, err := Parse(v02Config)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Profiles != nil {
		t.Fatalf("Profiles = %v after parse of v0.2.x YAML; want nil", cfg.Profiles)
	}
	if cfg.ActiveProfile != "" {
		t.Fatalf("ActiveProfile = %q after parse of v0.2.x YAML; want empty", cfg.ActiveProfile)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "profiles:") {
		t.Fatalf("round-trip emitted profiles: key on zero-value;\n---\n%s\n---", out)
	}
	if strings.Contains(string(out), "active_profile:") {
		t.Fatalf("round-trip emitted active_profile: key on zero value;\n---\n%s\n---", out)
	}
}

// TestProfilesRoundTripEnabled covers the opt-in path: a v0.3 YAML
// carrying a profiles block parses into a populated map and the
// re-marshalled YAML preserves the structure so an operator who has
// defined profiles does not lose them on the next Save.
func TestProfilesRoundTripEnabled(t *testing.T) {
	enabled := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors: []
curves: []
controls: []
profiles:
  silent:
    bindings:
      cpu_fan: cpu_linear_silent
      sys_fan1: case_linear_silent
  balanced:
    bindings:
      cpu_fan: cpu_linear_balanced
      sys_fan1: case_linear_balanced
active_profile: balanced
`)
	cfg, err := Parse(enabled)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("len(Profiles) = %d after parse; want 2", len(cfg.Profiles))
	}
	if cfg.ActiveProfile != "balanced" {
		t.Fatalf("ActiveProfile = %q; want balanced", cfg.ActiveProfile)
	}
	if got := cfg.Profiles["silent"].Bindings["cpu_fan"]; got != "cpu_linear_silent" {
		t.Fatalf("silent.bindings.cpu_fan = %q; want cpu_linear_silent", got)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "profiles:") {
		t.Fatalf("round-trip dropped profiles: key;\n---\n%s\n---", out)
	}
	if !strings.Contains(string(out), "active_profile: balanced") {
		t.Fatalf("round-trip dropped active_profile: balanced;\n---\n%s\n---", out)
	}
	cfg2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(cfg2.Profiles) != 2 {
		t.Fatalf("re-parse lost profiles; got len=%d", len(cfg2.Profiles))
	}
	if cfg2.ActiveProfile != "balanced" {
		t.Fatalf("re-parse lost ActiveProfile; got %q", cfg2.ActiveProfile)
	}
}

// TestValidateAllowStopGate pins the hwmon-safety rule 1 load-time gate:
// a fan with min_pwm: 0 and no allow_stop must be rejected, because the
// controller would otherwise silently skip every PWM=0 write at runtime.
// The table drives the pump-minimum rule as well — pumps must never stop,
// so pump_minimum: 0 is rejected regardless of allow_stop, and a pump
// with min_pwm: 0 is rejected by the pump floor before ever reaching the
// allow_stop gate.
func TestValidateAllowStopGate(t *testing.T) {
	cases := []struct {
		name    string
		fan     Fan
		wantErr string // substring; "" means accept
	}{
		{
			name: "minpwm_zero_allowstop_false_rejects",
			fan: Fan{
				Name: "case_fan", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  0, MaxPWM: 255,
				AllowStop: false,
			},
			wantErr: "min_pwm is 0 but allow_stop is false",
		},
		{
			name: "minpwm_zero_allowstop_true_accepts",
			fan: Fan{
				Name: "case_fan", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  0, MaxPWM: 255,
				AllowStop: true,
			},
		},
		{
			name: "minpwm_ten_allowstop_false_accepts",
			fan: Fan{
				Name: "case_fan", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  10, MaxPWM: 255,
				AllowStop: false,
			},
		},
		{
			name: "minpwm_ten_allowstop_true_accepts",
			fan: Fan{
				Name: "case_fan", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  10, MaxPWM: 255,
				AllowStop: true,
			},
		},
		{
			name: "pump_pumpminimum_zero_rejects_even_with_allowstop",
			fan: Fan{
				Name: "pump", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  50, MaxPWM: 255,
				IsPump: true, PumpMinimum: 0,
				AllowStop: true,
			},
			wantErr: "pump_minimum is 0",
		},
		{
			name: "pump_pumpminimum_zero_rejects_without_allowstop",
			fan: Fan{
				Name: "pump", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  50, MaxPWM: 255,
				IsPump: true, PumpMinimum: 0,
			},
			wantErr: "pump_minimum is 0",
		},
		{
			// Pumps must never stop: min_pwm=0 on a pump is rejected
			// by the pump floor check (floor >= MinPumpPWM=20), not by
			// the allow_stop gate. Pinning the behaviour so the pump
			// floor stays the binding constraint for pumps.
			name: "pump_minpwm_zero_rejects_at_pump_floor",
			fan: Fan{
				Name: "pump", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  0, MaxPWM: 255,
				IsPump: true, PumpMinimum: 10,
				AllowStop: true,
			},
			wantErr: "below pump floor",
		},
		{
			name: "pump_valid_with_explicit_minimum",
			fan: Fan{
				Name: "pump", Type: "hwmon",
				PWMPath: "/sys/class/hwmon/hwmon0/pwm1",
				MinPWM:  50, MaxPWM: 255,
				IsPump: true, PumpMinimum: 50,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Version: CurrentVersion,
				Fans:    []Fan{tc.fan},
			}
			err := validate(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q missing %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateAllowStopGateFixtures exercises the full Parse path against
// on-disk YAML fixtures, mirroring how the daemon loads an operator's
// hand-edited config. Keeping the fixtures under testdata/ makes the
// unsafe YAML shapes visible to a reader who runs `ls internal/config/
// testdata` without having to parse Go struct literals.
func TestValidateAllowStopGateFixtures(t *testing.T) {
	cases := []struct {
		file    string
		wantErr string // substring; "" means accept
	}{
		{file: "valid_all_safe.yaml", wantErr: ""},
		{file: "reject_minpwm_zero_no_allowstop.yaml", wantErr: "allow_stop is false"},
		{file: "reject_pump_minimum_zero.yaml", wantErr: "pump_minimum is 0"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, err = Parse(data)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("fixture %s: unexpected error: %v", tc.file, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("fixture %s: expected error containing %q, got nil", tc.file, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("fixture %s: error %q missing %q", tc.file, err, tc.wantErr)
			}
		})
	}
}

// TestCurveHysteresisSmoothingRoundTripPreservesNothingForZeroValue pins
// the backwards-compat contract introduced with PR 3a (Session D). A
// v0.2.x config with no hysteresis or smoothing must parse unchanged and
// re-marshal without emitting those keys — otherwise every existing
// on-disk config would start drifting a `hysteresis: 0` or
// `smoothing: 0s` line into diff output on the first save.
func TestCurveHysteresisSmoothingRoundTripPreservesNothingForZeroValue(t *testing.T) {
	v02Curve := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors:
  - name: cpu
    type: hwmon
    path: /sys/class/hwmon/hwmon0/temp1_input
curves:
  - name: cpu_linear
    type: linear
    sensor: cpu
    min_temp: 40
    max_temp: 80
    min_pwm: 30
    max_pwm: 255
controls: []
`)
	cfg, err := Parse(v02Curve)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Curves[0].Hysteresis != 0 {
		t.Fatalf("Hysteresis = %v; want 0", cfg.Curves[0].Hysteresis)
	}
	if cfg.Curves[0].Smoothing.Duration != 0 {
		t.Fatalf("Smoothing = %v; want 0", cfg.Curves[0].Smoothing.Duration)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "hysteresis:") {
		t.Fatalf("round-trip emitted hysteresis: on zero value;\n---\n%s\n---", out)
	}
	if strings.Contains(string(out), "smoothing:") {
		t.Fatalf("round-trip emitted smoothing: on zero value;\n---\n%s\n---", out)
	}
}

// TestCurveHysteresisSmoothingRoundTripEnabled covers the opt-in path:
// an operator who sets hysteresis and smoothing must get those values
// back unchanged after a Parse → Marshal → Parse cycle. Guards against
// a silent drop if either field gets a typo in its yaml struct tag.
func TestCurveHysteresisSmoothingRoundTripEnabled(t *testing.T) {
	withBoth := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors:
  - name: cpu
    type: hwmon
    path: /sys/class/hwmon/hwmon0/temp1_input
curves:
  - name: cpu_linear
    type: linear
    sensor: cpu
    min_temp: 40
    max_temp: 80
    min_pwm: 30
    max_pwm: 255
    hysteresis: 3.5
    smoothing: 4s
controls: []
`)
	cfg, err := Parse(withBoth)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Curves[0].Hysteresis != 3.5 {
		t.Fatalf("Hysteresis = %v; want 3.5", cfg.Curves[0].Hysteresis)
	}
	if cfg.Curves[0].Smoothing.String() != "4s" {
		t.Fatalf("Smoothing = %s; want 4s", cfg.Curves[0].Smoothing.Duration)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "hysteresis: 3.5") {
		t.Fatalf("round-trip dropped hysteresis: 3.5;\n---\n%s\n---", out)
	}
	if !strings.Contains(string(out), "smoothing: 4s") {
		t.Fatalf("round-trip dropped smoothing: 4s;\n---\n%s\n---", out)
	}
	cfg2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg2.Curves[0].Hysteresis != 3.5 {
		t.Fatalf("re-parse Hysteresis = %v; want 3.5", cfg2.Curves[0].Hysteresis)
	}
	if cfg2.Curves[0].Smoothing.String() != "4s" {
		t.Fatalf("re-parse Smoothing = %s; want 4s", cfg2.Curves[0].Smoothing)
	}
}

// TestPointsCurveValidation pins the validate() rules for the points
// type: min 2 anchors, sensor must exist, strictly increasing Temp
// after sort. The happy path produces a sorted, deduped slice the
// runtime can trust.
func TestPointsCurveValidation(t *testing.T) {
	cases := []struct {
		name    string
		points  []CurvePoint
		wantErr string
	}{
		{"valid 3 sorted", []CurvePoint{{Temp: 40, PWM: 50}, {Temp: 60, PWM: 150}, {Temp: 80, PWM: 250}}, ""},
		{"valid 2 sorted", []CurvePoint{{Temp: 40, PWM: 80}, {Temp: 80, PWM: 250}}, ""},
		{"single point rejected", []CurvePoint{{Temp: 40, PWM: 80}}, "at least 2 anchors"},
		{"empty rejected", []CurvePoint{}, "at least 2 anchors"},
		{"duplicate temps rejected", []CurvePoint{{Temp: 40, PWM: 50}, {Temp: 40, PWM: 150}}, "strictly increasing"},
		{"reversed sorts, ok", []CurvePoint{{Temp: 80, PWM: 250}, {Temp: 40, PWM: 50}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Version: CurrentVersion,
				Sensors: []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}},
				Curves: []CurveConfig{{
					Name: "p", Type: "points", Sensor: "cpu", Points: tc.points,
				}},
			}
			err := validate(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				// Verify the points are sorted ascending.
				for k := 1; k < len(cfg.Curves[0].Points); k++ {
					if cfg.Curves[0].Points[k].Temp <= cfg.Curves[0].Points[k-1].Temp {
						t.Errorf("points not ascending after validate: %v", cfg.Curves[0].Points)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestPointsCurveSensorMustExist pins the cross-reference check:
// points curves, like linear curves, must bind to a defined sensor.
func TestPointsCurveSensorMustExist(t *testing.T) {
	cfg := &Config{
		Version: CurrentVersion,
		Sensors: []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}},
		Curves: []CurveConfig{{
			Name: "ghost", Type: "points", Sensor: "missing",
			Points: []CurvePoint{{Temp: 40, PWM: 80}, {Temp: 80, PWM: 250}},
		}},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("undefined sensor: err = %v", err)
	}
}

// TestPointsCurveRoundTripPreservesOrder pins the YAML round-trip: an
// operator's carefully-ordered anchors survive Parse → Marshal → Parse
// in the same shape they were authored in.
func TestPointsCurveRoundTripPreservesOrder(t *testing.T) {
	yamlBody := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors:
  - name: cpu
    type: hwmon
    path: /sys/class/hwmon/hwmon0/temp1_input
curves:
  - name: pts
    type: points
    sensor: cpu
    points:
      - {temp: 30, pwm: 0}
      - {temp: 55, pwm: 100}
      - {temp: 75, pwm: 220}
controls: []
`)
	cfg, err := Parse(yamlBody)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(cfg.Curves[0].Points); got != 3 {
		t.Fatalf("Points len = %d; want 3", got)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "type: points") {
		t.Fatalf("re-marshal dropped type;\n%s", out)
	}
	if !strings.Contains(string(out), "temp: 30") || !strings.Contains(string(out), "pwm: 220") {
		t.Fatalf("re-marshal dropped anchors;\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
}

// TestMigrateCurvePWM_LegacyConfigUpgrades covers the v0.2.x →
// 3f migration: a YAML carrying only raw `min_pwm` parses, migrates
// into a populated `MinPWMPct`, and a Save → Load cycle re-emits only
// the percent form. Tolerates the ±1 rounding that survives a
// raw → pct → raw trip (e.g. 30/255 → 12% → 31/255).
func TestMigrateCurvePWM_LegacyConfigUpgrades(t *testing.T) {
	legacyYAML := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors:
  - name: cpu
    type: hwmon
    path: /sys/class/hwmon/hwmon0/temp1_input
curves:
  - name: cpu_linear
    type: linear
    sensor: cpu
    min_temp: 30
    max_temp: 80
    min_pwm: 30
    max_pwm: 255
controls: []
`)
	cfg, err := Parse(legacyYAML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Curves[0].MinPWMPct == nil || *cfg.Curves[0].MinPWMPct != 12 {
		t.Errorf("MinPWMPct = %v, want ptr(12)", cfg.Curves[0].MinPWMPct)
	}
	if cfg.Curves[0].MaxPWMPct == nil || *cfg.Curves[0].MaxPWMPct != 100 {
		t.Errorf("MaxPWMPct = %v, want ptr(100)", cfg.Curves[0].MaxPWMPct)
	}
	// After migration, raw and _pct both populated; validate saw the
	// raw form so the MinPWM<=MaxPWM gate held up.
	if cfg.Curves[0].MinPWM == 0 || cfg.Curves[0].MaxPWM == 0 {
		t.Errorf("raw fields should be mirrored from _pct, got min=%d max=%d", cfg.Curves[0].MinPWM, cfg.Curves[0].MaxPWM)
	}
}

// TestMigrateCurvePWM_SaveDropsLegacyFields confirms that after a
// Save → reload, the on-disk YAML carries only the `_pct` form.
// No regression of the round-trip drift other than the ±1 rounding
// inherent to 255<->100 scaling.
func TestMigrateCurvePWM_SaveDropsLegacyFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := Empty()
	cfg.Sensors = []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}}
	cfg.Curves = []CurveConfig{{
		Name: "cpu_linear", Type: "linear", Sensor: "cpu",
		MinTemp: 30, MaxTemp: 80, MinPWM: 30, MaxPWM: 255,
	}}
	saved, err := Save(cfg, path)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// Re-read the on-disk bytes; legacy keys must be absent.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "min_pwm:") {
		t.Errorf("YAML retained min_pwm: key after save;\n---\n%s\n---", raw)
	}
	if strings.Contains(string(raw), "max_pwm:") {
		t.Errorf("YAML retained max_pwm: key after save;\n---\n%s\n---", raw)
	}
	if !strings.Contains(string(raw), "min_pwm_pct") {
		t.Errorf("YAML missing min_pwm_pct key;\n---\n%s\n---", raw)
	}
	// saved reflects the migrated, re-parsed config.
	if saved.Curves[0].MinPWMPct == nil {
		t.Errorf("saved config missing MinPWMPct")
	}
}

// TestMigrateCurvePWM_BothSetPrefersPct covers the "YAML carries both
// `min_pwm` and `min_pwm_pct` and they disagree" edge case. The _pct
// side wins; the warning is observable to a `slog` handler we inject.
func TestMigrateCurvePWM_BothSetPrefersPct(t *testing.T) {
	bothYAML := []byte(`version: 1
poll_interval: 2s
web:
  listen: 0.0.0.0:9999
fans: []
sensors:
  - name: cpu
    type: hwmon
    path: /sys/class/hwmon/hwmon0/temp1_input
curves:
  - name: cpu_linear
    type: linear
    sensor: cpu
    min_temp: 30
    max_temp: 80
    min_pwm: 128
    min_pwm_pct: 30
    max_pwm_pct: 100
controls: []
`)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := Parse(bothYAML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// _pct wins. MinPWMPct stays 30; MinPWM is derived from it, not
	// from the disagreeing legacy 128. pctToRaw(30) = round(30*255/100) = 77.
	if cfg.Curves[0].MinPWMPct == nil || *cfg.Curves[0].MinPWMPct != 30 {
		t.Errorf("MinPWMPct = %v, want ptr(30)", cfg.Curves[0].MinPWMPct)
	}
	if cfg.Curves[0].MinPWM != 77 {
		t.Errorf("MinPWM = %d, want 77 (derived from min_pwm_pct=30)", cfg.Curves[0].MinPWM)
	}
	if !strings.Contains(buf.String(), "disagree") {
		t.Errorf("expected warning about disagreeing fields, got:\n%s", buf.String())
	}
}

// TestMigrateCurvePWM_PointsPct covers the per-anchor migration. An
// operator who hand-writes `pwm: 128` on a points curve gets a
// `pwm_pct: 50` back after Save.
func TestMigrateCurvePWM_PointsPct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "points.yaml")
	cfg := Empty()
	cfg.Sensors = []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}}
	cfg.Curves = []CurveConfig{{
		Name: "pts", Type: "points", Sensor: "cpu",
		Points: []CurvePoint{{Temp: 40, PWM: 0}, {Temp: 80, PWM: 255}},
	}}
	saved, err := Save(cfg, path)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Curves[0].Points[0].PWMPct == nil || *saved.Curves[0].Points[0].PWMPct != 0 {
		t.Errorf("anchor 0 PWMPct = %v, want ptr(0)", saved.Curves[0].Points[0].PWMPct)
	}
	if saved.Curves[0].Points[1].PWMPct == nil || *saved.Curves[0].Points[1].PWMPct != 100 {
		t.Errorf("anchor 1 PWMPct = %v, want ptr(100)", saved.Curves[0].Points[1].PWMPct)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "pwm_pct") {
		t.Errorf("YAML missing pwm_pct key;\n---\n%s\n---", raw)
	}
	// `pwm: 0` for anchor 0 is legal YAML with omitempty but the
	// round-trip must not carry the legacy key on any anchor.
	if strings.Contains(string(raw), "\n      pwm: 255") {
		t.Errorf("YAML retained legacy pwm: 255 after save;\n---\n%s\n---", raw)
	}
}

// TestMigrateCurvePWM_Idempotent — running migration twice leaves the
// config untouched after the first pass. Guards against a regression
// where the second pass re-computes percent from raw and drifts a
// value by the rounding error.
func TestMigrateCurvePWM_Idempotent(t *testing.T) {
	cfg := &Config{
		Curves: []CurveConfig{{
			Name: "cpu_linear", Type: "linear", Sensor: "cpu",
			MinTemp: 30, MaxTemp: 80, MinPWM: 30, MaxPWM: 255,
		}},
	}
	MigrateCurvePWMFields(cfg)
	firstMinPWM := cfg.Curves[0].MinPWM
	firstMinPct := *cfg.Curves[0].MinPWMPct
	MigrateCurvePWMFields(cfg)
	if cfg.Curves[0].MinPWM != firstMinPWM {
		t.Errorf("MinPWM drifted across two migrations: %d vs %d", firstMinPWM, cfg.Curves[0].MinPWM)
	}
	if *cfg.Curves[0].MinPWMPct != firstMinPct {
		t.Errorf("MinPWMPct drifted across two migrations: %d vs %d", firstMinPct, *cfg.Curves[0].MinPWMPct)
	}
}

// TestCurveHysteresisSmoothingRejectNegative pins the validate()
// sanity check. Negative values are never meaningful (a negative
// deadband would invert the gate; a negative EMA window is incoherent);
// reject at config load rather than letting them drift into the control
// loop.
func TestCurveHysteresisSmoothingRejectNegative(t *testing.T) {
	bad := &Config{
		Version: CurrentVersion,
		Sensors: []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}},
		Curves: []CurveConfig{{
			Name: "bad", Type: "linear", Sensor: "cpu",
			MinTemp: 40, MaxTemp: 80, MinPWM: 30, MaxPWM: 255,
			Hysteresis: -1,
		}},
	}
	if err := validate(bad); err == nil || !strings.Contains(err.Error(), "hysteresis") {
		t.Fatalf("negative hysteresis: err = %v", err)
	}
}
