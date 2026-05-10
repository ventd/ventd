package main

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestNewChannelResolver_NvidiaTypeRoutesToNVMLBackend pins the
// fan.Type → backend-name dispatch the resolver does. With a fan whose
// Type is "nvidia" (the value the v0.5.1 probe writes for NVIDIA GPU
// fans) the resolver MUST route to the "nvml" backend, not "nvidia"
// — hal.Resolve("nvidia:...") returns "backend not registered" because
// the GPU backend registers under the name "nvml".
//
// Issue #1025 fix invariant: the dispatch logic that previously lived
// inline at cmd/ventd/main.go:858 now lives in newChannelResolver and
// is shared with runSetup. A regression that drops the nvidia → nvml
// remap silently breaks every NVIDIA GPU fan calibration on the daemon
// path AND the CLI path simultaneously.
func TestNewChannelResolver_NvidiaTypeRoutesToNVMLBackend(t *testing.T) {
	r := newChannelResolver()
	fan := &config.Fan{Type: "nvidia", PWMPath: "/dev/null"}
	_, _, err := r(context.Background(), fan)
	if err == nil {
		t.Fatal("expected backend-not-registered error (no backend registered in test process); got nil")
	}
	got := err.Error()
	if !strings.Contains(got, `"nvml:/dev/null"`) {
		t.Errorf("expected error to reference dispatched id %q; got: %q", "nvml:/dev/null", got)
	}
	if strings.Contains(got, `"nvidia:/dev/null"`) {
		t.Errorf("dispatch did not remap nvidia → nvml; got: %q", got)
	}
}

// TestNewChannelResolver_HwmonTypePassThrough pins the non-nvidia
// pass-through: a fan whose Type is "hwmon" (the most common case)
// dispatches to "hwmon:<pwmPath>" verbatim.
func TestNewChannelResolver_HwmonTypePassThrough(t *testing.T) {
	r := newChannelResolver()
	fan := &config.Fan{Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmonX/pwm1"}
	_, _, err := r(context.Background(), fan)
	if err == nil {
		t.Fatal("expected backend-not-registered error (no backend registered in test process); got nil")
	}
	if got := err.Error(); !strings.Contains(got, `"hwmon:/sys/class/hwmon/hwmonX/pwm1"`) {
		t.Errorf("expected error to reference hwmon backend with verbatim path; got: %q", got)
	}
}
