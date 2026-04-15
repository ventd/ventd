package web

import (
	"bytes"
	"context"
	"io"
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

// newSecuritySrv builds a Server whose handler chain (headers + origin check
// + mux) is wired up exactly as in production, then returns a session token
// so tests can hit auth-gated endpoints.
func newSecuritySrv(t *testing.T) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	sm := setupmgr.New(cal, logger)
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(config.Empty())
	restartCh := make(chan struct{}, 1)
	srv := New(context.Background(), &liveCfg, t.TempDir()+"/config.yaml", logger, cal, sm, restartCh, "", hwdiag.NewStore())
	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return srv, tok
}

// --- Origin check --------------------------------------------------------

func TestOriginMismatchRejected(t *testing.T) {
	srv, tok := newSecuritySrv(t)

	cases := []struct {
		name, origin string
		host         string
		want         int
	}{
		{"cross_origin", "http://evil.example", "ventd.local:9999", http.StatusForbidden},
		{"missing_origin", "", "ventd.local:9999", http.StatusForbidden},
		{"same_origin", "http://ventd.local:9999", "ventd.local:9999", http.StatusOK},
		{"loopback_aliases", "http://127.0.0.1:9999", "localhost:9999", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.NewReader(`{"version":1}`)
			req := httptest.NewRequest("PUT", "/api/config", body)
			req.Host = tc.host
			req.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			rr := httptest.NewRecorder()
			srv.handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestOriginCheckSkipsSafeMethods(t *testing.T) {
	srv, tok := newSecuritySrv(t)
	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Host = "ventd.local:9999"
	// No Origin header; GET must pass regardless.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// --- Security headers ----------------------------------------------------

func TestSecurityHeadersOnAllResponses(t *testing.T) {
	srv, _ := newSecuritySrv(t)
	req := httptest.NewRequest("GET", "/login", nil)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	h := rr.Result().Header
	for _, name := range []string{
		"Content-Security-Policy",
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
	} {
		if h.Get(name) == "" {
			t.Errorf("%s header missing", name)
		}
	}
	// Plain HTTP must not carry HSTS.
	if h.Get("Strict-Transport-Security") != "" {
		t.Errorf("HSTS set on plain HTTP: %q", h.Get("Strict-Transport-Security"))
	}
}

// --- Rate limiter --------------------------------------------------------

func TestLoginLimiterLockoutAndReset(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := newLoginLimiter(ctx, 3, time.Minute)
	l.now = func() time.Time { return now }

	// Below threshold: allowed.
	for i := 0; i < 2; i++ {
		if ok, _ := l.allow("1.2.3.4"); !ok {
			t.Fatalf("iter %d: unexpected block", i)
		}
		if locked := l.recordFailure("1.2.3.4"); locked {
			t.Fatalf("iter %d: locked too early", i)
		}
	}
	// Third failure trips the lock.
	if locked := l.recordFailure("1.2.3.4"); !locked {
		t.Fatal("3rd failure should lock")
	}
	if ok, retry := l.allow("1.2.3.4"); ok || retry <= 0 {
		t.Fatalf("locked key should be blocked, got ok=%v retry=%v", ok, retry)
	}
	// Other IPs are unaffected.
	if ok, _ := l.allow("5.6.7.8"); !ok {
		t.Fatal("unrelated IP should be allowed")
	}
	// After cooldown, allow returns true and clears state.
	now = now.Add(2 * time.Minute)
	if ok, _ := l.allow("1.2.3.4"); !ok {
		t.Fatal("cooldown expired; allow should be true")
	}
	// And the failure counter has been cleared so a single fresh failure
	// does not immediately re-lock.
	if locked := l.recordFailure("1.2.3.4"); locked {
		t.Fatal("counter should reset after cooldown")
	}

	// recordSuccess clears state mid-window.
	l.recordFailure("1.2.3.4")
	l.recordSuccess("1.2.3.4")
	if _, ok := l.state["1.2.3.4"]; ok {
		t.Fatal("recordSuccess should clear tracked state")
	}
}

func TestLoginLimiterEvictsOverCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := newLoginLimiter(ctx, 10, time.Minute)
	l.now = func() time.Time { return now }
	l.maxKeys = 3

	// Walk 5 IPs, one failure each. Cap is 3, so two must be evicted.
	// Advance time so lastSeen is ordered — the two oldest get dropped.
	ips := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5"}
	for _, ip := range ips {
		l.recordFailure(ip)
		now = now.Add(time.Second)
	}
	if got := len(l.state); got != 3 {
		t.Fatalf("state size=%d want 3", got)
	}
	// The last three IPs should survive; the first two were oldest.
	for _, ip := range ips[:2] {
		if _, ok := l.state[ip]; ok {
			t.Errorf("oldest %q still present", ip)
		}
	}
	for _, ip := range ips[2:] {
		if _, ok := l.state[ip]; !ok {
			t.Errorf("recent %q was evicted", ip)
		}
	}

	// sweep() drops expired-cooldown and stale pre-lock entries.
	now = now.Add(2 * time.Minute) // past cooldown + staleness window
	l.sweep()
	if got := len(l.state); got != 0 {
		t.Fatalf("sweep left %d entries, want 0", got)
	}
}

func TestLoginHandlerLocksOutThenResets(t *testing.T) {
	srv, _ := newSecuritySrv(t)
	// Install a password so the normal login branch runs.
	hash, err := HashPassword("correcthorse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash
	srv.cfg.Store(live)

	// Drive the limiter to lock a specific IP.
	post := func(pw string) int {
		body := strings.NewReader("password=" + pw)
		req := httptest.NewRequest("POST", "/login", body)
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "10.0.0.5:55555"
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		return rr.Code
	}
	for i := 0; i < config.DefaultLoginFailThreshold; i++ {
		if got := post("wrong"); got != http.StatusUnauthorized {
			t.Fatalf("iter %d: status=%d want 401", i, got)
		}
	}
	// Next attempt — even with the correct password — is blocked by the
	// limiter because state["10.0.0.5"] locked out.
	if got := post("correcthorse"); got != http.StatusTooManyRequests {
		t.Fatalf("post-lockout status=%d want 429", got)
	}

	// A successful login from a different IP bypasses the lock and clears
	// no state for the locked IP. Reset via direct recordSuccess to prove
	// the reset path works end-to-end.
	srv.loginLim.recordSuccess("10.0.0.5")
	if got := post("correcthorse"); got != http.StatusOK {
		t.Fatalf("after reset status=%d want 200", got)
	}
}

// TestFirstBootProbeDoesNotConsumeAttempts guards against regression of
// audit finding S2: the old login page probed for first-boot mode by
// POSTing an empty password. That probe went through the rate limiter,
// so five page loads on a freshly-started daemon would lock the operator
// out for the full cooldown before they had ever typed a password.
//
// The fix moves the probe to GET /api/auth/state, and POST /login rejects
// an empty password with 400 without calling recordFailure. This test
// exercises both halves: GET probes don't touch the limiter, and five
// empty POSTs followed by one real login still succeed.
func TestFirstBootProbeDoesNotConsumeAttempts(t *testing.T) {
	srv, _ := newSecuritySrv(t)
	hash, err := HashPassword("correcthorse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash
	srv.cfg.Store(live)

	postLogin := func(pw string) int {
		body := strings.NewReader("password=" + pw)
		req := httptest.NewRequest("POST", "/login", body)
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "10.0.0.7:55555"
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		return rr.Code
	}

	// Five GET probes against /api/auth/state — each must return 200 and
	// none may touch the limiter. If any does, the real login below will
	// already be locked out (the default threshold is 5).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/auth/state", nil)
		req.Host = "ventd.local:9999"
		req.RemoteAddr = "10.0.0.7:55555"
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("probe iter %d: status=%d body=%s", i, rr.Code, rr.Body.String())
		}
	}

	// Five empty-password POSTs — the old code path — must each return 400
	// and must NOT count as failed-login attempts. If they did, the last
	// real login below would return 429.
	for i := 0; i < 5; i++ {
		if got := postLogin(""); got != http.StatusBadRequest {
			t.Fatalf("empty POST iter %d: status=%d want 400", i, got)
		}
	}

	// Real login from the same IP still succeeds.
	if got := postLogin("correcthorse"); got != http.StatusOK {
		t.Fatalf("real login after probes: status=%d want 200 (probe must not consume limiter)", got)
	}
}

// TestAuthStateReportsFirstBoot covers the other half of S2: when the
// daemon has no password configured, /api/auth/state must report that so
// the login page knows to switch forms without making a POST.
func TestAuthStateReportsFirstBoot(t *testing.T) {
	srv, _ := newSecuritySrv(t)
	// newSecuritySrv uses config.Empty() which has no PasswordHash —
	// that is the first-boot state.
	req := httptest.NewRequest("GET", "/api/auth/state", nil)
	req.Host = "ventd.local:9999"
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"first_boot":true`) {
		t.Errorf("body=%s want first_boot:true", body)
	}

	// Once a password is set, the probe reports false.
	hash, err := HashPassword("correcthorse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash
	srv.cfg.Store(live)

	req2 := httptest.NewRequest("GET", "/api/auth/state", nil)
	req2.Host = "ventd.local:9999"
	rr2 := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), `"first_boot":false`) {
		t.Errorf("after password set, body=%s want first_boot:false", rr2.Body.String())
	}
}

// --- MaxBytesReader ------------------------------------------------------

func TestConfigPutRejectsOversizedBody(t *testing.T) {
	srv, tok := newSecuritySrv(t)

	// Valid JSON whose single string value outgrows the 1 MiB cap. The
	// decoder has to read the entire quoted string before it can close the
	// value, which forces the MaxBytesReader over its limit.
	filler := bytes.Repeat([]byte("A"), (1<<20)+1024)
	big := append([]byte(`{"pad":"`), filler...)
	big = append(big, []byte(`"}`)...)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(big))
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetPasswordRejectsOversizedBody(t *testing.T) {
	srv, tok := newSecuritySrv(t)

	filler := bytes.Repeat([]byte("A"), (64<<10)+1024)
	big := append([]byte(`{"new":"`), filler...)
	big = append(big, []byte(`"}`)...)
	req := httptest.NewRequest("POST", "/api/set-password", bytes.NewReader(big))
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413", rr.Code)
	}
}

// --- Setup token ---------------------------------------------------------

func TestConsumeSetupTokenConstantTimeAndTTL(t *testing.T) {
	srv, _ := newSecuritySrv(t)
	srv.setupMu.Lock()
	srv.setupToken = "ABCDE-FGHIJ-KLMNO"
	srv.setupExp = time.Now().Add(time.Minute)
	srv.setupMu.Unlock()

	if srv.consumeSetupToken("wrong") {
		t.Fatal("wrong token accepted")
	}
	if srv.consumeSetupToken("short") {
		t.Fatal("length-mismatch token accepted")
	}
	if !srv.consumeSetupToken("ABCDE-FGHIJ-KLMNO") {
		t.Fatal("correct token rejected")
	}

	// Expire the token and verify subsequent calls fail.
	srv.setupMu.Lock()
	srv.setupExp = time.Now().Add(-time.Second)
	srv.setupMu.Unlock()
	if srv.consumeSetupToken("ABCDE-FGHIJ-KLMNO") {
		t.Fatal("expired token accepted")
	}
}
