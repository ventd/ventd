package polarity

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

const (
	nsCalib       = "calibration"
	keyPolarity   = "polarity"
	schemaVersion = 1
)

// PolarityStore is the serialised form of all channel results in the KV store.
type PolarityStore struct {
	SchemaVersion int             `json:"schema_version"`
	Channels      []ChannelResult `json:"channels"`
}

// Persist writes all channel results to the calibration KV namespace
// (spec §4.1 / RULE-POLARITY-08).
func Persist(db *state.KVDB, results []ChannelResult) error {
	if db == nil {
		return fmt.Errorf("polarity persist: nil KVDB")
	}
	store := PolarityStore{
		SchemaVersion: schemaVersion,
		Channels:      results,
	}
	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("polarity persist: marshal: %w", err)
	}
	return db.WithTransaction(func(tx *state.KVTx) error {
		tx.Set(nsCalib, keyPolarity, string(data))
		return nil
	})
}

// Load reads persisted polarity results from the calibration namespace.
// Returns (nil, nil) when no persisted state exists.
func Load(db *state.KVDB) (*PolarityStore, error) {
	if db == nil {
		return nil, nil
	}
	v, ok, err := db.Get(nsCalib, keyPolarity)
	if err != nil {
		return nil, fmt.Errorf("polarity load: %w", err)
	}
	if !ok {
		return nil, nil
	}
	s, _ := v.(string)
	if s == "" {
		return nil, nil
	}
	var store PolarityStore
	if err := json.Unmarshal([]byte(s), &store); err != nil {
		return nil, fmt.Errorf("polarity load: unmarshal: %w", err)
	}
	return &store, nil
}

// MatchKey builds the unique key for a ChannelResult used in daemon-start
// matching (spec §4.2). Format: "<backend>:<pwm_path_or_pci>:<fan_index>".
func MatchKey(r ChannelResult) string {
	switch r.Backend {
	case "nvml":
		return fmt.Sprintf("nvml:%s:%d", r.Identity.PCIAddress, r.Identity.FanIndex)
	case "ipmi":
		return fmt.Sprintf("ipmi:%s:%s", r.Identity.Vendor, r.Identity.ChannelID)
	default: // hwmon / ec
		return fmt.Sprintf("hwmon:%s", r.Identity.PWMPath)
	}
}

// channelKey builds the same key form from a live ControllableChannel.
func channelKey(ch *probe.ControllableChannel) string {
	switch ch.Driver {
	case "nvml":
		return fmt.Sprintf("nvml::%s", ch.PWMPath) // pci address not in channel yet
	case "ipmi":
		return fmt.Sprintf("ipmi::%s", ch.SourceID)
	default:
		return fmt.Sprintf("hwmon:%s", ch.PWMPath)
	}
}

// MatchResult describes the outcome of ApplyPersisted for one channel.
type MatchResult int

const (
	MatchApplied MatchResult = iota // persisted entry found and applied
	MatchMissing                    // no persisted entry; re-probe needed
	MatchOrphan                     // persisted entry has no matching live channel
)

// ApplyPersisted applies persisted polarity to live channels (spec §4.2 /
// RULE-POLARITY-08). Returns per-channel match results.
func ApplyPersisted(db *state.KVDB, channels []*probe.ControllableChannel, now time.Time) (map[string]MatchResult, error) {
	_ = now
	results := make(map[string]MatchResult)

	store, err := Load(db)
	if err != nil {
		return nil, err
	}
	if store == nil {
		for _, ch := range channels {
			results[ch.PWMPath] = MatchMissing
		}
		return results, nil
	}

	// Index persisted results by match key.
	persisted := make(map[string]ChannelResult, len(store.Channels))
	for _, r := range store.Channels {
		persisted[MatchKey(r)] = r
	}

	// Apply to live channels.
	matched := make(map[string]bool)
	for _, ch := range channels {
		k := channelKey(ch)
		r, ok := persisted[k]
		if !ok {
			results[ch.PWMPath] = MatchMissing
			continue
		}
		ApplyToChannel(ch, r)
		results[ch.PWMPath] = MatchApplied
		matched[k] = true
	}

	// Identify orphans (persisted entries with no live channel).
	for k := range persisted {
		if !matched[k] {
			results["orphan:"+k] = MatchOrphan
		}
	}
	return results, nil
}
