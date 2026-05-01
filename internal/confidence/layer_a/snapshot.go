// Package layer_a implements v0.5.9's per-channel Layer-A confidence
// estimator (`conf_A`). Per spec-v0_5_9-confidence-controller.md §2.4
// and §3.5.
//
// Conf_A is a scalar in [0, 1] computed from four orthogonal terms:
//
//	conf_A = R8_tier_ceiling × √coverage × (1 − norm_residual) × recency
//
// Inputs come from v0.5.4's observation log via the fast-path Observe
// hook the controller calls after every successful PWM write. The
// estimator maintains a per-channel 16-bin histogram of PWM coverage
// and a per-bin sum-of-squared-residuals (predicted RPM minus observed
// RPM next tick), and computes the four-term product on demand.
//
// Read() is lock-free via atomic.Pointer. Save/Load persist a Bucket
// shape compatible with R15 §104 (KV namespace smart/conf-A/<channel>).
// Hwmon-fingerprint or schema-version mismatches discard on Load.
//
// First-contact (the "never reduce cooling on the first w_pred>0 tick")
// invariant is tracked via SeenFirstContact in the Bucket — persisted
// per-lifetime, re-armed only when the calibration KV namespace is
// wiped.
package layer_a

import "time"

// Snapshot is the lock-free read-side view of a channel's Layer-A
// confidence state. The controller call site reads this every tick
// without taking any mutex.
type Snapshot struct {
	ChannelID string

	// Tier is the R8 fallback tier this channel was admitted with.
	// 0 = RPM tach, 7 = open-loop pinned. Drives the R8 ceiling.
	Tier uint8

	// R8Ceiling is the tier-ceiling scalar from RULE-CONFA-TIER-01.
	R8Ceiling float64

	// Coverage is the fraction of 16 PWM bins (width 16 raw units)
	// that have at least 3 observations. ∈ [0, 1].
	Coverage float64

	// RMSResidual is sqrt(Σε² / N) over curve-fit residuals across
	// all bins with samples.
	RMSResidual float64

	// NoiseFloor frozen at admit, refreshed on tier change. Tach'd
	// channels = 150 RPM (R6); tier-equivalent fallback for tach-less.
	NoiseFloor float64

	// Age is wall-clock since the last admissible Layer-A update.
	// Used to compute recency = exp(-age/604800s).
	Age time.Duration

	// ConfA is the four-term product, ∈ [0, 1]. The controller's
	// confidence aggregator consumes this value directly.
	ConfA float64

	// SeenFirstContact gates the v0.5.9 first-contact invariant —
	// persisted per-lifetime, re-armed only on KV wipe.
	SeenFirstContact bool
}
