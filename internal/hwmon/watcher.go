package hwmon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/ventd/ventd/internal/hwdiag"
)

// Default timings. README's "Plug a new fan or GPU in; ventd notices
// within ten seconds" promise is satisfied two ways:
//
//   - Uevent path (default on systemd / container hosts that allow
//     AF_NETLINK): reaction is sub-second after kernel emits the
//     ADD/REMOVE event.
//
//   - Periodic rescan path (fallback for environments where AF_NETLINK
//     is filtered, e.g. some restricted containers, custom seccomp
//     policies, or VENTD_DISABLE_UEVENT=1 set for testing): the rescan
//     ticker runs every 10 seconds, capping detection latency at the
//     same 10s upper bound the README promises.
//
// Cost: a rescan stats ~50 sysfs files on a typical desktop, finishing
// in well under 1 ms. Running every 10s adds negligible CPU.
const (
	defaultRescanPeriod = 10 * time.Second
	defaultDebounce     = 2 * time.Second
	// defaultRebindMinInterval caps how often the watcher can fire a rebind
	// trigger at the daemon. A flapping driver ("device gone" → "device back"
	// on a loose sensor connection) could otherwise pingpong the whole
	// controller teardown + restart cycle, which costs ~1-2s of pwm_enable=2
	// fallback every time per Option A's documented tradeoff.
	//
	// 10 s is deliberately larger than the 2 s settle window: the settle
	// guards a single topology-change event; the rebind rate limit guards
	// repeated add events crossing the settle line. At 10 s a genuinely
	// recurring flap surfaces in the hwdiag diagnostic but stops taking the
	// daemon down every cycle. See issue #95 for the full rationale.
	defaultRebindMinInterval = 10 * time.Second
)

// DeviceFingerprint captures the shape of one hwmon device to the precision
// the watcher cares about. Two devices with identical fingerprints are
// considered unchanged; any difference (chip name, class, or the sorted set
// of pwm/fan/temp basenames) produces a diagnostic.
//
// Fingerprints intentionally omit hwmonX indices and full paths: those churn
// across reboots even when no hardware changed. The stable-device path keys
// the map instead, and ChipName+Class+Bases names the content.
type DeviceFingerprint struct {
	ChipName string          `json:"chip_name"`
	Class    CapabilityClass `json:"class"`
	Bases    []string        `json:"bases"`
}

// Fingerprint derives a DeviceFingerprint from an enumerated HwmonDevice.
// Exported so tests and callers can assert fingerprint shape without going
// through the full watcher plumbing.
func Fingerprint(d HwmonDevice) DeviceFingerprint {
	var bases []string
	for _, p := range d.PWM {
		bases = append(bases, "pwm"+p.Index)
		if p.EnablePath != "" {
			bases = append(bases, "pwm"+p.Index+"_enable")
		}
		if p.FanInput != "" {
			bases = append(bases, filepath.Base(p.FanInput))
		}
	}
	for _, f := range d.FanInputs {
		bases = append(bases, filepath.Base(f))
	}
	for _, r := range d.RPMTargets {
		bases = append(bases, "fan"+r.Index+"_target")
	}
	for _, t := range d.TempInputs {
		bases = append(bases, filepath.Base(t))
	}
	bases = dedupSorted(bases)
	return DeviceFingerprint{
		ChipName: d.ChipName,
		Class:    d.Class,
		Bases:    bases,
	}
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var prev string
	for i, v := range in {
		if i == 0 || v != prev {
			out = append(out, v)
		}
		prev = v
	}
	return out
}

