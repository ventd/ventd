// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package legion implements the hal.FanBackend interface over the
// legion_laptop kernel module's control surface — the OOT DKMS driver
// from github.com/johnfanv2/LenovoLegionLinux that exposes Lenovo
// Legion gaming-laptop fan + perf-mode control to userspace.
//
// Legion's write surface is structurally different from every other
// Mode B HAL backend in ventd: there is no per-tick PWM byte. The
// driver exposes three control axes:
//
//  1. /sys/firmware/acpi/platform_profile — standard 3-state ACPI
//     enum (quiet / balanced / performance). Universal across kernels
//     that expose the ACPI platform_profile API.
//  2. /sys/module/legion_laptop/drivers/platform:legion/PNP0C09:00/powermode
//     — legion_laptop-specific 4-state (0/1/2/3 ≈ quiet / balanced /
//     performance / custom).
//  3. /sys/kernel/debug/legion/fancurve — 10-point batch curve upload
//     via debugfs. The EC then drives per-tick using its own loop.
//
// This backend (spec-17 PR-1) ships the **state-switcher** path —
// coalescing the controller's per-tick PWM byte into a platform_profile
// state via three bucket thresholds:
//
//	PWM 0..84   → "quiet"
//	PWM 85..170 → "balanced"
//	PWM 171..255 → "performance"
//
// The powermode write happens alongside platform_profile when the
// legion_laptop powermode node exists (it's optional — older legion_laptop
// builds didn't expose it). powermode mirrors platform_profile bucketing
// with the addition of "custom" reserved for future spec-05 P4-HWCURVE
// work (the curve-upload story).
//
// The fancurve debugfs path is exposed via spec-17 PR-1b through the
// new hal.CurveSink interface extension — implementations of CurveSink
// upload a precomputed curve once at apply-time and the EC handles
// per-tick. The state-switcher path remains the fallback when no curve
// has been uploaded.
//
// Restore writes "balanced" — the safe firmware-managed default. Writing
// "performance" on exit would leave the fan loud after daemon shutdown;
// "quiet" would risk thermal headroom on a host doing work the daemon
// can no longer manage. "balanced" is the BIOS-managed equivalent of
// thinkpad's "level auto" — the EC's own curve takes over.
//
// Channel.Opaque carries the resolved platform_profile path + the
// optional powermode path + the choices set so test fixtures can mint
// channels pointing at temp files without redirecting the whole backend.
// Production resolves the paths via stat at Enumerate time.
//
// Writes are unconditional once Enumerate returns a channel — no
// per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair / thinkpad
// posture (see feedback-dont-default-writes-off). Safety is enforced by:
//   - the closed enum (platform_profile_choices is the catalogue;
//     unknown values are refused by the kernel before reaching the EC);
//   - the watchdog's Restore-on-exit (RULE-WD-RESTORE-EXIT);
//   - RULE-IDLE-02 + RULE-IDLE-03 refusing the daemon on battery / in
//     a container before any write fires.
package legion

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by this
// backend. Kept as a package-level constant so callers (cmd/ventd/
// calresolver.go, watchdog, doctor) reference it without importing the
// hal package just for the string.
const BackendName = "legion"

// DefaultPlatformProfilePath is the canonical sysfs path. The ACPI
// platform_profile API is the same across vendors that implement it;
// the path is universal.
const DefaultPlatformProfilePath = "/sys/firmware/acpi/platform_profile"

// DefaultPlatformProfileChoicesPath enumerates the accepted values.
// The kernel refuses any write whose value isn't in this list — a
// closed set the matcher reads at Enumerate time.
const DefaultPlatformProfileChoicesPath = "/sys/firmware/acpi/platform_profile_choices"

// DefaultPowermodePath is the legion_laptop-specific 4-state node.
// Optional: older legion_laptop builds didn't expose it. Enumerate
// stats this path and only includes it in the channel state if
// present.
const DefaultPowermodePath = "/sys/module/legion_laptop/drivers/platform:legion/PNP0C09:00/powermode"

