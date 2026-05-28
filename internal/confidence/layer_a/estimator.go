package layer_a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Bin width and count from spec-v0_5_9 §2.4 / RULE-CONFA-COVERAGE-01.
const (
	NumBins                 = 16
	BinWidth                = 16 // raw PWM units per bin (0/16/32/.../240)
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

// PersistEvery is the cadence of the periodic-save goroutine the
// daemon spins up via Estimator.Run (RULE-CONFA-PERSIST-RUNNER-01).
// Matches internal/coupling.PersistEvery and internal/marginal.PersistEvery
// so all three smart-mode layers persist in lockstep. Declared as a var
// (not a const) so persistence-runner tests can override it; production
// callers must not write to it.
var PersistEvery = time.Minute

// Config drives Estimator construction.
type Config struct{}

// Estimator owns per-channel Layer-A state. Hot-path Observe is
// O(1); Read is lock-free via atomic.Pointer; Save/Load persist a
// Bucket shape compatible with R15 §104.
type Estimator struct {
	mu       sync.Mutex
	channels map[string]*channelState

	// Persistence-runner inputs, set by SetPersistContext. Stored on
	// the Estimator (rather than threaded through Run's signature) so
	// Run's surface matches internal/coupling.Runtime.Run and
	// internal/marginal.Runtime.Run — daemon wiring in cmd/ventd/main.go
	// invokes all three the same way. stateDir == "" disables the
	// runner; hwmonFingerprint is stamped on every persisted bucket.
	// Protected by mu.
	stateDir         string
	hwmonFingerprint string
	logger           *slog.Logger

	// runStarted is set by Run on first entry and guarantees the
	// periodic-save loop is launched at most once over the
	// Estimator's lifetime (mirrors internal/coupling.Runtime.runStarted
	// and internal/marginal.Runtime.runStarted). Protected by mu.
	runStarted bool
}

// SetPersistContext stamps the state directory + hwmon fingerprint
// the periodic-save runner will use. Called once by
// cmd/ventd/smart_builders.go:buildLayerAEstimator after the load
// loop completes. Safe to call before Run; not safe to call
// concurrently with Run. A subsequent Run with an empty stateDir
// short-circuits to a no-op runner.
//
// logger may be nil — Run falls back to slog.Default().
func (e *Estimator) SetPersistContext(stateDir, hwmonFingerprint string, logger *slog.Logger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stateDir = stateDir
	e.hwmonFingerprint = hwmonFingerprint
	e.logger = logger
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

// Run drives the periodic-save loop until ctx is cancelled. Mirrors
// internal/coupling.Runtime.Run and internal/marginal.Runtime.Run so
// all three smart-mode layers persist in lockstep at PersistEvery
// cadence (1 min). Layer-A is simpler than B/C because Save already
// walks every channel internally, so one ticker and one Save per tick
// is the whole loop.
//
// Without this runner, Save is never called in production: only
// LoadChannel runs at startup (cmd/ventd/smart_builders.go:
// buildLayerAEstimator), so a daemon restart always finds zero
// persisted state and cold-starts every channel — conf_A collapses
// to √(1/16) = 0.25 on the first idle PWM bin and never recovers
// without an external excitation source. This is the regression
// RULE-CONFA-PERSIST-RUNNER-01 pins.
//
// The state directory + hwmon fingerprint + logger are read from the
// Estimator (set by SetPersistContext). An empty stateDir short-
// circuits the runner — matches the empty-stateDir case in
// buildLayerAEstimator where state-dir resolution failed and the
// daemon runs in-memory only.
//
// Calling Run twice on the same Estimator returns an error — matches
// coupling.Runtime.Run / marginal.Runtime.Run.
func (e *Estimator) Run(ctx context.Context) error {
	e.mu.Lock()
	stateDir := e.stateDir
	hwmonFingerprint := e.hwmonFingerprint
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	if stateDir == "" {
		e.mu.Unlock()
		logger.Info("layer_a: persistence runner not started (stateDir empty)")
		return nil
	}
	if e.runStarted {
		e.mu.Unlock()
		return errors.New("layer_a: Run already called")
	}
	e.runStarted = true
	channelCount := len(e.channels)
	e.mu.Unlock()

	logger.Info("layer_a: persistence runner started",
		"channels", channelCount,
		"persist_every", PersistEvery,
		"state_dir", stateDir)
	defer logger.Info("layer_a: persistence runner stopped")

	persistTick := time.NewTicker(PersistEvery)
	defer persistTick.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final save before exit, matching Layer-B / Layer-C shutdown
			// behaviour. Errors are warned but never returned — the daemon
			// is shutting down anyway, and the next start will cold-start
			// from whatever the prior periodic save persisted.
			if err := e.Save(stateDir, hwmonFingerprint); err != nil {
				logger.Warn("layer_a: shutdown save failed", "err", err)
			}
			return ctx.Err()
		case <-persistTick.C:
			if err := e.Save(stateDir, hwmonFingerprint); err != nil {
				logger.Warn("layer_a: periodic save failed", "err", err)
				continue
			}
			logger.Debug("layer_a: periodic save complete",
				"channels", channelCount)
		}
	}
}
