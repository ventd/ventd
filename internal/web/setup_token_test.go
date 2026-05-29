package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstBootPost drives POST /login with new_password from a given remote
// address, optionally carrying a setup token header. Returns the status.
func firstBootPost(t *testing.T, srv *Server, password, remoteAddr, token string) int {
	t.Helper()
	body := strings.NewReader("new_password=" + password)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	if token != "" {
		req.Header.Set(setupTokenHeader, token)
	}
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr.Code
}

// TestH1_FirstBootTokenGuard verifies the claim-window guard: loopback
// enrolment is tokenless, non-loopback enrolment needs the minted token,
// and the token is retired once a password is set.
func TestH1_FirstBootTokenGuard(t *testing.T) {
	// Redirect the setup-token file into the test temp dir so we never
	// touch the real /run/ventd, and restore the default afterwards.
	prev := setupTokenFile
	tokenFile := filepath.Join(t.TempDir(), "setup-token")
	setupTokenFile = tokenFile
	t.Cleanup(func() { setupTokenFile = prev })

	srv, _, cancel := newAuthHarness(t)
	defer cancel()

	// A token must have been minted at first boot.
	tok, active := srv.currentSetupToken()
	if !active || tok == "" {
		t.Fatal("expected a setup token to be minted on first boot")
	}
	if data, err := os.ReadFile(tokenFile); err != nil || strings.TrimSpace(string(data)) != tok {
		t.Fatalf("setup-token file = %q, err=%v; want %q", string(data), err, tok)
	}

	const lan = "192.168.7.55:40000"

	// Non-loopback, no token -> refused.
	if got := firstBootPost(t, srv, "correct horse battery", lan, ""); got != http.StatusForbidden {
		t.Errorf("LAN enrolment without token: status=%d want 403", got)
	}
	// Non-loopback, wrong token -> refused.
	if got := firstBootPost(t, srv, "correct horse battery", lan, "deadbeef"); got != http.StatusForbidden {
		t.Errorf("LAN enrolment with wrong token: status=%d want 403", got)
	}
	// Password must still be unset after refused attempts.
	if srv.authHashValue() != "" {
		t.Fatal("password was set despite refused enrolment")
	}

	// Non-loopback, correct token -> accepted.
	if got := firstBootPost(t, srv, "correct horse battery", lan, tok); got != http.StatusOK {
		t.Errorf("LAN enrolment with valid token: status=%d want 200", got)
	}
	if srv.authHashValue() == "" {
		t.Fatal("password not set after valid-token enrolment")
	}
	// Token retired: in-memory cleared and file removed.
	if _, stillActive := srv.currentSetupToken(); stillActive {
		t.Error("setup token still active after enrolment")
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("setup-token file still present after enrolment (err=%v)", err)
	}
}

// TestH1_LoopbackEnrolmentTokenless confirms the on-box case keeps working
// without a token (the deliberate UX from #765, preserved for localhost).
func TestH1_LoopbackEnrolmentTokenless(t *testing.T) {
	prev := setupTokenFile
	setupTokenFile = filepath.Join(t.TempDir(), "setup-token")
	t.Cleanup(func() { setupTokenFile = prev })

	srv, _, cancel := newAuthHarness(t)
	defer cancel()

	if got := firstBootPost(t, srv, "correct horse battery", "127.0.0.1:50000", ""); got != http.StatusOK {
		t.Errorf("loopback enrolment without token: status=%d want 200", got)
	}
}
