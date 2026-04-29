package envelope

import (
	"encoding/json"
	"fmt"
	"time"

	msgpack "github.com/vmihailenco/msgpack/v5"

	"github.com/ventd/ventd/internal/state"
)

const (
	kvNamespace     = "calibration"
	kvSchemaKey     = "envelope.schema_version"
	kvSchemaVersion = 1
	logStoreName    = "envelope"
)

func channelKVKey(channelID string) string {
	return fmt.Sprintf("envelope.channels.%s", channelID)
}

// PersistChannelKV writes the channel state atomically to the KV store.
func PersistChannelKV(db *state.KVDB, channelID string, kv ChannelKV) error {
	kv.LastUpdate = time.Now()
	data, err := json.Marshal(kv)
	if err != nil {
		return fmt.Errorf("marshal channel kv %s: %w", channelID, err)
	}
	return db.WithTransaction(func(tx *state.KVTx) error {
		tx.Set(kvNamespace, kvSchemaKey, kvSchemaVersion)
		tx.Set(kvNamespace, channelKVKey(channelID), string(data))
		return nil
	})
}

// LoadChannelKV reads the persisted KV for a channel.
// Returns (zero, false) when no entry exists.
func LoadChannelKV(db *state.KVDB, channelID string) (ChannelKV, bool) {
	if db == nil {
		return ChannelKV{}, false
	}
	val, ok, err := db.Get(kvNamespace, channelKVKey(channelID))
	if err != nil || !ok || val == nil {
		return ChannelKV{}, false
	}
	var raw string
	switch v := val.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return ChannelKV{}, false
	}
	var kv ChannelKV
	if err2 := json.Unmarshal([]byte(raw), &kv); err2 != nil {
		return ChannelKV{}, false
	}
	return kv, true
}

// appendStepEvent encodes ev as msgpack and appends it to the LogStore.
func appendStepEvent(db *state.LogDB, ev StepEvent) error {
	payload, err := msgpack.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal step event: %w", err)
	}
	return db.Append(logStoreName, payload)
}
