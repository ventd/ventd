package web

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- Security headers ---------------------------------------------------

// securityHeaders sets conservative headers on every response. HSTS is
// only emitted when the response is being served over TLS; setting it
// over plain HTTP does nothing useful and poisons browsers if the
// deployment later downgrades.
func securityHeaders() func(http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			// Only emit HSTS when the request actually arrived over TLS.
			// Over plain HTTP it is a no-op; behind a non-TLS proxy it
			// poisons clients if the deployment later downgrades. Operators
			// terminating TLS at a proxy should inject HSTS at the proxy.
			if r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- Origin / CSRF check ------------------------------------------------

// originCheck rejects non-safe methods unless the Origin header matches
// the request's Host. Same-origin fetches in browsers always send Origin
// on POST/PUT/DELETE, so missing Origin on a mutation is a red flag on a
// LAN-bound daemon. GET/HEAD/OPTIONS pass through.
func originCheck(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if !originAllowed(r) {
				logger.Warn("web: origin check failed",
					"path", r.URL.Path,
					"method", r.Method,
					"origin", r.Header.Get("Origin"),
					"host", r.Host,
					"remote", r.RemoteAddr,
				)
				http.Error(w, "forbidden: origin mismatch", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Browsers send Origin on all cross-origin POSTs and all same-origin
		// POST/PUT/DELETE. Rejecting when it is missing denies the main CSRF
		// shape. Scripts that omit Origin should send a password, not a cookie.
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return hostsMatch(u.Host, r.Host)
}

// hostsMatch compares two "host[:port]" strings for equality, tolerating
// default ports. It also treats literal loopback addresses as equivalent
// to "localhost" so same-host browser loads work.
func hostsMatch(a, b string) bool {
	aHost, aPort := splitHostPortDefault(a)
	bHost, bPort := splitHostPortDefault(b)
	if aPort != bPort {
		return false
	}
	if aHost == bHost {
		return true
	}
	aLoop := isLoopbackHost(aHost)
	bLoop := isLoopbackHost(bHost)
	return aLoop && bLoop
}

func splitHostPortDefault(hp string) (string, string) {
	h, p, err := net.SplitHostPort(hp)
	if err != nil {
		return hp, ""
	}
	return h, p
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// --- Login rate limiter --------------------------------------------------

// loginLimiter tracks consecutive failed logins per client IP and locks
// the key out for cooldown after threshold failures. Successful auth
// clears the counter. A nil limiter is a no-op so tests can opt out.
type loginLimiter struct {
	mu        sync.Mutex
	state     map[string]*ipAttempt
	threshold int
	cooldown  time.Duration
	now       func() time.Time // overridable for tests
}

type ipAttempt struct {
	failures    int
	lockedUntil time.Time
}

func newLoginLimiter(threshold int, cooldown time.Duration) *loginLimiter {
	return &loginLimiter{
		state:     make(map[string]*ipAttempt),
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
	}
}

// allow reports whether the key is currently allowed to attempt login.
// If false, retryAfter is the remaining cooldown.
func (l *loginLimiter) allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.state[key]
	if !ok {
		return true, 0
	}
	now := l.now()
	if now.Before(s.lockedUntil) {
		return false, s.lockedUntil.Sub(now)
	}
	// Cooldown expired — clear state so the client gets a fresh window.
	if !s.lockedUntil.IsZero() {
		delete(l.state, key)
	}
	return true, 0
}

// recordFailure bumps the counter and returns whether the client is now
// locked out (transition into lockout).
func (l *loginLimiter) recordFailure(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.state[key]
	if !ok {
		s = &ipAttempt{}
		l.state[key] = s
	}
	s.failures++
	if s.failures >= l.threshold {
		s.lockedUntil = l.now().Add(l.cooldown)
		return true
	}
	return false
}

// recordSuccess clears any tracked state for the key.
func (l *loginLimiter) recordSuccess(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.state, key)
	l.mu.Unlock()
}

// clientIP extracts the IP portion of r.RemoteAddr. X-Forwarded-For is
// intentionally NOT consulted unless the operator has opted in to a
// trusted-proxy configuration — otherwise the limiter is trivially
// bypassed by spoofing the header. The hook is here for future use.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- Body size guard -----------------------------------------------------

// defaultMaxBody is the cap applied to JSON request bodies unless a
// specific handler needs headroom. 1 MiB dwarfs any config we realistically
// accept and still blocks trivial OOM attempts.
const defaultMaxBody = 1 << 20

// limitBody wraps r.Body with a MaxBytesReader and returns the wrapped
// request. Call before json.Decode so oversized bodies surface as
// http.MaxBytesError and the handler can emit 413.
func limitBody(w http.ResponseWriter, r *http.Request, max int64) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
}

// isMaxBytesErr reports whether err is the sentinel returned by
// http.MaxBytesReader when the limit is exceeded.
func isMaxBytesErr(err error) bool {
	if err == nil {
		return false
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return true
	}
	// json.Decoder wraps the underlying read error as a plain error; the
	// text match is the only reliable fallback.
	return strings.Contains(err.Error(), "http: request body too large")
}
