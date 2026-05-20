// SPDX-License-Identifier: GPL-3.0-or-later
package msiec

import "errors"

// ErrSysfsAbsent is returned when /sys/devices/platform/msi-ec/ does
// not exist at probe time. The msi-ec out-of-tree module (BeardOverflow)
// is not loaded, or the host firmware revision is not in its allow-list
// (the driver refuses to bind on unsupported EC firmware). The probe
// layer treats this as "not an MSI-EC host" — not a daemon failure.
var ErrSysfsAbsent = errors.New("msiec: /sys/devices/platform/msi-ec not found; msi-ec module not loaded or firmware not in allow-list")

// ErrNoWritableModes is returned when available_fan_modes parses cleanly
// but contains only "auto" — i.e. the board exposes mode switching but
// there is no non-firmware-managed option for the daemon to drive. With
// only "auto" available, every Write would re-affirm BIOS control, which
// is observably indistinguishable from doing nothing. The backend
// refuses to expose a CapWritePWM channel in that case so the wizard's
// "no controllable fans" branch surfaces honestly instead of pretending
// to drive.
var ErrNoWritableModes = errors.New("msiec: only 'auto' fan_mode available; no daemon-drivable modes on this board")

// ErrInvalidFanMode is returned by parseFanMode when the kernel emits a
// mode string that is not in any known allow-list and not present in the
// board's available_fan_modes set. The parser returns this rather than
// silently substituting a default so a kernel-driver change (new mode
// added upstream) surfaces as a clean log line instead of a quiet wrong
// mapping.
var ErrInvalidFanMode = errors.New("msiec: unrecognised fan_mode value")

// ErrInvalidShiftMode is returned by WritePowerProfile when the
// requested profile is not in the board's available_shift_modes set
// (#1166). Backends refuse to silently substitute on unknown values
// for the same reason ErrInvalidFanMode does: a kernel-driver change
// must surface, not be papered over.
var ErrInvalidShiftMode = errors.New("msiec: unrecognised shift_mode value")