// Watcher observes /sys/class/hwmon for topology changes at runtime. It runs
// two complementary loops: a periodic rescan (safety net, 5 min) and a
// netlink-uevent consumer (event-driven, immediate). Transient flaps that
// resolve within the debounce window produce no diagnostic.
//
// The watcher is read-only. It never writes to sysfs, never modprobes, never
// mutates existing controller state. Detected changes only surface as a
// hwdiag.Entry the user can act on via the web UI.
type Watcher struct {
	logger *slog.Logger
	store  *hwdiag.Store

	// enumerate and subscribe are injection points. Production callers leave
	// both nil and NewWatcher substitutes EnumerateDevices + subscribeUevents.
	enumerate func() []HwmonDevice
	subscribe func(context.Context, *slog.Logger) <-chan UeventMessage

	rescanPeriod   time.Duration
	debounce       time.Duration
	disableUevents bool

	// rebindTrigger is invoked once per `action=added` promotion, rate-limited
	// by rebindMinInterval. Implementation lives in main.go — it inspects
	// liveCfg to decide whether the added device matches a configured
	// HwmonDevice and, if so, signals restartCh. Split this way so the
	// watcher package stays independent of config. See issue #95.
	rebindTrigger     RebindTrigger
	rebindMinInterval time.Duration

	// Internal state. Only the Run goroutine touches these; no locks needed.
	stable       map[string]DeviceFingerprint // confirmed topology; key = StableDevice
	pending      map[string]pendingChange     // in-flight candidates still inside the debounce window
	lastRebindAt time.Time                    // last wallclock at which rebindTrigger fired

	emissions   int // diagnostic emit count (exposed for tests)
	rebindCalls int // rebindTrigger invocation count (exposed for tests)
	rebindDrops int // rebindTrigger invocations suppressed by rate limit (exposed for tests)
}

// RebindTrigger is called on every `action=added` promotion, rate-limited to
// WithRebindMinInterval. key is the added device's StableDevice path; fp is
// the freshly promoted fingerprint.
//
// The callback runs on the watcher's Run goroutine — it must not block.
// Signalling an unbuffered channel inside the callback is a deadlock;
// use a buffered-by-one channel with a `select { case ch <- ...: default: }`
// send instead.
type RebindTrigger func(key string, fp DeviceFingerprint)

type pendingChange struct {
	fp         *DeviceFingerprint // nil means "device currently absent"
	observedAt time.Time
}

// Option configures a Watcher at construction time. Prefer the With*
// helpers — the struct is exported only so tests can build one directly.
type Option func(*Watcher)

// WithEnumerator replaces the hwmon enumeration function. Tests pass a
// deterministic mock; production leaves this unset.
func WithEnumerator(fn func() []HwmonDevice) Option {
	return func(w *Watcher) { w.enumerate = fn }
}

// WithRescanPeriod overrides the periodic rescan cadence. Production is 5
// minutes; tests use millisecond-scale values.
func WithRescanPeriod(d time.Duration) Option {
	return func(w *Watcher) { w.rescanPeriod = d }
}

// WithDebounce overrides the settle window. Production is 2 seconds; tests
// use shorter values to keep runtimes reasonable.
func WithDebounce(d time.Duration) Option {
	return func(w *Watcher) { w.debounce = d }
}

// WithoutUevents disables netlink entirely — the watcher runs on periodic
// rescan alone. Used by the VENTD_DISABLE_UEVENT env var path and by tests
// that exercise the safety-net cadence.
func WithoutUevents() Option {
	return func(w *Watcher) { w.subscribe = nil; w.disableUevents = true }
}

// WithRebindTrigger installs a callback fired on every action=added
// promotion, rate-limited to the watcher's rebind-min-interval. See
// RebindTrigger for the callback contract.
func WithRebindTrigger(t RebindTrigger) Option {
	return func(w *Watcher) { w.rebindTrigger = t }
}

// WithRebindMinInterval overrides the minimum interval between successive
// rebind-trigger invocations. Production is 10 s; tests use shorter values
// to exercise the rate-limit path quickly.
func WithRebindMinInterval(d time.Duration) Option {
	return func(w *Watcher) { w.rebindMinInterval = d }
}

