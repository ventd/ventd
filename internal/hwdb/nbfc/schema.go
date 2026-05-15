// Package nbfc vendors the upstream nbfc-linux laptop-EC configuration
// catalogue and provides DMI-based matching + control-mode classification.
//
// Upstream: github.com/nbfc-linux/nbfc-linux@0.5.2 (GPL-3.0, license-
// compatible with ventd). The configs/ subdirectory carries 311 JSON
// model files; UPSTREAM tracks the synced commit + tag.
//
// Phase A (this package) is read-only: it surfaces matched laptops in
// the doctor card so operators on EC-locked hardware see that their
// model is recognised and what control becomes possible at v0.8.0 GA.
// No EC writes, no privileged code paths.
//
// Phase B (spec-09 PRs B1 / B2 / B3) builds the pure-Go EC transport
// (internal/ec), the HAL backend (internal/hal/nbfc), and the ACPI
// bridge (internal/acpi). The Config schema here is shaped to support
// that downstream consumption — fields we don't read in Phase A are
// still parsed so the schema is forward-compatible.
package nbfc

// Config is the upstream nbfc-linux ModelConfig shape. Field names mirror
// the upstream JSON keys verbatim; doc/nbfc_service.json.5.md at the
// synced tag is the canonical reference.
//
// All fields are best-effort parsed: a config that omits an optional
// key loads with the zero value. A config that fails to parse at all
// makes the whole catalogue load fail (RULE-NBFC-CATALOG-01).
type Config struct {
	// NotebookModel is the upstream BIOS-reported model name this
	// config targets. Matched against DMI ProductName by Match.
	NotebookModel string `json:"NotebookModel"`

	// Author is the upstream contributor handle. Surfaced in the
	// doctor card for attribution.
	Author string `json:"Author,omitempty"`

	// EcPollInterval is the polling frequency in milliseconds the
	// upstream daemon uses. Advisory for the Phase B HAL backend.
	EcPollInterval int `json:"EcPollInterval,omitempty"`

	// CriticalTemperature is the threshold (°C) at which the upstream
	// daemon forces 100% fan. ventd's smart-mode controller honours
	// this as a hard ceiling per the curve compiler.
	CriticalTemperature int `json:"CriticalTemperature,omitempty"`

	// CriticalTemperatureOffset is the hysteresis below
	// CriticalTemperature used to clear the 100% override.
	CriticalTemperatureOffset int `json:"CriticalTemperatureOffset,omitempty"`

	// ReadWriteWords selects 16-bit register access when true. Phase
	// B EC transport uses Read16 / Write16 instead of Read / Write
	// for this config's fans.
	ReadWriteWords bool `json:"ReadWriteWords,omitempty"`

	// LegacyTemperatureThresholdsBehaviour preserves a pre-upstream
	// quirk in the threshold-crossing logic. Phase B controller honours.
	LegacyTemperatureThresholdsBehaviour bool `json:"LegacyTemperatureThresholdsBehaviour,omitempty"`

	// LuaLibraries lists Lua standard modules the config wants
	// available. Non-empty implies the config uses Lua somewhere; we
	// classify as ControlModeLua and refuse to control (no Lua
	// runtime in v0.8.0; 0/311 catalog configs use Lua today).
	LuaLibraries []string `json:"LuaLibraries,omitempty"`

	// FanConfigurations declares one or more fans. At least one is
	// required by the upstream spec.
	FanConfigurations []FanConfiguration `json:"FanConfigurations"`

	// RegisterWriteConfigurations declares per-register-byte
	// initialisation + reset side effects. Applied on OnInitialization
	// at Phase B Enumerate and on ResetRequired at watchdog Restore.
	RegisterWriteConfigurations []RegisterWriteConfiguration `json:"RegisterWriteConfigurations,omitempty"`
}

