package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
)

// TestSmartStatus_MicCalibratedTrueWhenKCalPresent verifies the
// #1281 wire: writing a calibration record to s.kCalPath flips the
// /api/v1/smart/status acoustic.mic_calibrated flag to true, so the
// UI knows current_dba is true dBA rather than within-host au.
func TestSmartStatus_MicCalibratedTrueWhenKCalPresent(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	srv.kCalPath = filepath.Join(t.TempDir(), "k_cal.json")
	if err := acrunner.WriteResultJSON(srv.kCalPath, acrunner.Result{
		MicDevice:  "fake",
		KCalOffset: 139.0,
	}); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/smart/status", nil)
	w := httptest.NewRecorder()
	srv.handleSmartStatus(w, req)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	var body smartStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Acoustic.MicCalibrated {
		t.Errorf("acoustic.mic_calibrated = false, want true")
	}
}

// TestSmartStatus_MicCalibratedFalseWhenKCalMissing covers the
// uncalibrated host path: with no k_cal.json on disk,
// acoustic.mic_calibrated must be false so the UI surfaces the
// "calibrate mic" hint.
func TestSmartStatus_MicCalibratedFalseWhenKCalMissing(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	srv.kCalPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/smart/status", nil)
	w := httptest.NewRecorder()
	srv.handleSmartStatus(w, req)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	var body smartStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Acoustic.MicCalibrated {
		t.Errorf("acoustic.mic_calibrated = true, want false (no k_cal.json staged)")
	}
}
