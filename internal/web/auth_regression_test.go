package web

// Regression tests for issue #463: admin password not persisted across daemon restart.
//
// The root cause was that any config.yaml write path (handleConfigPut,
// handleSetupApply, schedule apply) could overwrite the file without preserving
// the admin hash stored in the same file. The fix moves credentials to a
// dedicated auth.json whose write path is independent of the config write path.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/web/authpersist"
)

// newAuthHarness sets up a Server backed by real temp-dir files so tests can
// exercise auth.json write/read cycles.
func newAuthHarness(t *testing.T) (srv *Server, dir string, cancel context.CancelFunc) {
	t.Helper()
	dir = t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	authPath := authpersist.DefaultPath(dir)

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(filepath.Join(dir, "cal.json"), logger, nil)
	sm := setupmgr.New(cal, logger)
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(config.Empty())
	restart := make(chan struct{}, 1)
	ctx, cancelFn := context.WithCancel(context.Background())

	srv = New(ctx, &cfgPtr, configPath, authPath, logger, cal, sm, restart, "tok123", hwdiag.NewStore())
	return srv, dir, cancelFn
}

// loginWith POSTs the given password to /login and returns the HTTP status.
func loginWith(t *testing.T, srv *Server, password string) int {
	t.Helper()
	body := strings.NewReader("password=" + password)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:50000"
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr.Code
}

// firstBootLogin exercises the setup-token + new-password flow.
func firstBootLogin(t *testing.T, srv *Server, token, password string) int {
	t.Helper()
	body := strings.NewReader("setup_token=" + token + "&new_password=" + password)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:50000"
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr.Code
}

// TestRegression_Issue463_PasswordSurvivesRestart verifies that after the
// first-boot wizard sets a password, the credentials survive a simulated daemon
// restart (a new Server reading the same disk state).
func TestRegression_Issue463_PasswordSurvivesRestart(t *testing.T) {
	srv, dir, cancel := newAuthHarness(t)
	defer cancel()

	const password = "CorrectHorseBattery1"
	if got := firstBootLogin(t, srv, "tok123", password); got != http.StatusOK {
		t.Fatalf("first-boot login: status=%d want 200", got)
	}

	// Confirm auth.json was written.
	authPath := authpersist.DefaultPath(dir)
	auth, err := authpersist.Load(authPath)
	if err != nil || auth == nil {
		t.Fatalf("auth.json not written after first-boot login: %v", err)
	}
	if auth.Admin.BcryptHash == "" {
		t.Fatal("auth.json: BcryptHash is empty")
	}

	// Simulate restart: construct a brand-new Server from the same disk state.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal2 := calibrate.New(filepath.Join(dir, "cal2.json"), logger, nil)
	sm2 := setupmgr.New(cal2, logger)
	var cfgPtr2 atomic.Pointer[config.Config]
	cfgPtr2.Store(config.Empty())
	restart2 := make(chan struct{}, 1)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	srv2 := New(ctx2, &cfgPtr2, filepath.Join(dir, "config.yaml"), authPath,
		logger, cal2, sm2, restart2, "", hwdiag.NewStore())

	// Login with the original password must succeed on the new server.
	if got := loginWith(t, srv2, password); got != http.StatusOK {
		t.Fatalf("post-restart login: status=%d want 200", got)
	}
}

// TestRegression_Issue463_CalibrationSaveDoesNotWipeAuth confirms that a
// PUT /api/config (simulating a config save from the web UI) does not clear
// the admin hash from auth.json or from the in-memory state.
func TestRegression_Issue463_CalibrationSaveDoesNotWipeAuth(t *testing.T) {
	srv, dir, cancel := newAuthHarness(t)
	defer cancel()

	const password = "StablePassword99"
	if got := firstBootLogin(t, srv, "tok123", password); got != http.StatusOK {
		t.Fatalf("first-boot login: status=%d want 200", got)
	}

	// Issue a session token so we can hit auth-gated endpoints.
	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Simulate a config save: PUT /api/config with a fan config but NO password_hash.
	fanCfg := config.Empty()
	body, _ := json.Marshal(fanCfg)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /api/config: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Auth hash must survive the config write.
	authPath := authpersist.DefaultPath(dir)
	auth, err := authpersist.Load(authPath)
	if err != nil || auth == nil {
		t.Fatalf("auth.json missing after config write: %v", err)
	}
	if auth.Admin.BcryptHash == "" {
		t.Fatal("auth.json: BcryptHash cleared by config write")
	}

	// Login must still work.
	if got := loginWith(t, srv, password); got != http.StatusOK {
		t.Fatalf("login after config write: status=%d want 200", got)
	}
}

