// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package thinkpad implements the hal.FanBackend interface over
// /proc/acpi/ibm/fan — the procfs surface that the thinkpad_acpi
// kernel module exposes for direct EC fan control on Lenovo ThinkPads
// (and a small number of relabelled IBM-era ThinkCentre desktops).
//
// The thinkpad_acpi driver profile in internal/hwdb/catalog/drivers/
// declares capability "rw_proc" and pwm_unit "thinkpad_level" with
// pwm_unit_max 7 — the firmware exposes exactly eight discrete fan
// states (level 0..7) plus the named pseudo-levels "auto",
// "disengaged", and "full-speed". This backend is the runtime that
// honours that profile.
//
// Why a dedicated backend rather than reusing internal/hal/hwmon:
//
//  1. The procfs interface is the canonical write path on kernels
//     built without CONFIG_THINKPAD_ACPI_HWMON (a small but non-empty
//     slice of mainline distros — Alpine, some embedded builds).
//     internal/hal/hwmon cannot drive these hosts at all; this backend
//     can.
//
//  2. Even where the hwmon surface exists, writes to pwm1 are
//     quantised internally by the kernel driver to the same eight-
//     level grid. Calibration and the controller's PWM-write cadence
//     benefit from knowing the grid up-front: PWM 33 and PWM 63 both
//     land on firmware level 1, so probing every PWM step is wasted
//     work.
//
//  3. The thinkpad_acpi exit contract is "restore_auto" (per the
//     driver profile + RULE-HWDB-PR2-13). The hwmon backend handles
//     this via pwm_enable=2, but the procfs analogue is the literal
//     string "level auto" — a different syscall sequence. Hwmon's
//     fallbacks (WritePWM=255 when pwm_enable is unsupported) would
//     pin the fan at the firmware ceiling indefinitely on these
//     hosts; "level auto" hands control back to the EC's BIOS curve
//     immediately and is the correct restore primitive.
//
//  4. ThinkPads ship with a kernel-side fan watchdog (the
//     fan_watchdog modparam, default 120 s): if no PWM write reaches
//     the EC within the timeout, the EC reverts to its auto curve.
//     The controller's 2 s tick comfortably beats this, but the
//     backend's Restore path additionally writes "level auto" so a
//     daemon-shutdown sequence longer than the watchdog window
//     still lands on a safe BIOS-managed state rather than the
//     daemon's last committed level.
//
// Channel.Opaque carries the procfs path explicitly so test fixtures
// can mint channels pointing at a temp file without redirecting the
// whole backend. Production always sees /proc/acpi/ibm/fan.
//
// Writes are unconditional once Enumerate returns a channel — there
// is no per-backend opt-in flag, matching the v0.6.1 NBFC and
// Corsair posture (see feedback-dont-default-writes-off). Safety is
// enforced by:
//   - the kernel returning EPERM when fan_control=0 (surfaced as
//     ErrFanControlDisabled; the modprobe-options-write endpoint
//     fixes this on operator click);
//   - the closed firmware level grid (0..7 + named pseudo-levels,
//     enforced by the kernel — there is no level 8);
//   - the watchdog's Restore-on-exit covering every shutdown path
//     (RULE-WD-RESTORE-EXIT);
//   - RULE-IDLE-02 + RULE-IDLE-03 refusing the daemon entirely on
//     battery or in a container before any write fires.
package thinkpad

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by
// this backend. Kept as a package-level constant so callers
// (cmd/ventd/calresolver.go, watchdog, doctor) reference it without
// importing the hal package just for the string.
const BackendName = "thinkpad"

// DefaultProcFanPath is the canonical procfs file for thinkpad_acpi.
// The kernel exposes exactly one such file regardless of how many
// physical fans are populated (dual-fan models share pwm1 — fan2 is
// tach-only, see the lenovo-thinkpad.yaml catalog notes).
const DefaultProcFanPath = "/proc/acpi/ibm/fan"

// FirmwareLevelMax is the highest accepted numeric level. "disengaged"
// and "full-speed" override this for emergency cooling; ventd's
// controller does not drive those — they remain reserved for an
// operator-explicit override surface (deferred).
const FirmwareLevelMax = 7

