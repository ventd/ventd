package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"testing"
)

// captureHandler is a minimal slog.Handler for tests that need to
// assert on log Level + Message without parsing serialised output.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// TestLogConfigReloadFailure_MissingFileIsInfo pins #1164: a config
// removed by the wizard-reset flow must log at INFO with explicit
// "wizard reset?" framing, not WARN — so factory-reset doesn't show
// up in journald as a fault.
func TestLogConfigReloadFailure_MissingFileIsInfo(t *testing.T) {
	h := &captureHandler{}
	logger := slog.New(h)

	// Shape matches what config.Load returns when ReadFile hits ENOENT:
	// fmt.Errorf("read config %s: %w", path, err) where err wraps fs.ErrNotExist.
	logConfigReloadFailure(logger, fmt.Errorf("read config %s: %w", "/etc/ventd/config.yaml", fs.ErrNotExist))

	recs := h.snapshot()
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if recs[0].Level != slog.LevelInfo {
		t.Errorf("level: got %v, want INFO", recs[0].Level)
	}
	if recs[0].Message != "config removed (wizard reset?); daemon continues with last loaded config until restart" {
		t.Errorf("message: %q", recs[0].Message)
	}
}

// TestLogConfigReloadFailure_OtherErrorsStayWarn pins the other half:
// every non-ENOENT failure (malformed YAML, validation, disk full,
// permission denied) keeps the WARN level + original wording so the
// downgrade doesn't mask real faults.
func TestLogConfigReloadFailure_OtherErrorsStayWarn(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"malformed yaml", errors.New("yaml: line 12: did not find expected key")},
		{"validation", errors.New("config: fan \"cpu\" references missing curve")},
		{"permission denied", fmt.Errorf("read config: %w", fs.ErrPermission)},
		{"disk full", errors.New("write tmp config: no space left on device")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &captureHandler{}
			logger := slog.New(h)

			logConfigReloadFailure(logger, tc.err)

			recs := h.snapshot()
			if len(recs) != 1 {
				t.Fatalf("want 1 record, got %d", len(recs))
			}
			if recs[0].Level != slog.LevelWarn {
				t.Errorf("level: got %v, want WARN", recs[0].Level)
			}
			if recs[0].Message != "config reload failed; keeping current config" {
				t.Errorf("message: %q", recs[0].Message)
			}
		})
	}
}
