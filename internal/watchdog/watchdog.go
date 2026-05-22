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

// SafePreDaemonEnableSequence is the ordered fallback sequence Register
// installs on the entry when the live pwm_enable read returns 1 (manual
// — typically a prior-daemon-crash residual) and the LastKnownStore has
// no persisted value. Restore walks the sequence on EINVAL of each
// candidate, picks the first that lands without EINVAL, and persists
// the winner back to the store so the next crash recovery skips the
// probe. RULE-WD-PRIOR-CRASH-FALLBACK.
//
// Sequence rationale:
//   - 2 — de-facto userspace convention for "automatic" (hits ~all in-
//     tree drivers on the first write; zero behaviour change for the
//     happy path).
//   - 99 — historic SuperIO placeholder used by NCT6687D pre-#169 and
//     other vendor drivers that pick a "deliberately weird" auto value
//     (Documentation/ABI/testing/sysfs-class-hwmon defines 2+ as a
//     range, not a single value).
//   - 0 — ABI "no fan speed control / full speed". Last-resort safe
//     stop: noisy but mechanically safe and explicitly defined in the
//     ABI.
//
// Do not reorder per-chip — same sequence everywhere keeps the prior-
// crash fallback fragility-free.
var SafePreDaemonEnableSequence = []int{2, 99, 0}

// SafePreDaemonEnable is the primary value the sequence walker tries
// first. Retained as a stable constant for callers + log lines that
// want the "head" value without depending on the slice layout.
const SafePreDaemonEnable = 2

// ChannelIdentity is the stable, rmmod-modprobe-safe shape the watchdog
// uses to key the LastKnownStore. The previous full-pwmPath key embedded
// the volatile /sys/class/hwmon/hwmonN/ prefix; udev reallocates that
// number across module reloads so the persisted pre-daemon pwm_enable
// became unreachable after every rebind. RULE-WD-PRIOR-CRASH-FALLBACK
// (#1331).
//
// ChipName + BusAddr survive module reload because they come from the
// chip's `name` file and the parent device's bus suffix (e.g.
// `nct6687.2592` — the trailing platform address is stable). ChannelIdx
// is the pwmN suffix parsed from the original path.
//
// LegacyPath is the full pre-#1331 pwmPath. Watchdog uses it to look up
// pre-migration entries in the store on a fresh read, then migrates the
// value forward under the new identity key.
type ChannelIdentity struct {
	ChipName   string
	BusAddr    string
	ChannelIdx int
	LegacyPath string
}

// Key returns the stable-identity KV key shape. When ChipName + BusAddr
// resolved, the key embeds them so it survives hwmonN renumbering; when
// resolution failed (rare — chip with no `name` and no `device` symlink)
// the key falls back to the legacy pwmPath shape so behaviour matches
// the pre-#1331 daemon.
func (id ChannelIdentity) Key() string {
	if id.ChipName != "" && id.BusAddr != "" {
		return fmt.Sprintf("watchdog.%s.%s.pwm%d.preDaemonEnable",
			id.ChipName, id.BusAddr, id.ChannelIdx)
	}
	return id.LegacyKey()
}

// LegacyKey returns the pre-#1331 pwmPath-based key. Used by the
// migration shim: on the first read after upgrade, the watchdog tries
// Key() first and falls back to LegacyKey() so a value persisted by a
// pre-stable-identity daemon is still recoverable. The next persist
// writes under Key() (the consumer is expected to delete the legacy
// entry as part of the migration).
func (id ChannelIdentity) LegacyKey() string {
	return fmt.Sprintf("watchdog.%s.preDaemonEnable", id.LegacyPath)
}

// PreDaemonEnableKey is retained for back-compat with tests + external
// callers. Equivalent to ChannelIdentity{LegacyPath: pwmPath}.LegacyKey().
func PreDaemonEnableKey(pwmPath string) string {
	return ChannelIdentity{LegacyPath: pwmPath}.LegacyKey()
}