// State is the per-channel payload carried in hal.Channel.Opaque.
// Exported so the contract-test harness (and any future channel-
// minting site) can construct channels without going through
// Enumerate.
type State struct {
	// ProcPath is the absolute path to the /proc/acpi/ibm/fan file
	// the backend reads / writes. In production this is always
	// DefaultProcFanPath; tests inject a temp file.
	ProcPath string

	// FanIndex is the firmware fan number (1-based). All current
	// ThinkPads expose a single PWM channel under fan1; fan2 (when
	// present) is tach-only and is not represented as a Channel.
	// Reserved for future expansion if/when Lenovo ships dual-PWM
	// ThinkPads.
	FanIndex int
}

// Backend is the thinkpad implementation of hal.FanBackend. One
// instance is shared across the daemon — channel state lives in
// Channel.Opaque, so the Backend itself only carries logging + the
// "have we issued enable on this path yet" guard.
type Backend struct {
	logger *slog.Logger

	// acquired tracks whether we've issued the procfs "enable"
	// command on a given path. The kernel accepts "level N" without
	// a prior "enable" on every modern build, but older 5.x kernels
	// (still common on Debian-stable and Ubuntu LTS) refuse the
	// first level write with EPERM until "enable" has run once.
	// We try the enable on first Write; failures are non-fatal (the
	// subsequent level write reports the canonical EPERM separately
	// if the gate really is closed).
	acquired sync.Map // key: ProcPath (string), value: struct{}

	// writeFile is the seam tests use to inject a failing write
	// (e.g. to exercise the EPERM-wrap path without depending on
	// chmod-based DAC behaviour that the CI runner's
	// CAP_DAC_OVERRIDE-equivalent silently bypasses). Defaults to
	// os.WriteFile in NewBackend; production code never overrides it.
	writeFile func(path string, data []byte, perm os.FileMode) error
}

// NewBackend constructs a Backend that logs through the given slog
// logger. A nil logger falls back to slog.Default so callers without
// a wired logger still see messages.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{logger: logger, writeFile: os.WriteFile}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close releases backend-level resources. The thinkpad backend holds
// no process-level state, so Close is a no-op. Idempotent per
// RULE-HAL-007.
func (b *Backend) Close() error { return nil }

// Enumerate probes for /proc/acpi/ibm/fan and returns a single
// channel when the file is readable. The procfs interface is exposed
// by the thinkpad_acpi kernel module; its presence is a sufficient
// signal that the host is a thinkpad_acpi-bearing system. Hosts
// without the module (or with the module excluded at kernel build
// time) return an empty slice — not an error — so the registry's
// fan-out Enumerate (internal/hal/registry.go) admits the absence
// gracefully.
//
// Idempotent (RULE-HAL-001): the procfs file's presence is determined
// by the kernel module's load state, which doesn't change within a
// single daemon lifetime under any realistic scenario.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	procPath := DefaultProcFanPath
	if _, err := os.Stat(procPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("thinkpad: stat %s: %w", procPath, err)
	}
	return []hal.Channel{{
		ID:   procPath,
		Role: hal.RoleCPU,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			ProcPath: procPath,
			FanIndex: 1,
		},
	}}, nil
}

// Read samples the current fan state from /proc/acpi/ibm/fan. The
// kernel emits a multi-line key:value response; the parser extracts
// speed (RPM) and level (firmware 0..7, "auto", "disengaged",
// "full-speed"). Failures populate Reading.OK=false and zero every
// other field per the empty-by-construction invariant in hal.Reading.
//
// Never mutates observable state (RULE-HAL-002): only os.ReadFile is
// invoked on the procfs path.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	data, err := os.ReadFile(st.ProcPath)
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	r, perr := parseProcFan(data)
	if perr != nil {
		b.logger.Debug("thinkpad: parse procfs response failed",
			"path", st.ProcPath, "err", perr)
		return hal.Reading{OK: false}, nil
	}
	return r, nil
}

