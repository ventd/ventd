package web

import (
	"bytes"
	"os"
	"testing"
)

// TestChangelogEmbedded_SyncedWithRepoCopy guards the
// CHANGELOG.md ↔ internal/web/CHANGELOG.md.embedded duplication:
// if either copy drifts (e.g. a CHANGELOG edit wasn't propagated
// via `make sync-changelog`), this test fails before a stale embed
// can ship in a release.
func TestChangelogEmbedded_SyncedWithRepoCopy(t *testing.T) {
	source, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	if !bytes.Equal(source, changelogEmbedded) {
		t.Fatalf("CHANGELOG embed drifted from CHANGELOG.md\n"+
			"source bytes=%d, embed bytes=%d\n"+
			"refresh with: cp CHANGELOG.md internal/web/CHANGELOG.md.embedded",
			len(source), len(changelogEmbedded))
	}
}

// TestLoadChangelog_EmbedFallback verifies the embed bootstrap
// path: when no on-disk candidate exists, loadChangelog falls
// through to the embedded bytes and parses successfully.
func TestLoadChangelog_EmbedFallback(t *testing.T) {
	if len(changelogEmbedded) == 0 {
		t.Skip("no embedded CHANGELOG in this build")
	}
	prevCandidates := releaseNotesCandidates
	releaseNotesCandidates = []string{"/nonexistent/ventd/CHANGELOG.md"}
	t.Cleanup(func() { releaseNotesCandidates = prevCandidates })

	prevSections, prevErr := changelogCacheSections, changelogCacheErr
	changelogCacheMu.Lock()
	changelogCacheSections = nil
	changelogCacheErr = ""
	changelogCacheMu.Unlock()
	t.Cleanup(func() {
		changelogCacheMu.Lock()
		changelogCacheSections, changelogCacheErr = prevSections, prevErr
		changelogCacheMu.Unlock()
	})

	sections, errStr := loadChangelog()
	if errStr != "" {
		t.Fatalf("loadChangelog returned error %q; expected embed-fallback success", errStr)
	}
	if len(sections) == 0 {
		t.Fatal("loadChangelog returned zero sections from embedded CHANGELOG")
	}
}
