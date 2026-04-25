package hwdb

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed catalog/drivers/*.yaml catalog/chips/*.yaml
var catalogFS embed.FS

// Capability classifies what level of PWM control a driver provides.
type Capability string

const (
	CapabilityRWFull              Capability = "rw_full"
	CapabilityRWQuirk             Capability = "rw_quirk"
	CapabilityRWStep              Capability = "rw_step"
	CapabilityRWProc              Capability = "rw_proc"
	CapabilityROSensorOnly        Capability = "ro_sensor_only"
	CapabilityRODesign            Capability = "ro_design"
	CapabilityROPendingOOT        Capability = "ro_pending_oot"
	CapabilityRequiresUserspaceEC Capability = "requires_userspace_ec"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilityRWFull: {}, CapabilityRWQuirk: {}, CapabilityRWStep: {},
	CapabilityRWProc: {}, CapabilityROSensorOnly: {}, CapabilityRODesign: {},
	CapabilityROPendingOOT: {}, CapabilityRequiresUserspaceEC: {},
}

// PWMUnit describes the semantics of the sysfs pwmN attribute value.
type PWMUnit string

const (
	PWMUnitDuty0255      PWMUnit = "duty_0_255"
	PWMUnitStep0N        PWMUnit = "step_0_N"
	PWMUnitThinkpadLevel PWMUnit = "thinkpad_level"
	PWMUnitPercentage    PWMUnit = "percentage_0_100"
	PWMUnitCoolingLevel  PWMUnit = "cooling_level"
)

var knownPWMUnits = map[PWMUnit]struct{}{
	PWMUnitDuty0255: {}, PWMUnitStep0N: {}, PWMUnitThinkpadLevel: {},
	PWMUnitPercentage: {}, PWMUnitCoolingLevel: {},
}

// OffBehaviour describes what happens when pwm=0 is written in manual mode.
type OffBehaviour string

const (
	OffBehaviourStops             OffBehaviour = "stops"
	OffBehaviourFallsToMin        OffBehaviour = "falls_to_min"
	OffBehaviourFallsToAutoSilent OffBehaviour = "falls_to_auto_silently"
	OffBehaviourBIOSDependent     OffBehaviour = "bios_dependent"
	OffBehaviourForcesMax         OffBehaviour = "forces_max"
	OffBehaviourStateOff          OffBehaviour = "state_off"
)

var knownOffBehaviours = map[OffBehaviour]struct{}{
	OffBehaviourStops: {}, OffBehaviourFallsToMin: {}, OffBehaviourFallsToAutoSilent: {},
	OffBehaviourBIOSDependent: {}, OffBehaviourForcesMax: {}, OffBehaviourStateOff: {},
}

// PolarityReservation declares what calibration should expect about PWM polarity.
type PolarityReservation string

const (
	PolarityReservationStaticNormal   PolarityReservation = "static_normal"
	PolarityReservationStaticInverted PolarityReservation = "static_inverted"
	PolarityReservationProbeRequired  PolarityReservation = "probe_required"
	PolarityReservationNotApplicable  PolarityReservation = "not_applicable"
)

var knownPolarityReservations = map[PolarityReservation]struct{}{
	PolarityReservationStaticNormal: {}, PolarityReservationStaticInverted: {},
	PolarityReservationProbeRequired: {}, PolarityReservationNotApplicable: {},
}

// ExitBehaviour declares the driver's shutdown safety action.
type ExitBehaviour string

const (
	ExitBehaviourForceMax      ExitBehaviour = "force_max"
	ExitBehaviourRestoreAuto   ExitBehaviour = "restore_auto"
	ExitBehaviourPreserve      ExitBehaviour = "preserve"
	ExitBehaviourBIOSDependent ExitBehaviour = "bios_dependent"
)

var knownExitBehaviours = map[ExitBehaviour]struct{}{
	ExitBehaviourForceMax: {}, ExitBehaviourRestoreAuto: {},
	ExitBehaviourPreserve: {}, ExitBehaviourBIOSDependent: {},
}

// ConflictResolution describes how ventd handles a detected userspace conflict.
type ConflictResolution string

const (
	ConflictResolutionStopAndDisable ConflictResolution = "stop_and_disable"
	ConflictResolutionCoexistWarning ConflictResolution = "coexist_warning"
	ConflictResolutionRefuseInstall  ConflictResolution = "refuse_install"
)

// InstallMethod for recommended alternative driver.
type InstallMethod string

const (
	InstallMethodDKMS          InstallMethod = "dkms"
	InstallMethodManualCompile InstallMethod = "manual_compile"
	InstallMethodDistroPackage InstallMethod = "distro_package"
)

// AlternativeDriver describes an OOT driver that replaces a read-only mainline one.
type AlternativeDriver struct {
	Module          string        `yaml:"module"`
	Source          string        `yaml:"source"`
	InstallMethod   InstallMethod `yaml:"install_method"`
	PackageHint     *string       `yaml:"package_hint"`
	Reason          string        `yaml:"reason"`
	AppliesToBoards []string      `yaml:"applies_to_boards"`
	ModuleArgsHint  []string      `yaml:"module_args_hint"`
}

// UserspaceConflict describes a daemon that conflicts with ventd on this driver.
type UserspaceConflict struct {
	Daemon     string             `yaml:"daemon"`
	Detection  string             `yaml:"detection"`
	Resolution ConflictResolution `yaml:"resolution"`
	Reason     string             `yaml:"reason"`
}

// ModprobeArg describes a required modprobe argument and its risk level.
type ModprobeArg struct {
	Arg        string `yaml:"arg"`
	Reason     string `yaml:"reason"`
	Risk       string `yaml:"risk"`
	RiskDetail string `yaml:"risk_detail,omitempty"`
}

// DriverProfile is one entry in the driver catalog. It describes a kernel
// module's fan control capabilities, PWM semantics, and safety properties.
// All fields are required; see RULE-HWDB-PR2-01.
type DriverProfile struct {
	Module                            string              `yaml:"module"`
	Family                            string              `yaml:"family"`
	Description                       string              `yaml:"description"`
	Capability                        Capability          `yaml:"capability"`
	PWMUnit                           PWMUnit             `yaml:"pwm_unit"`
	PWMUnitMax                        *int                `yaml:"pwm_unit_max"`
	PWMEnableModes                    map[string]string   `yaml:"pwm_enable_modes"`
	OffBehaviour                      OffBehaviour        `yaml:"off_behaviour"`
	PollingLatencyMSHint              int                 `yaml:"polling_latency_ms_hint"`
	RecommendedAlternativeDriver      *AlternativeDriver  `yaml:"recommended_alternative_driver"`
	ConflictsWithUserspace            []UserspaceConflict `yaml:"conflicts_with_userspace"`
	FanControlCapable                 bool                `yaml:"fan_control_capable"`
	FanControlVia                     *string             `yaml:"fan_control_via"`
	RequiredModprobeArgs              []ModprobeArg       `yaml:"required_modprobe_args"`
	PWMPolarityReservation            PolarityReservation `yaml:"pwm_polarity_reservation"`
	ExitBehaviour                     ExitBehaviour       `yaml:"exit_behaviour"`
	RuntimeConflictDetectionSupported *bool               `yaml:"runtime_conflict_detection_supported"` // pointer to detect absence
	FirmwareCurveOffloadOverride      *bool               `yaml:"firmware_curve_offload_override"`
	Citations                         []string            `yaml:"citations"`
}

// ChannelOverride captures per-channel restrictions on a chip.
type ChannelOverride struct {
	PWMEnableModesLockedTo *string `yaml:"pwm_enable_modes_locked_to"`
}

// ChipProfile is one entry in the chip catalog. It describes a specific chip
// (identified by its hwmon /sys/class/hwmon/*/name value) and its overrides
// relative to the parent driver profile. inherits_driver is required.
type ChipProfile struct {
	Name             string                     `yaml:"name"`
	InheritsDriver   string                     `yaml:"inherits_driver"`
	Description      string                     `yaml:"description"`
	Overrides        DriverProfileOverrides     `yaml:"overrides"`
	ChannelOverrides map[string]ChannelOverride `yaml:"channel_overrides"`
	Citations        []string                   `yaml:"citations"`
}

