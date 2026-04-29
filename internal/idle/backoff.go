package idle

import (
	"math"
	"math/rand/v2"
	"time"
)

const (
	backoffBase     = 60 * time.Second
	backoffCap      = 3600 * time.Second
	backoffJitter   = 0.20 // ±20%
	backoffDailyCap = 12
)

// Backoff computes the next retry delay for attempt n (0-indexed consecutive
// refusal count). Formula: min(60×2^n, 3600) ± 20% jitter.
// Returns zero when n >= backoffDailyCap (daily cap reached).
func Backoff(n int) time.Duration {
	if n >= backoffDailyCap {
		return 0
	}
	// Compute exponential: 60 × 2^n, clamped to 3600.
	base := float64(backoffBase) * math.Pow(2, float64(n))
	if base > float64(backoffCap) {
		base = float64(backoffCap)
	}
	// Apply ±20% uniform jitter.
	jitter := (rand.Float64()*2 - 1) * backoffJitter * base
	return time.Duration(base + jitter)
}

// BackoffDet is identical to Backoff but accepts an explicit rng source for
// deterministic testing. src must return values in [0.0, 1.0).
func BackoffDet(n int, src func() float64) time.Duration {
	if n >= backoffDailyCap {
		return 0
	}
	base := float64(backoffBase) * math.Pow(2, float64(n))
	if base > float64(backoffCap) {
		base = float64(backoffCap)
	}
	jitter := (src()*2 - 1) * backoffJitter * base
	return time.Duration(base + jitter)
}
