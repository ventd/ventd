package watchdog

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	"github.com/ventd/ventd/internal/hwmon"
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
//     restores pre-ventd state. Fan stays at the last-written PWM.
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
	// hwmonBe / nvmlBe are the FanBackend instances Restore delegates
	// into. Per-watchdog instances (constructed in New) keep the
	// backend's log output scoped to this watchdog's logger — the
	// pre-refactor restoreOne wrote through w.logger directly, and
	// the test suite asserts on that logger's buffer.
	hwmonBe *halhwmon.Backend
	nvmlBe  *halnvml.Backend
}

func New(logger *slog.Logger) *Watchdog {
	return &Watchdog{
		logger:  logger,
		hwmonBe: halhwmon.NewBackend(logger),
		nvmlBe:  halnvml.NewBackend(logger),
	}
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

// RestoreOne restores the most recently registered entry for pwmPath.
// No-op when no matching entry exists — a controller whose fan was
// deregistered concurrently should not cause a panic. Inherits the
// same per-entry panic-recovery envelope as Restore().
func (w *Watchdog) RestoreOne(pwmPath string) {
	w.mu.Lock()
	var matched entry
	var found bool
	for i := len(w.entries) - 1; i >= 0; i-- {
		if w.entries[i].pwmPath == pwmPath {
			matched = w.entries[i]
			found = true
			break
		}
	}
	w.mu.Unlock()

	if !found {
		return
	}
	w.restoreOne(matched)
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

// restoreOne dispatches a single entry's restore to the appropriate
// FanBackend. The backend owns the byte-level sysfs write and the
// operator-facing log lines; this method owns only the per-entry
// panic recovery envelope.
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

	var (
		be hal.FanBackend
		ch hal.Channel
	)
	if e.fanType == "nvidia" {
		be = w.nvmlBe
		ch = hal.Channel{
			ID:     e.pwmPath,
			Role:   hal.RoleGPU,
			Caps:   hal.CapRestore,
			Opaque: halnvml.State{Index: e.pwmPath},
		}
	} else {
		be = w.hwmonBe
		caps := hal.CapRestore
		if e.rpmTarget {
			caps |= hal.CapWriteRPMTarget
		} else {
			caps |= hal.CapWritePWM
		}
		ch = hal.Channel{
			ID:   e.pwmPath,
			Role: hal.RoleUnknown,
			Caps: caps,
			Opaque: halhwmon.State{
				PWMPath:    e.pwmPath,
				RPMTarget:  e.rpmTarget,
				OrigEnable: e.origEnable,
			},
		}
	}
	// Backend.Restore is expected to log operator-visible detail
	// itself. We intentionally swallow the returned error here: the
	// loop-level contract is that one failing entry never blocks the
	// remaining entries, and a backend that logged its failure has
	// already communicated it.
	_ = be.Restore(ch)
}
