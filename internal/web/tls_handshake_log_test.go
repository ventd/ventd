package web

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type tlsRecordCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *tlsRecordCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *tlsRecordCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *tlsRecordCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *tlsRecordCapture) WithGroup(string) slog.Handler      { return h }
func (h *tlsRecordCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

func findTLSRecord(records []slog.Record, substr string) (slog.Record, bool) {
	for _, r := range records {
		if strings.Contains(r.Message, substr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// TestTLSHandshakeWatcher_UnknownCertThreshold pins #1169: three
// unknown-certificate handshakes from the same client IP within the
// 30s window must trip exactly one WARN, with the client IP attached
// and the "browser-cached old cert" framing in the message.
func TestTLSHandshakeWatcher_UnknownCertThreshold(t *testing.T) {
	h := &tlsRecordCapture{}
	w := newTLSHandshakeWatcher(slog.New(h))
	base := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return base }

	line := "2026/05/17 12:00:00 http: TLS handshake error from 192.0.2.55:54321: remote error: tls: unknown certificate\n"
	for i := 0; i < 3; i++ {
		n, err := w.Write([]byte(line))
		if err != nil || n != len(line) {
			t.Fatalf("write #%d: n=%d err=%v", i, n, err)
		}
	}

	r, ok := findTLSRecord(h.snapshot(), "client rejected our certificate repeatedly")
	if !ok {
		t.Fatalf("expected one-shot WARN after 3 unknown-cert lines; got %d records", len(h.snapshot()))
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level: got %v, want WARN", r.Level)
	}
	var clientAttr string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "client" {
			clientAttr = a.Value.String()
		}
		return true
	})
	if clientAttr != "192.0.2.55" {
		t.Errorf("client attr: got %q, want 192.0.2.55", clientAttr)
	}
}

// TestTLSHandshakeWatcher_BelowThresholdStaysQuiet — two failures must
// NOT trip the WARN; the threshold is 3.
func TestTLSHandshakeWatcher_BelowThresholdStaysQuiet(t *testing.T) {
	h := &tlsRecordCapture{}
	w := newTLSHandshakeWatcher(slog.New(h))
	w.now = func() time.Time { return time.Now() }

	line := "http: TLS handshake error from 10.0.0.1:1234: remote error: tls: unknown certificate\n"
	_, _ = w.Write([]byte(line))
	_, _ = w.Write([]byte(line))

	if _, ok := findTLSRecord(h.snapshot(), "client rejected our certificate"); ok {
		t.Fatal("two unknown-cert lines must not trip the WARN")
	}
}

// TestTLSHandshakeWatcher_OneShotPerBurst — once the WARN fires for a
// burst, additional lines within the same window are silent. After the
// window clears, a fresh burst can re-warn.
func TestTLSHandshakeWatcher_OneShotPerBurst(t *testing.T) {
	h := &tlsRecordCapture{}
	w := newTLSHandshakeWatcher(slog.New(h))
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }

	line := "http: TLS handshake error from 198.51.100.7:9000: remote error: tls: unknown certificate\n"
	for i := 0; i < 5; i++ {
		_, _ = w.Write([]byte(line))
	}

	warns := 0
	for _, r := range h.snapshot() {
		if strings.Contains(r.Message, "client rejected our certificate") {
			warns++
		}
	}
	if warns != 1 {
		t.Errorf("burst within window: got %d warns, want 1", warns)
	}

	// Advance past the window and fire another burst.
	now = now.Add(tlsCachedCertWindow + time.Second)
	for i := 0; i < 3; i++ {
		_, _ = w.Write([]byte(line))
	}
	warns = 0
	for _, r := range h.snapshot() {
		if strings.Contains(r.Message, "client rejected our certificate") {
			warns++
		}
	}
	if warns != 2 {
		t.Errorf("post-window burst: got %d warns total, want 2", warns)
	}
}

// TestTLSHandshakeWatcher_NonHandshakeLinePassesThrough — anything
// that isn't a TLS handshake line must reach the underlying slog at
// WARN so the operator doesn't lose unexpected stdlib output.
func TestTLSHandshakeWatcher_NonHandshakeLinePassesThrough(t *testing.T) {
	h := &tlsRecordCapture{}
	w := newTLSHandshakeWatcher(slog.New(h))
	w.now = func() time.Time { return time.Now() }

	_, _ = w.Write([]byte("some other http server message\n"))

	r, ok := findTLSRecord(h.snapshot(), "http server log")
	if !ok {
		t.Fatal("non-handshake line should be forwarded as WARN")
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level: got %v, want WARN", r.Level)
	}
}

// TestTLSHandshakeWatcher_PerIPIsolation — three failures from
// different IPs each don't trip a WARN; the threshold is per-IP.
func TestTLSHandshakeWatcher_PerIPIsolation(t *testing.T) {
	h := &tlsRecordCapture{}
	w := newTLSHandshakeWatcher(slog.New(h))
	w.now = func() time.Time { return time.Now() }

	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		line := "http: TLS handshake error from " + ip + ":1000" +
			string(rune('0'+i)) + ": remote error: tls: unknown certificate\n"
		_, _ = w.Write([]byte(line))
	}

	if _, ok := findTLSRecord(h.snapshot(), "client rejected our certificate"); ok {
		t.Fatal("one failure per distinct IP must not trip the per-IP WARN")
	}
}
