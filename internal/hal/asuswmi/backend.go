// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package asuswmi implements the hal.FanBackend + hal.CurveSink interfaces over
// the mainline asus-wmi driver's custom-fan-curve hwmon surface — the
// "asus_custom_fan_curve" hwmon device that drivers/platform/x86/asus-wmi.c
// registers on ROG / TUF / Strix / Scar / Flow / Zephyrus / Zenbook / Vivobook
// / ProArt / ROG Ally hardware (kernel 5.17+, the feature stabilised across the
// ASUS line by 6.4).
//
// Why a dedicated CurveSink backend rather than the generic hwmon pwm path:
//
// The asus_custom_fan_curve interface is NOT a per-tick duty register. Its
// write surface is an eight-point fan curve per fan:
//
//	pwm1_auto_point1..8_temp   CPU fan curve temperatures (whole °C)
//	pwm1_auto_point1..8_pwm    CPU fan curve duty (0-255, scaled to percent
//	                           by the kernel before the ACPI call)
//	pwm1_enable                1 = manual (apply the custom curve),
//	                           2 = factory auto (curve retained),
//	                           3 = factory auto + reset curve to default
//	pwm2_*                     the same block for the GPU fan
//
// The firmware then runs the control loop itself against the EC's own thermal
// zone — exactly the model hal.CurveSink exists for (spec-17 PR-1b, shared with
// the amdgpu RDNA3/4 gpu_od/fan_ctrl/fan_curve backend). The controller
// programs the curve once at apply time and re-programs only when the bound
// curve changes; there is no per-tick PWM write for these channels. Driving
// this surface through the per-tick hwmon backend would mean rewriting all
// eight points every tick with a flattened curve — thrashing the EC and
// fighting the firmware's own interpolation — so this backend advertises
// CapWriteCurve, not CapWritePWM.
//
// pwm1 is the CPU fan (RoleCPU); pwm2 is the GPU fan (RoleGPU). A host may
// expose one or both depending on the model (ultrabooks without a discrete GPU
// expose only pwm1).
//
// Writes are unconditional once Enumerate returns a channel — no per-backend
// opt-in flag, matching the v0.6.1 NBFC / Corsair / thinkpad / legion posture
// (see feedback-dont-default-writes-off). Safety is enforced by:
//   - the closed eight-point curve shape the kernel + firmware validate (a
//     malformed curve is refused with EIO → ErrFanCurveRefused, not applied);
//   - the controller programming the curve once and the watchdog's
//     Restore-on-exit writing pwm_enable=2 to hand the fan back to firmware
//     auto (RULE-WD-RESTORE-EXIT);
//   - RULE-IDLE-02 + RULE-IDLE-03 refusing the daemon on battery / in a
//     container before any write fires.
package asuswmi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hal"
)

// Compile-time proof the backend satisfies both the per-tick FanBackend
// contract and the curve-upload CurveSink contract. asus_custom_fan_curve is
// curve-only, so every channel it emits is a CurveSink channel; Write returns a
// descriptive error (the surface has no per-tick duty register).
var (
	_ hal.FanBackend = (*Backend)(nil)
	_ hal.CurveSink  = (*Backend)(nil)
)

// BackendName is the registry tag applied to channels produced by this backend.
// config.Fan.Type == "asuswmi" routes here via the controller's registry-lookup
// dispatch.
const BackendName = "asuswmi"

// DefaultHwmonRoot is the canonical sysfs hwmon class directory. Enumerate
// scans its hwmonN/name entries for the asus_custom_fan_curve device. Tests
// inject a fake tree.
const DefaultHwmonRoot = "/sys/class/hwmon"

// HwmonName is the hwmon `name` attribute the asus-wmi driver registers for the
// custom-fan-curve device (devm_hwmon_device_register_with_groups(...,
// "asus_custom_fan_curve", ...)). Enumerate keys on it.
const HwmonName = "asus_custom_fan_curve"

// Fan indices on the asus_custom_fan_curve hwmon. pwm1 is the CPU fan, pwm2 the
// GPU fan — fixed by the driver's FAN_CURVE_DEV_CPU (0x00) / FAN_CURVE_DEV_GPU
// (0x01) ordering.
const (
	cpuFanIndex = 1
	gpuFanIndex = 2
)

// pwm_enable values accepted by the asus_custom_fan_curve hwmon (from the
// kernel driver's store handler): 1 applies the custom curve, 2 hands the fan
// back to factory auto while retaining the stored curve, 3 additionally resets
// the curve to the factory default. ventd acquires with 1 and restores with 2.
const (
	enableManual = 1
	enableAuto   = 2
)

// State is the per-channel payload carried in hal.Channel.Opaque. It holds the
// resolved hwmon directory and the fan index so Read / WriteCurve / Restore
// operate without re-enumerating. Exported so the backend test harness can mint
// channels without going through Enumerate.
type State struct {
	// HwmonDir is the absolute path to the asus_custom_fan_curve hwmon
	// directory (e.g. /sys/class/hwmon/hwmon4). In production this is resolved
	// by Enumerate; tests inject a temp dir.
	HwmonDir string
	// FanIndex is the 1-based pwm fan number: 1 = CPU, 2 = GPU.
	FanIndex int
}

