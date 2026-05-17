package calibrate

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyPath_NoLegacy_NoOp(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Errorf("migrate on empty state should be a no-op, got err: %v", err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("newPath should not exist after no-op migration; err=%v", err)
	}
}

func TestMigrateLegacyPath_NewAlreadyExists_NoCopy(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(`{"schema_version":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"schema_version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Errorf("migrate when newPath exists should be a no-op, got err: %v", err)
	}
	body, _ := os.ReadFile(newPath)
	if string(body) != `{"schema_version":3}` {
		t.Errorf("newPath was overwritten despite already existing; got %q", string(body))
	}
}

func TestMigrateLegacyPath_CopiesLegacyAndWritesTombstone(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyBody := `{"schema_version":2,"results":{"/sys/x":{"pwm_path":"/sys/x","start_pwm":50,"max_rpm":1500,"sweep_mode":"pwm"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	gotBody, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("expected newPath at %s: %v", newPath, err)
	}
	if string(gotBody) != legacyBody {
		t.Errorf("copy content mismatch:\n  got: %s\n want: %s", gotBody, legacyBody)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("legacy file should remain in place for one release cycle, got err=%v", err)
	}
	tombstone := legacyPath + ".moved-to-var-lib"
	if _, err := os.Stat(tombstone); err != nil {
		t.Errorf("tombstone should be created at %s, got err=%v", tombstone, err)
	}
}

func TestMigrateLegacyPath_IdempotentOnRepeatedCalls(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First call copies.
	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Fatal(err)
	}
	// Mutate newPath to prove the second call doesn't overwrite.
	if err := os.WriteFile(newPath, []byte("user-edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(newPath)
	if string(got) != "user-edited" {
		t.Errorf("idempotent migration overwrote newPath; got %q", got)
	}
}

func TestMigrateLegacyPath_LegacyDir_SkippedNotError(t *testing.T) {
	// Defensive: if the legacy path is somehow a directory (operator
	// custom layout, recovery from a botched manual mv), don't error
	// or delete it. Just skip.
	dir := t.TempDir()
	newPath := filepath.Join(dir, "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := os.MkdirAll(legacyPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Errorf("legacy-as-dir should skip silently, got err: %v", err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("newPath should not exist; err=%v", err)
	}
}

func TestMigrateLegacyPath_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "deeply", "nested", "var", "calibration.json")
	legacyPath := filepath.Join(dir, "etc", "calibration.json")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyPath(newPath, legacyPath, slog.Default()); err != nil {
		t.Fatalf("migrate should create nested parent dirs: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("newPath at nested location missing: %v", err)
	}
}
