package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/probe"
)

func TestPanic_MethodNotAllowed(t *testing.T) {
	srv := newVersionTestServer(t)
	for _, tc := range []struct {
		path    string
		method  string
		handler http.HandlerFunc
	}{
		{"/api/panic", http.MethodGet, srv.handlePanic},
		{"/api/panic/state", http.MethodPost, srv.handlePanicState},
		{"/api/panic/cancel", http.MethodGet, srv.handlePanicCancel},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("status %d want 405", w.Code)
			}
		})
	}
}

func TestPanic_InvalidBodyRejected(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{not-json`)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON status = %d, want 400", w.Code)
	}
}

func TestPanic_NegativeDurationRejected(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{"duration_s": -1}`)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative duration status = %d, want 400", w.Code)
	}
}

func TestPanic_StateBeforeAnyPanic(t *testing.T) {
	srv := newVersionTestServer(t)
	w := httptest.NewRecorder()
	srv.handlePanicState(w, httptest.NewRequest(http.MethodGet, "/api/panic/state", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got panicPayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Active {
		t.Errorf("Active should be false before any panic")
	}
	if got.RemainingS != 0 || got.StartedAt != "" || got.EndAt != "" {
		t.Errorf("zero-state should have all zero/empty fields: %+v", got)
	}
}

func TestPanic_IndefiniteDurationStaysActive(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{"duration_s": 0}`)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !srv.IsPanicked("ignored") {
		t.Errorf("IsPanicked should be true after indefinite panic")
	}
	snap := srv.panicSnapshot()
	if !snap.Active {
		t.Errorf("snapshot Active = false, want true")
	}
	if snap.EndAt != "" {
		t.Errorf("indefinite panic should have empty EndAt, got %q", snap.EndAt)
	}
	// Cleanup — so other tests see zero state.
	srv.restorePanic("test-cleanup")
}

func TestPanic_BoundedDurationExpiresAndClears(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{"duration_s": 1}`)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !srv.IsPanicked("") {
		t.Fatalf("panic flag should be true right after POST")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !srv.IsPanicked("") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if srv.IsPanicked("") {
		t.Errorf("panic flag still true after 1s duration + 3s wait")
	}
}

func TestPanic_CancelClearsImmediately(t *testing.T) {
	srv := newVersionTestServer(t)
	body := bytes.NewBufferString(`{"duration_s": 60}`)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic", body))
	if !srv.IsPanicked("") {
		t.Fatalf("panic flag not set after POST")
	}
	w2 := httptest.NewRecorder()
	srv.handlePanicCancel(w2, httptest.NewRequest(http.MethodPost, "/api/panic/cancel", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("cancel status %d: %s", w2.Code, w2.Body)
	}
	if srv.IsPanicked("") {
		t.Errorf("panic flag still set after cancel")
	}
}

func TestPanic_CancelWithNoActivePanicIsNoop(t *testing.T) {
	srv := newVersionTestServer(t)
	w := httptest.NewRecorder()
	srv.handlePanicCancel(w, httptest.NewRequest(http.MethodPost, "/api/panic/cancel", nil))
	if w.Code != http.StatusOK {
		t.Errorf("idempotent cancel should return 200, got %d", w.Code)
	}
}

func TestPanic_ConcurrentPanicsReplacePriorTimer(t *testing.T) {
	srv := newVersionTestServer(t)
	// First panic: 60s
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic",
		bytes.NewBufferString(`{"duration_s": 60}`)))
	first := srv.panicSnapshot()
	// Second panic: indefinite — should replace the 60s timer.
	w2 := httptest.NewRecorder()
	srv.handlePanic(w2, httptest.NewRequest(http.MethodPost, "/api/panic",
		bytes.NewBufferString(`{"duration_s": 0}`)))
	second := srv.panicSnapshot()
	if !second.Active {
		t.Fatal("second panic should still be active")
	}
	if second.EndAt != "" {
		t.Errorf("second panic replaced the first but EndAt = %q, want empty", second.EndAt)
	}
	// The first panic had a non-empty EndAt; the replacement must not
	// inherit it (replacement timer is indefinite). StartedAt may or
	// may not round to the same RFC3339 string depending on wall-
	// clock granularity, so we assert on EndAt which does change.
	if first.EndAt == "" {
		t.Errorf("first panic should have had a non-empty EndAt")
	}
	srv.restorePanic("test-cleanup")
}

func TestPanic_RemainingSDecreases(t *testing.T) {
	srv := newVersionTestServer(t)
	w := httptest.NewRecorder()
	srv.handlePanic(w, httptest.NewRequest(http.MethodPost, "/api/panic",
		bytes.NewBufferString(`{"duration_s": 5}`)))
	snap := srv.panicSnapshot()
	if snap.RemainingS <= 0 || snap.RemainingS > 5 {
		t.Errorf("RemainingS = %d, want 1..5", snap.RemainingS)
	}
	srv.restorePanic("test-cleanup")
}

// TestPanic_InvertedPolarityWritesZero binds the RULE-POLARITY-05 contract
// at the web panic-handler boundary (pass-6-web.md M4): an inverted-polarity
// fan flipped MaxPWM→MinPWM (effectively OFF) when handlePanic wrote
// MaxPWM direct via hwmon.WritePWM. Issue #1037. After the wiring through
// polarity.WritePWM, an inverted fan with MaxPWM=255 must see the
// underlying sysfs file land at 0 (max cooling under inverted polarity).
func TestPanic_InvertedPolarityWritesZero(t *testing.T) {
	srv := newVersionTestServer(t)

	// Seed a writable pwm sysfs file under a temp dir; pwm_enable=1
	// so hwmon.WritePWM can proceed.
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("seed pwm file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1_enable"), []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed pwm_enable: %v", err)
	}

	// Wire the live config with a single inverted-polarity hwmon fan.
	cfg := config.Empty()
	cfg.Fans = []config.Fan{{
		Name: "cpu", Type: "hwmon", PWMPath: pwmPath,
		MinPWM: 0, MaxPWM: 255,
	}}
	var live atomic.Pointer[config.Config]
	live.Store(cfg)
	srv.cfg = &live

	// Polarity channel: inverted.
	srv.SetPolarityChannels([]*probe.ControllableChannel{{
		PWMPath:  pwmPath,
		Polarity: "inverted",
	}})

	// Direct call to the helper (avoids panic-state plumbing).
	srv.writeMaxPWMToAllFans(cfg)

	// Inverted polarity: MaxPWM=255 → write 0.
	got := readUintFile(t, pwmPath)
	if got != 0 {
		t.Errorf("inverted-polarity panic write: pwm1 = %d, want 0 (255-255)", got)
	}
}

