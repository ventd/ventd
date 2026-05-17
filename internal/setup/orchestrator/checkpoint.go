package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// checkpointSchemaVersion is bumped when State's on-disk format changes
// in a non-additive way. The loader rejects any newer version it doesn't
// understand (forward-compat is not promised); rolling back is safe via
// the orchestrator's normal sanitize path.
const checkpointSchemaVersion = 1

// State is the orchestrator's persisted progress: one Outcome per phase
// name, keyed by Phase.Name(). Written after every phase entry+exit so a
// crash leaves an accurate "last known" snapshot.
type State struct {
	SchemaVersion int                `json:"schema_version"`
	Outcomes      map[string]Outcome `json:"outcomes"`
}

// CheckpointStore reads and writes State to a single JSON file under
// the orchestrator's StateDir. Concurrent Save calls are serialised by
// the embedded mutex — production has exactly one writer (the
// orchestrator goroutine) but the lock guards against a future per-phase
// retry handler racing the main loop.
type CheckpointStore struct {
	mu   sync.Mutex
	path string
}

// NewCheckpointStore creates a store that persists to <stateDir>/state.json.
// Does NOT create stateDir — that's the orchestrator's job (so tests can
// inject a t.TempDir() without an extra MkdirAll round-trip).
func NewCheckpointStore(stateDir string) *CheckpointStore {
	return &CheckpointStore{path: filepath.Join(stateDir, "state.json")}
}

// Path returns the on-disk location of the state file. Exposed so the
// CLI's --print-state and test assertions can read it directly.
func (s *CheckpointStore) Path() string { return s.path }

// Load reads the persisted state. Returns an empty State on first run
// (when the file doesn't exist) so callers don't need to special-case
// the bootstrap path.
//
// A malformed or schema-incompatible file is reported as an error rather
// than silently zero-ing the state — recovering from that case is the
// orchestrator's policy decision (sanitize-then-fresh-start), not the
// store's.
func (s *CheckpointStore) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{
				SchemaVersion: checkpointSchemaVersion,
				Outcomes:      map[string]Outcome{},
			}, nil
		}
		return State{}, fmt.Errorf("checkpoint: read %s: %w", s.path, err)
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, fmt.Errorf("checkpoint: parse %s: %w", s.path, err)
	}
	if st.SchemaVersion == 0 {
		// Initial release with no version field — treat as v1.
		st.SchemaVersion = checkpointSchemaVersion
	}
	if st.SchemaVersion > checkpointSchemaVersion {
		return State{}, fmt.Errorf(
			"checkpoint: schema v%d in %s newer than this binary's v%d",
			st.SchemaVersion, s.path, checkpointSchemaVersion)
	}
	if st.Outcomes == nil {
		st.Outcomes = map[string]Outcome{}
	}
	return st, nil
}

// Save persists state atomically. Writes to <path>.tmp, fsyncs, then renames
// over the target so a crash mid-write never leaves a half-written file that
// the next Load would reject.
func (s *CheckpointStore) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st.SchemaVersion == 0 {
		st.SchemaVersion = checkpointSchemaVersion
	}
	if st.Outcomes == nil {
		st.Outcomes = map[string]Outcome{}
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("checkpoint: mkdir %s: %w", filepath.Dir(s.path), err)
	}

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal: %w", err)
	}

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("checkpoint: open %s: %w", tmp, err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("checkpoint: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("checkpoint: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("checkpoint: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("checkpoint: rename %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}

// Wipe removes the checkpoint file. Used by the sanitize phase and the
// /api/setup/reset handler. Returns nil if the file doesn't exist (idempotent).
func (s *CheckpointStore) Wipe() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checkpoint: remove %s: %w", s.path, err)
	}
	return nil
}
