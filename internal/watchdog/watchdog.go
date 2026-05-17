package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halmsiec "github.com/ventd/ventd/internal/hal/msiec"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	"github.com/ventd/ventd/internal/hwmon"
)

// DefaultRestoreBudget caps the total wall-clock time RestoreCtx
// spends completing per-channel restore goroutines. Per
// RULE-WD-RESTORE-BUDGET. 1.8 s leaves 200 ms of headroom under
// systemd's typical 2 s WatchdogSec / TimeoutStopSec, so the
// daemon's own restore path is the load-bearing safety primitive
// rather than systemd's belt-and-braces SIGKILL + ventd-recover.
const DefaultRestoreBudget = 1800 * time.Millisecond

// SetRestoreOneFnForTest installs a per-instance override for the
// per-entry restore call. Tests use this to simulate a hung backend
// (exercising RestoreCtx's deadline-exceeded branch) without needing a
// real /sys driver wedge. Production never calls this — the nil
// default falls through to (*Watchdog).restoreOne, the unchanged
// per-entry restore + panic-recover envelope.
//
// Issue #1178: per-instance rather than a package-global var. The
// previous global-swap pattern raced the race detector against
// goroutines that had read the seam before the cleanup fired; the
// per-instance field lives and dies with the test's own Watchdog, so
// there is no shared mutable state across tests.
func (w *Watchdog) SetRestoreOneFnForTest(fn func(e entry)) {
	w.mu.Lock()
	w.restoreOneFn = fn
	w.mu.Unlock()
}

func (w *Watchdog) loadRestoreOneFn() func(e entry) {
	w.mu.Lock()
	fn := w.restoreOneFn
	w.mu.Unlock()
	if fn == nil {
		return w.restoreOne
	}
	return fn
}

// SafePreDaemonEnable is the fallback origEnable value Register uses
// when the live pwm_enable read returns 1 (manual) — typically a
// prior-daemon-crash residual that left the chip in manual mode.
// 2 (BIOS auto) is the safest unhanded-back: hwmon convention is
// {0=off, 1=manual, 2=auto}; writing 2 back hands fan control to
// the firmware regardless of what state the previous daemon left
// the chip in. RULE-WD-PRIOR-CRASH-FALLBACK pins this default.
const SafePreDaemonEnable = 2

// PreDaemonEnableKey returns the canonical KV key the watchdog uses
// to persist the last-known-good pre-daemon pwm_enable value for a
// channel. The key is namespaced under "watchdog" so future state-
// dir migrations can move it without colliding with calibration /
// polarity records. Per RULE-WD-PRIOR-CRASH-FALLBACK.
func PreDaemonEnableKey(pwmPath string) string {
	return fmt.Sprintf("watchdog.%s.preDaemonEnable", pwmPath)
}

// LastKnownStore is the optional per-channel persistence seam used
// by Register to recover the LAST-KNOWN-GOOD pre-daemon pwm_enable
// value across daemon crashes. When non-nil and the chip's live
// pwm_enable reads 1 (manual — typically a prior-crash residual),
// Register consults the store under PreDaemonEnableKey(pwmPath) and
// uses the persisted value if present; otherwise it falls back to
// SafePreDaemonEnable.
//
// The interface is deliberately narrow so callers (cmd/ventd/main.go)
// can wrap state.KVDB without exposing the wider KV surface to the
// watchdog package.
type LastKnownStore interface {
	// GetPreDaemonEnable returns the persisted pre-daemon pwm_enable
	// value for the given pwm path, or (-1, false) if no value is
	// persisted.
	GetPreDaemonEnable(pwmPath string) (int, bool)
	// SetPreDaemonEnable persists the given value as the last-known-
	// good pre-daemon pwm_enable for the given path. Errors are
	// swallowed by Register — the watchdog cannot fail-start the
	// daemon because of a state-dir write failure.
	SetPreDaemonEnable(pwmPath string, value int) error
}

