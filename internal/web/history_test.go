package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

func TestHistoryRingBasic(t *testing.T) {
	h := NewHistoryStore(time.Second, 10*time.Second)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		h.Record("cpu", float32(i), now.Add(time.Duration(i)*time.Second))
	}
	snap := h.Snapshot("cpu", 0)
	if len(snap) != 5 {
		t.Fatalf("snapshot len = %d, want 5", len(snap))
	}
	for i, p := range snap {
		if p.V != float64(i) {
			t.Errorf("snap[%d].V = %v, want %d", i, p.V, i)
		}
	}
	// Newest timestamp is the last Record timestamp; older samples
	// walk backwards by interval.
	if snap[4].T != now.Add(4*time.Second).Unix() {
		t.Errorf("newest T = %d, want %d", snap[4].T, now.Add(4*time.Second).Unix())
	}
	if snap[0].T != snap[4].T-4 {
		t.Errorf("oldest T = %d, want %d (newest - 4)", snap[0].T, snap[4].T-4)
	}
}

func TestHistoryRingOverflow(t *testing.T) {
	// cap = window/interval + 1 = 5/1 + 1 = 6 slots.
	h := NewHistoryStore(time.Second, 5*time.Second)
	if h.Capacity() != 6 {
		t.Fatalf("capacity = %d, want 6", h.Capacity())
	}
	now := time.Unix(1_700_000_000, 0)
	// Write 20 samples into a cap-6 ring. Only the last 6 should remain.
	for i := 0; i < 20; i++ {
		h.Record("cpu", float32(i), now.Add(time.Duration(i)*time.Second))
	}
	snap := h.Snapshot("cpu", 0)
	if len(snap) != 6 {
		t.Fatalf("snapshot len after overflow = %d, want 6", len(snap))
	}
	wantValues := []float64{14, 15, 16, 17, 18, 19}
	for i, w := range wantValues {
		if snap[i].V != w {
			t.Errorf("snap[%d].V = %v, want %v", i, snap[i].V, w)
		}
	}
}

