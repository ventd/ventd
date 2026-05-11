package hwmon

import (
	"context"
	"log/slog"
	"time"
)

// DefaultSwapMonitorInterval is the cadence at which MonitorSwap
// re-resolves stored hwmon paths against their stable-device
// anchors. 10 minutes is slow enough that the syscall overhead is
// negligible (open + read of /sys/class/hwmon/* once per tick) and
// fast enough that an operator triggering a USB GPU hotplug or
// modprobe -r/-i sees the swap detected within a real-time
// remediation window. RULE-HWMON-SWAP-MONITOR.
const DefaultSwapMonitorInterval = 10 * time.Minute

// ChannelInput names one stored channel and its stable device
// anchor. The caller (typically the daemon at startup) builds the
// slice from the per-channel configuration captured at probe time.
//
// StoredPath: the sysfs path stamped into config at startup (e.g.
//
//	/sys/class/hwmon/hwmon2/pwm1). This is what the controller
//	writes through; the path may go stale across kernel module
//	reloads or hotplug events.
//
// StableDevice: the boot-persistent parent device directory (e.g.
//
//	/sys/devices/platform/nct6687.2608) as returned by
//	StableDevice() at startup. The path of THIS doesn't change
//	across reboots; it's the anchor we rebase StoredPath against.
type ChannelInput struct {
	StoredPath   string
	StableDevice string
}

// SwapDetection describes one re-resolution result. Changed=true
// means the hwmonN index moved since startup; ResolvedPath is the
// current sysfs path. Changed=false means either the StoredPath
// is still valid or no rebase candidate exists.
type SwapDetection struct {
	StoredPath   string
	StableDevice string
	ResolvedPath string
	Changed      bool
}

// ReResolveAll runs ResolvePath against every input and returns
// per-input SwapDetection. Pure helper — no I/O beyond what
// ResolvePath already does (os.Stat + filepath reads). Callable
// from any cadence the caller picks. RULE-HWMON-SWAP-MONITOR.
func ReResolveAll(inputs []ChannelInput) []SwapDetection {
	out := make([]SwapDetection, 0, len(inputs))
	for _, in := range inputs {
		resolved, changed := ResolvePath(in.StoredPath, in.StableDevice)
		out = append(out, SwapDetection{
			StoredPath:   in.StoredPath,
			StableDevice: in.StableDevice,
			ResolvedPath: resolved,
			Changed:      changed,
		})
	}
	return out
}

// SwapHandler is the callback shape MonitorSwap invokes on every
// SwapDetection where Changed==true. The handler is responsible for
// dispatching the remap into the daemon's per-channel state
// (controller's cache, watchdog entries, calibration manager). A nil
// handler is a clean no-op — MonitorSwap still logs every detection
// at WARN, so observability holds without a wired remap.
type SwapHandler func(SwapDetection)

// MonitorSwap is the long-running goroutine entry point. Every
// interval, it calls ReResolveAll(inputs) and dispatches the
// SwapHandler for every Changed=true result. Logs every change at
// WARN regardless of whether the handler is wired. Returns when
// ctx.Done fires; ticker is stopped on exit. RULE-HWMON-SWAP-MONITOR.
//
// The function is intentionally not a method on Backend so monitor-
// only deployments (no hwmon control path) can still benefit from
// the swap observability — e.g. a doctor sweep that wants to surface
// "hwmon path shifted since startup" as a recovery card.
//
// A nil or zero interval falls back to DefaultSwapMonitorInterval so
// callers that wire MonitorSwap without thinking about cadence still
// get a reasonable default. A nil logger falls through to
// slog.Default.
//
// Cancellation latency is bounded by interval: ctx.Done fires
// asynchronously with the ticker, and the next tick check handles
// the unwind. For a 10-minute default this means up to 10 minutes
// of grace; callers that need tighter shutdown should pass a
// shorter interval.
func MonitorSwap(
	ctx context.Context,
	inputs []ChannelInput,
	interval time.Duration,
	logger *slog.Logger,
	onSwap SwapHandler,
) {
	if interval <= 0 {
		interval = DefaultSwapMonitorInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	if len(inputs) == 0 {
		logger.Info("hwmon: swap monitor not started (no channels)")
		return
	}
	logger.Info("hwmon: swap monitor started",
		"channels", len(inputs),
		"interval_seconds", int(interval.Seconds()))
	defer logger.Info("hwmon: swap monitor stopped")

	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			detections := ReResolveAll(inputs)
			for _, d := range detections {
				if !d.Changed {
					continue
				}
				logger.Warn("hwmon: swap detected — sysfs path moved since startup",
					"stored", d.StoredPath,
					"resolved", d.ResolvedPath,
					"stable_device", d.StableDevice)
				if onSwap != nil {
					onSwap(d)
				}
			}
		}
	}
}