// IPMIRestoreFn is the narrow callback shape the watchdog uses to
// route IPMI-channel restores through the canonical exit path. The
// callback owns the vendor-specific restore primitive (SET_FAN_MODE
// for Supermicro, sub-command 0x01,0x01 for Dell); the watchdog owns
// the cross-cutting safety contract (panic-recover, per-entry
// budget, restore-on-every-exit).
//
// Per RULE-WD-IPMI-ROUTING the IPMI backend's own Restore method is
// the canonical implementation; cmd/ventd wires it as
//
//	wd.RegisterIPMI(channelID, ipmiBackend.Restore)
//
// so a future refactor that adds new IPMI vendors automatically picks
// up the watchdog's safety envelope without touching the watchdog
// package.
type IPMIRestoreFn func() error

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
	fanType    string // "hwmon", "nvidia", or "ipmi"
	origEnable int    // hwmon only; -1 if unsupported
	// rpmTarget is true when pwmPath is a fan*_target RPM-setpoint file
	// (pre-RDNA AMD). Dictates which sysfs attributes Restore reads/writes:
	// the enable file is pwm*_enable in the same directory, and the failsafe
	// on enable-missing is WriteFanTarget(fan*_max) rather than WritePWM(255),
	// since writing "255" to fan*_target would mean 255 RPM, not full speed.
	rpmTarget bool
	// ipmiRestore is non-nil for entries registered via RegisterIPMI.
	// When set, restoreOne dispatches to ipmiRestore() instead of the
	// hwmon/nvml backends. The cross-cutting safety contract
	// (RULE-WD-RESTORE-EXIT, RULE-WD-RESTORE-PANIC,
	// RULE-WD-RESTORE-BUDGET) covers IPMI channels identically — only
	// the byte-level restore primitive differs.
	ipmiRestore IPMIRestoreFn
}

type Watchdog struct {
	mu      sync.Mutex
	entries []entry
	logger  *slog.Logger
	store   LastKnownStore
	// hwmonBe / nvmlBe are the FanBackend instances Restore delegates
	// into. Per-watchdog instances (constructed in New) keep the
	// backend's log output scoped to this watchdog's logger — the
	// pre-refactor restoreOne wrote through w.logger directly, and
	// the test suite asserts on that logger's buffer.
	hwmonBe *halhwmon.Backend
	nvmlBe  *halnvml.Backend
	msiecBe *halmsiec.Backend
	// restoreOneFn is the per-instance swappable seam used by
	// restoreOneCtx. nil → use the default (*Watchdog).restoreOne.
	// Issue #1178: per-instance rather than a package-global var so
	// the test fixture's swap+cleanup is scoped to the test's own
	// Watchdog and the race detector can prove the seam never escapes
	// the test's lifecycle.
	restoreOneFn func(e entry)
}

func New(logger *slog.Logger) *Watchdog {
	return &Watchdog{
		logger:  logger,
		hwmonBe: halhwmon.NewBackend(logger),
		nvmlBe:  halnvml.NewBackend(logger),
		msiecBe: halmsiec.NewBackend(logger),
	}
}

// NewWithStore is the production constructor that wires the optional
// LastKnownStore for prior-crash recovery (RULE-WD-PRIOR-CRASH-FALLBACK).
// A nil store is equivalent to New — Register falls back to
// SafePreDaemonEnable when the live read indicates a prior-crash
// residual.
func NewWithStore(logger *slog.Logger, store LastKnownStore) *Watchdog {
	w := New(logger)
	w.store = store
	return w
}

