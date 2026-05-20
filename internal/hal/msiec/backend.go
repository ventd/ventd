// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package msiec implements the hal.FanBackend interface over
// /sys/devices/platform/msi-ec/ — the sysfs surface exposed by the
// out-of-tree BeardOverflow/msi-ec driver
// (https://github.com/BeardOverflow/msi-ec, GPL-2.0). The in-tree
// msi_wmi_platform driver exposes RPM-only readings on MSI laptops
// but no PWM-write surface; msi-ec is the canonical path for actually
// driving the fans.
//
// Unlike the hwmon backend's continuous 0-255 PWM duty surface, msi-ec
// exposes the fan as a small set of named EC operating modes — typically
// some subset of {"auto", "silent", "basic", "advanced"} depending on
// the board's firmware allow-list group (CONF_G1..CONF_G2_8 in msi-ec.c).
// This backend reads the per-board mode list at probe time and quantises
// the controller's 0-255 PWM Writes into mode commands at runtime —
// same shape as internal/hal/thinkpad/, which quantises 0-255 PWM to
// the firmware's 0..7 level grid via round-half-up arithmetic.
//
// Why a dedicated backend rather than letting calibration fall through
// to "no controllable fans":
//
//  1. The msi_wmi_platform tach-only readings + msi-ec's mode surface
//     between them cover every MSI laptop with a kernel ≥ 6.x and the
//     out-of-tree driver loaded. Without this backend, ventd's wizard
//     fires "monitor-only mode applied (no controllable fans found)"
//     immediately after a successful driver install — the silent
//     dead-end #1116 / #1154 documented from the user's perspective.
//
//  2. The mode surface is structurally different from PWM duty: there
//     is no useful intermediate value between "silent" and "advanced".
//     Calibration's existing PWM-step sweep would either land on the
//     same mode for every step (no observable response curve, so the
//     setup wizard's chip-mismatch retry path would refuse the channel)
//     or rapidly toggle modes (unnecessarily disruptive). The backend's
//     pwmToMode quantiser places mode boundaries at the band midpoints
//     so the sweep crosses each mode exactly once.
//
//  3. The msi-ec EC mode change has no in-driver acquire / release
//     contract — writes are unconditional. The Restore exit primitive
//     is "auto" (BIOS-managed curve), matching the
//     {hwmon: pwm_enable=2 / thinkpad: level auto / nbfc: stop} pattern.
//     Symmetric with the existing backends, but with the additional
//     guarantee that the EC retains the last operator-set mode across
//     ventd restarts unless Restore fires — so the watchdog's
//     end-of-lifetime restore is load-bearing on MSI laptops.
//
//  4. cooler_boost is a separate orthogonal flag (max-fan override that
//     ignores the curve while it's on). Calibration must NEVER assert
//     cooler_boost on its own — pinning the fan at maximum without
//     operator consent is exactly the kind of unattended-burst
//     RULE-IDLE-02 forbids. This backend exposes only fan_mode; a
//     future operator-explicit "cooler boost" toggle is deferred until
//     there's a wizard surface for it.
//
// Writes are unconditional once Enumerate returns a channel — there is
// no per-backend opt-in flag, matching the v0.6.1 thinkpad / NBFC /
// Corsair posture. Safety is enforced by:
//   - the closed mode set (silent/auto/basic/advanced, validated
//     against the per-board available_fan_modes file at probe time);
//   - Restore-on-exit (RULE-WD-RESTORE-EXIT) writing "auto" so the
//     fan returns to BIOS control on every shutdown path;
//   - RULE-IDLE-02 / RULE-IDLE-03 refusing the daemon entirely on
//     battery or in a container before any write fires.
//
// See also #1154 (the DKMS placeholder fix that unblocked the driver
// install on the user's MSI Thin GF63 12UDX / MS-16R8) and #1116
// (the original "no fan controllers found" report) for the user-facing
// failure mode this backend closes.
package msiec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by this
// backend.
const BackendName = "msiec"

// DefaultSysfsRoot is the canonical sysfs base directory the msi-ec
// out-of-tree driver registers under. The kernel exposes exactly one
// such directory regardless of how many physical fans the board has;
// fan_mode is a board-wide setting.
const DefaultSysfsRoot = "/sys/devices/platform/msi-ec"

