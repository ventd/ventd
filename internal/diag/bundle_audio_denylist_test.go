package diag

import (
	"testing"
)

// TestRuleDiagPR2C_11_AudioTempFilesNeverCaptured exercises the v0.5.12
// PR-D additions to the architectural denylist. Raw audio captures from
// `ventd calibrate --acoustic` are deleted by the CLI immediately after
// parsing, but a crashed subprocess could leave a stale `.wav` in `/tmp`.
// The denylist must catch any such file before it could enter a bundle.
//
// Bound: RULE-DIAG-PR2C-11.
func TestRuleDiagPR2C_11_AudioTempFilesNeverCaptured(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Canonical CLI temp paths from cmd/ventd/calibrate_acoustic.go.
		{"/tmp/ventd-acoustic-1234.wav", true},
		{"/tmp/ventd-acoustic-abc-DEF-456.wav", true},
		// The reference-tone capture path some operators may stage manually.
		{"/tmp/ventd-mic-ref.wav", true},
		// Generic .wav / .raw anywhere on the filesystem.
		{"/home/phoenix/test.wav", true},
		{"/var/lib/ventd/calibration/leftover.raw", true},
		// Non-audio paths under /tmp must NOT match the audio prefixes.
		{"/tmp/ventd-state-snapshot.json", false},
		// Existing denylist patterns still match (regression guard).
		{"/etc/shadow", true},
		{"/root/.ssh/id_rsa", true},
	}
	for _, c := range cases {
		got := isDenied(c.path)
		if got != c.want {
			t.Errorf("isDenied(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
