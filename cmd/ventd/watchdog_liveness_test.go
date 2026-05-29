package main

import (
	"testing"
	"time"
)

// TestControlLoopAlive is the policy half of RULE-WD-HEARTBEAT-LIVENESS: it
// decides when the systemd watchdog heartbeat should be withheld because the
// control loop has stalled, while never killing a daemon that has no control
// loop to stall (monitor-only / startup).
func TestControlLoopAlive(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	window := 8 * time.Second

	// Zero last-tick → alive: monitor-only (no controllers ever tick, so
	// MarkSensorRead never fires) or pre-first-tick startup. Never kill these.
	if !controlLoopAlive(time.Time{}, now, window, nil) {
		t.Error("zero last-tick must read as alive (monitor-only / startup)")
	}
	// A tick within the window → alive.
	if !controlLoopAlive(now.Add(-3*time.Second), now, window, nil) {
		t.Error("a tick within the window must read as alive")
	}
	// Exactly at the window edge → still alive (<=).
	if !controlLoopAlive(now.Add(-window), now, window, nil) {
		t.Error("a tick exactly at the window edge must read as alive")
	}
	// Older than the window → stalled: the heartbeat must withhold its ping so
	// WatchdogSec restarts the daemon.
	if controlLoopAlive(now.Add(-window-time.Second), now, window, nil) {
		t.Error("a tick older than the window must read as stalled (control loop wedged)")
	}
}