// Canonical mode names. The msi-ec driver upstream uses these exact
// strings in fan_mode + available_fan_modes (see drivers/msi-ec.c
// MODE_DESC table). Adding a new mode here is not enough; the kernel
// driver must also expose it.
const (
	ModeAuto     = "auto"
	ModeSilent   = "silent"
	ModeBasic    = "basic"
	ModeAdvanced = "advanced"
)

// Canonical shift_mode names. The msi-ec driver exposes these in
// available_shift_modes / shift_mode (see drivers/msi-ec.c
// MSIEC_SHIFTMODE_DESC and CONF_G2_6 mapping). shift_mode is MSI
// Center's "User Scenario" surface — it shapes CPU PL1/PL2 limits and
// the BIOS fan curve that runs underneath ventd's fan_mode writes.
// On boards with incomplete mappings the kernel may emit
// "unknown (NN)" verbatim; backends pass that through so the operator
// can see what the firmware reports (#1166).
const (
	ShiftModeEco     = "eco"
	ShiftModeComfort = "comfort"
	ShiftModeTurbo   = "turbo"
)

// State is the per-channel payload carried in hal.Channel.Opaque.
// Exported so the contract test (and any future channel-minting site)
// can construct channels without going through Enumerate.
type State struct {
	// SysfsRoot is the absolute path of the msi-ec sysfs directory
	// the backend reads / writes under. Production: DefaultSysfsRoot.
	// Tests inject a t.TempDir() to drive the parser hermetically.
	SysfsRoot string

	// WritableModes is the ordered (low-airflow → high-airflow) set
	// of mode names the controller is allowed to drive on this board.
	// Populated from available_fan_modes at Enumerate time minus
	// "auto" (which is the Restore target, not a daemon-driven mode).
	// The PWM quantiser places mode boundaries on the band midpoints
	// of this slice's index range — len 2 splits at PWM 128, len 3
	// at PWM 85/170, etc.
	WritableModes []string

	// WritableShiftModes is the ordered (low-power → high-power) set
	// of shift_mode values the platform accepts (#1166). Populated
	// from available_shift_modes at Enumerate time. Empty on boards
	// whose msi-ec firmware allow-list group doesn't expose the file
	// (Enumerate degrades cleanly: the channel still works for
	// fan_mode-only control without CapWritePowerProfile set).
	WritableShiftModes []string
}

// Backend is the msi-ec implementation of hal.FanBackend. One instance
// is shared across the daemon — channel state lives in Channel.Opaque,
// so the Backend itself only carries logging + the test write-seam.
type Backend struct {
	logger *slog.Logger

	// writeFile is the seam tests use to inject a failing write or
	// observe write calls without going through the filesystem.
	// Defaults to os.WriteFile in NewBackend; production never
	// overrides it.
	writeFile func(path string, data []byte, perm os.FileMode) error
}

// NewBackend constructs a Backend that logs through the given slog
// logger. A nil logger falls back to slog.Default so callers without a
// wired logger still see messages.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{logger: logger, writeFile: os.WriteFile}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close releases backend-level resources. msi-ec holds no process-level
// state, so Close is a no-op. Idempotent per RULE-HAL-007.
func (b *Backend) Close() error { return nil }

