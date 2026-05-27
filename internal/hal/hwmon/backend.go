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
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hwmon"
)

// BackendName is the registry tag applied to channels produced by this
// backend. Kept as a package-level constant so callers (main.go,
// watchdog) can reference it without importing hal just for the string.
const BackendName = "hwmon"

// EBUSY rate-observability thresholds. The Write path tracks EBUSY
// occurrences per channel in a 60s rolling window (counter reset on
// window expiry) and escalates the log level as the rate climbs.
// RULE-HWMON-EBUSY-RATE-OBSERVABILITY.
const (
	// EBUSYWindow is the rolling-counter span. A burst of EBUSY events
	// spread over more than this collapses into separate windows; a
	// burst within the span accumulates.
	EBUSYWindow = 60 * time.Second
	// EBUSYWarnThreshold is the count within EBUSYWindow that escalates
	// the log line from one-off WARN to repeated-storm WARN. Tells the
	// operator "this isn't a transient; the BIOS is in a tight
	// reassertion loop".
	EBUSYWarnThreshold = 5
	// EBUSYEscalateThreshold is the count within EBUSYWindow that
	// escalates to ERROR. A doctor detector reading EBUSYRates()
	// surfaces this as a recovery card; v0.5.40 logs it; the
	// detector wiring is a follow-up PR.
	EBUSYEscalateThreshold = 20
)

// ebusyStats tracks the rolling EBUSY count for one channel. The
// window resets when EBUSYWindow elapses since the first event in
// the current burst.
type ebusyStats struct {
	mu              sync.Mutex
	firstEventUnix  int64 // unix-seconds; zero means no event seen yet
	count           int
	lastWarnedCount int // last count at which we emitted an escalating log line; debounces noise
}

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
	// FallbackEnable, when non-nil, marks OrigEnable as having come
	// from the prior-crash branch with no LastKnownStore hit. Restore
	// walks the sequence on EINVAL of OrigEnable; the first value the
	// chip accepts wins, and OnEINVALRecovery (if non-nil) is invoked
	// with the winning value so the watchdog can persist it back for
	// the next prior-crash recovery to skip the walk. Per
	// RULE-WD-PRIOR-CRASH-FALLBACK / #1332.
	FallbackEnable []int
	// OnEINVALRecovery is the watchdog-side persistence callback fired
	// once the EINVAL walker lands on an accepted value. Nil-safe.
	OnEINVALRecovery func(int)
}

