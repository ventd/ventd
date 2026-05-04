package web

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestInstallShEmbedded_SyncedWithScriptsCopy guards the
// scripts/install.sh ↔ internal/web/install.sh.embedded
// duplication: if either copy drifts (e.g. an edit to
// scripts/install.sh wasn't propagated via `make sync-install-sh`),
// this test fails before a stale embed can ship in a release.
//
// Both files are checked into the repo; the test reads the source
// copy via a relative path from the package dir.
func TestInstallShEmbedded_SyncedWithScriptsCopy(t *testing.T) {
	source, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		t.Fatalf("read scripts/install.sh: %v", err)
	}
	if !bytes.Equal(source, installShEmbedded) {
		t.Fatalf("install.sh embed drifted from scripts/install.sh\n"+
			"source bytes=%d, embed bytes=%d\n"+
			"refresh with: cp scripts/install.sh internal/web/install.sh.embedded",
			len(source), len(installShEmbedded))
	}
}

// TestFindInstallScript_EmbedFallback verifies the embed bootstrap
// path: when no on-disk candidate exists, findInstallScript writes
// the embedded bytes to a temp file with mode 0755 and returns its
// path. The temp file's content matches the embedded bytes byte-equal.
func TestFindInstallScript_EmbedFallback(t *testing.T) {
	if len(installShEmbedded) == 0 {
		t.Skip("no embedded install.sh in this build")
	}
	// Swap the candidate list to a single non-existent path so the
	// embed fallback fires. Restore on cleanup.
	prev := updateInstallScriptCandidates
	updateInstallScriptCandidates = []string{"/nonexistent/ventd/install.sh"}
	t.Cleanup(func() { updateInstallScriptCandidates = prev })

	got := findInstallScript()
	if got == "" {
		t.Fatal("findInstallScript() returned empty; expected embed-fallback path")
	}
	t.Cleanup(func() { _ = os.Remove(got) })

	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read embed-fallback temp file: %v", err)
	}
	if !bytes.Equal(body, installShEmbedded) {
		t.Errorf("embed-fallback temp content != installShEmbedded (got %d bytes, want %d)",
			len(body), len(installShEmbedded))
	}
	st, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat embed-fallback temp file: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o755 {
		t.Errorf("embed-fallback mode = %#o, want 0o755", mode)
	}
	if !strings.Contains(got, "ventd-install-") {
		t.Errorf("embed-fallback path %q lacks the ventd-install- prefix", got)
	}
}
