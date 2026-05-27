// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package lenovoideapad implements the hal.FanBackend interface over the
// /sys/firmware/acpi/platform_profile sysfs surface for Lenovo IdeaPad-class
// laptops driven by the ideapad_laptop kernel module.
//
// IdeaPads' fan-control surface is structurally identical to Legion's
// platform_profile state-switcher path, but the firmware enum uses the
// canonical ACPI names {low-power, balanced, performance} (not Legion's
// {quiet, balanced, performance}) and the platform exposes none of the
// legion_laptop-specific secondaries — no powermode sysfs node, no
// fancurve debugfs path, no per-tick PWM byte. Live-tested on an IdeaPad
// Flex 5 14ITL05 (82HS, BIOS FXCN28WW): every userspace fan-control
// path beyond platform_profile is rejected by the EC firmware. See
// /home/phoenix/ventd-7280-fan-rev/lenovo-ideapad-flex-5-driver-notes.md
// for the full survey.
//
// This backend ships the state-switcher path — coalescing the controller's
// per-tick PWM byte into one of three platform_profile states via the same
// bucket thresholds Legion uses (84 / 170):
//
//	PWM 0..84   → "low-power"
//	PWM 85..170 → "balanced"
//	PWM 171..255 → "performance"
//
// Restore writes "balanced" — the safe firmware-managed default. Writing
// "performance" on exit would leave the BIOS curve aggressive after daemon
// shutdown; "low-power" would risk thermal headroom on a host doing work
// the daemon can no longer manage.
//
// Channel.Opaque carries the resolved platform_profile path + the choices
// set so test fixtures can mint channels pointing at temp files without
// redirecting the whole backend. Production resolves the paths via stat at
// Enumerate time.
//
// Discovery is exclusive: a host must have ideapad_laptop loaded AND must
// NOT have legion_laptop loaded for this backend to enumerate a channel.
// Both modules export sysfs entries at /sys/module/<name>; the
// presence/absence pair is the signal. This prevents both the legion and
// lenovoideapad backends from enumerating the same platform_profile path
// on hybrid hosts.
//
// Writes are unconditional once Enumerate returns a channel — no
// per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair / thinkpad /
// legion posture (see feedback-dont-default-writes-off). Safety is
// enforced by:
//   - the closed platform_profile_choices enum (the kernel refuses any
//     write whose value isn't in the catalogue);
//   - the watchdog's Restore-on-exit (RULE-WD-RESTORE-EXIT) writing
//     "balanced" on every documented shutdown path;
//   - RULE-IDLE-02 (battery refusal) + RULE-IDLE-03 (container refusal)
//     closing the daemon before any write fires on a host where writes
//     would be unsafe.
package lenovoideapad

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
const BackendName = "lenovoideapad"

// DefaultPlatformProfilePath is the canonical sysfs path. The ACPI
// platform_profile API is the same across vendors that implement it;
// the path is universal.
const DefaultPlatformProfilePath = "/sys/firmware/acpi/platform_profile"

// DefaultPlatformProfileChoicesPath enumerates the accepted values. The
// kernel refuses any write whose value isn't in this list — a closed set
// the matcher reads at Enumerate time.
const DefaultPlatformProfileChoicesPath = "/sys/firmware/acpi/platform_profile_choices"

// DefaultIdeapadModulePath is the sysfs directory that exists iff
// ideapad_laptop is loaded. Presence is the positive signal that this
// host is an IdeaPad (or a Lenovo non-ThinkPad/non-Legion laptop that
// ideapad_laptop bound to — both warrant this backend).
const DefaultIdeapadModulePath = "/sys/module/ideapad_laptop"

// DefaultLegionModulePath is the sysfs directory that exists iff the OOT
// legion_laptop DKMS module is loaded. Presence is the exclusion signal:
// when this path exists, the legion backend owns the channel and this
// backend defers (RULE-HAL-LENOVO-IDEAPAD-03).
const DefaultLegionModulePath = "/sys/module/legion_laptop"

// Bucket thresholds on the 0..255 PWM input. Same boundaries as
// internal/hal/legion (ThresholdQuiet=84, ThresholdBalanced=170); kept
// as package-local constants so future operator tuning has a slot
// without coupling the two backends.
const (
	ThresholdLowPower uint8 = 84
	ThresholdBalanced uint8 = 170
)

// Profile names — the canonical ACPI platform_profile vocabulary used by
// ideapad_laptop. Case-sensitive, no trailing newline (the kernel
// tolerates a newline but we omit it for byte-exact write auditing in
// tests).
const (
	ProfileLowPower    = "low-power"
	ProfileBalanced    = "balanced"
	ProfilePerformance = "performance"
)

