package web

import (
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Markers used to recognise stdlib net/http log lines for TLS
// handshake failures. The format string in net/http/server.go is
// stable: `http: TLS handshake error from %s: %v`.
const (
	tlsHandshakeMarker     = "http: TLS handshake error from "
	tlsUnknownCertMarker   = "remote error: tls: unknown certificate"
	tlsCachedCertWindow    = 30 * time.Second
	tlsCachedCertThreshold = 3
)

// tlsHandshakeWatcher is an io.Writer designed to be plugged into
// http.Server.ErrorLog. It parses each log line for the stdlib's
// `http: TLS handshake error from <ip:port>: <err>` shape and, when a
// client IP racks up ≥3 "unknown certificate" handshakes within 30s
// (the classic browser-cached-old-self-signed-cert pattern), emits a
// one-shot WARN telling the operator what the user needs to do (#1169).
// Non-handshake lines are forwarded as Warn so we don't lose anything
// the stdlib used to print to stderr.
type tlsHandshakeWatcher struct {
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	state  map[string]*tlsClientState
}

type tlsClientState struct {
	times  []time.Time
	warned bool
}

func newTLSHandshakeWatcher(logger *slog.Logger) *tlsHandshakeWatcher {
	return &tlsHandshakeWatcher{
		logger: logger,
		now:    time.Now,
		state:  map[string]*tlsClientState{},
	}
}

// Write implements io.Writer for log.New. The stdlib hands us one line
// per call (trailing newline included). Always returns len(p), nil so
// the underlying logger never sees a write error.
func (w *tlsHandshakeWatcher) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	idx := strings.Index(line, tlsHandshakeMarker)
	if idx < 0 {
		w.logger.Warn("web: http server log", "line", line)
		return len(p), nil
	}
	rest := line[idx+len(tlsHandshakeMarker):]
	addrEnd := strings.Index(rest, ": ")
	if addrEnd < 0 {
		w.logger.Warn("tls: handshake error", "raw", line)
		return len(p), nil
	}
	addr := rest[:addrEnd]
	msg := rest[addrEnd+2:]
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if !strings.Contains(msg, tlsUnknownCertMarker) {
		// Other handshake failure — keep at Debug so the journal
		// isn't flooded with the normal background hum of port
		// scanners hitting :9999.
		w.logger.Debug("tls: handshake error", "client", host, "err", msg)
		return len(p), nil
	}
	w.recordUnknownCert(host)
	return len(p), nil
}

// recordUnknownCert bumps the per-IP counter, prunes stamps older than
// the window, and emits a one-shot WARN when the threshold trips.
// `warned` is re-armed when the window clears so a *fresh* burst from
// the same IP can re-warn later.
func (w *tlsHandshakeWatcher) recordUnknownCert(host string) {
	now := w.now()
	cutoff := now.Add(-tlsCachedCertWindow)

	w.mu.Lock()
	defer w.mu.Unlock()

	st := w.state[host]
	if st == nil {
		st = &tlsClientState{}
		w.state[host] = st
	}
	kept := st.times[:0]
	for _, t := range st.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	st.times = append(kept, now)

	if len(st.times) >= tlsCachedCertThreshold && !st.warned {
		st.warned = true
		w.logger.Warn(
			"tls: client rejected our certificate repeatedly; likely browser-cached old cert — user needs to accept the new cert warning",
			"client", host,
			"count", len(st.times),
			"window", tlsCachedCertWindow.String(),
		)
		return
	}
	if st.warned && len(st.times) < tlsCachedCertThreshold {
		st.warned = false
	}
}
