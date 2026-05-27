// Package massstall tracks system-wide concurrent fan-stall state so the
// v0.5.9 confidence controller's w_pred_system global gate can drop
// predictive control to safe when several fans are stalled at once.
//
// A single dead or operator-parked (AllowStop) fan is normal and must NOT
// disable predictive control. A *cluster* of fans that are commanded to
// spin but read zero RPM is the signature of a system-level cooling fault
// (PSU sag, a failed fan hub, a tripped header rail) — exactly the
// condition under which the learned predictive arm should yield to the
// reactive curve until the operator intervenes. Per spec-v0_5_9 §2.5
// ("no mass-stall in the last MassStallDuration").
//
// The controllers feed the tracker once per committed tick via Report;
// the gate evaluator reads MassStalled on its own cadence. The stall
// pattern is the same one the controller's stuck-fan warning uses:
// commanded PWM at or above the stiction floor AND an observed tach of
// exactly zero. A tach-less or failed read (-1) is NOT a stall — it
// cannot be distinguished from a healthy fan, so it is never counted.
//
// State is per-channel last-stall timestamps. A channel that reports a
// non-stall (recovered, or dropped below the floor) is cleared
// immediately; a channel that stops reporting entirely (e.g. its
// controller goroutine exited while stalled) expires after Window so a
// phantom stall cannot linger forever. The tracker therefore answers
// "are >= MinChannels fans stalled right now (within Window)", not
// "did a stall ever happen".
package massstall

import (
	"sort"
	"sync"
	"time"
)

// StallPWMFloor mirrors the controller's stuckFanWarnPWMFloor (77 = 30%
// of 255): a fan commanded below this byte that reads zero RPM is below
// the stiction floor, working as intended, not stalled.
const StallPWMFloor uint8 = 77

// DefaultWindow is the MassStallDuration from spec-v0_5_9 §2.5: a stalled
// channel that stops reporting expires from the count after this long.
const DefaultWindow = 3 * time.Minute

// DefaultMinChannels is the smallest concurrent-stall count that trips a
// mass-stall. Two is the minimum that distinguishes a single dead /
// AllowStop fan (legitimate) from a system-level cooling failure.
const DefaultMinChannels = 2

// Tracker is a thread-safe system-wide concurrent-stall counter. The
// zero value is not usable; construct with New. Report is called from
// the per-fan control loops (one goroutine per channel); MassStalled /
// Snapshot are called from the gate evaluator goroutine.
type Tracker struct {
	mu          sync.Mutex
	window      time.Duration
	minChannels int
	// lastStall holds, per channel, the wall-clock of the most recent
	// tick whose report matched the stall pattern. A non-stall report
	// deletes the entry.
	lastStall map[string]time.Time
}

// New constructs a Tracker. Non-positive window or minChannels < 1 fall
// back to the package defaults.
func New(window time.Duration, minChannels int) *Tracker {
	if window <= 0 {
		window = DefaultWindow
	}
	if minChannels < 1 {
		minChannels = DefaultMinChannels
	}
	return &Tracker{
		window:      window,
		minChannels: minChannels,
		lastStall:   make(map[string]time.Time),
	}
}

// Report folds one channel's committed tick into the tracker. commandedPWM
// is the byte just written; observedRPM is the tach reading (-1 when
// tach-less or the read failed). The stall pattern is
// commandedPWM >= StallPWMFloor && observedRPM == 0; any other report
// (including a tach-less -1, or a recovery to RPM > 0) clears the channel.
// nil-safe.
func (t *Tracker) Report(channelID string, commandedPWM uint8, observedRPM int32, now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if commandedPWM >= StallPWMFloor && observedRPM == 0 {
		t.lastStall[channelID] = now
		return
	}
	delete(t.lastStall, channelID)
}

// MassStalled reports whether at least MinChannels distinct channels each
// have a stall observation within the last Window ending at now. Stale
// entries (older than Window) are pruned as a side effect. nil-safe
// (returns false), so a monitor-only daemon with no tracker never gates.
func (t *Tracker) MassStalled(now time.Time) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.countLocked(now) >= t.minChannels
}

// Snapshot returns the count and sorted IDs of channels currently stalled
// (within Window at now), for the API / doctor surface. Prunes stale
// entries. nil-safe.
func (t *Tracker) Snapshot(now time.Time) (int, []string) {
	if t == nil {
		return 0, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.lastStall))
	for ch, ts := range t.lastStall {
		if now.Sub(ts) <= t.window {
			ids = append(ids, ch)
		} else {
			delete(t.lastStall, ch)
		}
	}
	sort.Strings(ids)
	return len(ids), ids
}

// countLocked returns the number of channels stalled within Window and
// prunes the rest. Caller holds t.mu.
func (t *Tracker) countLocked(now time.Time) int {
	count := 0
	for ch, ts := range t.lastStall {
		if now.Sub(ts) <= t.window {
			count++
		} else {
			delete(t.lastStall, ch)
		}
	}
	return count
}