// Backend is the hwmon implementation of hal.FanBackend. Construct
// one per consumer (controller, watchdog) so logging is scoped to
// the caller.
type Backend struct {
	logger   *slog.Logger
	acquired sync.Map // key: pwmPath (string), value: struct{}

	// ebusyRates tracks per-channel EBUSY occurrence counts in a 60s
	// rolling window. RULE-HWMON-EBUSY-RATE-OBSERVABILITY. Key:
	// pwmPath; value: *ebusyStats. Concurrent Write calls update
	// per-channel stats under the ebusyStats.mu lock.
	ebusyRates sync.Map

	// nowFn is the clock seam for ebusy rate tracking. nil → time.Now.
	// Tests override via export_test.go to drive the rolling window
	// deterministically.
	nowFn func() time.Time

	// writePWMEnable is the function called by ensureManualMode to flip a
	// pwm*_enable sysfs file into manual mode. nil → hwmon.WritePWMEnable.
	// Overridden in tests via export_test.go to inject failures.
	writePWMEnable func(pwmPath string, value int) error
	// writePWMEnablePath is the function called by ensureManualMode for
	// fan*_target (RPM-target) channels. nil → hwmon.WritePWMEnablePath.
	// Overridden in tests via export_test.go to inject failures.
	writePWMEnablePath func(path string, value int) error
	// writeDutyFn lets tests inject the actual duty-cycle write so the
	// EBUSY-retry path (RULE-HWMON-MODE-REACQUIRE) can be exercised
	// without real sysfs. nil in production: writeDuty falls through
	// to the real hwmon.WritePWM / hwmon.WriteFanTarget dispatch.
	writeDutyFn func(st State, pwm uint8) error
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
//
// Read enforces the empty-by-construction invariant on the returned
// Reading: when OK=false, every other field (PWM, RPM, Temp) is zeroed
// before return. Callers that ignore OK and read a partial-populated
// Reading (e.g. a valid RPM accompanying a failed PWM read) used to see
// stale values from the partial population; the fix guarantees that
// {OK:false} carries no meaningful sub-state — see #1049.
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
		// Reject driver sentinels and implausible values before they
		// propagate into calibration or the control loop.
		if IsSentinelRPM(rpm) {
			b.logger.Warn("hal/hwmon: RPM sentinel or implausible value, marking reading invalid",
				"rpm_path", rpmPath, "raw_rpm", rpm)
			reading.OK = false
		} else {
			reading.RPM = uint16(rpm)
		}
	} else {
		reading.OK = false
	}
	// Enforce the empty-by-construction invariant: a Reading with
	// OK=false carries no partial state. Prior to #1049, a PWM-read
	// failure left a valid RPM on the Reading, and consumers that
	// ignored OK saw a misleading partial reading.
	if !reading.OK {
		return hal.Reading{OK: false}, nil
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
// failure.
//
// EBUSY recovery (RULE-HWMON-MODE-REACQUIRE / issue #904): some
// BIOSes — Gigabyte Q-Fan / Smart Fan Control on IT8xxx chips is the
// canonical case — periodically reassert pwm_enable=2 on channels
// that ventd has already acquired. The next PWM write then returns
// EBUSY because the chip is back under firmware control. When that
// happens we drop the acquired-cache entry, re-write pwm_enable=1,
// and retry the original write exactly once. A second EBUSY surfaces
// the original failure so the controller logs it against the fan and
// triggers the fan-aborted path. Single retry only — if the BIOS is
// re-asserting on a tighter timer than our retry, that's a heartbeat
// problem worth its own fix; this path is the recovery primitive.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	if err := b.ensureManualMode(st); err != nil {
		return err
	}
	writeErr := b.writeDuty(st, pwm)
	if writeErr != nil && errors.Is(writeErr, syscall.EBUSY) {
		// Record the EBUSY event in the per-channel rolling window
		// BEFORE the re-acquire retry, so a tight reassertion loop's
		// rate is tracked even when the retry succeeds — observability
		// of the storm shape, not just the write outcome.
		b.recordEBUSY(st.PWMPath, writeErr)
		b.acquired.Delete(st.PWMPath)
		if reacqErr := b.ensureManualMode(st); reacqErr != nil {
			return fmt.Errorf("hal/hwmon: EBUSY re-acquire failed for %s: %w", st.PWMPath, reacqErr)
		}
		writeErr = b.writeDuty(st, pwm)
		if writeErr == nil {
			b.logger.Info("hwmon: re-acquired manual mode after EBUSY, write succeeded",
				"pwm_path", st.PWMPath)
		}
	}
	return writeErr
}

// recordEBUSY updates the per-channel rolling EBUSY counter and emits
// an escalating log line on threshold crossings.
// RULE-HWMON-EBUSY-RATE-OBSERVABILITY.
//
// The window is a counter-reset, not a true sliding window: when
// EBUSYWindow elapses since the first event in the burst, the
// counter resets to one and a fresh window begins. This is cheaper
// than a per-event ring buffer and adequate for "is this channel in
// a BIOS-reassertion storm right now?" — true if count climbs past
// EBUSYWarnThreshold inside one window.
//
// Log escalation:
//   - count == 1            → WARN, original "re-acquiring" message
//     (preserves existing operator-facing log for one-off events).
//   - count == EBUSYWarnThreshold     → WARN escalation, "storm
//     detected" — tells operators this isn't a transient.
//   - count == EBUSYEscalateThreshold → ERROR escalation. A future
//     doctor detector reads EBUSYRates() to surface the storm as a
//     recovery card; v0.5.40 ships the log line + the accessor seam,
//     the detector wires up in a follow-up PR.
//   - count > EBUSYEscalateThreshold  → silent (debounced). The
//     operator already has the ERROR; no value in spamming.
func (b *Backend) recordEBUSY(pwmPath string, writeErr error) {
	now := b.now()
	v, _ := b.ebusyRates.LoadOrStore(pwmPath, &ebusyStats{})
	st := v.(*ebusyStats)
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.firstEventUnix == 0 || now.Unix()-st.firstEventUnix >= int64(EBUSYWindow.Seconds()) {
		// Fresh burst — reset window.
		st.firstEventUnix = now.Unix()
		st.count = 1
		st.lastWarnedCount = 0
	} else {
		st.count++
	}

	switch {
	case st.count == 1:
		b.logger.Warn("hwmon: write returned EBUSY, BIOS may be contesting manual mode — re-acquiring",
			"pwm_path", pwmPath, "first_err", writeErr)
		st.lastWarnedCount = 1
	case st.count == EBUSYWarnThreshold && st.lastWarnedCount < EBUSYWarnThreshold:
		b.logger.Warn("hwmon: EBUSY storm detected on channel — BIOS reassertion in tight loop",
			"pwm_path", pwmPath,
			"events_in_window", st.count,
			"window_seconds", int(EBUSYWindow.Seconds()))
		st.lastWarnedCount = EBUSYWarnThreshold
	case st.count == EBUSYEscalateThreshold && st.lastWarnedCount < EBUSYEscalateThreshold:
		b.logger.Error("hwmon: EBUSY storm escalating — operator action recommended",
			"pwm_path", pwmPath,
			"events_in_window", st.count,
			"window_seconds", int(EBUSYWindow.Seconds()),
			"hint", "BIOS fan-control feature (Q-Fan / Smart Fan) likely needs disabling")
		st.lastWarnedCount = EBUSYEscalateThreshold
	}
}