// DriverProfileOverrides holds the subset of DriverProfile fields a chip can override.
// All fields are pointers/nullable so unset fields don't accidentally override parents.
type DriverProfileOverrides struct {
	Capability                        *Capability          `yaml:"capability,omitempty"`
	PWMUnit                           *PWMUnit             `yaml:"pwm_unit,omitempty"`
	PWMUnitMax                        *int                 `yaml:"pwm_unit_max,omitempty"`
	PWMEnableModes                    map[string]string    `yaml:"pwm_enable_modes,omitempty"`
	OffBehaviour                      *OffBehaviour        `yaml:"off_behaviour,omitempty"`
	PollingLatencyMSHint              *int                 `yaml:"polling_latency_ms_hint,omitempty"`
	FanControlCapable                 *bool                `yaml:"fan_control_capable,omitempty"`
	FanControlVia                     *string              `yaml:"fan_control_via,omitempty"`
	PWMPolarityReservation            *PolarityReservation `yaml:"pwm_polarity_reservation,omitempty"`
	ExitBehaviour                     *ExitBehaviour       `yaml:"exit_behaviour,omitempty"`
	RuntimeConflictDetectionSupported *bool                `yaml:"runtime_conflict_detection_supported,omitempty"`
	FirmwareCurveOffloadOverride      *bool                `yaml:"firmware_curve_offload_override,omitempty"`
}

