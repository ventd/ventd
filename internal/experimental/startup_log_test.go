package experimental_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/experimental"
)

// captureLogs returns a logger that writes JSON to buf and a reset function.
func captureLogs(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestStartupLog_FirstRunEmits binds RULE-EXPERIMENTAL-STARTUP-LOG-ONCE (first-run case):
// when no state file exists, the log is emitted and the state file is created.
func TestStartupLog_FirstRunEmits(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "exp-startup.json")
	flags := experimental.Flags{AMDOverdrive: true}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	logger := captureLogs(&buf)

	experimental.LogActiveFlagsOnce(flags, statePath, logger, func() time.Time { return now })

	if buf.Len() == 0 {
		t.Error("expected INFO log on first run, got nothing")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("state file not created: %v", err)
	}
}

// TestStartupLog_WithinSuppressionWindowSilent: state file exists with a recent
// timestamp (< 24h ago) → no log emitted.
func TestStartupLog_WithinSuppressionWindowSilent(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "exp-startup.json")
	flags := experimental.Flags{AMDOverdrive: true}
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Write state file with timestamp 1h ago (within suppression window).
	lastLog := baseTime.Add(-1 * time.Hour)
	if err := os.WriteFile(statePath, []byte(lastLog.Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := captureLogs(&buf)

	experimental.LogActiveFlagsOnce(flags, statePath, logger, func() time.Time { return baseTime })

	if buf.Len() != 0 {
		t.Errorf("expected silence within suppression window, got log: %s", buf.String())
	}
}

// TestStartupLog_AfterSuppressionWindowEmits: state file exists but timestamp
// is > 24h old → log is emitted and state file is updated.
func TestStartupLog_AfterSuppressionWindowEmits(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "exp-startup.json")
	flags := experimental.Flags{ILO4Unlocked: true}
	baseTime := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)

	// Write state file with timestamp 25h ago (outside suppression window).
	lastLog := baseTime.Add(-25 * time.Hour)
	if err := os.WriteFile(statePath, []byte(lastLog.Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := captureLogs(&buf)

	experimental.LogActiveFlagsOnce(flags, statePath, logger, func() time.Time { return baseTime })

	if buf.Len() == 0 {
		t.Error("expected INFO log after suppression window expired, got nothing")
	}

	// State file must be updated to the new timestamp.
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	updated, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		t.Fatalf("parse updated state file: %v", err)
	}
	if !updated.Equal(baseTime) {
		t.Errorf("state file timestamp = %v, want %v", updated, baseTime)
	}
}

// TestStartupLog_NoActiveFlags: no active flags → no log, no state file.
func TestStartupLog_NoActiveFlags(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "exp-startup.json")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	logger := captureLogs(&buf)

	experimental.LogActiveFlagsOnce(experimental.Flags{}, statePath, logger, func() time.Time { return now })

	if buf.Len() != 0 {
		t.Errorf("expected silence with no active flags, got log: %s", buf.String())
	}
	if _, err := os.Stat(statePath); err == nil {
		t.Error("state file created for zero active flags, want no file")
	}
}
