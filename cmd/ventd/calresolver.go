package main

import (
	"context"
	"log/slog"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	halasahi "github.com/ventd/ventd/internal/hal/asahi"
	halcrosec "github.com/ventd/ventd/internal/hal/crosec"
	halgpu "github.com/ventd/ventd/internal/hal/gpu"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halipmi "github.com/ventd/ventd/internal/hal/ipmi"
	halcorsair "github.com/ventd/ventd/internal/hal/liquid/corsair"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	halpwmsys "github.com/ventd/ventd/internal/hal/pwmsys"
)

// newChannelResolver returns the calibrate.ChannelResolver shared by
// runDaemon and runSetup. The closure dispatches on `fan.Type` to pick
// the correct HAL backend (nvml vs hwmon vs IPMI etc.) and resolves the
// fan's `PWMPath` to a live `(hal.FanBackend, hal.Channel)` pair via
// the package-level `hal.Resolve` registry.
//
// Without this resolver wired, `calibrate.DetectRPMSensor` returns
// `"detect: no channel resolver set for <pwm_path>"` for every channel
// and the wizard aborts with `"no fans detected"`. That was issue #1025
// — the daemon path wired the resolver inline at the controller setup
// site but the standalone `ventd -setup` CLI path never did, so the
// CLI wizard reliably failed on every host.
//
// Pre-conditions: callers MUST have invoked `registerHALBackends(...)`
// before this resolver fires, so `hal.Resolve` finds a registered
// backend for the `fan.Type` it receives. Both callers in this package
// (runDaemon, runSetup) do that.
func newChannelResolver() calibrate.ChannelResolver {
	return func(ctx context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error) {
		backendName := fan.Type
		if backendName == "nvidia" {
			backendName = halnvml.BackendName
		}
		return hal.Resolve(backendName + ":" + fan.PWMPath)
	}
}

// registerHALBackends populates the package-level HAL registry so
// `hal.Resolve` and `hal.Enumerate` can drive every Phase 2 surface
// (controller writes, calibration sweeps, opportunistic probing,
// diagnostics, setup wizard channel resolution).
//
// Shared between runDaemon (which registers before the controller
// setup phase) and runSetup (which previously skipped this step,
// breaking the CLI wizard end-to-end — issue #1025).
func registerHALBackends(logger *slog.Logger, enableGPUWrite bool) {
	hal.Register(halasahi.BackendName, halasahi.NewBackend(logger))
	halcorsair.RegisterAll(logger, halcorsair.ProbeOptions{})
	halgpu.RegisterAll(logger, halgpu.ProbeOptions{EnableGPUWrite: enableGPUWrite})
	hal.Register(halcrosec.BackendName, halcrosec.NewBackend(logger))
	hal.Register(halhwmon.BackendName, halhwmon.NewBackend(logger))
	hal.Register(halipmi.BackendName, halipmi.NewBackend(logger))
	hal.Register(halnvml.BackendName, halnvml.NewBackend(logger))
	hal.Register(halpwmsys.BackendName, halpwmsys.NewBackend(logger))
}
