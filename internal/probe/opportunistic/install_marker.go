package opportunistic

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// DefaultFirstInstallMarkerPath is the production location of the
// first-install timestamp file. v0.5.5 writes the file on first
// daemon start if absent. The marker is retained as a forensic
// breadcrumb (operators can correlate first-probe activity with
// install time) even after v0.5.30 dropped the 24h post-install gate
// (RULE-OPP-PROBE-07).
const DefaultFirstInstallMarkerPath = "/var/lib/ventd/.first-install-ts"

// FirstInstallDelay is the minimum age of the first-install marker
// before the scheduler may fire a probe.
//
// v0.5.30: dropped from 24 h to 0. The 24 h gate compressed the
// available excitation window for first-time users — operators
// watched their dashboard for an hour and saw "smart-mode warming
// up" with no actual probes happening, because RULE-OPP-PROBE-07
// refused every tick. The hard idle preconditions (idle gate's
// 600 s durability, no active SSH, no battery, no container, no
// scrub, no blocked process, ≥ 24 h post-resume warmup) remain the
// load-bearing protection against probing during real workload —
// only the post-install delay was redundant on top of those.
//
// Kept as a constant (not removed) so a future operator-tunable
// knob has a slot to hang on. `PastFirstInstallDelay` returns true
// immediately when this is 0; the function isn't dead code, it's a
// reservation for the v0.6+ tunable.
const FirstInstallDelay = 0

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
