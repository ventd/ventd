package asus

import (
	"strings"

	"github.com/ventd/ventd/internal/hwdb"
)

// SourceGHelper is the canonical vendored preset set (g-helper's default
// curves), recommended for any ASUS host.
const SourceGHelper = "g-helper"

// IsASUS reports whether the DMI identifies an ASUS machine. ASUS firmware sets
// sys_vendor "ASUSTeK COMPUTER INC." across the ROG/TUF/Zenbook/Vivobook/ProArt
// line (and "ASUS" on a handful of older/OEM boards), so the match is a
// case-folded substring on "asus". Matching on sys_vendor alone is intentional
// — the vendored curves are g-helper's model-agnostic defaults, so any ASUS
// host gets the same recommendation.
func IsASUS(dmi hwdb.DMI) bool {
	return strings.Contains(strings.ToLower(dmi.SysVendor), "asus")
}

// Match returns the canonical g-helper preset entry for an ASUS host, or
// (nil, false) on a non-ASUS host. Deterministic — a pure function of the
// catalog + DMI (RULE-ASUS-CATALOG-03), no map-iteration order leaking into the
// choice.
func Match(cat *Catalog, dmi hwdb.DMI) (*Entry, bool) {
	if cat == nil || !IsASUS(dmi) {
		return nil, false
	}
	if e := cat.Lookup(SourceGHelper); e != nil {
		return e, true
	}
	return nil, false
}
