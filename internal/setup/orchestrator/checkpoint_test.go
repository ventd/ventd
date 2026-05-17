package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/recovery"
)

func TestCheckpointStore_LoadEmptyOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	st, err := store.Load()
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if st.SchemaVersion != checkpointSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", st.SchemaVersion, checkpointSchemaVersion)
	}
	if st.Outcomes == nil {
		t.Error("Outcomes map should be initialised, got nil")
	}
	if len(st.Outcomes) != 0 {
		t.Errorf("Outcomes len = %d on fresh state, want 0", len(st.Outcomes))
	}
}

func TestCheckpointStore_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	original := State{
		SchemaVersion: checkpointSchemaVersion,
		Outcomes: map[string]Outcome{
			"inventory": {
				Phase:      "inventory",
				Status:     StatusSuccess,
				StartedAt:  time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
				FinishedAt: time.Date(2026, 5, 17, 10, 0, 1, 0, time.UTC),
				Artifact:   json.RawMessage(`{"board_vendor":"MSI"}`),
			},
			"driver_install": {
				Phase:  "driver_install",
				Status: StatusFailed,
				Class:  recovery.ClassMissingHeaders,
				Detail: "kernel headers not found",
			},
		},
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", loaded.SchemaVersion, original.SchemaVersion)
	}
	if len(loaded.Outcomes) != 2 {
		t.Fatalf("Outcomes len = %d, want 2", len(loaded.Outcomes))
	}
	if got := loaded.Outcomes["inventory"].Status; got != StatusSuccess {
		t.Errorf("inventory.Status = %q, want %q", got, StatusSuccess)
	}
	if got := loaded.Outcomes["driver_install"].Class; got != recovery.ClassMissingHeaders {
		t.Errorf("driver_install.Class = %q, want %q", got, recovery.ClassMissingHeaders)
	}
}

func TestCheckpointStore_SaveAtomicViaTmpRename(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	// Write a known state, then check that no .tmp file is left behind.
	if err := store.Save(State{Outcomes: map[string]Outcome{"x": {Phase: "x", Status: StatusSuccess}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(store.Path() + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist after successful Save, stat err=%v", err)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Errorf("state.json should exist after Save: %v", err)
	}
}

func TestCheckpointStore_LoadRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)
	future := State{SchemaVersion: checkpointSchemaVersion + 1, Outcomes: map[string]Outcome{}}
	b, _ := json.Marshal(future)
	if err := os.WriteFile(store.Path(), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(); err == nil {
		t.Error("Load should reject newer schema version, got nil error")
	}
}

func TestCheckpointStore_LoadRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Error("Load should reject malformed JSON, got nil error")
	}
}

func TestCheckpointStore_WipeIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	// Wipe on non-existent file → no error.
	if err := store.Wipe(); err != nil {
		t.Errorf("Wipe on missing file: %v", err)
	}

	// Write, then wipe → file gone.
	if err := store.Save(State{Outcomes: map[string]Outcome{}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Wipe(); err != nil {
		t.Fatalf("Wipe after Save: %v", err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Errorf("state.json should be gone after Wipe, stat err=%v", err)
	}
}

func TestCheckpointStore_LoadHandlesVersionZeroAsV1(t *testing.T) {
	// Initial release wrote no version field; loader must treat
	// "missing schema_version" as v1 for forward compatibility.
	dir := t.TempDir()
	store := NewCheckpointStore(dir)
	raw := []byte(`{"outcomes":{}}`)
	if err := os.WriteFile(store.Path(), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.SchemaVersion != checkpointSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d (v0 should default to v1)",
			st.SchemaVersion, checkpointSchemaVersion)
	}
}

func TestCheckpointStore_CreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "subdir")
	store := NewCheckpointStore(dir)
	if err := store.Save(State{Outcomes: map[string]Outcome{}}); err != nil {
		t.Fatalf("Save should create parent dir: %v", err)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Errorf("state.json should exist in created parent: %v", err)
	}
}
