// Package sensorfreeze centrally records the per-sensor temperature readings
// every controller reports each tick and flags sensors that appear *stuck* —
// frozen at a single plausible value for a long span while the rest of the
// system is thermally active.
//
// This is the harder cousin of the per-read plausibility filters
// (RULE-CTRL-LOWTEMP-DISCONNECT, the sentinel guard): a stuck-but-plausible
// sensor — a temp pinned mid-range while the chip actually heats — passes every
// single-sample check, because nothing about one 40.0 °C reading is wrong. It
// can only be caught across *time* (the value never moves) and across *sensors*
// (something else in the box clearly is moving). The cross-sensor activity gate
// is what keeps a genuinely idle, steady-state machine — where every sensor
// legitimately sits flat — from tripping a false positive.
//
// Observability-only by design (RULE-DOCTOR-DETECTOR-STUCK-SENSOR): the tracker
// surfaces a doctor Warning, never a control action. Stuck detection is
// inherently heuristic and false-positive prone, so a wrong guess must do
// nothing worse than show the operator a card asking them to check a sensor —
// it must never change cooling. This mirrors the ebusy_storm and SwapHandler
// observability-only shipping decisions.
package sensorfreeze

import (
	"sort"
	"sync"
	"time"
)

const (
	// StuckMinDuration is how long a sensor must hold a single value
	// (within freezeEpsilonC) before it is a freeze candidate. Deliberately
	// long: real temps on an idle machine can legitimately read the same
	// whole/quantized value for a minute or two, so a short window would be
	// dominated by false positives. Five minutes of an unmoving reading,
	// combined with the activity gate, is a strong stuck signal.
	StuckMinDuration = 5 * time.Minute

	// RiseThresholdC is the temperature swing some *other* sensor must show,
	// over its recent window, for the system to count as thermally active.
	// Only then is a frozen sensor reported — a flat reading while the whole
	// box is also flat is just steady state, not a fault. 10 °C is well
	// above sensor jitter and normal idle drift.
	RiseThresholdC = 10.0

	// freezeEpsilonC is the band within which two consecutive readings count
	// as "the same value". hwmon temps are integer-millidegree quantized so
	// steady reads repeat exactly; a small epsilon only absorbs float
	// representation noise without admitting genuine drift.
	freezeEpsilonC = 0.05

	// staleTimeout drops a sensor that has stopped reporting (its controller
	// exited, the config dropped it). Without this a sensor frozen by virtue
	// of nobody updating it would flag forever. Two windows of silence is
	// unambiguously "no longer observed".
	staleTimeout = 2 * StuckMinDuration
)

// Stuck is one sensor the tracker currently believes is frozen, already
// filtered to "frozen long enough while the system is active" by Stuck().
type Stuck struct {
	// Name is the configured sensor name.
	Name string
	// ValueC is the value the sensor has been frozen at.
	ValueC float64
	// FrozenSeconds is how long the value has been held.
	FrozenSeconds int
	// ReferenceRiseC is the largest recent-window swing seen on any *other*
	// sensor — the evidence that the box is thermally active while this one
	// sits still.
	ReferenceRiseC float64
}

// sensorState is the O(1)-per-sensor freeze + activity bookkeeping. The
// activity range is tracked with two rolling buckets (current + previous,
// each StuckMinDuration wide) rather than a sample deque, bounding memory to a
// handful of floats per sensor while still reporting the swing over the last
// one-to-two windows.
type sensorState struct {
	constVal   float64   // value of the current unbroken constant run
	constSince time.Time // when the current constant run began
	lastSeen   time.Time

	bucketStart      time.Time
	curMin, curMax   float64
	prevMin, prevMax float64
	havePrev         bool
}

// rangeC is the temperature swing observed across the current and previous
// rolling buckets — an approximation of the last one-to-two windows.
func (s *sensorState) rangeC() float64 {
	mn, mx := s.curMin, s.curMax
	if s.havePrev {
		if s.prevMin < mn {
			mn = s.prevMin
		}
		if s.prevMax > mx {
			mx = s.prevMax
		}
	}
	return mx - mn
}

