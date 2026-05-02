package marginal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// FallbackLabelDisabled / FallbackLabelWarming are R7's reserved
// labels emitted when the signature library is shutdown or warming.
// RULE-CMB-LIB-02 — Layer-C never creates shards keyed on these.
const (
	FallbackLabelDisabled = "fallback/disabled"
	FallbackLabelWarming  = "fallback/warming"
)

// MaxShardsPerChannel is the per-channel shard map cap (spec §2.3).
// 95th-percentile signature distribution per user; long-tail eviction
// uses HitCount × exp(-(age/τ)) with τ=14 days.
const MaxShardsPerChannel = 32

// IdentifiabilityCheckEvery / PersistEvery mirror v0.5.7 cadences.
const (
	IdentifiabilityCheckEvery = time.Minute
	PersistEvery              = time.Minute
	ActivationRetryAfter      = time.Hour // RULE-CMB-IDENT-01: τ_retry
)

// ShardLookup is the minimal Layer-B interface marginal.Runtime
// consumes for prior seeding + parent warmup gate. Extracted so
// tests can stub it without importing the full coupling package.
type ShardLookup interface {
	// Shard returns the parent Layer-B shard or nil. Implementations:
	// coupling.Runtime.Shard(channelID).
	Shard(channelID string) ShardSnapshotReader
}

// ShardSnapshotReader is the read-side interface: Layer-B's snapshot
// is consumed for parent-warmup, κ check, and b_ii prior. The real
// type is *coupling.Shard which exposes Read() *coupling.Snapshot;
// here we keep an interface with just the bits we need.
type ShardSnapshotReader interface {
	Read() ParentSnapshot
}

// ParentSnapshot is the Layer-B view marginal needs at admission.
type ParentSnapshot struct {
	WarmingUp bool
	Kind      SnapshotKind
	BiiAtZero float64 // self-coupling diagonal for prior seeding
}

// SignguardLookup is the minimal interface marginal consumes from
// signguard. Returns true when the channel's b_ii sign has been
// confirmed by ≥5/7 opportunistic-probe agreements.
type SignguardLookup interface {
	Confirmed(channelID string) bool
}

// noSignguard is the trivial implementation when no signguard is
// wired (tests / monitor-only systems). Always returns false →
// Layer-B prior is never consumed; θ admits at zero.
type noSignguard struct{}

func (noSignguard) Confirmed(string) bool { return false }

// Runtime owns the Layer-C goroutine pool. One goroutine per active
// shard inbox (RULE-CMB-RUNTIME-01); OnObservation is non-blocking
// (RULE-CMB-RUNTIME-02).
type Runtime struct {
	stateDir         string
	hwmonFingerprint string
	logger           *slog.Logger

	parents   ShardLookup
	signguard SignguardLookup

	// pwmUnitMax is used to normalise b_ii into β_0 per
	// RULE-CMB-PRIOR-01. Defaults to 255 for duty_0_255 channels.
	pwmUnitMax int

	mu sync.Mutex
	// shards is keyed by (channel, signature) → *Shard.
	shards map[shardKey]*Shard
	// channelCounts mirrors len(shards) per channel for the cap
	// check (RULE-CMB-LIB-01); avoids O(n) scan on each tick.
	channelCounts map[string]int
	// lastFailedActivation records the time we last refused to
	// admit a (channel, sig) shard because the parent was κ-bad.
	lastFailedActivation map[shardKey]time.Time
	// recentPWM tracks per-channel PWM history for the OAT gate
	// (RULE-CMB-OAT-01). Index 0 is the most recent.
	recentPWM map[string]*ringBuffer

	runStarted bool
}

type shardKey struct {
	channel   string
	signature string
}

// ringBuffer holds the last N PWM values per channel for the OAT
// gate. N=5 per spec §2.6.
type ringBuffer struct {
	vals [5]uint8
	head int
	full bool
}

func (r *ringBuffer) push(v uint8) {
	r.vals[r.head] = v
	r.head = (r.head + 1) % len(r.vals)
	if r.head == 0 {
		r.full = true
	}
}

// allEqual returns true when every sample in the buffer equals the
// most recent value. False until the buffer is full.
func (r *ringBuffer) allEqual() bool {
	if !r.full {
		return false
	}
	last := r.vals[(r.head-1+len(r.vals))%len(r.vals)]
	for _, v := range r.vals {
		if v != last {
			return false
		}
	}
	return true
}

// NewRuntime constructs a Runtime; goroutine pool starts on Run.
//
// parents may be nil when v0.5.8 ships standalone (no Layer-B
// integration); admission then never seeds from a Layer-B prior.
// signguard may be nil with the same effect.
func NewRuntime(
	stateDir, hwmonFingerprint string,
	parents ShardLookup,
	signguard SignguardLookup,
	logger *slog.Logger,
) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	if signguard == nil {
		signguard = noSignguard{}
	}
	return &Runtime{
		stateDir:             stateDir,
		hwmonFingerprint:     hwmonFingerprint,
		logger:               logger,
		parents:              parents,
		signguard:            signguard,
		pwmUnitMax:           255,
		shards:               make(map[shardKey]*Shard),
		channelCounts:        make(map[string]int),
		lastFailedActivation: make(map[shardKey]time.Time),
		recentPWM:            make(map[string]*ringBuffer),
	}
}