// Write commands the channel to a 0-255 PWM byte. The byte is
// quantised to a firmware level via pwmToLevel and dispatched as
// "level N\n" to the procfs file.
//
// The first Write on each path also issues "enable\n" so 5.x-era
// kernels that gate level writes behind a prior enable command
// don't reject the first tick. Newer kernels treat the enable as a
// no-op. Enable failures are logged at DEBUG and ignored — the
// subsequent level write surfaces the canonical EPERM separately if
// the gate really is closed.
//
// EPERM on the level write is wrapped as ErrFanControlDisabled so
// downstream classification (recovery card + modprobe-options-write
// dispatch) can branch on the typed sentinel without string matching.
// RULE-WIZARD-RECOVERY-10 + RULE-MODPROBE-OPTIONS-01 are the existing
// recovery surfaces; the wrapped error is the runtime signal that
// fires them.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	if _, loaded := b.acquired.LoadOrStore(st.ProcPath, struct{}{}); !loaded {
		if err := b.writeFile(st.ProcPath, []byte("enable\n"), 0o644); err != nil {
			b.logger.Debug("thinkpad: enable command failed on first write; continuing",
				"path", st.ProcPath, "err", err)
			// Don't propagate — many kernels accept "level N" without
			// a prior "enable", and we want the EPERM (if any) to
			// surface on the level write below where it carries
			// actionable context.
		}
	}
	level := pwmToLevel(pwm)
	cmd := fmt.Sprintf("level %d\n", level)
	if werr := b.writeFile(st.ProcPath, []byte(cmd), 0o644); werr != nil {
		switch {
		case errors.Is(werr, fs.ErrPermission):
			// EPERM almost always means fan_control=0. The kernel's
			// thinkpad_acpi.c returns -EPERM silently in this case —
			// no dmesg, no syslog — so the typed wrap is the only
			// signal the wizard / doctor can branch on. Double-%w
			// preserves the underlying syscall.EPERM chain so
			// existing fs.ErrPermission classifiers still match.
			return fmt.Errorf("%w: %w", ErrFanControlDisabled, werr)
		case errors.Is(werr, os.ErrInvalid):
			return fmt.Errorf("thinkpad: write %q to %s: %w", strings.TrimSpace(cmd), st.ProcPath, werr)
		default:
			return fmt.Errorf("thinkpad: write %q to %s: %w", strings.TrimSpace(cmd), st.ProcPath, werr)
		}
	}
	return nil
}

// Restore returns the channel to BIOS-managed mode by writing
// "level auto" to the procfs file. The driver profile's exit_behaviour
// is "restore_auto"; this is the literal procfs encoding of that
// directive.
//
// On the rare case where "level auto" itself returns EPERM (operator
// has manually clobbered fan_control between Write and Restore, or a
// kernel update changed the gate mid-daemon-lifetime), the backend
// falls back to "disable\n" — the procfs command that hands the fan
// back to the firmware curve without touching the manual-mode gate.
// If both fail, the original error is surfaced so the watchdog logs
// the channel as un-restored and continues with the remaining
// channels per RULE-WD-RESTORE-PANIC.
//
// Idempotent + safe on un-written channels (RULE-HAL-004): "level
// auto" + "disable" are both clean writes that the kernel accepts on
// any channel in any state.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	autoErr := b.writeFile(st.ProcPath, []byte("level auto\n"), 0o644)
	if autoErr == nil {
		b.acquired.Delete(st.ProcPath)
		b.logger.Info("thinkpad: restored to BIOS-managed level auto",
			"path", st.ProcPath)
		return nil
	}
	disErr := b.writeFile(st.ProcPath, []byte("disable\n"), 0o644)
	if disErr == nil {
		b.acquired.Delete(st.ProcPath)
		b.logger.Warn("thinkpad: 'level auto' refused; fell back to 'disable'",
			"path", st.ProcPath, "auto_err", autoErr)
		return nil
	}
	b.logger.Error("thinkpad: restore failed on both 'level auto' and 'disable'",
		"path", st.ProcPath, "auto_err", autoErr, "disable_err", disErr)
	return autoErr
}

