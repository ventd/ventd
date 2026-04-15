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

// ─────────────────────────────────────────────────────────────────────
// REVIEW NOTES — phoenixdnb (PR #19, docs correctness sweep)
// ─────────────────────────────────────────────────────────────────────
//
// PR #19 is docs-only but its central claim concerns THIS file's
// behaviour. Reviewed on #19 branch; PR #19's description of actual
// behaviour was verified against the code directly.
//
// STATUS
//   No hardware verification required — docs-only PR. Notes below
//   live in code so future README authors see the real safety
//   envelope without having to reverse-engineer it again.
//
// CROSS-REF securitytodo.md — #19 doesn't claim to close a
// securitytodo item; the README drift it fixes is safety-facing, not
// security-facing. If securitytodo.md tracks user-facing safety
// attestations, cross-check there anyway.
//
// ACTUAL SAFETY ENVELOPE (what this watchdog really does)
//
//   COVERED:
//     - Graceful process exit (SIGTERM, SIGINT, context cancel).
//       Restore() runs from the defer chain in cmd/ventd/main.go.
//     - Panic INSIDE Restore() → recovered per-entry by restoreOne's
//       defer/recover, so one bad fan doesn't abort restore for the
//       rest. Covered by TestRestorePanicInOneEntryContinuesLoop.
//
//   NOT COVERED:
//     - SIGKILL (kill -9). Process dies before defers run; nothing
//       restores pwm_enable. Fan stays at last-written PWM.
//     - Kernel panic. Same — user-space defers never execute.
//     - Power loss. Obviously — no user-space code runs at all.
//     - Daemon crash via uncaught panic OUTSIDE a recovered frame.
//       The per-entry recover in restoreOne covers panics during
//       restore; panics during steady-state control are not caught
//       here (they are caught at the controller layer separately —
//       see internal/controller).
//
//   FALLBACK BEHAVIOUR (when origEnable is unknown / unsupported):
//     - hwmon non-rpm_target fans: WritePWM(path, 255) — full speed.
//       This is intentionally noisy: WARN log, not silent. See line
//       where "wrote PWM=255 as safe fallback" fires.
//     - hwmon rpm_target fans (pre-RDNA amdgpu): WriteFanTarget with
//       max_rpm. Same fail-safe pattern.
//     - nvidia fans: nvidia.ResetFanToAuto — hand control back to
//       the NVIDIA driver's autonomous curve. Never write PWM=255
//       on NVIDIA GPUs (the NVML abstraction doesn't expose a
//       matching primitive and auto is the safer equivalent).
//
// README-DRIFT (PR #19 addresses; do NOT drift again)
//
// Main-branch README.md claims "Hardware watchdog restores fan state
// on any exit path — signal, crash, panic, or power loss — within
// two seconds". That is WRONG in four ways:
//
//   1. "Hardware" → software. No BIOS/IPMI/hardware watchdog is
//      involved. Fan control reverts via user-space writes in defer
//      chains.
//   2. "Any exit path" → only graceful exits (see NOT COVERED).
//      SIGKILL, kernel panic, power loss do NOT trigger a restore.
//   3. "Within two seconds" → no such timer exists. Restore is
//      effectively instantaneous on graceful exit; there is no
//      upper bound encoded here. The two-second figure appears to
//      be aspirational.
//   4. Calls out "crash" and "panic" as if hardware restore covers
//      them. Panics INSIDE the restore path ARE recovered per entry
//      (tested); panics outside are handled elsewhere or kill the
//      process, in which case SIGKILL-like behaviour applies.
//
// If these four points ever need to be re-surfaced in user-facing
// docs (README, SECURITY.md, install.md), this block is the source
// of truth. Do not edit the README from this branch per the review
// rules; fix docs in the correctness-sweep PR (#19) only.
//
// ─────────────────────────────────────────────────────────────────────

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
