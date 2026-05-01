// Package signguard implements R27's wrong-direction-prior detector
// for v0.5.8 Layer-C. It consumes the v0.5.5 opportunistic-probe
// observation-record stream and votes on whether each channel's
// Layer-B b_ii sign matches the expected polarity for a cooling fan
// (ΔPWM > 0 should produce ΔT < 0).
//
// Per RULE-SGD-VOTE-01: ≥5 of last 7 sign-vote agreements promote
// the channel to "polarity-confirmed". RULE-SGD-NOISE-01 discards
// probes whose |ΔT| is below 2× the R11 noise floor (uninformative).
// RULE-SGD-CONT-01 keeps the detector running continuously — a
// re-cabled fan that flips polarity mid-deployment is caught at
// any point in daemon lifetime.
package signguard

import (
	"sync"
)

// VoteWindow is the rolling sample size; ≥5/7 agreements promote.
const (
	VoteWindow      = 7
	VoteThreshold   = 5
	NoiseFloorDelta = 2.0 // °C; per R11 §0 the unambiguous floor is 2°C.
)

// Sample is one ground-truth datum: a known PWM step and the
// observed temperature change over the probe-hold window.
//
// Convention: for a cooling fan with correct polarity convention,
// positive ΔPWM should produce negative ΔT; the product
// `sign(ΔPWM) · sign(ΔT)` is the sign-vote contribution. -1 = agree
// (correct polarity), +1 = disagree (sign-flipped).
type Sample struct {
	ChannelID string
	DeltaPWM  int8    // signed delta: +1, -1, or 0 (skipped)
	DeltaT    float64 // °C
}

// Detector is the per-(channel) sign-vote aggregator. Concurrent
// Add / Confirmed calls are serialised by mu. Votes that fall in
// the noise floor are ignored.
type Detector struct {
	mu   sync.Mutex
	hist map[string]*history
}

type history struct {
	// votes[i] is -1 (agree, correct polarity), +1 (disagree,
	// sign-flipped), or 0 (un-cast — buffer not full).
	votes [VoteWindow]int8
	head  int
	count int
}

// NewDetector constructs an empty Detector. Use Add to feed
// opportunistic-probe samples; Confirmed reads the latest vote.
func NewDetector() *Detector {
	return &Detector{hist: make(map[string]*history)}
}

// Add folds one sample into the channel's rolling vote. Returns
// false when the sample was discarded as below the noise floor.
func (d *Detector) Add(s Sample) bool {
	if s.ChannelID == "" || s.DeltaPWM == 0 {
		return false
	}
	if abs(s.DeltaT) < NoiseFloorDelta {
		return false
	}
	vote := signProduct(int(s.DeltaPWM), s.DeltaT)
	d.mu.Lock()
	defer d.mu.Unlock()
	h, ok := d.hist[s.ChannelID]
	if !ok {
		h = &history{}
		d.hist[s.ChannelID] = h
	}
	h.votes[h.head] = vote
	h.head = (h.head + 1) % VoteWindow
	if h.count < VoteWindow {
		h.count++
	}
	return true
}

// Confirmed returns true when the channel's most recent VoteWindow
// samples include at least VoteThreshold "agree" votes (-1). A
// channel with fewer than VoteWindow samples cannot be confirmed —
// avoids premature promotion.
func (d *Detector) Confirmed(channelID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	h, ok := d.hist[channelID]
	if !ok || h.count < VoteWindow {
		return false
	}
	agree := 0
	for _, v := range h.votes {
		if v == -1 {
			agree++
		}
	}
	return agree >= VoteThreshold
}

// signProduct returns -1 when sign(ΔPWM) · sign(ΔT) is negative
// (= correct polarity for a cooling fan), +1 when positive
// (= sign-flipped). ΔT == 0 returns 0; caller filters via the
// noise-floor gate before this is reached.
func signProduct(deltaPWM int, deltaT float64) int8 {
	switch {
	case deltaPWM > 0 && deltaT < 0,
		deltaPWM < 0 && deltaT > 0:
		return -1 // agree: cooling fan, correct polarity
	case deltaPWM > 0 && deltaT > 0,
		deltaPWM < 0 && deltaT < 0:
		return +1 // disagree: sign-flipped
	default:
		return 0
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
