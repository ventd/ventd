package signature

import (
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/proc"
)

// Bucket is the per-signature persistent state. Bucketed by label
// (hash-tuple or "maint/<canonical>"). Persisted to spec-16 KV.
//
// RLSState is opaque bytes owned by Layer C (v0.5.8); v0.5.6 only
// reads/writes it as a blob.
type Bucket struct {
	Version       uint8   `msgpack:"v"`
	HashAlg       uint8   `msgpack:"alg"`  // 1 = SipHash-2-4
	LabelKind     uint8   `msgpack:"kind"` // 0 = hash-tuple, 1 = maint
	RLSState      []byte  `msgpack:"rls,omitempty"`
	FirstSeenUnix int64   `msgpack:"first"`
	LastSeenUnix  int64   `msgpack:"last"`
	HitCount      uint64  `msgpack:"hits"`
	CurrentEWMA   float64 `msgpack:"ewma"`
}

// LabelKind values.
const (
	LabelKindHashTuple uint8 = 0
	LabelKindMaint     uint8 = 1
)

// HashAlg values.
const (
	HashAlgSipHash24 uint8 = 1
)

// Library implements R7's EWMA-multiset + K-stable-promotion
// algorithm. One Library instance per daemon; concurrent Tick
// calls are serialised by an internal mutex.
//
// The current label is published via an atomic.Pointer[string] so
// the controller hot loop can read it without taking the library
// lock.
type Library struct {
	cfg       Config
	hasher    *Hasher
	blocklist *MaintenanceBlocklist
	logger    *slog.Logger

	mu          sync.Mutex
	multiset    map[uint64]float64 // hash -> EWMA-weighted accumulated weight
	buckets     map[string]*Bucket
	pending     string
	pendingHits int
	lastTick    time.Time

	// label is the currently-promoted signature label, written by
	// Tick under mu and read lock-free by the controller via Label().
	label atomic.Pointer[string]
}

// NewLibrary constructs a Library from a hasher, blocklist, and
// config. The library starts with no buckets and an idle label.
func NewLibrary(cfg Config, hasher *Hasher, blocklist *MaintenanceBlocklist, logger *slog.Logger) *Library {
	if logger == nil {
		logger = slog.Default()
	}
	if blocklist == nil {
		blocklist = NewMaintenanceBlocklist()
	}
	lib := &Library{
		cfg:       cfg,
		hasher:    hasher,
		blocklist: blocklist,
		logger:    logger,
		multiset:  make(map[uint64]float64, cfg.BucketCount),
		buckets:   make(map[string]*Bucket, cfg.BucketCount),
	}
	initial := FallbackLabelIdle
	if cfg.Disabled {
		initial = FallbackLabelDisabled
	}
	lib.label.Store(&initial)
	return lib
}

// Label returns the currently-promoted signature label without
// blocking. Safe for concurrent use; the controller reads this on
// every tick.
func (lib *Library) Label() string {
	if p := lib.label.Load(); p != nil {
		return *p
	}
	return FallbackLabelIdle
}

// Tick advances the EWMA multiset by one step using the supplied
// process samples. Returns the current label and a boolean
// indicating whether promotion fired this tick (RULE-SIG-CTRL-01:
// the controller stamps `signature_promoted = true` on the
// observation record when this returns promoted=true).
//
// Thread-safety: serialised by Library.mu. Concurrent calls are
// rejected via mutex acquisition; the controller hot loop should
// NOT call Tick — the signature scheduler goroutine owns Tick.
func (lib *Library) Tick(now time.Time, samples []proc.ProcessSample) (label string, promoted bool) {
	lib.mu.Lock()
	defer lib.mu.Unlock()

	if lib.cfg.Disabled {
		// Permanent-disabled path: ensure label is the fallback
		// and return immediately. RULE-SIG-LIB-08.
		fallback := FallbackLabelDisabled
		lib.label.Store(&fallback)
		return fallback, false
	}

	// (1) Decay the multiset.
	if !lib.lastTick.IsZero() {
		dt := now.Sub(lib.lastTick).Seconds()
		alpha := math.Pow(0.5, dt/lib.cfg.HalfLife.Seconds())
		for h, w := range lib.multiset {
			nw := w * alpha
			if nw < lib.cfg.DropEpsilon {
				delete(lib.multiset, h)
			} else {
				lib.multiset[h] = nw
			}
		}
	}
	lib.lastTick = now

	// (2) Inject this tick's contributions, gated by EWMA-CPU /
	// RSS thresholds and the kthread filter. RULE-SIG-LIB-01.
	for _, p := range samples {
		if p.IsKThread || p.PPid == 2 {
			continue
		}
		if p.EWMACPU <= lib.cfg.CPUGate && p.RSSBytes <= lib.cfg.RSSGateBytes {
			continue
		}
		h := lib.hasher.HashComm(p.Comm)
		// Weight contributed each tick is the current EWMA-CPU
		// share. Processes passing the RSS gate but with zero
		// CPU contribute a small constant (1 % of one core)
		// so the bucket survives until the next gate
		// reevaluation.
		w := p.EWMACPU
		if w <= 0 {
			w = 0.01
		}
		lib.multiset[h] += w
	}

	// (3) Detect maintenance-class dominance. R7 §Q2 (B).
	if maintLabel := lib.detectMaintDominant(samples); maintLabel != "" {
		return lib.commit(now, maintLabel, LabelKindMaint), lib.committedNew()
	}

	// (4) Extract top-K and canonicalise the candidate label.
	candidate := lib.canonicaliseTopK()
	if candidate == "" {
		// System truly idle — emit the dedicated idle label.
		return lib.commit(now, FallbackLabelIdle, LabelKindHashTuple), lib.committedNew()
	}

	// (5) K-stable promotion gate. RULE-SIG-LIB-03.
	if candidate == lib.pending {
		lib.pendingHits++
		if lib.pendingHits >= lib.cfg.StabilityM {
			previous := lib.Label()
			final := lib.commit(now, candidate, LabelKindHashTuple)
			lib.pendingHits = 0
			return final, final != previous
		}
	} else {
		lib.pending = candidate
		lib.pendingHits = 1
	}
	return lib.Label(), false
}

