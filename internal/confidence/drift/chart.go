package drift

import "math"

// chartState is the per-(channel, layer) state of one EWMA control chart.
// It is a plain value — step() takes one and returns the next, with no
// mutex and no clock, so the drift math is deterministic and unit-testable
// in isolation from the Detector that owns the per-channel map.
type chartState struct {
	z          float64 // EWMA monitor of the sqrt-residual (the charted statistic)
	baseline   float64 // μ — slow EWMA of the healthy sqrt-residual level
	dispersion float64 // EWMA of |z − μ| (a robust σ proxy)
	warmCount  int     // post-convergence observations folded into the baseline
	tripCount  int     // consecutive over-trip-limit observations (debounce)
	clearCount int     // consecutive under-clear-limit observations (debounce)
	flagged    bool    // currently drifting
	inited     bool    // baseline seeded (set on the first converged observation)
	seededZ    bool    // z seeded (set on the first observation, converged or not)
}

// step folds one observation into an EWMA control chart and returns the
// next state. r is the monitored statistic (the sqrt of the layer's
// residual magnitude); converged says whether the layer has learned
// enough to be judged.
//
// The chart is the Ross et al. (2012) residual EWMA control chart adapted
// for a self-referential baseline:
//
//  1. z tracks r as an EWMA (EWMAAlpha) at all times, so it is already
//     warm when the layer converges.
//  2. CONVERGENCE GUARD: while !converged, never flag and never update the
//     baseline — a warming layer's high residual is not drift. Dwell
//     counters reset; the baseline is re-seeded when convergence returns.
//  3. While converged AND not flagged, the baseline μ and dispersion σ
//     update (slow EWMAs). They FREEZE while flagged so the anomaly that
//     tripped the chart cannot poison the reference it is compared against.
//  4. Trip when z exceeds μ + L·σ for TripDwell consecutive observations
//     (and only after WarmupTicks post-convergence observations, so the
//     baseline is trusted); clear when z falls below μ + LClear·σ for
//     ClearDwell consecutive observations. σ has a MinSigma floor so a
//     near-constant residual can't make any micro-wobble trip.
func step(s chartState, r float64, converged bool, c Config) chartState {
	if r < 0 {
		r = 0
	}

	// 1. EWMA monitor — always tracks. First-ever observation seeds z
	// directly so it isn't biased toward a cold 0.
	if !s.seededZ {
		s.z = r
		s.seededZ = true
	} else {
		s.z = c.EWMAAlpha*r + (1-c.EWMAAlpha)*s.z
	}

	// 2. Convergence guard.
	if !converged {
		s.flagged = false
		s.tripCount = 0
		s.clearCount = 0
		s.warmCount = 0
		s.inited = false
		return s
	}

	// 3. Baseline + dispersion (frozen while flagged).
	if !s.inited {
		s.baseline = s.z
		s.dispersion = 0
		s.inited = true
	} else if !s.flagged {
		dev := math.Abs(s.z - s.baseline)
		s.baseline = c.BaselineAlpha*s.z + (1-c.BaselineAlpha)*s.baseline
		s.dispersion = c.DispersionAlpha*dev + (1-c.DispersionAlpha)*s.dispersion
	}
	if s.warmCount < c.WarmupTicks {
		s.warmCount++
	}

	// 4. Control limits + trip/clear with dwell hysteresis.
	sigma := s.dispersion
	if sigma < c.MinSigma {
		sigma = c.MinSigma
	}
	tripLimit := s.baseline + c.L*sigma
	clearLimit := s.baseline + c.LClear*sigma
	warm := s.warmCount >= c.WarmupTicks

	if !s.flagged {
		if warm && s.z > tripLimit {
			s.tripCount++
			if s.tripCount >= c.TripDwell {
				s.flagged = true
				s.clearCount = 0
			}
		} else {
			s.tripCount = 0
		}
	} else {
		if s.z < clearLimit {
			s.clearCount++
			if s.clearCount >= c.ClearDwell {
				s.flagged = false
				s.tripCount = 0
			}
		} else {
			s.clearCount = 0
		}
	}
	return s
}

// controlLimit returns μ + L·σ (the trip threshold) for the surface.
func (s chartState) controlLimit(c Config) float64 {
	sigma := s.dispersion
	if sigma < c.MinSigma {
		sigma = c.MinSigma
	}
	return s.baseline + c.L*sigma
}

// sigmaFloored returns the dispersion after the MinSigma floor.
func (s chartState) sigmaFloored(c Config) float64 {
	if s.dispersion < c.MinSigma {
		return c.MinSigma
	}
	return s.dispersion
}
