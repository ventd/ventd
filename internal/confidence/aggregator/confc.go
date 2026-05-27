package aggregator

import "github.com/ventd/ventd/internal/marginal"

// ConfCFromMarginal collapses a Layer-C marginal snapshot into the
// scalar conf_C the aggregator consumes (RULE-AGG-SIG-COLLAPSE-01): the
// active-signature shard's product term
// (ResidualTerm·CovarianceTerm·SampleCountTerm) when the saturation gate
// admits, and 0 otherwise — including a nil snapshot or a shard still
// warming up.
//
// v0.5.8 deliberately emits only the ConfidenceComponents and does not
// compute the aggregated float (RULE-CMB-CONF-01). This caller-side
// collapse therefore lives in the v0.5.9 aggregator package that owns
// the conf_C scalar — not in marginal, and no longer stranded inline in
// package main where it could be neither unit-tested nor rule-bound.
func ConfCFromMarginal(s *marginal.Snapshot) float64 {
	if s == nil || s.WarmingUp {
		return 0
	}
	cc := s.Confidence
	if !cc.SaturationAdmit {
		return 0
	}
	return cc.ResidualTerm * cc.CovarianceTerm * cc.SampleCountTerm
}
