package nbfc

import (
	"strings"

	"github.com/ventd/ventd/internal/hwdb"
)

// MatchTier describes how strongly a Match resolved.
type MatchTier int

const (
	// MatchNone: no catalog entry matched the DMI.
	MatchNone MatchTier = iota

	// MatchExact: DMI product name exactly equals an upstream
	// NotebookModel (case-folded, whitespace-normalised).
	MatchExact

	// MatchPrefix: DMI product name shares the upstream wildcard
	// stem (e.g. live "HP Pavilion 15-cs0098nx" against catalog
	// "HP Pavilion 15-cs0xxx" by trailing-x masking, or substring
	// when no wildcard).
	MatchPrefix

	// MatchSubstring: weaker — the catalog NotebookModel appears
	// as a substring of the live product name or vice versa.
	// Useful for OEM rebrands where upstream catalogues the
	// OEM-original SKU and the live system reports the rebrand.
	MatchSubstring
)

// String renders the tier for diagnostics.
func (t MatchTier) String() string {
	switch t {
	case MatchExact:
		return "exact"
	case MatchPrefix:
		return "prefix"
	case MatchSubstring:
		return "substring"
	default:
		return "none"
	}
}

// Match resolves a hwdb.DMI tuple to the highest-confidence catalogue
// entry. Tier order: Exact > Prefix > Substring > None. Returns
// (nil, MatchNone) on no match — that is the not-an-error case;
// the caller surfaces a doctor card inviting upstream contribution.
//
// Match is pure: same DMI in, same Entry out, regardless of process
// state. The catalog argument is the parsed result from LoadCatalog;
// passing nil returns (nil, MatchNone) cleanly so callers can defer
// the load.
func Match(cat *Catalog, dmi hwdb.DMI) (*Entry, MatchTier) {
	if cat == nil || len(cat.Entries) == 0 {
		return nil, MatchNone
	}
	product := strings.TrimSpace(dmi.ProductName)
	if product == "" {
		return nil, MatchNone
	}

	// Tier 1 — exact match on the live ProductName.
	if entry := cat.Lookup(product); entry != nil {
		return entry, MatchExact
	}

	// Tier 1b — exact match, case-insensitive (some OEMs change
	// casing between catalog contribution and shipped DMI).
	for _, entry := range cat.Entries {
		if strings.EqualFold(entry.Config.NotebookModel, product) {
			return entry, MatchExact
		}
	}

	// Tier 2 — wildcard / prefix glob. Upstream catalogues use the
	// "-cs0xxx" / "-aXXX" convention to denote a family with one
	// shared firmware. We strip the trailing x-runs from both sides
	// and compare prefixes.
	productStem := stripTrailingX(product)
	var prefixBest *Entry
	var prefixBestLen int
	for _, entry := range cat.Entries {
		modelStem := stripTrailingX(entry.Config.NotebookModel)
		if modelStem == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(productStem), strings.ToLower(modelStem)) ||
			strings.HasPrefix(strings.ToLower(modelStem), strings.ToLower(productStem)) {
			// Prefer the longest matched stem when multiple
			// candidates collide.
			if len(modelStem) > prefixBestLen {
				prefixBest = entry
				prefixBestLen = len(modelStem)
			}
		}
	}
	if prefixBest != nil {
		return prefixBest, MatchPrefix
	}

	// Tier 3 — substring fallback. Looser but useful for OEM rebrands.
	lowerProduct := strings.ToLower(product)
	var substrBest *Entry
	var substrBestLen int
	for _, entry := range cat.Entries {
		lowerModel := strings.ToLower(entry.Config.NotebookModel)
		if lowerModel == "" {
			continue
		}
		if strings.Contains(lowerProduct, lowerModel) || strings.Contains(lowerModel, lowerProduct) {
			if len(lowerModel) > substrBestLen {
				substrBest = entry
				substrBestLen = len(lowerModel)
			}
		}
	}
	if substrBest != nil {
		return substrBest, MatchSubstring
	}

	return nil, MatchNone
}

// stripTrailingX removes runs of x / X at the end of the string,
// folding upstream-convention placeholders ("-cs0xxx") to their
// glob-stem ("-cs0"). Whitespace + punctuation around the stem is
// preserved so case-folded prefix matches still anchor cleanly.
func stripTrailingX(s string) string {
	s = strings.TrimSpace(s)
	// Walk from the end; stop at the first non-x rune.
	end := len(s)
	for end > 0 {
		r := s[end-1]
		if r == 'x' || r == 'X' {
			end--
			continue
		}
		break
	}
	return strings.TrimRight(s[:end], "- ")
}
