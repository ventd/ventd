// Package hwmon is the hwmon-sysfs implementation of hal.FanBackend.
// It wraps internal/hwmon — it never re-implements the sysfs primitives,
// so all existing hwmon-safety guarantees (clamping in the controller,
// pwm_enable save/restore via the watchdog, PWM=0 sentinel cooperation
// in calibration) continue to apply unchanged.
package hwmon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hwmon"
)

// BackendName is the registry tag applied to channels produced by this
// backend. Kept as a package-level constant so callers (main.go,
// watchdog) can reference it without importing hal just for the string.
const BackendName = "hwmon"

// State is the per-channel payload carried in hal.Channel.Opaque.
// Exported so the watchdog and controller can construct channels for
// fans they discovered via config rather than via Enumerate (see the
// package comment on why Enumerate-driven wiring is future-facing).
type State struct {
	// PWMPath is the full sysfs path to the pwm*N or fan*N_target
	// file that receives duty-cycle / RPM-setpoint writes.
	PWMPath string
	// RPMTarget is true when PWMPath is a fan*N_target file (pre-RDNA
	// AMD) rather than a pwm*N file. Changes the Write dispatch and
	// the Restore fallback.
	RPMTarget bool
	// MaxRPM is the cached fan*_max value for RPM-target channels.
	// Opt-4: the controller reads fan*_max once at startup and embeds
	// it here so Write can skip the per-tick sysfs round-trip.
	// Zero means "not cached" — Write falls back to hwmon.ReadFanMaxRPM.
	MaxRPM int
	// OrigEnable is the pre-ventd pwm_enable value captured by the
	// watchdog before the controller flipped the channel into manual
	// mode. -1 means "captured nothing usable" — Restore falls back
	// to the max-speed safety net. Ignored by Write.
	OrigEnable int
}

// Backend is the hwmon implementation of hal.FanBackend. Construct
// one per consumer (controller, watchdog) so logging is scoped to
// the caller.
type Backend struct {
	logger   *slog.Logger
	acquired sync.Map // key: pwmPath (string), value: struct{}
}

// NewBackend constructs a Backend that logs through the given slog
// logger. A nil logger falls through to slog.Default so callers that
// don't wire logging still see messages.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{logger: logger}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op — hwmon holds no process-level resources.
func (b *Backend) Close() error { return nil }

// Enumerate walks /sys/class/hwmon and returns one Channel per pwm*
// and fan*_target file that exposes a writable channel. It is safe
// to call multiple times and never mutates hardware state.
//
// Enumerate is intentionally minimal: it returns the IDs and caps
// needed to resolve channels by path. Role classification (CPU /
// GPU / Pump) is left to the daemon because the richer classification
// lives in internal/hwmon + internal/hwdiag and pulling it in here
// would widen the import graph without benefit.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	devices := hwmon.EnumerateDevices(hwmon.DefaultHwmonRoot)
	var out []hal.Channel
	for _, dev := range devices {
		if dev.Class == hwmon.ClassSkipNVIDIA {
			continue
		}
		for _, ch := range dev.PWM {
			caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
			out = append(out, hal.Channel{
				ID:   ch.Path,
				Role: hal.RoleUnknown,
				Caps: caps,
				Opaque: State{
					PWMPath:    ch.Path,
					OrigEnable: -1,
				},
			})
		}
		for _, t := range dev.RPMTargets {
			caps := hal.CapRead | hal.CapWriteRPMTarget | hal.CapRestore
			out = append(out, hal.Channel{
				ID:   t.Path,
				Role: hal.RoleUnknown,
				Caps: caps,
				Opaque: State{
					PWMPath:    t.Path,
					RPMTarget:  true,
					OrigEnable: -1,
				},
			})
		}
	}
	return out, nil
}

