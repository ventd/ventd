package web

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRULE_WEB_UPDATE_STAGE_PATH_OUTSIDE_PRIVATETMP pins the staging-
// path contract for writeInstallShBytes — the function that lands the
// install.sh used by the in-UI updater on disk before systemd-run
// spawns the transient ventd-update.service.
//
// The bound rule lives in .claude/rules/web-ui.md.
//
// History: v0.5.26 staged via os.CreateTemp("", ...), which on
// systemd hosts with PrivateTmp=yes (every modern ventd install)
// resolves to a per-unit /tmp namespace. The transient unit spawned
// by systemd-run then cannot see the staged file (it runs in the
// host namespace) and exits 127 / ENOENT — silently from the API
// caller's perspective. Diagnosed end-to-end on Phoenix's MSI Z690-A
// desktop on 2026-05-08; this rule prevents the regression class.
//
// The fix is to stage under /run/ventd by default — host-visible,
// not affected by PrivateTmp, in ventd.service's ReadWritePaths — with
// a fallback to the default tmp dir when /run/ventd is unavailable
// (dev-tree invocation, non-systemd hosts).
func TestRULE_WEB_UPDATE_STAGE_PATH_OUTSIDE_PRIVATETMP(t *testing.T) {
	t.Run("staging_dir_default_is_run_ventd", func(t *testing.T) {
		// The package-level default at process start MUST be the
		// /run/ventd path, not /tmp. A regression that defaults
		// the staging dir back to "" (Go's os.TempDir() resolution)
		// or to "/tmp" reintroduces the silent-fail bug.
		if installStagingDir != "/run/ventd" {
			t.Fatalf("installStagingDir = %q; want %q "+
				"(staging under /tmp breaks PrivateTmp=yes hosts; "+
				"see installStagingDir doc comment)",
				installStagingDir, "/run/ventd")
		}
	})

	t.Run("happy_path_stages_under_configured_dir", func(t *testing.T) {
		// When installStagingDir is writable, writeInstallShBytes
		// MUST stage under it. We point the seam at a fresh temp
		// directory the test owns (so we can run in CI without root
		// access to /run/ventd) and assert the staged file lives
		// under that exact directory — proving the seam is honoured.
		stagingDir := t.TempDir()
		t.Cleanup(swapInstallStagingDir(t, stagingDir))

		path, err := writeInstallShBytes([]byte("#!/bin/sh\nexit 0\n"), "ventd-install-test-*.sh")
		if err != nil {
			t.Fatalf("writeInstallShBytes: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })

		if dir := filepath.Dir(path); dir != stagingDir {
			t.Fatalf("staged path %q has dir %q; want %q "+
				"(staging dir seam not honoured)",
				path, dir, stagingDir)
		}
	})

	t.Run("falls_back_to_default_tmp_when_staging_dir_unwritable", func(t *testing.T) {
		// On dev-tree invocation (no /run/ventd) and on non-systemd
		// hosts, the staging-dir creation will fail. The function
		// MUST NOT bubble that as an error; it must fall through to
		// the default tmp dir so existing tests + non-systemd hosts
		// continue working. This is the deliberate fallback path
		// the doc comment on writeInstallShBytes documents.
		//
		// We force the failure by pointing the seam at a path
		// whose parent component is a regular file — MkdirAll
		// will refuse to create a directory inside a file.
		fileDir := t.TempDir()
		blocker := filepath.Join(fileDir, "blocker")
		if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
			t.Fatalf("seed blocker file: %v", err)
		}
		unwritable := filepath.Join(blocker, "child")
		t.Cleanup(swapInstallStagingDir(t, unwritable))

		path, err := writeInstallShBytes([]byte("#!/bin/sh\nexit 0\n"), "ventd-install-fallback-*.sh")
		if err != nil {
			t.Fatalf("writeInstallShBytes (fallback path): %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })

		// The fallback writes via os.CreateTemp("", ...) which
		// resolves to os.TempDir(). We just assert the file
		// exists and is readable — the path itself is whatever
		// the runtime resolved.
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fallback temp file unreadable: %v", err)
		}
	})

	t.Run("empty_staging_dir_seam_uses_default_tmp", func(t *testing.T) {
		// Setting the seam to "" disables the staging-dir branch
		// entirely and falls through to os.CreateTemp("", ...).
		// This is the production-equivalent escape hatch for an
		// operator who doesn't want /run/ventd staging — used by
		// no shipping code today, but the seam contract is that
		// "" means "default tmp dir, no opinion".
		t.Cleanup(swapInstallStagingDir(t, ""))

		path, err := writeInstallShBytes([]byte("#!/bin/sh\nexit 0\n"), "ventd-install-empty-seam-*.sh")
		if err != nil {
			t.Fatalf("writeInstallShBytes (empty seam): %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })

		// We don't assert the path's prefix — under bazel / CI /
		// custom $TMPDIR the default could be anywhere — only
		// that the file exists and is mode 0755 like the
		// production behaviour.
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("staged file stat: %v", err)
		}
		if got := st.Mode().Perm(); got != 0o755 {
			t.Fatalf("staged file mode = %o; want 0755", got)
		}
	})
}

// swapInstallStagingDir is the seam-swap helper. Pattern matches the
// other package-level seam swaps in update_spawn_test.go.
func swapInstallStagingDir(t *testing.T, want string) func() {
	t.Helper()
	prev := installStagingDir
	installStagingDir = want
	return func() { installStagingDir = prev }
}