// Enumerate probes for /sys/devices/platform/msi-ec/fan_mode +
// available_fan_modes. Hosts without the msi-ec module loaded (no
// directory at all) return an empty slice without an error so the
// registry's fan-out Enumerate admits the absence gracefully.
//
// When the directory exists but available_fan_modes lists only
// "auto", the backend logs at Info and returns an empty slice — the
// honest "no daemon-drivable surface here" path. The wizard's
// monitor-only branch handles that correctly and the journal carries
// the reason instead of a silent dead-end.
//
// Idempotent (RULE-HAL-001): the per-board mode list is determined by
// the loaded driver's firmware allow-list group and does not change
// within a single daemon lifetime.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := DefaultSysfsRoot
	fanModePath := filepath.Join(root, "fan_mode")
	if _, err := os.Stat(fanModePath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("msiec: stat %s: %w", fanModePath, err)
	}
	modes, err := readAvailableModes(root)
	if err != nil {
		// available_fan_modes is part of the msi-ec interface in every
		// released version; its absence on a host where fan_mode does
		// exist indicates a partial sysfs registration we shouldn't
		// drive blind.
		return nil, fmt.Errorf("msiec: read available_fan_modes: %w", err)
	}
	writable := filterWritableModes(modes)
	if len(writable) == 0 {
		b.logger.Info("msiec: no daemon-drivable modes on this board; only 'auto' available",
			"root", root, "modes", modes)
		return nil, nil
	}
	// Optional shift_mode surface — present on most CONF_G2_x boards,
	// absent on the early G1 group. Missing → drop the capability
	// silently (board still works for fan_mode-only control).
	shiftModes, shiftErr := readAvailableShiftModes(root)
	if shiftErr != nil && !errors.Is(shiftErr, fs.ErrNotExist) {
		b.logger.Warn("msiec: read available_shift_modes",
			"root", root, "err", shiftErr)
	}
	caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
	if len(shiftModes) > 0 {
		caps |= hal.CapWritePowerProfile
		b.logger.Info("msiec: shift_mode (power-profile) surface available",
			"root", root, "shift_modes", shiftModes)
	}
	return []hal.Channel{{
		ID:   root,
		Role: hal.RoleCPU,
		Caps: caps,
		Opaque: State{
			SysfsRoot:          root,
			WritableModes:      writable,
			WritableShiftModes: shiftModes,
		},
	}}, nil
}

// Read samples the current fan_mode + cpu realtime temperature. The
// fan_speed percentage exposed by msi-ec is not actual RPM (the
// driver's gpu/cpu realtime_fan_speed is a 0..100 / 0..150 percent
// reading, not a tachometer count) so Reading.RPM is intentionally
// left 0 — the in-kernel msi_wmi_platform hwmon device provides
// canonical tach RPM independently on the same machine and the
// controller correlates the two. Populating RPM with a percentage
// would violate the no-theatre rule (#1031).
//
// Failures populate Reading.OK=false and zero every other field per
// the hal.Reading empty-by-construction invariant. Never mutates
// observable state (RULE-HAL-002).
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	modeBytes, err := os.ReadFile(filepath.Join(st.SysfsRoot, "fan_mode"))
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	mode := strings.TrimSpace(string(modeBytes))
	pwm, perr := modeToPWM(mode, st.WritableModes)
	if perr != nil {
		b.logger.Debug("msiec: unrecognised fan_mode value",
			"root", st.SysfsRoot, "mode", mode, "err", perr)
		return hal.Reading{OK: false}, nil
	}
	r := hal.Reading{PWM: pwm, OK: true}
	if tempBytes, err := os.ReadFile(filepath.Join(st.SysfsRoot, "cpu", "realtime_temperature")); err == nil {
		if t, err := strconv.ParseFloat(strings.TrimSpace(string(tempBytes)), 64); err == nil {
			r.Temp = t
		}
	}
	return r, nil
}

// Write commands the channel to a 0-255 PWM byte. The byte is
// quantised to a mode name via pwmToMode and written to fan_mode.
// Unlike thinkpad, msi-ec has no per-write "enable" handshake — every
// fan_mode write is unconditional from the kernel's perspective.
//
// Errors from the sysfs write surface verbatim. Permission failures
// here would indicate a SELinux / AppArmor confinement issue rather
// than a kernel-side gate, so they are not wrapped in a typed sentinel
// (no recovery surface exists for it; the operator must adjust LSM
// policy).
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	mode := pwmToMode(pwm, st.WritableModes)
	if mode == "" {
		// Defence in depth: pwmToMode only returns "" when
		// WritableModes is empty, which Enumerate already refuses to
		// expose. A panic here would be louder than the silent no-op
		// it would replace.
		return fmt.Errorf("msiec: no writable modes for channel %q (broken state)", ch.ID)
	}
	path := filepath.Join(st.SysfsRoot, "fan_mode")
	if werr := b.writeFile(path, []byte(mode), 0o644); werr != nil {
		return fmt.Errorf("msiec: write %q to %s: %w", mode, path, werr)
	}
	return nil
}

