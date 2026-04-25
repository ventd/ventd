package hwdb

import (
	"log/slog"
	"strings"
)

// MatchV1 runs the three-tier resolver against a detected hwmon chip name and
// optional DMI board fingerprint. Tier definitions:
//
//  1. Board tier: exact DMI fingerprint match against a board profile.
//  2. Vendor tier: DMI vendor partial match (sys_vendor prefix against board_vendor).
//  3. Chip tier: generic chip-family fallback by hwmon name value.
//
// On any tier match, the resolver flattens driver → chip → board into an
// EffectiveControllerProfile and records diagnostics in MatchDiagnostics.
//
// ErrNoMatch is returned when no tier matches the provided signals.
func MatchV1(cat *Catalog, chipName string, dmi DMIFingerprint) (*EffectiveControllerProfile, error) {
	ecp, err := matchV1WithDiag(cat, chipName, dmi, nil, slog.Default())
	if err != nil {
		return nil, err
	}
	return ecp, nil
}

// MatchV1WithCalibration is MatchV1 extended with a calibration results map
// (keyed by channel index). The calibration layer is overlaid as layer 4.
func MatchV1WithCalibration(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint,
	cal map[int]*CalibrationResult,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	return matchV1WithDiag(cat, chipName, dmi, cal, log)
}

// DMIFingerprint holds the DMI/SMBIOS fields used for board matching.
type DMIFingerprint struct {
	SysVendor   string
	ProductName string
	BoardVendor string
	BoardName   string
}

func matchV1WithDiag(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint, //nolint:unparam // board matching uses dmi in PR 2b
	cal map[int]*CalibrationResult,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	var diag MatchDiagnostics
	diag.DetectedChipName = chipName

	// Tier 1 and 2: board/vendor matching via DMI fields is PR 2b scope. PR 2a
	// seeds the driver/chip catalog only. The dmi parameter is accepted now so
	// callers can pass it without a signature change in PR 2b.
	_ = dmi

	// Tier 3: chip-family fallback by name.
	chip, ok := cat.Chips[chipName]
	if !ok {
		// Normalise common underscored/hyphenated variants.
		normalised := strings.ReplaceAll(chipName, "_", "-")
		chip, ok = cat.Chips[normalised]
		if !ok {
			return nil, ErrNoMatch
		}
	}

	driver, ok := cat.Drivers[chip.InheritsDriver]
	if !ok {
		return nil, ErrNoMatch
	}

	diag.Tier = MatchTierChip
	diag.Confidence = 0.6
	diag.MatchedChipName = chip.Name
	diag.MatchedDriverModule = driver.Module

	log.Debug("hwdb: tier-3 chip match",
		slog.String("chip", chip.Name),
		slog.String("driver", driver.Module))

	ecp := ResolveEffectiveProfile(driver, chip, nil, cal, diag)
	return ecp, nil
}

// MigrateModuleProfileToECP resolves a PR 1 hardware.pwm_control string to an
// EffectiveControllerProfile using the PR 2 catalog. RULE-HWDB-PR2-11:
// first attempts chip-name lookup, then driver-module lookup with a warning.
// Returns ErrNoMatch only when neither lookup resolves.
func MigrateModuleProfileToECP(cat *Catalog, pwmControl string, log *slog.Logger) (*EffectiveControllerProfile, error) {
	var diag MatchDiagnostics

	// Try chip name first.
	if chip, ok := cat.Chips[pwmControl]; ok {
		if driver, ok := cat.Drivers[chip.InheritsDriver]; ok {
			diag.Tier = MatchTierChip
			diag.Confidence = 0.7
			diag.MatchedChipName = chip.Name
			diag.MatchedDriverModule = driver.Module
			return ResolveEffectiveProfile(driver, chip, nil, nil, diag), nil
		}
	}

	// Fallback: try driver module name, synthesise anonymous chip with no overrides.
	if driver, ok := cat.Drivers[pwmControl]; ok {
		warn := "PR 1 pwm_control string matched a driver module, not a chip name; using anonymous chip profile"
		diag.Warnings = append(diag.Warnings, warn)
		log.Warn("hwdb: PR1→PR2 migration fallback",
			slog.String("pwm_control", pwmControl),
			slog.String("warning", warn))

		diag.Tier = MatchTierChip
		diag.Confidence = 0.5
		diag.MatchedDriverModule = driver.Module

		// Synthesise a minimal chip profile with no overrides.
		anonChip := &ChipProfile{
			Name:           pwmControl,
			InheritsDriver: driver.Module,
			Description:    "anonymous chip (synthesised from PR 1 pwm_control migration)",
		}
		return ResolveEffectiveProfile(driver, anonChip, nil, nil, diag), nil
	}

	return nil, ErrNoMatch
}
