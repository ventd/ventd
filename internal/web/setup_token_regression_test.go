package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegression_Issue458_TokenRecoveryTextMatches asserts that the login page
// contains the correct file-path recovery instructions and does NOT contain
// the broken journalctl grep that was the root cause of issue #458.
func TestRegression_Issue458_TokenRecoveryTextMatches(t *testing.T) {
	b, err := fs.ReadFile(uiFS, "ui-old/login.html")
	if err != nil {
		t.Fatalf("read login.html: %v", err)
	}
	html := string(b)

	if !strings.Contains(html, "/run/ventd/setup-token") {
		t.Error("login.html missing /run/ventd/setup-token recovery path")
	}
	if !strings.Contains(html, "/var/lib/ventd/setup-token") {
		t.Error("login.html missing /var/lib/ventd/setup-token recovery path")
	}
	if strings.Contains(html, `grep "Setup token"`) {
		t.Error(`login.html still contains broken 'grep "Setup token"' instruction`)
	}
	if strings.Contains(html, "journalctl -u ventd | grep") {
		t.Error("login.html still references journalctl pipe as primary recovery method")
	}
}

// TestRegression_Issue458_RestartRotatesBothFiles exercises token rotation and
// asserts that WriteSetupTokenFiles overwrites both paths with the new value,
// matching the behaviour on every daemon restart when first-boot is pending.
func TestRegression_Issue458_RestartRotatesBothFiles(t *testing.T) {
	dir := t.TempDir()
	runtime := filepath.Join(dir, "run", "setup-token")
	persist := filepath.Join(dir, "var", "lib", "setup-token")

	const tok1 = "AAAAA-BBBBB-CCCCC"
	if err := WriteSetupTokenFiles(tok1, runtime, persist); err != nil {
		t.Fatalf("first write: %v", err)
	}
	checkTokenFile(t, runtime, tok1)
	checkTokenFile(t, persist, tok1)

	// Simulate restart: a new token must overwrite both files.
	const tok2 = "DDDDD-EEEEE-FFFFF"
	if err := WriteSetupTokenFiles(tok2, runtime, persist); err != nil {
		t.Fatalf("second write (rotation): %v", err)
	}
	checkTokenFile(t, runtime, tok2)
	checkTokenFile(t, persist, tok2)
}

// TestRegression_Issue458_ResetWritesBothFiles exercises the reset-to-initial-setup
// path: after a reset the daemon restarts, generates a fresh token, and must
// overwrite both token files — not leave the pre-reset values behind.
func TestRegression_Issue458_ResetWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	runtime := filepath.Join(dir, "run", "setup-token")
	persist := filepath.Join(dir, "var", "lib", "setup-token")

	// Pre-populate both files as if they existed before the reset.
	for _, p := range []string{runtime, persist} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("OLD-TOKEN\n"), 0o640); err != nil {
			t.Fatalf("write pre-reset token: %v", err)
		}
	}

	const freshTok = "FRESH-TOKEN-AFTER-RESET"
	if err := WriteSetupTokenFiles(freshTok, runtime, persist); err != nil {
		t.Fatalf("WriteSetupTokenFiles after reset: %v", err)
	}
	checkTokenFile(t, runtime, freshTok)
	checkTokenFile(t, persist, freshTok)
}

func checkTokenFile(t *testing.T, path, wantTok string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := strings.TrimSpace(string(b)); got != wantTok {
		t.Errorf("%s: got %q, want %q", path, got, wantTok)
	}
}
