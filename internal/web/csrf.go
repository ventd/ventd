package web

import (
	"crypto/subtle"
	"net/http"
	"time"
)

// csrfCookie is the name of the non-HttpOnly cookie that carries the
// CSRF token. The JS layer reads this cookie via `document.cookie`
// and echoes the value in the `X-CSRF-Token` header on every state-
// changing fetch (handled by the fetch monkey-patch in
// web/shared/brand.js).
//
// Distinct from sessionCookie ("ventd_session") which is HttpOnly so
// the JS layer cannot read it. The CSRF cookie is intentionally NOT
// HttpOnly — that's the read-side of the synchroniser-token pattern.
// What protects against forgery is the server-side compare: middleware
// looks up the session's bound CSRF token (sessionStore.csrfFor) and
// constant-time compares against the X-CSRF-Token header, NOT against
// the cookie. A malicious site cannot read the cookie (cookies are
// SOP-isolated) and cannot construct an X-CSRF-Token header that
// matches a victim's per-session token without first reading it.
const csrfCookie = "ventd_csrf"

// setCSRFCookie writes the CSRF cookie alongside the session cookie.
// Same TTL + Secure flags as the session cookie so the pair are
// rotated atomically.
//
// HttpOnly is FALSE (the JS layer must read the value). SameSite is
// Strict to match the session cookie's posture under v0.5.31.
func setCSRFCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCSRFCookie removes the CSRF cookie (paired with logout's
// clearSessionCookie). Called on logout so the JS layer's cached
// token becomes invalid in the same beat as the session.
func clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
	})
}

// requireCSRF wraps h so that state-changing requests must carry a
// valid `X-CSRF-Token` header matching the session's bound CSRF token.
//
// Safe methods (GET / HEAD / OPTIONS) bypass the check — they don't
// mutate state and shouldn't be subject to CSRF on the server side.
// (The JS-side fetch wrapper still injects the header on every
// authenticated state-changing fetch; that's a defence-in-depth, not
// the load-bearing check.)
//
// Unsafe methods (POST / PUT / PATCH / DELETE) require:
//
//  1. A valid session cookie (looked up in sessionStore). Without an
//     authenticated session there's nothing for CSRF to bind to —
//     returns 401. (`requireAuth` typically wraps the same handler
//     and would catch this first; the duplicate check in CSRF
//     middleware is defensive.)
//  2. An `X-CSRF-Token` header. Missing → 403.
//  3. The header value MUST equal `session.csrfToken` under
//     `subtle.ConstantTimeCompare`. Mismatch → 403.
//
// Tests construct authenticated POST requests by setting both the
// session cookie AND the X-CSRF-Token header (helpers in
// hwdiag_install_test.go's newTestServer return both).
func (s *Server) requireCSRF(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			h(w, r)
			return
		}

		sessTok := sessionToken(r)
		want, ok := s.sessions.csrfFor(sessTok)
		if !ok {
			http.Error(w, "session not found or expired", http.StatusUnauthorized)
			return
		}

		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			http.Error(w, "X-CSRF-Token header required", http.StatusForbidden)
			return
		}

		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
			return
		}

		h(w, r)
	}
}

// requireMaxBody wraps h so that POST / PUT / PATCH / DELETE bodies
// are capped at max bytes. Bodies above the cap surface as
// http.MaxBytesError on the next read; handlers that decode JSON or
// parse forms see the standard 413-on-overflow handshake.
//
// The 1 MiB default (defaultMaxBody in security.go) dwarfs any
// realistic state-change payload (config push, password set, version
// string) and still blocks trivial OOM attempts on an unauthenticated
// upload.
func requireMaxBody(max int64, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		h(w, r)
	}
}
