package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/aggregator"
	layera "github.com/ventd/ventd/internal/confidence/layer_a"
)

// ─────────────────────────────────────────────────────────────────────
// v0.5.12 #104: smart-mode endpoints — /api/v1/smart/{status,channels}.
//
// These tests cover behaviour + a load-test runner that verifies the
// endpoints stay responsive under concurrent polling. The load runner
// is the Phoenix-requested guard against accidental serialisation
// between the controller hot loop and the web handler — handlers MUST
// hit aggregator/coupling/marginal SnapshotAll (lock-free atomic
// reads) and never block.
// ─────────────────────────────────────────────────────────────────────

func TestHandleSmartStatus_NonGET_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/v1/smart/status", nil)
		w := httptest.NewRecorder()
		srv.gateMethods([]string{http.MethodGet}, srv.handleSmartStatus)(w, req)
		if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/v1/smart/status: status = %d, want %d", m, got, http.StatusMethodNotAllowed)
		}
	}
}

func TestHandleSmartStatus_NoAggregator_ReportsDisabled(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()
	// harness leaves aggregator nil — represents monitor-only mode.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/smart/status", nil)
	w := httptest.NewRecorder()
	srv.handleSmartStatus(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", got, w.Body.String())
	}
	var body smartStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Enabled {
		t.Errorf("Enabled = true, want false (aggregator nil)")
	}
	if body.Preset == "" {
		t.Errorf("Preset is empty")
	}
}

func TestHandleSmartChannels_NonGET_RejectedAs405(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/smart/channels", nil)
	w := httptest.NewRecorder()
	srv.gateMethods([]string{http.MethodGet}, srv.handleSmartChannels)(w, req)

	if got := w.Result().StatusCode; got != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

func TestHandleSmartChannels_NoAggregator_ReturnsEmptyArray(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/smart/channels", nil)
	w := httptest.NewRecorder()
	srv.handleSmartChannels(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	var body []smartChannelEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty array, got %d entries", len(body))
	}
}

// TestSmartEndpoints_LoadTest_ConcurrentPollsStayResponsive is the
// main delivery for #104's "load-test runner" — Phoenix's HIL
// requirement that the smart endpoints never block the controller
// hot loop. Spawns 8 goroutines hammering /smart/status + /smart/channels
// in parallel for 2 seconds and asserts:
//
//   - p99 latency stays under 50 ms (atomic.Pointer reads should be
//     in the microsecond range; any millisecond-level reading means a
//     mutex contended path)
//   - zero errors (5xx, parse failures, panics)
//   - >= 800 successful requests across all goroutines (8 × 100/s
//     conservative floor; in practice closer to 8 × 5000/s)
//
// The harness uses nil aggregator (monitor-only path) so the loop
// exercises the same code branches that fire when smart-mode is
// disabled — the handler still has to return a valid response in
// that mode without falling through to a panic.
func TestSmartEndpoints_LoadTest_ConcurrentPollsStayResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("load test: skipping in -short mode")
	}
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	const (
		workers  = 8
		duration = 2 * time.Second
		// p99 latency budget. Atomic reads should be sub-millisecond;
		// 50 ms gives ample headroom for httptest's internal overhead
		// + GC pauses on a busy CI runner.
		p99Budget = 50 * time.Millisecond
	)

	var (
		totalReqs atomic.Uint64
		errReqs   atomic.Uint64
		latencies = make([]time.Duration, 0, 32_000)
		latMu     sync.Mutex
	)

	deadline := time.Now().Add(duration)
	endpoints := []string{"/api/v1/smart/status", "/api/v1/smart/channels"}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			rng := time.Now().UnixNano() + int64(wid)
			for time.Now().Before(deadline) {
				ep := endpoints[rng%int64(len(endpoints))]
				rng++
				req := httptest.NewRequest(http.MethodGet, ep, nil)
				rec := httptest.NewRecorder()
				start := time.Now()
				switch ep {
				case "/api/v1/smart/status":
					srv.handleSmartStatus(rec, req)
				case "/api/v1/smart/channels":
					srv.handleSmartChannels(rec, req)
				}
				lat := time.Since(start)
				totalReqs.Add(1)
				if rec.Result().StatusCode != http.StatusOK {
					errReqs.Add(1)
				}
				latMu.Lock()
				latencies = append(latencies, lat)
				latMu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	total := totalReqs.Load()
	errs := errReqs.Load()
	if total < 800 {
		t.Errorf("throughput too low: %d reqs in %v (want >= 800)", total, duration)
	}
	if errs > 0 {
		t.Errorf("got %d non-200 responses out of %d", errs, total)
	}

	// p99 latency
	if len(latencies) > 0 {
		// In-place insertion sort would be O(n²) on 30k samples;
		// quickselect via sort is fine here.
		sortDurations(latencies)
		idx := int(float64(len(latencies)) * 0.99)
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		p99 := latencies[idx]
		if p99 > p99Budget {
			t.Errorf("p99 latency %v exceeds %v budget (n=%d)", p99, p99Budget, len(latencies))
		}
		t.Logf("load test: %d reqs, %d errs, p99=%v over %v", total, errs, p99, duration)
	}
}

