package coupling

import "math"

// Confidence returns the R12 §Q1 four-term product collapsed to a
// single scalar in [0, 1]. v0.5.9's blended controller consumes this
// as conf_B per spec-v0_5_9 §2.5.
//
// The four terms applied to Layer-B's snapshot:
//
//   - saturation_admit: Layer-B has no saturation analogue (that
//     belongs to Layer-C); we treat as 1.0 unless the shard is
//     warming up (then 0).
//   - identifiability: 1 when κ ≤ 100 (healthy); tapers linearly in
//     log10(κ) to 0 at κ = 1e4 (R10's unidentifiable threshold).
//   - covariance: 1 - clamp(tr(P) / TrPCap, 0, 1). High tr(P) means
//     the estimator is uncertain → low confidence.
//   - sample_count: clamp(NSamples / 50, 0, 1) per R12 §Q1's
//     sample-saturation point.
//
// WarmingUp short-circuits to 0 — the warmup gate is the canonical
// "not yet trustworthy" signal.
func (s *Snapshot) Confidence() float64 {
	if s == nil || s.WarmingUp {
		return 0
	}

	ident := identifiabilityTerm(s.Kappa)
	cov := covarianceTerm(s.TrP)
	samples := sampleCountTerm(s.NSamples)

	out := ident * cov * samples
	if out < 0 {
		out = 0
	}
	if out > 1 {
		out = 1
	}
	return out
}

// identifiabilityTerm tapers linearly in log10(κ) from 1 (at κ ≤ 100)
// to 0 (at κ ≥ 1e4). R10 §10.2 thresholds.
func identifiabilityTerm(kappa float64) float64 {
	if kappa <= 100 {
		return 1.0
	}
	if kappa >= 1e4 || math.IsInf(kappa, 0) || math.IsNaN(kappa) {
		return 0.0
	}
	// log10(100)=2, log10(1e4)=4 → 2 → 0 as we go.
	return 1.0 - (math.Log10(kappa)-2.0)/2.0
}

// covarianceTerm is 1 - clamp(tr(P) / TrPCap, 0, 1). High tr(P) ⇒
// high uncertainty ⇒ low confidence. Clamps the input to handle
// stale persisted shards that haven't been re-clamped on Load yet.
func covarianceTerm(trP float64) float64 {
	if trP <= 0 || math.IsNaN(trP) {
		return 1.0
	}
	x := trP / TrPCap
	if x > 1 {
		return 0.0
	}
	return 1.0 - x
}

// sampleCountTerm = clamp(NSamples / NMinR12, 0, 1) where NMinR12=50
// per R12 §Q1's sample-saturation. We re-export the constant here
// rather than import from internal/marginal because that would
// create a circular dependency (marginal depends on coupling for
// its SnapshotKind alias).
const NMinR12 = 50

func sampleCountTerm(n uint64) float64 {
	if n == 0 {
		return 0
	}
	x := float64(n) / NMinR12
	if x > 1 {
		return 1
	}
	return x
}
