package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/lastfatal"
)

// writeHealthFiles seeds the state dir with an optional last-fatal line and an
// optional live pidfile (using this test process's own PID, which is alive).
func writeHealthFiles(t *testing.T, dir, fatalLine string, running bool) {
	t.Helper()
	if fatalLine != "" {
		if err := os.WriteFile(filepath.Join(dir, lastfatal.FileName), []byte(fatalLine), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if running {
		if err := os.WriteFile(filepath.Join(dir, "ventd.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRunHealth binds RULE-CLI-HEALTH: the health probe reports a recorded
// startup fatal as the headline (exit 1), and otherwise reports liveness from
// the pidfile (exit 0).
func TestRunHealth(t *testing.T) {
	cases := []struct {
		name       string
		fatal      string
		running    bool
		wantExit   int
		wantSubstr string
	}{
		{"clean and not running", "", false, healthExitOK, "not running; no recorded startup fatal"},
		{"clean and running", "", true, healthExitOK, "healthy (running"},
		{"fatal recorded, not running", "2026-06-02T01:02:03Z ventd-v0.9.0: load config: boom\n", false, healthExitFatal, "load config: boom"},
		{"fatal recorded but now running", "2026-06-02T01:02:03Z ventd-v0.9.0: load config: boom\n", true, healthExitFatal, "earlier failed start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeHealthFiles(t, dir, tc.fatal, tc.running)
			var out bytes.Buffer
			got := runHealth(dir, &out)
			if got != tc.wantExit {
				t.Errorf("exit = %d, want %d", got, tc.wantExit)
			}
			if !strings.Contains(out.String(), tc.wantSubstr) {
				t.Errorf("output %q does not contain %q", out.String(), tc.wantSubstr)
			}
		})
	}
}