// LastKnownStore is the optional per-channel persistence seam used by
// Register to recover the LAST-KNOWN-GOOD pre-daemon pwm_enable value
// across daemon crashes. When non-nil and the chip's live pwm_enable
// reads 1 (manual — typically a prior-crash residual), Register
// consults the store under the channel's stable identity and uses the
// persisted value if present; otherwise it installs the prior-crash
// fallback sequence onto the entry so Restore walks
// SafePreDaemonEnableSequence on EINVAL.
//
// The interface is deliberately narrow so callers (cmd/ventd/main.go)
// can wrap state.KVDB without exposing the wider KV surface to the
// watchdog package. The migration shim (try new key → fall back to
// legacy key) lives on the consumer side: the watchdog hands the
// consumer a ChannelIdentity carrying both the stable identity and the
// pre-#1331 LegacyPath; the consumer's Get returns whichever lookup
// hits, and Set persists under the new key + deletes the legacy entry.
type LastKnownStore interface {
	// GetPreDaemonEnable returns the persisted pre-daemon pwm_enable
	// value for the given channel identity, or (-1, false) if no value
	// is persisted under either the stable key or the legacy key.
	GetPreDaemonEnable(id ChannelIdentity) (int, bool)
	// SetPreDaemonEnable persists the given value under the new stable-
	// identity key shape and migrates (deletes) any legacy pwmPath-
	// keyed entry for this channel. Errors are swallowed by Register —
	// the watchdog cannot fail-start the daemon because of a state-dir
	// write failure.
	SetPreDaemonEnable(id ChannelIdentity, value int) error
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
	// identity is the stable-identity capture used to persist the
	// LAST-KNOWN-GOOD pre-daemon pwm_enable across daemon crashes +
	// rmmod-modprobe cycles. Empty for ipmi / nvidia / msi-ec entries
	// (those use their backend-native restore primitives).
	identity ChannelIdentity
	// fallbackSeq, when non-nil, marks origEnable as having come from
	// the prior-crash branch with no persisted store value. Restore
	// walks the sequence on EINVAL of origEnable. nil means origEnable
	// was captured from a live pre-daemon read (or from LastKnownStore)
	// and is authoritative — Restore writes it and tolerates EINVAL via
	// the historical manual-mode fallback.
	fallbackSeq []int
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
		e.identity = resolveChannelIdentity(ctx, pwmPath)
		enablePath := hwmon.RPMTargetEnablePath(pwmPath)
		orig, err := readPWMEnableWithDeadline(ctx, enablePath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable for rpm_target fan, will use max-rpm fallback on restore",
					"target_path", pwmPath, "enable_path", enablePath, "err", err)
			}
		}
		e.origEnable, e.fallbackSeq = w.applyPriorCrashFallback(e.identity, orig)
	default:
		e.identity = resolveChannelIdentity(ctx, pwmPath)
		enablePath := pwmPath + "_enable"
		orig, err := readPWMEnableWithDeadline(ctx, enablePath)
		if err != nil {
			orig = -1
			if !errors.Is(err, fs.ErrNotExist) {
				w.logger.Warn("watchdog: could not read initial pwm_enable, will use full-speed fallback on restore",
					"path", pwmPath, "err", err)
			}
		}
		e.origEnable, e.fallbackSeq = w.applyPriorCrashFallback(e.identity, orig)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, e)
}

// applyPriorCrashFallback implements RULE-WD-PRIOR-CRASH-FALLBACK:
// when the live pwm_enable read returns 1 (manual mode — typically a
// prior-daemon-crash residual), consult the LastKnownStore for the
// LAST-KNOWN-GOOD pre-daemon value; if none is persisted, install the
// SafePreDaemonEnableSequence as the entry's fallback so Restore walks
// it on EINVAL. Any non-1, non-error value is treated as a legitimate
// pre-daemon capture and persisted to the store so a subsequent
// daemon-crash can recover it.
//
// Returns (value, fallbackSeq). fallbackSeq is non-nil only on the
// prior-crash branch with no store hit — restore consults it to walk
// alternative values on EINVAL. A nil fallbackSeq means origEnable is
// authoritative (live capture or stored last-known-good).
//
// orig=-1 (read failed) falls through unchanged — the restore-time
// failsafe is the existing PWM=255 path, not the prior-crash
// fallback.
func (w *Watchdog) applyPriorCrashFallback(id ChannelIdentity, orig int) (int, []int) {
	if orig == -1 {
		return -1, nil
	}
	if orig == 1 {
		// Live read says manual mode. Prefer last-known-good from the
		// store; otherwise install the fallback sequence so restore
		// walks it on EINVAL.
		if w.store != nil {
			if stored, ok := w.store.GetPreDaemonEnable(id); ok && stored != 1 {
				w.logger.Warn("watchdog: live pwm_enable=1 (prior-crash residual?); recovered last-known-good value from state",
					"path", id.LegacyPath, "stored", stored)
				return stored, nil
			}
		}
		w.logger.Warn("watchdog: live pwm_enable=1 (prior-crash residual?); installing fallback sequence",
			"path", id.LegacyPath, "sequence", SafePreDaemonEnableSequence)
		seq := append([]int(nil), SafePreDaemonEnableSequence...)
		return seq[0], seq
	}
	// Live read is a legitimate pre-daemon capture. Persist it so a
	// future daemon-crash + restart can recover it via the path
	// above. Errors are swallowed — the watchdog cannot fail-start
	// the daemon because of a state-dir write failure.
	if w.store != nil {
		if err := w.store.SetPreDaemonEnable(id, orig); err != nil {
			w.logger.Warn("watchdog: could not persist pre-daemon pwm_enable to state",
				"path", id.LegacyPath, "value", orig, "err", err)
		}
	}
	return orig, nil
}