// NewWatcher constructs a Watcher with production defaults, applying opts on
// top. store must be non-nil — a watcher without a diagnostic sink is a
// no-op; callers that genuinely don't want diagnostics should skip the
// watcher instead.
func NewWatcher(store *hwdiag.Store, logger *slog.Logger, opts ...Option) *Watcher {
	w := &Watcher{
		logger:            logger,
		store:             store,
		rescanPeriod:      defaultRescanPeriod,
		debounce:          defaultDebounce,
		rebindMinInterval: defaultRebindMinInterval,
		enumerate:         func() []HwmonDevice { return EnumerateDevices("") },
		subscribe:         subscribeUevents,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// withUevents reports whether the watcher should try to open a netlink
// subscription. Flipped by WithoutUevents (env-var-driven fallback mode).
func (w *Watcher) withUevents() bool { return !w.disableUevents }

// Run blocks until ctx is cancelled. The baseline snapshot is taken on
// entry; subsequent changes (from periodic rescan or uevent) are compared
// against it. Errors from the netlink goroutine degrade gracefully: the
// watcher keeps running on the periodic rescan alone.
func (w *Watcher) Run(ctx context.Context) error {
	if w.store == nil {
		return fmt.Errorf("hwmon watcher: nil diagnostic store")
	}

	w.stable = snapshotFingerprints(w.enumerate())
	w.pending = make(map[string]pendingChange)
	w.logger.Info("hwmon watcher started",
		"devices", len(w.stable),
		"rescan_period", w.rescanPeriod,
		"debounce", w.debounce,
		"rebind_min_interval", w.rebindMinInterval,
		"rebind_trigger_installed", w.rebindTrigger != nil)

	rescan := time.NewTicker(w.rescanPeriod)
	defer rescan.Stop()

	var uevents <-chan UeventMessage
	if w.withUevents() && w.subscribe != nil {
		uevents = w.subscribe(ctx, w.logger)
	}

	settle := time.NewTimer(time.Hour)
	if !settle.Stop() {
		<-settle.C
	}
	settleArmed := false

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("hwmon watcher stopped")
			return nil

		case <-rescan.C:
			if w.check(time.Now()) {
				if settleArmed && !settle.Stop() {
					select {
					case <-settle.C:
					default:
					}
				}
				settle.Reset(w.debounce + 50*time.Millisecond)
				settleArmed = true
			}

		case msg, ok := <-uevents:
			if !ok {
				uevents = nil // netlink goroutine exited; keep running on periodic only
				continue
			}
			w.logger.Debug("hwmon uevent", "action", msg.Action, "devpath", msg.DevPath)
			if w.check(time.Now()) {
				if settleArmed && !settle.Stop() {
					select {
					case <-settle.C:
					default:
					}
				}
				settle.Reset(w.debounce + 50*time.Millisecond)
				settleArmed = true
			}

		case <-settle.C:
			settleArmed = false
			if w.check(time.Now()) {
				settle.Reset(w.debounce + 50*time.Millisecond)
				settleArmed = true
			}
		}
	}
}

// check re-enumerates, compares against the stable snapshot, processes any
// pending candidates whose debounce has elapsed, and returns true when at
// least one pending change is still in flight (i.e. the caller should arm
// the settle timer).
func (w *Watcher) check(now time.Time) bool {
	devices := w.enumerate()
	current := snapshotFingerprints(devices)

	union := make(map[string]struct{}, len(w.stable)+len(current)+len(w.pending))
	for k := range w.stable {
		union[k] = struct{}{}
	}
	for k := range current {
		union[k] = struct{}{}
	}
	for k := range w.pending {
		union[k] = struct{}{}
	}

	for key := range union {
		stable, stableOK := w.stable[key]
		cur, curOK := current[key]

		// Case 1: current matches stable — no change, drop any pending flap.
		if stableOK == curOK && (!stableOK || reflect.DeepEqual(stable, cur)) {
			delete(w.pending, key)
			continue
		}

		// Case 2: a change is observed. Snapshot the current observed value.
		var observed *DeviceFingerprint
		if curOK {
			fp := cur
			observed = &fp
		}

		p, pendingExists := w.pending[key]
		if !pendingExists || !equalFPPtr(p.fp, observed) {
			// New (or flipped) candidate — start the debounce timer fresh.
			w.pending[key] = pendingChange{fp: observed, observedAt: now}
			continue
		}

		// Case 3: candidate has held its value; promote when the window elapses.
		if now.Sub(p.observedAt) >= w.debounce {
			w.promote(key, stable, observed, now)
			delete(w.pending, key)
		}
	}

	return len(w.pending) > 0
}

