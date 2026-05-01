package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// silentLogger returns a slog logger that discards output. Used by
// wiring tests that want to assert behaviour without log noise.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildCouplingRuntime_NilOnNoChannels — RULE-CPL-WIRING-01.
//
// Monitor-only systems (Steam Deck, MiniPCs without exposed PWM)
// have no controllable channels. buildCouplingRuntime returns nil
// and the daemon never starts the coupling goroutine.
func TestBuildCouplingRuntime_NilOnNoChannels(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir, silentLogger())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	rt := buildCouplingRuntime(nil, st, "fp1", silentLogger())
	if rt != nil {
		t.Errorf("buildCouplingRuntime: expected nil for empty channels, got non-nil")
	}

	rt = buildCouplingRuntime([]*probe.ControllableChannel{}, st, "fp1", silentLogger())
	if rt != nil {
		t.Errorf("buildCouplingRuntime: expected nil for zero-length channels, got non-nil")
	}
}

// TestBuildCouplingRuntime_OneShardPerChannel — RULE-CPL-WIRING-02.
//
// Per spec §8.2 + R10 §10.5: exactly one shard per controllable
// channel, channelID is the PWM sysfs path (R24-stable identity),
// N_coupled = 0 (well-posed reduced-model case).
func TestBuildCouplingRuntime_OneShardPerChannel(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir, silentLogger())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	channels := []*probe.ControllableChannel{
		{PWMPath: "/sys/class/hwmon/hwmon0/pwm1", Polarity: "normal"},
		{PWMPath: "/sys/class/hwmon/hwmon0/pwm2", Polarity: "normal"},
		{PWMPath: "/sys/class/hwmon/hwmon0/pwm3", Polarity: "inverted"},
	}

	rt := buildCouplingRuntime(channels, st, "fp1", silentLogger())
	if rt == nil {
		t.Fatal("buildCouplingRuntime: expected non-nil for 3 channels, got nil")
	}

	for _, ch := range channels {
		if rt.Shard(ch.PWMPath) == nil {
			t.Errorf("Shard(%q) returned nil; expected registered shard", ch.PWMPath)
		}
	}
	if rt.Shard("/sys/class/hwmon/hwmon0/pwm-nonexistent") != nil {
		t.Errorf("Shard for unregistered path should return nil")
	}

	snaps := rt.SnapshotAll()
	if len(snaps) != len(channels) {
		t.Errorf("SnapshotAll: got %d, want %d", len(snaps), len(channels))
	}
}