func TestHistorySnapshotWindow(t *testing.T) {
	h := NewHistoryStore(time.Second, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 30; i++ {
		h.Record("cpu", float32(i), now.Add(time.Duration(i)*time.Second))
	}
	// Window of 5 s at 1 s interval = last ~6 samples (cap rule).
	snap := h.Snapshot("cpu", 5*time.Second)
	if len(snap) > 6 {
		t.Errorf("windowed snap len = %d, want <= 6", len(snap))
	}
	if snap[len(snap)-1].V != 29 {
		t.Errorf("newest value = %v, want 29", snap[len(snap)-1].V)
	}
}

func TestHistorySnapshotEmpty(t *testing.T) {
	h := NewHistoryStore(time.Second, time.Minute)
	snap := h.Snapshot("missing", 0)
	if snap == nil {
		t.Fatalf("snapshot should be empty slice, not nil")
	}
	if len(snap) != 0 {
		t.Errorf("empty metric len = %d, want 0", len(snap))
	}
	// Must JSON-encode as [] — callers that return snap directly rely
	// on this so an empty body isn't "null" and breaks client parsers.
	buf, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(buf) != "[]" {
		t.Errorf("empty snap JSON = %s, want []", buf)
	}
}

func TestHistoryRecordStatus(t *testing.T) {
	h := NewHistoryStore(time.Second, time.Minute)
	ts := time.Unix(1_700_000_000, 0)
	status := statusResponse{
		Timestamp: ts,
		Sensors: []sensorStatus{
			{Name: "cpu_temp", Value: ptrFloat64(47.3), Unit: "°C"},
			{Name: "gpu_temp", Value: ptrFloat64(62.1), Unit: "°C"},
		},
		Fans: []fanStatus{
			{Name: "cpu_fan", PWM: 128, Duty: 50.2},
		},
	}
	h.RecordStatus(status)
	if got := h.Snapshot("cpu_temp", 0); len(got) != 1 || got[0].V != 47.3 {
		t.Errorf("cpu_temp snapshot = %+v, want single 47.3", got)
	}
	// Fans must be stored as duty %, not raw PWM — clients read
	// percentages, and the sparkline colour ramp is keyed to duty %.
	if got := h.Snapshot("cpu_fan", 0); len(got) != 1 || got[0].V != 50.2 {
		t.Errorf("cpu_fan snapshot = %+v, want single 50.2", got)
	}
}

func TestHistoryMemoryFootprint(t *testing.T) {
	// Typical desktop config: 5 sensors + 3 fans. The task budget is
	// < 100 KB of ring storage so every daemon can afford sparklines
	// without pinning an extra hundred kilobytes per launch.
	h := NewHistoryStore(2*time.Second, 60*time.Minute)
	metrics := []string{
		"cpu_temp", "gpu_temp", "mb_temp", "nvme_temp", "vrm_temp",
		"cpu_fan", "sys_fan", "gpu_fan",
	}
	now := time.Unix(1_700_000_000, 0)
	for _, m := range metrics {
		// Overfill to exercise the overflow path — the ring should
		// pin at cap whatever we throw at it.
		for i := 0; i < h.Capacity()+500; i++ {
			h.Record(m, float32(i%100), now.Add(time.Duration(i)*2*time.Second))
		}
	}
	bytes := h.ValueFootprintBytes()
	const budget = 100 * 1024
	if bytes > budget {
		t.Errorf("value footprint %d B > budget %d B", bytes, budget)
	}
	t.Logf("value footprint: %d B for %d metrics × %d slots × 4 B",
		bytes, len(metrics), h.Capacity())
}

func TestHistoryRoundValue(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{47.234567, 47.23},
		{47.235, 47.24},
		{0, 0},
		{-12.345, -12.35},
	}
	for _, c := range cases {
		if got := roundHistoryValue(c.in); got != c.want {
			t.Errorf("round(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	// NaN / Inf must not leak into the ring as exotic JSON tokens —
	// the Go stdlib json encoder refuses to encode them.
	if got := roundHistoryValue(asNaN()); got != 0 {
		t.Errorf("round(NaN) = %v, want 0", got)
	}
}

// asNaN returns a NaN float64 via an expression the compiler does not
// fold to a constant (which would trip a "constant NaN" error).
func asNaN() float64 {
	z := float64(0)
	return z / z
}

func TestHandleHistoryEndpoint(t *testing.T) {
	srv, cookie := newHistoryTestServer(t)
	defer srv.Close()

	client := &http.Client{}

	// Hammer the history store directly so we don't have to wait for
	// the sampler's ticker — deterministic and fast.
	underlying := historyStoreFromURL(t, srv.URL)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 10; i++ {
		underlying.Record("cpu_temp", float32(40+i), now.Add(time.Duration(i)*time.Second))
		underlying.Record("cpu_fan", float32(i*10), now.Add(time.Duration(i)*time.Second))
	}

	t.Run("single_metric", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history?metric=cpu_temp", nil)
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var out []HistorySample
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out) != 10 {
			t.Errorf("sample count = %d, want 10", len(out))
		}
	})

	t.Run("missing_metric_returns_empty_array", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history?metric=does_not_exist", nil)
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := httpReadAll(resp)
		if strings.TrimSpace(string(body)) != "[]" {
			t.Errorf("body = %q, want []", body)
		}
	})

	t.Run("all_metrics_envelope", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history", nil)
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var out HistoryResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.IntervalS != int(defaultSSEInterval.Seconds()) {
			t.Errorf("interval_s = %d, want %d", out.IntervalS, int(defaultSSEInterval.Seconds()))
		}
		if len(out.Metrics) < 2 {
			t.Errorf("metrics count = %d, want >= 2", len(out.Metrics))
		}
		if _, ok := out.Metrics["cpu_temp"]; !ok {
			t.Errorf("missing cpu_temp in metrics")
		}
	})

	t.Run("method_not_allowed", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/history", nil)
		req.AddCookie(cookie)
		// origin check requires matching Origin on non-GET requests.
		req.Header.Set("Origin", srv.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
	})

	t.Run("unauthenticated_blocked", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history?metric=cpu_temp", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func TestHandleHistoryWindowClamped(t *testing.T) {
	srv, cookie := newHistoryTestServer(t)
	defer srv.Close()
	underlying := historyStoreFromURL(t, srv.URL)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 60; i++ {
		underlying.Record("cpu_temp", float32(i), now.Add(time.Duration(i)*time.Second))
	}
	// window_s=5 with the default 2 s interval → at most ceil(5/2)+1 = 4.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history?metric=cpu_temp&window_s=5", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out []HistorySample
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) == 0 || len(out) > 10 {
		t.Errorf("window_s=5 returned %d samples, want a small bounded count", len(out))
	}
	// Ridiculous window_s should clamp to cap, not explode.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/history?metric=cpu_temp&window_s=99999999", nil)
	req2.AddCookie(cookie)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var out2 []HistorySample
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out2) > underlying.Capacity() {
		t.Errorf("huge window_s returned %d samples, expected <= %d", len(out2), underlying.Capacity())
	}
}