// EBUSYRate is the per-channel rolling-window snapshot returned by
// EBUSYRates. RULE-HWMON-EBUSY-RATE-OBSERVABILITY exposes this as
// the seam for a future doctor detector — the v0.5.40 ship is the
// accessor + the escalating log lines; the detector PR consumes
// this map to emit recovery cards.
type EBUSYRate struct {
	// PWMPath is the channel identifier (matches hal.Channel.ID).
	PWMPath string
	// EventCount is the EBUSY events seen in the current window.
	// Zero when no event has happened or when the previous burst's
	// window has fully expired (a subsequent EBUSY would reset to 1).
	EventCount int
	// WindowStart is the unix-second timestamp of the first event in
	// the current window. Zero when EventCount == 0.
	WindowStart int64
	// WindowSeconds is the window span (EBUSYWindow in seconds).
	WindowSeconds int
}

// EBUSYRates returns a snapshot of per-channel EBUSY rolling-window
// stats. The map is keyed by PWMPath. Empty when no EBUSY events
// have been recorded.
//
// Stale windows (firstEventUnix older than EBUSYWindow) are
// reported with their last-known EventCount and the original
// WindowStart so the doctor detector can distinguish "currently
// storming" (WindowStart within last 60s) from "recently stormed,
// now quiet" (WindowStart older than 60s but counter not yet
// cleared by a subsequent EBUSY). The next recordEBUSY call resets
// the stats on the same channel.
func (b *Backend) EBUSYRates() map[string]EBUSYRate {
	out := map[string]EBUSYRate{}
	b.ebusyRates.Range(func(key, value any) bool {
		path, _ := key.(string)
		st, _ := value.(*ebusyStats)
		if st == nil {
			return true
		}
		st.mu.Lock()
		if st.count > 0 {
			out[path] = EBUSYRate{
				PWMPath:       path,
				EventCount:    st.count,
				WindowStart:   st.firstEventUnix,
				WindowSeconds: int(EBUSYWindow.Seconds()),
			}
		}
		st.mu.Unlock()
		return true
	})
	return out
}

