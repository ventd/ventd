package coupling

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// IdentifiabilityCheckEvery is the cadence at which the κ
// detector runs per shard. R10 §10.3 picks K=60 (once per minute
// at 1 Hz controller cadence). The runtime ticker drives this.
const IdentifiabilityCheckEvery = time.Minute

// PersistEvery is the periodic-save cadence per spec-v0_5_7
// §2.1. 1 minute matches both v0.5.6's signature library and the
// identifiability cadence — one tick interval covers both.
const PersistEvery = time.Minute

// Runtime owns the per-channel goroutine pool implementing R10
// §10.5's concurrency model:
//
//   - One estimator goroutine per channel (max 24 on Tier L).
//   - Per-shard mutex for update / snapshot separation.
//   - lock-free Snapshot.Read() via atomic.Pointer.
//
// RULE-CPL-RUNTIME-01: NOT one goroutine per shard.
type Runtime struct {
	stateDir         string
	hwmonFingerprint string
	logger           *slog.Logger

	mu     sync.Mutex
	shards map[string]*Shard

	// runStarted is set true after Start is called; subsequent
	// calls are no-ops. Protects against double-start.
	runStarted bool
}

// NewRuntime constructs an empty runtime. Add shards via
// AddShard; start the goroutine pool with Run.
func NewRuntime(stateDir, hwmonFingerprint string, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		stateDir:         stateDir,
		hwmonFingerprint: hwmonFingerprint,
		logger:           logger,
		shards:           make(map[string]*Shard),
	}
}

// AddShard registers a shard with the runtime. Loads persisted
// state from disk if available; logs the load outcome at info
// level for audit gap #7 (per-subsystem observability).
//
// Returns an error if a shard with the same channel ID already
// exists in the runtime.
func (r *Runtime) AddShard(shard *Shard) error {
	if shard == nil {
		return errors.New("coupling: AddShard: nil shard")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.shards[shard.channelID]; exists {
		return errors.New("coupling: AddShard: channel ID already registered: " + shard.channelID)
	}

	loaded, err := shard.Load(r.stateDir, r.hwmonFingerprint)
	switch {
	case err != nil:
		r.logger.Warn("coupling: shard load failed (cold start)",
			"channel", shard.channelID,
			"err", err)
	case loaded:
		r.logger.Info("coupling: shard warm-started from persisted state",
			"channel", shard.channelID,
			"n_samples", shard.nSamples,
			"lambda", shard.lambda,
			"d_b", shard.d,
			"hwmon_fp", r.hwmonFingerprint)
	default:
		r.logger.Info("coupling: shard cold-start (no persisted state or fingerprint mismatch)",
			"channel", shard.channelID,
			"d_b", shard.d,
			"hwmon_fp", r.hwmonFingerprint)
	}

	r.shards[shard.channelID] = shard
	return nil
}

// Shard returns the shard for the given channel ID, or nil when
// not registered.
func (r *Runtime) Shard(channelID string) *Shard {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shards[channelID]
}

// Run starts the goroutine pool. Blocks until ctx is cancelled.
// Each registered shard gets its own goroutine implementing the
// κ-check + periodic-save loop.
//
// Audit gap #7: emits "coupling: runtime started" at info level
// with the channel count so journalctl shows the layer ran.
func (r *Runtime) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.runStarted {
		r.mu.Unlock()
		return errors.New("coupling: Run already called")
	}
	r.runStarted = true
	shards := make([]*Shard, 0, len(r.shards))
	for _, s := range r.shards {
		shards = append(shards, s)
	}
	r.mu.Unlock()

	if len(shards) == 0 {
		r.logger.Info("coupling: runtime not started (no shards registered)")
		return nil
	}
	r.logger.Info("coupling: runtime started",
		"channels", len(shards),
		"identifiability_every", IdentifiabilityCheckEvery,
		"persist_every", PersistEvery)

	var wg sync.WaitGroup
	for _, shard := range shards {
		wg.Add(1)
		go func(s *Shard) {
			defer wg.Done()
			r.runShardLoop(ctx, s)
		}(shard)
	}
	wg.Wait()
	r.logger.Info("coupling: runtime stopped", "channels", len(shards))
	return ctx.Err()
}

// runShardLoop is the per-shard goroutine. Two timers:
// identifiability check (every minute) and persistence save
// (every minute, offset). Update calls happen externally via
// the caller's tick path; this loop handles the bookkeeping.
func (r *Runtime) runShardLoop(ctx context.Context, s *Shard) {
	identTick := time.NewTicker(IdentifiabilityCheckEvery)
	defer identTick.Stop()
	persistTick := time.NewTicker(PersistEvery)
	defer persistTick.Stop()

	r.logger.Debug("coupling: shard loop started", "channel", s.channelID)
	defer r.logger.Debug("coupling: shard loop stopped", "channel", s.channelID)

	for {
		select {
		case <-ctx.Done():
			// Final save before exit.
			if err := s.Save(r.stateDir, r.hwmonFingerprint); err != nil {
				r.logger.Warn("coupling: shutdown save failed",
					"channel", s.channelID, "err", err)
			}
			return
		case <-identTick.C:
			// Identifiability is computed inline by the
			// caller's window helper; runtime just records
			// the result. Caller passes via SetKind.
			//
			// This tick is currently a no-op stub — the
			// concrete identifiability path lands when
			// PR-B wires window updates from the controller's
			// per-tick observation. Today the shard runs
			// at warmup until n_samples + tr(P) clear; κ
			// classification awaits that wiring.
		case <-persistTick.C:
			if err := s.Save(r.stateDir, r.hwmonFingerprint); err != nil {
				r.logger.Warn("coupling: periodic save failed",
					"channel", s.channelID, "err", err)
				continue
			}
			snap := s.Read()
			if snap != nil {
				r.logger.Debug("coupling: periodic save",
					"channel", s.channelID,
					"kind", snap.Kind,
					"n_samples", snap.NSamples,
					"tr_p", snap.TrP,
					"warming_up", snap.WarmingUp)
			}
		}
	}
}

// SnapshotAll returns a slice of every registered shard's
// current snapshot. Lock-free reads; safe for the v0.5.10
// doctor surface to call.
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
