package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// TestHwdiagEndpointSurfacesFutureSchema wires the full stack: calibrate
// loads a future-schema calibration.json and emits a hwdiag entry; the
// web server's /api/hwdiag endpoint returns it.
func TestHwdiagEndpointSurfacesFutureSchema(t *testing.T) {
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")
	const futurePath = "/sys/class/hwmon/hwmon0/pwm1"
	future := []byte(`{
  "schema_version": 999,
  "results": {
    "` + futurePath + `": { "pwm_path": "` + futurePath + `" }
  }
}`)
	if err := os.WriteFile(calPath, future, 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cal := calibrate.New(calPath, logger, nil)
	diag := hwdiag.NewStore()
	cal.SetDiagnosticStore(diag)

	sm := setupmgr.New(cal, logger)
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(config.Empty())
	restartCh := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, &liveCfg, "", "", logger, cal, sm, restartCh, "", diag)

	// Create a session so requireAuth passes.
	tok, err := srv.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/hwdiag", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var snap hwdiag.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap.Entries))
	}
	e := snap.Entries[0]
	if e.ID != hwdiag.IDCalibrationFutureSchema {
		t.Errorf("id=%q want %q", e.ID, hwdiag.IDCalibrationFutureSchema)
	}
	if e.Component != hwdiag.ComponentCalibration {
		t.Errorf("component=%q", e.Component)
	}
	if e.Remediation == nil || e.Remediation.AutoFixID != hwdiag.AutoFixRecalibrate {
		t.Errorf("remediation: %+v", e.Remediation)
	}
	if len(e.Affected) != 1 || e.Affected[0] != futurePath {
		t.Errorf("affected: %+v", e.Affected)
	}
	if snap.Revision == 0 {
		t.Errorf("revision should be non-zero")
	}
}
