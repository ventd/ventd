package web

import (
	"context"
	"encoding/json"
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

// newTestServer builds a minimal authenticated Server and returns it plus a
// session token. Shared between the install/mok-enroll endpoint tests.
func newTestServer(t *testing.T) (*Server, string) {
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
	srv := New(ctx, &liveCfg, "", "", logger, cal, sm, restartCh, diag)
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
		"/api/hwdiag/modprobe-options-write",
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

func TestModprobeOptionsWrite_AllowlistEnforced(t *testing.T) {
	srv, tok := newTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"unknown module", `{"module":"rogue","options":"fan_control=1"}`},
		{"empty body", ``},
		{"empty fields", `{"module":"","options":""}`},
		{"thinkpad with shell injection in options", `{"module":"thinkpad_acpi","options":"fan_control=1;rm -rf /"}`},
		{"thinkpad with disallowed value", `{"module":"thinkpad_acpi","options":"fan_control=0"}`},
		{"it87 not yet allowed", `{"module":"it87","options":"ignore_resource_conflict=1"}`},
		{"malformed JSON", `{"module":"thinkpad_acpi"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body == "" {
				req = httptest.NewRequest("POST", "/api/hwdiag/modprobe-options-write", nil)
			} else {
				req = httptest.NewRequest("POST", "/api/hwdiag/modprobe-options-write", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status=%d body=%s; want 400", rr.Code, rr.Body.String())
			}
		})
	}
}