func (w *Watchdog) Register(pwmPath string, fanType string) {
	e := entry{pwmPath: pwmPath, fanType: fanType}
	// Per RULE-WD-PER-SYSCALL-DEADLINE the Register-time pwm_enable
	// reads run under a bounded context so a hot-plug or hung chip
	// cannot block daemon startup indefinitely (#1042). On deadline
	// fired we proceed with SafePreDaemonEnable as if the chip
	// reported manual mode (which would otherwise have hit the
	// prior-crash fallback path below anyway).
	ctx, cancel := context.WithTimeout(context.Background(), DefaultRegisterDeadline)
	defer cancel()

	switch {
	case fanType == "nvidia":
		e.origEnable = -1
	case fanType == halmsiec.BackendName:
		// msi-ec exposes mode-switching at /sys/devices/platform/msi-ec/
		// fan_mode — there is no hwmon-style pwm_enable file. Skip the
		// read and let restoreOne dispatch through the msi-ec backend's
		// "auto" write at exit time. Mirrors the nvidia branch above.
		e.origEnable = -1
	case fanType == "ipmi":
		// IPMI channels carry their restore primitive in
		// e.ipmiRestore. Register-via-pwmPath is unsupported for
		// IPMI; callers must use RegisterIPMI which sets the
		// callback. Keeping the case here pins the entry-shape so
		// restoreOne's IPMI branch never sees a partially-formed
		// entry.
		w.logger.Warn("watchdog: Register(pwmPath, \"ipmi\") is unsupported; use RegisterIPMI",
			"path", pwmPath)
		return
	case hwmon.IsRPMTargetPath(pwmPath):
		e.rpmTarget = true
		enablePath := hwmon.RPMTargetEnablePath(pwmPath)
		orig, err := readPWMEnableWithDeadline(ctx, enablePath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable for rpm_target fan, will use max-rpm fallback on restore",
					"target_path", pwmPath, "enable_path", enablePath, "err", err)
			}
		}
		e.origEnable = w.applyPriorCrashFallback(pwmPath, orig)
	default:
		enablePath := pwmPath + "_enable"
		orig, err := readPWMEnableWithDeadline(ctx, enablePath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable, will use full-speed fallback on restore",
					"path", pwmPath, "err", err)
			}
		}
		e.origEnable = w.applyPriorCrashFallback(pwmPath, orig)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, e)
}

// applyPriorCrashFallback implements RULE-WD-PRIOR-CRASH-FALLBACK:
// when the live pwm_enable read returns 1 (manual mode — typically a
// prior-daemon-crash residual), consult the LastKnownStore for the
// LAST-KNOWN-GOOD pre-daemon value; if none is persisted, fall back
// to SafePreDaemonEnable (2, BIOS auto). Any non-1, non-error value
// is treated as a legitimate pre-daemon capture and persisted to the
// store so a subsequent daemon-crash can recover it.
//
// orig=-1 (read failed) falls through unchanged — the restore-time
// failsafe is the existing PWM=255 path, not the prior-crash
// fallback.
func (w *Watchdog) applyPriorCrashFallback(pwmPath string, orig int) int {
	if orig == -1 {
		return -1
	}
	if orig == 1 {
		// Live read says manual mode. Prefer last-known-good from the
		// store; otherwise fall back to BIOS auto.
		if w.store != nil {
			if stored, ok := w.store.GetPreDaemonEnable(pwmPath); ok && stored != 1 {
				w.logger.Warn("watchdog: live pwm_enable=1 (prior-crash residual?); recovered last-known-good value from state",
					"path", pwmPath, "stored", stored)
				return stored
			}
		}
		w.logger.Warn("watchdog: live pwm_enable=1 (prior-crash residual?); falling back to BIOS auto",
			"path", pwmPath, "fallback", SafePreDaemonEnable)
		return SafePreDaemonEnable
	}
	// Live read is a legitimate pre-daemon capture. Persist it so a
	// future daemon-crash + restart can recover it via the path
	// above. Errors are swallowed — the watchdog cannot fail-start
	// the daemon because of a state-dir write failure.
	if w.store != nil {
		if err := w.store.SetPreDaemonEnable(pwmPath, orig); err != nil {
			w.logger.Warn("watchdog: could not persist pre-daemon pwm_enable to state",
				"path", pwmPath, "value", orig, "err", err)
		}
	}
	return orig
}