// promote updates stable, emits a diagnostic, and logs the change. The
// diagnostic is a single rolled-up entry (ID is constant) so the UI never
// accumulates per-device banners — the latest change wins.
func (w *Watcher) promote(key string, prev DeviceFingerprint, cur *DeviceFingerprint, now time.Time) {
	var action string
	switch {
	case cur == nil:
		action = "removed"
		delete(w.stable, key)
	case prev.ChipName == "" && len(prev.Bases) == 0:
		action = "added"
		w.stable[key] = *cur
	default:
		action = "changed"
		w.stable[key] = *cur
	}

	summary := hardwareSummary(action, key, prev, cur)
	w.logger.Info("hwmon topology change", "action", action, "device", key, "summary", summary)

	ctxMap := map[string]any{
		"action":        action,
		"stable_device": key,
	}
	if cur != nil {
		ctxMap["current"] = *cur
	}
	if prev.ChipName != "" || len(prev.Bases) != 0 {
		ctxMap["previous"] = prev
	}

	w.store.Set(hwdiag.Entry{
		ID:        hwdiag.IDHardwareChanged,
		Component: hwdiag.ComponentHardware,
		Severity:  hwdiag.SeverityInfo,
		Summary:   summary,
		Detail: "Ventd detected a change in the hardware it manages. Your existing fans keep their " +
			"current settings — nothing is altered automatically. Click **Re-run setup** to rescan " +
			"and pick up the change.",
		Timestamp: now,
		Affected:  []string{key},
		Context:   ctxMap,
		Remediation: &hwdiag.Remediation{
			AutoFixID: hwdiag.AutoFixReRunSetup,
			Label:     "Re-run setup",
			Endpoint:  "/api/setup/start",
		},
	})
	w.emissions++

	// Issue #95 — on `action=added`, invite the daemon to re-resolve hwmon
	// paths and rebind controllers. Scoped to `added` only; `removed` is a
	// separate tracker (fail-safe restore on a disappearing chip has its own
	// design). The trigger is rate-limited so a flapping driver doesn't
	// ping-pong controller teardowns.
	if action == "added" && cur != nil && w.rebindTrigger != nil {
		if now.Sub(w.lastRebindAt) >= w.rebindMinInterval {
			w.lastRebindAt = now
			w.rebindCalls++
			w.rebindTrigger(key, *cur)
		} else {
			w.rebindDrops++
			w.logger.Debug("hwmon rebind trigger suppressed by rate limit",
				"device", key,
				"chip_name", cur.ChipName,
				"since_last", now.Sub(w.lastRebindAt).String(),
				"min_interval", w.rebindMinInterval)
		}
	}
}

func hardwareSummary(action, key string, prev DeviceFingerprint, cur *DeviceFingerprint) string {
	name := filepath.Base(key)
	switch action {
	case "added":
		if cur != nil && cur.ChipName != "" {
			return fmt.Sprintf("New hardware detected: %s (%s)", cur.ChipName, name)
		}
		return fmt.Sprintf("New hardware detected: %s", name)
	case "removed":
		if prev.ChipName != "" {
			return fmt.Sprintf("Hardware removed: %s (%s)", prev.ChipName, name)
		}
		return fmt.Sprintf("Hardware removed: %s", name)
	default:
		if cur != nil && cur.ChipName != "" {
			return fmt.Sprintf("Hardware configuration changed: %s (%s)", cur.ChipName, name)
		}
		return "Hardware configuration changed"
	}
}

func equalFPPtr(a, b *DeviceFingerprint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(*a, *b)
}

// snapshotFingerprints converts a device slice into a StableDevice-keyed map.
// Devices without a resolvable stable path fall back to the hwmonX Dir so the
// watcher still tracks them (the UI diagnostic just reads less neatly for
// those — a cosmetic issue, not a correctness one).
func snapshotFingerprints(devs []HwmonDevice) map[string]DeviceFingerprint {
	out := make(map[string]DeviceFingerprint, len(devs))
	for _, d := range devs {
		key := d.StableDevice
		if key == "" {
			key = d.Dir
		}
		out[key] = Fingerprint(d)
	}
	return out
}
