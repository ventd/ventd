package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// newTestServer builds a minimal authenticated Server and returns it plus a
// session token. Shared between the install/mok-enroll endpoint tests.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	diag := hwdiag.NewStore()
	cal.SetDiagnosticStore(diag)
	sm := setupmgr.New(cal, logger)
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(config.Empty())
	restartCh := make(chan struct{}, 1)
	srv := New(&liveCfg, "", logger, cal, sm, restartCh, "", diag)
	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return srv, tok
}

func TestMOKEnrollReturnsInstructions(t *testing.T) {
	srv, tok := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/hwdiag/mok-enroll", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Kind     string   `json:"kind"`
		Commands []string `json:"commands"`
		Detail   string   `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "instructions" {
		t.Errorf("kind=%q want instructions", resp.Kind)
	}
	if len(resp.Commands) == 0 {
		t.Errorf("expected non-empty commands")
	}
	if resp.Detail == "" {
		t.Errorf("expected non-empty detail")
	}
}

func TestInstallEndpointsRejectGET(t *testing.T) {
	srv, tok := newTestServer(t)
	for _, path := range []string{
		"/api/hwdiag/install-kernel-headers",
		"/api/hwdiag/install-dkms",
		"/api/hwdiag/mok-enroll",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status=%d want 405", path, rr.Code)
		}
	}
}
