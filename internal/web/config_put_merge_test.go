package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestConfigPUT_PartialBodyPreservesOtherTopLevelKeys is the regression
// guard for #1408. A PUT body containing only the `smart` key used to
// silently zero every other top-level field — controls/sensors/fans
// vanished, web.listen fell back from 0.0.0.0:9999 to the
// 127.0.0.1:9999 default, and the dashboard read as a fresh install.
// The handler now does a top-level merge against the live config; only
// keys present in the body are replaced, everything else is preserved.
func TestConfigPUT_PartialBodyPreservesOtherTopLevelKeys(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Seed the live config with state across multiple top-level keys
	// so a destructive PUT would be visible across the surface.
	starting := config.Empty()
	starting.Web.Listen = "0.0.0.0:9999"
	starting.Sensors = []config.Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}}
	starting.Fans = []config.Fan{{Name: "fan1", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon0/pwm1", MinPWM: 60, MaxPWM: 255}}
	starting.Curves = []config.CurveConfig{{Name: "test-curve", Type: "linear", Sensor: "cpu", MinTemp: 30, MaxTemp: 80}}
	srv.cfg.Store(starting)

	// Partial body — only the smart sub-object. Pre-fix this wiped
	// every other field; post-fix the handler merges.
	body := []byte(`{"smart": {"preset": "silent"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config: status=%d body=%q", w.Code, w.Body.String())
	}

	cached := srv.cfg.Load()
	if cached == nil {
		t.Fatal("nil cached config after PUT")
	}

	// Smart preset must have been applied.
	if cached.Smart.Preset != "silent" {
		t.Errorf("smart.preset = %q, want %q", cached.Smart.Preset, "silent")
	}

	// Web.Listen must NOT have fallen back to the 127.0.0.1:9999 default.
	if cached.Web.Listen != "0.0.0.0:9999" {
		t.Errorf("web.listen = %q, want %q (#1408 LAN-binding regression)",
			cached.Web.Listen, "0.0.0.0:9999")
	}

	// Sensors / fans / curves must have survived the partial PUT.
	if len(cached.Sensors) != 1 || cached.Sensors[0].Name != "cpu" {
		t.Errorf("sensors zeroed by partial PUT: %+v", cached.Sensors)
	}
	if len(cached.Fans) != 1 || cached.Fans[0].Name != "fan1" {
		t.Errorf("fans zeroed by partial PUT: %+v", cached.Fans)
	}
	if len(cached.Curves) != 1 || cached.Curves[0].Name != "test-curve" {
		t.Errorf("curves zeroed by partial PUT: %+v", cached.Curves)
	}
}

// TestConfigPUT_EmptyBodyRejected — an empty body used to silently
// reset the whole config to defaults; now returns 400.
func TestConfigPUT_EmptyBodyRejected(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader([]byte{}))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400", w.Code)
	}
}

// TestConfigPUT_FullBodyStillReplaces — a body that includes every
// top-level field still fully replaces the live config (the path
// the existing settings.js takes). The fix is additive: previously-
// working clients are unaffected.
func TestConfigPUT_FullBodyStillReplaces(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	starting := config.Empty()
	starting.Web.Listen = "0.0.0.0:9999"
	starting.Sensors = []config.Sensor{{Name: "stale", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"}}
	srv.cfg.Store(starting)

	replacement := config.Empty()
	replacement.Web.Listen = "127.0.0.1:1234"
	// Sensors deliberately left empty in the replacement — the full PUT
	// must drop the "stale" entry from the seed.
	body, _ := json.Marshal(replacement)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config: status=%d body=%q", w.Code, w.Body.String())
	}
	cached := srv.cfg.Load()
	if cached.Web.Listen != "127.0.0.1:1234" {
		t.Errorf("web.listen = %q, want %q (full PUT must replace)",
			cached.Web.Listen, "127.0.0.1:1234")
	}
	if len(cached.Sensors) != 0 {
		t.Errorf("sensors should be empty after full PUT replaced them; got %+v",
			cached.Sensors)
	}
}
