package signature

import (
	"math"
	"time"
)

// evictOne removes the bucket with the lowest weighted-LRU score
// from the library. Score = HitCount × exp(-(now - LastSeen)/τ).
// τ defaults to 14 days per R7 §Q5. RULE-SIG-LIB-05.
//
// Caller MUST hold lib.mu.
func (lib *Library) evictOne(now time.Time) {
	if len(lib.buckets) == 0 {
		return
	}
	var (
		victim      string
		victimScore = math.Inf(1)
		tauSec      = lib.cfg.LRUTau.Seconds()
	)
	if tauSec <= 0 {
		tauSec = (14 * 24 * time.Hour).Seconds()
	}
	for label, b := range lib.buckets {
		ageSec := float64(now.Unix() - b.LastSeenUnix)
		if ageSec < 0 {
			ageSec = 0
		}
		score := float64(b.HitCount) * math.Exp(-ageSec/tauSec)
		// Deterministic tie-break: lexicographically earliest
		// label loses the tie. Matches the test expectation that
		// repeated evictions produce a stable order.
		if score < victimScore || (score == victimScore && label < victim) {
			victimScore = score
			victim = label
		}
	}
	if victim != "" {
		delete(lib.buckets, victim)
	}
}
