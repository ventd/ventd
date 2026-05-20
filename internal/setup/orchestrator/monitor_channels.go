// Package orchestrator — read-side accessor for monitor-channel
// classifications produced by ProbePhase and forwarded through
// ApplyPhase (#796). The daemon's web layer joins these visibility
// verdicts to live `fan*_input` readings so the dashboard hides
// mirror / phantom rows by default.
package orchestrator

import (
	"encoding/json"
	"errors"

	"github.com/ventd/ventd/internal/probe"
)

// LoadMonitorChannels reads the last ApplyArtifact (or ProbeArtifact
// if Apply hasn't run yet) from stateDir/state.json and returns the
// MonitorChannels it carries. Returns an empty slice + nil when no
// classifications are persisted yet — callers treat that as "show
// every channel" so first-boot before the wizard runs doesn't
// over-aggressively hide tach readings.
//
// Failure modes:
//   - state.json missing               → ([], nil)        first boot
//   - state.json malformed             → (nil, err)       surface to caller
//   - no apply / probe artifact yet    → ([], nil)        pre-wizard
//   - artifact carries no channels     → ([], nil)        v1.0.0 pre-#796
//
// The function does not cache. The web layer is expected to be the
// only caller and its polling cadence (≥1 s) is slow enough that one
// JSON parse per request is negligible (state.json is < 50 KB on a
// fully-populated host).
func LoadMonitorChannels(stateDir string) ([]probe.MonitorChannel, error) {
	if stateDir == "" {
		return nil, nil
	}
	store := NewCheckpointStore(stateDir)
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	// Prefer ApplyArtifact (most-recent, includes restore-time state)
	// over ProbeArtifact; only fall back when Apply hasn't recorded a
	// success yet — Apply forwards Probe's MonitorChannels unchanged
	// so the two are equivalent once Apply runs.
	if outcome, ok := state.Outcomes[(ApplyPhase{}).Name()]; ok &&
		outcome.Status == StatusSuccess && len(outcome.Artifact) > 0 {
		var art ApplyArtifact
		if err := json.Unmarshal(outcome.Artifact, &art); err != nil {
			return nil, errors.New("monitor_channels: decode apply artifact: " + err.Error())
		}
		return art.MonitorChannels, nil
	}
	if outcome, ok := state.Outcomes[(ProbePhase{}).Name()]; ok &&
		outcome.Status == StatusSuccess && len(outcome.Artifact) > 0 {
		var art ProbeArtifact
		if err := json.Unmarshal(outcome.Artifact, &art); err != nil {
			return nil, errors.New("monitor_channels: decode probe artifact: " + err.Error())
		}
		return art.MonitorChannels, nil
	}
	return nil, nil
}
