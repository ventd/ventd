package hwmon

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// recordCapture is a slog.Handler that appends every incoming Record to a
// slice. Tests assert on Record fields directly (Level, Message, attrs)
// rather than on formatted text — PR #13 flagged string-matching on slog
// output as fragile.
type recordCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordCapture) WithGroup(string) slog.Handler      { return h }

func (h *recordCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

func findRecord(records []slog.Record, message string) (slog.Record, bool) {
	for _, r := range records {
		if r.Message == message {
			return r, true
		}
	}
	return slog.Record{}, false
}

func attrValue(r slog.Record, key string) (slog.Value, bool) {
	var (
		found bool
		v     slog.Value
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			v = a.Value
			return false
		}
		return true
	})
	return v, found
}

func TestLogPersistFailure(t *testing.T) {
	// Reset the package-level gate between test cases so subtests are
	// independent; run serially, not t.Parallel, because the gate is global.
	resetGate := func() {
		persistWarned.Range(func(k, _ any) bool {
			persistWarned.Delete(k)
			return true
		})
	}

	const persistMsg = "could not persist hwmon module"
	errFS := errors.New("read-only file system")

	t.Run("first call per module emits INFO", func(t *testing.T) {
		resetGate()
		h := &recordCapture{}
		logger := slog.New(h)

		logPersistFailure(logger, "nct6687d", errFS)

		records := h.snapshot()
		r, ok := findRecord(records, persistMsg)
		if !ok {
			t.Fatalf("no record with message %q captured; got %d records", persistMsg, len(records))
		}
		if r.Level != slog.LevelInfo {
			t.Errorf("first call level: got %v, want INFO", r.Level)
		}
		mod, ok := attrValue(r, "module")
		if !ok || mod.String() != "nct6687d" {
			t.Errorf("first call module attr: got %v ok=%v, want nct6687d", mod, ok)
		}
		errAttr, ok := attrValue(r, "err")
		if !ok {
			t.Fatalf("first call missing err attr")
		}
		if !strings.Contains(errAttr.String(), "read-only file system") {
			t.Errorf("first call err attr: got %q, want it to contain the reason", errAttr.String())
		}
	})

	t.Run("second call same module emits DEBUG", func(t *testing.T) {
		resetGate()
		h := &recordCapture{}
		logger := slog.New(h)

		logPersistFailure(logger, "nct6687d", errFS) // warm the gate
		logPersistFailure(logger, "nct6687d", errFS) // under test

		records := h.snapshot()
		if len(records) < 2 {
			t.Fatalf("want 2 records, got %d", len(records))
		}
		second := records[1]
		if second.Message != persistMsg {
			t.Fatalf("second record message: got %q, want %q", second.Message, persistMsg)
		}
		if second.Level != slog.LevelDebug {
			t.Errorf("second call level: got %v, want DEBUG", second.Level)
		}
	})

	t.Run("different module name resets the gate", func(t *testing.T) {
		resetGate()
		h := &recordCapture{}
		logger := slog.New(h)

		logPersistFailure(logger, "nct6687d", errFS) // first for nct6687d
		logPersistFailure(logger, "it87", errFS)     // first for it87 — INFO

		records := h.snapshot()
		if len(records) < 2 {
			t.Fatalf("want 2 records, got %d", len(records))
		}
		for i, want := range []string{"nct6687d", "it87"} {
			if records[i].Level != slog.LevelInfo {
				t.Errorf("record %d level: got %v, want INFO", i, records[i].Level)
			}
			mod, ok := attrValue(records[i], "module")
			if !ok || mod.String() != want {
				t.Errorf("record %d module attr: got %v ok=%v, want %q", i, mod, ok, want)
			}
		}
	})
}
