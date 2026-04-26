package hwdb

// MatchTier identifies which tier of the three-tier matcher succeeded.
type MatchTier int

const (
	MatchTierNone   MatchTier = 0 // no match found
	MatchTierBoard  MatchTier = 1 // tier-1: exact DMI board fingerprint
	MatchTierVendor MatchTier = 2 // tier-2: DMI vendor partial / regex
	MatchTierChip   MatchTier = 3 // tier-3: generic chip-family fallback
)

// MatchDiagnostics records how the three-tier matcher arrived at its result.
// Returned alongside EffectiveControllerProfile so callers (diagnostic bundle,
// setup wizard) can explain the match to users and log confidence signals.
type MatchDiagnostics struct {
	Tier       MatchTier
	Confidence float64 // 0.0..1.0; 1.0 = exact DMI match, lower for partial/generic

	// Fields that contributed to the match (non-empty means that field matched).
	MatchedBoardID      string // v1.1: board catalog ID for tier-1 matches
	MatchedBoardVendor  string
	MatchedBoardName    string
	MatchedChipName     string
	MatchedDriverModule string

	// Nullable-but-populated: fields known at match time but not resolved.
	// Used by the diagnostic bundle to surface "chip detected but no board profile."
	DetectedChipName string // hwmon name value found in /sys/class/hwmon/*/name
	UnmatchedFields  []string

	// Warnings surfaced during resolution (e.g. PR 1 migration path taken).
	Warnings []string
}