// Restore returns the channel to BIOS-managed mode by writing "auto"
// to fan_mode. Matches the thinkpad backend's "level auto" semantics
// and the hwmon backend's pwm_enable=2.
//
// Idempotent + safe on un-written channels (RULE-HAL-004): "auto" is
// a clean write the kernel accepts in any state. There is no fallback
// — if the kernel refuses "auto" the underlying error surfaces so the
// watchdog logs the channel as un-restored and continues with the
// remaining channels per RULE-WD-RESTORE-PANIC.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	path := filepath.Join(st.SysfsRoot, "fan_mode")
	if werr := b.writeFile(path, []byte(ModeAuto), 0o644); werr != nil {
		return fmt.Errorf("msiec: restore (write %q to %s): %w", ModeAuto, path, werr)
	}
	b.logger.Info("msiec: restored to BIOS-managed fan_mode=auto", "root", st.SysfsRoot)
	return nil
}

// pwmToMode quantises a 0..255 PWM byte to a mode name from the
// ordered WritableModes slice (low-airflow → high-airflow). The band
// boundaries are placed at the midpoints so the sweep crosses each
// mode exactly once: for N modes the band width is 256/N rounded to
// integer, with the last band absorbing any remainder.
//
// Returns "" when modes is empty — Enumerate refuses to expose such a
// channel, but Write checks defensively anyway.
func pwmToMode(pwm uint8, modes []string) string {
	if len(modes) == 0 {
		return ""
	}
	// idx = floor(pwm * N / 256); clamp to [0, N-1].
	idx := int(pwm) * len(modes) / 256
	if idx < 0 {
		idx = 0
	}
	if idx >= len(modes) {
		idx = len(modes) - 1
	}
	return modes[idx]
}

// modeToPWM is the inverse mapping used by Read to populate
// Reading.PWM. Each writable mode maps to the centre of its band so a
// closed-loop write→read→compare round-trip is stable. "auto" maps to
// PWM=0 — the explicit "BIOS-managed, no daemon write yet" sentinel
// that signals the controller to ramp from the bottom of its curve on
// the first tick rather than from a midpoint guess.
//
// Returns ErrInvalidFanMode for any mode string not in either the
// canonical {silent,basic,advanced} set or the per-board WritableModes
// slice — the parser refuses to silently substitute on unknown values.
func modeToPWM(mode string, modes []string) (uint8, error) {
	if mode == ModeAuto {
		return 0, nil
	}
	for i, m := range modes {
		if m == mode {
			// Band centre: midpoint of [i*256/N, (i+1)*256/N).
			centre := (i*256 + 128) / len(modes)
			if centre < 0 {
				centre = 0
			}
			if centre > 255 {
				centre = 255
			}
			return uint8(centre), nil
		}
	}
	return 0, fmt.Errorf("%w: %q (writable=%v)", ErrInvalidFanMode, mode, modes)
}

