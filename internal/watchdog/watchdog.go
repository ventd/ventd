package watchdog

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
)

// Safety envelope — what this watchdog actually covers.
//
// Covered:
//   - Graceful process exit (SIGTERM, SIGINT, context cancel).
//     Restore() runs from the defer chain in cmd/ventd/main.go.
//   - Panic inside Restore() — recovered per-entry by restoreOne's
//     defer/recover, so one bad fan does not abort restore for the
//     rest. See TestRestorePanicInOneEntryContinuesLoop.
//
// NOT covered (do not surface these as guarantees in user-facing docs):
//   - SIGKILL (kill -9). Process dies before defers run; nothing
//     restores pwm_enable. Fan stays at the last-written PWM.
//   - Kernel panic. Same — user-space defers never execute.
//   - Power loss. Obviously — no user-space code runs at all.
//   - Daemon crash via uncaught panic outside a recovered frame.
//     The per-entry recover in restoreOne covers panics DURING
//     restore; panics during steady-state control are caught at
//     the controller layer (see internal/controller), not here.
//
// Fallback behaviour when origEnable is unknown or unsupported:
//   - hwmon non-rpm_target fans: WritePWM(path, 255) — full speed.
//     Intentionally noisy: WARN log, not silent.
//   - hwmon rpm_target fans (pre-RDNA amdgpu): WriteFanTarget with
//     max_rpm. Same fail-safe pattern.
//   - nvidia fans: nvidia.ResetFanToAuto — hand control back to the
//     NVIDIA driver's autonomous curve. Never write PWM=255 on
//     NVIDIA GPUs (the NVML abstraction does not expose a matching
//     primitive; auto is the safer equivalent).

type entry struct {
	pwmPath    string
	fanType    string // "hwmon" or "nvidia"
	origEnable int    // hwmon only; -1 if unsupported
	// rpmTarget is true when pwmPath is a fan*_target RPM-setpoint file
	// (pre-RDNA AMD). Dictates which sysfs attributes Restore reads/writes:
	// the enable file is pwm*_enable in the same directory, and the failsafe
	// on enable-missing is WriteFanTarget(fan*_max) rather than WritePWM(255),
	// since writing "255" to fan*_target would mean 255 RPM, not full speed.
	rpmTarget bool
}

type Watchdog struct {
	mu      sync.Mutex
	entries []entry
	logger  *slog.Logger
}

func New(logger *slog.Logger) *Watchdog {
	return &Watchdog{logger: logger}
}

func (w *Watchdog) Register(pwmPath string, fanType string) {
	e := entry{pwmPath: pwmPath, fanType: fanType}
	switch {
	case fanType == "nvidia":
		e.origEnable = -1
	case hwmon.IsRPMTargetPath(pwmPath):
		e.rpmTarget = true
		enablePath := hwmon.RPMTargetEnablePath(pwmPath)
		orig, err := hwmon.ReadPWMEnablePath(enablePath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable for rpm_target fan, will use max-rpm fallback on restore",
					"target_path", pwmPath, "enable_path", enablePath, "err", err)
			}
		}
		e.origEnable = orig
	default:
		orig, err := hwmon.ReadPWMEnable(pwmPath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable, will use full-speed fallback on restore",
					"path", pwmPath, "err", err)
			}
		}
		e.origEnable = orig
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, e)
}

// Deregister removes the most recently added entry matching pwmPath.
// Per-sweep registrations stack on top of the daemon-startup registration;
// Deregister pops the top one so the startup entry continues to drive the
// daemon-exit Restore. Idempotent: no-op when no matching entry exists.
func (w *Watchdog) Deregister(pwmPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.entries) - 1; i >= 0; i-- {
		if w.entries[i].pwmPath == pwmPath {
			w.entries = append(w.entries[:i], w.entries[i+1:]...)
			return
		}
	}
}

