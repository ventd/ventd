package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

func TestProfile_GetReturnsActiveAndMap(t *testing.T) {
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}},
	}
	live.ActiveProfile = "silent"
	srv.cfg.Store(&live)

	req := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	w := httptest.NewRecorder()
	srv.handleProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d want 200: %s", w.Code, w.Body)
	}
	var body profileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Active != "silent" {
		t.Errorf("Active = %q want silent", body.Active)
	}
	if got := body.Profiles["silent"].Bindings["cpu_fan"]; got != "cpu_linear_silent" {
		t.Errorf("bindings = %v; missing silent.cpu_fan", body.Profiles)
	}
}

func TestProfile_GetReturnsEmptyMapWhenNoneConfigured(t *testing.T) {
	srv := newVersionTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	w := httptest.NewRecorder()
	srv.handleProfile(w, req)
	var body profileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Profiles == nil {
		t.Errorf("Profiles should be non-nil empty map, got nil")
	}
}

func TestProfile_MethodNotAllowed(t *testing.T) {
	srv := newVersionTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/profile", nil)
	w := httptest.NewRecorder()
	srv.handleProfile(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/profile status %d want 405", w.Code)
	}
}

func TestProfileActive_RewritesBindingsAtomically(t *testing.T) {
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = map[string]config.Profile{
		"silent":   {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}},
		"balanced": {Bindings: map[string]string{"cpu_fan": "cpu_linear_balanced", "sys_fan": "sys_linear_balanced"}},
	}
	live.ActiveProfile = "silent"
	live.Controls = []config.Control{
		{Fan: "cpu_fan", Curve: "cpu_linear_silent"},
		{Fan: "sys_fan", Curve: "sys_linear_silent"},
		{Fan: "gpu_fan", Curve: "gpu_linear_gpu_dedicated"}, // not in any binding map
	}
	srv.cfg.Store(&live)

	body := bytes.NewBufferString(`{"name": "balanced"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d want 200: %s", w.Code, w.Body)
	}
	updated := srv.cfg.Load()
	if updated.ActiveProfile != "balanced" {
		t.Errorf("ActiveProfile = %q want balanced", updated.ActiveProfile)
	}
	for _, c := range updated.Controls {
		switch c.Fan {
		case "cpu_fan":
			if c.Curve != "cpu_linear_balanced" {
				t.Errorf("cpu_fan.Curve = %q want cpu_linear_balanced", c.Curve)
			}
		case "sys_fan":
			if c.Curve != "sys_linear_balanced" {
				t.Errorf("sys_fan.Curve = %q want sys_linear_balanced", c.Curve)
			}
		case "gpu_fan":
			if c.Curve != "gpu_linear_gpu_dedicated" {
				t.Errorf("gpu_fan.Curve = %q; fan not in profile bindings should be left untouched", c.Curve)
			}
		}
	}
}

func TestProfileActive_UnknownProfileRejected(t *testing.T) {
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}},
	}
	srv.cfg.Store(&live)

	body := bytes.NewBufferString(`{"name": "does-not-exist"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d want 400: %s", w.Code, w.Body)
	}
}

func TestProfileActive_EmptyNameRejected(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{"name": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d want 400", w.Code)
	}
}

func TestProfileActive_BadJSONRejected(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{not-json`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d want 400", w.Code)
	}
}

func TestProfileActive_MethodNotAllowed(t *testing.T) {
	srv := newVersionTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/profile/active", nil)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d want 405", w.Code)
	}
}