// SetPWMUnitMax overrides the default 255 for non-duty_0_255
// channels (e.g. step_0_N or cooling_level — see RULE-HWDB-PR2-04).
func (r *Runtime) SetPWMUnitMax(n int) { r.pwmUnitMax = n }

// Shard returns the Layer-C shard for (channelID, signatureLabel),
// or nil when no shard exists.
func (r *Runtime) Shard(channelID, signatureLabel string) *Shard {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shards[shardKey{channel: channelID, signature: signatureLabel}]
}

// ShardCount returns the number of live shards for the given channel.
// Used by R13 doctor surface.
func (r *Runtime) ShardCount(channelID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.channelCounts[channelID]
}

// SnapshotAll returns a copy of every shard's current Snapshot.
// Lock-free reads; safe for the v0.5.10 doctor surface.
func (r *Runtime) SnapshotAll() []*Snapshot {
	r.mu.Lock()
	out := make([]*Snapshot, 0, len(r.shards))
	for _, s := range r.shards {
		if snap := s.Read(); snap != nil {
			out = append(out, snap)
		}
	}
	r.mu.Unlock()
	return out
}

// ObservationInput is the minimal per-tick payload Layer-C consumes.
// Equivalent to a stripped-down observation.Record so the marginal
// package does not depend on internal/observation directly.
type ObservationInput struct {
	Now            time.Time
	ChannelID      string
	SignatureLabel string
	PWMWritten     uint8
	DeltaT         float64 // °C — caller computes from rec.SensorReadings
	Load           float64 // PSI cpu.some avg10 (preferred) or loadavg/1
}

// OnObservation is the non-blocking entry point called from the
// controller's ObsAppend hook. It enforces:
//
//   - fallback/* labels skipped (RULE-CMB-LIB-02).
//   - OAT gate: every other channel's PWM must have been static
//     for the last 5 ticks (RULE-CMB-OAT-01).
//   - Per-channel cap (RULE-CMB-LIB-01) with weighted-LRU eviction.
//   - Parent Layer-B κ > 10⁴ → defer activation by τ_retry.
func (r *Runtime) OnObservation(in ObservationInput) {
	if in.SignatureLabel == FallbackLabelDisabled ||
		in.SignatureLabel == FallbackLabelWarming ||
		in.SignatureLabel == "" {
		return
	}

	// OAT gate (RULE-CMB-OAT-01). The current channel's PWM history
	// is updated AFTER the gate so we only assess "other channels"
	// in the buffer.
	r.mu.Lock()
	oatPass := r.oatGate(in.ChannelID)
	r.bumpRecentPWM(in.ChannelID, in.PWMWritten)
	r.mu.Unlock()
	if !oatPass {
		return
	}

	key := shardKey{channel: in.ChannelID, signature: in.SignatureLabel}

	r.mu.Lock()
	s := r.shards[key]
	if s == nil {
		// Defer activation when last failure was within τ_retry
		// (RULE-CMB-IDENT-01).
		if last, ok := r.lastFailedActivation[key]; ok &&
			in.Now.Sub(last) < ActivationRetryAfter {
			r.mu.Unlock()
			return
		}
		s = r.admitLocked(key, in.Now)
		if s == nil {
			r.mu.Unlock()
			return
		}
	}
	r.mu.Unlock()

	// Update the RLS estimate (RULE-CMB-SHARD-02 / -03).
	phi := []float64{1.0, in.Load}
	// y = ΔT given a unit-PWM excitation. Caller supplies ΔT/ΔPWM
	// by convention (or raw ΔT when ΔPWM = 1). For non-unit ΔPWM
	// the controller divides; this keeps the estimator dimensionless
	// in PWM units.
	if err := s.Update(in.Now, phi, in.DeltaT); err != nil {
		r.logger.Warn("marginal: Update failed",
			"channel", in.ChannelID,
			"signature", in.SignatureLabel,
			"err", err)
	}

	// Path-B observed-saturation gate.
	s.ObserveOutcome(in.DeltaT, in.PWMWritten)
}

