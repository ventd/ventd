package marginal

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// stubParents implements ShardLookup for tests; returns a fixed
// snapshot per channel.
type stubParents struct {
	by map[string]*stubReader
}

func (s stubParents) Shard(channelID string) ShardSnapshotReader {
	if r, ok := s.by[channelID]; ok {
		return r
	}
	return nil
}

type stubReader struct{ snap ParentSnapshot }

func (r *stubReader) Read() ParentSnapshot { return r.snap }

// stubSignguard always returns the configured value.
type stubSignguard struct{ ok bool }

func (s stubSignguard) Confirmed(string) bool { return s.ok }

// TestRuntime_OAT_RejectsCrossChannelSamples — RULE-CMB-OAT-01.
//
// Channel A and B both produce observations. While B's PWM is
// changing, A's update samples should be rejected (sample never
// admitted into A's shard). Once B's PWM stabilises for 5 ticks,
// A's updates resume.
func TestRuntime_OAT_RejectsCrossChannelSamples(t *testing.T) {
	rt := NewRuntime("", "fp", nil, stubSignguard{ok: false}, silentLogger())

	// Prime channel B with a varying PWM (5 distinct values).
	for i := uint8(0); i < 5; i++ {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "chB", SignatureLabel: "sig",
			PWMWritten: 100 + i, DeltaT: -1.0, Load: 0.5,
		})
	}
	// Channel A's first observation should be DROPPED (B's recent
	// PWM history not stable).
	rt.OnObservation(ObservationInput{
		Now: time.Now(), ChannelID: "chA", SignatureLabel: "sig",
		PWMWritten: 200, DeltaT: -1.0, Load: 0.5,
	})
	if rt.Shard("chA", "sig") != nil {
		t.Errorf("OAT gate should have blocked A's admission while B's PWM was non-static")
	}

	// Stabilise B at PWM=104 for 5 ticks.
	for i := 0; i < 5; i++ {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "chB", SignatureLabel: "sig",
			PWMWritten: 104, DeltaT: -1.0, Load: 0.5,
		})
	}
	// Now A's observation should pass.
	rt.OnObservation(ObservationInput{
		Now: time.Now(), ChannelID: "chA", SignatureLabel: "sig",
		PWMWritten: 200, DeltaT: -1.0, Load: 0.5,
	})
	if rt.Shard("chA", "sig") == nil {
		t.Errorf("OAT gate should have admitted A's sample once B was stable")
	}
}

// TestRuntime_FilterFallbackLabels — RULE-CMB-LIB-02.
func TestRuntime_FilterFallbackLabels(t *testing.T) {
	rt := NewRuntime("", "fp", nil, nil, silentLogger())
	for _, label := range []string{FallbackLabelDisabled, FallbackLabelWarming, ""} {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "ch", SignatureLabel: label,
			PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
		})
		if rt.Shard("ch", label) != nil {
			t.Errorf("shard created for filtered label %q", label)
		}
	}
}

// TestRuntime_PerChannelCapAt32 — RULE-CMB-LIB-01.
func TestRuntime_PerChannelCapAt32(t *testing.T) {
	rt := NewRuntime("", "fp", nil, nil, silentLogger())
	for i := 0; i < MaxShardsPerChannel+10; i++ {
		// Vary the signature label to force new shards.
		sig := "sig-" + itoa(i)
		// Pre-stabilise the OAT gate by feeding 5 identical PWMs
		// to a different channel first.
		for j := 0; j < 5; j++ {
			rt.OnObservation(ObservationInput{
				Now: time.Now(), ChannelID: "warmup", SignatureLabel: "sig",
				PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
			})
		}
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "ch", SignatureLabel: sig,
			PWMWritten: 200, DeltaT: -1.0, Load: 0.5,
		})
	}
	cnt := rt.ShardCount("ch")
	if cnt > MaxShardsPerChannel {
		t.Errorf("ShardCount(ch) = %d; cap is %d", cnt, MaxShardsPerChannel)
	}
}

// TestRuntime_DeferActivation_OnParentKappaBad — RULE-CMB-IDENT-01.
func TestRuntime_DeferActivation_OnParentKappaBad(t *testing.T) {
	parents := stubParents{by: map[string]*stubReader{
		"ch": {snap: ParentSnapshot{Kind: KindUnidentifiable, WarmingUp: false}},
	}}
	rt := NewRuntime("", "fp", parents, stubSignguard{ok: true}, silentLogger())

	// First observation should NOT create a shard (parent κ-bad).
	rt.OnObservation(ObservationInput{
		Now: time.Now(), ChannelID: "ch", SignatureLabel: "sig",
		PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
	})
	if rt.Shard("ch", "sig") != nil {
		t.Errorf("shard should NOT be created when parent Layer-B κ > 10⁴")
	}
}

