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

// TestHandlerError_IsJSON confirms a converted handler's error path now emits
// the JSON envelope instead of text/plain — handleProfile rejecting a non-GET
// is a representative method-gate that used to call http.Error.
func TestHandlerError_IsJSON(t *testing.T) {
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(config.Empty())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(Deps{Ctx: ctx, Cfg: &cfgPtr, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", nil)
	rec := httptest.NewRecorder()
	s.handleProfile(rec, req)

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
