package hwdb

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	ExperimentalRaw                   map[string]any      `yaml:"experimental,omitempty"`
	Experimental                      ExperimentalBlock   `yaml:"-"`
	BlacklistBeforeInstall            []string            `yaml:"blacklist_before_install,omitempty"` // v1.3
	KernelVersion                     *KernelVersionRange `yaml:"kernel_version,omitempty"`           // v1.3
	// StateQuantizedN, when set, declares that the driver's PWM surface is
	// state-quantized to N distinct stable values rather than a continuous
	// 0..pwm_unit_max range. Calibration interprets the sweep as an N-step
	// staircase and observation tags the channel with a state_quantized_N
	// fingerprint. Only valid when pwm_unit is step_0_N; pwm_unit_max+1 must
	// equal N (one fan-state index per step). Nil/absent means "not declared
	// state-quantized" (continuous PWM or behaviour unknown). v1.4.
	StateQuantizedN *int `yaml:"state_quantized_n,omitempty"`
	// PWMModeWritable, when set, declares whether the driver exposes a
	// writable `pwmN_mode` sysfs attribute that flips an individual
	// channel between PWM (4-pin) and DC (3-pin / voltage) drive. The
	// nct6775 family exposes one (set to true); it87 does not (false).
	// Nil means "not declared" — the chip-mode self-heal path probes
	// at runtime and persists the discovered capability through the
	// catalog-capture flow (#759).
	//
	// Used by the calibrate mode-mismatch detector
	// (RULE-CALIB-MODE-MISMATCH-01) at the controller boundary:
	//
	//	true  → write pwmN_mode=0 (DC) on detected mismatch + retest
	//	false → cannot self-heal; surface the BIOS path to the operator
	//	         and exclude the channel from the controllable set
	//	nil   → probe-write at runtime, treat EBUSY/ENOENT as false,
	//	         success as true, and persist the result.
	PWMModeWritable *bool `yaml:"pwm_mode_writable,omitempty"`
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

	// CalibrateWithinChipParallel is the per-chip-family safety
	// assertion for the orchestrator's CalibratePhase: when true, fans
	// on this chip may be swept in parallel (within-chip-group fanout);
	// when false (the default), fans on this chip stay serial within
	// the chip group (cross-chip parallelism is unaffected).
	//
	// Some Super-I/O parts (early NCT6775 / shared pwm_enable register
	// designs) race the chip's fan-control state machine when two
	// pwmN sweeps overlap on the same chip. Others (NCT6687-class
	// chips with per-channel pwm_enable registers) are independently
	// addressable and parallel-safe. Verified per chip family — opt
	// in here only after HIL confirmation; default-false keeps
	// pre-#1219 behaviour. (#1219)
	CalibrateWithinChipParallel bool `yaml:"calibrate_within_chip_parallel,omitempty"`
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