// Backend is the asus-wmi custom-fan-curve implementation of hal.FanBackend /
// hal.CurveSink. One instance is shared across the daemon — channel state lives
// in Channel.Opaque, so the Backend only carries logging, the hwmon scan root,
// and the file I/O seams tests override.
type Backend struct {
	logger    *slog.Logger
	hwmonRoot string

	// File I/O seams default to the os.* functions in NewBackend. Tests
	// override them to point at fixtures or inject failures; production code
	// never reassigns. Kept as struct fields (not package vars) so parallel
	// test backends don't clobber each other.
	writeFile func(path string, data []byte, perm os.FileMode) error
	readFile  func(path string) ([]byte, error)
	statFile  func(path string) (os.FileInfo, error)
	glob      func(pattern string) ([]string, error)
}

// NewBackend constructs a Backend rooted at DefaultHwmonRoot. A nil logger
// falls back to slog.Default so callers without a wired logger still see
// messages.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:    logger,
		hwmonRoot: DefaultHwmonRoot,
		writeFile: os.WriteFile,
		readFile:  os.ReadFile,
		statFile:  os.Stat,
		glob:      filepath.Glob,
	}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close releases backend-level resources. The asuswmi backend holds no
// process-level state, so Close is a no-op. Idempotent per RULE-HAL-007.
func (b *Backend) Close() error { return nil }

// Enumerate scans the hwmon class for the asus_custom_fan_curve device and
// returns one CurveSink channel per fan whose eight-point curve attributes are
// present (pwm1 → CPU, pwm2 → GPU). Hosts without the device — every
// non-ASUS-ROG machine, and ASUS hosts on a kernel without the custom-fan-curve
// feature — return an empty slice (not an error), so the registry's fan-out
// Enumerate admits the absence gracefully.
//
// Idempotent (RULE-HAL-001): the hwmon device's presence is fixed by the
// kernel module's load state, which doesn't change within a daemon lifetime;
// the returned IDs are sorted by the hwmon glob so the set is stable across
// calls.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches, err := b.glob(filepath.Join(b.hwmonRoot, "hwmon*"))
	if err != nil {
		return nil, fmt.Errorf("asuswmi: glob %s: %w", b.hwmonRoot, err)
	}
	var out []hal.Channel
	for _, dir := range matches {
		nameData, err := b.readFile(filepath.Join(dir, "name"))
		if err != nil {
			continue // hwmon dir without a name file — not our device.
		}
		if strings.TrimSpace(string(nameData)) != HwmonName {
			continue
		}
		for _, fan := range []struct {
			index int
			role  hal.ChannelRole
		}{
			{cpuFanIndex, hal.RoleCPU},
			{gpuFanIndex, hal.RoleGPU},
		} {
			// A fan is present only if its first curve anchor exists; ASUS
			// ultrabooks expose pwm1 (CPU) but no pwm2 (no discrete GPU fan).
			probe := fmt.Sprintf("pwm%d_auto_point1_pwm", fan.index)
			if _, err := b.statFile(filepath.Join(dir, probe)); err != nil {
				continue
			}
			pwmPath := filepath.Join(dir, fmt.Sprintf("pwm%d", fan.index))
			out = append(out, hal.Channel{
				ID:   pwmPath,
				Role: fan.role,
				Caps: hal.CapRead | hal.CapWriteCurve | hal.CapRestore,
				Opaque: State{
					HwmonDir: dir,
					FanIndex: fan.index,
				},
			})
		}
	}
	return out, nil
}

// Read reports the current commanded duty by reading the bare pwmN node when
// the kernel exposes it (0-255). The asus_custom_fan_curve hwmon does not
// surface a fan tachometer — RPM lives on the separate asus sensors hwmon — so
// RPM is left zero. When pwmN is absent or unreadable the Reading is OK=false
// ("skip this tick"); the controller drives these channels via CurveSink and
// does not call Read per tick, so a non-OK reading is harmless.
//
// Never mutates observable state (RULE-HAL-002): only readFile is invoked.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	pwmPath := filepath.Join(st.HwmonDir, fmt.Sprintf("pwm%d", st.FanIndex))
	data, err := b.readFile(pwmPath)
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || v < 0 || v > 255 {
		return hal.Reading{OK: false}, nil
	}
	return hal.Reading{PWM: uint8(v), OK: true}, nil
}

// Write is unsupported on this backend: the asus_custom_fan_curve surface has
// no per-tick duty register, only the eight-point curve. The channels Enumerate
// emits advertise CapWriteCurve (not CapWritePWM), so the controller routes
// them through WriteCurve and never calls Write here. The explicit error guards
// against a mis-wired caller rather than silently no-op'ing.
func (b *Backend) Write(ch hal.Channel, _ uint8) error {
	if _, err := stateFrom(ch); err != nil {
		return err
	}
	return fmt.Errorf("asuswmi: channel %q is curve-controlled (CapWriteCurve); use WriteCurve, not per-tick Write", ch.ID)
}

