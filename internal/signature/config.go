package signature

import "time"

// Knob defaults locked by R7 §Q4 / §Q5 / §Confidence. v0.5.6 hard-
// codes these; revisit at v1.0 per R7's deferral.
const (
	DefaultBucketCount  = 128
	DefaultK            = 4
	DefaultStabilityM   = 3
	DefaultHalfLife     = 2 * time.Second
	DefaultLRUTau       = 14 * 24 * time.Hour
	DefaultDropEpsilon  = 1e-3
	DefaultCPUGate      = 0.05 // share of one core
	DefaultRSSGateBytes = uint64(256 * 1024 * 1024)
)

// FallbackLabelDisabled is the fixed label emitted when the library
// is in a permanent-disabled state (R1 Tier-2 BLOCK, R3 hardware-
// refused, or operator toggle off). RULE-SIG-LIB-07 / RULE-SIG-LIB-08.
const FallbackLabelDisabled = "fallback/disabled"

// FallbackLabelIdle is emitted when the system is genuinely idle —
// no process passes the gate. Distinct from `fallback/disabled` so
// downstream consumers can tell "we're not learning" from "nothing
// to learn from."
const FallbackLabelIdle = "fallback/idle"

// MaintLabelPrefix is the canonical prefix for reserved
// maintenance-class labels per R7 §Q2 (B). The full label is
// "maint/<canonical-name>".
const MaintLabelPrefix = "maint/"

// Config bundles the runtime knobs. Production code should use the
// Default* constants above; tests inject Config to vary timing.
type Config struct {
	BucketCount  int
	K            int
	StabilityM   int
	HalfLife     time.Duration
	LRUTau       time.Duration
	DropEpsilon  float64
	CPUGate      float64
	RSSGateBytes uint64
	// Disabled is the operator-toggle (Config.SignatureLearningDisabled)
	// or R1/R3 inheritance gate. When true, every Tick returns
	// FallbackLabelDisabled and persistence is suppressed.
	Disabled bool
}

// DefaultConfig returns a Config populated with the R7-locked
// defaults.
func DefaultConfig() Config {
	return Config{
		BucketCount:  DefaultBucketCount,
		K:            DefaultK,
		StabilityM:   DefaultStabilityM,
		HalfLife:     DefaultHalfLife,
		LRUTau:       DefaultLRUTau,
		DropEpsilon:  DefaultDropEpsilon,
		CPUGate:      DefaultCPUGate,
		RSSGateBytes: DefaultRSSGateBytes,
	}
}
