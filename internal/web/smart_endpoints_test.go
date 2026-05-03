package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		srv.handleSmartStatus(w, req)
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
	srv.handleSmartChannels(w, req)

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
	src := smartStatusResponse{
		Enabled:       true,
		Preset:        "balanced",
		GlobalState:   "warming",
		Channels:      4,
		WarmingUp:     2,
		Converged:     2,
		ConfidenceMin: 0.45,
		ConfidenceMax: 0.92,
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var dst smartStatusResponse
	if err := json.Unmarshal(b, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst.Channels != src.Channels || dst.GlobalState != src.GlobalState ||
		fmt.Sprintf("%.4f", dst.ConfidenceMin) != fmt.Sprintf("%.4f", src.ConfidenceMin) {
		t.Errorf("round-trip drifted: src=%+v dst=%+v", src, dst)
	}
}
