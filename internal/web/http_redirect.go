package web

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// tlsSniffListener wraps an inner net.Listener and dispatches each
// accepted connection based on whether it starts with a TLS ClientHello.
//
// Motivation (see #200): the daemon binds a single port (9999 by
// default) and serves HTTPS on it. A browser that autocompletes the
// scheme to `http://` when the user types `host:9999` lands on the
// TLS socket with a plaintext HTTP request and the stdlib TLS server
// responds with "client sent an HTTP request to an HTTPS server", a
// user-hostile error. This listener detects the scheme mismatch by
// peeking the first byte — a TLS ClientHello always starts with 0x16
// (ContentType.Handshake, RFC 5246 §6.2.1) — and for plaintext HTTP
// connections responds inline with a 301 redirect to https:// before
// the connection is handed back to the main server.
//
// Connections that pass the sniff are returned to the caller exactly
// as they arrived, with the peeked byte prepended back to the read
// stream via peekedConn. The TLS server never sees the difference.
type tlsSniffListener struct {
	inner      net.Listener
	logger     *slog.Logger
	listenAddr string        // host:port the listener was asked to bind; used for the redirect target
	readLimit  time.Duration // cap the time spent reading a plaintext request before giving up
}

func newTLSSniffListener(inner net.Listener, listenAddr string, logger *slog.Logger) *tlsSniffListener {
	return &tlsSniffListener{
		inner:      inner,
		logger:     logger,
		listenAddr: listenAddr,
		readLimit:  5 * time.Second,
	}
}

func (l *tlsSniffListener) Accept() (net.Conn, error) {
	for {
		c, err := l.inner.Accept()
		if err != nil {
			return nil, err
		}
		// Peek the first byte. TLS handshake records start with 0x16.
		// Anything else on a TLS listener is either a plaintext HTTP
		// request from a mis-schemed browser, or garbage (port-scanner,
		// misconfigured proxy, etc.) that we still handle gracefully.
		var first [1]byte
		_ = c.SetReadDeadline(time.Now().Add(l.readLimit))
		n, readErr := io.ReadFull(c, first[:])
		_ = c.SetReadDeadline(time.Time{})
		if readErr != nil || n == 0 {
			// Silent drop — the peer never sent anything, there's no
			// request to respond to and nothing useful to log at Info
			// level. A log line per dropped scan is noise.
			_ = c.Close()
			continue
		}
		if first[0] == 0x16 {
			return &peekedConn{Conn: c, prefix: first[:1]}, nil
		}
		// Plaintext connection — serve a 301 inline in its own
		// goroutine so the Accept loop can keep servicing new TLS
		// clients. The goroutine is self-contained: it finishes the
		// one request it's given, closes the conn, exits.
		go l.serveRedirect(&peekedConn{Conn: c, prefix: first[:1]})
	}
}

func (l *tlsSniffListener) Close() error   { return l.inner.Close() }
func (l *tlsSniffListener) Addr() net.Addr { return l.inner.Addr() }

// serveRedirect reads a single HTTP request off conn and responds with
// a 301 to the https:// equivalent. Runs in its own goroutine per
// plaintext connection. Best-effort: malformed requests close silently
// so scanners don't get a useful response.
func (l *tlsSniffListener) serveRedirect(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(l.readLimit))

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	// Pick the best Host we can: the request's own Host header
	// (preserves `host:port` if the client typed it) falls back to
	// the listener's address.
	host := req.Host
	if host == "" {
		host = l.listenAddr
	}
	// Force the port onto the host. If the client's Host already has
	// a port, trust it; otherwise append ours so the redirect URL
	// points back to the same daemon.
	if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
		if _, port, pErr := net.SplitHostPort(l.listenAddr); pErr == nil {
			host = net.JoinHostPort(host, port)
		}
	}
	target := "https://" + host + req.URL.RequestURI()

	body := `<!DOCTYPE html><html><body>Redirecting to <a href="` + target + `">` + target + `</a>…</body></html>` + "\n"

	resp := "HTTP/1.1 301 Moved Permanently\r\n" +
		"Location: " + target + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"Connection: close\r\n" +
		"Cache-Control: no-store\r\n" +
		"\r\n" +
		body

	_ = conn.SetWriteDeadline(time.Now().Add(l.readLimit))
	_, _ = conn.Write([]byte(resp))

	if l.logger != nil {
		l.logger.Info("web: http->https redirect",
			"client", clientIP(conn.RemoteAddr().String()),
			"method", req.Method,
			"uri", req.URL.RequestURI(),
			"target", target)
	}
}

// clientIP strips the port off a net.Addr string so log lines carry a
// stable key. Best-effort: malformed input returns as-is.
func clientIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// peekedConn replays a previously-peeked byte slice at the head of the
// Read stream, then falls through to the underlying Conn. After the
// prefix is exhausted it's a plain passthrough — we never buffer more
// than the initial peek.
type peekedConn struct {
	net.Conn
	prefix []byte
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