// Tracker holds the freeze state for every observed sensor. Safe for concurrent
// use: many controller goroutines push via Observe while the doctor reads via
// Stuck.
type Tracker struct {
	mu     sync.Mutex
	states map[string]*sensorState
}

// New constructs an empty tracker.
func New() *Tracker {
	return &Tracker{states: map[string]*sensorState{}}
}

// Observe records one temperature reading for a named sensor at time now. Wired
// as the controller's per-tick sensor-read hook (gated there to hwmon temp
// sensors — the only kind disconnected-pin / stuck-pin prone). Nil-safe so a
// monitor-only wiring path can pass (*Tracker)(nil).Observe.
func (t *Tracker) Observe(name string, valueC float64, now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.states[name]
	if !ok {
		t.states[name] = &sensorState{
			constVal:    valueC,
			constSince:  now,
			lastSeen:    now,
			bucketStart: now,
			curMin:      valueC,
			curMax:      valueC,
		}
		return
	}

	// Freeze run: a move beyond the epsilon restarts the constant run.
	if abs(valueC-s.constVal) > freezeEpsilonC {
		s.constVal = valueC
		s.constSince = now
	}
	s.lastSeen = now

	// Roll the activity buckets once a full window has elapsed; otherwise
	// widen the current bucket's range.
	if now.Sub(s.bucketStart) >= StuckMinDuration {
		s.prevMin, s.prevMax = s.curMin, s.curMax
		s.havePrev = true
		s.bucketStart = now
		s.curMin, s.curMax = valueC, valueC
		return
	}
	if valueC < s.curMin {
		s.curMin = valueC
	}
	if valueC > s.curMax {
		s.curMax = valueC
	}
}

// Stuck returns the sensors that have been frozen for at least StuckMinDuration
// while at least one *other* freshly-reporting sensor shows a recent swing of
// at least RiseThresholdC. Sorted by name for deterministic output. Nil-safe.
func (t *Tracker) Stuck(now time.Time) []Stuck {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// First pass: the largest recent swing among fresh sensors, and which
	// sensor produced it, so a sensor is never its own activity reference.
	maxRise, maxRiseName := 0.0, ""
	for name, s := range t.states {
		if now.Sub(s.lastSeen) > staleTimeout {
			continue
		}
		if r := s.rangeC(); r > maxRise {
			maxRise, maxRiseName = r, name
		}
	}

	var out []Stuck
	for name, s := range t.states {
		if now.Sub(s.lastSeen) > staleTimeout {
			continue
		}
		if now.Sub(s.constSince) < StuckMinDuration {
			continue
		}
		// The activity reference must be a *different* sensor. If the only
		// sensor meeting the rise threshold is this one, fall back to zero —
		// a frozen sensor cannot vouch for its own surroundings.
		ref := maxRise
		if name == maxRiseName {
			ref = secondLargestRise(t.states, now, name)
		}
		if ref < RiseThresholdC {
			continue
		}
		out = append(out, Stuck{
			Name:           name,
			ValueC:         s.constVal,
			FrozenSeconds:  int(now.Sub(s.constSince).Seconds()),
			ReferenceRiseC: ref,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// secondLargestRise returns the largest recent swing among fresh sensors other
// than exclude — used when the frozen candidate is itself the system's most
// active sensor (it can't be, in practice, since a frozen sensor has range ~0,
// but the guard keeps the activity reference honest regardless).
func secondLargestRise(states map[string]*sensorState, now time.Time, exclude string) float64 {
	best := 0.0
	for name, s := range states {
		if name == exclude {
			continue
		}
		if now.Sub(s.lastSeen) > staleTimeout {
			continue
		}
		if r := s.rangeC(); r > best {
			best = r
		}
	}
	return best
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