// CatalogDocument is the top-level shape of a catalog YAML file (driver or chip).
type CatalogDocument struct {
	SchemaVersion  string          `yaml:"schema_version"`
	DriverProfiles []DriverProfile `yaml:"driver_profiles,omitempty"`
	ChipProfiles   []ChipProfile   `yaml:"chip_profiles,omitempty"`
}

// Catalog holds the full merged driver + chip catalog loaded from the embedded FS.
type Catalog struct {
	Drivers map[string]*DriverProfile // keyed by module name
	Chips   map[string]*ChipProfile   // keyed by chip name value
}

// CalibrationRun is the top-level runtime probe result, written as JSON to disk
// at /var/lib/ventd/calibration/<dmi_fingerprint>-<bios_safe>.json.
// Unifies the previous parallel types (hwdb.CalibrationResult and
// calibration.CalibrationResult) into a single authoritative definition.
type CalibrationRun struct {
	SchemaVersion   int                  `json:"schema_version" yaml:"schema_version"`
	DMIFingerprint  string               `json:"dmi_fingerprint" yaml:"dmi_fingerprint"`
	BIOSVersion     string               `json:"bios_version" yaml:"bios_version"`
	BIOSReleaseDate string               `json:"bios_release_date,omitempty" yaml:"bios_release_date,omitempty"`
	CalibratedAt    time.Time            `json:"calibrated_at" yaml:"calibrated_at"`
	VentdVersion    string               `json:"ventd_version,omitempty" yaml:"ventd_version,omitempty"`
	Channels        []ChannelCalibration `json:"channels" yaml:"channels"`
}

// ChannelCalibration is the per-channel runtime calibration data populated by
// the probe (internal/calibration) and consumed by the controller apply path
// (internal/controller). Keyed via ChannelKey on EffectiveControllerProfile.
type ChannelCalibration struct {
	HwmonName         string `json:"hwmon_name" yaml:"hwmon_name"`
	ChannelIndex      int    `json:"channel_index" yaml:"channel_index"`
	PolarityInverted  bool   `json:"polarity_inverted" yaml:"polarity_inverted"`
	MinResponsivePWM  *int   `json:"min_responsive_pwm" yaml:"min_responsive_pwm"`
	MaxResponsivePWM  *int   `json:"max_responsive_pwm,omitempty" yaml:"max_responsive_pwm,omitempty"`
	MaxObservedRPM    *int   `json:"max_observed_rpm,omitempty" yaml:"max_observed_rpm,omitempty"`
	StallPWM          *int   `json:"stall_pwm" yaml:"stall_pwm"`
	Phantom           bool   `json:"phantom" yaml:"phantom"`
	BIOSOverridden    bool   `json:"bios_overridden" yaml:"bios_overridden"`
	ProbeMethod       string `json:"probe_method" yaml:"probe_method"`
	ProbeDurationMS   int    `json:"probe_duration_ms" yaml:"probe_duration_ms"`
	ProbeObservations int    `json:"probe_observations" yaml:"probe_observations"`
}

