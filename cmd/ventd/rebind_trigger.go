package main

import (
	"log/slog"
	"sync/atomic"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwmon"
)

// newRebindTrigger builds the hwmon.RebindTrigger that runDaemon installs on
// the watcher. Issue #95 Option A: when a topology add event is promoted for
// a device whose StableDevice path matches a configured Fan/Sensor's
// HwmonDevice, signal restartCh so the controllers + web server tear down
// and re-enter runDaemon — ResolveHwmonPaths then picks the correct hwmonN.
//
// Isolated into its own function for unit-testability (see
// rebind_trigger_test.go). The rate limit lives in the watcher, so the
// trigger itself is a pure function of (liveCfg, key, fp).
func newRebindTrigger(
	liveCfg *atomic.Pointer[config.Config],
	restartCh chan<- struct{},
	logger *slog.Logger,
) hwmon.RebindTrigger {
	return func(key string, fp hwmon.DeviceFingerprint) {
		live := liveCfg.Load()
		if live == nil {
			return
		}
		if !configHasHwmonDevice(live, key) {
			logger.Debug("hwmon topology change: no configured fan/sensor bound to added device",
				"device", key, "chip_name", fp.ChipName)
			return
		}
		logger.Info("hwmon topology change matched a configured device; restarting to rebind controllers",
			"device", key, "chip_name", fp.ChipName)
		select {
		case restartCh <- struct{}{}:
		default:
			// A restart is already pending; dropping this signal is correct —
			// the in-flight restart will pick up the new topology.
		}
	}
}

// configHasHwmonDevice reports whether any hwmon-typed Fan or Sensor in cfg
// is bound (via HwmonDevice) to the given stable device path. Used by the
// rebind trigger to filter out add events for hwmon devices the daemon
// doesn't care about (e.g. a newly-inserted USB thermistor on a host with
// only mainboard chips configured).
func configHasHwmonDevice(cfg *config.Config, stablePath string) bool {
	for _, f := range cfg.Fans {
		if f.Type == "hwmon" && f.HwmonDevice != "" && f.HwmonDevice == stablePath {
			return true
		}
	}
	for _, s := range cfg.Sensors {
		if s.Type == "hwmon" && s.HwmonDevice != "" && s.HwmonDevice == stablePath {
			return true
		}
	}
	return false
}
