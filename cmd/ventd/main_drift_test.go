package main

import (
	"testing"

	"github.com/ventd/ventd/internal/probe"
)

// TestBuildDriftDetector_NilWhenNoChannels binds RULE-DRIFT-WIRING-01:
// buildDriftDetector returns nil iff there are no controllable channels
// (monitor-only), and a non-nil detector otherwise.
func TestBuildDriftDetector_NilWhenNoChannels(t *testing.T) {
	if d := buildDriftDetector(nil, silentLogger()); d != nil {
		t.Errorf("buildDriftDetector: expected nil for nil channels")
	}
	if d := buildDriftDetector([]*probe.ControllableChannel{}, silentLogger()); d != nil {
		t.Errorf("buildDriftDetector: expected nil for zero-length channels")
	}
	if d := buildDriftDetector([]*probe.ControllableChannel{{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}}, silentLogger()); d == nil {
		t.Errorf("buildDriftDetector: expected non-nil for 1 channel")
	}
}