// State is the per-channel payload carried in hal.Channel.Opaque.
// Exported so the contract-test harness can construct channels without
// going through Enumerate.
type State struct {
	// PlatformProfilePath is the sysfs file the backend reads / writes.
	// In production this is always DefaultPlatformProfilePath; tests
	// inject a temp file.
	PlatformProfilePath string

	// Choices is the set parsed from platform_profile_choices at
	// Enumerate time. Used to refuse Writes mapping to a state the
	// kernel would reject — better to fall back to "balanced" than
	// surface an EINVAL the operator can't act on.
	Choices map[string]bool
}

// Backend is the lenovoideapad implementation of hal.FanBackend. One
// instance is shared across the daemon — channel state lives in
// Channel.Opaque.
type Backend struct {
	logger *slog.Logger

	// Path fields default to the Default* constants in NewBackend.
	// Tests override them to point at fixture files; production code
	// never reassigns. Kept as struct fields rather than package-level
	// vars so multiple Backend instances (test parallelism) don't
	// clobber each other.
	profilePath string
	choicesPath string
	ideapadPath string
	legionPath  string

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
		logger:      logger,
		profilePath: DefaultPlatformProfilePath,
		choicesPath: DefaultPlatformProfileChoicesPath,
		ideapadPath: DefaultIdeapadModulePath,
		legionPath:  DefaultLegionModulePath,
		writeFile:   os.WriteFile,
		readFile:    os.ReadFile,
		statFile:    os.Stat,
	}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close releases backend-level resources. The lenovoideapad backend
// holds no process-level state, so Close is a no-op. Idempotent per
// RULE-HAL-007.
func (b *Backend) Close() error { return nil }

// Enumerate returns a single channel when ALL of the following hold:
//
//   - /sys/firmware/acpi/platform_profile is readable
//   - /sys/firmware/acpi/platform_profile_choices is readable with ≥2 values
//   - /sys/module/ideapad_laptop exists (positive signal)
//   - /sys/module/legion_laptop does NOT exist (Legion-exclusion signal)
//
// Hosts missing any signal return an empty slice (not an error) so the
// registry's fan-out Enumerate admits the absence gracefully.
//
// The Legion-exclusion check is defence-in-depth: legion's own backend
// already enumerates platform_profile, so without the check both
// backends would expose the same path and produce duplicate channels.
// In practice no shipping Lenovo loads both modules at once, but the
// guard makes the discovery contract self-documenting and the test
// suite can exercise the hybrid case explicitly.
//
// Idempotent (RULE-HAL-001): the presence of these sysfs nodes is
// determined by kernel-module load state, which doesn't change within a
// single daemon lifetime.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := b.statFile(b.profilePath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("lenovoideapad: stat %s: %w", b.profilePath, err)
	}
	if _, err := b.statFile(b.choicesPath); err != nil {
		// platform_profile without choices is a degenerate kernel state;
		// treat as "not enumerable" rather than fail so the daemon
		// proceeds without ideapad control on this host.
		b.logger.Debug("lenovoideapad: platform_profile present but choices file missing; skipping enumerate",
			"profile", b.profilePath, "choices", b.choicesPath, "err", err)
		return nil, nil
	}

	if _, err := b.statFile(b.ideapadPath); err != nil {
		// ideapad_laptop module not loaded — this isn't an IdeaPad host
		// (or the module was unloaded post-boot). Defer to whichever
		// other backend owns platform_profile on this machine.
		return nil, nil
	}
	if _, err := b.statFile(b.legionPath); err == nil {
		// legion_laptop is loaded — Legion's backend already owns
		// platform_profile. Stand down to avoid duplicate-channel
		// enumeration.
		b.logger.Debug("lenovoideapad: legion_laptop module also present; deferring to legion backend",
			"profile", b.profilePath)
		return nil, nil
	}

	choices, err := b.parseChoices(b.choicesPath)
	if err != nil {
		b.logger.Debug("lenovoideapad: parse choices failed; skipping enumerate",
			"path", b.choicesPath, "err", err)
		return nil, nil
	}
	if len(choices) < 2 {
		// A single-choice enum is degenerate — surfacing a channel
		// would promise control the hardware can't deliver.
		b.logger.Debug("lenovoideapad: platform_profile_choices has < 2 values; skipping enumerate",
			"choices", choices)
		return nil, nil
	}

	st := State{
		PlatformProfilePath: b.profilePath,
		Choices:             choices,
	}
	return []hal.Channel{{
		ID:     b.profilePath,
		Role:   hal.RoleCPU,
		Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: st,
	}}, nil
}

// Read samples the current platform_profile state and maps it to a PWM
// byte via profileToPWM (each profile reports the centre of its band so
// a write→read→compare round-trip is stable).
//
// Never mutates observable state (RULE-HAL-002): only readFile is
// invoked on the sysfs path. RPM telemetry is not available on IdeaPad
// hosts (ec_sys is removed in modern kernels and there is no ACPI FRSP
// equivalent in the DSDT — see lenovo-ideapad-flex-5-driver-notes.md);
// Reading.RPM stays 0 with OK=true so the controller sees a valid PWM
// without a stale or fabricated RPM.
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
		b.logger.Debug("lenovoideapad: read returned unknown profile",
			"path", st.PlatformProfilePath, "profile", profile)
		return hal.Reading{OK: false}, nil
	}
	return hal.Reading{
		PWM: pwm,
		OK:  true,
	}, nil
}

