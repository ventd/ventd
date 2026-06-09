package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
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
		srv.gateMethods([]string{http.MethodGet}, srv.handleDoctorReport)(w, req)
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

// TestDoctorReportWithRemediation binds RULE-WEB-DOCTOR-FIX-THIS: each fact in
// the doctor response carries the actionable remediation for its failure class
// (recovery.RemediationFor), so the Doctor page can offer "Apply fix" / "Learn
// more" — while the report's other fields are preserved unchanged.
func TestDoctorReportWithRemediation(t *testing.T) {
	t.Parallel()
	report := doctor.Report{
		Schema:    "1",
		Generated: time.Unix(1700000000, 0).UTC(),
		Severity:  doctor.SeverityBlocker,
		Facts: []doctor.Fact{
			{Detector: "dkms_status", Class: recovery.ClassMissingHeaders, Title: "headers", Severity: doctor.SeverityBlocker},
			{Detector: "misc", Class: recovery.ClassUnknown, Title: "unknown thing", Severity: doctor.SeverityWarning},
		},
	}

	view := doctorReportWithRemediation(report)

	// Report-level fields preserved.
	if view.Schema != "1" || !view.Generated.Equal(report.Generated) || view.Severity != doctor.SeverityBlocker {
		t.Fatalf("report fields not preserved: %+v", view)
	}
	if len(view.Facts) != 2 {
		t.Fatalf("got %d facts, want 2", len(view.Facts))
	}

	// Fact 0 (missing headers) carries the real backed action.
	var hasInstallHeaders bool
	for _, r := range view.Facts[0].Remediation {
		if r.ActionURL == "/api/hwdiag/install-kernel-headers" {
			hasInstallHeaders = true
		}
	}
	if !hasInstallHeaders {
		t.Errorf("missing_headers fact lacks the install-kernel-headers action: %+v", view.Facts[0].Remediation)
	}
	// Embedded Fact fields promote through.
	if view.Facts[0].Title != "headers" || view.Facts[0].Class != recovery.ClassMissingHeaders {
		t.Errorf("embedded Fact fields lost: %+v", view.Facts[0].Fact)
	}

	// Unknown class → only the generic diagnostic-bundle action, no invented fix.
	rem1 := view.Facts[1].Remediation
	if len(rem1) != 1 {
		t.Fatalf("unknown class should have exactly the bundle action, got %d: %+v", len(rem1), rem1)
	}
	if rem1[0].ActionURL != "/api/diag/bundle" {
		t.Errorf("unknown-class remediation = %q, want the diag bundle", rem1[0].ActionURL)
	}
}

// TestDoctorReportSkipsBundleOnOKFacts binds #1510: all-clear (OK) facts get
// no diagnostic-bundle escalation card — there's nothing to escalate on a
// healthy finding, so offering it reads as noise to a first-time operator.
// Non-OK facts still carry the bundle (covered by TestDoctorReportWithRemediation).
func TestDoctorReportSkipsBundleOnOKFacts(t *testing.T) {
	t.Parallel()
	report := doctor.Report{
		Schema:    "1",
		Generated: time.Unix(1700000000, 0).UTC(),
		Severity:  doctor.SeverityOK,
		Facts: []doctor.Fact{
			{Detector: "all_clear", Class: recovery.ClassUnknown, Title: "everything fine", Severity: doctor.SeverityOK},
			{Detector: "warn", Class: recovery.ClassUnknown, Title: "a warning", Severity: doctor.SeverityWarning},
		},
	}

	view := doctorReportWithRemediation(report)

	// OK fact: bundle stripped → no remediation at all (ClassUnknown's only
	// entry was the bundle).
	if got := len(view.Facts[0].Remediation); got != 0 {
		t.Errorf("OK fact should carry no remediation, got %d: %+v", got, view.Facts[0].Remediation)
	}
	// Warning fact: bundle retained.
	rem := view.Facts[1].Remediation
	if len(rem) != 1 || rem[0].ActionURL != recovery.DiagnosticBundleActionURL {
		t.Errorf("warning fact should keep the diag bundle, got %+v", rem)
	}
}
