package main

import (
	"log/slog"
	"strings"

	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/watchdog"
)

// kvdbLastKnownStore is the production watchdog.LastKnownStore that
// persists the pre-daemon pwm_enable values inside state.KVDB. Wraps
// the KVDB read/write surface in a narrow shape so the watchdog
// package stays free of state imports (RULE-WD-PRIOR-CRASH-FALLBACK).
//
// Issue #1331: the persisted key is now keyed by a stable identity
// (chip name + bus address + pwmN index) rather than the volatile
// /sys/class/hwmon/hwmonN path so the value survives rmmod+modprobe
// cycles. A migration shim looks up the legacy pwmPath key on a fresh
// read and SetPreDaemonEnable deletes the legacy entry once a new
// write lands.
type kvdbLastKnownStore struct {
	kv     *state.KVDB
	logger *slog.Logger
}

// kvdbLastKnownNamespace is the state.KVDB namespace the watchdog's
// pre-daemon pwm_enable values live under. Lifted into a constant so
// future migrations can grep for the namespace cleanly.
const kvdbLastKnownNamespace = "watchdog"

// newKVDBLastKnownStore returns the production watchdog.LastKnownStore.
// Always non-nil — a nil-store deployment uses watchdog.New directly.
func newKVDBLastKnownStore(kv *state.KVDB, logger *slog.Logger) *kvdbLastKnownStore {
	return &kvdbLastKnownStore{kv: kv, logger: logger}
}

// keyFor returns the KVDB key (under namespace "watchdog") that
// corresponds to a watchdog.ChannelIdentity. The watchdog package
// formats the full key as "watchdog.<...>.preDaemonEnable"; this
// wrapper splits the namespace ("watchdog") and the per-identity tail
// (everything after the first dot) so the value lands in the namespaced
// KVDB row the rest of the daemon uses.
func keyFor(full string) string {
	if strings.HasPrefix(full, kvdbLastKnownNamespace+".") {
		return full[len(kvdbLastKnownNamespace)+1:]
	}
	return full
}

func (s *kvdbLastKnownStore) GetPreDaemonEnable(id watchdog.ChannelIdentity) (int, bool) {
	if v, ok := s.lookup(keyFor(id.Key())); ok {
		return v, true
	}
	// Migration shim: pre-#1331 entries are keyed by LegacyKey().
	// Honour them on the first read so an upgrade doesn't lose any
	// stored pre-daemon value. The next Set under the new identity
	// will delete the legacy entry.
	legacyKey := keyFor(id.LegacyKey())
	if legacyKey == keyFor(id.Key()) {
		return 0, false
	}
	if v, ok := s.lookup(legacyKey); ok {
		s.logger.Info("watchdog store: migrated legacy pre-daemon entry",
			"legacy_key", legacyKey, "stable_key", keyFor(id.Key()), "value", v)
		return v, true
	}
	return 0, false
}

func (s *kvdbLastKnownStore) SetPreDaemonEnable(id watchdog.ChannelIdentity, value int) error {
	stableKey := keyFor(id.Key())
	if err := s.kv.Set(kvdbLastKnownNamespace, stableKey, value); err != nil {
		return err
	}
	legacyKey := keyFor(id.LegacyKey())
	if legacyKey != stableKey {
		if _, ok, _ := s.kv.Get(kvdbLastKnownNamespace, legacyKey); ok {
			if err := s.kv.Delete(kvdbLastKnownNamespace, legacyKey); err != nil {
				s.logger.Warn("watchdog store: legacy entry delete failed (continuing)",
					"legacy_key", legacyKey, "err", err)
			}
		}
	}
	return nil
}

// lookup reads a key under namespace "watchdog" and decodes it as an
// integer. Returns (0, false) on miss or non-integer payload — the
// non-integer case is the only way a corrupted state file could
// surface here and the safest reaction is to fall through to the
// fallback sequence rather than panic.
func (s *kvdbLastKnownStore) lookup(key string) (int, bool) {
	raw, ok, err := s.kv.Get(kvdbLastKnownNamespace, key)
	if err != nil || !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		// YAML unmarshalling routes integers through float64 in some
		// shapes; round-trip safely as long as the value is a whole
		// number in int range.
		i := int(v)
		if float64(i) == v {
			return i, true
		}
	}
	s.logger.Warn("watchdog store: non-integer payload, treating as missing",
		"key", key, "type", typeName(raw))
	return 0, false
}

func typeName(v any) string {
	switch v.(type) {
	case int:
		return "int"
	case int64:
		return "int64"
	case float64:
		return "float64"
	case string:
		return "string"
	default:
		return "unknown"
	}
}
