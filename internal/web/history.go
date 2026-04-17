package web

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Session D 3c — time-series sparklines. Per-metric ring buffers keep
// the last hour of sensor and fan samples so the dashboard can render
// tiny historical sparklines without a round-trip per tick. Lost on
// daemon restart by design: sparklines exist for at-a-glance trend
// spotting, not for long-term telemetry storage.

const (
	// historyDefaultWindow caps how far back /api/history looks when
	// the client does not specify window_s. One hour matches the ring
	// capacity at the default sample interval.
	historyDefaultWindow = 60 * time.Minute

	// historyDefaultInterval is the sampler tick rate when no
	// explicit value is supplied. Matches the SSE cadence (2 s) so
	// a browser tab that stays open sees one sample per visible
	// status frame.
	historyDefaultInterval = 2 * time.Second
)

// HistoryStore keeps a fixed-size ring buffer of sampled values per
// metric. Values are stored as float32 — ±16M integer range, ~7
// decimal digits — which is ample for temperatures (°C), fan duty
// (%), voltages (V) and power (W). float64 would double the memory
// cost without adding anything a human can read on a 30 px sparkline.
//
// Per-sample timestamps are NOT stored. The ring carries the wall
// time of the newest sample; older timestamps are reconstructed as
// newest - (n-1-i) * interval at read time. This halves the
// footprint vs. parallel time+value slices, at the cost of 0–1 s
// skew on older samples — immaterial for an at-a-glance chart.
type HistoryStore struct {
	mu       sync.RWMutex
	interval time.Duration
	cap      int
	rings    map[string]*historyRing
}

// historyRing is a single metric's circular buffer. head points at
// the next write slot; (head-1+cap)%cap is the newest sample. n
// grows up to cap and then stays pinned while older samples are
// overwritten in place.
type historyRing struct {
	values []float32
	head   int
	n      int
	newest time.Time
}

// HistorySample is one (timestamp, value) pair rendered into JSON.
// Timestamp is a Unix epoch second so the client can feed it to
// Date.parse without a format shim; value is a float64 to leave room
// for future metrics with more precision than the current float32
// storage.
type HistorySample struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// HistoryResponse is the shape returned when the client requests all
// metrics at once (no ?metric query). Single-metric requests return
// the raw []HistorySample directly for minimal response size.
type HistoryResponse struct {
	Metrics    map[string][]HistorySample `json:"metrics"`
	IntervalS  int                        `json:"interval_s"`
	WindowS    int                        `json:"window_s"`
	CapSamples int                        `json:"cap_samples"`
}

// NewHistoryStore sizes the per-metric ring to hold window at
// interval spacing. Defaults (2 s, 60 min) apply when either arg is
// zero. The cap is held slightly above window/interval so a sampler
// that ticks faster than expected for a brief period still fits a
// full window.
func NewHistoryStore(interval, window time.Duration) *HistoryStore {
	if interval <= 0 {
		interval = historyDefaultInterval
	}
	if window <= 0 {
		window = historyDefaultWindow
	}
	c := int(window/interval) + 1
	if c < 2 {
		c = 2
	}
	return &HistoryStore{
		interval: interval,
		cap:      c,
		rings:    make(map[string]*historyRing),
	}
}

// Record appends one sample for metric. Thread-safe; called from the
// sampler goroutine. A new ring is lazily created the first time a
// metric is seen — adding a sensor via /api/config does not require
// a separate allocate step.
func (h *HistoryStore) Record(metric string, v float32, at time.Time) {
	if metric == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rings[metric]
	if !ok {
		r = &historyRing{values: make([]float32, h.cap)}
		h.rings[metric] = r
	}
	r.values[r.head] = v
	r.head = (r.head + 1) % h.cap
	if r.n < h.cap {
		r.n++
	}
	r.newest = at
}

// RecordStatus fans one buildStatus() snapshot out to the per-metric
// rings. Sensors keep their raw value + unit; fans store duty
// percentage (0–100) so a sparkline reads the same way the card's
// duty bar does.
func (h *HistoryStore) RecordStatus(s statusResponse) {
	t := s.Timestamp
	if t.IsZero() {
		t = time.Now().UTC()
	}
	for _, sensor := range s.Sensors {
		h.Record(sensor.Name, float32(sensor.Value), t)
	}
	for _, fan := range s.Fans {
		h.Record(fan.Name, float32(fan.Duty), t)
	}
}

