package web

// Coverage for the three setup/wizard-adjacent handlers that
// setup_handlers_test.go leaves uncovered:
//
//   * handleDetectRPM          — POST /api/detect-rpm?fan=<path>
//   * handleCalibrateAbort     — POST /api/calibrate/abort?fan=<path>
//   * handleSetupCalibrateAbort — POST /api/setup/calibrate/abort
//   * handleSetupStatus        — GET  /api/setup/status
//
// What this file pins:
//
//   * Method enforcement:       GET / DELETE / PUT → 405, not 500.
//   * Required params:          missing ?fan= → 400, not a crash.
//   * Unknown fan lookups:      fan not in liveCfg.Fans → 404.
//   * Idempotent abort:         Abort on no-op calibrate state → 204.
//   * Setup status shape:       JSON body contains "needed" key so the
//                               wizard UI can branch on it. Regression
//                               target: a future rename of
//                               ProgressNeeded.Needed → ProgressNeeded.Required
//                               would silently break the UI without
//                               this anchor.
//
// Reference for future sessions:
//
//   These handlers are wired under s.registerAPIRoutes with a session-
//   auth wrapper. We call them directly (same convention as
//   setup_handlers_test.go) because the auth layer has its own
//   coverage in security_test.go. Mixing auth + state-machine tests
//   makes regressions harder to localise, so keep the split.
//
//   If you add a new "/api/..." POST handler that mutates state and
//   takes a ?fan= query param, add the same three tests here:
//   method, missing-param, unknown-fan. That's the shape of the
//   "cheap contract" that catches most regressions.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// ─── handleDetectRPM ──────────────────────────────────────────────────────

func TestHandleDetectRPM_NonPOST_Returns405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/detect-rpm?fan=/sys/x/pwm1", nil)
			w := httptest.NewRecorder()
			srv.handleDetectRPM(w, req)
			if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
				t.Fatalf("%s /api/detect-rpm: status = %d, want 405", method, got)
			}
		})
	}
}

func TestHandleDetectRPM_MissingFanParam_Returns400(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/detect-rpm", nil)
	w := httptest.NewRecorder()
	srv.handleDetectRPM(w, req)

	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("missing fan param: status = %d, want 400", got)
	}
	if body := w.Body.String(); !strings.Contains(body, "fan query param") {
		t.Fatalf("missing fan param body = %q, want \"fan query param\" message", body)
	}
}

func TestHandleDetectRPM_UnknownFan_Returns404(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// liveCfg from newHandlerHarness is config.Empty() — zero fans. Any
	// ?fan= value is therefore unknown.
	req := httptest.NewRequest(http.MethodPost, "/api/detect-rpm?fan=/sys/class/hwmon/hwmon0/pwm1", nil)
	w := httptest.NewRecorder()
	srv.handleDetectRPM(w, req)

	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("unknown fan: status = %d, want 404 (body=%q)", got, w.Body.String())
	}
}

// ─── handleCalibrateAbort ─────────────────────────────────────────────────

func TestHandleCalibrateAbort_NonPOST_Returns405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/calibrate/abort?fan=/x", nil)
	w := httptest.NewRecorder()
	srv.handleCalibrateAbort(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/calibrate/abort: status = %d, want 405", got)
	}
}

func TestHandleCalibrateAbort_MissingFanParam_Returns400(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/calibrate/abort", nil)
	w := httptest.NewRecorder()
	srv.handleCalibrateAbort(w, req)

	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("missing fan: status = %d, want 400", got)
	}
}

// TestHandleCalibrateAbort_NoActiveRun_Returns204 — Abort is idempotent:
// aborting when nothing is running must still return 204, not 500. The
// UI fires Abort on modal close regardless of whether a sweep is live.
func TestHandleCalibrateAbort_NoActiveRun_Returns204(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/calibrate/abort?fan=/sys/class/hwmon/hwmon0/pwm1", nil)
	w := httptest.NewRecorder()
	srv.handleCalibrateAbort(w, req)

	if got := w.Result().StatusCode; got != http.StatusNoContent {
		t.Fatalf("no-op Abort: status = %d, want 204 (body=%q)", got, w.Body.String())
	}
}

// ─── handleSetupCalibrateAbort ────────────────────────────────────────────

func TestHandleSetupCalibrateAbort_NonPOST_Returns405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/calibrate/abort", nil)
	w := httptest.NewRecorder()
	srv.handleSetupCalibrateAbort(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/setup/calibrate/abort: status = %d, want 405", got)
	}
}

func TestHandleSetupCalibrateAbort_NoActiveRun_Returns204(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/setup/calibrate/abort", nil)
	w := httptest.NewRecorder()
	srv.handleSetupCalibrateAbort(w, req)

	if got := w.Result().StatusCode; got != http.StatusNoContent {
		t.Fatalf("no-op setup Abort: status = %d, want 204 (body=%q)", got, w.Body.String())
	}
}

// ─── handleSetupStatus ────────────────────────────────────────────────────

// TestHandleSetupStatus_ReturnsJSONWithNeededField — the wizard UI
// branches on the `needed` bool to decide whether to render the first-
// boot flow at all. The handler must return a JSON object that has
// this key. We decode into map[string]any rather than the concrete
// ProgressNeeded struct so this test survives additive field renames
// but fails on removal of the "needed" key.
func TestHandleSetupStatus_ReturnsJSONWithNeededField(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	srv.handleSetupStatus(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("setup status: status = %d, want 200", got)
	}

	// no-store header so the UI's short poll doesn't ever serve stale
	// wizard progress from a browser cache.
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want \"no-store\"", got)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("setup status body not JSON: %v — body=%q", err, w.Body.String())
	}
	if _, ok := body["needed"]; !ok {
		t.Fatalf("setup status body missing \"needed\" key: %q", w.Body.String())
	}
}

// TestHandleDetectRPM_KnownFan_ReachesManager exercises the successful
// resolution path: the fan IS found in liveCfg.Fans, so DetectRPMSensor
// runs against the (bogus) pwm path it holds. We expect a 500 because
// the path doesn't resolve to a real hwmon channel, and we're fine with
// that — the test's purpose is to pin that the handler routes through
// to cal.DetectRPMSensor instead of short-circuiting at the name lookup.
//
// If a future refactor silently swaps the lookup from "PWMPath string
// equality" to "a path resolver that normalises" this test fails loudly
// (wrong status) instead of silently (still 404 but for a different
// reason).
func TestHandleDetectRPM_KnownFan_ReachesManager(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Swap the live config to include a single fan. The handler reads
	// s.cfg.Load() fresh on every request, so mutation after
	// newHandlerHarness is safe.
	cfg := config.Empty()
	cfg.Fans = []config.Fan{{
		Name:    "test_fan",
		Type:    "hwmon",
		PWMPath: "/tmp/ventd-test-nonexistent-pwm1",
		MaxPWM:  255,
	}}
	srv.cfg.Store(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/detect-rpm?fan=/tmp/ventd-test-nonexistent-pwm1", nil)
	w := httptest.NewRecorder()
	srv.handleDetectRPM(w, req)

	// DetectRPMSensor on a nonexistent PWM file returns an error from
	// hwmon.ReadPWM; the handler wraps that as 500.
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("detect on known but nonexistent pwm: status = %d, want 500 (body=%q)", got, w.Body.String())
	}
}