// ChannelKey is the lookup key for EffectiveControllerProfile.CalibrationByChannel.
// Using Hwmon+Index together preserves chip identity when multiple chips are present.
type ChannelKey struct {
	Hwmon string // e.g. "hwmon4"
	Index int    // e.g. 2 for pwm2
}

// Sentinel errors for the apply-path refusal. Returned by ShouldApplyCurve.
var (
	ErrPhantom        = errors.New("channel is monitor-only (phantom)")
	ErrBIOSOverridden = errors.New("channel is monitor-only (bios_overridden: BIOS actively overrides PWM writes)")
)

// ErrCatalog is the sentinel for catalog validation failures.
var ErrCatalog = fmt.Errorf("hwdb catalog")

func catalogErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrCatalog}, args...)...)
}

// LoadCatalog reads all driver and chip profiles from the embedded catalog FS
// and returns a validated Catalog. RULE-HWDB-PR2-12: any profile violating
// PR2-01..05 causes the entire load to fail with a structured error.
func LoadCatalog() (*Catalog, error) {
	cat := &Catalog{
		Drivers: make(map[string]*DriverProfile),
		Chips:   make(map[string]*ChipProfile),
	}

	// Load all driver YAML files.
	driverEntries, err := fs.ReadDir(catalogFS, "catalog/drivers")
	if err != nil {
		return nil, catalogErrorf("read drivers dir: %w", err)
	}
	for _, e := range driverEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := catalogFS.ReadFile("catalog/drivers/" + e.Name())
		if err != nil {
			return nil, catalogErrorf("read %s: %w", e.Name(), err)
		}
		var doc CatalogDocument
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, catalogErrorf("parse %s: %w", e.Name(), err)
		}
		for i := range doc.DriverProfiles {
			dp := &doc.DriverProfiles[i]
			if err := validateDriverProfile(dp); err != nil {
				return nil, err
			}
			cat.Drivers[dp.Module] = dp
		}
	}

	// Load all chip YAML files.
	chipEntries, err := fs.ReadDir(catalogFS, "catalog/chips")
	if err != nil {
		return nil, catalogErrorf("read chips dir: %w", err)
	}
	for _, e := range chipEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := catalogFS.ReadFile("catalog/chips/" + e.Name())
		if err != nil {
			return nil, catalogErrorf("read %s: %w", e.Name(), err)
		}
		var doc CatalogDocument
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, catalogErrorf("parse %s: %w", e.Name(), err)
		}
		for i := range doc.ChipProfiles {
			cp := &doc.ChipProfiles[i]
			if err := validateChipProfile(cp, cat.Drivers); err != nil {
				return nil, err
			}
			cat.Chips[cp.Name] = cp
		}
	}

	return cat, nil
}

