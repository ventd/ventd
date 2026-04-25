package hwdb

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

//go:embed profiles-v1.yaml
var profilesV1YAML []byte

// ErrSchema is the sentinel for all schema validation failures. Callers use
// errors.Is(err, hwdb.ErrSchema) to distinguish parse/validation errors from
// I/O errors.
var ErrSchema = errors.New("hwdb schema")

// knownSchemaVersions is the set of schema_version values this binary accepts.
var knownSchemaVersions = map[int]struct{}{
	1: {},
}

// knownPWMModules is the v1 allowlist of kernel module names accepted in
// hardware.pwm_control. Adding a name is a v1.x amendment, not a schema break.
var knownPWMModules = map[string]struct{}{
	"nct6775": {}, "nct6779": {}, "nct6791": {}, "nct6792": {}, "nct6793": {},
	"nct6795": {}, "nct6796": {}, "nct6797": {}, "nct6798": {}, "nct6798d": {},
	"nct6799": {}, "it87": {}, "it8728": {}, "it8772": {}, "it8728f": {},
	"it8732e": {}, "it8771e": {}, "it8772e": {}, "f71808e": {}, "f71869": {},
	"f71869a": {}, "f71889ad": {}, "f71889ed": {}, "f71889fg": {}, "w83627dhg": {},
	"w83627ehf": {}, "w83627uhg": {}, "w83795g": {}, "w83795adg": {},
	"asus-ec-sensors": {}, "asus-wmi-sensors": {}, "dell-smm-hwmon": {},
	"hp-wmi-sensors": {}, "thinkpad_acpi": {}, "applesmc": {}, "surface_fan": {},
	"gigabyte-waterforce": {}, "asus-rog-ryujin": {}, "corsair-cpro": {},
	"corsair-psu": {}, "nzxt-kraken2": {}, "nzxt-kraken3": {}, "nzxt-smart2": {},
	"aquacomputer-d5next": {}, "drivetemp": {}, "k10temp": {}, "coretemp": {},
	"amdgpu": {}, "peci-cputemp": {}, "sch5627": {}, "sch5636": {}, "f71882fg": {},
	"fam15h_power": {}, "lm75": {}, "lm85": {}, "adt7475": {}, "adt7476": {},
	"max6645": {}, "max31790": {}, "emc2103": {}, "nct7802": {}, "pwm-fan": {},
}

// contributedByRE accepts "anonymous" or a GitHub handle (1-39 alphanumeric/-).
var contributedByRE = regexp.MustCompile(`^(anonymous|[a-zA-Z0-9-]{1,39})$`)

// Profile is one entry in the v1 hardware profile library. It describes a
// specific board's fan hardware, default control curves, and optional
// predictive hints for spec-05 consumers. The schema is frozen at v1 for the
// lifetime of the v0.5.x release series; breaking changes require a schema_version bump.
type Profile struct {
	ID              string           `yaml:"id"`
	SchemaVersion   int              `yaml:"schema_version"`
	Fingerprint     BoardFingerprint `yaml:"fingerprint"`
	Hardware        Hardware         `yaml:"hardware"`
	Defaults        *Defaults        `yaml:"defaults,omitempty"`
	PredictiveHints *PredictiveHints `yaml:"predictive_hints,omitempty"`
	SensorTrust     []SensorTrust    `yaml:"sensor_trust,omitempty"`
	ContributedBy   string           `yaml:"contributed_by"`
	CapturedAt      string           `yaml:"captured_at"`
	Verified        bool             `yaml:"verified"`
}

// BoardFingerprint holds the DMI and chip fields used to identify a board. At least
// one of the four primary anchor fields must be non-empty (RULE-HWDB-01).
type BoardFingerprint struct {
	DMISysVendor    string   `yaml:"dmi_sys_vendor,omitempty"`
	DMIProductName  string   `yaml:"dmi_product_name,omitempty"`
	DMIBoardVendor  string   `yaml:"dmi_board_vendor,omitempty"`
	DMIBoardName    string   `yaml:"dmi_board_name,omitempty"`
	DMIBoardVersion []string `yaml:"dmi_board_version,omitempty"`
	Family          string   `yaml:"family,omitempty"`
	SuperIOChip     string   `yaml:"superio_chip,omitempty"`
}