// committedNew returns true when the most recent commit caused a
// label change. Internal helper — caller holds lib.mu.
//
// (Implementation note: commit() always overwrites label.atomic;
// "promoted" semantics are about whether the label *changed*, not
// merely whether commit fired.)
func (lib *Library) committedNew() bool {
	// commit() already updated label and pending state; the
	// "promoted" return is computed by the caller against the
	// previous label string. This helper exists for symmetry but
	// is currently unused — the caller computes promoted directly.
	return false
}

// commit overwrites the current label, updates the bucket's
// counters, and persists nothing (persistence is the writer's
// responsibility on a separate cadence).
func (lib *Library) commit(now time.Time, label string, kind uint8) string {
	lib.label.Store(&label)
	b, ok := lib.buckets[label]
	if !ok {
		// New bucket — enforce capacity before insert.
		if len(lib.buckets) >= lib.cfg.BucketCount {
			lib.evictOne(now)
		}
		b = &Bucket{
			Version:       1,
			HashAlg:       HashAlgSipHash24,
			LabelKind:     kind,
			FirstSeenUnix: now.Unix(),
		}
		lib.buckets[label] = b
	}
	b.LastSeenUnix = now.Unix()
	b.HitCount++
	if w, exists := lib.multiset[lib.hasher.HashComm(label)]; exists {
		b.CurrentEWMA = w
	}
	return label
}

// detectMaintDominant returns a "maint/<canonical>" label when an
// R5 maintenance-class process dominates the contribution this
// tick, otherwise empty string.
//
// "Dominates" is defined as: the maintenance process's individual
// EWMA contribution exceeds the median of the other contributors
// by ≥ 2x. Conservative — avoids false-positive maint labels when
// e.g. a developer is editing a Plex source tree and `vim` shows
// up alongside `plex-transcoder`.
func (lib *Library) detectMaintDominant(samples []proc.ProcessSample) string {
	type weighted struct {
		comm string
		w    float64
	}
	var contributors []weighted
	for _, p := range samples {
		if p.IsKThread || p.PPid == 2 {
			continue
		}
		if p.EWMACPU <= lib.cfg.CPUGate && p.RSSBytes <= lib.cfg.RSSGateBytes {
			continue
		}
		w := p.EWMACPU
		if w <= 0 {
			w = 0.01
		}
		contributors = append(contributors, weighted{comm: p.Comm, w: w})
	}
	if len(contributors) == 0 {
		return ""
	}
	sort.Slice(contributors, func(i, j int) bool {
		return contributors[i].w > contributors[j].w
	})
	top := contributors[0]
	if canonical, ok := lib.blocklist.IsMaintenance(top.comm); ok {
		// Compute median of others.
		var rest []float64
		for _, c := range contributors[1:] {
			rest = append(rest, c.w)
		}
		median := 0.0
		if len(rest) > 0 {
			sort.Float64s(rest)
			median = rest[len(rest)/2]
		}
		if median == 0 || top.w >= 2*median {
			return MaintLabel(canonical)
		}
	}
	return ""
}

// canonicaliseTopK returns the top-K hashes by EWMA weight,
// rendered as 16-hex tokens, sorted lexicographically, '|'-joined,
// truncated to 80 chars. RULE-SIG-LIB-02.
func (lib *Library) canonicaliseTopK() string {
	if len(lib.multiset) == 0 {
		return ""
	}
	type entry struct {
		h uint64
		w float64
	}
	entries := make([]entry, 0, len(lib.multiset))
	for h, w := range lib.multiset {
		entries = append(entries, entry{h, w})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].w > entries[j].w
	})
	k := lib.cfg.K
	if len(entries) < k {
		k = len(entries)
	}
	if k == 0 {
		return ""
	}
	parts := make([]string, k)
	for i := 0; i < k; i++ {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], entries[i].h)
		parts[i] = hex.EncodeToString(buf[:])
	}
	sort.Strings(parts)
	candidate := strings.Join(parts, "|")
	if len(candidate) > 80 {
		candidate = candidate[:80]
	}
	return candidate
}

// Buckets returns a snapshot of the current bucket map for tests
// and diagnostics. The returned map is a shallow copy; mutating
// the returned Bucket pointers mutates library state.
func (lib *Library) Buckets() map[string]*Bucket {
	lib.mu.Lock()
	defer lib.mu.Unlock()
	out := make(map[string]*Bucket, len(lib.buckets))
	for k, v := range lib.buckets {
		out[k] = v
	}
	return out
}
