package hwmon

import (
	"log/slog"

	"github.com/ventd/ventd/internal/hal"
)

// NewBackendForTest constructs a Backend with injected sysfs write hooks so
// tests can simulate permission errors and transient failures without touching
// real sysfs files.
func NewBackendForTest(
	logger *slog.Logger,
	writePWMEnable func(pwmPath string, value int) error,
	writePWMEnablePath func(path string, value int) error,
) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:             logger,
		writePWMEnable:     writePWMEnable,
		writePWMEnablePath: writePWMEnablePath,
	}
}

// NewBackendForTestWithDuty constructs a Backend with both the
// pwm_enable hooks AND the duty-cycle write hook injected. Used by
// EBUSY-retry tests where the duty-cycle write needs to fail
// transiently (returning syscall.EBUSY) so the retry path is
// exercised. Production callers leave writeDutyFn nil and use
// NewBackend instead.
func NewBackendForTestWithDuty(
	logger *slog.Logger,
	writePWMEnable func(pwmPath string, value int) error,
	writeDutyFn func(st State, pwm uint8) error,
) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:         logger,
		writePWMEnable: writePWMEnable,
		writeDutyFn:    writeDutyFn,
	}
}

// MakeTestChannel constructs a hal.Channel with the hwmon State opaque for
// use in unit tests that exercise Write / ensureManualMode without real sysfs.
func MakeTestChannel(pwmPath string, rpmTarget bool) hal.Channel {
	caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
	if rpmTarget {
		caps = hal.CapRead | hal.CapWriteRPMTarget | hal.CapRestore
	}
	return hal.Channel{
		ID:   pwmPath,
		Role: hal.RoleUnknown,
		Caps: caps,
		Opaque: State{
			PWMPath:    pwmPath,
			RPMTarget:  rpmTarget,
			OrigEnable: -1,
		},
	}
}
