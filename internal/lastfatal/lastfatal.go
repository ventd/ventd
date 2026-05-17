// Package lastfatal surfaces a one-line summary of the most recent
// startup-fatal exit so the next successful start (or an external
// observer) can name what went wrong.
//
// The pre-fix behaviour: ventd hit a fatal during startup, logged
// it to the journal, and systemd restarted the unit. Same error →
// same restart → after 5 restarts in 10 s systemd hits "Start request
// repeated too quickly". The journal is the source of truth, but
// no surface ever shows the operator who didn't think to run
// `journalctl -u ventd` what's wrong. The web UI is unreachable
// (the daemon never bound), so the box looks dead.
//
// Write is called from main()'s fatal-exit path before os.Exit(1).
// Clear is called from run() once startup has progressed past the
// most common fatal modes (config load, state.Open). The next
// successful boot leaves no file behind; only an unrecovered
// fatal persists.
//
// Issue #1165.
package lastfatal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultDir is the production state directory under which the
// last-fatal sentinel lives. Mirrors state.DefaultDir so the file
// lands alongside state.yaml; deliberately duplicated here rather
// than imported to keep this leaf package dependency-free.
const DefaultDir = "/var/lib/ventd"

// FileName is the canonical filename written under DefaultDir.
const FileName = "last-fatal.txt"

// Write best-effort persists a single-line summary of err to
// dir/FileName. Caller is expected to pass DefaultDir in production;
// tests pass a t.TempDir. version is the build version string from
// main (e.g. "0.7.4"); reason is the wrapped error returned by run().
//
// Errors from the write itself are swallowed — we are already on
// the fatal-exit path; surfacing a second error would just race the
// existing logger.Error call in main(). The journal remains the
// authoritative diagnostic; this file is a convenience surface.
//
// The format is one line, UTC ISO-8601, version, error text:
//
//	2026-05-17T16:04:10Z ventd-v0.7.3: load config /etc/ventd/config.yaml: ...
//
// MkdirAll handles the early-fatal case where /var/lib/ventd does
// not yet exist (state.Open hadn't run).
func Write(dir, version string, reason error) {
	if reason == nil || dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	line := fmt.Sprintf("%s ventd-v%s: %s\n",
		time.Now().UTC().Format(time.RFC3339),
		version,
		reason.Error())
	// 0o640 matches state.fileMode; readable by the ventd group
	// (operators in the group can `cat` it without sudo) but not
	// world-readable.
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte(line), 0o640)
}

// Clear removes the last-fatal sentinel. Called from run() once
// startup has progressed past the modes Write covers — by then any
// prior fatal is no longer relevant and the file would lie if it
// survived.
//
// Errors are swallowed: ENOENT is the common case (no prior fatal
// to clear), and any other failure is non-actionable from startup.
func Clear(dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, FileName))
}

// Read returns the contents of the last-fatal file, or empty string
// when no fatal is recorded. Exposed for future surfaces (the issue
// text mentions a `ventd --health` subcommand and an /api/health
// surface) so callers can quote the line back to the operator
// without re-implementing the path/format.
//
// Read errors collapse to empty string by design — every consumer
// would treat "file missing" and "file unreadable" the same.
func Read(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return ""
	}
	return string(data)
}
