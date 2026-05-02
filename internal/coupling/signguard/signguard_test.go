package signguard

import "testing"

// TestSignVote_5Of7Threshold — RULE-SGD-VOTE-01.
//
// Feed 7 samples: 5 agree (correct cooling polarity, ΔT < 0 with
// ΔPWM > 0), 2 disagree. Confirmed must return true.
// With only 4 agree, Confirmed must return false.
func TestSignVote_5Of7Threshold(t *testing.T) {
	d := NewDetector()
	feed := func(deltaPWM int8, deltaT float64) {
		d.Add(Sample{ChannelID: "ch", DeltaPWM: deltaPWM, DeltaT: deltaT})
	}
	// 5 agree.
	for i := 0; i < 5; i++ {
		feed(+1, -3.0)
	}
	// 2 disagree.
	for i := 0; i < 2; i++ {
		feed(+1, +3.0)
	}
	if !d.Confirmed("ch") {
		t.Errorf("5/7 agree → Confirmed should be true")
	}

	// Reset: 4 agree, 3 disagree.
	d2 := NewDetector()
	for i := 0; i < 4; i++ {
		d2.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: -3.0})
	}
	for i := 0; i < 3; i++ {
		d2.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: +3.0})
	}
	if d2.Confirmed("ch") {
		t.Errorf("4/7 agree → Confirmed should be false")
	}
}

// TestSignVote_DiscardsBelowNoise — RULE-SGD-NOISE-01.
//
// Probes with |ΔT| < 2°C MUST be ignored (uninformative). Feed 7
// sub-noise samples; Confirmed remains false (no votes recorded).
func TestSignVote_DiscardsBelowNoise(t *testing.T) {
	d := NewDetector()
	for i := 0; i < 7; i++ {
		ok := d.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: -1.5})
		if ok {
			t.Errorf("Add returned true for sub-noise ΔT=1.5°C")
		}
	}
	if d.Confirmed("ch") {
		t.Errorf("sub-noise probes should never produce confirmation")
	}
}

// TestSignVote_DowngradeOnFlipMidLifetime — RULE-SGD-CONT-01.
//
// 7 agreeing samples → confirmed. Then 5 disagreeing samples push
// the rolling vote below threshold → unconfirmed. Continuous
// detection (not warmup-only).
func TestSignVote_DowngradeOnFlipMidLifetime(t *testing.T) {
	d := NewDetector()
	for i := 0; i < 7; i++ {
		d.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: -3.0})
	}
	if !d.Confirmed("ch") {
		t.Fatalf("expected confirmed after 7 agree votes")
	}
	// Now feed 5 disagrees → window contains 2 agree + 5 disagree.
	for i := 0; i < 5; i++ {
		d.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: +3.0})
	}
	if d.Confirmed("ch") {
		t.Errorf("expected NOT confirmed after sign-flip mid-lifetime")
	}
}

// TestSignVote_PreWindowConfirmedFalse — sanity: with < VoteWindow
// samples, Confirmed must be false (avoids premature promotion).
func TestSignVote_PreWindowConfirmedFalse(t *testing.T) {
	d := NewDetector()
	for i := 0; i < VoteWindow-1; i++ {
		d.Add(Sample{ChannelID: "ch", DeltaPWM: +1, DeltaT: -3.0})
	}
	if d.Confirmed("ch") {
		t.Errorf("pre-window Confirmed should be false")
	}
}
