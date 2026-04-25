package hwdb

import "time"

// EffectiveControllerProfile is the fully resolved fan controller profile for
// a specific board. The matcher resolves layers 1-3 (driver→chip→board) and
// optionally overlays layer-4 calibration results to produce this struct.
// PR 2b consumes this struct; the apply path dispatches PWM-unit-aware writes
// based on it.
type EffectiveControllerProfile struct {
	// From driver profile (layer 1)
	Module                            string
	Family                            string
	Capability                        Capability
	PWMUnit                           PWMUnit
	PWMUnitMax                        *int
	PWMEnableModes                    map[string]string // key: enable int as string, val: mode name
	OffBehaviour                      OffBehaviour
	PollingLatencyHint                time.Duration
	RecommendedAlternativeDriver      *AlternativeDriver
	ConflictsWithUserspace            []UserspaceConflict
	FanControlCapable                 bool
	FanControlVia                     *string
	RequiredModprobeArgs              []ModprobeArg
	PWMPolarityReservation            PolarityReservation
	ExitBehaviour                     ExitBehaviour
	RuntimeConflictDetectionSupported bool
	FirmwareCurveOffloadCapable       bool

	// From chip profile (layer 2)
	ChipName         string
	ChannelOverrides map[string]ChannelOverride

	// From board profile (layer 3)
	BoardID      *string
	CPUTINFloats bool

	// From calibration (layer 4 — per channel; nil map means no calibration loaded)
	CalibrationByChannel map[int]*CalibrationResult

	// Diagnostics from the matching process
	Diagnostics MatchDiagnostics
}

// ResolveEffectiveProfile merges driver → chip → board (→ calibration) into a
// single EffectiveControllerProfile. Layer precedence: board > chip > driver
// for static fields; calibration overrides all three for runtime fields.
// RULE-HWDB-PR2-10.
func ResolveEffectiveProfile(
	driver *DriverProfile,
	chip *ChipProfile,
	board *BoardProfileV2,
	cal map[int]*CalibrationResult,
	diag MatchDiagnostics,
) *EffectiveControllerProfile {
	ecp := &EffectiveControllerProfile{
		// Layer 1: driver defaults
		Module:                            driver.Module,
		Family:                            driver.Family,
		Capability:                        driver.Capability,
		PWMUnit:                           driver.PWMUnit,
		PWMUnitMax:                        driver.PWMUnitMax,
		PWMEnableModes:                    driver.PWMEnableModes,
		OffBehaviour:                      driver.OffBehaviour,
		PollingLatencyHint:                time.Duration(driver.PollingLatencyMSHint) * time.Millisecond,
		RecommendedAlternativeDriver:      driver.RecommendedAlternativeDriver,
		ConflictsWithUserspace:            driver.ConflictsWithUserspace,
		FanControlCapable:                 driver.FanControlCapable,
		FanControlVia:                     driver.FanControlVia,
		RequiredModprobeArgs:              driver.RequiredModprobeArgs,
		PWMPolarityReservation:            driver.PWMPolarityReservation,
		ExitBehaviour:                     driver.ExitBehaviour,
		RuntimeConflictDetectionSupported: ptrBool(driver.RuntimeConflictDetectionSupported),
		FirmwareCurveOffloadCapable:       FirmwareCurveOffloadCapable(driver),
		Diagnostics:                       diag,
	}

	// Layer 2: chip overrides
	if chip != nil {
		ecp.ChipName = chip.Name
		ecp.ChannelOverrides = chip.ChannelOverrides
		applyDriverOverrides(ecp, &chip.Overrides)
	}

	// Layer 3: board overrides
	if board != nil {
		ecp.BoardID = &board.ID
		ecp.CPUTINFloats = board.Overrides.CPUTINFloats
		// Board-level modprobe args and conflicts are additive.
		ecp.RequiredModprobeArgs = append(ecp.RequiredModprobeArgs, board.RequiredModprobeArgs...)
		ecp.ConflictsWithUserspace = append(ecp.ConflictsWithUserspace, board.ConflictsWithUserspace...)
		// Apply board overrides (same mechanism as chip overrides).
		applyDriverOverrides(ecp, &board.Overrides.DriverProfileOverrides)
		if board.Overrides.PollingLatencyMSHint != nil {
			ecp.PollingLatencyHint = time.Duration(*board.Overrides.PollingLatencyMSHint) * time.Millisecond
		}
	}

	// Layer 4: calibration results (nil-safe)
	ecp.CalibrationByChannel = cal

	return ecp
}

// applyDriverOverrides merges non-nil override fields onto ecp (in-place).
// Called for both chip and board override layers.
func applyDriverOverrides(ecp *EffectiveControllerProfile, ov *DriverProfileOverrides) {
	if ov == nil {
		return
	}
	if ov.Capability != nil {
		ecp.Capability = *ov.Capability
	}
	if ov.PWMUnit != nil {
		ecp.PWMUnit = *ov.PWMUnit
	}
	if ov.PWMUnitMax != nil {
		ecp.PWMUnitMax = ov.PWMUnitMax
	}
	if ov.PWMEnableModes != nil {
		ecp.PWMEnableModes = ov.PWMEnableModes
	}
	if ov.OffBehaviour != nil {
		ecp.OffBehaviour = *ov.OffBehaviour
	}
	if ov.PollingLatencyMSHint != nil {
		ecp.PollingLatencyHint = time.Duration(*ov.PollingLatencyMSHint) * time.Millisecond
	}
	if ov.FanControlCapable != nil {
		ecp.FanControlCapable = *ov.FanControlCapable
	}
	if ov.FanControlVia != nil {
		ecp.FanControlVia = ov.FanControlVia
	}
	if ov.PWMPolarityReservation != nil {
		ecp.PWMPolarityReservation = *ov.PWMPolarityReservation
	}
	if ov.ExitBehaviour != nil {
		ecp.ExitBehaviour = *ov.ExitBehaviour
	}
	if ov.RuntimeConflictDetectionSupported != nil {
		ecp.RuntimeConflictDetectionSupported = *ov.RuntimeConflictDetectionSupported
	}
	if ov.FirmwareCurveOffloadOverride != nil {
		ecp.FirmwareCurveOffloadCapable = *ov.FirmwareCurveOffloadOverride
	}
}

// ptrBool safely dereferences a bool pointer, returning false for nil.
func ptrBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// BoardProfileV2 is the PR 2a board profile shape. It wraps the PR 1 Profile
// struct for forward compatibility. Board profiles continue to live in
// profiles-v1.yaml for the PR 2 release; a dedicated board catalog is PR 2b scope.
type BoardProfileV2 struct {
	ID                     string
	DMIBoardVendor         string
	DMIBoardName           string
	PrimaryControllerChip  string
	RequiredModprobeArgs   []ModprobeArg
	ConflictsWithUserspace []UserspaceConflict
	Overrides              BoardOverrides
}

// BoardOverrides holds board-specific overrides merged on top of chip/driver.
type BoardOverrides struct {
	DriverProfileOverrides
	CPUTINFloats         bool `yaml:"cputin_floats,omitempty"`
	PollingLatencyMSHint *int `yaml:"polling_latency_ms_hint,omitempty"`
}
