package web

import (
	"testing"
)

// Path-traversal guard for the /api/diag/download/<name> endpoint. The
// filename component is constrained to the goreleaser-style pattern
// produced by diag.Generate; anything else (../, absolute paths, leading
// dots, suffixes that aren't .tar.gz) MUST NOT match.
func TestBundleNameRe_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	good := []string{
		"ventd-diag-obf_host-2026-05-02T12-34-56Z.tar.gz",
		"ventd-diag-A_b-1.tar.gz",
	}
	bad := []string{
		"../etc/passwd",
		"ventd-diag-../etc/shadow.tar.gz",
		"/etc/shadow",
		"ventd-diag-host.tar.gz/../escape",
		"diag.tar.gz",                 // wrong prefix
		"ventd-diag-host",             // missing extension
		"ventd-diag-host.tar.gz.evil", // wrong suffix
		"",
		"ventd-diag-host\x00.tar.gz", // null byte
	}
	for _, name := range good {
		if !bundleNameRe.MatchString(name) {
			t.Errorf("bundleNameRe rejected legitimate name %q", name)
		}
	}
	for _, name := range bad {
		if bundleNameRe.MatchString(name) {
			t.Errorf("bundleNameRe accepted disallowed name %q", name)
		}
	}
}
