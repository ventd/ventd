package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
