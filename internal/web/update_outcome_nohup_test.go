package web

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWatchUpdateApplyOutcomeNohup_NonZeroSentinelCapturesOutcome
// covers the #1305 happy-failure path: when the nohup-path subshell
// writes a non-zero exit code to /run/ventd/update.exitcode, the
// watcher must capture it into lastApplyOutcomePtr alongside the
// tail of /var/log/ventd-update.log. Matches the systemd-run path's
// behaviour for OpenRC / runit operators.
func TestWatchUpdateApplyOutcomeNohup_NonZeroSentinelCapturesOutcome(t *testing.T) {
	resetLastApplyOutcomeForTest()
	t.Cleanup(resetLastApplyOutcomeForTest)

	sentinelDir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "ventd-update.log")
	if err := os.WriteFile(logFile, []byte("preflight: starting\nventd preflight: dkms_missing\n"+
		"ERROR: install aborted (rc=42)\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	prevLog := updateNohupLogPath
	prevTimeout := updateOutcomeWatchTimeout
	prevInterval := updateOutcomePollInterval
	updateNohupLogPath = logFile
	updateOutcomeWatchTimeout = 2 * time.Second
	updateOutcomePollInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		updateNohupLogPath = prevLog
		updateOutcomeWatchTimeout = prevTimeout
		updateOutcomePollInterval = prevInterval
	})

	// Stage the sentinel write on a short delay so the watcher's
	// first poll misses, then the second poll picks it up — exercises
	// the loop rather than just the fast-path read.
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(sentinelDir, "update.exitcode"), []byte("42\n"), 0o644)
	}()

	done := make(chan struct{})
	go func() {
		watchNohupSentinelInDir("v0.5.99", "/run/ventd/ventd-install-fetched-X.sh", sentinelDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not return within 3s")
	}

	out := lastApplyOutcomePtr.Load()
	if out == nil {
		t.Fatal("watcher did not capture outcome despite non-zero sentinel")
	}
	if out.Status != "failed" {
		t.Errorf("Status = %q, want %q", out.Status, "failed")
	}
	if out.Version != "v0.5.99" {
		t.Errorf("Version = %q, want %q", out.Version, "v0.5.99")
	}
	if !strings.Contains(out.Detail, "rc=42") {
		t.Errorf("Detail = %q; want substring %q", out.Detail, "rc=42")
	}
	if !strings.Contains(out.JournalTail, "ERROR: install aborted (rc=42)") {
		t.Errorf("JournalTail = %q; want last log line", out.JournalTail)
	}
}

// TestWatchUpdateApplyOutcomeNohup_ZeroSentinelDoesNotStore covers
// the success path: a zero-exit sentinel means install.sh has swapped
// the binary and asked the init system to restart ventd — the new
// daemon's startup is the user-visible success signal, so the watcher
// must NOT store a success outcome.
func TestWatchUpdateApplyOutcomeNohup_ZeroSentinelDoesNotStore(t *testing.T) {
	resetLastApplyOutcomeForTest()
	t.Cleanup(resetLastApplyOutcomeForTest)

	sentinelDir := t.TempDir()
	prevTimeout := updateOutcomeWatchTimeout
	prevInterval := updateOutcomePollInterval
	updateOutcomeWatchTimeout = 500 * time.Millisecond
	updateOutcomePollInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		updateOutcomeWatchTimeout = prevTimeout
		updateOutcomePollInterval = prevInterval
	})

	if err := os.WriteFile(filepath.Join(sentinelDir, "update.exitcode"), []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	watchNohupSentinelInDir("v0.5.99", "/run/ventd/install.sh", sentinelDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if out := lastApplyOutcomePtr.Load(); out != nil {
		t.Errorf("watcher stored an outcome on rc=0: %+v (want nil — success is communicated by daemon restart, not stored)", out)
	}
}

// TestWatchUpdateApplyOutcomeNohup_TimeoutDoesNotStore covers the
// "sentinel never showed up" path. Could mean install.sh wedged
// (timeout(1) should have killed it but didn't), or install.sh
// succeeded and the daemon is winding down before the sentinel
// landed. Either way, no surface-able outcome.
func TestWatchUpdateApplyOutcomeNohup_TimeoutDoesNotStore(t *testing.T) {
	resetLastApplyOutcomeForTest()
	t.Cleanup(resetLastApplyOutcomeForTest)

	sentinelDir := t.TempDir()
	prevTimeout := updateOutcomeWatchTimeout
	prevInterval := updateOutcomePollInterval
	updateOutcomeWatchTimeout = 80 * time.Millisecond
	updateOutcomePollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		updateOutcomeWatchTimeout = prevTimeout
		updateOutcomePollInterval = prevInterval
	})

	watchNohupSentinelInDir("v0.5.99", "/run/ventd/install.sh", sentinelDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if out := lastApplyOutcomePtr.Load(); out != nil {
		t.Errorf("watcher stored an outcome on timeout: %+v (want nil)", out)
	}
}