// Catalog holds the full merged driver + chip + board catalog loaded from the embedded FS.
type Catalog struct {
	Drivers map[string]*DriverProfile // keyed by module name
	Chips   map[string]*ChipProfile   // keyed by chip name value
	Boards  []*BoardCatalogEntry      // ordered board profiles for tier-1/2 matching
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

	// Acoustic calibration fields (R30 / v0.5.12 PR-D). All optional —
	// channels probed without `ventd calibrate --acoustic` leave these
	// nil/zero and the cost gate falls back to R33's no-mic proxy.
	//
	// DBAPerPWM is the measured slope of dBA vs PWM at this channel
	// in dB(A) per raw PWM unit. Pointer so a freshly-probed channel
	// (no acoustic calibration) is distinguishable from a channel
	// whose measured slope happens to be zero (acoustically inert
	// fan, e.g. a pump on a closed-loop AIO at idle).
	DBAPerPWM *float64 `json:"dba_per_pwm,omitempty" yaml:"dba_per_pwm,omitempty"`

	// DBABaseline is the dBA reading at MinResponsivePWM — the
	// "channel just spinning" anchor. Combined with DBAPerPWM, lets
	// the controller predict total host dBA at any candidate PWM.
	DBABaseline *float64 `json:"dba_baseline,omitempty" yaml:"dba_baseline,omitempty"`

	// DBAPWMCurve is the per-step measured (PWM, dBA) sweep that
	// generated DBAPerPWM. Persisted so we can re-fit the slope
	// after a partial re-calibration without re-running every step.
	DBAPWMCurve []DBASweepPoint `json:"dba_pwm_curve,omitempty" yaml:"dba_pwm_curve,omitempty"`

	// KCalOffset is R30's per-mic K_cal = SPL_ref - dBFS_ref offset
	// from the reference-tone calibration that preceded the per-fan
	// sweep. Adding K_cal to AWeightedDBFS yields dBA SPL.
	KCalOffset *float64 `json:"k_cal_offset,omitempty" yaml:"k_cal_offset,omitempty"`

	// KCalMicID is the mic identity (USB vendor:product + serial-hash)
	// captured at calibration time. The daemon warns if the mic
	// changed since this calibration was written — a different mic
	// has a different K_cal and the persisted slope is stale.
	KCalMicID string `json:"k_cal_mic_id,omitempty" yaml:"k_cal_mic_id,omitempty"`

	// ModeMismatchSuspected is set by the calibrate mode-mismatch
	// detector (RULE-CALIB-MODE-MISMATCH-01 / #759) when the flat-
	// RPM-across-sweep heuristic trips: writing PWM=255, 128, 64 in
	// turn produced essentially the same RPM at each step. The most
	// common cause is a 3-pin (voltage-controlled) fan plugged into
	// a header set to PWM mode in BIOS — the chip emits a fixed-
	// width PWM signal that the fan converts to a constant DC, so
	// RPM is unresponsive. Other plausible causes (seized fan,
	// stiction, broken tach) are tagged via ModeMismatchEvidence.
	ModeMismatchSuspected bool `json:"mode_mismatch_suspected,omitempty" yaml:"mode_mismatch_suspected,omitempty"`

	// ModeMismatchEvidence carries the qualitative signal the
	// detector used. Stable tokens:
	//
	//	"flat_rpm_across_sweep"         — primary trigger
	//	"flat_rpm_with_zero_low_step"   — flat + R_low==0 (also possibly dead fan)
	//	"flat_rpm_with_stuck_full_speed"— flat + R_low > 0.9 * R_max
	//	"self_healed_dc_mode"           — chip-mode self-heal succeeded;
	//	                                  pwmN_mode flipped to DC; channel
	//	                                  is back under closed-loop control
	//	"bios_mode_action_required"     — driver does NOT expose pwmN_mode
	//	                                  (it87 family); operator must flip
	//	                                  the header mode in BIOS, then reboot
	ModeMismatchEvidence string `json:"mode_mismatch_evidence,omitempty" yaml:"mode_mismatch_evidence,omitempty"`
}

