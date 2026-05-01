package layer_a

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Bin width and count from spec-v0_5_9 §2.4 / RULE-CONFA-COVERAGE-01.
const (
	NumBins   = 16
	BinWidth  = 16 // raw PWM units per bin (0/16/32/.../240)
	MinObsPerBinForCoverage = 3
)

// RecencyTau is the time constant for recency = exp(-age/τ).
// 7 days per spec-v0_5_9 §2.4 / RULE-CONFA-RECENCY-01.
const RecencyTau = 7 * 24 * time.Hour

// DefaultNoiseFloor is the tach'd-channel noise floor from R6.
// 150 RPM applied to tier 0/1/2 channels by default; callers may
// override per-tier when admitting to a tach-less tier.
const DefaultNoiseFloor = 150.0

// NormResidualScale is the multiplier on noise floor for the
// norm_residual denominator. Per spec-v0_5_9 §2.4:
//
//	norm_residual = clamp(rms_residual / (5 · noise_floor), 0, 1)
const NormResidualScale = 5.0

// Config drives Estimator construction.
type Config struct{}

// Estimator owns per-channel Layer-A state. Hot-path Observe is
// O(1); Read is lock-free via atomic.Pointer; Save/Load persist a
// Bucket shape compatible with R15 §104.
type Estimator struct {
	mu       sync.Mutex
	channels map[string]*channelState
}

// channelState holds the in-memory per-channel histogram and counters.
// The published Snapshot pointer is replaced atomically on every
// Observe to give Read its lock-free guarantee.
type channelState struct {
	tier       uint8
	noiseFloor float64

	binCounts        [NumBins]uint32
	binResidualSumSq [NumBins]float64

	lastUpdate       time.Time
	tierPinnedUntil  time.Time
	seenFirstContact bool

	// snapshot is the lock-free view. Mutators take c.mu; readers
	// load this pointer without any lock.
	snapshot atomic.Pointer[Snapshot]
}

// New constructs an empty Estimator. Channels are admitted lazily
// via Admit + Observe.
func New(cfg Config) (*Estimator, error) {
	return &Estimator{channels: make(map[string]*channelState)}, nil
}