// readAvailableModes parses sysfsRoot/available_fan_modes. Format is
// whitespace-separated (the msi-ec driver emits each valid mode on a
// single line, but historical tooling has used space separation; the
// parser tolerates both shapes). Returns the deduplicated, sorted-by-
// canonical-airflow-order list.
func readAvailableModes(sysfsRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(sysfsRoot, "available_fan_modes"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, tok := range strings.Fields(string(data)) {
		seen[tok] = true
	}
	out := make([]string, 0, len(seen))
	// Emit modes in canonical low→high airflow order so callers don't
	// need to re-sort. Unknown modes (forward-compat for new upstream
	// driver values) are appended after the known set alphabetically.
	for _, m := range canonicalModeOrder {
		if seen[m] {
			out = append(out, m)
			delete(seen, m)
		}
	}
	extra := make([]string, 0, len(seen))
	for m := range seen {
		extra = append(extra, m)
	}
	sort.Strings(extra)
	out = append(out, extra...)
	return out, nil
}

// readAvailableShiftModes parses sysfsRoot/available_shift_modes,
// mirroring readAvailableModes but for the shift_mode surface (#1166).
// Returns (nil, fs.ErrNotExist) wrapped when the file is absent so
// Enumerate can drop the capability silently on boards without it.
// Otherwise: deduplicated, sorted in eco → comfort → turbo canonical
// order, with any unknown forward-compat values appended alphabetically.
func readAvailableShiftModes(sysfsRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(sysfsRoot, "available_shift_modes"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, tok := range strings.Fields(string(data)) {
		seen[tok] = true
	}
	out := make([]string, 0, len(seen))
	for _, m := range canonicalShiftModeOrder {
		if seen[m] {
			out = append(out, m)
			delete(seen, m)
		}
	}
	extra := make([]string, 0, len(seen))
	for m := range seen {
		extra = append(extra, m)
	}
	sort.Strings(extra)
	out = append(out, extra...)
	return out, nil
}

// canonicalShiftModeOrder is the low-power → high-power ordering of
// the upstream msi-ec shift_mode set. Roughly: eco aggressive thermal
// throttling + longest battery life, comfort balanced (default),
// turbo max sustained power + lifted BIOS fan curve.
var canonicalShiftModeOrder = []string{ShiftModeEco, ShiftModeComfort, ShiftModeTurbo}

// AvailablePowerProfiles satisfies hal.PowerProfileBackend (#1166).
// Returns the channel's WritableShiftModes verbatim; empty slice when
// the board's msi-ec firmware allow-list group doesn't expose
// shift_mode.
func (b *Backend) AvailablePowerProfiles(ch hal.Channel) ([]string, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return nil, err
	}
	if len(st.WritableShiftModes) == 0 {
		return nil, fmt.Errorf("msiec: shift_mode surface not exposed on %q", ch.ID)
	}
	// Defensive copy: caller mutations must not bleed into channel state.
	out := make([]string, len(st.WritableShiftModes))
	copy(out, st.WritableShiftModes)
	return out, nil
}

// ReadPowerProfile satisfies hal.PowerProfileBackend (#1166). Returns
// the current shift_mode verbatim. On boards with incomplete CONF_G2_6
// mappings the kernel may emit a raw "unknown (NN)" string; we pass it
// through so the operator sees what the firmware reports.
func (b *Backend) ReadPowerProfile(ch hal.Channel) (string, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(st.SysfsRoot, "shift_mode"))
	if err != nil {
		return "", fmt.Errorf("msiec: read shift_mode: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WritePowerProfile satisfies hal.PowerProfileBackend (#1166). Refuses
// values not in WritableShiftModes so a typo can't silently set an
// unmapped raw value. Errors from the sysfs write surface verbatim.
func (b *Backend) WritePowerProfile(ch hal.Channel, profile string) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	if len(st.WritableShiftModes) == 0 {
		return fmt.Errorf("msiec: shift_mode surface not exposed on %q", ch.ID)
	}
	matched := false
	for _, m := range st.WritableShiftModes {
		if m == profile {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("msiec: %w: %q (available=%v)",
			ErrInvalidShiftMode, profile, st.WritableShiftModes)
	}
	path := filepath.Join(st.SysfsRoot, "shift_mode")
	if werr := b.writeFile(path, []byte(profile), 0o644); werr != nil {
		return fmt.Errorf("msiec: write shift_mode %q: %w", profile, werr)
	}
	return nil
}

// canonicalModeOrder is the low-airflow → high-airflow ordering of
// the upstream msi-ec mode set (drivers/msi-ec.c MODE_DESC). When the
// driver adds new modes between releases, readAvailableModes appends
// them alphabetically after this prefix — the new modes still drive
// at the high end of the PWM band, just not in a hand-curated order.
var canonicalModeOrder = []string{ModeAuto, ModeSilent, ModeBasic, ModeAdvanced}

// filterWritableModes removes "auto" from the per-board mode list —
// "auto" is the Restore target, not a daemon-driven mode. Returns the
// remaining modes in the same order.
func filterWritableModes(modes []string) []string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		if m == ModeAuto {
			continue
		}
		out = append(out, m)
	}
	return out
}

// stateFrom coerces a Channel's Opaque payload into the msiec State
// shape. Accepts both the value and pointer form so callers can
// construct either — matches the equivalent helper in
// internal/hal/thinkpad/.
func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		if v.SysfsRoot == "" {
			return State{}, errors.New("msiec: channel state has empty SysfsRoot")
		}
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("msiec: nil opaque state")
		}
		if v.SysfsRoot == "" {
			return State{}, errors.New("msiec: channel state has empty SysfsRoot")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("msiec: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}
