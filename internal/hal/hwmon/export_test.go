package hwmon

import (
	"log/slog"
	"time"

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

// SetClockForTest overrides the EBUSY rate-tracking clock so a test
// can drive the rolling window deterministically. nil resets to the
// production wall clock (time.Now). RULE-HWMON-EBUSY-RATE-OBSERVABILITY.
func (b *Backend) SetClockForTest(nowFn func() time.Time) {
	b.nowFn = nowFn
}

// NewBackendForModeTest constructs a Backend with the pwm_enable /
// duty writes stubbed to no-ops and the pwm*_mode read/write seams
// injected, so a test can observe assertResolvedMode + ModeHealer
// behaviour without real sysfs. (#759.)
func NewBackendForModeTest(
	logger *slog.Logger,
	readMode func(pwmPath string) (int, error),
	writeMode func(pwmPath string, mode int) error,
) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:         logger,
		writePWMEnable: func(string, int) error { return nil },
		writeDutyFn:    func(State, uint8) error { return nil },
		readPWMMode:    readMode,
		writePWMMode:   writeMode,
	}
}

// MakeTestChannelWithMode is MakeTestChannel with a ResolvedMode set on
// the opaque State so ensureManualMode's mode re-assertion fires. (#759.)
func MakeTestChannelWithMode(pwmPath string, resolvedMode *int) hal.Channel {
	ch := MakeTestChannel(pwmPath, false)
	st := ch.Opaque.(State)
	st.ResolvedMode = resolvedMode
	ch.Opaque = st
	return ch
}

// ModePtr returns a pointer to m for building ResolvedMode in tests.
func ModePtr(m int) *int { return &m }

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
