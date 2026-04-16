package web

import (
	"net/http"
	"sync/atomic"
	"time"
)

// readyStaleWindow bounds how fresh the last recorded sensor read must be
// for /readyz to return 200. Matches the default poll interval's worst case
// plus one tick of slack.
const readyStaleWindow = 5 * time.Second

// ReadyState is the atomic readiness snapshot driven by main.go and the
// controller tick and consumed by /healthz and /readyz. All fields use
// sync/atomic so the HTTP handlers never race the control loop.
//
// Semantics:
//   - Healthy: post-init has completed (daemon has finished startup).
//   - WatchdogPinged: the systemd heartbeat goroutine has been armed at
//     least once; a one-shot latch flipped in main.go after
//     sdnotify.StartHeartbeat returns with a non-zero interval.
//   - LastSensorRead: monotonic UnixNano timestamp of the last completed
//     controller tick that attempted a sensor read.
type ReadyState struct {
	healthy          atomic.Bool
	watchdogPinged   atomic.Bool
	lastSensorReadNs atomic.Int64
}

// NewReadyState returns a zero-valued ReadyState. Safe for concurrent use
// across the web handlers, the controller tick, and main.
func NewReadyState() *ReadyState { return &ReadyState{} }

// SetHealthy flips the post-init latch. Call from main.go once the daemon
// has finished startup.
func (r *ReadyState) SetHealthy() { r.healthy.Store(true) }

// Healthy reports whether SetHealthy has been called.
func (r *ReadyState) Healthy() bool { return r.healthy.Load() }

// SetWatchdogPinged flips the heartbeat-armed latch. Call from main.go once
// sdnotify.StartHeartbeat has returned a running heartbeat.
func (r *ReadyState) SetWatchdogPinged() { r.watchdogPinged.Store(true) }

// WatchdogPinged reports whether the heartbeat-armed latch has been flipped.
func (r *ReadyState) WatchdogPinged() bool { return r.watchdogPinged.Load() }

// MarkSensorRead records the timestamp of the latest completed sensor-read
// tick. The controller calls this at the end of each tick; /readyz compares
// against time.Now() and the readyStaleWindow.
func (r *ReadyState) MarkSensorRead(t time.Time) {
	r.lastSensorReadNs.Store(t.UnixNano())
}

// LastSensorRead returns the latest recorded timestamp or the zero time if
// no read has been recorded yet.
func (r *ReadyState) LastSensorRead() time.Time {
	ns := r.lastSensorReadNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// handleHealthz GET /healthz — 200 once post-init has completed, 503 before.
// Unauthenticated: used by container orchestrators and reverse proxies.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.ready == nil || !s.ready.Healthy() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("starting\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz GET /readyz — 200 when the watchdog has pinged at least once
// AND a sensor read has completed in the last readyStaleWindow. 503
// otherwise, with a plain-text body naming which gate failed so Prometheus
// and operators can distinguish "never started" from "stalled".
// Unauthenticated for the same reason as /healthz.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.ready == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: readiness tracking disabled\n"))
		return
	}
	if !s.ready.WatchdogPinged() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: watchdog has not pinged\n"))
		return
	}
	last := s.ready.LastSensorRead()
	if last.IsZero() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: no sensor read recorded\n"))
		return
	}
	if s.readyNow().Sub(last) > readyStaleWindow {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: last sensor read too old\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// readyNow returns time.Now() or the injected clock in tests. Lifted to a
// method so /readyz's staleness check is deterministic under test.
func (s *Server) readyNow() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}
