// Package fallback implements the R8 fallback-tier classifier from
// spec-v0_5_9-confidence-controller.md §2.4. Each `probe.Controllable
// Channel` is mapped to a tier in [0, 7], and the per-tier ceiling
// (in `internal/confidence/layer_a`'s `R8Ceiling`) clamps how much
// trust the predictive controller can place in this channel's
// measurement chain.
//
// v0.5.9 ships a 3-tier minimum-viable classifier:
//
//   - Tier 0 (RPM tach):       `TachPath != ""`
//   - Tier 4 (thermal-invert): tach-less, but the EC driver is in a
//     known-laptop / known-NAS family that exposes a bound thermal
//     sensor we can invert.
//   - Tier 7 (open-loop):      no useful feedback chain.
//
// Tiers 1/2/3/5/6 require infrastructure that doesn't exist yet
// (`bmc.View`, peer-fan correlation, RAPL probe, thermal-invert hint
// plumbing). Adding them in a follow-up patch is non-breaking: the
// per-channel ceiling table in `layer_a/tier.go` already covers 0-7,
// so a future SelectTier returning Tier 1/2/3/5/6 just lands lower
// `R8Ceiling` values without code changes elsewhere.
package fallback

import (
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/probe"
)

// thermalInvertDrivers is the set of EC / SMC drivers that report a
// fan in their hwmon node but do NOT expose a tach (`fan*_input`).
// On these channels the predictive controller can still engage if a
// bound thermal sensor exists — Layer A treats this as a Tier 4
// fallback (R8 ceiling 0.45). The list intentionally excludes
// drivers whose hwmon nodes ALWAYS expose a tach (nct6798 etc.) —
// those ride Tier 0 when present.
var thermalInvertDrivers = map[string]struct{}{
	"legion-laptop":    {}, // Lenovo Legion family (includes IdeaPad Gaming / LOQ)
	"msi-ec":           {}, // MSI gaming laptops, mainline since 6.10
	"thinkpad_acpi":    {}, // ThinkPad fan_level via /proc/acpi/ibm/fan
	"dell-smm-hwmon":   {}, // Dell consumer + Precision SFF
	"hp-wmi-sensors":   {}, // HP Pavilion / EliteBook / Omen
	"asus-wmi-sensors": {}, // ASUS WMI ROG / TUF / ROG Ally
	"surface_fan":      {}, // Microsoft Surface
	"applesmc":         {}, // Intel-era Macs
	"macsmc-hwmon":     {}, // Apple Silicon (Asahi)
	"qnap8528":         {}, // QNAP TS-series NAS EC
	// Note: Steam Deck (firmware-owns-fans) and most embedded ECs
	// fall through to Tier 7. That's correct — they don't admit
	// predictive control today.
}

// SelectTier maps `ch` to its R8 fallback tier. nil channel → Tier 7.
//
// The classifier is intentionally conservative: when in doubt, fall
// through to Tier 7 (R8 ceiling 0.0 ⇒ conf_A=0 ⇒ w_pred=0 ⇒ pure
// reactive control). Mis-classifying upward (Tier 0 when no real
// tach exists) would let the predictive controller run on garbage
// RPM data; mis-classifying downward at worst preserves v0.4.x
// behaviour for that channel.
func SelectTier(ch *probe.ControllableChannel) uint8 {
	if ch == nil {
		return layer_a.TierOpenLoopPinned
	}
	if ch.TachPath != "" {
		return layer_a.TierRPMTach
	}
	if _, ok := thermalInvertDrivers[ch.Driver]; ok {
		return layer_a.TierThermalInvert
	}
	return layer_a.TierOpenLoopPinned
}