// DBASweepPoint is one step of the dBA-vs-PWM measurement that
// produced ChannelCalibration.DBAPerPWM.
type DBASweepPoint struct {
	PWM int     `json:"pwm" yaml:"pwm"`
	DBA float64 `json:"dba" yaml:"dba"`
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

// IsChipCalibrateWithinChipParallel reports whether the chip family
// named by chipName has been verified parallel-safe for within-chip
// calibrate sweeps (#1219). A nil catalog or unknown chip name returns
// false — conservative default that preserves the pre-#1219 serial-
// within-chip behaviour.
//
// The orchestrator's CalibratePhase consults this to decide whether
// to fan out fans inside a single chip group; cross-chip parallelism
// is unconditional regardless of the flag.
func IsChipCalibrateWithinChipParallel(cat *Catalog, chipName string) bool {
	if cat == nil || chipName == "" {
		return false
	}
	cp := cat.Chips[chipName]
	if cp == nil {
		return false
	}
	return cp.CalibrateWithinChipParallel
}

// IsChipPWMModeWritable reports whether the driver named by chipName
// is catalogued PWMModeWritable: true — the policy gate for the
// calibrate mode-mismatch self-heal (#759). chipName is the hwmon
// `name` attribute, which equals the kernel module name for the
// Super-I/O families that expose a writable pwm*_mode (nct6775,
// nct6776, nct6779, nct6798, w83627ehf, …); the catalog's Drivers map
// is keyed by that module name.
//
// A nil catalog, an unknown driver, or a driver whose PWMModeWritable
// is nil (unknown) or false returns false. The self-heal therefore
// runs ONLY on families explicitly confirmed writable; every other
// case — including drivers like it87 that expose no mode attribute and
// drivers we simply haven't characterised — falls back to surfacing
// BIOS guidance, the conservative direction.
func IsChipPWMModeWritable(cat *Catalog, chipName string) bool {
	if cat == nil || chipName == "" {
		return false
	}
	dp := cat.Drivers[chipName]
	if dp == nil || dp.PWMModeWritable == nil {
		return false
	}
	return *dp.PWMModeWritable
}

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

	// Load board catalog (embedded catalog/boards/*.yaml).
	boards, err := LoadBoardCatalog()
	if err != nil {
		return nil, catalogErrorf("board catalog: %w", err)
	}
	cat.Boards = boards

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
	// v1.2: validate experimental block if present
	if dp.ExperimentalRaw != nil {
		eb, err := validateExperimental(dp.ExperimentalRaw, slog.Default())
		if err != nil {
			return catalogErrorf("driver %q: %w", mod, err)
		}
		dp.Experimental = eb
	}
	// v1.3: validate kernel_version range. Both Min and Max are optional but
	// must be dotted-numeric strings when set, and Min <= Max when both present.
	if dp.KernelVersion != nil {
		if dp.KernelVersion.Min != "" && !isDottedNumeric(dp.KernelVersion.Min) {
			return catalogErrorf("driver %q: kernel_version.min %q is not a valid dotted version (RULE-HWDB-PR2-17)", mod, dp.KernelVersion.Min)
		}
		if dp.KernelVersion.Max != "" && !isDottedNumeric(dp.KernelVersion.Max) {
			return catalogErrorf("driver %q: kernel_version.max %q is not a valid dotted version (RULE-HWDB-PR2-17)", mod, dp.KernelVersion.Max)
		}
		if dp.KernelVersion.Min != "" && dp.KernelVersion.Max != "" {
			if compareDottedVersions(dp.KernelVersion.Min, dp.KernelVersion.Max) > 0 {
				return catalogErrorf("driver %q: kernel_version.min %q exceeds kernel_version.max %q (RULE-HWDB-PR2-17)", mod, dp.KernelVersion.Min, dp.KernelVersion.Max)
			}
		}
	}
	// v1.4: validate state_quantized_n — only valid for step_0_N, must equal pwm_unit_max+1.
	if dp.StateQuantizedN != nil {
		if dp.PWMUnit != PWMUnitStep0N {
			return catalogErrorf("driver %q: state_quantized_n only valid when pwm_unit is step_0_N (got %q)", mod, dp.PWMUnit)
		}
		if *dp.StateQuantizedN < 2 {
			return catalogErrorf("driver %q: state_quantized_n must be >= 2 (got %d)", mod, *dp.StateQuantizedN)
		}
		if dp.PWMUnitMax != nil && *dp.StateQuantizedN != *dp.PWMUnitMax+1 {
			return catalogErrorf("driver %q: state_quantized_n (%d) must equal pwm_unit_max+1 (%d)", mod, *dp.StateQuantizedN, *dp.PWMUnitMax+1)
		}
	}
	// v1.3: validate blacklist_before_install — entries must be non-empty + unique.
	seenBL := make(map[string]struct{}, len(dp.BlacklistBeforeInstall))
	for i, m := range dp.BlacklistBeforeInstall {
		m = strings.TrimSpace(m)
		if m == "" {
			return catalogErrorf("driver %q: blacklist_before_install[%d]: empty module name (RULE-HWDB-PR2-16)", mod, i)
		}
		if _, dup := seenBL[m]; dup {
			return catalogErrorf("driver %q: blacklist_before_install: duplicate module %q (RULE-HWDB-PR2-16)", mod, m)
		}
		seenBL[m] = struct{}{}
	}
	return nil
}

// isDottedNumeric returns true if s is a non-empty dotted-numeric version
// string like "6.2", "6.13.4", "1". Used by kernel_version validation.
func isDottedNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// compareDottedVersions returns -1/0/1 like strings.Compare but parses each
// dot-segment as an integer so "6.10" > "6.9" instead of lexicographic order.
// Both inputs must already be valid dotted-numeric (caller guarantees).
func compareDottedVersions(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var ax, bx int
		if i < len(pa) {
			for _, r := range pa[i] {
				ax = ax*10 + int(r-'0')
			}
		}
		if i < len(pb) {
			for _, r := range pb[i] {
				bx = bx*10 + int(r-'0')
			}
		}
		if ax < bx {
			return -1
		}
		if ax > bx {
			return 1
		}
	}
	return 0
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

	// Optionally load boards from the "boards/" sub-directory.
	boardsFS, err := fs.Sub(fsys, "boards")
	if err == nil {
		boards, err := LoadBoardCatalogFromFS(boardsFS)
		if err != nil {
			return nil, catalogErrorf("board catalog: %w", err)
		}
		cat.Boards = boards
	}

	return cat, nil
}
