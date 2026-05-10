package calibrate

import (
	"math"
	"testing"
)

// TestStdDevInt_KnownValues pins the population-stddev math the
// pre-ramp stability gate (RULE-CAL-DETECT-STABILITY) consumes. The
// gate refuses to start a sweep when any tach's stddev across three
// 200 ms-spaced baseline samples exceeds detectStabilityThreshold
// (50 RPM); a regression in this helper would falsely admit jittery
// channels or falsely refuse stable ones.
func TestStdDevInt_KnownValues(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want float64
	}{
		{"empty", nil, 0},
		{"single", []int{1500}, 0},
		{"identical", []int{1500, 1500, 1500}, 0},
		{"phantom_zeros", []int{0, 0, 0}, 0},
		// stable case: small jitter well under the 50 RPM noise floor.
		{"stable_2_rpm_jitter", []int{1500, 1502, 1499}, 1.247}, // sqrt(((-0.33)^2 + (1.67)^2 + (-1.33)^2)/3)
		// boundary case: 50 RPM stddev — gate would accept (strict >).
		// Three values [-50, 0, +50] from mean=0: stddev = sqrt((2500+0+2500)/3) ≈ 40.82.
		{"boundary_40", []int{1450, 1500, 1550}, 40.825},
		// jitter case: 100 RPM swing — gate would refuse.
		{"jitter_100", []int{1500, 1700, 1900}, 163.299},
		// chip-residue case: one tach reads 0 once, then real values.
		// This is the canonical issue #1026 failure mode — the first
		// post-mode-change read is a glitch.
		{"chip_residue", []int{0, 1500, 1500}, 707.107}, // sqrt(2/3)*1000
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stdDevInt(tc.in)
			if math.Abs(got-tc.want) > 0.01 {
				t.Errorf("stdDevInt(%v) = %.3f; want %.3f", tc.in, got, tc.want)
			}
		})
	}
}

// TestStdDevInt_BelowThresholdAdmits pins the gate'#39;s admit decision
// for the canonical phantom case — three identical RPM=0 reads. The
// gate relies on stddev=0 admitting; the downstream minDelta check
// catches the phantom via "no winner" in the post-ramp path.
func TestStdDevInt_BelowThresholdAdmits(t *testing.T) {
	// Phantom channel: all reads zero.
	if got := stdDevInt([]int{0, 0, 0}); got > 50 {
		t.Errorf("phantom samples [0,0,0] produced stddev %.1f > 50; gate would falsely refuse", got)
	}
	// Stable real fan: 1500 ± 5 RPM (tach noise floor).
	if got := stdDevInt([]int{1500, 1495, 1505}); got > 50 {
		t.Errorf("stable real-fan samples produced stddev %.1f > 50; gate would falsely refuse", got)
	}
}

// TestStdDevInt_AboveThresholdRefuses pins the gate'#39;s refuse decision
// for the issue #1026 chip-transition glitch — a tach that reads 0 once
// then jumps to 1500 RPM produces stddev far above the noise floor and
// refuses the sweep, preventing the false-positive correlation that
// fooled DetectRPMSensor on Phoenix's IT8688 host.
func TestStdDevInt_AboveThresholdRefuses(t *testing.T) {
	// Chip-residue glitch: first read 0, then real values.
	if got := stdDevInt([]int{0, 1500, 1500}); got <= 50 {
		t.Errorf("chip-residue samples [0,1500,1500] produced stddev %.1f <= 50; gate would falsely admit", got)
	}
	// Tach jitter: 200 RPM swing across 3 samples.
	if got := stdDevInt([]int{1300, 1500, 1700}); got <= 50 {
		t.Errorf("jittery samples produced stddev %.1f <= 50; gate would falsely admit", got)
	}
}
