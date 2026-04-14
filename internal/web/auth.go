package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "ventd_session"
	bcryptCost    = 12
)

// sessionStore holds active authenticated sessions in memory.
// Sessions are lost on daemon restart — users re-login, which is acceptable.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	s := &sessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
	go s.reap()
	return s
}

// create generates a new session token, stores it, and returns it.
func (s *sessionStore) create() (string, error) {
	tok, err := randomHex(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[tok] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return tok, nil
}

// valid reports whether the token is present and not expired.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	exp, ok := s.sessions[token]
	s.mu.Unlock()
	return ok && time.Now().Before(exp)
}

// delete removes a session (logout).
func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// reap periodically removes expired sessions.
func (s *sessionStore) reap() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for tok, exp := range s.sessions {
			if now.After(exp) {
				delete(s.sessions, tok)
			}
		}
		s.mu.Unlock()
	}
}

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkPassword reports whether plaintext matches the stored bcrypt hash.
func checkPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

// GenerateSetupToken returns a cryptographically random token formatted as
// XXXXX-XXXXX-XXXXX for display in the terminal on first boot.
func GenerateSetupToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h := hex.EncodeToString(b) // 16 hex chars
	// Format as XXXXX-XXXXX-XXXXX (use first 15 chars, upper-cased)
	h = h[:15]
	return h[0:5] + "-" + h[5:10] + "-" + h[10:15], nil
}

// sessionToken reads the session cookie value from r.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// setSessionCookie writes the session token cookie to w.
func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
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
		return "", err
	}
	return hex.EncodeToString(b), nil
}