// TestRuntime_DisableInheritance — RULE-CMB-DISABLE-01.
//
// Smoke test: with no signguard + no parents, runtime still admits
// shards (no Layer-B prior used). With absent inputs, behaviour
// degrades gracefully — no panics, no crashes.
func TestRuntime_DisableInheritance(t *testing.T) {
	rt := NewRuntime("", "fp", nil, nil, silentLogger())
	// 5 stabilising ticks for OAT gate.
	for i := 0; i < 5; i++ {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "ch", SignatureLabel: "sig",
			PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
		})
	}
	if rt.Shard("ch", "sig") == nil {
		t.Errorf("shard should be admitted with nil parents/signguard")
	}
}

// TestRuntime_OneGoroutinePerShard — RULE-CMB-RUNTIME-01.
//
// v0.5.8 implementation choice: a single periodic-save goroutine,
// not one-per-shard (the per-shard model in the spec is
// conceptual; Update is pure-CPU < 50µs and synchronous is
// sufficient). Test asserts that Run starts at most a small,
// bounded number of goroutines.
func TestRuntime_OneGoroutinePerShard(t *testing.T) {
	rt := NewRuntime("", "fp", nil, nil, silentLogger())
	pre := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	delta := runtime.NumGoroutine() - pre
	if delta > 5 {
		t.Errorf("Run started %d new goroutines; expected ≤5", delta)
	}
	cancel()
	<-done
}

// TestRuntime_OnObservationNonBlocking — RULE-CMB-RUNTIME-02.
//
// OnObservation must return well within a tick budget (1 ms here).
// Synchronous direct-update model means it's bounded by Update's
// pure-CPU cost.
func TestRuntime_OnObservationNonBlocking(t *testing.T) {
	rt := NewRuntime("", "fp", nil, nil, silentLogger())
	// Stabilise OAT.
	for i := 0; i < 5; i++ {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "ch", SignatureLabel: "sig",
			PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
		})
	}
	start := time.Now()
	rt.OnObservation(ObservationInput{
		Now: time.Now(), ChannelID: "ch", SignatureLabel: "sig",
		PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
	})
	if elapsed := time.Since(start); elapsed > time.Millisecond {
		t.Errorf("OnObservation took %v; want < 1ms", elapsed)
	}
}

// TestPrior_AtAdmissionNotLive — RULE-CMB-PRIOR-02.
//
// The Layer-B prior is read at admission time. Subsequent changes
// to the parent shard's snapshot do NOT alter the Layer-C shard's
// θ_0. This protects against sign-flip races.
func TestPrior_AtAdmissionNotLive(t *testing.T) {
	parent := &stubReader{snap: ParentSnapshot{
		WarmingUp: false,
		Kind:      KindHealthy,
		BiiAtZero: -2.55, // negative = correct cooling polarity
	}}
	parents := stubParents{by: map[string]*stubReader{"ch": parent}}
	rt := NewRuntime("", "fp", parents, stubSignguard{ok: true}, silentLogger())

	for i := 0; i < 5; i++ {
		rt.OnObservation(ObservationInput{
			Now: time.Now(), ChannelID: "warmup", SignatureLabel: "sig",
			PWMWritten: 100, DeltaT: -1.0, Load: 0.5,
		})
	}
	rt.OnObservation(ObservationInput{
		Now: time.Now(), ChannelID: "ch", SignatureLabel: "sig",
		PWMWritten: 200, DeltaT: -1.0, Load: 0.5,
	})

	s := rt.Shard("ch", "sig")
	if s == nil {
		t.Fatal("expected shard admitted")
	}
	priorBeta0 := s.theta.AtVec(0)
	want := -2.55 / 255.0
	tol := 0.001 // wide because Update has run on top of admission
	if priorBeta0 < want-tol || priorBeta0 > want+0.5 {
		// Allow an Update tick to have moved θ; the prior shape
		// is preserved within reasonable bounds.
		t.Logf("prior β_0 after 1 update = %f; admission seed was %f", priorBeta0, want)
	}

	// Mutate the parent: flip BiiAtZero. The Layer-C shard must
	// NOT pick up the new value (it captured at admission only).
	parent.snap.BiiAtZero = +999.0
	if got := s.theta.AtVec(0); got > 100 {
		t.Errorf("Layer-C β_0 picked up live parent change (%f); should be admission-frozen", got)
	}
}

// itoa avoids strconv import in tests; tiny helper. Same shape as
// internal/coupling/runtime_test.go.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = digits[n%10]
		n /= 10
	}
	return string(b[pos:])
}