func sortDurations(s []time.Duration) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

// TestHandleSmartChannels_StableOrder_AcrossPolls verifies the v0.8.x
// fix for the dashboard tile-shuffle bug: aggregator.SnapshotAll iterates
// a map and gives no order guarantee, so the per-channel grid would
// reshuffle on every poll. The handler sorts by ChannelID so the order
// is byte-stable across requests.
func TestHandleSmartChannels_StableOrder_AcrossPolls(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Wire a real aggregator with channels admitted in non-alphabetical
	// order. Without the sort, repeated calls return them in map-walk
	// order (different each iteration). With the sort, every call yields
	// the same lexicographic order.
	agg := aggregator.New(aggregator.Config{})
	srv.aggregator = agg
	now := time.Now()
	for _, id := range []string{
		"/sys/class/hwmon/hwmon9/pwm7",
		"gpu0:fan0",
		"/sys/class/hwmon/hwmon9/pwm1",
		"/sys/class/hwmon/hwmon9/pwm3",
	} {
		agg.Tick(id, 0.5, 0.5, 0.5, [3]bool{}, true, now)
	}

	want := []string{
		"/sys/class/hwmon/hwmon9/pwm1",
		"/sys/class/hwmon/hwmon9/pwm3",
		"/sys/class/hwmon/hwmon9/pwm7",
		"gpu0:fan0",
	}

	// 5 polls — every one must yield the same order.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/smart/channels", nil)
		w := httptest.NewRecorder()
		srv.handleSmartChannels(w, req)
		if got := w.Result().StatusCode; got != http.StatusOK {
			t.Fatalf("poll %d: status = %d, want 200", i, got)
		}
		var body []smartChannelEntry
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("poll %d: unmarshal: %v", i, err)
		}
		if len(body) != len(want) {
			t.Fatalf("poll %d: got %d entries, want %d", i, len(body), len(want))
		}
		for j, e := range body {
			if e.ChannelID != want[j] {
				t.Errorf("poll %d entry %d: ChannelID = %q, want %q (full order: %v)",
					i, j, e.ChannelID, want[j], channelIDs(body))
			}
		}
	}
}

// TestHandleConfidenceStatus_StableOrder_AcrossPolls — same invariant for
// the /api/v1/confidence/status payload, which the dashboard pill grid
// reads from. Requires both aggregator and layerA to be wired.
func TestHandleConfidenceStatus_StableOrder_AcrossPolls(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	agg := aggregator.New(aggregator.Config{})
	la, err := layera.New(layera.Config{})
	if err != nil {
		t.Fatalf("layer_a New: %v", err)
	}
	srv.aggregator = agg
	srv.layerA = la

	now := time.Now()
	ids := []string{
		"/sys/class/hwmon/hwmon9/pwm7",
		"gpu0:fan0",
		"/sys/class/hwmon/hwmon9/pwm1",
		"/sys/class/hwmon/hwmon9/pwm3",
	}
	for _, id := range ids {
		agg.Tick(id, 0.5, 0.5, 0.5, [3]bool{}, true, now)
		if err := la.Admit(id, 0, 0, now); err != nil {
			t.Fatalf("layer_a Admit %s: %v", id, err)
		}
	}

	wantOrder := []string{
		"/sys/class/hwmon/hwmon9/pwm1",
		"/sys/class/hwmon/hwmon9/pwm3",
		"/sys/class/hwmon/hwmon9/pwm7",
		"gpu0:fan0",
	}

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/confidence/status", nil)
		w := httptest.NewRecorder()
		srv.handleConfidenceStatus(w, req)
		if got := w.Result().StatusCode; got != http.StatusOK {
			t.Fatalf("poll %d: status = %d, want 200", i, got)
		}
		var body confidenceStatus
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("poll %d: unmarshal: %v", i, err)
		}
		if len(body.Channels) != len(wantOrder) {
			t.Fatalf("poll %d: got %d channels, want %d", i, len(body.Channels), len(wantOrder))
		}
		for j, e := range body.Channels {
			if e.ChannelID != wantOrder[j] {
				t.Errorf("poll %d entry %d: ChannelID = %q, want %q", i, j, e.ChannelID, wantOrder[j])
			}
		}
	}
}

