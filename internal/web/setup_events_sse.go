package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// setupEventsTickInterval bounds the polling cadence on the in-memory
// event ring buffer. 250 ms keeps the activity-feed perceptually live
// (the calibration UI's structured-event renderer fades each row in
// over ~150 ms; a tick faster than that just wastes CPU) while
// staying well below the 10 s server WriteTimeout. Keep-alive comments
// fire on every tick whether or not there's a payload, so the TCP
// path stays warm and middleboxes don't drop the connection during
// long phases like the 20 s sweep.
const setupEventsTickInterval = 250 * time.Millisecond

// handleSetupEvents streams the setup activity feed over Server-Sent
// Events. Subscribers receive the structured {ts, level, tag, text}
// stream that setup.Manager appends to on every phase transition (and
// any future per-fan event hook). The transport is "ring buffer +
// cursor poll" rather than per-subscriber channels: the daemon owns
// the bounded ring, and each connection keeps its own cursor (the TS
// of the last event it saw) and re-flushes anything newer on each
// tick. Avoids the goroutine-plumbing surface for a write-rare /
// read-rare workload (one calibration run is ~200 events over ~50 s).
//
// The handler accepts an optional `?since=<unix-ms>` query parameter
// so a reconnecting client can resume without re-receiving the full
// log. Default cursor is 0 — the client gets every event currently
// in the ring on connect, which doubles as the "browser opens the
// calibration page mid-run" use case.
//
// Like handleEvents, this never returns from the natural exit until
// the client disconnects (r.Context().Done()) or a write/flush fails.
// Auth is enforced by the route registration in server.go.
func (s *Server) handleSetupEvents(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.Error(w, "setup manager not wired", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)

	var cursor int64
	if v := r.URL.Query().Get("since"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			cursor = parsed
		}
	}

	// First pass — flush whatever's already in the ring so a
	// late-connecting client sees the full backlog without waiting
	// a tick.
	if err := s.writeSetupEventsFrame(w, rc, &cursor); err != nil {
		return
	}

	ticker := time.NewTicker(setupEventsTickInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.writeSetupEventsFrame(w, rc, &cursor); err != nil {
				return
			}
		}
	}
}

// writeSetupEventsFrame emits one SSE frame: zero or more structured
// events plus a keep-alive comment when the ring has nothing new.
// Returns the first underlying write/flush error so the loop in
// handleSetupEvents can bail cleanly on a dead client.
func (s *Server) writeSetupEventsFrame(w http.ResponseWriter, rc *http.ResponseController, cursor *int64) error {
	deadline := time.Now().Add(5 * time.Second)
	_ = rc.SetWriteDeadline(deadline)

	events, latest := s.setup.EventsSince(*cursor)
	*cursor = latest

	if len(events) == 0 {
		// SSE comment line — keeps the TCP path warm without
		// firing a JS event handler. Single-byte payloads avoid
		// proxies treating empty frames as half-broken.
		if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
			return fmt.Errorf("sse: write keepalive: %w", err)
		}
		return rc.Flush()
	}

	for _, e := range events {
		payload, err := json.Marshal(e)
		if err != nil {
			// A failure to marshal one event must not break the
			// whole stream — skip it and keep going. In practice
			// the Event struct is all primitives so this never
			// trips on real data.
			continue
		}
		if _, err := fmt.Fprintf(w, "event: setup\ndata: %s\n\n", payload); err != nil {
			return fmt.Errorf("sse: write event: %w", err)
		}
	}
	return rc.Flush()
}
