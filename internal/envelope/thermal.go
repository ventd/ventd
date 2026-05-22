package envelope

import (
	"time"
)

// DefaultSlopeAbortConsecutive is the number of consecutive over-
// threshold rate observations a SlopeAbortGate requires before
// aborting. 3 samples at SampleHz=10 (production default) = 0.3 s of
// sustained over-threshold; single-sample sensor-quantization noise
// produces 1 over-threshold observation followed by a return to
// baseline, which resets the counter without aborting.
//
// Lower values (1, 2) re-introduce the spurious-abort regression for
// which TestProber_NoSpuriousAbortOnIdleCoreTempNoise is the canonical
// reproducer; higher values (>5) delay reaction to a real thermal
// runaway by tenths of a second per extra sample at 10 Hz, which is
// not justified for genuine over-threshold ramps. 3 is the smallest
// value that fully absorbs the recorded idle-CPU coretemp noise
// pattern (4 °C single-sample steps with a 1-sample recovery).
const DefaultSlopeAbortConsecutive = 3

// SlopeAbortGate wraps the dT/dt abort check with an N-of-M
// consecutive-observation requirement. A single-sample over-threshold
// rate (e.g., a 4 °C coretemp quantization step in 100 ms producing
// 40 °C/s on an otherwise-idle CPU) does not trip; only genuinely
// sustained over-threshold rates do.
//
// The instance is stateful: callers construct one gate per probe run
// and feed each sample through ShouldAbort. Zero value uses
// DefaultSlopeAbortConsecutive.
//
// RULE-ENVELOPE-04 boundary semantics are preserved — exactly-at-
// threshold never trips at any sample count, because the underlying
// per-sample check uses strict > comparison.
type SlopeAbortGate struct {
	// Consecutive is the number of consecutive over-threshold samples
	// required before aborting. Zero uses DefaultSlopeAbortConsecutive.
	Consecutive int
	consec      int
}

// ShouldAbort returns true when Consecutive samples in a row have
// shown an over-threshold dT/dt rate. Any under-threshold sample
// resets the counter, so spike-then-recover noise never accumulates
// toward an abort.
func (g *SlopeAbortGate) ShouldAbort(cur, prev map[string]float64, dt time.Duration, thr Thresholds) bool {
	if !thermalAbort(cur, prev, dt, thr) {
		g.consec = 0
		return false
	}
	g.consec++
	n := g.Consecutive
	if n <= 0 {
		n = DefaultSlopeAbortConsecutive
	}
	return g.consec >= n
}

// thermalAbort returns true when the dT/dt rate exceeds the class
// threshold for any sensor in the current sample. For NAS
// (DTDtAbortCPerSec==0, DTDtAbortCPerMin>0) the per-minute rate is
// used. prev may be nil on the first sample; in that case no abort
// is triggered.
//
// Single-sample semantics — does NOT filter noise. Callers that want
// the noise-tolerant N-of-M behaviour for genuine probe-runtime use
// should wrap this in SlopeAbortGate rather than calling it directly.
// Kept exported-package-internal for the boundary tests in
// envelope_test.go which assert RULE-ENVELOPE-04 boundary semantics.
func thermalAbort(temps, prev map[string]float64, dt time.Duration, thr Thresholds) bool {
	if len(prev) == 0 || dt <= 0 {
		return false
	}
	if thr.DTDtAbortCPerSec == 0 && thr.DTDtAbortCPerMin == 0 {
		return false
	}
	for id, cur := range temps {
		p, ok := prev[id]
		if !ok {
			continue
		}
		delta := cur - p
		if thr.DTDtAbortCPerSec > 0 {
			rate := delta / dt.Seconds()
			if rate > thr.DTDtAbortCPerSec {
				return true
			}
		} else {
			rate := delta / dt.Minutes()
			if rate > thr.DTDtAbortCPerMin {
				return true
			}
		}
	}
	return false
}

// absoluteTempAbort returns true when any sensor reading exceeds the ceiling.
// For classes with TAbsOffsetBelowTjmax > 0 the ceiling is tjmax - offset.
// For NAS (TAbsAbsolute > 0) the ceiling is TAbsAbsolute.
func absoluteTempAbort(temps map[string]float64, tjmax float64, thr Thresholds) bool {
	var ceiling float64
	if thr.TAbsOffsetBelowTjmax > 0 {
		ceiling = tjmax - thr.TAbsOffsetBelowTjmax
	} else {
		ceiling = thr.TAbsAbsolute
	}
	if ceiling <= 0 {
		return false
	}
	for _, t := range temps {
		if t > ceiling {
			return true
		}
	}
	return false
}

// ambientHeadroomOK returns true when there is sufficient thermal headroom
// between ambient and Tjmax to safely run Envelope C.
func ambientHeadroomOK(ambient, tjmax float64, thr Thresholds) bool {
	return ambient < tjmax-thr.AmbientHeadroomMin
}

// copyTemps returns a shallow copy of a temperature map.
func copyTemps(src map[string]float64) map[string]float64 {
	if src == nil {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
