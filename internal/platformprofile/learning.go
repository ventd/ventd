package platformprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LearningStore persists rolling statistics per profile so a future revision
// of the selector can refine thresholds from observed outcomes. v1 just
// accumulates the data — no policy adaptation yet — but the surface is in
// place so the learning loop is a wiring change, not a schema change.
//
// Per [[feedback-ventd-zero-config-smart]]: every active feature should
// observe its own outcomes so it can improve over time. This is that observer
// for the platform-profile controller.
type LearningStore struct {
	path string
	mu   sync.Mutex
	data Snapshot2
}

// Snapshot2 is the on-disk shape. Keeping the type small and explicit so
// hand-edits during debugging stay tractable.
type Snapshot2 struct {
	SchemaVersion int                          `json:"schema_version"`
	UpdatedAt     time.Time                    `json:"updated_at"`
	HwIdentity    string                       `json:"hw_identity,omitempty"` // DMI fingerprint at write time
	Profiles      map[string]*ProfileOutcome   `json:"profiles"`
	Transitions   []Transition                 `json:"recent_transitions,omitempty"` // capped at 64
}

// ProfileOutcome aggregates ventd's experience operating under one profile.
type ProfileOutcome struct {
	Samples       int     `json:"samples"`
	MeanTempC     float64 `json:"mean_temp_c"`
	MeanRPM       float64 `json:"mean_rpm"`
	MeanLoadPct   float64 `json:"mean_load_pct"`
	MeanPressure  float64 `json:"mean_pressure"`
	OverbudgetN   int     `json:"overbudget_n"` // ticks where temp > 0.9 * TJmax during this profile
	LastObservedAt time.Time `json:"last_observed_at"`
}

// Transition records a profile change so we can post-hoc analyse whether
// switching helped (e.g. did temp converge after we moved up to performance?).
type Transition struct {
	At            time.Time `json:"at"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	PressureScore float64   `json:"pressure_score"`
	Reason        string    `json:"reason"`
}

// NewLearningStore opens or creates the store at path. Missing file is
// fine — first run starts empty.
func NewLearningStore(path string) *LearningStore {
	ls := &LearningStore{
		path: path,
		data: Snapshot2{
			SchemaVersion: 1,
			Profiles:      map[string]*ProfileOutcome{},
		},
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &ls.data) // best-effort; on corruption start fresh
		if ls.data.Profiles == nil {
			ls.data.Profiles = map[string]*ProfileOutcome{}
		}
	}
	return ls
}

// Observe records one tick's inputs alongside the active profile. Called by
// the controller after every poll. Updates running means via Welford-style
// incremental averaging so memory stays bounded.
func (ls *LearningStore) Observe(profile string, in Inputs, pressure float64, tjmaxC int) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	po := ls.data.Profiles[profile]
	if po == nil {
		po = &ProfileOutcome{}
		ls.data.Profiles[profile] = po
	}
	po.Samples++
	n := float64(po.Samples)
	po.MeanTempC += (in.CurrentTempC - po.MeanTempC) / n
	po.MeanRPM += (float64(in.CurrentRPM) - po.MeanRPM) / n
	po.MeanLoadPct += (in.CPULoadPct - po.MeanLoadPct) / n
	po.MeanPressure += (pressure - po.MeanPressure) / n
	if tjmaxC > 0 && in.CurrentTempC > 0.9*float64(tjmaxC) {
		po.OverbudgetN++
	}
	po.LastObservedAt = time.Now()
}

// RecordTransition adds a profile-change entry. Bounded ring buffer of 64.
func (ls *LearningStore) RecordTransition(from, to string, pressure float64, reason string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.data.Transitions = append(ls.data.Transitions, Transition{
		At: time.Now(), From: from, To: to, PressureScore: pressure, Reason: reason,
	})
	if len(ls.data.Transitions) > 64 {
		ls.data.Transitions = ls.data.Transitions[len(ls.data.Transitions)-64:]
	}
}

// Persist atomically writes the current store to disk.
func (ls *LearningStore) Persist() error {
	ls.mu.Lock()
	ls.data.UpdatedAt = time.Now()
	body, err := json.MarshalIndent(ls.data, "", "  ")
	ls.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ls.path), 0o755); err != nil {
		return err
	}
	tmp := ls.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ls.path)
}

// SetHwIdentity stamps the DMI fingerprint on the store so a future
// hardware migration can detect "the learned data is for a different box"
// and start fresh.
func (ls *LearningStore) SetHwIdentity(id string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.data.HwIdentity = id
}

// Outcomes returns a deep-copy of the current per-profile statistics for
// the API + dashboard.
func (ls *LearningStore) Outcomes() map[string]ProfileOutcome {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	out := make(map[string]ProfileOutcome, len(ls.data.Profiles))
	for k, v := range ls.data.Profiles {
		out[k] = *v
	}
	return out
}