// TestSmartGlobalHyst_HoldsBriefRegressionFromConverged verifies the
// v0.8.x topbar-flap fix: a converged → warming regression that
// arrives within smartGlobalHystWindow returns the prior "converged"
// to the caller. Anything older than the window commits.
func TestSmartGlobalHyst_HoldsBriefRegressionFromConverged(t *testing.T) {
	var h smartGlobalHystState
	t0 := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)

	if got := h.observe("converged", t0); got != "converged" {
		t.Errorf("first observe: got %q, want converged", got)
	}
	// Brief regression within the hysteresis window — held.
	if got := h.observe("warming", t0.Add(2*time.Second)); got != "converged" {
		t.Errorf("brief regression: got %q, want converged (smoothed)", got)
	}
	// Improvement back to converged — committed.
	if got := h.observe("converged", t0.Add(3*time.Second)); got != "converged" {
		t.Errorf("recovery: got %q, want converged", got)
	}
	// Long regression — committed.
	if got := h.observe("warming", t0.Add(3*time.Second+smartGlobalHystWindow+time.Second)); got != "warming" {
		t.Errorf("sustained regression: got %q, want warming", got)
	}
}

// TestSmartGlobalHyst_DoesNotSmoothDriftingOrRefused verifies that
// the smoothing applies ONLY to the warming/cold-start transients
// produced by the marginal-shard warm-start path — genuine
// deteriorations to "drifting" or "refused" reach the operator
// immediately.
func TestSmartGlobalHyst_DoesNotSmoothDriftingOrRefused(t *testing.T) {
	t0 := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{"drifting", "refused"} {
		var h smartGlobalHystState
		_ = h.observe("converged", t0)
		if got := h.observe(raw, t0.Add(2*time.Second)); got != raw {
			t.Errorf("regression to %q: smoothed to %q, must commit immediately", raw, got)
		}
	}
}

// TestSmartGlobalHyst_FirstObservationCommitsImmediately — the
// hysteresis must not artificially delay the first state report.
func TestSmartGlobalHyst_FirstObservationCommitsImmediately(t *testing.T) {
	var h smartGlobalHystState
	t0 := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	if got := h.observe("warming", t0); got != "warming" {
		t.Errorf("first observe of warming: got %q, want warming", got)
	}
}

func channelIDs(entries []smartChannelEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ChannelID
	}
	return out
}

// Sanity check — handler signature didn't drift.
func TestSmartEndpoints_HandlerSignaturesPreserved(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Each handler must accept (ResponseWriter, *Request) — verified
	// by direct invocation with a recorder. Compile-time guarantee
	// that the route table can wire them.
	for _, h := range []http.HandlerFunc{srv.handleSmartStatus, srv.handleSmartChannels} {
		req := httptest.NewRequest(http.MethodGet, "/_test", nil)
		w := httptest.NewRecorder()
		h(w, req)
		if w.Result().StatusCode == 0 {
			t.Errorf("handler returned no status code")
		}
	}
}

// Compile-time guard that the smartStatusResponse + smartChannelEntry
// structs marshal to stable JSON. We don't need to assert exact bytes
// (field order varies), but any field that changes shape without a
// schema bump shows up here. The smoke test just ensures Marshal
// doesn't panic on a populated struct.
func TestSmartStatus_JSONMarshalRoundTrip(t *testing.T) {
	cmin, cmax := 0.45, 0.92
	src := smartStatusResponse{
		Enabled:       true,
		Preset:        "balanced",
		GlobalState:   "warming",
		Channels:      4,
		WarmingUp:     2,
		Converged:     2,
		ConfidenceMin: &cmin,
		ConfidenceMax: &cmax,
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var dst smartStatusResponse
	if err := json.Unmarshal(b, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst.Channels != src.Channels || dst.GlobalState != src.GlobalState {
		t.Errorf("round-trip drifted: src=%+v dst=%+v", src, dst)
	}
	if dst.ConfidenceMin == nil || fmt.Sprintf("%.4f", *dst.ConfidenceMin) != fmt.Sprintf("%.4f", *src.ConfidenceMin) {
		t.Errorf("ConfidenceMin round-trip drifted: src=%v dst=%v", src.ConfidenceMin, dst.ConfidenceMin)
	}
}

// TestSmartStatus_NullConfidenceWhenAllPreWarmup pins B1 from the
// v0.5.26 bug-floor probe: when every channel reports w_pred=0
// (cold-start window per RULE-AGG-COLDSTART-01, or every channel is
// still warming), ConfidenceMin/Max MUST emit JSON null rather than a
// literal 0.0 — otherwise the smart-mode card renders a misleading
// "Conf min: 0.00 / Conf max: 0.00".
func TestSmartStatus_NullConfidenceWhenAllPreWarmup(t *testing.T) {
	// Direct unit test of the JSON shape: with both pointers nil, the
	// API must emit JSON null on both fields so web/smart.js's
	// `val == null` branch in sysRow renders "—".
	out := smartStatusResponse{
		Enabled:     true,
		Preset:      "silent",
		GlobalState: "warming",
		Channels:    2,
		WarmingUp:   2,
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"confidence_min":null`) {
		t.Errorf("confidence_min should serialize as null, got: %s", got)
	}
	if !strings.Contains(got, `"confidence_max":null`) {
		t.Errorf("confidence_max should serialize as null, got: %s", got)
	}
}
