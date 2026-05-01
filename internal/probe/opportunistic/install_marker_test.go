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

func TestPastFirstInstallDelay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".first-install-ts")
	now := time.Now().Truncate(time.Second)

	if _, err := EnsureMarker(path, now); err != nil {
		t.Fatalf("EnsureMarker: %v", err)
	}

	past, err := PastFirstInstallDelay(path, now)
	if err != nil {
		t.Fatalf("PastFirstInstallDelay: %v", err)
	}
	if past {
		t.Error("PastFirstInstallDelay: got true at age 0, want false")
	}

	past, err = PastFirstInstallDelay(path, now.Add(FirstInstallDelay+time.Second))
	if err != nil {
		t.Fatalf("PastFirstInstallDelay (aged): %v", err)
	}
	if !past {
		t.Error("PastFirstInstallDelay: got false at age > 24h, want true")
	}
}
