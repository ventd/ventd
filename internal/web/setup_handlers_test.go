package web

import (
	"bytes"
	"context"
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
)

// Tests for #133 — setup wizard state-machine invariants and the system
// reboot handler.
//
// These exercise the handlers directly (bypassing the auth middleware
// that wraps them in s.registerAPIRoutes). The auth layer has its own
// coverage in security_test.go; pinning the state-machine behaviour
// below the auth boundary means a regression in the state machine
// surfaces even if the session wrapper changes.
//
// Five wizard invariants pinned:
//
//  1. Non-POST to any wizard handler returns 405, not 500.
//  2. POST /api/setup/apply on a fresh manager returns 409 "setup not
//     complete" — no partial config write, no 500.
//  3. POST /api/setup/start while already running (or already done)
//     returns 409; the state machine only runs once per daemon
//     lifetime, and a retry without a daemon restart must be rejected.
//  4. POST /api/setup/reset removes the config file and returns 200.
//  5. POST /api/setup/reset on a missing file is idempotent (200, no
//     500) — matches os.IsNotExist tolerance in the handler.
//
// The reboot handler test (TestHandleSystemReboot_CurrentBehaviour_*)
// pins CURRENT behaviour: /api/system/reboot returns 200 + "rebooting"
// and schedules a 300 ms-delayed reboot in a ctx-cancellable goroutine.
// A PID-1 refusal guard is NOT in scope for #133 — that work is
// tracked by #177, which will flip this test's assertion when the
// guard lands.

// newHandlerHarness spins up a minimal *Server suitable for direct
// handler calls: real setup.Manager, real calibrate.Manager, real
// config atomic pointer, no httptest.Server, no browser. Returns the
// server, the configPath it's been wired to, and a cancel func the
// caller must defer. Cancelling the returned cancel also drops any
// goroutine handleSystemReboot kicks off before its 300 ms timer.
func newHandlerHarness(t *testing.T) (srv *Server, configPath string, cancel context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	tempDir := t.TempDir()
	configPath = filepath.Join(tempDir, "config.yaml")
	calPath := filepath.Join(tempDir, "cal.json")

	live := config.Empty()
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)

	ctx, cancel := context.WithCancel(context.Background())
	cal := calibrate.New(calPath, logger, nil)
	sm := setupmgr.New(cal, logger)
	restart := make(chan struct{}, 1)
	srv = New(ctx, &cfgPtr, configPath, logger, cal, sm, restart, "", hwdiag.NewStore())

	return srv, configPath, cancel
}

// TestHandleSetupStart_NonPOST_RejectedAs405 — a stray GET from a URL
// opened in a browser tab must not accidentally kick the wizard.
func TestHandleSetupStart_NonPOST_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/start", nil)
	w := httptest.NewRecorder()
	srv.handleSetupStart(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/setup/start: status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

// TestHandleSetupApply_BeforeStart_Returns409 — "apply" before "start"
// is a broken state-machine traversal, not a server error. The user-
// facing body must name the condition (per usability.md).
func TestHandleSetupApply_BeforeStart_Returns409(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", nil)
	w := httptest.NewRecorder()
	srv.handleSetupApply(w, req)

	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("apply before start: status = %d, want %d", got, http.StatusConflict)
	}
	if body := w.Body.String(); !strings.Contains(body, "setup not complete") {
		t.Fatalf("apply before start: body = %q, want substring %q", body, "setup not complete")
	}
}

