package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPermissionsPolicyOnHTMLAndAPI asserts that Permissions-Policy is present
// on the HTML entry page (/) and on a representative /api/* route, and that it
// contains the expected explicit deny list — not a wildcard.
func TestPermissionsPolicyOnHTMLAndAPI(t *testing.T) {
	srv, tok := newSecuritySrv(t)

	wantDenied := []string{
		"accelerometer=()",
		"camera=()",
		"geolocation=()",
		"gyroscope=()",
		"magnetometer=()",
		"microphone=()",
		"payment=()",
		"usb=()",
	}

	routes := []struct {
		path   string
		cookie bool
	}{
		{"/", true},
		{"/api/ping", false},
	}

	for _, rt := range routes {
		t.Run(rt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, rt.path, nil)
			if rt.cookie {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			}
			rr := httptest.NewRecorder()
			srv.handler.ServeHTTP(rr, req)

			pp := rr.Header().Get("Permissions-Policy")
			if pp == "" {
				t.Fatalf("Permissions-Policy header missing on %s", rt.path)
			}
			for _, deny := range wantDenied {
				if !strings.Contains(pp, deny) {
					t.Errorf("Permissions-Policy on %s missing %q: got %q", rt.path, deny, pp)
				}
			}
		})
	}
}

// TestETagRoundTripOnStaticAsset asserts:
//   - First GET on an embedded /ui/* asset returns 200 + ETag header.
//   - Second GET with matching If-None-Match returns 304 with empty body.
func TestETagRoundTripOnStaticAsset(t *testing.T) {
	srv, _ := newSecuritySrv(t)

	// Use a known static asset — styles/base.css is always present per the
	// go:embed directive in ui.go.
	assetURL := "/ui/styles/base.css"

	// First GET: expect 200 and an ETag.
	req1 := httptest.NewRequest(http.MethodGet, assetURL, nil)
	rr1 := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first GET %s: status=%d want 200", assetURL, rr1.Code)
	}
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("ETag missing on first GET %s", assetURL)
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag is not a quoted string: %q", etag)
	}

	// Second GET with matching If-None-Match: expect 304 and empty body.
	req2 := httptest.NewRequest(http.MethodGet, assetURL, nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional GET %s: status=%d want 304", assetURL, rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Errorf("304 response has non-empty body (%d bytes)", rr2.Body.Len())
	}
}

// TestHTMLEntryPageHasNoETag asserts that the authenticated dashboard HTML
// served at / does not carry an ETag header (fresh HTML is required for the
// token flow to work correctly across daemon restarts).
func TestHTMLEntryPageHasNoETag(t *testing.T) {
	srv, tok := newSecuritySrv(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /: status=%d want 200", rr.Code)
	}
	if etag := rr.Header().Get("ETag"); etag != "" {
		t.Errorf("HTML entry page / must not have ETag, got %q", etag)
	}
}

// TestAPIResponseHasNoETag asserts that /api/* responses never carry an ETag.
func TestAPIResponseHasNoETag(t *testing.T) {
	srv, _ := newSecuritySrv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)

	if etag := rr.Header().Get("ETag"); etag != "" {
		t.Errorf("/api/ping must not have ETag, got %q", etag)
	}
}
