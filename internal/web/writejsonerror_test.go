package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestWriteJSONError pins the uniform error envelope introduced with R8: a
// JSON body {"error": msg} with Content-Type application/json and the given
// status, matching the already-JSON 401/404 responses and what the frontend
// parses.
func TestWriteJSONError(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()

	s.writeJSONError(rec, http.StatusBadRequest, "invalid JSON body")

	res := rec.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "invalid JSON body" {
		t.Errorf("body = %+v, want error=%q", body, "invalid JSON body")
	}
}

// TestGateMethods covers the declarative method gate that replaced the
// per-handler `if r.Method != … { … }` blocks: an allowed method passes
// through to the wrapped handler; any other is rejected with a JSON 405
// without the handler running.
func TestGateMethods(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	called := false
	inner := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }
	gated := s.gateMethods([]string{http.MethodGet, http.MethodPut}, inner)

	// Allowed method reaches the handler.
	rec := httptest.NewRecorder()
	gated(rec, httptest.NewRequest(http.MethodPut, "/x", nil))
	if !called || rec.Code != http.StatusOK {
		t.Errorf("allowed method: called=%v status=%d, want true/200", called, rec.Code)
	}

	// Disallowed method is rejected before the handler runs.
	called = false
	rec = httptest.NewRecorder()
	gated(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if called {
		t.Error("disallowed method reached the handler")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if ct := rec.Result().Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestHandlerError_IsJSON confirms the method-gate error path emits the JSON
// envelope instead of text/plain — the declarative gate rejecting a non-GET on
// the GET-only profile route is representative of every gated apiRoute.
func TestHandlerError_IsJSON(t *testing.T) {
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(config.Empty())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(Deps{Ctx: ctx, Cfg: &cfgPtr, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", nil)
	rec := httptest.NewRecorder()
	s.gateMethods([]string{http.MethodGet}, s.handleProfile)(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (was text/plain before R8)", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "method not allowed" {
		t.Errorf("body = %+v, want error=%q", body, "method not allowed")
	}
}
