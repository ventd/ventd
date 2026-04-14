package watchdog

import (
	"errors"
	"io/fs"
	"log/slog"
	"strconv"
	"sync"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
)

type entry struct {
	pwmPath    string
	fanType    string // "hwmon" or "nvidia"
	origEnable int    // hwmon only; -1 if unsupported
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
	if fanType == "nvidia" {
		e.origEnable = -1
	} else {
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
		if e.fanType == "nvidia" {
			idx, _ := strconv.ParseUint(e.pwmPath, 10, 32)
			if err := nvidia.ResetFanSpeed(uint(idx)); err != nil {
				w.logger.Error("watchdog: nvidia fan reset failed",
					"gpu_index", e.pwmPath, "err", err)
			} else {
				w.logger.Info("watchdog: nvidia fan restored to auto",
					"gpu_index", e.pwmPath)
			}
			continue
		}

		if e.origEnable < 0 {
			if writeErr := hwmon.WritePWM(e.pwmPath, 255); writeErr != nil {
				w.logger.Error("watchdog: pwm_enable unsupported and full-speed fallback failed",
					"path", e.pwmPath, "err", writeErr)
			} else {
				w.logger.Warn("watchdog: pwm_enable unsupported, wrote PWM=255 as safe fallback",
					"path", e.pwmPath)
			}
			continue
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
}
