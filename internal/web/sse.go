package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// defaultSSEInterval matches the client-side /api/status poll cadence
// (scripts/setup.js → setInterval(loadStatus, 2000)). Keeping the same
// tick rate means switching a browser from polling to SSE doesn't shift
// the daemon's sensor/fan read load — one client still pays exactly
// one hwmon sweep every two seconds.
const defaultSSEInterval = 2 * time.Second

// handleEvents streams status frames over Server-Sent Events. The
// handler lives behind the same auth middleware as /api/status (see
// server.go route registration). On first connect the client receives
// an immediate frame so it doesn't have to wait a full tick for its
// first render; subsequent frames fire on a ticker at s.sseInterval.
//
// The handler exits cleanly on three conditions:
//   - r.Context() is cancelled — happens when the client disconnects
//     or the daemon shuts down (request contexts inherit from the
//     Server's base ctx).
//   - A write fails — means the TCP socket is gone; no point looping.
//   - A flush fails — means the underlying writer doesn't support
//     flushing (e.g. a mis-wired test harness); bail rather than
//     buffer events that never reach the client.
//
// WriteTimeout on s.httpSrv is 10s, which would kill any connection
// that outlives a single response write. SSE needs an unbounded
// connection, so we reset the write deadline per frame via
// http.ResponseController rather than carrying the server-level one.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Disable response buffering in front proxies (Nginx honours this
	// as the documented opt-out). Without it a reverse proxy would
	// gather many frames before forwarding, defeating live updates.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)

	interval := s.sseInterval
	if interval <= 0 {
		interval = defaultSSEInterval
	}

	// First frame immediately so the initial paint doesn't wait for a
	// full tick. If this fails the client is already gone — bail.
	if err := s.writeStatusEvent(w, rc); err != nil {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.writeStatusEvent(w, rc); err != nil {
				return
			}
		}
	}
}

// writeStatusEvent serialises one status snapshot and flushes it. The
// event field is hard-coded (`status`) so the client can dispatch by
// name if future frames carry other event types (e.g. calibration
// progress). The payload is the same JSON shape /api/status returns,
// never a user-controlled string — no injection surface on `data:`.
func (s *Server) writeStatusEvent(w http.ResponseWriter, rc *http.ResponseController) error {
	// Cap each write at the tick interval plus a small margin. Without
	// this the server-level WriteTimeout (10s) would kick in and kill
	// the connection after its first timeout window; with it we also
	// guard against a half-broken TCP peer that accepts but never
	// drains the kernel send buffer.
	deadline := time.Now().Add(5 * time.Second)
	// SetWriteDeadline may return http.ErrNotSupported when the
	// underlying writer doesn't expose a net.Conn (rare — only custom
	// test harnesses). Ignore that error: the write itself will still
	// succeed or fail, and the deadline is an optimisation not a
	// correctness requirement.
	_ = rc.SetWriteDeadline(deadline)

	payload, err := json.Marshal(s.buildStatus())
	if err != nil {
		return fmt.Errorf("sse: marshal status: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", payload); err != nil {
		return fmt.Errorf("sse: write frame: %w", err)
	}
	if err := rc.Flush(); err != nil {
		return fmt.Errorf("sse: flush: %w", err)
	}
	return nil
}
