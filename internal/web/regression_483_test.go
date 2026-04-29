package web

// Regression tests for issue #483: curve-editor form-state mismatch.
//
// Root cause: the form submitted the full config via PUT, but the JS tracked
// raw PWM (0-255) in cfg.curves[i].min_pwm while the server's canonical field
// is min_pwm_pct. MigrateCurvePWMFields saw the disagreement and silently
// reverted the user's edit by preferring the stale _pct value.
//
// Fix: PATCH /api/config merges only the explicitly provided fields over the
// live config, PUT returns the validated config so the UI can rehydrate, and
// the JS form tracks which fields changed and sends only those.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/web/authpersist"
)

// newPatch483Harness builds a Server backed by a temp dir with a pre-loaded
// config containing one sensor and one linear curve. All tests use this
// consistent starting state so assertions are deterministic.
func newPatch483Harness(t *testing.T) (srv *Server, tok string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	authPath := authpersist.DefaultPath(dir)

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cal := calibrate.New(filepath.Join(dir, "cal.json"), logger, nil)
	sm := setupmgr.New(cal, logger)
	var cfgPtr atomic.Pointer[config.Config]
	restart := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	minPct := uint8(20) // 20% → raw ≈ 51
	maxPct := uint8(80) // 80% → raw ≈ 204
	initial := config.Empty()
	initial.Sensors = []config.Sensor{{
		Name: "cpu_temp",
		Type: "hwmon",
		Path: "/sys/class/hwmon/hwmon0/temp1_input",
	}}
	initial.Curves = []config.CurveConfig{{
		Name:      "cpu_linear",
		Type:      "linear",
		Sensor:    "cpu_temp",
		MinTemp:   30,
		MaxTemp:   80,
		MinPWMPct: &minPct,
		MaxPWMPct: &maxPct,
	}}
	config.MigrateCurvePWMFields(initial)
	cfgPtr.Store(initial)

	srv = New(ctx, &cfgPtr, configPath, authPath, logger, cal, sm, restart, "tok483", hwdiag.NewStore())

	sessionTok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return srv, sessionTok
}

// patchReq builds an authenticated PATCH /api/config request.
func patchReq(t *testing.T, srv *Server, tok string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(body))
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr
}

// getConfigReq builds an authenticated GET /api/config request.
func getConfigReq(t *testing.T, srv *Server, tok string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Host = "ventd.local:9999"
	req.Header.Set("Origin", "http://ventd.local:9999")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	return rr
}

// TestRegression_Issue483_PatchPreservesUntouched verifies that a PATCH
// carrying only min_pwm_pct leaves all other curve fields (sensor, min_temp,
// max_temp, max_pwm_pct) unchanged in memory.
func TestRegression_Issue483_PatchPreservesUntouched(t *testing.T) {
	srv, tok := newPatch483Harness(t)

	newMinPct := 15.0
	patch := ConfigPatch{
		Curves: []CurvePatch{{Name: "cpu_linear", MinPWMPct: &newMinPct}},
	}
	body, _ := json.Marshal(patch)
	rr := patchReq(t, srv, tok, body)

	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH: status=%d body=%q", rr.Code, rr.Body.String())
	}

	live := srv.cfg.Load()
	if len(live.Curves) != 1 {
		t.Fatalf("want 1 curve, got %d", len(live.Curves))
	}
	c := live.Curves[0]

	// Fields not in the PATCH must be untouched.
	if c.Sensor != "cpu_temp" {
		t.Errorf("sensor mutated: got %q, want %q", c.Sensor, "cpu_temp")
	}
	if c.MinTemp != 30 {
		t.Errorf("min_temp mutated: got %v, want 30", c.MinTemp)
	}
	if c.MaxTemp != 80 {
		t.Errorf("max_temp mutated: got %v, want 80", c.MaxTemp)
	}
	if c.MaxPWMPct == nil || *c.MaxPWMPct != 80 {
		t.Errorf("max_pwm_pct mutated: got %v, want ptr(80)", c.MaxPWMPct)
	}

	// The patched field must reflect the new value.
	if c.MinPWMPct == nil || *c.MinPWMPct != 15 {
		t.Errorf("min_pwm_pct: got %v, want ptr(15)", c.MinPWMPct)
	}
}