// validateDriverProfile enforces RULE-HWDB-PR2-01, PR2-04, PR2-05, PR2-06,
// PR2-13, and PR2-14.
func validateDriverProfile(dp *DriverProfile) error {
	mod := dp.Module
	if mod == "" {
		return catalogErrorf("driver: required field \"module\" absent")
	}
	// RULE-HWDB-PR2-01: required fields
	if dp.Family == "" {
		return catalogErrorf("driver %q: required field \"family\" absent", mod)
	}
	if dp.Description == "" {
		return catalogErrorf("driver %q: required field \"description\" absent", mod)
	}
	if dp.Capability == "" {
		return catalogErrorf("driver %q: required field \"capability\" absent", mod)
	}
	if _, ok := knownCapabilities[dp.Capability]; !ok {
		return catalogErrorf("driver %q: unknown capability %q", mod, dp.Capability)
	}
	if dp.PWMUnit == "" {
		return catalogErrorf("driver %q: required field \"pwm_unit\" absent", mod)
	}
	if _, ok := knownPWMUnits[dp.PWMUnit]; !ok {
		return catalogErrorf("driver %q: unknown pwm_unit %q", mod, dp.PWMUnit)
	}
	if dp.PWMEnableModes == nil {
		return catalogErrorf("driver %q: required field \"pwm_enable_modes\" absent", mod)
	}
	if dp.OffBehaviour == "" {
		return catalogErrorf("driver %q: required field \"off_behaviour\" absent", mod)
	}
	if _, ok := knownOffBehaviours[dp.OffBehaviour]; !ok {
		return catalogErrorf("driver %q: unknown off_behaviour %q", mod, dp.OffBehaviour)
	}
	if dp.PWMPolarityReservation == "" {
		return catalogErrorf("driver %q: required field \"pwm_polarity_reservation\" absent", mod)
	}
	if _, ok := knownPolarityReservations[dp.PWMPolarityReservation]; !ok {
		return catalogErrorf("driver %q: unknown pwm_polarity_reservation %q", mod, dp.PWMPolarityReservation)
	}
	// RULE-HWDB-PR2-13: exit_behaviour required
	if dp.ExitBehaviour == "" {
		return catalogErrorf("driver %q: required field \"exit_behaviour\" absent", mod)
	}
	if _, ok := knownExitBehaviours[dp.ExitBehaviour]; !ok {
		return catalogErrorf("driver %q: unknown exit_behaviour %q (valid: force_max, restore_auto, preserve, bios_dependent)", mod, dp.ExitBehaviour)
	}
	// RULE-HWDB-PR2-14: runtime_conflict_detection_supported required (pointer detects absence)
	if dp.RuntimeConflictDetectionSupported == nil {
		return catalogErrorf("driver %q: required field \"runtime_conflict_detection_supported\" absent", mod)
	}
	// RULE-HWDB-PR2-04: pwm_unit_max required for step/cooling drivers
	if dp.PWMUnit == PWMUnitStep0N || dp.PWMUnit == PWMUnitCoolingLevel {
		if dp.PWMUnitMax == nil {
			return catalogErrorf("driver %q: pwm_unit_max required when pwm_unit is %q", mod, dp.PWMUnit)
		}
	}
	// RULE-HWDB-PR2-05: manual mode required for writable drivers
	if dp.Capability == CapabilityRWFull || dp.Capability == CapabilityRWQuirk || dp.Capability == CapabilityRWStep {
		hasManual := false
		for _, mode := range dp.PWMEnableModes {
			if mode == "manual" {
				hasManual = true
				break
			}
		}
		if !hasManual {
			return catalogErrorf("driver %q: capability %q requires a \"manual\" entry in pwm_enable_modes", mod, dp.Capability)
		}
	}
	// RULE-HWDB-PR2-06: ro_pending_oot requires recommended alternative
	if dp.Capability == CapabilityROPendingOOT && dp.RecommendedAlternativeDriver == nil {
		return catalogErrorf("driver %q: capability ro_pending_oot requires non-null recommended_alternative_driver", mod)
	}
	return nil
}

// validateChipProfile enforces RULE-HWDB-PR2-02.
func validateChipProfile(cp *ChipProfile, drivers map[string]*DriverProfile) error {
	if cp.Name == "" {
		return catalogErrorf("chip: required field \"name\" absent")
	}
	// RULE-HWDB-PR2-02: inherits_driver must resolve
	if cp.InheritsDriver == "" {
		return catalogErrorf("chip %q: required field \"inherits_driver\" absent", cp.Name)
	}
	if _, ok := drivers[cp.InheritsDriver]; !ok {
		return catalogErrorf("chip %q: inherits_driver %q does not resolve to a known driver module", cp.Name, cp.InheritsDriver)
	}
	return nil
}

