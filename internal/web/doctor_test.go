package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
)

// TestHandleDoctorReport_Smoke runs the handler against the harness
// once and asserts the response unmarshals to a doctor.Report with
// the expected schema_version + a non-zero generated timestamp + at
// least zero facts (some hosts may genuinely produce zero — that's
// allowed; the contract is "valid Report shape", not "non-empty").
func TestHandleDoctorReport_Smoke(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	w := httptest.NewRecorder()
	srv.handleDoctorReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.Schema == "" {
		t.Error("schema_version empty")
	}
	if report.Generated.IsZero() {
		t.Error("generated timestamp zero")
	}
	// Severity is a stringer enum — its String() should match one of
	// the canonical names the JS layer keys on.
	switch s := report.Severity.String(); s {
	case "ok", "info", "warning", "blocker", "error":
	default:
		t.Errorf("unexpected severity %q", s)
	}
}

// TestHandleDoctorReport_RejectsNonGET pins the API contract — Doctor
// is a pure read; PUT/POST must return 405 so a misconfigured client
// can't accidentally trigger something destructive.
func TestHandleDoctorReport_RejectsNonGET(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/doctor", nil)
		w := httptest.NewRecorder()
		srv.handleDoctorReport(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status=%d, want 405", method, w.Code)
		}
	}
}

// TestHandleDoctorReport_CachesWithinTTL verifies that two GETs
// within the doctorReportCacheTTL window return reports with the
// same Generated timestamp — the second hit must be served from
// the in-memory cache rather than re-running the detector pack.
func TestHandleDoctorReport_CachesWithinTTL(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	first := doGetDoctor(t, srv)
	if first.Generated.IsZero() {
		t.Fatal("first report had zero Generated stamp")
	}
	time.Sleep(50 * time.Millisecond)
	second := doGetDoctor(t, srv)
	if !second.Generated.Equal(first.Generated) {
		t.Errorf("Generated stamps differ inside cache window: first=%v second=%v",
			first.Generated, second.Generated)
	}
}

func doGetDoctor(t *testing.T, srv *Server) doctor.Report {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	w := httptest.NewRecorder()
	srv.handleDoctorReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	var r doctor.Report
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return r
}

// TestDoctorPage_EmbeddedAndRoutesRegistered asserts that the
// doctor.html / .css / .js triple is reachable through the registered
// /doctor + /doctor.css + /doctor.js routes — catches the next
// embed-list omission or missing registerWebPage call. Mirrors the
// implicit guarantee in TestUI_NoExternalCDN that every page resource
// resolves locally.
func TestDoctorPage_EmbeddedAndRoutesRegistered(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, path := range []string{"/doctor", "/doctor.css", "/doctor.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status=%d, want 200", path, w.Code)
			continue
		}
		body := w.Body.String()
		if path == "/doctor" && !strings.Contains(body, "<title>Doctor") {
			t.Errorf("%s: body missing expected <title>", path)
		}
	}
}
