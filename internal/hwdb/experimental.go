package hwdb

import (
	"fmt"
	"log/slog"
	"sync"
)

// ExperimentalBlock declares per-board / per-driver experimental feature
// eligibility. Absent block → all keys default false. Pointer on the
// containing struct so absent is distinguishable from all-false.
//
// v1.2 ships four keys. Future keys arrive via catalog updates; unrecognised
// keys with Levenshtein distance > 2 from any known key are accepted with a
// one-shot WARN (forward-compat shim for v1.3+ catalogs on v1.2 ventd).
type ExperimentalBlock struct {
	ILO4Unlocked    bool `yaml:"ilo4_unlocked,omitempty"`
	AMDOverdrive    bool `yaml:"amd_overdrive,omitempty"`
	NvidiaCoolbits  bool `yaml:"nvidia_coolbits,omitempty"`
	IDRAC9LegacyRaw bool `yaml:"idrac9_legacy_raw,omitempty"`
}

// experimentalKnownKeys is the exhaustive list of validator-recognized keys.
var experimentalKnownKeys = []string{
	"ilo4_unlocked",
	"amd_overdrive",
	"nvidia_coolbits",
	"idrac9_legacy_raw",
}

// warnedExperimentalKeys deduplicates forward-compat WARNs per process lifetime.
var warnedExperimentalKeys sync.Map

// resetWarnedExperimentalKeysForTest clears the once-per-lifetime warn state.
// Only for test isolation.
func resetWarnedExperimentalKeysForTest() {
	warnedExperimentalKeys.Range(func(k, _ any) bool {
		warnedExperimentalKeys.Delete(k)
		return true
	})
}

// validateExperimental validates and parses the raw experimental block map
// (from YAML decoding). It returns the parsed ExperimentalBlock and any hard
// error. Unknown keys with Levenshtein distance ≤ 2 from a recognized key are
// rejected as likely typos. Unknown keys with distance > 2 are accepted with
// a one-shot WARN via log (forward-compat for newer catalog entries).
func validateExperimental(raw map[string]any, log *slog.Logger) (ExperimentalBlock, error) {
	var out ExperimentalBlock
	for k, v := range raw {
		// Check for exact match first.
		isKnown := false
		for _, known := range experimentalKnownKeys {
			if k != known {
				continue
			}
			isKnown = true
			b, ok := v.(bool)
			if !ok {
				return ExperimentalBlock{}, fmt.Errorf("experimental.%s: expected bool, got %T", k, v)
			}
			switch k {
			case "ilo4_unlocked":
				out.ILO4Unlocked = b
			case "amd_overdrive":
				out.AMDOverdrive = b
			case "nvidia_coolbits":
				out.NvidiaCoolbits = b
			case "idrac9_legacy_raw":
				out.IDRAC9LegacyRaw = b
			}
			break
		}
		if isKnown {
			continue
		}

		// Unknown key: compute min Levenshtein distance to find closest match.
		minDist := len(k) + 20 // sentinel: larger than any real distance
		bestMatch := ""
		for _, known := range experimentalKnownKeys {
			if d := damerauLevenshtein(k, known); d < minDist {
				minDist = d
				bestMatch = known
			}
		}
		if minDist <= 2 {
			return ExperimentalBlock{}, fmt.Errorf("experimental.%s: unknown key. Did you mean: %s?", k, bestMatch)
		}

		// Distance > 2: forward-compat — warn once, continue.
		if _, alreadyWarned := warnedExperimentalKeys.LoadOrStore(k, struct{}{}); !alreadyWarned {
			log.Warn("experimental block: unknown key ignored; upgrade ventd if this key is from a newer catalog",
				"key", k)
		}
	}
	return out, nil
}

// CatalogMatch holds the board entry and driver profile resolved for a system.
// ExperimentalEligibility OR-merges experimental feature eligibility from
// both sources per RULE-EXPERIMENTAL-MERGE-01.
//
// Nothing in this PR calls ExperimentalEligibility; the method is the API
// surface for the spec-15 framework PR.
type CatalogMatch struct {
	Board  *BoardCatalogEntry
	Driver *DriverProfile
}

// ExperimentalEligibility returns the OR-merged experimental eligibility
// from the matched board profile and matched GPU driver entry. A feature is
// eligible if either source asserts true.
func (m *CatalogMatch) ExperimentalEligibility() ExperimentalBlock {
	var out ExperimentalBlock
	if m.Board != nil {
		b := m.Board.Experimental
		out.ILO4Unlocked = out.ILO4Unlocked || b.ILO4Unlocked
		out.AMDOverdrive = out.AMDOverdrive || b.AMDOverdrive
		out.NvidiaCoolbits = out.NvidiaCoolbits || b.NvidiaCoolbits
		out.IDRAC9LegacyRaw = out.IDRAC9LegacyRaw || b.IDRAC9LegacyRaw
	}
	if m.Driver != nil {
		d := m.Driver.Experimental
		out.ILO4Unlocked = out.ILO4Unlocked || d.ILO4Unlocked
		out.AMDOverdrive = out.AMDOverdrive || d.AMDOverdrive
		out.NvidiaCoolbits = out.NvidiaCoolbits || d.NvidiaCoolbits
		out.IDRAC9LegacyRaw = out.IDRAC9LegacyRaw || d.IDRAC9LegacyRaw
	}
	return out
}