// admitLocked creates and registers a new shard. Caller holds r.mu.
// Returns nil when the cap forces an eviction failure or when parent
// Layer-B is κ-bad.
func (r *Runtime) admitLocked(key shardKey, now time.Time) *Shard {
	cnt := r.channelCounts[key.channel]
	if cnt >= MaxShardsPerChannel {
		// Evict the oldest-LRU shard for this channel (R7 §Q5
		// scoring with τ=14d).
		evicted := r.evictLRUForChannelLocked(key.channel)
		if evicted == "" {
			r.logger.Warn("marginal: per-channel cap reached and no eviction candidate",
				"channel", key.channel)
			return nil
		}
	}

	cfg := DefaultConfig(key.channel, key.signature)
	cfg.PWMUnitMax = r.pwmUnitMax

	// Layer-B prior seeding gate (RULE-CMB-PRIOR-01..02).
	if r.parents != nil && r.signguard.Confirmed(key.channel) {
		parent := r.parents.Shard(key.channel)
		if parent != nil {
			snap := parent.Read()
			if snap.Kind == KindUnidentifiable {
				// RULE-CMB-IDENT-01: defer activation.
				r.lastFailedActivation[key] = now
				return nil
			}
			if !snap.WarmingUp {
				cfg.LayerBPriorBii = snap.BiiAtZero
				cfg.LayerBConfirmed = true
			}
		}
	}

	s, err := New(cfg)
	if err != nil {
		r.logger.Warn("marginal: shard New failed",
			"channel", key.channel,
			"signature", key.signature,
			"err", err)
		return nil
	}

	// Parent-warmup-cleared default true when parents nil; otherwise
	// echoes the parent's WarmingUp.
	if r.parents != nil {
		parent := r.parents.Shard(key.channel)
		if parent != nil {
			snap := parent.Read()
			s.SetParentOutOfWarmup(!snap.WarmingUp)
		}
	} else {
		s.SetParentOutOfWarmup(true)
	}

	// Try to load persisted state. Errors are logged but never
	// fatal (cold-start fallback).
	if r.stateDir != "" {
		if loaded, err := s.Load(r.stateDir, r.hwmonFingerprint, r.logger); err != nil {
			r.logger.Warn("marginal: shard load failed (cold start)",
				"channel", key.channel,
				"signature", key.signature,
				"err", err)
		} else if loaded {
			r.logger.Info("marginal: shard warm-started",
				"channel", key.channel,
				"signature", key.signature)
		}
	}

	r.shards[key] = s
	r.channelCounts[key.channel]++
	delete(r.lastFailedActivation, key)
	return s
}

// evictLRUForChannelLocked removes the LRU shard (lowest n_samples
// here as a proxy for HitCount; v0.7.0+ R29 will refine with τ=14d
// scoring). Returns the evicted signature label or "".
func (r *Runtime) evictLRUForChannelLocked(channel string) string {
	var (
		victim shardKey
		minN   = ^uint64(0)
		found  bool
	)
	for k, s := range r.shards {
		if k.channel != channel {
			continue
		}
		snap := s.Read()
		if snap == nil {
			continue
		}
		if snap.NSamples < minN {
			minN = snap.NSamples
			victim = k
			found = true
		}
	}
	if !found {
		return ""
	}
	delete(r.shards, victim)
	r.channelCounts[channel]--
	return victim.signature
}

func (r *Runtime) oatGate(channelID string) bool {
	for ch, rb := range r.recentPWM {
		if ch == channelID {
			continue
		}
		if !rb.allEqual() {
			return false
		}
	}
	return true
}

func (r *Runtime) bumpRecentPWM(channelID string, pwm uint8) {
	rb, ok := r.recentPWM[channelID]
	if !ok {
		rb = &ringBuffer{}
		r.recentPWM[channelID] = rb
	}
	rb.push(pwm)
}

// Run starts the persistence ticker. Per RULE-CMB-RUNTIME-01, one
// goroutine per active shard inbox is the conceptual model; the
// current implementation routes Update calls directly (synchronous,
// non-blocking) and uses a single periodic-save goroutine. This is
// sufficient for v0.5.8 because Update is pure-CPU < 50µs at d=2;
// per-shard goroutines add complexity without latency benefit.
//
// Returns ctx.Err() when ctx is cancelled. RULE-CMB-WIRING-03 binds
// the outer call site (cmd/ventd/main.go) to "started exactly once."
func (r *Runtime) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.runStarted {
		r.mu.Unlock()
		return errors.New("marginal: Run already called")
	}
	r.runStarted = true
	r.mu.Unlock()

	r.logger.Info("marginal: runtime started",
		"hwmon_fp", r.hwmonFingerprint,
		"state_dir", r.stateDir,
		"persist_every", PersistEvery)

	saveTick := time.NewTicker(PersistEvery)
	defer saveTick.Stop()

	for {
		select {
		case <-ctx.Done():
			r.persistAll()
			r.logger.Info("marginal: runtime stopped")
			return ctx.Err()
		case <-saveTick.C:
			r.persistAll()
		}
	}
}

func (r *Runtime) persistAll() {
	if r.stateDir == "" {
		return
	}
	r.mu.Lock()
	shards := make([]*Shard, 0, len(r.shards))
	for _, s := range r.shards {
		shards = append(shards, s)
	}
	r.mu.Unlock()
	for _, s := range shards {
		if err := s.Save(r.stateDir, r.hwmonFingerprint); err != nil {
			r.logger.Warn("marginal: periodic save failed",
				"channel", s.cfg.ChannelID,
				"signature", s.cfg.SignatureLabel,
				"err", err)
		}
	}
}
