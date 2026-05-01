package main

import (
	"testing"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// TestBuildMarginalRuntime_NilWhenAbsent — RULE-CMB-WIRING-01.
//
// Monitor-only systems have no controllable channels; the marginal
// runtime should be nil and the daemon's Run goroutine never starts.
func TestBuildMarginalRuntime_NilWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir, silentLogger())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	if rt := buildMarginalRuntime(nil, st, "fp1", silentLogger()); rt != nil {
		t.Errorf("buildMarginalRuntime: expected nil for nil channels")
	}
	if rt := buildMarginalRuntime([]*probe.ControllableChannel{}, st, "fp1", silentLogger()); rt != nil {
		t.Errorf("buildMarginalRuntime: expected nil for zero-length channels")
	}
}

// TestBuildMarginalRuntime_RunOnce — RULE-CMB-WIRING-03.
//
// With ≥1 channel, the runtime is non-nil, and ShardCount returns
// 0 initially (shards admitted lazily).
func TestBuildMarginalRuntime_RunOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir, silentLogger())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	channels := []*probe.ControllableChannel{
		{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"},
	}
	rt := buildMarginalRuntime(channels, st, "fp1", silentLogger())
	if rt == nil {
		t.Fatalf("buildMarginalRuntime: expected non-nil for 1 channel")
	}
	// No shards admitted yet; cap-aware ShardCount returns 0.
	if got := rt.ShardCount(channels[0].PWMPath); got != 0 {
		t.Errorf("ShardCount = %d; want 0 (shards admitted lazily)", got)
	}
}
