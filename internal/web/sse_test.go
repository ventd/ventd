package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// newSSEServer spins up a fully-wired Server behind an httptest server
// and returns both the server and a ready-to-use session cookie. SSE
// tests run through the real middleware stack (auth, origin-check, CSP
// headers) so the handler's behaviour under the same conditions as a
// live daemon is what's being exercised.
func newSSEServer(t *testing.T, tickInterval time.Duration) (*httptest.Server, *http.Cookie) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	password := "ssepass123!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	sm := setupmgr.New(cal, logger)
	restart := make(chan struct{}, 1)
	srv := New(ctx, &cfgPtr, t.TempDir()+"/config.yaml", "", logger, cal, sm, restart, hwdiag.NewStore())
	srv.sseInterval = tickInterval

	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)

	// Authenticate and pluck the session cookie for reuse on /api/events.
	// originCheck middleware (security.go) rejects non-GET requests whose
	// Origin header doesn't match the Host — the httptest server URL
	// supplies both.
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader("password="+password))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", ts.URL)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatalf("login did not set %q cookie; status=%d cookies=%v", sessionCookie, resp.StatusCode, resp.Cookies())
	}
	return ts, session
}

// TestSSE_EmitsStatusFrames verifies the handler writes a well-formed
// initial frame immediately and then one frame per tick. A 50ms tick
// keeps the test fast while leaving enough slack that CI scheduling
// jitter doesn't cause flakes.
func TestSSE_EmitsStatusFrames(t *testing.T) {
	ts, session := newSSEServer(t, 50*time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	req.AddCookie(session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", cc)
	}

	// Collect events off the stream until we've seen at least 3 frames
	// or the overall deadline trips. A ~50ms tick should deliver three
	// frames (initial + two ticks) well within 500ms.
	events, err := readSSEFrames(resp.Body, 3, 2*time.Second)
	if err != nil {
		t.Fatalf("read SSE frames: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("got %d events, want >=3", len(events))
	}

	for i, ev := range events {
		if ev.name != "status" {
			t.Errorf("event[%d].name=%q, want %q", i, ev.name, "status")
		}
		var payload statusResponse
		if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
			t.Errorf("event[%d] json: %v; data=%q", i, err, ev.data)
			continue
		}
		// Empty config — no sensors or fans — but the timestamp must
		// be set and the slices must be non-nil (the handler allocates
		// zero-length slices so JSON serialises them as [] not null).
		if payload.Timestamp.IsZero() {
			t.Errorf("event[%d] timestamp is zero", i)
		}
		if payload.Sensors == nil {
			t.Errorf("event[%d] sensors is nil, want []", i)
		}
		if payload.Fans == nil {
			t.Errorf("event[%d] fans is nil, want []", i)
		}
	}

	// Advancing timestamps prove events are freshly built per tick
	// rather than a single snapshot replayed. The first frame fires
	// synchronously on connect, subsequent frames on the ticker — so
	// the gap between events[0] and events[2] must be at least one
	// tick interval.
	if !events[2].ts.After(events[0].ts) {
		t.Errorf("expected monotonic timestamps across frames; first=%v last=%v",
			events[0].ts, events[2].ts)
	}
}

// TestSSE_RequiresAuth guards the auth middleware wiring. SSE carries
// the same live fan/sensor data as /api/status, so leaking it
// unauth'd would be equivalent to exposing /api/status.
func TestSSE_RequiresAuth(t *testing.T) {
	ts, _ := newSSEServer(t, 50*time.Millisecond)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	// No session cookie attached.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unauth'd request returned 200; expected 401/redirect")
	}
}

// TestSSE_ClientDisconnectStopsHandler exercises the ctx.Done() path:
// cancel the request, confirm the handler doesn't leak a goroutine by
// observing that a follow-up request on the same server returns
// promptly (a stuck handler would keep the test server responsive
// either way, but a blocked handler goroutine would not release the
// server's WriteTimeout deadline — this test is a light sanity check,
// not a strict goroutine-leak assertion).
func TestSSE_ClientDisconnectStopsHandler(t *testing.T) {
	ts, session := newSSEServer(t, 50*time.Millisecond)

	reqCtx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/events", nil)
	req.AddCookie(session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}

	// Read one frame to confirm the handler started streaming.
	if _, err := readSSEFrames(resp.Body, 1, 500*time.Millisecond); err != nil {
		t.Fatalf("read first frame: %v", err)
	}

	// Cancel: the transport closes the connection, the handler's
	// r.Context() fires Done, and the handler returns.
	cancel()
	_ = resp.Body.Close()

	// Give the handler a moment to unwind, then fire a second request
	// on a fresh context. If the server is healthy it responds
	// immediately; a hung handler wouldn't block the server (each
	// request has its own goroutine) but this confirms the server is
	// still serving.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("post-cancel GET /api/ping: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("ping after cancel: status=%d, want 200", resp2.StatusCode)
	}
}

type sseEvent struct {
	name string
	data string
	ts   time.Time // parsed from payload.Timestamp for ordering checks
}

// readSSEFrames reads `want` frames or times out. Each frame ends on a
// blank line per the EventStream spec; lines are accumulated into
// (event, data) pairs.
func readSSEFrames(body interface {
	Read([]byte) (int, error)
}, want int, timeout time.Duration) ([]sseEvent, error) {
	type result struct {
		events []sseEvent
		err    error
	}
	done := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(newReaderWrap(body))
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		var events []sseEvent
		var curName, curData string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				// Frame terminator.
				if curName == "" && curData == "" {
					continue
				}
				ev := sseEvent{name: curName, data: curData}
				if curData != "" {
					var p statusResponse
					if err := json.Unmarshal([]byte(curData), &p); err == nil {
						ev.ts = p.Timestamp
					}
				}
				events = append(events, ev)
				curName, curData = "", ""
				if len(events) >= want {
					done <- result{events: events}
					return
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				curName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				curData = strings.TrimPrefix(line, "data: ")
			}
		}
		done <- result{events: events, err: scanner.Err()}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return r.events, r.err
		}
		return r.events, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %v waiting for %d frames", timeout, want)
	}
}

// newReaderWrap adapts the minimal Read interface the helper takes
// into an io.Reader so bufio.Scanner can consume it. This keeps the
// helper signature independent of the net/http.Response type, which
// matters for the discard test where we don't use http.Response.
type readerWrap struct {
	r interface {
		Read([]byte) (int, error)
	}
}

func newReaderWrap(r interface {
	Read([]byte) (int, error)
}) *readerWrap {
	return &readerWrap{r: r}
}

func (rw *readerWrap) Read(p []byte) (int, error) { return rw.r.Read(p) }
