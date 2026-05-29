package web

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// setupTokenFile is the root-only file the daemon writes the first-boot
// enrolment token to so the installer (scripts/install.sh) can surface it
// to the operator. Lives under /run (tmpfs, cleared on reboot) and is
// removed the moment a password is set. Overridable for tests.
var setupTokenFile = "/run/ventd/setup-token"

// setupTokenHeader / setupTokenField are the two ways a first-boot client
// may present the enrolment token: an HTTP header (API clients) or a form
// field (the wizard form post).
const (
	setupTokenHeader = "X-Ventd-Setup-Token"
	setupTokenField  = "setup_token"
)

// mintSetupToken generates the one-time first-boot enrolment token, stores
// it on the Server, logs it, and writes it to setupTokenFile (0600). Called
// from New() only when no admin password exists yet (first boot).
func (s *Server) mintSetupToken() {
	tok, err := randomHex(16)
	if err != nil {
		// Non-fatal: without a token, non-loopback enrolment is refused
		// outright (fail-closed) rather than allowed. Loopback still works.
		s.logger.Error("web: could not generate first-boot setup token", "err", err)
		return
	}
	s.setupToken.Store(&tok)

	if err := os.MkdirAll(filepath.Dir(setupTokenFile), 0o700); err == nil {
		// 0600: only root (the daemon user) reads it; the installer runs
		// as root. O_TRUNC so a stale token from a prior boot is replaced.
		if werr := os.WriteFile(setupTokenFile, []byte(tok+"\n"), 0o600); werr != nil {
			s.logger.Warn("web: could not write setup-token file", "path", setupTokenFile, "err", werr)
		}
	}

	s.logger.Warn("web: first-boot setup token minted — required to enrol from a non-loopback address",
		"token", tok,
		"file", setupTokenFile,
		"hint", "open the UI on this host (localhost) to enrol without it, or pass "+setupTokenHeader)
}

// clearSetupToken drops the in-memory token and removes the on-disk file.
// Called once an admin password has been set — the first-boot window is
// closed and the token must not linger.
func (s *Server) clearSetupToken() {
	empty := ""
	s.setupToken.Store(&empty)
	_ = os.Remove(setupTokenFile)
}

// currentSetupToken returns the active token and whether one is in effect.
func (s *Server) currentSetupToken() (string, bool) {
	if p := s.setupToken.Load(); p != nil && *p != "" {
		return *p, true
	}
	return "", false
}

// firstBootClientIsLoopback reports whether the first-boot enrolment request
// originates from a loopback address. When trusted proxies are configured,
// the resolved client IP (not the proxy peer) is evaluated, so a loopback
// reverse proxy forwarding for a LAN client is correctly treated as remote.
func (s *Server) firstBootClientIsLoopback(r *http.Request) bool {
	ip := net.ParseIP(resolveClientIP(r, s.trustedProxies))
	return ip != nil && ip.IsLoopback()
}

// firstBootTokenOK enforces the H1 claim-window guard. It reports whether a
// first-boot enrolment request is permitted to proceed:
//
//   - loopback client: always allowed (tokenless, the on-box / SSH-tunnel case)
//   - no token in effect: allowed (token generation failed; loopback-only is
//     then the de-facto policy — but a non-loopback caller with no token to
//     match still falls through to the compare below and is refused)
//   - non-loopback client: must present a token equal (constant-time) to the
//     active setup token
func (s *Server) firstBootTokenOK(r *http.Request) bool {
	if s.firstBootClientIsLoopback(r) {
		return true
	}
	want, active := s.currentSetupToken()
	if !active {
		// No token to match against and the caller is not loopback: refuse.
		return false
	}
	got := r.Header.Get(setupTokenHeader)
	if got == "" {
		got = r.FormValue(setupTokenField)
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