// hasAnchor reports whether the fingerprint has at least one matchable field.
func (f BoardFingerprint) hasAnchor() bool {
	return f.DMISysVendor != "" || f.DMIProductName != "" ||
		f.DMIBoardVendor != "" || f.DMIBoardName != "" || f.SuperIOChip != ""
}

// Hardware describes the controllable fan hardware on the board.
type Hardware struct {
	FanCount    int             `yaml:"fan_count"`
	PWMControl  string          `yaml:"pwm_control"`
	TempSensors []string        `yaml:"temp_sensors,omitempty"`
	Fans        []FanMeta       `yaml:"fans,omitempty"`
	Quirks      map[string]bool `yaml:"quirks,omitempty"`
}

// FanMeta holds per-fan metadata. StallPWMMin is required when any curve
// referencing this fan has allow_stop: true (RULE-HWDB-09).
type FanMeta struct {
	ID          int    `yaml:"id"`
	Label       string `yaml:"label,omitempty"`
	StallPWMMin *int   `yaml:"stall_pwm_min,omitempty"`
}

// Defaults holds optional suggested control curves for the board.
type Defaults struct {
	CPUSensor string  `yaml:"cpu_sensor,omitempty"`
	Curves    []Curve `yaml:"curves,omitempty"`
}

// Curve is a single fan control curve. Points must be monotonic non-decreasing
// in both temp and PWM (RULE-HWDB-04).
type Curve struct {
	Role      string   `yaml:"role"`
	FanIDs    []int    `yaml:"fan_ids"`
	AllowStop bool     `yaml:"allow_stop,omitempty"`
	Points    [][2]int `yaml:"points"`
}

// PredictiveHints carries optional hints consumed by spec-05 thermal models.
// All three numeric fields are required when the block is present (RULE-HWDB-08).
type PredictiveHints struct {
	PlatformHeavyThresholdWatts int `yaml:"platform_heavy_threshold_watts"`
	ThermalCriticalC            int `yaml:"thermal_critical_c"`
	ThermalSafeCeilingC         int `yaml:"thermal_safe_ceiling_c"`
}

// SensorTrust records a contributor-observed trust level for a named sensor.
type SensorTrust struct {
	Sensor string `yaml:"sensor"`
	Trust  string `yaml:"trust"`
	Reason string `yaml:"reason,omitempty"`
}

