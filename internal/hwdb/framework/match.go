package framework

import (
	"strings"

	"github.com/ventd/ventd/internal/hwdb"
)

// SourceMainline is the canonical fw-fanctrl preset set, recommended for any
// Framework laptop. SourceAMD is the fw-fanctrl-AMD fork's battery-aware
// variant, surfaced as an alternative.
const (
	SourceMainline = "fw-fanctrl"
	SourceAMD      = "fw-fanctrl-amd"
)

// IsFramework reports whether the DMI identifies a Framework Computer laptop.
// Framework firmware sets sys_vendor "Framework" across the 13"/16" line
// (both Intel and AMD mainboards); board_name is the per-mainboard FRANxxxx
// code. Matching on sys_vendor alone is intentional — the curve presets are
// not mainboard-specific, so any Framework host gets the same recommendation.
func IsFramework(dmi hwdb.DMI) bool {
	return strings.EqualFold(strings.TrimSpace(dmi.SysVendor), "Framework")
}

// Match returns the canonical fw-fanctrl preset entry for a Framework host, or
// (nil, false) on a non-Framework host. The mainline preset set is canonical;
// the AMD fork's battery-aware variant is reachable via Catalog.Lookup(SourceAMD)
// and surfaced separately by the doctor (deterministic — pure function of the
// catalog + DMI, RULE-FRAMEWORK-CATALOG-03).
func Match(cat *Catalog, dmi hwdb.DMI) (*Entry, bool) {
	if cat == nil || !IsFramework(dmi) {
		return nil, false
	}
	if e := cat.Lookup(SourceMainline); e != nil {
		return e, true
	}
	return nil, false
}