func (w *Watchdog) Restore() {
	w.mu.Lock()
	entries := make([]entry, len(w.entries))
	copy(entries, w.entries)
	w.mu.Unlock()

	for _, e := range entries {
		// Restore is the daemon's last-line safety net. A panic inside any one
		// entry must not abort the loop — remaining fans would be left at
		// whatever PWM the daemon last wrote, potentially unsafe.
		w.restoreOne(e)
	}
}

func (w *Watchdog) restoreOne(e entry) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("watchdog: restore panic recovered, continuing with next fan",
				"path", e.pwmPath,
				"hwmon_dir", filepath.Dir(e.pwmPath),
				"fan_type", e.fanType,
				"rpm_target", e.rpmTarget,
				"orig_enable", e.origEnable,
				"panic", fmt.Sprintf("%v", r))
		}
	}()

	if e.fanType == "nvidia" {
		idx, err := strconv.ParseUint(e.pwmPath, 10, 32)
		if err != nil {
			w.logger.Error("watchdog: nvidia gpu index parse failed, skipping restore",
				"gpu_index", e.pwmPath, "err", err)
			return
		}
		if err := nvidia.ResetFanSpeed(uint(idx)); err != nil {
			w.logger.Error("watchdog: nvidia fan reset failed",
				"gpu_index", e.pwmPath, "err", err)
		} else {
			w.logger.Info("watchdog: nvidia fan restored to auto",
				"gpu_index", e.pwmPath)
		}
		return
	}

	if e.rpmTarget {
		enablePath := hwmon.RPMTargetEnablePath(e.pwmPath)
		maxRPM := hwmon.ReadFanMaxRPM(e.pwmPath)
		if e.origEnable < 0 {
			if writeErr := hwmon.WriteFanTarget(e.pwmPath, maxRPM); writeErr != nil {
				w.logger.Error("watchdog: pwm_enable unsupported and max-rpm fallback failed",
					"target_path", e.pwmPath, "err", writeErr)
			} else {
				w.logger.Warn("watchdog: pwm_enable unsupported, wrote fan_target=max_rpm as safe fallback",
					"target_path", e.pwmPath, "rpm", maxRPM)
			}
			return
		}
		if err := hwmon.WritePWMEnablePath(enablePath, e.origEnable); err != nil {
			w.logger.Error("watchdog: failed to restore pwm_enable for rpm_target fan",
				"enable_path", enablePath, "value", e.origEnable, "err", err)
			if writeErr := hwmon.WriteFanTarget(e.pwmPath, maxRPM); writeErr != nil {
				w.logger.Error("watchdog: max-rpm fallback also failed",
					"target_path", e.pwmPath, "err", writeErr)
			}
		} else {
			w.logger.Info("watchdog: restored pwm_enable for rpm_target fan",
				"enable_path", enablePath, "value", e.origEnable)
		}
		return
	}

	if e.origEnable < 0 {
		if writeErr := hwmon.WritePWM(e.pwmPath, 255); writeErr != nil {
			w.logger.Error("watchdog: pwm_enable unsupported and full-speed fallback failed",
				"path", e.pwmPath, "err", writeErr)
		} else {
			w.logger.Warn("watchdog: pwm_enable unsupported, wrote PWM=255 as safe fallback",
				"path", e.pwmPath)
		}
		return
	}

	if err := hwmon.WritePWMEnable(e.pwmPath, e.origEnable); err != nil {
		w.logger.Error("watchdog: failed to restore pwm_enable",
			"path", e.pwmPath, "value", e.origEnable, "err", err)
		if writeErr := hwmon.WritePWM(e.pwmPath, 255); writeErr != nil {
			w.logger.Error("watchdog: full-speed fallback also failed",
				"path", e.pwmPath, "err", writeErr)
		}
	} else {
		w.logger.Info("watchdog: restored pwm_enable",
			"path", e.pwmPath, "value", e.origEnable)
	}
}
