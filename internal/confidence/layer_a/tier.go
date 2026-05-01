package layer_a

// R8 fallback tier ceilings from spec-v0_5_9 §2.4 / RULE-CONFA-TIER-01.
//
// Each tier represents how the channel reports RPM back to ventd, and
// the ceiling clamps how much trust the predictive controller can
// place in the per-channel measurement chain. Source: R8 fallback-
// tier table in docs/research/r-bundle/R8-R12-tachless-fallback-and-
// blended-confidence.md.
const (
	TierRPMTach          uint8 = 0 // R8 §"Tier 0" — real fan*_input tach
	TierCoupledInference uint8 = 1 // R8 §"Tier 1" — RPM inferred from coupled fan
	TierBMCIPMI          uint8 = 2 // R8 §"Tier 2" — BMC IPMI sensor proxy
	TierECStepped        uint8 = 3 // R8 §"Tier 3" — laptop EC discrete steps
	TierThermalInvert    uint8 = 4 // R8 §"Tier 4" — invert temp curve
	TierRAPLEcho         uint8 = 5 // R8 §"Tier 5" — RAPL energy proxy
	TierPWMEnableEcho    uint8 = 6 // R8 §"Tier 6" — pwm_enable readback only
	TierOpenLoopPinned   uint8 = 7 // R8 §"Tier 7" — no feedback at all
)

// tierCeilings is the R8 fallback ceiling for each tier. Values lock
// the upper bound on conf_A regardless of coverage/residual/recency.
//
// RULE-CONFA-TIER-01 binds these constants. A change requires the
// rule binding test to be updated in the same commit.
var tierCeilings = [8]float64{
	1.00, // TierRPMTach
	0.85, // TierCoupledInference
	0.70, // TierBMCIPMI
	0.55, // TierECStepped
	0.45, // TierThermalInvert
	0.30, // TierRAPLEcho
	0.30, // TierPWMEnableEcho — same as RAPL per spec-v0_5_9 §2.4
	0.00, // TierOpenLoopPinned
}

// R8Ceiling returns the tier ceiling for tier t. Out-of-range tier
// values clamp to TierOpenLoopPinned (0.0) so a corrupted persisted
// tier byte cannot escape the locked ceiling table.
func R8Ceiling(t uint8) float64 {
	if int(t) >= len(tierCeilings) {
		return tierCeilings[TierOpenLoopPinned]
	}
	return tierCeilings[t]
}
