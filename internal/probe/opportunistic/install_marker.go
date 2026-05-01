package opportunistic

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// DefaultFirstInstallMarkerPath is the production location of the
// first-install timestamp file. v0.5.5 writes the file on first
// daemon start if absent; the first opportunistic probe may not
// fire until at least 24 hours after the file's mtime
// (RULE-OPP-PROBE-07).
const DefaultFirstInstallMarkerPath = "/var/lib/ventd/.first-install-ts"

// FirstInstallDelay is the minimum age of the first-install marker
// before the scheduler may fire a probe.
const FirstInstallDelay = 24 * time.Hour

// EnsureMarker creates the marker file at path with the current mtime
// if it does not exist. Returns the marker's mtime (existing or just-
// created) and any I/O error. Empty path is a no-op that returns
// time.Now() so unit tests can run without touching the filesystem.
func EnsureMarker(path string, now time.Time) (time.Time, error) {
	if path == "" {
		return now, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err == nil {
		return info.ModTime(), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return time.Time{}, err
	}
	if _, err := f.WriteString(now.UTC().Format(time.RFC3339Nano) + "\n"); err != nil {
		_ = f.Close()
		return time.Time{}, err
	}
	if err := f.Close(); err != nil {
		return time.Time{}, err
	}
	if err := os.Chtimes(path, now, now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// MarkerAge returns now - (marker mtime) for a file at path, or 0 if
// the marker is absent.
func MarkerAge(path string, now time.Time) (time.Duration, error) {
	if path == "" {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	return now.Sub(info.ModTime()), nil
}

// PastFirstInstallDelay returns true when the marker at path has
// existed for at least FirstInstallDelay. Empty path treats the
// delay as already-satisfied (test convenience).
func PastFirstInstallDelay(path string, now time.Time) (bool, error) {
	if path == "" {
		return true, nil
	}
	age, err := MarkerAge(path, now)
	if err != nil {
		return false, err
	}
	return age >= FirstInstallDelay, nil
}
