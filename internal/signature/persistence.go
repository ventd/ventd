package signature

import (
	"errors"
	"fmt"
	"strings"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// KVNamespace is the spec-16 KV namespace used by the signature
// library. RULE-SIG-PERSIST-01.
const KVNamespace = "signature"

// kvStore is the spec-16 KV interface this package consumes.
// Satisfied by *state.KVDB; tests inject a map-backed mock.
type kvStore interface {
	Get(namespace, key string) (any, bool, error)
	Set(namespace, key string, value any) error
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
