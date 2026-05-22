package polarity

import "strings"

// BackendCaps captures two facts about a hwmon-exposed fan-control
// driver that the polarity probe needs to make the right call.
//
// MonotonicByConstruction is true when the driver's PWM write surface
// physically cannot present an inverted channel — its kernel API takes a
// monotonic level (state index, duty %, fan_target enum, etc.) and a
// "higher value ⇒ higher RPM" relationship is guaranteed by the
// driver's own translation layer. Probing such a channel is wasted
// time AND a misclassification risk: if the EC declines to spin the
// fan during the probe window (e.g. cold chassis, ambient below the
// firmware-enforced "fan-on" threshold), the bipolar pulse returns
// ΔRPM=0 and ventd would emit a false phantom verdict on a fan that
// is in fact perfectly controllable.
//
// EcCanThermalVeto is true when the embedded controller behind the
// driver is known to ignore manual PWM writes whenever board temps
// sit below an internal "fan-on" threshold — most commonly seen on
// laptop SMM/EC paths that gate the fan output through the firmware's
// own thermal table. When this is set, a no_response polarity result
// is reclassified to PolarityProbational rather than terminal phantom:
// the fan is admitted with conservative defaults and the apply path
// surfaces a provisional badge so the wizard UI explains why the
// channel wasn't fully calibrated.
//
// Add an entry here ONLY when the driver's behaviour has been
// confirmed from a primary source (kernel driver, vendor datasheet,
// HIL probe). The default zero value is correct for every chip ventd
// has not been told about — those run the existing bipolar probe.
type BackendCaps struct {
	MonotonicByConstruction bool
	EcCanThermalVeto        bool
}

// backendCapsTable is keyed by the hwmon `name` file content (see
// readChipNameFromDir in internal/setup/orchestrator/probe.go). The
// hwmon name comes straight from the in-kernel driver's hwmon device
// registration, so the strings here match what the chip exposes at
// /sys/class/hwmon/hwmonN/name.
var backendCapsTable = map[string]BackendCaps{
	// Dell SMM. drivers/hwmon/dell-smm-hwmon.c uses i8k_set_fan() which
	// maps pwm bytes to a state index {OFF, LOW, HIGH} via the SMM
	// call I8K_SET_FAN — strictly monotonic, no possibility of
	// inversion. The EC behind the SMM handler is known to refuse
	// manual fan-on requests below its internal thermal threshold
	// (memory: reference_dell_smm_state_quantized.md,
	// reference_dell_7280_ec_smm_private.md). Both caps apply.
	"dell_smm": {MonotonicByConstruction: true, EcCanThermalVeto: true},

	// ThinkPad ACPI hwmon. drivers/platform/x86/thinkpad_acpi.c exposes
	// pwm1 as a scaled fan_target byte (the driver does the 0-255 → 0-7
	// remap internally); writing fan_target=7 always means "fastest"
	// per the ACPI EC method spec. The EC honours level=7 unconditionally
	// once pwm_enable=1 is set, so EcCanThermalVeto stays false.
	"thinkpad": {MonotonicByConstruction: true},
}

// CapsForDriver returns the static capabilities for a driver name. The
// zero value (no capabilities set) is returned for any driver the table
// does not list — that's the safe default: such a channel runs the
// existing bipolar probe and may be classified normal / inverted /
// phantom as before.
//
// Driver matching is case-sensitive and exact; the hwmon `name` file
// content is the canonical key. Some drivers historically include a
// trailing variant suffix (e.g. "nct6687d" vs "nct6687") — those are
// kept distinct on purpose because their actual fan-write semantics
// can differ.
func CapsForDriver(driver string) BackendCaps {
	if driver == "" {
		return BackendCaps{}
	}
	if caps, ok := backendCapsTable[driver]; ok {
		return caps
	}
	// thinkpad_acpi exposes the hwmon device as "thinkpad" today, but
	// older kernel revisions used "thinkpad_acpi" verbatim. Fold both
	// onto the same caps without bloating the static table.
	if strings.HasPrefix(driver, "thinkpad") {
		return backendCapsTable["thinkpad"]
	}
	return BackendCaps{}
}
