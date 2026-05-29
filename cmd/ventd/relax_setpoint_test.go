package main

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestHwmonSensorPathForCurve: the curve→sensor→hwmon-path resolver used to
// derive a smart-mode fallback setpoint. Only hwmon sensors yield a path (they
// alone expose a sysfs crit register); non-hwmon, unknown, and dangling
// references yield "" so the channel runs reactive-only.
// RULE-CTRL-SMART-RELAX-FLOOR.
func TestHwmonSensorPathForCurve(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Sensors: []config.Sensor{
			{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon9/temp1_input"},
			{Name: "gpu", Type: "nvidia", Path: "0"},
		},
		Curves: []config.CurveConfig{
			{Name: "cpu-curve", Sensor: "cpu"},
			{Name: "gpu-curve", Sensor: "gpu"},
			{Name: "dangling", Sensor: "missing"},
		},
	}
	cases := []struct {
		name, curve, want string
	}{
		{"hwmon sensor resolves", "cpu-curve", "/sys/class/hwmon/hwmon9/temp1_input"},
		{"non-hwmon sensor empty", "gpu-curve", ""},
		{"unknown curve empty", "nope", ""},
		{"dangling sensor empty", "dangling", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hwmonSensorPathForCurve(cfg, tc.curve); got != tc.want {
				t.Fatalf("hwmonSensorPathForCurve(%q) = %q, want %q", tc.curve, got, tc.want)
			}
		})
	}
}