// now returns the clock's current instant via the test seam.
func (b *Backend) now() time.Time {
	if b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// writeDuty performs the actual duty-cycle write. Split out from
// Write so the EBUSY-retry path can re-invoke it after re-acquiring
// manual mode without duplicating the rpm-target / pwm dispatch.
// b.writeDutyFn lets tests inject failures (only used in tests).
func (b *Backend) writeDuty(st State, pwm uint8) error {
	if b.writeDutyFn != nil {
		return b.writeDutyFn(st, pwm)
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
		// Prior-crash branch with no persisted LastKnownStore hit: the
		// watchdog installed SafePreDaemonEnableSequence on the entry.
		// Walk the remaining values on EINVAL — the first non-EINVAL
		// write wins and is persisted back via OnEINVALRecovery so the
		// next prior-crash recovery skips the probe. Per #1332.
		if errors.Is(err, syscall.EINVAL) && len(st.FallbackEnable) > 1 {
			for _, v := range st.FallbackEnable[1:] {
				if v == st.OrigEnable {
					continue
				}
				if fbErr := hwmon.WritePWMEnable(st.PWMPath, v); fbErr == nil {
					b.logger.Info("watchdog: prior-crash fallback walker found accepted pwm_enable",
						"path", st.PWMPath, "tried", st.OrigEnable, "accepted", v)
					if st.OnEINVALRecovery != nil {
						st.OnEINVALRecovery(v)
					}
					return nil
				}
			}
		}
		// NCT6687D (and other OOT drivers without a thermal-cruise mode)
		// reject pwm_enable values > 1 with EINVAL even though the chip
		// returned that value at register time. Retry with manual mode
		// (pwm_enable=1) — the chip retains its current pwm<N> byte
		// (the polarity prober's safe-mid byte per #1241) so fans stay
		// at a quiet floor instead of jumping to PWM=255. (#1249.)
		if errors.Is(err, syscall.EINVAL) && st.OrigEnable > 1 {
			if fbErr := hwmon.WritePWMEnable(st.PWMPath, 1); fbErr == nil {
				b.logger.Info("watchdog: chip rejected pwm_enable>1, restored to manual mode",
					"path", st.PWMPath, "requested", st.OrigEnable, "fallback", 1)
				return nil
			}
		}
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
		// Same prior-crash fallback walker as restorePWM (#1332).
		if errors.Is(err, syscall.EINVAL) && len(st.FallbackEnable) > 1 {
			for _, v := range st.FallbackEnable[1:] {
				if v == st.OrigEnable {
					continue
				}
				if fbErr := hwmon.WritePWMEnablePath(enablePath, v); fbErr == nil {
					b.logger.Info("watchdog: prior-crash fallback walker found accepted pwm_enable for rpm_target fan",
						"enable_path", enablePath, "tried", st.OrigEnable, "accepted", v)
					if st.OnEINVALRecovery != nil {
						st.OnEINVALRecovery(v)
					}
					return nil
				}
			}
		}
		// Same EINVAL fallback as restorePWM — chip families that don't
		// support pwm_enable>1 (NCT6687D and friends) retain their current
		// fan_target byte under manual mode, so the operator-facing
		// behaviour is "chip stays at the target the controller last
		// set" rather than max-RPM spin-up. (#1249.)
		if errors.Is(err, syscall.EINVAL) && st.OrigEnable > 1 {
			if fbErr := hwmon.WritePWMEnablePath(enablePath, 1); fbErr == nil {
				b.logger.Info("watchdog: chip rejected pwm_enable>1, restored rpm_target fan to manual mode",
					"enable_path", enablePath, "requested", st.OrigEnable, "fallback", 1)
				return nil
			}
		}
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
//     log it against the fan. The acquired flag is NOT set so a
//     subsequent call will re-attempt the sysfs write, allowing
//     recovery from transient errors without a daemon restart.
//
// The acquired flag is stored only after a successful sysfs write
// (or a documented-absence ErrNotExist) to prevent a failed write
// from silently masking retries on every subsequent tick.
func (b *Backend) ensureManualMode(st State) error {
	if _, ok := b.acquired.Load(st.PWMPath); ok {
		return nil
	}
	writePWMEnable := b.writePWMEnable
	if writePWMEnable == nil {
		writePWMEnable = hwmon.WritePWMEnable
	}
	writePWMEnablePath := b.writePWMEnablePath
	if writePWMEnablePath == nil {
		writePWMEnablePath = hwmon.WritePWMEnablePath
	}
	var writeErr error
	if st.RPMTarget {
		enablePath := hwmon.RPMTargetEnablePath(st.PWMPath)
		writeErr = writePWMEnablePath(enablePath, 1)
	} else {
		writeErr = writePWMEnable(st.PWMPath, 1)
	}
	if writeErr == nil {
		b.acquired.Store(st.PWMPath, struct{}{})
		if st.RPMTarget {
			b.logger.Info("controller: RPM-target fan manual control acquired", "path", st.PWMPath)
		} else {
			b.logger.Info("controller: manual PWM control acquired", "pwm_path", st.PWMPath)
		}
		return nil
	}
	if errors.Is(writeErr, fs.ErrNotExist) {
		b.acquired.Store(st.PWMPath, struct{}{})
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
	return hal.StateFrom[State](ch, "hal/hwmon", nil)
}

// rpmPathFromPWM derives fan*N_input from pwm*N. Mirrors the private
// helper in internal/hwmon; reimplemented here to avoid widening the
// hwmon package API.
func rpmPathFromPWM(pwmPath string) string {
	base := filepath.Base(pwmPath)
	num := strings.TrimPrefix(base, "pwm")
	return filepath.Join(filepath.Dir(pwmPath), "fan"+num+"_input")
}