// WriteCurve programs the channel's eight-point hardware fan curve
// (hal.CurveSink). The caller supplies points ascending by TempC with
// percentages 0-100; resampleCurve normalises them to exactly eight anchors
// with strictly-increasing temperatures and non-decreasing duty, converting
// each percentage to the kernel's 0-255 PWM byte. Each anchor's temp + pwm node
// is written, then pwmN_enable is set to 1 (manual) to apply the curve.
//
// A firmware rejection (the "BIOS rejected fan curve" failure on some models)
// surfaces as EIO/ENODEV on the sysfs write and is wrapped as ErrFanCurveRefused
// so downstream classification can branch via errors.Is. EPERM (the node isn't
// writable — permissions, or the module loaded without the feature) is wrapped
// as hal.ErrNotPermitted, matching the rest of the HAL.
func (b *Backend) WriteCurve(ch hal.Channel, points []hal.CurvePoint) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	anchors, err := resampleCurve(points)
	if err != nil {
		return err
	}
	for i, a := range anchors {
		n := i + 1 // sysfs points are 1-based.
		tempPath := filepath.Join(st.HwmonDir, fmt.Sprintf("pwm%d_auto_point%d_temp", st.FanIndex, n))
		if werr := b.writeFile(tempPath, []byte(strconv.Itoa(a.TempC)), 0o644); werr != nil {
			return wrapWrite("auto_point temp", tempPath, werr)
		}
		pwmPath := filepath.Join(st.HwmonDir, fmt.Sprintf("pwm%d_auto_point%d_pwm", st.FanIndex, n))
		if werr := b.writeFile(pwmPath, []byte(strconv.Itoa(int(a.PWM))), 0o644); werr != nil {
			return wrapWrite("auto_point pwm", pwmPath, werr)
		}
	}
	// Apply the curve: pwmN_enable = 1 (manual). Writing the points without
	// flipping enable leaves the firmware on its factory curve.
	enablePath := filepath.Join(st.HwmonDir, fmt.Sprintf("pwm%d_enable", st.FanIndex))
	if werr := b.writeFile(enablePath, []byte(strconv.Itoa(enableManual)), 0o644); werr != nil {
		return wrapWrite("pwm_enable", enablePath, werr)
	}
	b.logger.Info("asuswmi: programmed custom fan curve",
		"hwmon", st.HwmonDir, "fan", st.FanIndex, "anchors", len(anchors))
	return nil
}

// Restore hands the fan back to factory-auto control by writing pwmN_enable=2.
// The driver profile's exit_behaviour is "restore_auto"; enable=2 keeps the
// stored curve intact (so a later re-acquire is cheap) while letting the
// firmware's own curve run — the asus_custom_fan_curve analogue of thinkpad's
// "level auto" / legion's "balanced".
//
// Idempotent + safe on un-written channels (RULE-HAL-004): writing enable=2 to
// a channel that ventd never programmed is a clean no-op from the firmware's
// perspective.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	enablePath := filepath.Join(st.HwmonDir, fmt.Sprintf("pwm%d_enable", st.FanIndex))
	if werr := b.writeFile(enablePath, []byte(strconv.Itoa(enableAuto)), 0o644); werr != nil {
		b.logger.Error("asuswmi: restore to factory auto failed",
			"path", enablePath, "err", werr)
		return wrapWrite("pwm_enable restore", enablePath, werr)
	}
	b.logger.Info("asuswmi: restored fan to factory auto (pwm_enable=2)",
		"hwmon", st.HwmonDir, "fan", st.FanIndex)
	return nil
}

// wrapWrite classifies a sysfs write failure into the HAL's typed sentinels:
// EPERM/EACCES → hal.ErrNotPermitted (a misconfiguration retries won't cure),
// other errors (EIO/ENODEV — the firmware refusing the curve) →
// ErrFanCurveRefused. Both double-%w so the underlying syscall error stays in
// the chain for callers matching fs.ErrPermission etc.
func wrapWrite(what, path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %s %s: %w", hal.ErrNotPermitted, what, path, err)
	default:
		return fmt.Errorf("%w: %s %s: %w", ErrFanCurveRefused, what, path, err)
	}
}

// stateFrom coerces a Channel's Opaque payload into the asuswmi State shape.
// Accepts both value and pointer forms — matches the helper in
// internal/hal/thinkpad and internal/hal/legion.
func stateFrom(ch hal.Channel) (State, error) {
	return hal.StateFrom(ch, "asuswmi", func(s State) error {
		if s.HwmonDir == "" {
			return ErrNoFanCurveHwmon
		}
		if s.FanIndex != cpuFanIndex && s.FanIndex != gpuFanIndex {
			return fmt.Errorf("asuswmi: channel state has invalid FanIndex %d (want 1=CPU or 2=GPU)", s.FanIndex)
		}
		return nil
	})
}
