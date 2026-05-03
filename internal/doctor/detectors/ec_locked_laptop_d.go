package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// PlatformProfileReadFn returns the live platform_profile choices and
// current value. Returns ok=false when the ACPI platform_profile sysfs
// surface is absent (no platform_profile driver, non-laptop hardware,
// or kernel < 4.18). Tests inject a stub returning canned values.
type PlatformProfileReadFn func() (current string, choices []string, ok bool)

// liveReadPlatformProfile reads /sys/firmware/acpi/platform_profile and
// platform_profile_choices. Empty / read-error → ok=false (graceful
// degrade per RULE-DOCTOR-04).
func liveReadPlatformProfile() (string, []string, bool) {
	currentBytes, err := os.ReadFile("/sys/firmware/acpi/platform_profile")
	if err != nil {
		return "", nil, false
	}
	choicesBytes, err := os.ReadFile("/sys/firmware/acpi/platform_profile_choices")
	if err != nil {
		return "", nil, false
	}
	current := strings.TrimSpace(string(currentBytes))
	choices := strings.Fields(strings.TrimSpace(string(choicesBytes)))
	if current == "" || len(choices) == 0 {
		return "", nil, false
	}
	return current, choices, true
}

// ECLockedLaptopDetector surfaces the "fan control owned entirely by
// the EC" case — common on consumer HP / Dell / Lenovo / ASUS laptops
// where userspace gets the platform_profile ACPI enum but no `pwm*`
// duty-cycle write file and no `fan*_input` tach. ventd's probe
// correctly classifies these as `monitor_only` per RULE-PROBE-04, but
// without this card the operator gets no diagnostic — just an empty
// dashboard with no path forward.
//
// The card explains the limitation, names the active platform_profile
// + available choices, and points at issue #872 (v0.6 platform_profile
// selector mode) so operators know follow-up work is scoped.
//
// Fires when ALL of:
//   - /sys/firmware/acpi/platform_profile exists with non-empty value
//   - /sys/firmware/acpi/platform_profile_choices lists ≥ 2 enum values
//   - The probe's controllable-channel count is zero
//
// Quiet when any of those conditions don't hold — desktops have
// controllable channels (smart_mode applies); servers without
// platform_profile aren't this category; hosts with platform_profile
// AND a writable pwm get smart_mode, not this card.
//
// Severity: OK. The hardware works as designed; the card is
// informational, not a warning (mirrors the experimental_flags
// detector's "surface for visibility, not for dismissal" pattern).
// dkms_status / hwmon_swap / etc. cover the "things that should work
// but don't" cases.
type ECLockedLaptopDetector struct {
	// ControllableChannelCount is len(probe.ProbeResult.ControllableChannels)
	// at daemon-start. Zero = monitor-only, the trigger condition.
	ControllableChannelCount int

	// ReadPlatformProfile returns the active value + available choices.
	// Defaults to liveReadPlatformProfile when nil.
	ReadPlatformProfile PlatformProfileReadFn
}

// NewECLockedLaptopDetector constructs a detector. readFn nil → live
// /sys/firmware/acpi reads.
func NewECLockedLaptopDetector(controllableChannels int, readFn PlatformProfileReadFn) *ECLockedLaptopDetector {
	if readFn == nil {
		readFn = liveReadPlatformProfile
	}
	return &ECLockedLaptopDetector{
		ControllableChannelCount: controllableChannels,
		ReadPlatformProfile:      readFn,
	}
}

// Name returns the stable detector ID.
func (d *ECLockedLaptopDetector) Name() string { return "ec_locked_laptop" }

// Probe reads platform_profile and emits an OK-severity (informational)
// Fact when the host is EC-locked.
func (d *ECLockedLaptopDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.ControllableChannelCount > 0 {
		// Smart-mode applies; this card is irrelevant.
		return nil, nil
	}
	current, choices, ok := d.ReadPlatformProfile()
	if !ok {
		// No platform_profile interface; the host is monitor-only for
		// some other reason (server with locked BIOS, embedded board,
		// container). Other detectors handle those.
		return nil, nil
	}
	if len(choices) < 2 {
		// Single-value enum — degenerate, no operator-meaningful
		// adjustment is possible. Don't surface a card that promises
		// control the hardware can't deliver.
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityOK,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("Fan control owned by the EC; manual selector is platform_profile (current: %s)", current),
		Detail: fmt.Sprintf(
			"This hardware exposes no PWM duty-cycle write surface — typical of HP/Dell/Lenovo/ASUS consumer laptops where the embedded controller owns fan actuation. ventd is running in monitor-only mode (no controllable channels were found at probe time). The only operator-facing fan-related control on this host is the ACPI platform_profile enum at /sys/firmware/acpi/platform_profile, currently set to %q with choices [%s]. Direct selection: `echo <choice> | sudo tee /sys/firmware/acpi/platform_profile`. A future v0.6 ventd mode (issue #872) will drive platform_profile from CPU temperature bands so this is automated; for v0.5.x the operator picks manually.",
			current,
			strings.Join(choices, ", "),
		),
		EntityHash: doctor.HashEntity("ec_locked_laptop", strings.Join(choices, ",")),
		Observed:   now,
	}}, nil
}