// FanConfiguration declares one controllable fan. Read / Write / Reset
// each have three mutually-exclusive transports (register, ACPI method,
// Lua code); the schema permits any combination across the three slots.
type FanConfiguration struct {
	FanDisplayName string `json:"FanDisplayName,omitempty"`

	// ReadRegister: 8-bit EC register read for current fan speed.
	// Mutually exclusive with ReadAcpiMethod / ReadLuaCode.
	ReadRegister uint8 `json:"ReadRegister,omitempty"`
	// ReadAcpiMethod: ACPI method path that returns the current speed.
	ReadAcpiMethod string `json:"ReadAcpiMethod,omitempty"`
	// ReadLuaCode: Lua expression returning the current speed. String
	// or array-of-strings on disk; we accept both shapes via decoding.
	ReadLuaCode rawStringOrArray `json:"ReadLuaCode,omitempty"`

	// WriteRegister: 8-bit EC register write for fan speed.
	WriteRegister uint8 `json:"WriteRegister,omitempty"`
	// WriteAcpiMethod: ACPI method path that takes the speed value
	// (passed at $ placeholder position in the upstream).
	WriteAcpiMethod string `json:"WriteAcpiMethod,omitempty"`
	// WriteLuaCode: Lua expression invoked to write fan speed.
	WriteLuaCode rawStringOrArray `json:"WriteLuaCode,omitempty"`

	// MinSpeedValue / MaxSpeedValue bound the WriteRegister byte
	// (16-bit when ReadWriteWords=true). They also bound the
	// read range when IndependentReadMinMaxValues=false.
	MinSpeedValue uint16 `json:"MinSpeedValue,omitempty"`
	MaxSpeedValue uint16 `json:"MaxSpeedValue,omitempty"`

	// IndependentReadMinMaxValues true => use the separate
	// MinSpeedValueRead / MaxSpeedValueRead bounds for read scaling.
	IndependentReadMinMaxValues bool   `json:"IndependentReadMinMaxValues,omitempty"`
	MinSpeedValueRead           uint16 `json:"MinSpeedValueRead,omitempty"`
	MaxSpeedValueRead           uint16 `json:"MaxSpeedValueRead,omitempty"`

	// ResetRequired triggers a reset write on watchdog Restore.
	ResetRequired bool `json:"ResetRequired,omitempty"`
	// FanSpeedResetValue is the register byte (or 16-bit value) to
	// write on reset when ResetRequired=true. Mutually exclusive with
	// ResetAcpiMethod / ResetLuaCode.
	FanSpeedResetValue uint16           `json:"FanSpeedResetValue,omitempty"`
	ResetAcpiMethod    string           `json:"ResetAcpiMethod,omitempty"`
	ResetLuaCode       rawStringOrArray `json:"ResetLuaCode,omitempty"`

	// Sensors lists temperature source names (or @CPU / @GPU groups)
	// to feed the per-fan curve. Phase B maps these onto ventd's
	// thermal-source registry.
	Sensors []string `json:"Sensors,omitempty"`

	// TemperatureAlgorithmType is "Average" / "Min" / "Max".
	TemperatureAlgorithmType string `json:"TemperatureAlgorithmType,omitempty"`

	// TemperatureThresholds is the upstream curve definition.
	TemperatureThresholds []TemperatureThreshold `json:"TemperatureThresholds,omitempty"`

	// FanSpeedPercentageOverrides allows sparse mappings of
	// percentage to register value, used when the controller's
	// linear scaling doesn't match the hardware (e.g. "0%" maps to a
	// specific non-zero "fan off" register byte on some HP Omens).
	FanSpeedPercentageOverrides []FanSpeedPercentageOverride `json:"FanSpeedPercentageOverrides,omitempty"`
}

// TemperatureThreshold is one row of the upstream curve.
type TemperatureThreshold struct {
	UpThreshold   int     `json:"UpThreshold"`
	DownThreshold int     `json:"DownThreshold"`
	FanSpeed      float64 `json:"FanSpeed"`
}

// FanSpeedPercentageOverride remaps a single percentage to a specific
// register value, scoped to read / write / read-write.
type FanSpeedPercentageOverride struct {
	FanSpeedPercentage float64 `json:"FanSpeedPercentage"`
	FanSpeedValue      uint16  `json:"FanSpeedValue"`
	TargetOperation    string  `json:"TargetOperation,omitempty"` // Read / Write / ReadWrite
}

// RegisterWriteConfiguration is one side-effect write that fires on
// initialisation, on every fan-speed write, or on reset.
type RegisterWriteConfiguration struct {
	WriteMode     string `json:"WriteMode"`               // Set / And / Or / Call / Lua
	WriteOccasion string `json:"WriteOccasion,omitempty"` // OnInitialization / OnWriteFanSpeed

	Register uint8 `json:"Register,omitempty"`

	// Value is the byte written when WriteMode = Set / And / Or.
	Value uint8 `json:"Value,omitempty"`
	// AcpiMethod is the method path invoked when WriteMode = Call.
	AcpiMethod string `json:"AcpiMethod,omitempty"`
	// LuaCode is the Lua snippet evaluated when WriteMode = Lua.
	LuaCode rawStringOrArray `json:"LuaCode,omitempty"`

	ResetRequired   bool             `json:"ResetRequired,omitempty"`
	ResetValue      uint8            `json:"ResetValue,omitempty"`
	ResetAcpiMethod string           `json:"ResetAcpiMethod,omitempty"`
	ResetLuaCode    rawStringOrArray `json:"ResetLuaCode,omitempty"`
	ResetWriteMode  string           `json:"ResetWriteMode,omitempty"`

	Description string `json:"Description,omitempty"`
}

// ControlMode classifies how a config drives its fans. v0.8.0 ships
// register + ACPI; Lua and Mixed-with-Lua are refused.
type ControlMode int

const (
	// ControlModeRegister: every Read / Write / Reset uses an 8-bit
	// EC register. Phase B2 handles. The bulk of the catalogue (279
	// of 311 configs).
	ControlModeRegister ControlMode = iota

	// ControlModeRegister16: every Read / Write / Reset uses 16-bit
	// EC register access (ReadWriteWords=true). Phase B2 handles. 26
	// of 311 configs.
	ControlModeRegister16

	// ControlModeACPI: at least one Read / Write / Reset uses an
	// AcpiMethod. Phase B3 (acpi_call DKMS) handles. 7 of 311 configs
	// today. May overlap with register paths in the same config
	// (Mixed); both are surfaced as ControlModeACPI for the operator.
	ControlModeACPI

	// ControlModeLua: any Read / Write / Reset / register-write
	// uses Lua. No catalogue config uses Lua today; the slot is
	// reserved for forward-compat and currently structurally refused
	// at backend Probe.
	ControlModeLua
)

// String returns the operator-facing label for the mode.
func (c ControlMode) String() string {
	switch c {
	case ControlModeRegister:
		return "register-only"
	case ControlModeRegister16:
		return "register-only (16-bit)"
	case ControlModeACPI:
		return "ACPI-method (requires acpi_call DKMS)"
	case ControlModeLua:
		return "Lua-driven (unsupported)"
	default:
		return "unknown"
	}
}