// Snapshot returns up to window worth of samples for metric, in
// oldest→newest order. Empty window (<=0) returns every stored
// sample. Missing metric returns an empty slice, never nil — the
// zero-value JSON shape downstream is `[]`, not `null`.
func (h *HistoryStore) Snapshot(metric string, window time.Duration) []HistorySample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rings[metric]
	if !ok || r.n == 0 {
		return []HistorySample{}
	}
	want := r.n
	if window > 0 {
		if capped := int(window/h.interval) + 1; capped < want {
			want = capped
		}
	}
	out := make([]HistorySample, 0, want)
	start := (r.head - r.n + h.cap) % h.cap
	skip := r.n - want
	newestTs := r.newest.Unix()
	intervalS := int64(h.interval.Seconds())
	if intervalS < 1 {
		intervalS = 1
	}
	for i := 0; i < want; i++ {
		idx := (start + skip + i) % h.cap
		age := int64(want-1-i) * intervalS
		out = append(out, HistorySample{
			T: newestTs - age,
			V: roundHistoryValue(float64(r.values[idx])),
		})
	}
	return out
}

// SnapshotAll returns Snapshot for every tracked metric in a single
// lock window. Used by /api/history with no ?metric param so a
// fresh browser tab can seed its client-side buffer in one request.
func (h *HistoryStore) SnapshotAll(window time.Duration) map[string][]HistorySample {
	h.mu.RLock()
	names := make([]string, 0, len(h.rings))
	for name := range h.rings {
		names = append(names, name)
	}
	h.mu.RUnlock()
	// Sort so the response is deterministic; simplifies cache
	// debugging and keeps test assertions stable.
	sort.Strings(names)
	out := make(map[string][]HistorySample, len(names))
	for _, name := range names {
		out[name] = h.Snapshot(name, window)
	}
	return out
}

// Interval returns the sampler's tick period. Exposed for the
// /api/history payload so clients can compute their own x-axis
// scale without a second round trip.
func (h *HistoryStore) Interval() time.Duration { return h.interval }

// Capacity returns the per-metric ring size. Useful for tests and
// for the /api/history payload so clients can bound their
// client-side buffers to match.
func (h *HistoryStore) Capacity() int { return h.cap }

// ValueFootprintBytes estimates the bytes used by all ring value
// arrays. Struct and map overhead are excluded — they're O(entries)
// and O(1) per ring respectively, dwarfed by the float32 arrays
// once any realistic config is running. Used by the memory test so
// regressions fail fast instead of creeping past the 100 KB budget.
func (h *HistoryStore) ValueFootprintBytes() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rings) * h.cap * 4
}

// roundHistoryValue trims each sample to 2 decimal places before
// serialisation. Sparklines paint at ~80×30 px — sub-decimal jitter
// is invisible, and trimming shaves ~30 % off the JSON payload for a
// full 1800-point window.
func roundHistoryValue(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

// runHistorySampler ticks at the store's interval and writes one
// sample per sensor and fan to the ring. Exits on ctx cancellation
// so daemon shutdown doesn't leave a goroutine leaking. Matches the
// expireSetupToken lifecycle pattern already in server.go.
func (s *Server) runHistorySampler(ctx context.Context) {
	interval := s.history.Interval()
	t := time.NewTicker(interval)
	defer t.Stop()
	// Record a sample immediately so the buffer isn't empty for the
	// first `interval` after daemon start. Matters for tests that
	// drop the tick rate into the hundreds of milliseconds and want
	// to see at least one point before their deadline.
	s.history.RecordStatus(s.buildStatus())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.history.RecordStatus(s.buildStatus())
		}
	}
}

// handleHistory GET /api/history?metric=<name>&window_s=<int>
//
//   - metric omitted → returns HistoryResponse (all metrics plus
//     interval/window metadata). Lets a freshly-mounted browser tab
//     seed its client-side buffer in one request instead of N.
//   - metric present → returns []HistorySample for that one metric.
//     Matches the task spec verbatim.
//
// window_s is clamped to the ring's capacity so a pathological
// window_s=999999999 request can't run the server out of RAM
// building a response larger than anything ever stored.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	window := s.resolveHistoryWindow(r.URL.Query().Get("window_s"))

	metric := r.URL.Query().Get("metric")
	if metric != "" {
		s.writeJSON(r, w, s.history.Snapshot(metric, window))
		return
	}

	resp := HistoryResponse{
		Metrics:    s.history.SnapshotAll(window),
		IntervalS:  int(s.history.Interval().Seconds()),
		WindowS:    int(window.Seconds()),
		CapSamples: s.history.Capacity(),
	}
	if resp.IntervalS < 1 {
		resp.IntervalS = 1
	}
	s.writeJSON(r, w, resp)
}

// resolveHistoryWindow parses the window_s query param and clamps it
// to a sensible range. Empty / non-numeric / non-positive values fall
// back to the store's full capacity-worth of history so the client
// doesn't have to know the daemon's sampler interval to get everything.
func (s *Server) resolveHistoryWindow(raw string) time.Duration {
	cap := time.Duration(s.history.Capacity()) * s.history.Interval()
	if cap <= 0 {
		cap = historyDefaultWindow
	}
	if raw == "" {
		return cap
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return cap
	}
	w := time.Duration(n) * time.Second
	if w > cap {
		w = cap
	}
	return w
}
