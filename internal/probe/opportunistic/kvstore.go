package opportunistic

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ventd/ventd/internal/state"
)

// KVLastProbeStore implements LastProbeStore against the spec-16 KV
// substrate. Keys live under the "opportunistic" namespace as
// "last_probe/<channel_id>" with the value formatted as Unix-nano
// integer string.
type KVLastProbeStore struct {
	kv *state.KVDB
}

// NewKVLastProbeStore wraps a state.KVDB.
func NewKVLastProbeStore(kv *state.KVDB) *KVLastProbeStore {
	return &KVLastProbeStore{kv: kv}
}

const (
	kvNamespace        = "opportunistic"
	kvLastProbeKey     = "last_probe/"
	kvLastProbeKeyName = "last_probe"
)

// GetLastProbe reads the most recent successful-or-aborted probe ts
// for the channel. Returns (zeroTime, false) on miss or any error.
func (s *KVLastProbeStore) GetLastProbe(channelID uint16) (time.Time, bool) {
	if s == nil || s.kv == nil {
		return time.Time{}, false
	}
	key := kvLastProbeKey + strconv.FormatUint(uint64(channelID), 10)
	raw, ok, err := s.kv.Get(kvNamespace, key)
	if err != nil || !ok {
		return time.Time{}, false
	}
	switch v := raw.(type) {
	case int64:
		return time.Unix(0, v), true
	case uint64:
		return time.Unix(0, int64(v)), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(0, n), true
	default:
		return time.Time{}, false
	}
}

// SetLastProbe persists ts for the channel. Caller treats failures
// as advisory (the scheduler logs and continues).
func (s *KVLastProbeStore) SetLastProbe(channelID uint16, ts time.Time) error {
	if s == nil || s.kv == nil {
		return errors.New("opportunistic: kv store unavailable")
	}
	key := kvLastProbeKey + strconv.FormatUint(uint64(channelID), 10)
	if err := s.kv.Set(kvNamespace, key, ts.UnixNano()); err != nil {
		return fmt.Errorf("opportunistic: kv set %s: %w", key, err)
	}
	return nil
}