// TestRegression_Issue463_StartupDetectsMissingAuth verifies that a Server
// created with an authPath that has no corresponding auth.json (and no hash in
// the config) reports first-boot mode. This models the integrity-check path
// that prevents a locked-out operator from being silently stuck.
func TestRegression_Issue463_StartupDetectsMissingAuth(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	authPath := authpersist.DefaultPath(dir) // file does not exist

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(filepath.Join(dir, "cal.json"), logger, nil)
	sm := setupmgr.New(cal, logger)
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(config.Empty()) // no PasswordHash in config
	restart := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New(ctx, &cfgPtr, configPath, authPath, logger, cal, sm, restart, "", hwdiag.NewStore())

	// With no auth.json and no hash in config, authHashValue must be empty.
	if srv.authHashValue() != "" {
		t.Errorf("authHashValue = %q, want empty", srv.authHashValue())
	}

	// /api/auth/state must report first_boot: true.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/state", nil)
	req.Host = "ventd.local:9999"
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"first_boot":true`) {
		t.Errorf("auth/state body=%q want first_boot:true", rr.Body.String())
	}
}

// TestRegression_Issue463_AtomicAuthWrite verifies that authpersist.Save writes
// the file atomically: no .tmp file is left behind, the .bak is present after
// a second write, and the data is immediately loadable.
func TestRegression_Issue463_AtomicAuthWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// First write.
	a1 := &authpersist.Auth{
		Admin: authpersist.AdminCreds{Username: "admin", BcryptHash: "$2a$12$first"},
	}
	if err := authpersist.Save(path, a1); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file leaked after first Save")
	}

	// Second write: .bak must now exist with the first hash.
	a2 := &authpersist.Auth{
		Admin: authpersist.AdminCreds{Username: "admin", BcryptHash: "$2a$12$second"},
	}
	if err := authpersist.Save(path, a2); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file leaked after second Save")
	}
	bak, err := authpersist.Load(path + ".bak")
	if err != nil || bak == nil {
		t.Fatalf(".bak not loadable: %v", err)
	}
	if bak.Admin.BcryptHash != "$2a$12$first" {
		t.Errorf(".bak hash = %q, want $2a$12$first", bak.Admin.BcryptHash)
	}

	// The live file must have the second hash.
	live, err := authpersist.Load(path)
	if err != nil || live == nil {
		t.Fatalf("Load after second Save: %v", err)
	}
	if live.Admin.BcryptHash != "$2a$12$second" {
		t.Errorf("live hash = %q, want $2a$12$second", live.Admin.BcryptHash)
	}
}

// changePasswordViaAPI calls POST /api/set-password with current and new password.
func changePasswordViaAPI(t *testing.T, srv *Server, tok, current, newPW string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"current": current, "new": newPW})
	req := httptest.NewRequest(http.MethodPost, "/api/set-password", bytes.NewReader(body))
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr.Code
}

// PropAuthSurvivesConfigWrite exercises the property that after any sequence
// of {setPassword, writeConfig×3} the last-set admin hash is always recoverable
// from auth.json and login still works.
func PropAuthSurvivesConfigWrite(t *testing.T) {
	srv, dir, cancel := newAuthHarness(t)
	defer cancel()

	// Set the initial password via first-boot flow.
	const initial = "InitialPassword99"
	if got := firstBootLogin(t, srv, "tok123", initial); got != http.StatusOK {
		t.Fatalf("initial first-boot login: status=%d", got)
	}

	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	authPath := authpersist.DefaultPath(dir)
	sequences := []struct{ oldPW, newPW string }{
		{initial, "AlphaPassword1!"},
		{"AlphaPassword1!", "BetaPassword2@"},
		{"BetaPassword2@", "GammaPassword3#"},
	}

	for _, seq := range sequences {
		// Change password via /api/set-password.
		if got := changePasswordViaAPI(t, srv, tok, seq.oldPW, seq.newPW); got != http.StatusOK {
			t.Fatalf("change pw to %q: status=%d", seq.newPW, got)
		}

		// Run several config writes.
		for range 3 {
			cfgBody := config.Empty()
			cfgJSON, _ := json.Marshal(cfgBody)
			req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(cfgJSON))
			req.Host = "ventd.local:9999"
			req.Header.Set("Origin", "http://ventd.local:9999")
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			rr := httptest.NewRecorder()
			srv.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("PUT /api/config: status=%d body=%s", rr.Code, rr.Body.String())
			}
		}

		// auth.json must be readable and contain the latest hash.
		auth, loadErr := authpersist.Load(authPath)
		if loadErr != nil || auth == nil {
			t.Fatalf("after pw=%q: auth.json not loadable: %v", seq.newPW, loadErr)
		}
		if auth.Admin.BcryptHash == "" {
			t.Fatalf("after pw=%q: auth.json has empty hash", seq.newPW)
		}

		// Login with the current (new) password must succeed.
		if got := loginWith(t, srv, seq.newPW); got != http.StatusOK {
			t.Fatalf("login with pw=%q after config writes: status=%d", seq.newPW, got)
		}
	}
}

// TestPropAuthSurvivesConfigWrite is the test entry-point for the property above.
func TestPropAuthSurvivesConfigWrite(t *testing.T) {
	PropAuthSurvivesConfigWrite(t)
}
