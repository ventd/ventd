package config

import (
	"strings"
	"testing"
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
