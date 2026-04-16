package config

import (
	"bytes"
	"encoding/json"
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