// TestRegression_Issue483_FormInitializesFromConfig verifies that GET
// /api/config returns a curve whose min_pwm and max_pwm raw fields are
// non-zero when min_pwm_pct / max_pwm_pct are set. The JS form reads
// c.min_pwm to initialise the percent input; a zero value means the form
// would open showing 0%, masking the real configured speed.
func TestRegression_Issue483_FormInitializesFromConfig(t *testing.T) {
	srv, tok := newPatch483Harness(t)

	rr := getConfigReq(t, srv, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config: status=%d", rr.Code)
	}

	var cfg config.Config
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(cfg.Curves) != 1 {
		t.Fatalf("want 1 curve in response, got %d", len(cfg.Curves))
	}
	c := cfg.Curves[0]

	// min_pwm must be non-zero: MigrateCurvePWMFields must have derived it
	// from min_pwm_pct=20 → raw≈51 so the form renders the correct value.
	if c.MinPWM == 0 {
		t.Errorf("min_pwm=0 in GET response (min_pwm_pct=%v): form would initialise to 0%%", c.MinPWMPct)
	}
	if c.MaxPWM == 0 {
		t.Errorf("max_pwm=0 in GET response (max_pwm_pct=%v): form would initialise to 0%%", c.MaxPWMPct)
	}

	// Spot-check: 20% → pctToRaw(20) = round(20/100*255) = 51.
	const wantMinRaw = uint8(51)
	if c.MinPWM != wantMinRaw {
		t.Errorf("min_pwm: want %d (20%%), got %d", wantMinRaw, c.MinPWM)
	}
}

// TestRegression_Issue483_InvalidApplyShowsError verifies that PATCH with an
// unknown curve name returns HTTP 400 with a plain-text body containing "not
// found", so the UI can surface it inline on the form.
func TestRegression_Issue483_InvalidApplyShowsError(t *testing.T) {
	srv, tok := newPatch483Harness(t)

	cases := []struct {
		name    string
		patch   ConfigPatch
		wantSub string
	}{
		{
			name:    "unknown curve",
			patch:   ConfigPatch{Curves: []CurvePatch{{Name: "does_not_exist"}}},
			wantSub: "not found",
		},
		{
			name: "min_temp exceeds max_temp",
			patch: ConfigPatch{Curves: []CurvePatch{{
				Name:    "cpu_linear",
				MinTemp: float64ptr(90), // 90 > existing max_temp=80
			}}},
			wantSub: "min_temp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.patch)
			rr := patchReq(t, srv, tok, body)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d body=%q", rr.Code, rr.Body.String())
			}
			got := rr.Body.String()
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.wantSub)) {
				t.Errorf("error body %q does not contain %q", got, tc.wantSub)
			}
		})
	}
}

// TestRegression_Issue483_ApplyResponseIncludesNewConfig verifies that a
// successful PATCH returns a JSON body that is the full merged config, and
// that the patched field is reflected in the response.
func TestRegression_Issue483_ApplyResponseIncludesNewConfig(t *testing.T) {
	srv, tok := newPatch483Harness(t)

	newMax := 90.0
	patch := ConfigPatch{
		Curves: []CurvePatch{{Name: "cpu_linear", MaxPWMPct: &newMax}},
	}
	body, _ := json.Marshal(patch)
	rr := patchReq(t, srv, tok, body)

	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Response must be valid JSON config, not a status string.
	var got config.Config
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("response is not a config JSON: %v (body: %q)", err, rr.Body.String())
	}

	if len(got.Curves) != 1 {
		t.Fatalf("response config: want 1 curve, got %d", len(got.Curves))
	}
	c := got.Curves[0]

	// Patched field must appear in the response.
	if c.MaxPWMPct == nil || *c.MaxPWMPct != 90 {
		t.Errorf("max_pwm_pct in response: got %v, want ptr(90)", c.MaxPWMPct)
	}

	// Unpatched fields must also be present (full merged config, not sparse).
	if c.Sensor != "cpu_temp" {
		t.Errorf("sensor missing from response: got %q", c.Sensor)
	}
	if c.MinTemp != 30 {
		t.Errorf("min_temp missing from response: got %v", c.MinTemp)
	}
}

func float64ptr(v float64) *float64 { return &v }
