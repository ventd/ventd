package web

import (
	"net/http"
	"testing"
)

// authAndCSRF adds the session cookie + X-CSRF-Token header to req
// so it passes both requireAuth and requireCSRF middleware. Helper
// for tests that POST through srv.mux and need to clear the gates
// introduced in v0.5.31 (RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE).
//
// Session token is supplied by the caller (typically returned from
// `srv.sessions.create()` in newTestServer or equivalent fixture).
// The CSRF token is looked up from the session store; absence is a
// test setup error and t.Fatalf's loudly so a regression that breaks
// CSRF token issuance is caught immediately.
func authAndCSRF(t *testing.T, req *http.Request, srv *Server, sessionTok string) {
	t.Helper()
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionTok})
	csrf, ok := srv.sessions.csrfFor(sessionTok)
	if !ok {
		t.Fatalf("authAndCSRF: no CSRF token for session %q (was sessions.create() called?)", sessionTok)
	}
	req.Header.Set("X-CSRF-Token", csrf)
}