// FirmwareCurveOffloadCapable derives whether the driver supports chip-internal
// trip-point tables for curve offload. Per §12.3 of the amendment.
func FirmwareCurveOffloadCapable(dp *DriverProfile) bool {
	if dp.FirmwareCurveOffloadOverride != nil {
		return *dp.FirmwareCurveOffloadOverride
	}
	offloadModes := map[string]struct{}{
		"thermal_cruise": {}, "smart_fan_iv": {},
		"auto_trip_points": {}, "firmware_curve": {},
	}
	for _, mode := range dp.PWMEnableModes {
		if _, ok := offloadModes[mode]; ok {
			return true
		}
	}
	return false
}

// ShouldCalibrate reports whether calibration should run for this profile.
// RULE-HWDB-PR2-07: fan_control_capable:false → no calibration.
func ShouldCalibrate(ecp *EffectiveControllerProfile) bool {
	return ecp.FanControlCapable
}

// ShouldApplyCurve returns (true, nil) if curve writes are permitted, or
// (false, sentinel error) if the channel is phantom or BIOS-overridden.
// Nil cal means no calibration data — returns (true, nil) so pre-calibration
// channels operate without enforcement. RULE-HWDB-PR2-08.
func ShouldApplyCurve(cal *ChannelCalibration) (bool, error) {
	if cal == nil {
		return true, nil
	}
	if cal.Phantom {
		return false, ErrPhantom
	}
	if cal.BIOSOverridden {
		return false, ErrBIOSOverridden
	}
	return true, nil
}

// NeedsRecalibration returns true if the calibration run's BIOS version
// differs from the current BIOS version. Nil run returns true (calibration
// needed). RULE-HWDB-PR2-09.
func NeedsRecalibration(run *CalibrationRun, currentBIOSVersion string) bool {
	if run == nil {
		return true
	}
	return run.BIOSVersion != currentBIOSVersion
}

// InvertPWM applies polarity inversion if the channel calibration indicates it.
// Returns pwm unchanged if cal is nil or PolarityInverted is false.
func InvertPWM(cal *ChannelCalibration, pwm, pwmUnitMax int) int {
	if cal == nil || !cal.PolarityInverted {
		return pwm
	}
	return pwmUnitMax - pwm
}

// LoadCatalogFromFS is the same as LoadCatalog but reads from a caller-supplied
// FS. Used in tests to inject synthetic catalog data.
func LoadCatalogFromFS(fsys fs.FS) (*Catalog, error) {
	cat := &Catalog{
		Drivers: make(map[string]*DriverProfile),
		Chips:   make(map[string]*ChipProfile),
	}

	driverEntries, err := fs.ReadDir(fsys, "drivers")
	if err == nil {
		for _, e := range driverEntries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, err := fs.ReadFile(fsys, "drivers/"+e.Name())
			if err != nil {
				return nil, catalogErrorf("read %s: %w", e.Name(), err)
			}
			var doc CatalogDocument
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, catalogErrorf("parse %s: %w", e.Name(), err)
			}
			for i := range doc.DriverProfiles {
				dp := &doc.DriverProfiles[i]
				if err := validateDriverProfile(dp); err != nil {
					return nil, err
				}
				cat.Drivers[dp.Module] = dp
			}
		}
	}

	chipEntries, err := fs.ReadDir(fsys, "chips")
	if err == nil {
		for _, e := range chipEntries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, err := fs.ReadFile(fsys, "chips/"+e.Name())
			if err != nil {
				return nil, catalogErrorf("read %s: %w", e.Name(), err)
			}
			var doc CatalogDocument
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, catalogErrorf("parse %s: %w", e.Name(), err)
			}
			for i := range doc.ChipProfiles {
				cp := &doc.ChipProfiles[i]
				if err := validateChipProfile(cp, cat.Drivers); err != nil {
					return nil, err
				}
				cat.Chips[cp.Name] = cp
			}
		}
	}

	return cat, nil
}
