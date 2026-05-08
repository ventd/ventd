package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// newCSRFServer is a minimal authenticated server fixture for the CSRF
// tests. Uses an empty config (no fan controls); we only exercise the
// middleware chain, not any handler logic.
func newCSRFServer(t *testing.T) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	diag := hwdiag.NewStore()
	cal.SetDiagnosticStore(diag)
	sm := setupmgr.New(cal, logger)
	sm.SetDiagnosticStore(diag)
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(config.Empty())
	restartCh := make(chan struct{}, 1)
	return New(ctx, &liveCfg, "", "", logger, cal, sm, restartCh, diag)
}

// TestRULE_WEB_CSRF_TOKEN_REQUIRED_ON_STATE_CHANGE pins the CSRF
// middleware contract: every state-changing API request through
// the authed routes MUST carry an X-CSRF-Token header that matches
// the session's bound CSRF token. Safe methods bypass the check.
//
// Bound rule: RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE in
// .claude/rules/web-ui.md.
func TestRULE_WEB_CSRF_TOKEN_REQUIRED_ON_STATE_CHANGE(t *testing.T) {
	t.Run("post_without_csrf_header_is_rejected_403", func(t *testing.T) {
		// Authenticated POST without X-CSRF-Token MUST return 403.
		// /api/v1/config is a state-changing endpoint; we don't
		// care about the response body, only the gate firing.
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{}`))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		// NO X-CSRF-Token header.
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403 (CSRF middleware must reject missing header)", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "X-CSRF-Token") {
			t.Errorf("response body = %q; want contains 'X-CSRF-Token' to surface the missing-header reason", rr.Body.String())
		}
	})

	t.Run("post_with_wrong_csrf_token_is_rejected_403", func(t *testing.T) {
		// X-CSRF-Token mismatch MUST return 403 with body
		// indicating CSRF mismatch.
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{}`))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		req.Header.Set("X-CSRF-Token", "0000000000000000000000000000000000000000000000000000000000000000")
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403 (CSRF middleware must reject wrong-token)", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "mismatch") {
			t.Errorf("response body = %q; want contains 'mismatch' to surface the wrong-token reason", rr.Body.String())
		}
	})

	t.Run("post_with_correct_csrf_token_passes_csrf_gate", func(t *testing.T) {
		// Valid X-CSRF-Token MUST clear the CSRF gate. The handler
		// itself may then return a different status (e.g. 400 for
		// empty body / 405 / etc.); the test cares ONLY that we
		// reached past the CSRF middleware. We assert the response
		// is NOT 403 (CSRF rejection) and NOT 401 (auth rejection).
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		csrf, _ := srv.sessions.csrfFor(tok)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{}`))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		req.Header.Set("X-CSRF-Token", csrf)
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusForbidden && strings.Contains(rr.Body.String(), "CSRF") {
			t.Errorf("status = 403 with CSRF body; want CSRF gate to PASS the request through to the handler")
		}
		if rr.Code == http.StatusUnauthorized {
			t.Errorf("status = 401; want auth gate to PASS the request through to CSRF then handler")
		}
	})

	t.Run("get_request_bypasses_csrf_check_entirely", func(t *testing.T) {
		// Safe methods (GET / HEAD / OPTIONS) MUST bypass the CSRF
		// check inside the middleware so read-only handlers aren't
		// affected. /api/v1/status returns 200 with the live cfg
		// snapshot regardless of CSRF header presence.
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		// No X-CSRF-Token header — must still succeed.
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d; want 200 (GET bypasses CSRF check)", rr.Code)
		}
	})

	t.Run("post_without_session_cookie_is_rejected_401_before_csrf_check", func(t *testing.T) {
		// Auth gate fires before CSRF gate. Without a session
		// cookie, the request gets 401 — not 403 — because there
		// is nothing to bind a CSRF token to.
		srv := newCSRFServer(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{}`))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "any-value-doesnt-matter-here")
		// No session cookie.
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401 (auth gate fires before CSRF)", rr.Code)
		}
	})
}