// TestPanic_NormalPolarityWritesMaxPWM pins the symmetric case: a
// normal-polarity fan must see MaxPWM=255 verbatim. Without this the
// inverted-fix could regress to "always write 0" which would silence
// the panic surface on every fan.
func TestPanic_NormalPolarityWritesMaxPWM(t *testing.T) {
	srv := newVersionTestServer(t)

	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm1")
	if err := os.WriteFile(pwmPath, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("seed pwm file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1_enable"), []byte("1\n"), 0o600); err != nil {
		t.Fatalf("seed pwm_enable: %v", err)
	}

	cfg := config.Empty()
	cfg.Fans = []config.Fan{{
		Name: "cpu", Type: "hwmon", PWMPath: pwmPath,
		MinPWM: 0, MaxPWM: 255,
	}}
	var live atomic.Pointer[config.Config]
	live.Store(cfg)
	srv.cfg = &live
	srv.SetPolarityChannels([]*probe.ControllableChannel{{
		PWMPath:  pwmPath,
		Polarity: "normal",
	}})

	srv.writeMaxPWMToAllFans(cfg)

	got := readUintFile(t, pwmPath)
	if got != 255 {
		t.Errorf("normal-polarity panic write: pwm1 = %d, want 255", got)
	}
}

// readUintFile reads a single integer from a sysfs-style file and
// returns it as uint64. Test helper for the panic polarity tests.
func readUintFile(t *testing.T, path string) uint64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatalf("parse %s = %q: %v", path, data, err)
	}
	return v
}
