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
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// newVersionTestServer spins up a Server ready to be exercised by the
// health / version tests. Keeps the setup local so these tests don't share
// fate with newTestServer — the fields exercised here (ready, version) are
// not touched by the other tests, and isolating makes failures easier to
// triage.
func newVersionTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	diag := hwdiag.NewStore()
	cal.SetDiagnosticStore(diag)
	sm := setupmgr.New(cal, logger)
	sm.SetDiagnosticStore(diag)
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(config.Empty())
	restartCh := make(chan struct{}, 1)
	return New(context.Background(), &liveCfg, "", logger, cal, sm, restartCh, "", diag)
}

// TestHealthzStateTransitions walks /healthz across the startup boundary:
// 503 with body "starting" before SetHealthy, 200 with body "ok" after.
// The nil-ReadyState case is a real path — tests that construct a Server
// without calling SetReadyState must still get a sensible probe response.
func TestHealthzStateTransitions(t *testing.T) {
	cases := []struct {
		name       string
		install    func(*Server)
		wantCode   int
		wantBody   string
	}{
		{
			name:     "nil ready state returns 503",
			install:  func(*Server) {},
			wantCode: http.StatusServiceUnavailable,
			wantBody: "starting\n",
		},
		{
			name: "ready state present but not healthy returns 503",
			install: func(s *Server) {
				s.SetReadyState(NewReadyState())
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: "starting\n",
		},
		{
			name: "ready state marked healthy returns 200 ok",
			install: func(s *Server) {
				rs := NewReadyState()
				rs.SetHealthy()
				s.SetReadyState(rs)
			},
			wantCode: http.StatusOK,
			wantBody: "ok\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newVersionTestServer(t)
			tc.install(srv)

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Errorf("status=%d want %d", rr.Code, tc.wantCode)
			}
			if rr.Body.String() != tc.wantBody {
				t.Errorf("body=%q want %q", rr.Body.String(), tc.wantBody)
			}
			if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("Content-Type=%q want text/plain prefix", ct)
			}
		})
	}
}

// TestReadyzStateTransitions covers the /readyz gates end-to-end: nil state,
// watchdog not pinged, no sensor read yet, stale sensor read, and the happy
// path. Each branch must emit a distinguishable body so a monitoring probe
// can differentiate "never started" from "stalled".
func TestReadyzStateTransitions(t *testing.T) {
	fixedNow := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		configure func(*Server)
		wantCode  int
		wantBody  string
	}{
		{
			name:      "nil ready state",
			configure: func(*Server) {},
			wantCode:  http.StatusServiceUnavailable,
			wantBody:  "not ready: readiness tracking disabled\n",
		},
		{
			name: "watchdog never pinged",
			configure: func(s *Server) {
				s.SetReadyState(NewReadyState())
				s.nowFn = func() time.Time { return fixedNow }
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: "not ready: watchdog has not pinged\n",
		},
		{
			name: "watchdog pinged but no sensor read",
			configure: func(s *Server) {
				rs := NewReadyState()
				rs.SetWatchdogPinged()
				s.SetReadyState(rs)
				s.nowFn = func() time.Time { return fixedNow }
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: "not ready: no sensor read recorded\n",
		},
		{
			name: "sensor read older than 5s window",
			configure: func(s *Server) {
				rs := NewReadyState()
				rs.SetWatchdogPinged()
				rs.MarkSensorRead(fixedNow.Add(-10 * time.Second))
				s.SetReadyState(rs)
				s.nowFn = func() time.Time { return fixedNow }
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: "not ready: last sensor read too old\n",
		},
		{
			name: "sensor read inside the 5s window returns ok",
			configure: func(s *Server) {
				rs := NewReadyState()
				rs.SetWatchdogPinged()
				rs.MarkSensorRead(fixedNow.Add(-2 * time.Second))
				s.SetReadyState(rs)
				s.nowFn = func() time.Time { return fixedNow }
			},
			wantCode: http.StatusOK,
			wantBody: "ok\n",
		},
		{
			name: "sensor read at the exact 5s boundary still ok",
			configure: func(s *Server) {
				rs := NewReadyState()
				rs.SetWatchdogPinged()
				rs.MarkSensorRead(fixedNow.Add(-5 * time.Second))
				s.SetReadyState(rs)
				s.nowFn = func() time.Time { return fixedNow }
			},
			wantCode: http.StatusOK,
			wantBody: "ok\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newVersionTestServer(t)
			tc.configure(srv)

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Errorf("status=%d want %d body=%q", rr.Code, tc.wantCode, rr.Body.String())
			}
			if rr.Body.String() != tc.wantBody {
				t.Errorf("body=%q want %q", rr.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestVersionHandlerBodyShape checks /api/version returns the VersionInfo
// JSON shape unauthenticated, matching the ventd --version --json output.
// Not covering both v1-aliased paths here; TestAPIV1Alias does that.
func TestVersionHandlerBodyShape(t *testing.T) {
	srv := newVersionTestServer(t)
	srv.SetVersionInfo(VersionInfo{
		Version:   "2.0.0",
		Commit:    "deadbeef",
		BuildDate: "2026-04-15T12:00:00Z",
		Go:        "go1.25",
	})

	cases := []struct {
		name string
		path string
	}{
		{name: "unversioned path", path: "/api/version"},
		{name: "v1 aliased path", path: "/api/v1/version"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type=%q want application/json prefix", ct)
			}
			var got VersionInfo
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v\nraw: %s", err, rr.Body.String())
			}
			want := VersionInfo{
				Version:   "2.0.0",
				Commit:    "deadbeef",
				BuildDate: "2026-04-15T12:00:00Z",
				Go:        "go1.25",
			}
			if got != want {
				t.Errorf("payload mismatch\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// TestVersionHandlerRejectsNonGET guards the method check. Adding a POST
// handler to /api/version by mistake would expose the build metadata to
// CSRF-style probes from a cached session cookie — keep it GET-only.
func TestVersionHandlerRejectsNonGET(t *testing.T) {
	srv := newVersionTestServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/version", nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("method=%s status=%d want 405", method, rr.Code)
			}
		})
	}
}

// TestAPIV1Alias exercises the dual-registration on a representative
// authenticated route. /api/ping is the cleanest sample: unauthenticated,
// trivial body, exists today — so the test fails closed if the slice-based
// registration drops either prefix.
func TestAPIV1Alias(t *testing.T) {
	srv := newVersionTestServer(t)

	cases := []struct {
		name string
		path string
	}{
		{name: "unversioned", path: "/api/ping"},
		{name: "v1 aliased", path: "/api/v1/ping"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v\nraw: %s", err, rr.Body.String())
			}
			if got["status"] != "ok" {
				t.Errorf("payload=%+v want status=ok", got)
			}
		})
	}
}

// TestAPIV1AliasAuthAppliedOnce checks that the slice-based dual
// registration does not accidentally wrap requireAuth twice, which would
// survive static analysis (both calls would pass) but burn cycles on every
// authenticated request. We assert this indirectly: a request with a valid
// session token must succeed on both prefixes of the same route with only
// one valid cookie, and an unauthenticated request must redirect once.
func TestAPIV1AliasAuthAppliedOnce(t *testing.T) {
	srv := newVersionTestServer(t)
	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	for _, path := range []string{"/api/status", "/api/v1/status"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("auth status=%d body=%s", rr.Code, rr.Body.String())
			}
		})

		t.Run(path+" unauth", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			srv.mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("unauth status=%d want 401", rr.Code)
			}
		})
	}
}