// Read samples the current PWM / RPM for a channel. Temperature is
// left zero — hwmon temp* files are exposed as sensors, not fan
// channels, and sit outside the FanBackend contract.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	var reading hal.Reading
	reading.OK = true
	if !st.RPMTarget {
		if pwm, err := hwmon.ReadPWM(st.PWMPath); err == nil {
			reading.PWM = pwm
		} else {
			reading.OK = false
		}
	}
	var rpmPath string
	if st.RPMTarget {
		rpmPath = hwmon.RPMTargetInputPath(st.PWMPath)
	} else {
		// Derive fan*_input from pwm*. Matches hwmon.ReadRPM which
		// internally does the same derivation.
		rpmPath = rpmPathFromPWM(st.PWMPath)
	}
	if rpm, err := hwmon.ReadRPMPath(rpmPath); err == nil {
		if rpm < 0 {
			rpm = 0
		}
		if rpm > 0xFFFF {
			rpm = 0xFFFF
		}
		reading.RPM = uint16(rpm)
	} else {
		reading.OK = false
	}
	return reading, nil
}

// Write commands a duty-cycle. For PWM channels it writes 0-255
// verbatim; for fan*_target channels it scales the 0-255 input to
// RPM via the fan*_max sidecar (matching the pre-refactor controller
// dispatch). The first Write to each channel also sets pwm_enable=1
// so subsequent writes aren't auto-overridden by the BIOS/kernel.
//
// Acquire failures with fs.ErrNotExist are logged and ignored (some
// drivers — notably nct6683 for NCT6687D — don't expose pwm_enable);
// other errors are surfaced so the controller can log the specific
// failure. Duty-cycle write errors are always surfaced.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	if err := b.ensureManualMode(st); err != nil {
		return err
	}
	if st.RPMTarget {
		// Opt-4: prefer the cached MaxRPM embedded by the controller; fall back
		// to a live sysfs read only when the cache is absent (first write, or
		// a channel constructed without a pre-cached value).
		maxRPM := st.MaxRPM
		if maxRPM <= 0 {
			maxRPM = hwmon.ReadFanMaxRPM(st.PWMPath)
		}
		rpm := int(math.Round(float64(pwm) / 255.0 * float64(maxRPM)))
		return hwmon.WriteFanTarget(st.PWMPath, rpm)
	}
	return hwmon.WritePWM(st.PWMPath, pwm)
}

// Restore writes the pre-ventd pwm_enable value back, falling back
// to a safe maximum (PWM=255 for duty cycle, fan*_max RPM for
// RPM-target) when the original is unknown or the write fails. This
// reproduces the pre-refactor watchdog.restoreOne semantics byte-for-
// byte; the log lines ("wrote PWM=255", "pwm_enable unsupported")
// are preserved so operator-facing diagnostics don't shift.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	if st.RPMTarget {
		return b.restoreRPMTarget(st)
	}
	return b.restorePWM(st)
}

func (b *Backend) restorePWM(st State) error {
	if st.OrigEnable < 0 {
		if writeErr := hwmon.WritePWM(st.PWMPath, 255); writeErr != nil {
			b.logger.Error("watchdog: pwm_enable unsupported and full-speed fallback failed",
				"path", st.PWMPath, "err", writeErr)
			return writeErr
		}
		b.logger.Warn("watchdog: pwm_enable unsupported, wrote PWM=255 as safe fallback",
			"path", st.PWMPath)
		return nil
	}
	if err := hwmon.WritePWMEnable(st.PWMPath, st.OrigEnable); err != nil {
		b.logger.Error("watchdog: failed to restore pwm_enable",
			"path", st.PWMPath, "value", st.OrigEnable, "err", err)
		if writeErr := hwmon.WritePWM(st.PWMPath, 255); writeErr != nil {
			b.logger.Error("watchdog: full-speed fallback also failed",
				"path", st.PWMPath, "err", writeErr)
			return writeErr
		}
		return nil
	}
	b.logger.Info("watchdog: restored pwm_enable",
		"path", st.PWMPath, "value", st.OrigEnable)
	return nil
}