// Write commands the channel to a 0..255 PWM byte by bucketing into a
// platform_profile state and dispatching the write. A bucket mapping to
// a state not in Choices falls back to "balanced" (which Enumerate
// guarantees is present given the ≥2-choices admission gate) rather
// than surfacing an EINVAL — preserving "writes always succeed once
// Enumerate returned a channel" (RULE-HAL-008-style contract).
//
// EPERM is wrapped as ErrPlatformProfileRefused (typed sentinel) so
// downstream classification can branch via errors.Is without string
// matching. Recovery is operator-visible only — there's no equivalent
// of thinkpad's modprobe-options-write auto-fix for this surface.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	profile := pwmToProfile(pwm)
	profile = clampToChoices(profile, st.Choices)
	b.acquired.LoadOrStore(st.PlatformProfilePath, struct{}{})

	if werr := b.writeFile(st.PlatformProfilePath, []byte(profile), 0o644); werr != nil {
		if errors.Is(werr, fs.ErrPermission) {
			return fmt.Errorf("%w: %w", ErrPlatformProfileRefused, werr)
		}
		return fmt.Errorf("lenovoideapad: write %q to %s: %w", profile, st.PlatformProfilePath, werr)
	}
	return nil
}

// Restore returns the channel to BIOS-managed mode by writing
// "balanced" to platform_profile. The driver profile's exit_behaviour
// is "restore_auto"; for IdeaPad this is "balanced" rather than a
// literal "auto" state because the platform_profile API has no auto
// keyword — the firmware curve is what runs when platform_profile is
// "balanced".
//
// Idempotent + safe on un-written channels (RULE-HAL-004): writing
// "balanced" to a channel that was never written is a clean operation.
// The acquired-flag clear lets a subsequent SIGHUP / config reload
// start fresh.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	target := clampToChoices(ProfileBalanced, st.Choices)
	werr := b.writeFile(st.PlatformProfilePath, []byte(target), 0o644)
	if werr == nil {
		b.acquired.Delete(st.PlatformProfilePath)
		b.logger.Info("lenovoideapad: restored to platform_profile balanced",
			"path", st.PlatformProfilePath)
		return nil
	}
	b.logger.Error("lenovoideapad: restore failed",
		"path", st.PlatformProfilePath, "target", target, "err", werr)
	return werr
}

// pwmToProfile maps a 0..255 PWM byte to one of the three canonical
// state names using the bucket thresholds. Pure function; no I/O.
func pwmToProfile(pwm uint8) string {
	switch {
	case pwm <= ThresholdLowPower:
		return ProfileLowPower
	case pwm <= ThresholdBalanced:
		return ProfileBalanced
	default:
		return ProfilePerformance
	}
}

// profileToPWM is the inverse mapping used by Read. Each profile maps
// to the centre of its band so write→read→compare is stable:
//
//	low-power   → 42  (centre of 0..84)
//	balanced    → 127 (centre of 85..170)
//	performance → 213 (centre of 171..255)
//
// Returns ok=false when the input isn't a known profile.
func profileToPWM(profile string) (uint8, bool) {
	switch profile {
	case ProfileLowPower:
		return 42, true
	case ProfileBalanced:
		return 127, true
	case ProfilePerformance:
		return 213, true
	default:
		return 0, false
	}
}

// clampToChoices returns target when present in choices; otherwise
// falls back to "balanced" (which is guaranteed present after the
// ≥2-choices admission gate at Enumerate). A choices map that somehow
// lacks balanced is a kernel pathology; we still return target so the
// write attempt surfaces the kernel's actual refusal rather than an
// empty payload.
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

// parseChoices reads platform_profile_choices and returns a set of the
// space-separated state names. Tolerates trailing whitespace.
func (b *Backend) parseChoices(path string) (map[string]bool, error) {
	data, err := b.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("lenovoideapad: read %s: %w", path, err)
	}
	out := make(map[string]bool)
	for _, tok := range strings.Fields(string(data)) {
		out[tok] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("lenovoideapad: %s: empty choices set", path)
	}
	return out, nil
}

// stateFrom coerces a Channel's Opaque payload into the lenovoideapad
// State shape. Accepts both value and pointer forms — matches the
// helper in internal/hal/legion and internal/hal/thinkpad.
func stateFrom(ch hal.Channel) (State, error) {
	return hal.StateFrom(ch, "lenovoideapad", func(s State) error {
		if s.PlatformProfilePath == "" {
			return errors.New("lenovoideapad: channel state has empty PlatformProfilePath")
		}
		return nil
	})
}
