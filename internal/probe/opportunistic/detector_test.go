package opportunistic

import (
	"sort"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
)

// TestDetector_LowHighGridSpacing verifies the canonical probe grid
// shape: 8-unit spacing in [0, 96], 16-unit spacing in [97, 255]
// (RULE-OPP-PROBE-12).
func TestDetector_LowHighGridSpacing(t *testing.T) {
	grid := ProbeGrid(ChannelKnowns{})
	if len(grid) == 0 {
		t.Fatal("empty grid")
	}

	// First half: every value is multiple of 8 and <= 96.
	for _, pwm := range grid {
		if pwm > LowHighBoundary {
			break
		}
		if pwm%LowHalfStep != 0 {
			t.Errorf("low-half PWM %d not aligned to %d", pwm, LowHalfStep)
		}
	}

	// Sweep boundary values: 0, 8, ..., 96 must all appear.
	for want := uint8(0); want <= LowHighBoundary; want += LowHalfStep {
		found := false
		for _, g := range grid {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("low-half value %d missing from grid", want)
		}
	}

	// High half: every value is in [97, 255] and on the high stride.
	for _, pwm := range grid {
		if pwm <= LowHighBoundary {
			continue
		}
		// Stride from 97; (pwm - 97) % HighHalfStep == 0.
		if (pwm-(LowHighBoundary+1))%HighHalfStep != 0 {
			t.Errorf("high-half PWM %d not aligned to %d from boundary", pwm, HighHalfStep)
		}
	}
}

// TestDetector_AnchorsStallAndMinSpin verifies that supplying stall +
// min-spin anchors merges them into the grid even if they don't align
// with the 8/16 spacing (RULE-OPP-PROBE-12).
func TestDetector_AnchorsStallAndMinSpin(t *testing.T) {
	knowns := ChannelKnowns{StallPWM: 47, MinSpinPWM: 53}
	grid := ProbeGrid(knowns)
	mustContain := []uint8{47, 53}
	for _, want := range mustContain {
		found := false
		for _, g := range grid {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("anchor %d missing from grid", want)
		}
	}
}

// TestDetector_BuildsGapSetFromLog feeds a synthetic observation log
// with a few visited bins and asserts the detector returns the
// expected gap set.
func TestDetector_BuildsGapSetFromLog(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ch := &probe.ControllableChannel{PWMPath: "/sys/class/hwmon/hwmon3/pwm1", Polarity: "normal"}
	chID := observation.ChannelID(ch.PWMPath)

	// Visited bins: PWM 0, 8, 64, 128.
	store := newFakeLogStore(now,
		fakeRec(now.Add(-3*24*time.Hour), chID, 0, 0),
		fakeRec(now.Add(-3*24*time.Hour), chID, 8, 0),
		fakeRec(now.Add(-2*24*time.Hour), chID, 64, 0),
		fakeRec(now.Add(-1*24*time.Hour), chID, 128, 0),
	)
	rd := observation.NewReader(store)

	d := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)
	gaps, err := d.Gaps(now)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}

	got := gaps[chID]
	for _, want := range []uint8{16, 24, 32, 40, 48, 56, 72, 80, 88, 96} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected PWM %d in gap set; got %v", want, got)
		}
	}

	// Bins we marked visited must NOT appear.
	for _, visited := range []uint8{0, 8, 64, 128} {
		for _, g := range got {
			if g == visited {
				t.Errorf("visited PWM %d incorrectly listed as gap", visited)
			}
		}
	}
}

// TestDetector_ExcludesBinsWithin7Days asserts that bins visited
// within CooldownWindow are excluded; bins visited before are
// included (RULE-OPP-PROBE-06).
func TestDetector_ExcludesBinsWithin7Days(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ch := &probe.ControllableChannel{PWMPath: "/sys/class/hwmon/hwmon3/pwm1", Polarity: "normal"}
	chID := observation.ChannelID(ch.PWMPath)

	// PWM 32 visited within window — must be excluded.
	// PWM 40 visited 8 days ago — must remain a gap.
	store := newFakeLogStore(now,
		fakeRec(now.Add(-3*24*time.Hour), chID, 32, 0),
		fakeRec(now.Add(-8*24*time.Hour), chID, 40, 0),
	)
	rd := observation.NewReader(store)
	d := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)
	gaps, err := d.Gaps(now)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	got := gaps[chID]
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })

	for _, g := range got {
		if g == 32 {
			t.Error("PWM 32 visited within cool-down should NOT be a gap")
		}
	}
	found40 := false
	for _, g := range got {
		if g == 40 {
			found40 = true
			break
		}
	}
	if !found40 {
		t.Error("PWM 40 visited > 7 days ago should remain a gap")
	}
}

// TestDetector_AbortedOpportunisticDoesNotCount asserts that an
// opportunistic probe record with the abort flag set does NOT count
// as a visited bin — the bin remains eligible for retry on the next
// gate window (RULE-OPP-PROBE-06).
func TestDetector_AbortedOpportunisticDoesNotCount(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ch := &probe.ControllableChannel{PWMPath: "/sys/class/hwmon/hwmon3/pwm1", Polarity: "normal"}
	chID := observation.ChannelID(ch.PWMPath)

	// One record: opportunistic + aborted on PWM 24.
	flags := observation.EventFlag_OPPORTUNISTIC_PROBE | observation.EventFlag_ENVELOPE_C_ABORT
	store := newFakeLogStore(now,
		fakeRec(now.Add(-1*24*time.Hour), chID, 24, flags),
	)
	rd := observation.NewReader(store)
	d := NewDetector(rd, []*probe.ControllableChannel{ch}, nil)
	gaps, err := d.Gaps(now)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	got := gaps[chID]
	found24 := false
	for _, g := range got {
		if g == 24 {
			found24 = true
			break
		}
	}
	if !found24 {
		t.Error("aborted opportunistic probe at PWM 24 should leave bin eligible for retry")
	}
}