func (b *Backend) restoreRPMTarget(st State) error {
	enablePath := hwmon.RPMTargetEnablePath(st.PWMPath)
	maxRPM := hwmon.ReadFanMaxRPM(st.PWMPath)
	if st.OrigEnable < 0 {
		if writeErr := hwmon.WriteFanTarget(st.PWMPath, maxRPM); writeErr != nil {
			b.logger.Error("watchdog: pwm_enable unsupported and max-rpm fallback failed",
				"target_path", st.PWMPath, "err", writeErr)
			return writeErr
		}
		b.logger.Warn("watchdog: pwm_enable unsupported, wrote fan_target=max_rpm as safe fallback",
			"target_path", st.PWMPath, "rpm", maxRPM)
		return nil
	}
	if err := hwmon.WritePWMEnablePath(enablePath, st.OrigEnable); err != nil {
		b.logger.Error("watchdog: failed to restore pwm_enable for rpm_target fan",
			"enable_path", enablePath, "value", st.OrigEnable, "err", err)
		if writeErr := hwmon.WriteFanTarget(st.PWMPath, maxRPM); writeErr != nil {
			b.logger.Error("watchdog: max-rpm fallback also failed",
				"target_path", st.PWMPath, "err", writeErr)
			return writeErr
		}
		return nil
	}
	b.logger.Info("watchdog: restored pwm_enable for rpm_target fan",
		"enable_path", enablePath, "value", st.OrigEnable)
	return nil
}

// ensureManualMode is the backend-side analogue of the pre-refactor
// controller.Run startup block. It writes pwm_enable = 1 exactly
// once per channel (tracked by b.acquired) and tolerates the two
// documented absence cases:
//
//   - pwm_enable file missing (fs.ErrNotExist): some drivers don't
//     expose it. Log INFO and proceed — the subsequent PWM write
//     lands verbatim.
//   - any other error: surface to the caller so the controller can
//     log it against the fan. The acquired flag is still set so we
//     don't log the same failure every tick.
func (b *Backend) ensureManualMode(st State) error {
	if _, loaded := b.acquired.LoadOrStore(st.PWMPath, struct{}{}); loaded {
		return nil
	}
	var writeErr error
	if st.RPMTarget {
		enablePath := hwmon.RPMTargetEnablePath(st.PWMPath)
		writeErr = hwmon.WritePWMEnablePath(enablePath, 1)
	} else {
		writeErr = hwmon.WritePWMEnable(st.PWMPath, 1)
	}
	if writeErr == nil {
		if st.RPMTarget {
			b.logger.Info("controller: RPM-target fan manual control acquired", "path", st.PWMPath)
		} else {
			b.logger.Info("controller: manual PWM control acquired", "pwm_path", st.PWMPath)
		}
		return nil
	}
	if errors.Is(writeErr, fs.ErrNotExist) {
		b.logger.Info("controller: pwm_enable not supported by driver, writing PWM values directly",
			"pwm_path", st.PWMPath)
		return nil
	}
	if errors.Is(writeErr, os.ErrPermission) {
		return fmt.Errorf("%w: %s", hal.ErrNotPermitted, writeErr)
	}
	return fmt.Errorf("hal/hwmon: take manual control %s: %w", st.PWMPath, writeErr)
}

// ClearAcquired forgets that a channel's manual-mode flag was set.
// The watchdog calls this from Restore so a follow-on controller
// re-start (e.g. daemon self-exec after a config apply) reacquires
// the mode explicitly rather than assuming the in-memory flag is
// still accurate.
func (b *Backend) ClearAcquired(pwmPath string) {
	b.acquired.Delete(pwmPath)
}

// stateFrom coerces a Channel's Opaque payload into the hwmon State
// shape. Accepts both the value and pointer form so callers can
// construct either.
func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("hal/hwmon: nil opaque state")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("hal/hwmon: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}

// rpmPathFromPWM derives fan*N_input from pwm*N. Mirrors the private
// helper in internal/hwmon; reimplemented here to avoid widening the
// hwmon package API.
func rpmPathFromPWM(pwmPath string) string {
	base := filepath.Base(pwmPath)
	num := strings.TrimPrefix(base, "pwm")
	return filepath.Join(filepath.Dir(pwmPath), "fan"+num+"_input")
}
