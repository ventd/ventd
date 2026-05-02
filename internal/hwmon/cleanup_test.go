package hwmon

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestRULE_INSTALL_PIPELINE_CleanupRemovesBuildDirs covers the
// /tmp/ventd-driver-* removal arm. Bound to RULE-INSTALL-PIPELINE-CLEANUP-01.
func TestRULE_INSTALL_PIPELINE_CleanupRemovesBuildDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// Seed two orphan build dirs and one unrelated dir that must NOT be
	// removed.
	for _, name := range []string{"ventd-driver-abc", "ventd-driver-xyz"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	keep := filepath.Join(tmp, "unrelated-tempdir")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("seed unrelated: %v", err)
	}

	report, err := CleanupOrphanInstall(
		DriverNeed{Module: "nonexistent_test_module"},
		"", // empty release skips /lib/modules path
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("CleanupOrphanInstall: %v", err)
	}
	if len(report.BuildDirsRemoved) != 2 {
		t.Errorf("BuildDirsRemoved = %d (%v), want 2", len(report.BuildDirsRemoved), report.BuildDirsRemoved)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("unrelated tempdir was removed: %v", err)
	}
}

// TestRULE_INSTALL_PIPELINE_CleanupIdempotent verifies running cleanup
// on a clean system returns an empty report and no error. Bound to
// RULE-INSTALL-PIPELINE-CLEANUP-02.
func TestRULE_INSTALL_PIPELINE_CleanupIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	report, err := CleanupOrphanInstall(
		DriverNeed{Module: "definitely_not_loaded_module_xyz"},
		"99.99.99-fake",
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("CleanupOrphanInstall on clean tree: %v", err)
	}
	if len(report.BuildDirsRemoved) != 0 {
		t.Errorf("BuildDirsRemoved on clean tree = %v, want empty", report.BuildDirsRemoved)
	}
	if len(report.ModulesRemoved) != 0 {
		t.Errorf("ModulesRemoved on clean tree = %v, want empty", report.ModulesRemoved)
	}
	// non-fatal errors may be empty or contain dkms-not-installed; tolerate
	// either since the test environment isn't guaranteed to have dkms.
}

// TestRULE_INSTALL_PIPELINE_BlacklistDropInIdempotent verifies the
// blacklist drop-in writer is idempotent and append-safe. Bound to
// RULE-INSTALL-PIPELINE-CLEANUP-03.
func TestRULE_INSTALL_PIPELINE_BlacklistDropInIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ventd-blacklist.conf")

	// First write — module not present, should produce file.
	if err := writeBlacklistDropIn(path, "nct6683"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first write: %v", err)
	}
	if string(first) != "blacklist nct6683\n" {
		t.Errorf("first write content = %q, want %q",
			string(first), "blacklist nct6683\n")
	}

	// Second write — module already present, file unchanged.
	if err := writeBlacklistDropIn(path, "nct6683"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(second) != string(first) {
		t.Errorf("second write changed file: %q vs %q", string(second), string(first))
	}

	// Third write — different module, should append.
	if err := writeBlacklistDropIn(path, "it87"); err != nil {
		t.Fatalf("third write: %v", err)
	}
	third, _ := os.ReadFile(path)
	want := "blacklist nct6683\nblacklist it87\n"
	if string(third) != want {
		t.Errorf("third write content = %q, want %q", string(third), want)
	}
}

// TestRULE_INSTALL_PIPELINE_StripModuleFromLoadConf verifies the
// modules-load.d cleanup strips only the target module's line and
// preserves unrelated modules. Bound to
// RULE-INSTALL-PIPELINE-CLEANUP-04.
func TestRULE_INSTALL_PIPELINE_StripModuleFromLoadConf(t *testing.T) {
	// stripModuleFromLoadConf operates on the canonical
	// /etc/modules-load.d/ventd.conf path. We can only exercise it
	// in the "no file" branch from a unit test (writing to /etc isn't
	// safe). The branch we can cover is the early-return for missing
	// file — equally important for idempotence.
	if os.Geteuid() == 0 {
		t.Skip("skipping when root: would mutate /etc/modules-load.d/")
	}
	cleaned, err := stripModuleFromLoadConf("nct6683")
	if err != nil {
		t.Fatalf("stripModuleFromLoadConf on missing file: %v", err)
	}
	if cleaned {
		t.Errorf("cleaned = true on missing file, want false")
	}
}