func TestHistorySamplerRunsOnTick(t *testing.T) {
	// Build a minimal Server that owns nothing but the fields the
	// sampler reads (cfg, logger, history). Bypassing New() keeps
	// the test free of the default sampler goroutine that would
	// race against the one we spawn below.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	live := config.Empty()
	// Write a real hwmon-style file so buildStatus produces a non-nil value.
	// /dev/null used to work when Value was float64 (zeroed on error), but now
	// that Value is *float64, failed reads produce nil and RecordStatus skips them.
	probeFile := t.TempDir() + "/temp1_input"
	if err := os.WriteFile(probeFile, []byte("47000\n"), 0600); err != nil {
		t.Fatalf("write probe file: %v", err)
	}
	live.Sensors = append(live.Sensors, config.Sensor{
		Name: "probe",
		Type: "hwmon",
		Path: probeFile,
	})
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)
	srv := &Server{
		cfg:     &cfgPtr,
		logger:  logger,
		history: NewHistoryStore(50*time.Millisecond, time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.runHistorySampler(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snap := srv.history.Snapshot("probe", 0); len(snap) >= 3 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("sampler never produced >= 3 samples for probe sensor")
}

// ── test helpers ────────────────────────────────────────────────

func newHistoryTestServer(t *testing.T) (*httptest.Server, *http.Cookie) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	password := "historypass123!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	sm := setupmgr.New(cal, logger)
	restart := make(chan struct{}, 1)
	srv := New(ctx, &cfgPtr, t.TempDir()+"/config.yaml", "", logger, cal, sm, restart, hwdiag.NewStore())

	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)
	historyTestServers.Store(ts.URL, srv)

	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader("password="+password))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(loginReq)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatalf("no session cookie after login")
	}
	return ts, cookie
}

// historyTestServers is a registry so test helpers can recover the
// Server struct from the httptest URL and poke the store directly.
// Keeps the helper surface small without exporting the whole Server.
var historyTestServers sync.Map

func historyStoreFromURL(t *testing.T, url string) *HistoryStore {
	t.Helper()
	v, ok := historyTestServers.Load(url)
	if !ok {
		t.Fatalf("server not registered for %s", url)
	}
	return v.(*Server).history
}

// httpReadAll is a tiny helper kept in the test file so this package
// doesn't grow an import of io/ioutil just for a one-liner.
func httpReadAll(r *http.Response) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// ptrFloat64 returns a pointer to v. Used in tests that construct
// sensorStatus literals now that Value is *float64.
func ptrFloat64(v float64) *float64 { return &v }
