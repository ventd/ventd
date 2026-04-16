package config

import (
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
