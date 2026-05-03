package doctor

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/state"
)

// stubDetector lets tests assert what facts come out and what timeouts
// fire without writing real probe code.
type stubDetector struct {
	name         string
	facts        []Fact
	err          error
	probeDelay   time.Duration
	probePanic   any
	probeCount   atomic.Int32
	lastDeadline time.Time
}

func (s *stubDetector) Name() string { return s.name }

func (s *stubDetector) Probe(ctx context.Context, deps Deps) ([]Fact, error) {
	s.probeCount.Add(1)
	if s.probePanic != nil {
		panic(s.probePanic)
	}
	if dl, ok := ctx.Deadline(); ok {
		s.lastDeadline = dl
	}
	if s.probeDelay > 0 {
		select {
		case <-time.After(s.probeDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.facts, s.err
}

func freshRunner(t *testing.T, dets ...Detector) *Runner {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	suppress := NewSuppressionStore(st.KV, time.Now)
	now := func() time.Time { return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC) }
	return NewRunner(dets, suppress, recovery.Classify, now)
}

func TestRULE_DOCTOR_RUNNER_RunOnceAggregatesFacts(t *testing.T) {
	a := &stubDetector{name: "a", facts: []Fact{
		{Detector: "a", Severity: SeverityWarning, Title: "warn-a", EntityHash: "1"},
	}}
	b := &stubDetector{name: "b", facts: []Fact{
		{Detector: "b", Severity: SeverityBlocker, Title: "blk-b", EntityHash: "2"},
		{Detector: "b", Severity: SeverityOK, Title: "ok-b", EntityHash: "3"},
	}}
	r := freshRunner(t, a, b)

	rep, err := r.RunOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(rep.Facts) != 3 {
		t.Errorf("Facts len = %d, want 3", len(rep.Facts))
	}
	if rep.Severity != SeverityBlocker {
		t.Errorf("Severity = %v, want %v", rep.Severity, SeverityBlocker)
	}
	if rep.Schema != schemaVersion {
		t.Errorf("Schema = %q, want %q", rep.Schema, schemaVersion)
	}
	// Facts ordered by detector name (alphabetical), then entity_hash.
	if rep.Facts[0].Detector != "a" || rep.Facts[1].Detector != "b" || rep.Facts[2].Detector != "b" {
		t.Errorf("ordering wrong: %+v", rep.Facts)
	}
}

func TestRULE_DOCTOR_RUNNER_SkipExcludesDetector(t *testing.T) {
	a := &stubDetector{name: "a", facts: []Fact{{Detector: "a", Severity: SeverityWarning, EntityHash: "x"}}}
	b := &stubDetector{name: "b", facts: []Fact{{Detector: "b", Severity: SeverityBlocker, EntityHash: "y"}}}
	r := freshRunner(t, a, b)

	rep, err := r.RunOnce(context.Background(), RunOptions{Skip: []string{"b"}})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].Detector != "a" {
		t.Errorf("Skip didn't exclude b; facts = %+v", rep.Facts)
	}
	if a.probeCount.Load() != 1 || b.probeCount.Load() != 0 {
		t.Errorf("probe counts: a=%d b=%d (want 1, 0)", a.probeCount.Load(), b.probeCount.Load())
	}
}

func TestRULE_DOCTOR_RUNNER_OnlyIncludesNamed(t *testing.T) {
	a := &stubDetector{name: "a", facts: []Fact{{Detector: "a", Severity: SeverityWarning, EntityHash: "x"}}}
	b := &stubDetector{name: "b", facts: []Fact{{Detector: "b", Severity: SeverityBlocker, EntityHash: "y"}}}
	r := freshRunner(t, a, b)

	rep, err := r.RunOnce(context.Background(), RunOptions{Only: []string{"b"}})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].Detector != "b" {
		t.Errorf("Only didn't restrict to b; facts = %+v", rep.Facts)
	}
}

func TestRULE_DOCTOR_RUNNER_PanicSurfacesAsDetectorError(t *testing.T) {
	good := &stubDetector{name: "good", facts: []Fact{{Detector: "good", Severity: SeverityOK, EntityHash: "z"}}}
	bad := &stubDetector{name: "bad", probePanic: "boom"}
	r := freshRunner(t, good, bad)

	rep, err := r.RunOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(rep.DetectorErrors) != 1 {
		t.Fatalf("DetectorErrors = %d, want 1; got %+v", len(rep.DetectorErrors), rep.DetectorErrors)
	}
	if rep.DetectorErrors[0].Detector != "bad" {
		t.Errorf("error attributed to wrong detector: %+v", rep.DetectorErrors[0])
	}
	// Other detectors still ran.
	if len(rep.Facts) != 1 || rep.Facts[0].Detector != "good" {
		t.Errorf("good detector's facts dropped after bad panic; facts = %+v", rep.Facts)
	}
}

func TestRULE_DOCTOR_RUNNER_PerDetectorTimeout(t *testing.T) {
	slow := &stubDetector{name: "slow", probeDelay: 100 * time.Millisecond}
	r := freshRunner(t, slow)

	start := time.Now()
	rep, err := r.RunOnce(context.Background(), RunOptions{PerDetectorTimeout: 10 * time.Millisecond})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("RunOnce took %v; expected <50ms (timeout=10ms)", elapsed)
	}
	if len(rep.DetectorErrors) != 1 {
		t.Errorf("expected 1 DetectorError for timed-out detector; got %+v", rep.DetectorErrors)
	}
}

func TestRULE_DOCTOR_RUNNER_RespectsContextCancel(t *testing.T) {
	r := freshRunner(t, &stubDetector{name: "any"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.RunOnce(ctx, RunOptions{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("RunOnce on cancelled ctx returned err=%v; want wrapping context.Canceled", err)
	}
}

func TestRULE_DOCTOR_RUNNER_SuppressionFiltersFacts(t *testing.T) {
	det := &stubDetector{name: "kernel_update", facts: []Fact{
		{Detector: "kernel_update", Severity: SeverityWarning, EntityHash: "abc"},
		{Detector: "kernel_update", Severity: SeverityWarning, EntityHash: "def"},
	}}

	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := func() time.Time { return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC) }
	suppress := NewSuppressionStore(st.KV, now)
	if err := suppress.Suppress("kernel_update", "abc", "test", time.Hour); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	r := NewRunner([]Detector{det}, suppress, recovery.Classify, now)
	rep, err := r.RunOnce(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].EntityHash != "def" {
		t.Errorf("suppression didn't filter; facts = %+v", rep.Facts)
	}
}

func TestRULE_DOCTOR_RUNNER_SeverityRollup(t *testing.T) {
	cases := []struct {
		facts []Fact
		want  Severity
	}{
		{nil, SeverityOK},
		{[]Fact{{Severity: SeverityOK}}, SeverityOK},
		{[]Fact{{Severity: SeverityOK}, {Severity: SeverityWarning}}, SeverityWarning},
		{[]Fact{{Severity: SeverityWarning}, {Severity: SeverityBlocker}, {Severity: SeverityOK}}, SeverityBlocker},
	}
	for i, c := range cases {
		if got := severityRollup(c.facts); got != c.want {
			t.Errorf("case %d: severityRollup() = %v, want %v", i, got, c.want)
		}
	}
}
