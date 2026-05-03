package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// ─────────────────────────────────────────────────────────────────────
// v0.5.12 #64: outbound diag ingest endpoint.
//
// /api/v1/diag/send is the daemon-side opt-in alternative to the
// existing diag/bundle download flow. Operators flip
// diag.upstream_ingest.enabled in config + supply an HTTPS URL; the
// daemon then auto-generates a per-installation bearer token on first
// send and POSTs redacted bundles to the maintainer endpoint.
// ─────────────────────────────────────────────────────────────────────

func TestHandleDiagSend_NonPOST_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/v1/diag/send", nil)
		w := httptest.NewRecorder()
		srv.handleDiagSend(w, req)
		if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/v1/diag/send: status = %d, want %d", m, got, http.StatusMethodNotAllowed)
		}
	}
}

func TestHandleDiagSend_DisabledByDefault_Returns412(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()
	// Default config has Diag.UpstreamIngest.Enabled = false.

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diag/send", nil)
	w := httptest.NewRecorder()
	srv.handleDiagSend(w, req)

	if got := w.Result().StatusCode; got != http.StatusPreconditionFailed {
		t.Fatalf("disabled: status = %d, want 412", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "disabled") {
		t.Errorf("response missing 'disabled' hint: %q", body)
	}
}

func TestHandleDiagSend_NonHTTPSURL_Refuses(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	live := config.Empty()
	live.Diag.UpstreamIngest.Enabled = true
	live.Diag.UpstreamIngest.URL = "http://insecure.example.com/upload"
	srv.cfg.Store(live)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diag/send", nil)
	w := httptest.NewRecorder()
	srv.handleDiagSend(w, req)

	if got := w.Result().StatusCode; got != http.StatusPreconditionFailed {
		t.Fatalf("non-https: status = %d, want 412", got)
	}
	if !strings.Contains(w.Body.String(), "https") {
		t.Errorf("response missing 'https' hint: %q", w.Body.String())
	}
}

// TestHandleDiagSend_HappyPath_SendsToStubIngest exercises the full
// upload chain against a httptest.Server-backed maintainer stub.
// Validates:
//
//   - Bundle is generated (fresh, no leftover file from prior run)
//   - POST hits the configured URL with Content-Type: application/gzip
//   - Authorization: Bearer <token> is set
//   - Server's reference id is returned in the JSON response
//   - Empty token in config gets auto-populated on first send
//
// Skipped under -short because diag.Generate walks the live system
// (hwmon, /proc, NVML) which doesn't exist in CI containers + takes
// several seconds. HIL-only.
func TestHandleDiagSend_HappyPath_SendsToStubIngest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires live hwmon / state for diag.Generate")
	}
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	var (
		gotAuth        atomic.Value
		gotContentType atomic.Value
		gotBytes       atomic.Int64
	)

	stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotContentType.Store(r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		gotBytes.Store(int64(len(body)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reference":"abc-123-stub","ok":true}`))
	}))
	defer stub.Close()

	// Inject the stub's HTTPS-skipping client so the daemon's TLS
	// verification doesn't reject the test cert.
	prevClient := uploadHTTPClient
	uploadHTTPClient = stub.Client()
	defer func() { uploadHTTPClient = prevClient }()

	live := config.Empty()
	live.Diag.UpstreamIngest.Enabled = true
	live.Diag.UpstreamIngest.URL = stub.URL // httptest server URL is https://...
	// Token left empty — handler auto-generates.
	srv.cfg.Store(live)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diag/send", nil)
	w := httptest.NewRecorder()
	srv.handleDiagSend(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("happy path: status = %d, want 200 (body=%q)", got, w.Body.String())
	}
	var resp diagSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Reference != "abc-123-stub" {
		t.Errorf("reference = %q, want abc-123-stub", resp.Reference)
	}
	if resp.Bytes <= 0 {
		t.Errorf("bytes = %d, want > 0", resp.Bytes)
	}
	if resp.RedactorProfile == "" {
		t.Errorf("redactor_profile is empty")
	}

	// Assertions on what the stub received.
	if auth, _ := gotAuth.Load().(string); !strings.HasPrefix(auth, "Bearer ") || len(auth) < 10 {
		t.Errorf("Authorization = %q, want Bearer <token>", auth)
	}
	if ct, _ := gotContentType.Load().(string); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	if got := gotBytes.Load(); got <= 0 {
		t.Errorf("body bytes = %d, want > 0", got)
	}

	// Token should now be populated in the live config.
	current := srv.cfg.Load()
	if current.Diag.UpstreamIngest.Token == "" {
		t.Errorf("token was not auto-generated + persisted to live config")
	}
	if len(current.Diag.UpstreamIngest.Token) != 64 {
		t.Errorf("token length = %d, want 64 (32-byte hex)", len(current.Diag.UpstreamIngest.Token))
	}
}

func TestHandleDiagSend_IngestRejects_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires live hwmon / state for diag.Generate")
	}
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("invalid token"))
	}))
	defer stub.Close()

	prevClient := uploadHTTPClient
	uploadHTTPClient = stub.Client()
	defer func() { uploadHTTPClient = prevClient }()

	live := config.Empty()
	live.Diag.UpstreamIngest.Enabled = true
	live.Diag.UpstreamIngest.URL = stub.URL
	live.Diag.UpstreamIngest.Token = "preset-token"
	srv.cfg.Store(live)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diag/send", nil)
	w := httptest.NewRecorder()
	srv.handleDiagSend(w, req)

	if got := w.Result().StatusCode; got != http.StatusBadGateway {
		t.Fatalf("ingest 403: handler status = %d, want 502", got)
	}
	if !strings.Contains(w.Body.String(), "403") && !strings.Contains(w.Body.String(), "HTTP 403") {
		t.Errorf("response missing ingest status: %q", w.Body.String())
	}
}

func TestParseIngestReference_HandlesBothShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"json", `{"reference":"abc-123","ok":true}`, "abc-123"},
		{"json-with-spaces", `{ "reference" :  "xyz-789" , "ok": true }`, "xyz-789"},
		{"plain-text", "ref-456\n", "ref-456"},
		{"empty", "", ""},
		{"json-no-reference", `{"ok":true}`, ""},
		{"truncated-plain-overlong", strings.Repeat("a", 200), strings.Repeat("a", 64)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseIngestReference([]byte(c.body))
			if got != c.want {
				t.Errorf("parseIngestReference(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestGenerateIngestToken_LengthAndUniqueness(t *testing.T) {
	a, err := generateIngestToken()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := generateIngestToken()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(a) != 64 || len(b) != 64 {
		t.Errorf("token length: %d, %d (want 64 each)", len(a), len(b))
	}
	if a == b {
		t.Errorf("two generated tokens collided — crypto/rand seeded incorrectly")
	}
}