// pwmToLevel maps a 0..255 PWM byte to a firmware level in the
// closed range [0, 7] using round-half-up quantisation:
//
//	level = (pwm * 7 + 127) / 255
//
// This places the band boundaries at PWM values
// {0,18}, {19,54}, {55,90}, ..., {235,255} — symmetric around level
// midpoints (18, 54, 90, 127, 163, 199, 235), matching what an
// operator typing "around 50%" expects (PWM=128 → level 4, the
// middle of the seven-level range above stop). The 7+127 form is the
// integer-arithmetic equivalent of math.Round(pwm/255 * 7) and
// avoids any floating-point dependency in the hot path.
func pwmToLevel(pwm uint8) uint8 {
	level := (int(pwm)*FirmwareLevelMax + 127) / 255
	if level < 0 {
		return 0
	}
	if level > FirmwareLevelMax {
		return FirmwareLevelMax
	}
	return uint8(level)
}

// levelToPWM is the inverse mapping used by Read to populate
// Reading.PWM. Each level maps to the centre of its band so a
// closed-loop write→read→compare round-trip is stable:
//
//	pwm = (level * 255 + 3) / 7
//
// Centres at PWM {0, 36, 73, 109, 146, 182, 219, 255}. The +3 is
// the round-to-nearest fixup for the integer divide; the choice of
// centres means writing a PWM in the middle of any band and reading
// it back returns the same PWM (mod the level-quantisation).
//
// "auto" (firmware-managed) is mapped to PWM=128 — the explicit
// midpoint sentinel — so a controller that reads before any Write
// sees a plausible baseline rather than 0 (which would suggest the
// fan is stopped).
//
// "disengaged" / "full-speed" both map to PWM=255 (the firmware's
// emergency-cooling override). These pseudo-levels exceed level 7
// in actual fan speed but report through the same 0..255 surface.
func levelToPWM(level uint8) uint8 {
	if level > FirmwareLevelMax {
		level = FirmwareLevelMax
	}
	pwm := (int(level)*255 + 3) / FirmwareLevelMax
	if pwm < 0 {
		return 0
	}
	if pwm > 255 {
		return 255
	}
	return uint8(pwm)
}

// parseProcFan extracts the speed (RPM) and level fields from a
// /proc/acpi/ibm/fan response. The kernel emits lines like:
//
//	status:		enabled
//	speed:		3742
//	level:		auto
//	commands:	level <level> (<level> is 0-7, auto, disengaged, full-speed)
//	commands:	enable, disable
//	commands:	watchdog <timeout> (<timeout> is 0 (off), 1-120 (seconds))
//
// Whitespace separation is one or more spaces / tabs; the key is
// case-sensitive ("status" lowercase). Some kernel builds omit the
// status line when fan_control is locked out — the parser tolerates
// any field missing except level (which is the canonical "is this
// readout actually meaningful" anchor).
func parseProcFan(data []byte) (hal.Reading, error) {
	var (
		rpm      uint16
		levelStr string
		haveLvl  bool
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "speed":
			if v, err := strconv.ParseUint(val, 10, 16); err == nil {
				rpm = uint16(v)
			}
		case "level":
			levelStr = val
			haveLvl = true
		}
	}
	if !haveLvl {
		return hal.Reading{}, ErrInvalidProcFanResponse
	}
	var pwm uint8
	switch levelStr {
	case "auto":
		// Firmware-managed: report the explicit midpoint as the
		// "no daemon write yet" sentinel. The controller's first
		// Write supersedes this on the next tick anyway.
		pwm = 128
	case "disengaged", "full-speed":
		pwm = 255
	default:
		n, err := strconv.ParseUint(levelStr, 10, 8)
		if err != nil {
			return hal.Reading{}, fmt.Errorf("%w: level=%q", ErrInvalidProcFanResponse, levelStr)
		}
		if n > FirmwareLevelMax {
			return hal.Reading{}, fmt.Errorf("%w: level=%d out of range [0,%d]", ErrInvalidProcFanResponse, n, FirmwareLevelMax)
		}
		pwm = levelToPWM(uint8(n))
	}
	return hal.Reading{
		PWM: pwm,
		RPM: rpm,
		OK:  true,
	}, nil
}

// stateFrom coerces a Channel's Opaque payload into the thinkpad
// State shape. Accepts both the value and pointer form so callers
// can construct either — matches the equivalent helper in
// internal/hal/hwmon.
func stateFrom(ch hal.Channel) (State, error) {
	return hal.StateFrom(ch, "thinkpad", func(s State) error {
		if s.ProcPath == "" {
			return errors.New("thinkpad: channel state has empty ProcPath")
		}
		return nil
	})
}