// Bucket thresholds on the 0..255 PWM input. PWM ≤ ThresholdQuiet maps
// to the "quiet" state; PWM > ThresholdBalanced maps to "performance";
// in-between maps to "balanced". Exposed for the bound subtests + for
// future operator tuning (currently fixed, not config-surfaced).
const (
	ThresholdQuiet    uint8 = 84
	ThresholdBalanced uint8 = 170
)

// Profile names — exactly what the kernel accepts on the
// platform_profile sysfs node. Case-sensitive, no trailing newline
// (the kernel tolerates a newline but we omit it for byte-exact write
// auditing in tests).
const (
	ProfileQuiet       = "quiet"
	ProfileBalanced    = "balanced"
	ProfilePerformance = "performance"
)

// Powermode integers — what the legion_laptop powermode sysfs node
// accepts. 0/1/2 mirror the platform_profile buckets; 3 is reserved
// for "custom" (used by the future CurveSink integration so the EC
// knows ventd is driving via fancurve rather than platform_profile).
const (
	PowermodeQuiet       = "0"
	PowermodeBalanced    = "1"
	PowermodePerformance = "2"
	PowermodeCustom      = "3"
)

// State is the per-channel payload carried in hal.Channel.Opaque.
// Exported so the contract-test harness can construct channels
// without going through Enumerate.
type State struct {
	// PlatformProfilePath is the sysfs file the backend reads / writes.
	// In production this is always DefaultPlatformProfilePath; tests
	// inject a temp file.
	PlatformProfilePath string

	// PowermodePath, when non-empty, is the legion_laptop-specific
	// 4-state node. Write dispatches to both nodes when set; Read
	// uses PlatformProfilePath as the authoritative source.
	PowermodePath string

	// Choices is the set parsed from platform_profile_choices at
	// Enumerate time. Used to refuse Writes mapping to a state the
	// kernel would reject — better to fall back to "balanced" than
	// surface an EINVAL the operator can't act on.
	Choices map[string]bool
}

// Backend is the legion implementation of hal.FanBackend. One instance
// is shared across the daemon — channel state lives in Channel.Opaque.
type Backend struct {
	logger *slog.Logger

	// Path fields default to the Default* constants in NewBackend.
	// Tests override them to point at fixture files; production code
	// never reassigns. Kept as struct fields rather than package-level
	// vars so multiple Backend instances (test parallelism) don't
	// clobber each other.
	profilePath   string
	choicesPath   string
	powermodePath string

	acquired sync.Map

	writeFile func(path string, data []byte, perm os.FileMode) error
	readFile  func(path string) ([]byte, error)
	statFile  func(path string) (os.FileInfo, error)
}

// NewBackend constructs a Backend that logs through the given slog
// logger. A nil logger falls back to slog.Default.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:        logger,
		profilePath:   DefaultPlatformProfilePath,
		choicesPath:   DefaultPlatformProfileChoicesPath,
		powermodePath: DefaultPowermodePath,
		writeFile:     os.WriteFile,
		readFile:      os.ReadFile,
		statFile:      os.Stat,
	}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close releases backend-level resources. The legion backend holds no
// process-level state, so Close is a no-op. Idempotent per RULE-HAL-007.
func (b *Backend) Close() error { return nil }