// Admit registers a channel with an initial tier and noise floor.
// Idempotent — a second Admit with the same channelID updates the
// tier + noise floor (e.g. after R8 tier-change re-classification).
//
// noiseFloor ≤ 0 falls back to DefaultNoiseFloor.
func (e *Estimator) Admit(channelID string, tier uint8, noiseFloor float64, now time.Time) error {
	if channelID == "" {
		return errors.New("layer_a: empty channelID")
	}
	if noiseFloor <= 0 {
		noiseFloor = DefaultNoiseFloor
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.channels[channelID]
	if !ok {
		c = &channelState{}
		e.channels[channelID] = c
	}
	c.tier = tier
	c.noiseFloor = noiseFloor
	if c.lastUpdate.IsZero() {
		c.lastUpdate = now
	}
	publish(channelID, c, now)
	return nil
}

// Observe is the controller hot path. Called after every successful
// PWM write. Updates the bin histogram and residual sum-of-squares,
// then republishes the Snapshot.
//
// Args:
//   - pwmWritten: the actual PWM byte just written (0..255)
//   - rpm: the observed RPM at this tick (-1 when tach-less)
//   - predictedRPM: the controller's prediction at the previous tick
//     (used to compute the residual ε = predicted − observed)
//   - now: wall-clock for recency tracking
//
// A predictedRPM == 0 OR rpm < 0 skips the residual update for this
// observation but still increments the bin count.
func (e *Estimator) Observe(channelID string, pwmWritten uint8, rpm int32, predictedRPM int32, now time.Time) {
	e.mu.Lock()
	c, ok := e.channels[channelID]
	if !ok {
		// Auto-admit at TierRPMTach with default noise floor when the
		// caller hasn't yet. The wiring layer (PR-B) will Admit
		// explicitly with the resolved tier; this fallback prevents
		// drops on the very first tick before Admit lands.
		c = &channelState{tier: TierRPMTach, noiseFloor: DefaultNoiseFloor, lastUpdate: now}
		e.channels[channelID] = c
	}

	bin := int(pwmWritten) / BinWidth
	if bin >= NumBins {
		bin = NumBins - 1
	}
	c.binCounts[bin]++

	if rpm >= 0 && predictedRPM > 0 {
		eps := float64(predictedRPM - rpm)
		c.binResidualSumSq[bin] += eps * eps
	}
	c.lastUpdate = now
	publish(channelID, c, now)
	e.mu.Unlock()
}

// Read returns the most recent snapshot for channelID. Lock-free via
// atomic.Pointer. Returns nil when the channel has never been admitted
// or observed.
func (e *Estimator) Read(channelID string) *Snapshot {
	e.mu.Lock()
	c, ok := e.channels[channelID]
	e.mu.Unlock()
	if !ok {
		return nil
	}
	return c.snapshot.Load()
}

// SnapshotAll returns a stable copy of every channel's snapshot.
// Order is unspecified.
func (e *Estimator) SnapshotAll() []*Snapshot {
	e.mu.Lock()
	out := make([]*Snapshot, 0, len(e.channels))
	for _, c := range e.channels {
		if s := c.snapshot.Load(); s != nil {
			out = append(out, s)
		}
	}
	e.mu.Unlock()
	return out
}

// MarkFirstContact records that this channel has had its first
// successful w_pred>0 tick. Called by the controller exactly once
// per channel per lifetime; persisted by the next periodic Save.
func (e *Estimator) MarkFirstContact(channelID string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.channels[channelID]
	if !ok {
		return
	}
	if c.seenFirstContact {
		return
	}
	c.seenFirstContact = true
	publish(channelID, c, now)
}

// publish builds a Snapshot from c and stores it in c.snapshot
// atomically. Caller MUST hold e.mu (or own c exclusively, e.g.
// during Load). The age + ConfA computation is done here so Read is
// truly lock-free.
func publish(channelID string, c *channelState, now time.Time) {
	cov := computeCoverage(&c.binCounts)
	rms := computeRMSResidual(&c.binCounts, &c.binResidualSumSq)
	age := now.Sub(c.lastUpdate)
	if age < 0 {
		age = 0
	}
	conf := computeConfA(c.tier, cov, rms, c.noiseFloor, age)

	s := &Snapshot{
		ChannelID:        channelID,
		Tier:             c.tier,
		R8Ceiling:        R8Ceiling(c.tier),
		Coverage:         cov,
		RMSResidual:      rms,
		NoiseFloor:       c.noiseFloor,
		Age:              age,
		ConfA:            conf,
		SeenFirstContact: c.seenFirstContact,
	}
	c.snapshot.Store(s)
}

// computeCoverage = |{bin: count ≥ MinObsPerBinForCoverage}| / NumBins.
// RULE-CONFA-COVERAGE-01.
func computeCoverage(counts *[NumBins]uint32) float64 {
	covered := 0
	for _, n := range counts {
		if n >= MinObsPerBinForCoverage {
			covered++
		}
	}
	return float64(covered) / float64(NumBins)
}

// computeRMSResidual = sqrt(ΣΣε² / Σn) summed across all bins with
// samples. A bin with no observations contributes nothing.
func computeRMSResidual(counts *[NumBins]uint32, sumSq *[NumBins]float64) float64 {
	var totalN uint64
	var totalSq float64
	for i := range counts {
		totalN += uint64(counts[i])
		totalSq += sumSq[i]
	}
	if totalN == 0 {
		return 0
	}
	return math.Sqrt(totalSq / float64(totalN))
}

// computeConfA is RULE-CONFA-FORMULA-01:
//
//	conf_A = R8_ceiling × √coverage × (1 − norm_residual) × recency
//
// with norm_residual = clamp(rms / (5·noise_floor), 0, 1) and
// recency = exp(-age / 7d).
func computeConfA(tier uint8, coverage, rms, noiseFloor float64, age time.Duration) float64 {
	ceil := R8Ceiling(tier)
	if ceil == 0 {
		return 0
	}
	if coverage < 0 {
		coverage = 0
	}
	if coverage > 1 {
		coverage = 1
	}
	covTerm := math.Sqrt(coverage)

	denom := NormResidualScale * noiseFloor
	if denom <= 0 {
		denom = NormResidualScale * DefaultNoiseFloor
	}
	norm := rms / denom
	if norm < 0 {
		norm = 0
	}
	if norm > 1 {
		norm = 1
	}
	residualTerm := 1.0 - norm

	recency := math.Exp(-age.Seconds() / RecencyTau.Seconds())

	out := ceil * covTerm * residualTerm * recency
	if out < 0 {
		out = 0
	}
	if out > 1 {
		out = 1
	}
	return out
}

// String makes Estimator self-describing in error messages.
func (e *Estimator) String() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprintf("layer_a.Estimator{channels=%d}", len(e.channels))
}
