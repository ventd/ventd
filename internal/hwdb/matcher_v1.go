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
// (keyed by ChannelKey). The calibration layer is overlaid as layer 4.
func MatchV1WithCalibration(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint,
	cal map[ChannelKey]*ChannelCalibration,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	return matchV1WithDiag(cat, chipName, dmi, cal, log)
}

// MatchV1WithDT is MatchV1 extended with device-tree signals for ARM/SBC systems.
// livedt should be obtained via ReadDTData; dmiPresent via IsDMIPresent.
// When dmiPresent is false, board entries with dt_fingerprint are considered for
// tier-1 matching. RULE-FINGERPRINT-06, RULE-FINGERPRINT-07.
func MatchV1WithDT(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint,
	livedt LiveDTData,
	dmiPresent bool,
	cal map[ChannelKey]*ChannelCalibration,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	return matchV1Full(cat, chipName, dmi, livedt, dmiPresent, cal, log)
}

// DMIFingerprint holds the DMI/SMBIOS fields used for board matching.
// v1.1 adds BoardVersion and BiosVersion for Lenovo Legion family dispatch.
type DMIFingerprint struct {
	SysVendor    string
	ProductName  string
	BoardVendor  string
	BoardName    string
	BoardVersion string // v1.1: /sys/class/dmi/id/board_version
	BiosVersion  string // v1.1: /sys/class/dmi/id/bios_version
}

func matchV1WithDiag(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint,
	cal map[ChannelKey]*ChannelCalibration,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	dmiPresent := dmi.SysVendor != ""
	return matchV1Full(cat, chipName, dmi, LiveDTData{}, dmiPresent, cal, log)
}

func matchV1Full(
	cat *Catalog,
	chipName string,
	dmi DMIFingerprint,
	livedt LiveDTData,
	dmiPresent bool,
	cal map[ChannelKey]*ChannelCalibration,
	log *slog.Logger,
) (*EffectiveControllerProfile, error) {
	var diag MatchDiagnostics
	diag.DetectedChipName = chipName

	// Tier 1: board fingerprint match (DMI or DT depending on dmiPresent).
	// Wildcard-only DMI entries (sys_vendor="*") are tier-3 generics — skip them here.
	for _, entry := range cat.Boards {
		if entry.DMIFingerprint != nil && isWildcardDMI(entry.DMIFingerprint) {
			continue
		}
		if !MatchBoardEntry(entry, dmi, livedt, dmiPresent) {
			continue
		}
		chip, ok := cat.Chips[entry.PrimaryController.Chip]
		if !ok {
			continue // board references unknown chip — skip rather than error
		}
		driver, ok := cat.Drivers[chip.InheritsDriver]
		if !ok {
			continue
		}
		diag.Tier = MatchTierBoard
		diag.Confidence = 0.9
		diag.MatchedBoardID = entry.ID
		diag.MatchedChipName = chip.Name
		diag.MatchedDriverModule = driver.Module

		log.Debug("hwdb: tier-1 board match",
			slog.String("board", entry.ID),
			slog.String("chip", chip.Name),
			slog.String("driver", driver.Module))

		boardV2 := boardEntryToProfileV2(entry)
		ecp := ResolveEffectiveProfile(driver, chip, boardV2, cal, diag)
		if ecp.Unsupported {
			LogUnsupportedOnce(entry.ID, log)
		}
		return ecp, nil
	}

	// Tier 3: chip-family fallback by detected hwmon chip name.
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

// isWildcardDMI reports whether a DMI fingerprint has all primary identifier
// fields set to wildcard/empty values (indicating a tier-3 generic entry).
func isWildcardDMI(fp *BoardDMIFingerprint) bool {
	wild := func(s string) bool { return s == "" || s == "*" }
	return wild(fp.SysVendor) && wild(fp.ProductName) && wild(fp.BoardVendor) && wild(fp.BoardName)
}

// boardEntryToProfileV2 converts a BoardCatalogEntry to the BoardProfileV2
// shape consumed by ResolveEffectiveProfile.
func boardEntryToProfileV2(entry *BoardCatalogEntry) *BoardProfileV2 {
	bp := &BoardProfileV2{
		ID:                    entry.ID,
		PrimaryControllerChip: entry.PrimaryController.Chip,
		Overrides: BoardOverrides{
			CPUTINFloats:            entry.Overrides.CPUTINFloats,
			Unsupported:             entry.Overrides.Unsupported,
			CoolingDeviceMustDetach: entry.Overrides.CoolingDeviceMustDetach,
		},
	}
	if entry.DMIFingerprint != nil {
		bp.DMIBoardVendor = entry.DMIFingerprint.BoardVendor
		bp.DMIBoardName = entry.DMIFingerprint.BoardName
	}
	for _, arg := range entry.RequiredModprobeArgs {
		bp.RequiredModprobeArgs = append(bp.RequiredModprobeArgs, ModprobeArg{Arg: arg})
	}
	for _, daemon := range entry.ConflictsWithUserspace {
		bp.ConflictsWithUserspace = append(bp.ConflictsWithUserspace, UserspaceConflict{Daemon: daemon})
	}
	return bp
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
