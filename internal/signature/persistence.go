package signature

import (
	"errors"
	"fmt"
	"strings"
	"time"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// KVNamespace is the spec-16 KV namespace used by the signature
// library. RULE-SIG-PERSIST-01.
const KVNamespace = "signature"

// PersistedEvictionAge is the default on-disk freshness window for
// signature buckets. A bucket whose LastSeenUnix is older than now
// minus this duration is evicted at daemon start via
// EvictPersistedBefore. 30 days matches the R7 §Q5 weighted-LRU
// time constant (τ=14 days) doubled — a workload that hasn't fired
// in a month is functionally gone, and persisting its bucket
// indefinitely contributes to unbounded state.yaml growth on
// long-running daemons. RULE-SIG-PERSIST-03.
const PersistedEvictionAge = 30 * 24 * time.Hour

// kvStore is the spec-16 KV interface this package consumes.
// Satisfied by *state.KVDB; tests inject a map-backed mock.
type kvStore interface {
	Get(namespace, key string) (any, bool, error)
	Set(namespace, key string, value any) error
	Delete(namespace, key string) error
}

// Save persists every bucket in the library to KV. Caller is
// expected to invoke this from a separate cadence (e.g. once a
// minute) — Tick does NOT save inline.
func (lib *Library) Save(kv kvStore) error {
	lib.mu.Lock()
	snapshot := make(map[string]*Bucket, len(lib.buckets))
	for label, b := range lib.buckets {
		snapshot[label] = b
	}
	lib.mu.Unlock()

	var firstErr error
	for label, b := range snapshot {
		payload, err := msgpack.Marshal(b)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("marshal %q: %w", label, err)
			}
			continue
		}
		if err := kv.Set(KVNamespace, label, payload); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("set %q: %w", label, err)
			}
		}
	}
	return firstErr
}

// Load populates the library from KV. Empty namespace is treated
// as a clean install (returns nil error). RULE-SIG-PERSIST-02.
//
// Note: spec-16 KVDB does not expose a prefix scan, so Load
// requires a list of known labels. Production wiring iterates a
// stored manifest under namespace="signature_manifest", key="labels".
// Tests pass labels directly via LoadLabels.
func (lib *Library) LoadLabels(kv kvStore, labels []string) error {
	lib.mu.Lock()
	defer lib.mu.Unlock()

	for _, label := range labels {
		raw, ok, err := kv.Get(KVNamespace, label)
		if err != nil {
			return fmt.Errorf("get %q: %w", label, err)
		}
		if !ok {
			continue
		}
		payload, ok := raw.([]byte)
		if !ok {
			// Some KV impls return a slice-of-bytes-shaped any;
			// handle that.
			if s, sok := raw.(string); sok {
				payload = []byte(s)
			} else {
				continue
			}
		}
		var b Bucket
		if err := msgpack.Unmarshal(payload, &b); err != nil {
			// Skip corrupt bucket; do not abort startup.
			continue
		}
		lib.buckets[label] = &b
		// Warm-restart EWMA: only the most recently active
		// bucket's CurrentEWMA contributes to the live multiset.
		// Older buckets keep HitCount/LastSeen for LRU but do
		// not pre-populate the multiset (would over-weight stale
		// observations after a long offline window).
	}
	return nil
}

// SaveManifest writes the list of currently-known bucket labels
// to spec-16 KV under namespace "signature_manifest", key
// "labels", as a newline-joined string. Used by daemon-start
// LoadLabels to discover what's persisted.
const manifestNamespace = "signature_manifest"
const manifestKey = "labels"

func (lib *Library) SaveManifest(kv kvStore) error {
	lib.mu.Lock()
	labels := make([]string, 0, len(lib.buckets))
	for label := range lib.buckets {
		labels = append(labels, label)
	}
	lib.mu.Unlock()
	return kv.Set(manifestNamespace, manifestKey, strings.Join(labels, "\n"))
}

// LoadManifest returns the list of bucket labels persisted by the
// most recent SaveManifest call. Empty list on first start.
func LoadManifest(kv kvStore) ([]string, error) {
	raw, ok, err := kv.Get(manifestNamespace, manifestKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return strings.Split(v, "\n"), nil
	case []byte:
		if len(v) == 0 {
			return nil, nil
		}
		return strings.Split(string(v), "\n"), nil
	default:
		return nil, errors.New("manifest: unexpected value type")
	}
}

// EvictPersistedBefore deletes every persisted bucket whose
// LastSeenUnix is strictly older than cutoff. Returns the count of
// rows deleted plus the first error encountered (best-effort
// sweep — per-row failures do not abort the loop). The manifest
// is rewritten after the sweep so the survivor list is canonical;
// a subsequent LoadLabels call restores only the survivors.
//
// Buckets that fail to decode (corrupt msgpack payload) are
// deleted unconditionally — a row we can't read is unrecoverable
// and counts against the on-disk budget the same as a healthy row.
//
// Manifest entries that point at missing KV rows are silently
// dropped from the rewritten manifest (no error surfaced), since
// a dangling pointer is the natural cleanup case for an evict-
// without-manifest-rewrite race.
//
// A nil-ish KV (no manifest, no rows) is a clean no-op returning
// (0, nil). RULE-SIG-PERSIST-03.
func (lib *Library) EvictPersistedBefore(kv kvStore, cutoff time.Time) (int, error) {
	labels, err := LoadManifest(kv)
	if err != nil {
		return 0, fmt.Errorf("manifest: %w", err)
	}
	if len(labels) == 0 {
		return 0, nil
	}
	cutoffUnix := cutoff.Unix()
	survivors := make([]string, 0, len(labels))
	deleted := 0
	var firstErr error
	for _, label := range labels {
		raw, ok, getErr := kv.Get(KVNamespace, label)
		if getErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("get %q: %w", label, getErr)
			}
			// Don't carry forward an unreadable row's label —
			// either it's gone or we'll re-discover it on the
			// next Save.
			continue
		}
		if !ok {
			// Dangling manifest entry; drop silently.
			continue
		}
		payload, _ := raw.([]byte)
		if payload == nil {
			if s, sok := raw.(string); sok {
				payload = []byte(s)
			}
		}
		var b Bucket
		if err := msgpack.Unmarshal(payload, &b); err != nil {
			// Corrupt row — best-effort delete, don't survive.
			if dErr := kv.Delete(KVNamespace, label); dErr != nil && firstErr == nil {
				firstErr = fmt.Errorf("delete corrupt %q: %w", label, dErr)
			}
			deleted++
			continue
		}
		if b.LastSeenUnix < cutoffUnix {
			if dErr := kv.Delete(KVNamespace, label); dErr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("delete stale %q: %w", label, dErr)
				}
				// Keep in survivors so the next sweep retries.
				survivors = append(survivors, label)
				continue
			}
			deleted++
			continue
		}
		survivors = append(survivors, label)
	}
	if deleted > 0 || len(survivors) != len(labels) {
		if err := kv.Set(manifestNamespace, manifestKey,
			strings.Join(survivors, "\n")); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("rewrite manifest: %w", err)
		}
	}
	return deleted, firstErr
}
