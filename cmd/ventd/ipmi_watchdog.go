package main

import (
	"context"
	"log/slog"

	"github.com/ventd/ventd/internal/hal"
	halipmi "github.com/ventd/ventd/internal/hal/ipmi"
	"github.com/ventd/ventd/internal/watchdog"
)

// registerIPMIWatchdogEntries enumerates IPMI channels via the HAL
// registry and routes each one through the watchdog so the
// cross-cutting RULE-WD-RESTORE-EXIT safety contract covers IPMI
// fans (issue #1043). Without this wiring, IPMI restore lives
// entirely inside internal/hal/ipmi's own Close path — which the
// daemon-exit defer wd.Restore() never visits, leaving the daemon's
// exit-path promise ("every fan restored to firmware auto") silently
// untrue for IPMI hosts.
//
// The IPMI backend's Restore method is the canonical implementation;
// this function only routes the call via the watchdog's panic-
// recovery + per-entry budget envelope. A future IPMI vendor (HPE
// iLO Advanced licence, custom OEM) automatically picks up the
// watchdog's safety guarantees by extending the backend's Restore
// dispatch — no change here required.
//
// Errors enumerating IPMI channels are non-fatal: the daemon
// proceeds without IPMI fan control on hosts where IPMI is
// unsupported or the BMC is unresponsive. The IPMI backend's
// Enumerate is responsible for DMI-gating and surfacing its own
// diagnostics.
func registerIPMIWatchdogEntries(wd *watchdog.Watchdog, logger *slog.Logger) {
	be, ok := hal.Backend(halipmi.BackendName)
	if !ok {
		// Backend not registered (registerHALBackends wasn't called
		// or wd.Register fired on a daemon path that doesn't run
		// hal.Register). The watchdog still covers hwmon + NVML; this
		// is the silently-fall-through branch.
		return
	}
	channels, err := be.Enumerate(context.Background())
	if err != nil {
		logger.Warn("watchdog: ipmi enumerate failed; IPMI fans will not be routed through watchdog exit",
			"err", err)
		return
	}
	for _, ch := range channels {
		ch := ch // capture for closure
		channelID := halipmi.BackendName + ":" + ch.ID
		wd.RegisterIPMI(channelID, func() error {
			return be.Restore(ch)
		})
	}
	if len(channels) > 0 {
		logger.Info("watchdog: ipmi channels routed through watchdog exit",
			"count", len(channels))
	}
}
