package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "ventd_session"
	bcryptCost    = 12
)

// sessionData carries per-session state in the in-memory store.
//
// Each session pairs a session-cookie token (random 32 bytes hex,
// HttpOnly) with a CSRF token (random 32 bytes hex, NOT HttpOnly so
// the JS layer can read it from the `ventd_csrf` cookie set on
// login). State-changing requests are required by `requireCSRF` to
// echo the CSRF token in the `X-CSRF-Token` header; the middleware
// validates `header == session.csrfToken` via constant-time compare.
//
// Pairing the CSRF token to the session means a session-rotated
// login (or operator logout-then-login) regenerates the CSRF token
// and any in-flight forged request signed with the prior CSRF
// becomes invalid.
type sessionData struct {
	expiry    time.Time
	csrfToken string
}

// sessionStore holds active authenticated sessions in memory.
// Sessions are lost on daemon restart — users re-login, which is acceptable.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]sessionData // session-cookie token → per-session data
	ttl      time.Duration
}

// newSessionStore starts a reaper goroutine that exits when ctx is cancelled,
// so the session store does not leak past daemon shutdown.
func newSessionStore(ctx context.Context, ttl time.Duration) *sessionStore {
	s := &sessionStore{
		sessions: make(map[string]sessionData),
		ttl:      ttl,
	}
	go s.reap(ctx)
	return s
}

// create generates a new session-cookie token + CSRF token, stores
// them paired, and returns the session-cookie token.
//
// To keep call-sites + tests compatible the function returns just
// the session token; production callers fetch the CSRF token via
// `csrfFor(token)` immediately after to set the `ventd_csrf` cookie
// + include it in the login response JSON.
func (s *sessionStore) create() (string, error) {
	tok, err := randomHex(32)
	if err != nil {
		return "", err
	}
	csrf, err := randomHex(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[tok] = sessionData{
		expiry:    time.Now().Add(s.ttl),
		csrfToken: csrf,
	}
	s.mu.Unlock()
	return tok, nil
}

// csrfFor returns the CSRF token paired with the given session-cookie
// token, or ("", false) when the session is unknown / expired.
//
// `requireCSRF` middleware uses this to look up the expected token
// before constant-time comparing against the `X-CSRF-Token` header.
func (s *sessionStore) csrfFor(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	sd, ok := s.sessions[token]
	s.mu.Unlock()
	if !ok || time.Now().After(sd.expiry) {
		return "", false
	}
	return sd.csrfToken, true
}

// valid reports whether the token is present and not expired.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	sd, ok := s.sessions[token]
	s.mu.Unlock()
	return ok && time.Now().Before(sd.expiry)
}

// delete removes a session (logout).
func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// reap periodically removes expired sessions and exits when ctx is done.
func (s *sessionStore) reap(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for tok, sd := range s.sessions {
				if now.After(sd.expiry) {
					delete(s.sessions, tok)
				}
			}
			s.mu.Unlock()
		}
	}
}

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("web: bcrypt hash: %w", err)
	}
	return string(b), nil
}

// checkPassword reports whether plaintext matches the stored bcrypt hash.
func checkPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

// sessionToken reads the session cookie value from r.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// setSessionCookie writes the session token cookie to w. secure controls
// the Secure flag — callers should pass true whenever TLS is active or a
// trusted TLS-terminating proxy is in front, so session tokens never
// travel in plaintext.
//
// SameSite=Strict (v0.5.31): Lax permitted some cross-site GET / form-
// POST flows from links and form submits in another tab to carry the
// session cookie. Strict refuses to attach the cookie to ANY cross-site
// navigation; combined with the per-request CSRF token (RULE-WEB-CSRF-
// TOKEN-REQUIRED-ON-STATE-CHANGE) and the Origin allow-list (the existing
// originAllowed check) this closes the layered CSRF defence.
func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie removes the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

// randomHex returns n random bytes as a hex string.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("web: read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