// Enumerate probes for /sys/firmware/acpi/platform_profile and returns
// a single channel when both the profile file and the choices file are
// readable. The presence of platform_profile is a sufficient signal
// that the host supports the ACPI state-switcher API; the matcher
// (catalog) is responsible for narrowing to "this is actually a Legion"
// when the operator wants legion-specific behaviour.
//
// Hosts without platform_profile return an empty slice — not an error
// — so the registry's fan-out Enumerate admits the absence gracefully.
//
// Idempotent (RULE-HAL-001): the presence of these sysfs files is
// determined by the kernel module's load state, which doesn't change
// within a single daemon lifetime.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profilePath := b.profilePath
	choicesPath := b.choicesPath
	powermodePath := b.powermodePath

	if _, err := b.statFile(profilePath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("legion: stat %s: %w", profilePath, err)
	}
	if _, err := b.statFile(choicesPath); err != nil {
		// platform_profile without choices file is a degenerate kernel
		// state; treat as "not enumerable" rather than fail so the
		// daemon proceeds without legion control on this host.
		b.logger.Debug("legion: platform_profile present but choices file missing; skipping enumerate",
			"profile", profilePath, "choices", choicesPath, "err", err)
		return nil, nil
	}

	choices, err := b.parseChoices(choicesPath)
	if err != nil {
		b.logger.Debug("legion: parse choices failed; skipping enumerate",
			"path", choicesPath, "err", err)
		return nil, nil
	}
	if len(choices) < 2 {
		// A single-choice enum is degenerate — surfacing a channel
		// would promise control the hardware can't deliver. Mirrors
		// RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP's quiet branch.
		b.logger.Debug("legion: platform_profile_choices has < 2 values; skipping enumerate",
			"choices", choices)
		return nil, nil
	}

	st := State{
		PlatformProfilePath: profilePath,
		Choices:             choices,
	}
	if _, err := b.statFile(powermodePath); err == nil {
		st.PowermodePath = powermodePath
	}
	return []hal.Channel{{
		ID:     profilePath,
		Role:   hal.RoleCPU,
		Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: st,
	}}, nil
}

// Read samples the current platform_profile state and maps it to a PWM
// byte via the inverse of the bucket function (each profile reports the
// centre of its band so a write→read→compare round-trip is stable).
//
// Never mutates observable state (RULE-HAL-002): only readFile is
// invoked on the sysfs path.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	data, err := b.readFile(st.PlatformProfilePath)
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	profile := strings.TrimSpace(string(data))
	pwm, ok := profileToPWM(profile)
	if !ok {
		b.logger.Debug("legion: read returned unknown profile",
			"path", st.PlatformProfilePath, "profile", profile)
		return hal.Reading{OK: false}, nil
	}
	return hal.Reading{
		PWM: pwm,
		OK:  true,
	}, nil
}

// Write commands the channel to a 0..255 PWM byte by bucketing into
// a platform_profile state and dispatching the write. When the
// powermode sysfs node is present (Legion-specific), the matching
// powermode integer is also written.
//
// A bucket mapping to a state not in Choices falls back to the next
// closest available state (performance → balanced; quiet → balanced)
// rather than surfacing an EINVAL. This preserves "writes always
// succeed once Enumerate returned a channel" — a contract every other
// HAL backend honours and downstream code expects.
//
// EPERM is wrapped as ErrPlatformProfileRefused (typed sentinel) so
// downstream classification can branch via errors.Is without string
// matching. The recovery path is operator-visible only — there's no
// equivalent of thinkpad's modprobe-options-write auto-fix for this
// surface.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	profile := pwmToProfile(pwm)
	profile = clampToChoices(profile, st.Choices)
	b.acquired.LoadOrStore(st.PlatformProfilePath, struct{}{})

	if werr := b.writeFile(st.PlatformProfilePath, []byte(profile), 0o644); werr != nil {
		switch {
		case errors.Is(werr, fs.ErrPermission):
			return fmt.Errorf("%w: %w", ErrPlatformProfileRefused, werr)
		default:
			return fmt.Errorf("legion: write %q to %s: %w", profile, st.PlatformProfilePath, werr)
		}
	}

	if st.PowermodePath != "" {
		pm := profileToPowermode(profile)
		if perr := b.writeFile(st.PowermodePath, []byte(pm), 0o644); perr != nil {
			// powermode failure is non-fatal — platform_profile already
			// committed the state change. Log at DEBUG; the next
			// write attempt re-tries the powermode side.
			b.logger.Debug("legion: powermode write failed; platform_profile committed",
				"path", st.PowermodePath, "powermode", pm, "err", perr)
		}
	}
	return nil
}

