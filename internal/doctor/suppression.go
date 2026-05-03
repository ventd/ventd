package doctor

import (
	"fmt"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/state"
)

// SuppressionEntry records that the operator dismissed a specific
// (detector, entity) Fact, optionally for a bounded time window.
//
// The "forever" semantic is encoded as Until well past any plausible
// wall-clock; the store treats any Until > Now as "still suppressed".
type SuppressionEntry struct {
	// Until is the unix timestamp after which the suppression expires.
	// Zero means "expired immediately" (effectively a no-op).
	Until int64 `yaml:"until_unix"`

	// Reason is a free-form note (operator-provided or "auto: <category>").
	Reason string `yaml:"reason,omitempty"`

	// AcknowledgedAt is when the operator clicked Dismiss / ran
	// `ventd doctor --suppress`. Audit trail for the diagnostic bundle.
	AcknowledgedAt int64 `yaml:"acknowledged_at_unix"`
}

// SuppressionStore is the KV-backed dismissal table. Detectors check
// IsSuppressed before emitting; the web UI mutates entries via
// Suppress / Unsuppress.
//
// Persistence uses spec-16 KV under namespace "doctor/suppression" per
// R13. Each suppression is one KV entry keyed by
// "<detector_name>:<entity_hash>".
type SuppressionStore struct {
	mu  sync.RWMutex
	db  *state.KVDB
	now func() time.Time
}

// NewSuppressionStore constructs a store backed by db. now is injected
// for deterministic tests; pass time.Now in production.
func NewSuppressionStore(db *state.KVDB, now func() time.Time) *SuppressionStore {
	if now == nil {
		now = time.Now
	}
	return &SuppressionStore{db: db, now: now}
}

// suppressionNamespace is the spec-16 KV namespace per R13. Constant
// rather than a method so tests + the rule-binding doc reference the
// same string.
const suppressionNamespace = "doctor/suppression"

// suppressionKey composes the (detector, entity) key. Both halves are
// expected to be filename-safe (snake_case detector names, hex entity
// hashes); we don't sanitise further.
func suppressionKey(detector, entityHash string) string {
	return fmt.Sprintf("%s:%s", detector, entityHash)
}

// IsSuppressed reports whether the given (detector, entity) Fact is
// currently dismissed. A nil store is never-suppressed (lets tests
// pass nil in Deps without setting up KV state).
func (s *SuppressionStore) IsSuppressed(detector, entityHash string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, ok, err := s.db.Get(suppressionNamespace, suppressionKey(detector, entityHash))
	if err != nil || !ok {
		return false
	}
	entry, ok := decodeSuppressionEntry(raw)
	if !ok {
		// Corrupted entry — treat as not-suppressed and let the next
		// Suppress() overwrite. We don't surface the error here because
		// IsSuppressed is on the detector hot path.
		return false
	}
	return s.now().Unix() < entry.Until
}

// Suppress records a dismissal that expires after `for`. Pass a very
// large duration (e.g. 100*365*24*time.Hour) for "forever". reason is
// optional free-form text.
func (s *SuppressionStore) Suppress(detector, entityHash, reason string, dur time.Duration) error {
	if s == nil {
		return fmt.Errorf("doctor: nil suppression store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	entry := SuppressionEntry{
		Until:          now.Add(dur).Unix(),
		Reason:         reason,
		AcknowledgedAt: now.Unix(),
	}
	return s.db.Set(suppressionNamespace, suppressionKey(detector, entityHash), entry)
}

// Unsuppress removes any active dismissal for (detector, entityHash).
// No-op if no entry exists. Returns an error only if the underlying
// KV operation fails.
func (s *SuppressionStore) Unsuppress(detector, entityHash string) error {
	if s == nil {
		return fmt.Errorf("doctor: nil suppression store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Delete(suppressionNamespace, suppressionKey(detector, entityHash))
}

// List returns every suppression entry (active or expired) for diag
// bundles + the web UI's "current dismissals" surface.
func (s *SuppressionStore) List() (map[string]SuppressionEntry, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := s.db.List(suppressionNamespace)
	if err != nil {
		return nil, fmt.Errorf("doctor: list suppressions: %w", err)
	}
	out := make(map[string]SuppressionEntry, len(raw))
	for k, v := range raw {
		entry, ok := decodeSuppressionEntry(v)
		if !ok {
			// Skip corrupted entries rather than failing the whole list.
			continue
		}
		out[k] = entry
	}
	return out, nil
}

// decodeSuppressionEntry unwraps the value KVDB hands us. The store
// serialises via YAML internally and returns the decoded form, which
// can be either a *SuppressionEntry (if Set passed the typed value) or
// a map[string]any (if loaded from disk and the type wasn't preserved).
func decodeSuppressionEntry(v any) (SuppressionEntry, bool) {
	switch t := v.(type) {
	case SuppressionEntry:
		return t, true
	case *SuppressionEntry:
		if t == nil {
			return SuppressionEntry{}, false
		}
		return *t, true
	case map[string]any:
		out := SuppressionEntry{}
		if u, ok := asInt64(t["until_unix"]); ok {
			out.Until = u
		}
		if r, ok := t["reason"].(string); ok {
			out.Reason = r
		}
		if a, ok := asInt64(t["acknowledged_at_unix"]); ok {
			out.AcknowledgedAt = a
		}
		return out, true
	}
	return SuppressionEntry{}, false
}

// asInt64 coerces YAML-decoded numeric values to int64. yaml.v3 may
// hand us int, int64, or uint64 depending on width; normalising here
// keeps the decode path tolerant.
func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case uint64:
		return int64(t), true
	case float64:
		return int64(t), true
	}
	return 0, false
}
