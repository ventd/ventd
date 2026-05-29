package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

func shadowTestServer(t *testing.T, shadow bool) *Server {
	t.Helper()
	live := config.Empty()
	live.Apply.Shadow = shadow
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)
	return &Server{
		cfg:    &cfgPtr,
		logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}
}

// TestRuleApplyShadow02_CalibrationRefusedInShadowMode pins
// RULE-APPLY-SHADOW-02: calibration cannot run without writing PWM, so
// handleCalibrateStart returns 423 Locked while shadow mode is on — and
// it short-circuits BEFORE touching s.cal or requiring the fan param,
// so the refusal is unambiguously the shadow gate. The control case
// (shadow off, no fan param) proves the 423 is shadow-specific: it falls
// through to the ordinary 400.
//
// Bound: internal/web/shadow_test.go:shadow_on_returns_423
// Bound: internal/web/shadow_test.go:shadow_off_does_not_return_423
func TestRuleApplyShadow02_CalibrationRefusedInShadowMode(t *testing.T) {
	t.Run("shadow_on_returns_423", func(t *testing.T) {
		s := shadowTestServer(t, true)
		rec := httptest.NewRecorder()
		// Deliberately pass a fan param to prove the shadow gate fires
		// before any fan lookup; cal is nil and must never be reached.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/calibrate/start?fan=/sys/class/hwmon/hwmon0/pwm1", nil)
		s.handleCalibrateStart(rec, req)
		if rec.Code != http.StatusLocked {
			t.Errorf("shadow mode calibrate/start: status = %d, want %d (Locked)", rec.Code, http.StatusLocked)
		}
	})

	t.Run("shadow_off_does_not_return_423", func(t *testing.T) {
		s := shadowTestServer(t, false)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/calibrate/start", nil)
		s.handleCalibrateStart(rec, req)
		if rec.Code == http.StatusLocked {
			t.Errorf("shadow off: calibrate/start returned 423 Locked; the gate must only fire in shadow mode")
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("shadow off, no fan param: status = %d, want %d (the ordinary fall-through)", rec.Code, http.StatusBadRequest)
		}
	})
}

// TestRuleApplyShadow03_StatusReportsShadowMode pins that the status
// payload the dashboard polls carries the shadow_mode flag so the
// "observing only" banner can render. Without this the operator can't
// tell the live duty readings aren't being driven.
//
// Bound: internal/web/shadow_test.go:status_reflects_shadow_flag
func TestRuleApplyShadow03_StatusReportsShadowMode(t *testing.T) {
	t.Run("status_reflects_shadow_flag", func(t *testing.T) {
		if got := shadowTestServer(t, true).buildStatus().ShadowMode; !got {
			t.Errorf("buildStatus().ShadowMode = false in shadow mode, want true")
		}
		if got := shadowTestServer(t, false).buildStatus().ShadowMode; got {
			t.Errorf("buildStatus().ShadowMode = true with shadow off, want false")
		}
	})
}
