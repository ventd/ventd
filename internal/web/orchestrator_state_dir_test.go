package web

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestWipeOrchestratorStateDir_RemovesPopulatedDir(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "setup")
	orig := orchestratorStateDir
	t.Cleanup(func() { orchestratorStateDir = orig })
	orchestratorStateDir = target

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "calibration.json"), []byte(`{"schema_version":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(target, "phases")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "002-inventory.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := wipeOrchestratorStateDir(slog.Default()); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone after wipe; stat err=%v", err)
	}
}

func TestWipeOrchestratorStateDir_NoErrorOnMissingDir(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "does-not-exist")
	orig := orchestratorStateDir
	t.Cleanup(func() { orchestratorStateDir = orig })
	orchestratorStateDir = target

	if err := wipeOrchestratorStateDir(slog.Default()); err != nil {
		t.Errorf("wipe of missing dir should be no-op, got err: %v", err)
	}
}

func TestWipeOrchestratorStateDir_IdempotentOnRepeatedCalls(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "setup")
	orig := orchestratorStateDir
	t.Cleanup(func() { orchestratorStateDir = orig })
	orchestratorStateDir = target

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := wipeOrchestratorStateDir(slog.Default()); err != nil {
		t.Fatal(err)
	}
	if err := wipeOrchestratorStateDir(slog.Default()); err != nil {
		t.Errorf("second wipe should be no-op, got err: %v", err)
	}
}