// persistRecoveredEnable writes the winning post-EINVAL fallback value
// to the LastKnownStore so the next prior-crash recovery skips the
// sequence walk. Called by Restore's fallback walker (via the
// hwmon backend) once it finds a value the chip accepts.
//
// Best-effort: errors are logged and the restore proceeds.
func (w *Watchdog) persistRecoveredEnable(id ChannelIdentity, value int) {
	if w.store == nil || id.LegacyPath == "" {
		return
	}
	if err := w.store.SetPreDaemonEnable(id, value); err != nil {
		w.logger.Warn("watchdog: could not persist recovered pwm_enable to state",
			"path", id.LegacyPath, "value", value, "err", err)
	}
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

// resolveChannelIdentity returns the rmmod-modprobe-stable identity
// for an hwmon pwmPath. Reads <hwmonN>/name and the <hwmonN>/device
// symlink target under the same per-syscall deadline budget Register
// uses for the pwm_enable read (RULE-WD-PER-SYSCALL-DEADLINE).
//
// Resolution falls back gracefully — on any deadline or read failure
// the returned identity carries an empty ChipName / BusAddr and the
// caller path through ChannelIdentity.Key() degrades to the legacy
// pwmPath shape. LegacyPath is always populated.
//
// pwmPath must point at a /sys/class/hwmon/hwmonN/pwmM file (or its
// rpm_target equivalent under the same dir); the function tolerates
// either by basing its readdir on filepath.Dir(pwmPath).
func resolveChannelIdentity(ctx context.Context, pwmPath string) ChannelIdentity {
	id := ChannelIdentity{LegacyPath: pwmPath}
	id.ChannelIdx = parseChannelIdx(filepath.Base(pwmPath))

	hwmonDir := filepath.Dir(pwmPath)
	if hwmonDir == "" || hwmonDir == "." {
		return id
	}
	if data, err := readWithDeadline(ctx, filepath.Join(hwmonDir, "name")); err == nil {
		id.ChipName = strings.TrimSpace(string(data))
	}
	// device is a symlink: resolve it via readlinkWithDeadline so the
	// kernel-side syscall is also bounded. We only need the parent
	// directory's basename — e.g. /sys/devices/platform/nct6687.2592
	// → "nct6687.2592" — to anchor the identity across hwmonN renumber.
	if target, err := readlinkWithDeadline(ctx, filepath.Join(hwmonDir, "device")); err == nil {
		// Strip everything before the platform/bus dot suffix. For
		// `nct6687.2592` we want `2592`; for PCI BDFs like `0000:01:00.0`
		// we keep the full bus tail since there is no leading driver
		// name component.
		base := filepath.Base(target)
		if dot := strings.LastIndex(base, "."); dot >= 0 && dot < len(base)-1 {
			suffix := base[dot+1:]
			// Heuristic: if the suffix is all digits the chip is a
			// platform device and we strip the driver-name prefix; if
			// it isn't (PCI BDFs end in `.0` which is digits — so this
			// branch hits both correctly), keep the full base.
			if isAllDigits(suffix) {
				id.BusAddr = suffix
			} else {
				id.BusAddr = base
			}
		} else {
			id.BusAddr = base
		}
	}
	return id
}

// parseChannelIdx extracts the trailing integer from "pwm12" / "fan3_target"
// style base names. Returns 0 on a non-matching shape — callers fall
// through to the legacy key shape via ChannelIdentity.Key().
func parseChannelIdx(base string) int {
	for i := len(base) - 1; i >= 0; i-- {
		c := base[i]
		if c < '0' || c > '9' {
			if i == len(base)-1 {
				return 0
			}
			v, err := strconv.Atoi(base[i+1:])
			if err != nil {
				return 0
			}
			return v
		}
	}
	v, err := strconv.Atoi(base)
	if err != nil {
		return 0
	}
	return v
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
		// onRecovery is the persistence callback the hwmon backend's
		// fallback walker fires after the first non-EINVAL write so the
		// next prior-crash recovery can skip the probe. Bound to the
		// captured identity so the watchdog's store key resolution
		// stays inside the watchdog package.
		identity := e.identity
		onRecovery := func(v int) { w.persistRecoveredEnable(identity, v) }
		ch = hal.Channel{
			ID:   e.pwmPath,
			Role: hal.RoleUnknown,
			Caps: caps,
			Opaque: halhwmon.State{
				PWMPath:          e.pwmPath,
				RPMTarget:        e.rpmTarget,
				OrigEnable:       e.origEnable,
				FallbackEnable:   e.fallbackSeq,
				OnEINVALRecovery: onRecovery,
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
