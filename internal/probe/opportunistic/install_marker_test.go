package opportunistic

import (
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureMarker_CreatesIfAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".first-install-ts")
	now := time.Now().Truncate(time.Second)

	got, err := EnsureMarker(path, now)
	if err != nil {
		t.Fatalf("EnsureMarker: %v", err)
	}
	if !got.Equal(now) {
		t.Errorf("returned mtime: got %v, want %v", got, now)
	}

	// Calling again returns the existing mtime, not now.
	got2, err := EnsureMarker(path, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("EnsureMarker repeat: %v", err)
	}
	if !got2.Equal(got) {
		t.Errorf("repeat call returned different mtime: got %v, want %v", got2, got)
	}
}

// TestPastFirstInstallDelay pins the v0.5.30 contract for the
// fresh-install gate (RULE-OPP-PROBE-07): with FirstInstallDelay = 0,
// the function returns true at every non-negative marker age — the
// gate is functionally dropped. The function and its constant are
// kept (not removed) so a future operator-tunable knob has a slot to
// hang on; the test pins both the constant and the behaviour so a
// regression that re-introduces the 24 h delay surfaces against the
// rule rewrite, not a silent revert.
func TestPastFirstInstallDelay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".first-install-ts")
	now := time.Now().Truncate(time.Second)

	if _, err := EnsureMarker(path, now); err != nil {
		t.Fatalf("EnsureMarker: %v", err)
	}

	t.Run("FirstInstallDelay_constant_is_zero", func(t *testing.T) {
		// Pin the constant directly. A regression that bumps it
		// back to 24 h re-introduces the silent-fail UX where
		// fresh-install operators saw "smart-mode warming up"
		// for a day with zero actual probes firing.
		if FirstInstallDelay != 0 {
			t.Errorf("FirstInstallDelay = %v; want 0 (v0.5.30 dropped the 24 h gate)", FirstInstallDelay)
		}
	})

	t.Run("zero_age_marker_returns_past_true", func(t *testing.T) {
		past, err := PastFirstInstallDelay(path, now)
		if err != nil {
			t.Fatalf("PastFirstInstallDelay: %v", err)
		}
		if !past {
			t.Error("PastFirstInstallDelay: got false at age 0; want true (FirstInstallDelay = 0 means gate is satisfied immediately)")
		}
	})

	t.Run("aged_marker_returns_past_true", func(t *testing.T) {
		past, err := PastFirstInstallDelay(path, now.Add(2*time.Hour))
		if err != nil {
			t.Fatalf("PastFirstInstallDelay (aged): %v", err)
		}
		if !past {
			t.Error("PastFirstInstallDelay: got false at age 2h; want true")
		}
	})

	t.Run("empty_path_returns_past_true_unchanged", func(t *testing.T) {
		// Test convenience: empty path treats the gate as
		// already-satisfied. Behaviour predates the constant flip
		// and is unchanged in v0.5.30.
		past, err := PastFirstInstallDelay("", now)
		if err != nil {
			t.Fatalf("PastFirstInstallDelay (empty path): %v", err)
		}
		if !past {
			t.Error("PastFirstInstallDelay (empty path): got false; want true")
		}
	})
}
