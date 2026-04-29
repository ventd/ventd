package polarity

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// StartResult summarises the outcome of ApplyOnStart for one channel.
type StartResult struct {
	Channel    string
	Match      MatchResult
	NeedsProbe bool // true when polarity must be resolved before control
}

// ApplyOnStart applies persisted polarity state to all live channels at daemon
// start (spec §4.2 / RULE-POLARITY-08). Returns whether any channels need a
// fresh probe before control mode can be entered.
func ApplyOnStart(
	db *state.KVDB,
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
	now time.Time,
) (needsProbe bool, results []StartResult, err error) {
	matchMap, err := ApplyPersisted(db, channels, now)
	if err != nil {
		return false, nil, fmt.Errorf("polarity start: %w", err)
	}

	for _, ch := range channels {
		mr := matchMap[ch.PWMPath]
		sr := StartResult{Channel: ch.PWMPath, Match: mr}
		switch mr {
		case MatchApplied:
			logger.Debug("polarity: applied persisted result",
				"channel", ch.PWMPath, "polarity", ch.Polarity)
		case MatchMissing:
			sr.NeedsProbe = true
			needsProbe = true
			logger.Info("polarity: no persisted result; probe required",
				"channel", ch.PWMPath)
		}
		results = append(results, sr)
	}

	// Log orphaned entries (hardware removed).
	for k, mr := range matchMap {
		if mr == MatchOrphan {
			logger.Info("polarity: persisted entry has no live channel; dropping",
				"key", k)
		}
	}
	return needsProbe, results, nil
}

// AllControllable returns true when every channel in results has a resolved
// non-phantom polarity (no channel has NeedsProbe=true).
func AllControllable(results []StartResult) bool {
	for _, r := range results {
		if r.NeedsProbe {
			return false
		}
	}
	return true
}
