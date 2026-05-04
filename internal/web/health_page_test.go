package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthPage_EmbeddedAndRoutesRegistered asserts that the
// health.html / .css / .js triple is reachable through the
// registered /health + /health.css + /health.js routes — catches
// the next embed-list omission or missing registerWebPage call.
func TestHealthPage_EmbeddedAndRoutesRegistered(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, path := range []string{"/health", "/health.css", "/health.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status=%d, want 200", path, w.Code)
			continue
		}
		body := w.Body.String()
		if path == "/health" && !strings.Contains(body, "<title>Health") {
			t.Errorf("%s: body missing expected <title>", path)
		}
	}
}
