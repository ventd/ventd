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

// ECLockedLaptopDetector surfaces that ventd's platform_profile control
// loop is live, so the operator can see and override it. It fires on any
// host whose kernel exposes the ACPI platform_profile enum with ≥ 2
// choices, because ventd's zero-config platform_profile controller starts
// unconditionally whenever that interface exists (see
// startPlatformProfileController) — meaning "interface present" is a
// faithful proxy for "ventd is actively driving the selector".
//
// Two flavours, keyed on the probe's controllable-channel count:
//
//   - count == 0 — pure EC-locked laptop (consumer HP / Dell / Lenovo /
//     ASUS): the EC owns all PWM actuation, so per-fan smart-mode does
//     not apply and the platform_profile selector is the only fan lever.
//     ventd's probe classifies these as `monitor_only` per RULE-PROBE-04;
//     the card names the limitation and the active selector value.
//
//   - count > 0 — hybrid host (e.g. Dell Latitude with a single
//     dell_smm pwm + platform_profile): smart-mode drives the writable
//     PWM channel(s) AND the platform_profile controller drives the
//     selector. Previously suppressed (#1415), which left the operator
//     blind to an active control loop they couldn't see or override.
//
// Both cards name the active platform_profile + available choices and
// give the kernel `echo` override (respected with a 10-minute back-off).
//
// Quiet when the platform_profile interface is absent (servers / embedded
// hosts — other detectors handle the monitor-only case) or exposes a
// single degenerate choice (no operator-meaningful adjustment).
//
// Severity: OK. The hardware works as designed; the card is
// informational, not a warning (mirrors the experimental_flags
// detector's "surface for visibility, not for dismissal" pattern).
// dkms_status / hwmon_swap / etc. cover the "things that should work
// but don't" cases.
type ECLockedLaptopDetector struct {
	// ControllableChannelCount is len(probe.ProbeResult.ControllableChannels)
	// at daemon-start. Zero selects the monitor-only card; > 0 selects
	// the hybrid card.
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
// Fact describing the live platform_profile control loop — the
// monitor-only flavour when no PWM channels are controllable, the hybrid
// flavour when smart-mode also owns writable channels.
func (d *ECLockedLaptopDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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

	// platform_profile present with a real choice set ⇒ ventd's
	// zero-config platform_profile controller is driving the selector
	// (it starts unconditionally whenever the interface exists). Surface
	// that the loop is live regardless of whether smart-mode also owns
	// writable PWM channels, so the operator can see and override it.
	if d.ControllableChannelCount > 0 {
		// Hybrid host: writable PWM channel(s) AND platform_profile,
		// both actively driven. #1415 — gating this on count == 0 left
		// hybrid operators blind to an active platform_profile loop.
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("platform_profile controller active (current: %s)", current),
			Detail: fmt.Sprintf(
				"This host has both writable PWM channel(s) — driven per-fan by smart-mode — and an ACPI platform_profile selector at /sys/firmware/acpi/platform_profile, currently set to %q with choices [%s]. In addition to the per-fan PWM loop, ventd's platform_profile controller actively drives the selector from live thermal/load inputs. To override, write the kernel interface directly — `echo <choice> | sudo tee /sys/firmware/acpi/platform_profile` — which ventd detects and respects with a 10-minute back-off before resuming automatic control.",
				current,
				strings.Join(choices, ", "),
			),
			EntityHash: doctor.HashEntity("platform_profile_active", strings.Join(choices, ",")),
			Observed:   now,
		}}, nil
	}

	// Monitor-only host: the EC owns all PWM actuation (no controllable
	// channels at probe time), so per-fan smart-mode does not apply and
	// the platform_profile selector is the only fan-related lever.
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityOK,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("Fan control owned by the EC; ventd drives platform_profile (current: %s)", current),
		Detail: fmt.Sprintf(
			"This hardware exposes no PWM duty-cycle write surface — typical of HP/Dell/Lenovo/ASUS consumer laptops where the embedded controller owns fan actuation. No controllable PWM channels were found at probe time, so per-fan smart-mode does not apply. The only operator-facing fan-related lever is the ACPI platform_profile enum at /sys/firmware/acpi/platform_profile, currently set to %q with choices [%s]. ventd's platform_profile controller (spec #872) drives this automatically from live thermal/load inputs. To override, write the kernel interface directly — `echo <choice> | sudo tee /sys/firmware/acpi/platform_profile` — which ventd detects and respects with a 10-minute back-off before resuming automatic control.",
			current,
			strings.Join(choices, ", "),
		),
		EntityHash: doctor.HashEntity("ec_locked_laptop", strings.Join(choices, ",")),
		Observed:   now,
	}}, nil
}
