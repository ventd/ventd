package hwdb

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog/boards/*.yaml
var boardCatalogFS embed.FS

// boardSupportedVersions is the set of schema_version strings accepted for
// the board catalog. Empty string covers legacy entries from before versioning.
var boardSupportedVersions = map[string]struct{}{
	"":    {}, // absent → treated as 1.0
	"1.0": {},
	"1.1": {},
	"1.2": {},
	"1.3": {}, // adds pwm_groups on board profile + kernel_version + blacklist_before_install on driver profile (R29 §4 finding + R36 mini-PC kernel gates)
}

// BoardCatalogCurrentVersion is the schema_version this binary writes for
// new board catalog entries.
const BoardCatalogCurrentVersion = "1.3"

// PWMGroup describes a single PWM channel that drives multiple physical fans.
// R29 §4 found Phoenix's MSI Z690-A drives Cpu_Fan + Pump_Fan + Sys_Fan_1 +
// Sys_Fan_2 with identical PWM values across all 2479 status samples — one
// PWM channel, four fans. R36 §B notes the same pattern for the IT8613E
// mini-PC pool. The cost gate must compute energetic sums across grouped
// fans (10·log10(N) higher per step than per-fan); without grouping data
// the predicted-loudness number is wrong by 6 dB on a 4-fan group.
//
// Channel is the PWM sysfs leaf name (e.g. "pwm1", "pwm5"); Fans is a list
// of fan IDs that share that channel. Fan IDs are opaque to the catalog —
// the calibration probe matches them up to the live channels via the
// PrimaryController's sysfs hint.
type PWMGroup struct {
	Channel string   `yaml:"channel"`
	Fans    []string `yaml:"fans"`
}

// KernelVersionRange gates a driver profile to a specific kernel-version
// window. Both Min and Max are inclusive; either can be empty to leave that
// end open. R36's per-row analysis identified eight catalog rows that need
// this — e.g. it87 quirks (kernel ≥6.2), MS-01 mainline support
// (kernel ≥5.14), Strix Halo (kernel ≥6.13).
type KernelVersionRange struct {
	Min string `yaml:"min,omitempty"` // e.g. "6.2" — minimum supported kernel (inclusive)
	Max string `yaml:"max,omitempty"` // e.g. "7.1" — maximum supported kernel (inclusive); rare, mostly for old-OOT-driver gates
}

// BoardCatalogDocument is the top-level shape of a catalog/boards/*.yaml file.
type BoardCatalogDocument struct {
	SchemaVersion string              `yaml:"schema_version,omitempty"`
	BoardProfiles []BoardCatalogEntry `yaml:"board_profiles"`
}

// BoardCatalogEntry is one board profile in the board catalog. A board entry
// must have exactly one of dmi_fingerprint or dt_fingerprint (RULE-HWDB-PR4-05).
type BoardCatalogEntry struct {
	ID                     string                `yaml:"id"`
	DMIFingerprint         *BoardDMIFingerprint  `yaml:"dmi_fingerprint,omitempty"`
	DTFingerprint          *DTFingerprint        `yaml:"dt_fingerprint,omitempty"` // v1.1
	PrimaryController      BoardController       `yaml:"primary_controller"`
	AdditionalControllers  []BoardController     `yaml:"additional_controllers"`
	Overrides              BoardCatalogOverrides `yaml:"overrides"`
	RequiredModprobeArgs   []string              `yaml:"required_modprobe_args"`
	ConflictsWithUserspace []string              `yaml:"conflicts_with_userspace"`
	Notes                  string                `yaml:"notes,omitempty"`
	Citations              []string              `yaml:"citations"`
	ContributedBy          string                `yaml:"contributed_by"`
	CapturedAt             string                `yaml:"captured_at"`
	Verified               bool                  `yaml:"verified"`
	Defaults               *BoardDefaults        `yaml:"defaults,omitempty"`
	ExperimentalRaw        map[string]any        `yaml:"experimental,omitempty"`
	Experimental           ExperimentalBlock     `yaml:"-"`
	PWMGroups              []PWMGroup            `yaml:"pwm_groups,omitempty"` // v1.3
}

// BoardDMIFingerprint is the DMI match pattern for a board catalog entry.
// Fields support glob with '*' wildcards; empty or "*" matches anything.
// v1.1 adds BiosVersion for Lenovo Legion family dispatch (RULE-HWDB-PR4-01).
type BoardDMIFingerprint struct {
	SysVendor    string `yaml:"sys_vendor"`
	ProductName  string `yaml:"product_name"`
	BoardVendor  string `yaml:"board_vendor"`
	BoardName    string `yaml:"board_name"`
	BoardVersion string `yaml:"board_version"`
	BiosVersion  string `yaml:"bios_version,omitempty"` // v1.1
}

// DTFingerprint is the device-tree match pattern for ARM/SBC boards without
// DMI (RULE-HWDB-PR4-03, RULE-HWDB-PR4-04). At least one field must be set.
// Mutually exclusive with dmi_fingerprint (RULE-HWDB-PR4-05).
type DTFingerprint struct {
	Compatible string `yaml:"compatible,omitempty"`
	Model      string `yaml:"model,omitempty"`
}

// BoardController identifies the hwmon chip for a board's fan controller.
type BoardController struct {
	Chip      string `yaml:"chip"`
	SysfsHint string `yaml:"sysfs_hint,omitempty"`
}

// BoardCatalogOverrides holds board-specific overrides applied on top of the
// chip and driver profile layers.
type BoardCatalogOverrides struct {
	CPUTINFloats            bool   `yaml:"cputin_floats,omitempty"`
	Unsupported             bool   `yaml:"unsupported,omitempty"` // v1.1: sensors-only mode
	ArmDeviceTree           bool   `yaml:"arm_device_tree,omitempty"`
	CoolingDeviceMustDetach bool   `yaml:"cooling_device_must_detach,omitempty"`   // v1.1: ARM boards
	BIOSOverriddenPWMWrites bool   `yaml:"bios_overridden_pwm_writes,omitempty"`   // Gigabyte X670E etc.
	ROSensorOnly            bool   `yaml:"ro_sensor_only,omitempty"`               // Dell laptops: reads OK, no fan writes
	BMCOverridesHwmon       bool   `yaml:"bmc_overrides_hwmon,omitempty"`          // Supermicro/Dell: hwmon writes ignored by BMC
	PreferIPMIBackend       bool   `yaml:"prefer_ipmi_backend,omitempty"`          // use spec-01 IPMI path
	FanModeOnly             bool   `yaml:"fan_mode_only,omitempty"`                // IdeaPad/Yoga: mode enum, not PWM
	ModeCount               int    `yaml:"mode_count,omitempty"`                   // number of fan modes when fan_mode_only
	DynamicEnumeration      bool   `yaml:"dynamic_enumeration,omitempty"`          // HP WMI: channels discovered at runtime
	PWMScale                string `yaml:"pwm_scale,omitempty"`                    // ThinkPad: "0-7-mapped-to-0-255"
	RequiresWatchdog        bool   `yaml:"requires_watchdog,omitempty"`            // Supermicro: BMC watchdog must be armed
	WatchdogSecondsDefault  int    `yaml:"watchdog_seconds_default,omitempty"`     // default BMC watchdog interval
	SecondaryFanUncontrol   bool   `yaml:"secondary_fan_uncontrollable,omitempty"` // ThinkPad P-series: 2nd fan read-only
	// Dell PowerEdge server overrides
	FanBlockedByIDRAC9v334    bool     `yaml:"fan_control_blocked_by_idrac9_3_34,omitempty"`    // iDRAC9 3.34+ blocks raw fan commands
	VendorRawCommandSet       string   `yaml:"vendor_raw_command_set,omitempty"`                // e.g. "dell_idrac_legacy", "supermicro_x11_x12"
	VendorRawCommandExtra     []string `yaml:"vendor_raw_command_extra,omitempty"`              // additional vendor-specific command IDs
	PCIeCardThermalAggressive bool     `yaml:"pcie_card_thermal_response_aggressive,omitempty"` // PCIe cards trigger aggressive fan response
	// HPE ProLiant server overrides
	FanBlockedByILO5Plus bool `yaml:"fan_control_blocked_by_ilo_5_plus,omitempty"` // iLO 5+ blocks user fan control
	IPMITelemetryOnly    bool `yaml:"ipmi_telemetry_only,omitempty"`               // IPMI read-only for telemetry
	// Supermicro server overrides
	BMCPanicModeRisk bool `yaml:"bmc_panic_mode_risk,omitempty"` // BMC may enter forced-100%-fan panic mode
	// Lenovo Legion overrides
	ECChipID                 string `yaml:"ec_chip_id,omitempty"`                   // embedded controller chip ID
	ECChipIDMismatchExpected bool   `yaml:"ec_chip_id_mismatch_expected,omitempty"` // known benign EC chip ID mismatch
	FancurveFormat           string `yaml:"fancurve_format,omitempty"`              // e.g. "10-point-debugfs"
	ForceLoadFallbackConfig  string `yaml:"force_load_fallback_config,omitempty"`   // fallback BIOS config name (e.g. "GKCN")
	RequiresDKMS             bool   `yaml:"requires_dkms,omitempty"`                // requires OOT DKMS module
	RequiresForceModparam    bool   `yaml:"requires_force_modparam,omitempty"`      // requires force=1 modparam
	// Raspberry Pi / ARM SBC overrides
	NoDMI                    bool   `yaml:"no_dmi,omitempty"`                     // no DMI/SMBIOS on this platform
	CarrierBoardDependent    bool   `yaml:"carrier_board_dependent,omitempty"`    // fan path depends on carrier board
	I2CAddress               string `yaml:"i2c_address,omitempty"`                // I2C address for fan controller
	I2CBus                   string `yaml:"i2c_bus,omitempty"`                    // I2C bus number
	RequiresI2CVCEnabled     bool   `yaml:"requires_i2c_vc_enabled,omitempty"`    // requires VideoCore I2C enabled
	RequiresOverlayDTOverlay string `yaml:"requires_overlay_dtoverlay,omitempty"` // required DT overlay name
	TachInputUnavailable     bool   `yaml:"tach_input_unavailable,omitempty"`     // no tach/RPM sensor input
}

// BoardDefaults holds optional default control curves for the board.
// Curve shape is validated by the controller layer; the catalog stores them
// as raw interface{} to avoid coupling to the curve schema here.
type BoardDefaults struct {
	Curves []any `yaml:"curves,omitempty"`
}

// LoadBoardCatalog loads all board catalog entries from the embedded FS.
// Returns a slice of validated board entries; any validation failure aborts
// the entire load (same policy as LoadCatalog for driver profiles).
func LoadBoardCatalog() ([]*BoardCatalogEntry, error) {
	return loadBoardCatalogFromFS(boardCatalogFS, "catalog/boards")
}

// LoadBoardCatalogFromFS loads board catalog entries from a caller-supplied FS.
// Used in tests to inject synthetic board data without touching the embedded FS.
func LoadBoardCatalogFromFS(fsys fs.FS) ([]*BoardCatalogEntry, error) {
	return loadBoardCatalogFromFS(fsys, ".")
}

func loadBoardCatalogFromFS(fsys fs.FS, dir string) ([]*BoardCatalogEntry, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		// Treat a missing boards directory as an empty catalog — test MapFS
		// fixtures that only seed drivers/chips don't need a boards/ dir.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("hwdb board catalog: read dir %q: %w", dir, err)
	}

	var all []*BoardCatalogEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := dir + "/" + e.Name()
		if dir == "." {
			path = e.Name()
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("hwdb board catalog: read %s: %w", e.Name(), err)
		}
		var doc BoardCatalogDocument
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("hwdb board catalog: parse %s: %w", e.Name(), err)
		}
		if _, ok := boardSupportedVersions[doc.SchemaVersion]; !ok {
			return nil, fmt.Errorf("hwdb board catalog: %s: unknown schema_version %q", e.Name(), doc.SchemaVersion)
		}
		for i := range doc.BoardProfiles {
			bp := &doc.BoardProfiles[i]
			if err := validateBoardCatalogEntry(bp); err != nil {
				return nil, fmt.Errorf("hwdb board catalog: %s: %w", e.Name(), err)
			}
			all = append(all, bp)
		}
	}
	return all, nil
}

// validateBoardCatalogEntry enforces RULE-FINGERPRINT-08 / RULE-SCHEMA-08:
// exactly one of dmi_fingerprint or dt_fingerprint must be set on each board
// entry, and a dt_fingerprint must have at least one non-empty field.
func validateBoardCatalogEntry(bp *BoardCatalogEntry) error {
	if bp.DMIFingerprint != nil && bp.DTFingerprint != nil {
		return fmt.Errorf("profile %q: both dmi_fingerprint and dt_fingerprint are set; exactly one is required", bp.ID)
	}
	if bp.DMIFingerprint == nil && bp.DTFingerprint == nil {
		return fmt.Errorf("profile %q: neither dmi_fingerprint nor dt_fingerprint is set; exactly one is required", bp.ID)
	}
	if bp.DTFingerprint != nil && bp.DTFingerprint.Compatible == "" && bp.DTFingerprint.Model == "" {
		return fmt.Errorf("profile %q: dt_fingerprint has no fields set; at least compatible or model is required", bp.ID)
	}
	// v1.2: validate experimental block if present
	if bp.ExperimentalRaw != nil {
		eb, err := validateExperimental(bp.ExperimentalRaw, slog.Default())
		if err != nil {
			return fmt.Errorf("profile %q: %w", bp.ID, err)
		}
		bp.Experimental = eb
	}
	// v1.3: validate pwm_groups
	for i, g := range bp.PWMGroups {
		if strings.TrimSpace(g.Channel) == "" {
			return fmt.Errorf("profile %q: pwm_groups[%d]: channel must be non-empty (RULE-HWDB-PR2-15)", bp.ID, i)
		}
		if len(g.Fans) == 0 {
			return fmt.Errorf("profile %q: pwm_groups[%d] (channel=%q): fans must list at least one fan id (RULE-HWDB-PR2-15)", bp.ID, i, g.Channel)
		}
		seen := make(map[string]struct{}, len(g.Fans))
		for j, f := range g.Fans {
			f = strings.TrimSpace(f)
			if f == "" {
				return fmt.Errorf("profile %q: pwm_groups[%d].fans[%d]: empty fan id (RULE-HWDB-PR2-15)", bp.ID, i, j)
			}
			if _, dup := seen[f]; dup {
				return fmt.Errorf("profile %q: pwm_groups[%d].fans: duplicate fan id %q (RULE-HWDB-PR2-15)", bp.ID, i, f)
			}
			seen[f] = struct{}{}
		}
	}
	return nil
}