// Restore returns the channel to BIOS-managed mode by writing
// "balanced" to platform_profile. The driver profile's exit_behaviour
// is "restore_auto"; for Legion this is "balanced" rather than a
// literal "auto" state because the platform_profile API has no auto
// keyword — the firmware curve is what runs when platform_profile is
// "balanced" and no other override (powermode custom, fancurve) is
// active.
//
// When the powermode node is present, "1" is written alongside
// (mirrors balanced). powermode write failure is non-fatal — the
// platform_profile restore is the load-bearing contract.
//
// Idempotent + safe on un-written channels (RULE-HAL-004): writing
// "balanced" to a channel that was never written is a clean operation.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	target := clampToChoices(ProfileBalanced, st.Choices)
	werr := b.writeFile(st.PlatformProfilePath, []byte(target), 0o644)
	if werr == nil {
		b.acquired.Delete(st.PlatformProfilePath)
		if st.PowermodePath != "" {
			_ = b.writeFile(st.PowermodePath, []byte(PowermodeBalanced), 0o644)
		}
		b.logger.Info("legion: restored to platform_profile balanced",
			"path", st.PlatformProfilePath)
		return nil
	}
	b.logger.Error("legion: restore failed",
		"path", st.PlatformProfilePath, "target", target, "err", werr)
	return werr
}

// pwmToProfile maps a 0..255 PWM byte to one of the three canonical
// state names using the bucket thresholds. Pure function; no I/O.
func pwmToProfile(pwm uint8) string {
	switch {
	case pwm <= ThresholdQuiet:
		return ProfileQuiet
	case pwm <= ThresholdBalanced:
		return ProfileBalanced
	default:
		return ProfilePerformance
	}
}

// profileToPWM is the inverse mapping used by Read. Each profile maps
// to the centre of its band so write→read→compare is stable:
//
//	quiet       → 42  (centre of 0..84)
//	balanced    → 127 (centre of 85..170)
//	performance → 213 (centre of 171..255)
//
// Returns ok=false when the input isn't a known profile.
func profileToPWM(profile string) (uint8, bool) {
	switch profile {
	case ProfileQuiet:
		return 42, true
	case ProfileBalanced:
		return 127, true
	case ProfilePerformance:
		return 213, true
	default:
		return 0, false
	}
}

// profileToPowermode maps a platform_profile state name to the
// equivalent legion_laptop powermode integer.
func profileToPowermode(profile string) string {
	switch profile {
	case ProfileQuiet:
		return PowermodeQuiet
	case ProfilePerformance:
		return PowermodePerformance
	default:
		return PowermodeBalanced
	}
}

// clampToChoices returns target when present in choices; otherwise
// falls back to "balanced" (which is guaranteed present in choices by
// Enumerate's len-check). A choices map that somehow lacks balanced
// after the Enumerate check is a kernel pathology; we still return
// target rather than empty-string so the write attempt surfaces the
// kernel's actual refusal rather than an empty payload.
func clampToChoices(target string, choices map[string]bool) string {
	if choices == nil {
		return target
	}
	if choices[target] {
		return target
	}
	if choices[ProfileBalanced] {
		return ProfileBalanced
	}
	return target
}

// parseChoices reads platform_profile_choices and returns a set of
// the space-separated state names. Tolerates trailing whitespace.
func (b *Backend) parseChoices(path string) (map[string]bool, error) {
	data, err := b.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("legion: read %s: %w", path, err)
	}
	out := make(map[string]bool)
	for _, tok := range strings.Fields(string(data)) {
		out[tok] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("legion: %s: empty choices set", path)
	}
	return out, nil
}

// stateFrom coerces a Channel's Opaque payload into the legion State
// shape. Accepts both value and pointer forms — matches the helper in
// internal/hal/thinkpad.
func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		if v.PlatformProfilePath == "" {
			return State{}, errors.New("legion: channel state has empty PlatformProfilePath")
		}
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("legion: nil opaque state")
		}
		if v.PlatformProfilePath == "" {
			return State{}, errors.New("legion: channel state has empty PlatformProfilePath")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("legion: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}