// TestHandleSetupApply_NonPOST_RejectedAs405 pins method enforcement on
// apply. A GET on this URL in a shared browser would be disastrous.
func TestHandleSetupApply_NonPOST_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/apply", nil)
	w := httptest.NewRecorder()
	srv.handleSetupApply(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/setup/apply: status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

// TestHandleSetupStart_Reentry_Returns409 — a second Start call while
// the first is still running (or has since failed) must refuse. The
// setup.Manager's state machine is once-per-lifetime; restart of the
// daemon is the only way to arm it again.
//
// Race note: on a sandbox with no /sys/class/hwmon, run() completes
// quickly with a "no fans" error. The second Start may observe either
// `running=true` (goroutine mid-flight) or `done=true` (errored out).
// Both return 409 via distinct error strings; the assertion only
// checks the status code, not the specific string.
func TestHandleSetupStart_Reentry_Returns409(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req1 := httptest.NewRequest(http.MethodPost, "/api/setup/start", nil)
	w1 := httptest.NewRecorder()
	srv.handleSetupStart(w1, req1)
	if got := w1.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("first Start: status = %d, want %d (body=%q)", got, http.StatusOK, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/setup/start", nil)
	w2 := httptest.NewRecorder()
	srv.handleSetupStart(w2, req2)
	if got := w2.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("re-entrant Start: status = %d, want %d (body=%q)", got, http.StatusConflict, w2.Body.String())
	}
}

// TestHandleSetupReset_NonPOST_RejectedAs405 — reset is destructive
// (deletes the on-disk config). Method enforcement is the cheapest
// guard against a stray cross-origin GET ever triggering it.
func TestHandleSetupReset_NonPOST_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/reset", nil)
	w := httptest.NewRecorder()
	srv.handleSetupReset(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/setup/reset: status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

// TestHandleSetupReset_RemovesConfigAndReturns200 — the happy-path
// reset. If there's a config on disk, reset removes it and returns
// 200. The restart trigger is non-blocking (restartCh has cap 1), so
// the handler still returns promptly regardless of whether anything
// drains the channel.
func TestHandleSetupReset_RemovesConfigAndReturns200(t *testing.T) {
	srv, configPath, cancel := newHandlerHarness(t)
	defer cancel()

	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/reset", nil)
	w := httptest.NewRecorder()
	srv.handleSetupReset(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("reset: status = %d, want %d (body=%q)", got, http.StatusOK, w.Body.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("reset: config file not removed: stat err = %v", err)
	}
}

// TestHandleSetupReset_MissingFile_Idempotent — a second reset on top
// of an already-gone config must return 200, not 500. Pins
// os.IsNotExist tolerance in the handler.
func TestHandleSetupReset_MissingFile_Idempotent(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/setup/reset", nil)
	w := httptest.NewRecorder()
	srv.handleSetupReset(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("reset-no-file: status = %d, want %d (body=%q)", got, http.StatusOK, w.Body.String())
	}
}

// TestHandleSystemReboot_NonPOST_RejectedAs405 — /api/system/reboot is
// the single most destructive URL in the repo. Method enforcement is
// non-negotiable.
func TestHandleSystemReboot_NonPOST_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.handleSystemReboot(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/system/reboot: status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

// TestHandleSystemReboot_CurrentBehaviour_Returns200 — PINNED CURRENT
// BEHAVIOUR. The reboot handler unconditionally returns 200 + a
// "rebooting" JSON body and schedules a 300 ms-delayed systemctl
// reboot command in a ctx-cancellable goroutine. The deferred cancel()
// below fires at test exit (well under 300 ms), so the goroutine
// observes ctx.Done() before the timer elapses — no real reboot is
// ever spawned in CI.
//
// This is a DELIBERATE pinned-current-behaviour anchor. #177 tracks
// the PID-1 refusal guard: refuse with 409 when ventd is PID 1 /
// /.dockerenv exists / systemd-detect-virt --container returns
// non-none. When that guard lands it will:
//   - flip this test's assertion from "expect 200" to "expect 409",
//   - add a mock-env companion test per clause,
//   - and allow this TODO to be removed.
//
// TODO(#177): flip assertion and remove this TODO once the PID-1 guard
// lands in handleSystemReboot at internal/web/server.go:981.
func TestHandleSystemReboot_CurrentBehaviour_Returns200(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.handleSystemReboot(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("reboot: status = %d, want %d (body=%q)", got, http.StatusOK, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "rebooting") {
		t.Fatalf("reboot: body = %q, want substring %q", body, "rebooting")
	}
}