// Load parses a profiles YAML document from r with strict field validation.
// All RULE-HWDB-* invariants are enforced. Returns an error wrapping ErrSchema
// on any validation failure; callers use errors.Is(err, hwdb.ErrSchema).
func Load(r io.Reader) ([]Profile, error) {
	var profiles []Profile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&profiles); err != nil {
		return nil, fmt.Errorf("hwdb: parse: %w: %w", ErrSchema, err)
	}
	if profiles == nil {
		profiles = []Profile{}
	}
	if err := validate(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// LoadEmbedded parses the embedded profiles-v1.yaml. Returns the canonical
// profile library (currently empty in PR 1; PR 3 adds seed entries).
func LoadEmbedded() ([]Profile, error) {
	return Load(bytes.NewReader(profilesV1YAML))
}

// validate runs all RULE-HWDB-* checks in order. Returns the first error.
func validate(profiles []Profile) error {
	seen := make(map[string]struct{}, len(profiles))
	for i, p := range profiles {
		label := p.ID
		if label == "" {
			label = fmt.Sprintf("#%d (id missing)", i+1)
		}

		// RULE-HWDB-01: required top-level fields
		if err := validateRequired(label, p); err != nil {
			return err
		}

		// RULE-HWDB-02: unique IDs
		if _, dup := seen[p.ID]; dup {
			return schemaErrorf("profile %q: unique-id: duplicate id %q", p.ID, p.ID)
		}
		seen[p.ID] = struct{}{}

		// RULE-HWDB-03: known schema_version
		if _, ok := knownSchemaVersions[p.SchemaVersion]; !ok {
			return schemaErrorf("profile %q: schema-version: unknown version %d (known: 1)", p.ID, p.SchemaVersion)
		}

		// RULE-HWDB-04: monotonic curves
		if p.Defaults != nil {
			for ci, c := range p.Defaults.Curves {
				if err := validateCurve(p.ID, ci, c); err != nil {
					return err
				}
			}
		}

		// RULE-HWDB-05: pwm_control allowlist
		if _, ok := knownPWMModules[p.Hardware.PWMControl]; !ok {
			allowed := pwmModuleList()
			return schemaErrorf("profile %q: pwm_control: unknown kernel module %q (allowed: %s)",
				p.ID, p.Hardware.PWMControl, allowed)
		}

		// RULE-HWDB-06: contributed_by format (KnownFields handles unknown fields)
		if !contributedByRE.MatchString(p.ContributedBy) {
			return schemaErrorf("profile %q: contributed-by: value %q must be \"anonymous\" or a GitHub handle (^[a-zA-Z0-9-]{1,39}$)",
				p.ID, p.ContributedBy)
		}

		// RULE-HWDB-08: predictive_hints when present
		if p.PredictiveHints != nil {
			if err := validatePredictiveHints(p.ID, *p.PredictiveHints); err != nil {
				return err
			}
		}

		// RULE-HWDB-09: stall_pwm_min required when allow_stop=true
		if p.Defaults != nil {
			if err := validateStallPWM(p.ID, p.Hardware.Fans, p.Defaults.Curves); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRequired(label string, p Profile) error {
	if p.ID == "" {
		return schemaErrorf("profile %s: required-fields: field \"id\" absent", label)
	}
	if p.SchemaVersion == 0 {
		return schemaErrorf("profile %q: required-fields: field \"schema_version\" absent", p.ID)
	}
	if !p.Fingerprint.hasAnchor() {
		return schemaErrorf("profile %q: required-fields: \"fingerprint\" has no matchable anchor (need at least one of dmi_board_vendor, dmi_board_name, dmi_product_name, superio_chip)", p.ID)
	}
	if p.Hardware.PWMControl == "" {
		return schemaErrorf("profile %q: required-fields: field \"hardware.pwm_control\" absent", p.ID)
	}
	if p.ContributedBy == "" {
		return schemaErrorf("profile %q: required-fields: field \"contributed_by\" absent", p.ID)
	}
	if p.CapturedAt == "" {
		return schemaErrorf("profile %q: required-fields: field \"captured_at\" absent", p.ID)
	}
	return nil
}

//nolint:unparam // segment index reserved for error messages, see RULE-HWDB-04
func validateCurve(id string, idx int, c Curve) error {
	for i := 1; i < len(c.Points); i++ {
		prev, cur := c.Points[i-1], c.Points[i]
		if cur[0] < prev[0] {
			return schemaErrorf("profile %q: monotonic-curves: curve %q segment %d: temp decreases (%d -> %d)",
				id, c.Role, i, prev[0], cur[0])
		}
		if cur[1] < prev[1] {
			return schemaErrorf("profile %q: monotonic-curves: curve %q segment %d: pwm decreases (%d -> %d)",
				id, c.Role, i, prev[1], cur[1])
		}
	}
	return nil
}

func validatePredictiveHints(id string, h PredictiveHints) error {
	if h.PlatformHeavyThresholdWatts <= 0 {
		return schemaErrorf("profile %q: predictive-hints: platform_heavy_threshold_watts must be > 0, got %d",
			id, h.PlatformHeavyThresholdWatts)
	}
	if h.ThermalCriticalC <= h.ThermalSafeCeilingC+5 {
		return schemaErrorf("profile %q: predictive-hints: thermal_critical_c (%d) must be > thermal_safe_ceiling_c+5 (%d)",
			id, h.ThermalCriticalC, h.ThermalSafeCeilingC+5)
	}
	return nil
}

func validateStallPWM(id string, fans []FanMeta, curves []Curve) error {
	fanByID := make(map[int]*FanMeta, len(fans))
	for i := range fans {
		fanByID[fans[i].ID] = &fans[i]
	}
	for _, c := range curves {
		if !c.AllowStop {
			continue
		}
		for _, fid := range c.FanIDs {
			fan, ok := fanByID[fid]
			if !ok || fan.StallPWMMin == nil {
				return schemaErrorf("profile %q: stall-pwm-min: curve %q has allow_stop:true but fan %d has no stall_pwm_min",
					id, c.Role, fid)
			}
		}
	}
	return nil
}

func schemaErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrSchema}, args...)...)
}

// pwmModuleList returns a compact sorted list of known module names for error messages.
func pwmModuleList() string {
	// Return a representative excerpt to keep error messages readable.
	return "nct6775, nct6779, nct6791, ..., pwm-fan (see docs/hwdb-schema.md)"
}