// readPWMEnableWithDeadline reads a pwm_enable sysfs file under a
// per-syscall deadline and parses the result. Wraps
// readWithDeadline from deadline.go. Per RULE-WD-PER-SYSCALL-DEADLINE.
//
// Returns wrapped fs.ErrNotExist when the file is absent so callers
// can fall through to the safe-default path. Returns the wrapped
// ctx.Err() on timeout — callers treat it as a non-ErrNotExist failure
// (logged WARN, origEnable=-1).
func readPWMEnableWithDeadline(ctx context.Context, enablePath string) (int, error) {
	data, err := readWithDeadline(ctx, enablePath)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("watchdog: parse pwm_enable %s: %w", enablePath, err)
	}
	return v, nil
}

// RegisterIPMI registers an IPMI fan channel with the watchdog. The
// restore primitive is supplied as a closure so the watchdog package
// stays free of hal/ipmi imports — cmd/ventd wires
//
//	wd.RegisterIPMI(channelID, func() error { return ipmiBackend.Restore(ch) })
//
// for each enumerated IPMI channel at daemon start. RULE-WD-IPMI-ROUTING.
func (w *Watchdog) RegisterIPMI(channelID string, restore IPMIRestoreFn) {
	if restore == nil {
		w.logger.Warn("watchdog: RegisterIPMI called with nil restore func; skipping",
			"channel", channelID)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, entry{
		pwmPath:     channelID,
		fanType:     "ipmi",
		origEnable:  -1,
		ipmiRestore: restore,
	})
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

// Restore wraps RestoreCtx with the default budget. Preserves the
// pre-RULE-WD-RESTORE-BUDGET API for existing call sites + tests.
// Callers who want a custom budget (or to honour an existing
// shutdown ctx) should call RestoreCtx directly.
func (w *Watchdog) Restore() {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultRestoreBudget)
	defer cancel()
	w.RestoreCtx(ctx)
}

// RestoreCtx is the budget-aware restore. Per-channel restores run
// in parallel goroutines so a single hung sysfs write on one fan
// doesn't stall the others. RestoreCtx returns when either every
// goroutine has completed OR ctx is cancelled (typically by
// DefaultRestoreBudget timing out). On deadline exceeded, channels
// whose goroutines are still in-flight are logged by name; their
// goroutines continue running because the underlying sysfs ioctl /
// NVML call is uncancellable from goroutine cancellation alone, but
// the daemon proceeds with its exit regardless. Per
// RULE-WD-RESTORE-BUDGET.
//
// Per-entry panic recovery (RULE-WD-RESTORE-PANIC) and the
// every-entry-touched contract (RULE-WD-RESTORE-EXIT) continue to
// hold: panics in any one goroutine are caught by restoreOne's
// existing defer/recover; entries whose goroutine completes within
// the budget receive their full restore write.
func (w *Watchdog) RestoreCtx(ctx context.Context) {
	w.mu.Lock()
	entries := make([]entry, len(w.entries))
	copy(entries, w.entries)
	w.mu.Unlock()
	if len(entries) == 0 {
		return
	}

	var imu sync.Mutex
	incomplete := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		incomplete[e.pwmPath] = struct{}{}
	}

	var wg sync.WaitGroup
	for _, e := range entries {
		e := e // capture by value for the goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.restoreOneCtx(ctx, e)
			imu.Lock()
			delete(incomplete, e.pwmPath)
			imu.Unlock()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-ctx.Done():
		// Give in-flight goroutines a short grace to complete any
		// remaining log writes / fast-returning syscalls before we
		// snapshot the abandoned set. Without this grace the
		// abandoned list races against goroutines that are
		// microseconds away from finishing, AND any logger backed
		// by a non-thread-safe writer (most test buffers, some
		// journald shims) sees concurrent writes after the function
		// returns. The grace is bounded so a truly hung goroutine
		// can't extend the daemon's exit indefinitely.
		grace := time.NewTimer(restoreGracePeriod)
		defer grace.Stop()
		select {
		case <-done:
		case <-grace.C:
		}
		imu.Lock()
		abandoned := make([]string, 0, len(incomplete))
		for p := range incomplete {
			abandoned = append(abandoned, p)
		}
		imu.Unlock()
		sort.Strings(abandoned)
		if len(abandoned) > 0 {
			w.logger.Warn("watchdog: restore budget exceeded; abandoning in-flight goroutines",
				"deadline_cause", ctx.Err(),
				"abandoned_channels", abandoned,
				"abandoned_count", len(abandoned))
		}
		return
	}
}

