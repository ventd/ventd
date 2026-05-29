package main

import (
	"log/slog"
	"time"
)

// watchdogStallFloor is the minimum control-loop-liveness window regardless of
// poll interval, so a very fast poll interval can't make the systemd watchdog
// trigger-happy on a single slow tick. RULE-WD-HEARTBEAT-LIVENESS.
const watchdogStallFloor = 8 * time.Second

// controlLoopAlive reports whether the control loop is making progress, for the
// systemd watchdog heartbeat gate (sdnotify.StartHeartbeat). It is the policy
// behind RULE-WD-HEARTBEAT-LIVENESS:
//
//   - last.IsZero() → alive. A zero timestamp means either monitor-only mode
//     (no controllers ever tick, so MarkSensorRead never fires) or pre-first-
//     tick startup. Neither has a control loop that can stall, so the daemon
//     must keep pinging; a startup hang is covered by systemd's start timeout.
//   - a controller ticked within window → alive.
//   - otherwise the control loop has stalled: return false so the heartbeat
//     withholds the WATCHDOG=1 ping and WatchdogSec restarts the daemon (fans →
//     firmware via OnFailure=ventd-recover). Logged at ERROR; logger is
//     nil-safe for tests.
func controlLoopAlive(last, now time.Time, window time.Duration, logger *slog.Logger) bool {
	if last.IsZero() || now.Sub(last) <= window {
		return true
	}
	if logger != nil {
		logger.Error("watchdog: control loop stalled — withholding systemd watchdog ping so the daemon restarts and fans return to firmware",
			"last_tick", last, "stalled_for", now.Sub(last), "window", window)
	}
	return false
}
