package web

import (
	"context"
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
			//
			// max-age starts at 5 minutes so an operator who misconfigures
			// a cert can recover by serving plain HTTP for a short window.
			// TODO: raise to 31536000 (1 year) once deployments are stable.
			if r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=300")
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
//
// The state map is bounded: an attacker who walks source IPs (cheap on
// IPv6) cannot grow it without limit. A background reaper sweeps expired
// entries; on write, an over-cap map evicts its least-recently-seen key.
type loginLimiter struct {
	mu        sync.Mutex
	state     map[string]*ipAttempt
	threshold int
	cooldown  time.Duration
	maxKeys   int
	now       func() time.Time // overridable for tests
}

type ipAttempt struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

// loginLimiterMaxKeys caps the tracked IPs. 4096 is generous for any
// realistic LAN deployment and cheap — each entry is ~64 bytes.
const loginLimiterMaxKeys = 4096

// newLoginLimiter starts a reaper goroutine scoped to ctx so the limiter
// does not leak state past daemon shutdown. Pass the daemon-lifetime
// context; the reaper exits when that context cancels.
func newLoginLimiter(ctx context.Context, threshold int, cooldown time.Duration) *loginLimiter {
	l := &loginLimiter{
		state:     make(map[string]*ipAttempt),
		threshold: threshold,
		cooldown:  cooldown,
		maxKeys:   loginLimiterMaxKeys,
		now:       time.Now,
	}
	go l.reap(ctx)
	return l
}

// reap periodically evicts entries whose cooldown has expired and any
// pre-lock records older than one cooldown window. The tick is half the
// cooldown so we converge quickly after a burst without spinning.
func (l *loginLimiter) reap(ctx context.Context) {
	interval := l.cooldown / 2
	if interval < time.Minute {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}

// sweep removes entries whose lockout expired or whose last activity was
// more than one cooldown ago. Exposed (lowercase) for tests.
func (l *loginLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.cooldown)
	for k, s := range l.state {
		if !s.lockedUntil.IsZero() && now.After(s.lockedUntil) {
			delete(l.state, k)
			continue
		}
		if s.lockedUntil.IsZero() && s.lastSeen.Before(cutoff) {
			delete(l.state, k)
		}
	}
}

// evictOldestLocked drops the single entry with the oldest lastSeen so a
// new key can be inserted. Caller must hold l.mu.
func (l *loginLimiter) evictOldestLocked() {
	var oldestKey string
	var oldestSeen time.Time
	for k, s := range l.state {
		if oldestKey == "" || s.lastSeen.Before(oldestSeen) {
			oldestKey, oldestSeen = k, s.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.state, oldestKey)
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
	now := l.now()
	s, ok := l.state[key]
	if !ok {
		// Cap check before insert — one synchronous eviction is cheaper
		// than letting the map grow unbounded, and O(n) on n≤4096 is fine.
		if len(l.state) >= l.maxKeys {
			l.evictOldestLocked()
		}
		s = &ipAttempt{}
		l.state[key] = s
	}
	s.failures++
	s.lastSeen = now
	if s.failures >= l.threshold {
		s.lockedUntil = now.Add(l.cooldown)
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

// parseTrustedProxies parses CIDRs from config.Web.TrustProxy. Validation
// already rejects invalid entries at Load time; a failure here means a
// hand-constructed test config — log and drop the bad entry rather than
// panic.
func parseTrustedProxies(cidrs []string, logger *slog.Logger) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			logger.Warn("web: invalid trust_proxy CIDR, skipping", "cidr", c, "err", err)
			continue
		}
		out = append(out, n)
	}
	return out
}

func isTrustedProxy(ip net.IP, proxies []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range proxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteIP returns the IP half of r.RemoteAddr, with the port stripped.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// resolveClientIP returns the IP the rate limiter and access logs should
// treat as the caller. When proxies is empty, X-Forwarded-For is ignored
// — otherwise a hostile client bypasses the limiter by setting the
// header itself.
//
// When the peer IS inside a trusted proxy CIDR, walk XFF right-to-left
// and return the first entry whose IP is NOT itself a trusted proxy.
// That entry is the real client; hops to its left are attacker-controlled
// and would let a client prepend a spoofed address. If every XFF entry is
// trusted, fall back to the leftmost entry. This matches nginx real_ip
// and Rails RemoteIp and is the standard secure XFF algorithm.
func resolveClientIP(r *http.Request, proxies []*net.IPNet) string {
	peer := remoteIP(r)
	if len(proxies) == 0 {
		return peer
	}
	peerIP := net.ParseIP(peer)
	if !isTrustedProxy(peerIP, proxies) {
		return peer
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if !isTrustedProxy(ip, proxies) {
			return candidate
		}
	}
	first := strings.TrimSpace(parts[0])
	if net.ParseIP(first) != nil {
		return first
	}
	return peer
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