// restoreGracePeriod caps the wait for in-flight goroutines after
// the budget has fired. 100 ms is generous for the goroutine to
// either return from a microsecond-scale syscall OR emit its
// per-entry skip-WARN; tight enough that a truly hung backend
// cannot extend the daemon's exit beyond budget + grace.
const restoreGracePeriod = 100 * time.Millisecond

// restoreOneCtx wraps the per-entry restore in a ctx pre-check. If
// ctx is already cancelled before we'd dispatch to the backend, we
// log + skip to avoid wasting time on a syscall we can't honour.
// The seam restoreOneImpl lets tests inject a stub that blocks past
// the budget so RestoreCtx's deadline-exceeded branch is reachable.
//
// Per RULE-WD-PER-SYSCALL-DEADLINE the inner sysfs writes the
// backend performs are bounded by the writeWithDeadline helper in
// deadline.go so a hung kernel-side syscall cannot stall the
// goroutine past the parent budget — the abandoned syscall keeps
// running inside the kernel, but the goroutine returns and the
// parent RestoreCtx's select observes the wg.Done.
func (w *Watchdog) restoreOneCtx(ctx context.Context, e entry) {
	if err := ctx.Err(); err != nil {
		w.logger.Warn("watchdog: restore skipped — ctx cancelled before backend call",
			"path", e.pwmPath,
			"err", err)
		return
	}
	w.loadRestoreOneFn()(e)
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
	if e.fanType == "ipmi" {
		// IPMI channels carry their vendor-specific restore primitive
		// directly (RULE-WD-IPMI-ROUTING). The cross-cutting
		// safety contract (panic-recover, budget, every-entry-touched)
		// is provided by the surrounding restoreOne envelope; the
		// callback supplies only the byte-level restore.
		if e.ipmiRestore == nil {
			w.logger.Error("watchdog: ipmi entry has nil restore func, skipping",
				"channel", e.pwmPath)
			return
		}
		if err := e.ipmiRestore(); err != nil {
			w.logger.Error("watchdog: ipmi restore failed",
				"channel", e.pwmPath, "err", err)
		} else {
			w.logger.Info("watchdog: ipmi restored to auto", "channel", e.pwmPath)
		}
		return
	}
	switch e.fanType {
	case "nvidia":
		be = w.nvmlBe
		ch = hal.Channel{
			ID:     e.pwmPath,
			Role:   hal.RoleGPU,
			Caps:   hal.CapRestore,
			Opaque: halnvml.State{Index: e.pwmPath},
		}
	case halmsiec.BackendName:
		// msi-ec Restore writes "auto" to fan_mode. The backend's
		// Enumerate-time WritableModes list is not needed at restore
		// time (Restore writes the fixed string "auto"), so we
		// construct the channel state with an empty WritableModes
		// slice — the backend's Restore path does not consult it.
		be = w.msiecBe
		ch = hal.Channel{
			ID:     e.pwmPath,
			Role:   hal.RoleCPU,
			Caps:   hal.CapRestore,
			Opaque: halmsiec.State{SysfsRoot: e.pwmPath},
		}
	default:
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