// TestRULE_WEB_COOKIE_SAMESITE_STRICT pins the v0.5.31 flip from
// SameSite=Lax to SameSite=Strict on the session cookie.
//
// Bound rule: RULE-WEB-COOKIE-SAMESITE-STRICT in .claude/rules/web-ui.md.
func TestRULE_WEB_COOKIE_SAMESITE_STRICT(t *testing.T) {
	t.Run("session_cookie_samesite_strict", func(t *testing.T) {
		rr := httptest.NewRecorder()
		setSessionCookie(rr, "test-token", 3600, false)
		got := rr.Result().Cookies()
		if len(got) != 1 {
			t.Fatalf("setSessionCookie wrote %d cookies; want 1", len(got))
		}
		if got[0].SameSite != http.SameSiteStrictMode {
			t.Errorf("SameSite = %v; want SameSiteStrictMode (v0.5.31 flip)", got[0].SameSite)
		}
		if !got[0].HttpOnly {
			t.Error("HttpOnly = false; want true (session cookie must remain HttpOnly)")
		}
	})

	t.Run("csrf_cookie_samesite_strict_and_not_httponly", func(t *testing.T) {
		// CSRF cookie MUST be SameSite=Strict (matches session
		// posture) and MUST NOT be HttpOnly so the JS layer can
		// read it via document.cookie (the read-side of the
		// synchroniser-token pattern).
		rr := httptest.NewRecorder()
		setCSRFCookie(rr, "csrf-token", 3600, false)
		got := rr.Result().Cookies()
		if len(got) != 1 {
			t.Fatalf("setCSRFCookie wrote %d cookies; want 1", len(got))
		}
		if got[0].SameSite != http.SameSiteStrictMode {
			t.Errorf("SameSite = %v; want SameSiteStrictMode", got[0].SameSite)
		}
		if got[0].HttpOnly {
			t.Error("HttpOnly = true; want false (JS layer must read this cookie)")
		}
	})
}

// TestRULE_WEB_BODY_SIZE_CAP_1MIB pins the body-size cap on every
// authed state-changing route. The cap is 1 MiB (defaultMaxBody);
// oversized payloads MUST surface as 413 Request Entity Too Large
// when handlers attempt to read or decode the body.
//
// Bound rule: RULE-WEB-BODY-SIZE-CAP-1MIB in .claude/rules/web-ui.md.
func TestRULE_WEB_BODY_SIZE_CAP_1MIB(t *testing.T) {
	t.Run("oversized_post_to_authed_route_returns_413", func(t *testing.T) {
		// PUT /api/v1/config with a 2 MiB body MUST return 413,
		// not 400 (would mean we read past the cap before the
		// JSON decoder noticed). The middleware applies
		// http.MaxBytesReader; the handler's json.Decoder.Decode
		// surfaces the cap as MaxBytesError → handler emits 413.
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		csrf, _ := srv.sessions.csrfFor(tok)
		// 2 MiB filler — well over the 1 MiB cap.
		filler := strings.Repeat("A", (1<<20)+1024)
		body := `{"pad":"` + filler + `"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		req.Header.Set("X-CSRF-Token", csrf)
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d; want 413 (body cap of 1 MiB must fire on 2 MiB payload)", rr.Code)
		}
	})

	t.Run("undersized_post_passes_body_cap", func(t *testing.T) {
		// A small body MUST clear the cap and reach the handler.
		// The handler may reject for other reasons (validation),
		// but it MUST NOT be a 413.
		srv := newCSRFServer(t)
		tok, err := srv.sessions.create()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		csrf, _ := srv.sessions.csrfFor(tok)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(`{}`))
		req.Host = "ventd.local:9999"
		req.Header.Set("Origin", "http://ventd.local:9999")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		req.Header.Set("X-CSRF-Token", csrf)
		rr := httptest.NewRecorder()
		srv.handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusRequestEntityTooLarge {
			t.Errorf("status = 413 on small body; body cap must NOT fire on under-1 MiB payload")
		}
	})
}
