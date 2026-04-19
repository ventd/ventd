// Package fakecrosec provides a deterministic Chrome EC command dispatcher
// for unit tests.  It implements crosec.SendFunc without requiring
// /dev/cros_ec or kernel ioctl support.
package fakecrosec

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

// EC command numbers used by canned defaults.
const (
	ecCmdHello         uint32 = 0x0001
	ecCmdGetVersion    uint32 = 0x0002
	ecCmdPWMSetFanDuty uint32 = 0x0024
)

// Options configures the behaviour of the fake CrOS EC device.
type Options struct {
	// CommandResponses maps EC command codes to fixed response bytes.
	// Overrides the built-in defaults for the same command.
	CommandResponses map[uint32][]byte

	// FailOn maps EC command codes to the error returned for that command.
	// Overridden by CommandResponses for the same code.
	FailOn map[uint32]error

	// LockoutAfter causes all Send calls after the Nth to return EPERM,
	// modelling EC hardware lockout.  Zero disables lockout.
	LockoutAfter int
}

// Handler is a function that handles a single EC command.
// out is the host→EC payload; the return value is the EC→host response.
type Handler func(cmd, ver uint32, out []byte) ([]byte, error)

// Fake is a configurable in-process CrOS EC command dispatcher.
// All state is guarded by an internal mutex so concurrent test goroutines
// are safe.
//
// To connect a Fake to the CrOS EC backend pass f.Send as the SendFunc:
//
//	b := crosec.NewBackendForTest(nil, f.Send)
type Fake struct {
	t        *testing.T
	dir      string
	mu       sync.Mutex
	handlers map[uint32]Handler

	lockoutAfter int
	callCount    int

	// writes records duty-percent values delivered to the default
	// EC_CMD_PWM_SET_FAN_DUTY handler (populated only when non-nil opts).
	writes []uint32
}

// New creates a Fake CrOS EC fixture registered with t.Cleanup().
//
// nil opts: no default handlers are pre-registered; the caller wires
// everything via Handle.  Non-nil opts: applies CommandResponses, FailOn,
// LockoutAfter, and pre-registers built-in defaults (EC_CMD_HELLO,
// EC_CMD_GET_VERSION, EC_CMD_PWM_SET_FAN_DUTY) for any command code not
// already covered by opts.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	dir := t.TempDir()
	devPath := filepath.Join(dir, "cros_ec")
	if err := os.WriteFile(devPath, nil, 0600); err != nil {
		t.Fatalf("fakecrosec: create device placeholder %s: %v", devPath, err)
	}

	f := &Fake{
		t:        t,
		dir:      dir,
		handlers: make(map[uint32]Handler),
	}

	if opts == nil {
		t.Cleanup(func() {
			// t.TempDir() owns dir cleanup; nothing else to release.
		})
		return f
	}

	f.lockoutAfter = opts.LockoutAfter

	// FailOn is lowest priority.
	for cmd, err := range opts.FailOn {
		captured := err
		f.handlers[cmd] = func(_, _ uint32, _ []byte) ([]byte, error) {
			return nil, captured
		}
	}

	// CommandResponses override FailOn.
	for cmd, resp := range opts.CommandResponses {
		captured := make([]byte, len(resp))
		copy(captured, resp)
		f.handlers[cmd] = func(_, _ uint32, _ []byte) ([]byte, error) {
			return captured, nil
		}
	}

	// Built-in defaults fill any remaining slots.
	if _, ok := f.handlers[ecCmdHello]; !ok {
		f.handlers[ecCmdHello] = HelloHandler()
	}
	if _, ok := f.handlers[ecCmdGetVersion]; !ok {
		f.handlers[ecCmdGetVersion] = GetVersionHandler()
	}
	if _, ok := f.handlers[ecCmdPWMSetFanDuty]; !ok {
		f.handlers[ecCmdPWMSetFanDuty] = SetDutyHandler(func(pct uint32) {
			f.mu.Lock()
			f.writes = append(f.writes, pct)
			f.mu.Unlock()
		})
	}

	t.Cleanup(func() {
		// t.TempDir() owns dir cleanup; nothing else to release.
	})
	return f
}

// DevicePath returns the placeholder filesystem path corresponding to
// /dev/cros_ec.  The crosec backend injects control via SendFunc (see
// export_test.go:NewBackendForTest); this path documents where the device
// would live if a device-path seam were added.
func (f *Fake) DevicePath() string {
	return filepath.Join(f.dir, "cros_ec")
}

// Handle registers h as the handler for EC command cmd.
// Overwrites any previous handler for the same cmd.
func (f *Fake) Handle(cmd uint32, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[cmd] = h
}

// Writes returns a snapshot of duty-percent values recorded by the default
// EC_CMD_PWM_SET_FAN_DUTY handler.  Only populated when New is called with
// non-nil opts and cmd 0x0024 is not overridden by CommandResponses or FailOn.
func (f *Fake) Writes() []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint32, len(f.writes))
	copy(out, f.writes)
	return out
}

// Send dispatches the EC command, filling in with the response bytes.
// Returns EPERM after LockoutAfter calls (when opts.LockoutAfter > 0).
// Returns an error if no handler is registered for cmd.
// Implements crosec.SendFunc.
func (f *Fake) Send(cmd, ver uint32, out, in []byte) error {
	f.mu.Lock()
	if f.lockoutAfter > 0 {
		f.callCount++
		if f.callCount > f.lockoutAfter {
			f.mu.Unlock()
			return syscall.EPERM
		}
	}
	h := f.handlers[cmd]
	f.mu.Unlock()

	if h == nil {
		return fmt.Errorf("fakecrosec: no handler for cmd 0x%04x", cmd)
	}
	resp, err := h(cmd, ver, out)
	if err != nil {
		return err
	}
	if len(in) > 0 {
		n := copy(in, resp)
		for i := n; i < len(in); i++ {
			in[i] = 0
		}
	}
	return nil
}

// --- Canned handlers --------------------------------------------------------

// HelloHandler returns a Handler for EC_CMD_HELLO (0x0001) that replies
// with in_data + 0x01020304.
func HelloHandler() Handler {
	return func(cmd, ver uint32, out []byte) ([]byte, error) {
		if len(out) < 4 {
			return nil, fmt.Errorf("fakecrosec: HELLO: short out payload (%d bytes)", len(out))
		}
		inData := binary.LittleEndian.Uint32(out)
		resp := make([]byte, 4)
		binary.LittleEndian.PutUint32(resp, inData+0x01020304)
		return resp, nil
	}
}

// GetVersionHandler returns a Handler for EC_CMD_GET_VERSION (0x0002) that
// replies with a canned firmware version string.
func GetVersionHandler() Handler {
	return func(_, _ uint32, _ []byte) ([]byte, error) {
		return []byte("fakecrosec-v1.0\x00"), nil
	}
}

// RPMHandler returns a Handler for EC_CMD_PWM_GET_FAN_TARGET_RPM (0x0020)
// that always replies with the given rpm value.
func RPMHandler(rpm uint32) Handler {
	return func(_, _ uint32, _ []byte) ([]byte, error) {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, rpm)
		return b, nil
	}
}

// SetDutyHandler returns a Handler for EC_CMD_PWM_SET_FAN_DUTY (0x0024)
// that records the last-written percent value via the provided setter.
func SetDutyHandler(setPercent func(uint32)) Handler {
	return func(cmd, ver uint32, out []byte) ([]byte, error) {
		if len(out) < 4 {
			return nil, fmt.Errorf("fakecrosec: SET_FAN_DUTY: short payload (%d bytes)", len(out))
		}
		setPercent(binary.LittleEndian.Uint32(out))
		return nil, nil
	}
}

// AutoFanCtrlHandler returns a Handler for EC_CMD_THERMAL_AUTO_FAN_CTRL (0x0052)
// that invokes onRestore when called.
func AutoFanCtrlHandler(onRestore func()) Handler {
	return func(_, _ uint32, _ []byte) ([]byte, error) {
		onRestore()
		return nil, nil
	}
}

// ErrorHandler returns a Handler that always returns the given error.
func ErrorHandler(err error) Handler {
	return func(_, _ uint32, _ []byte) ([]byte, error) {
		return nil, err
	}
}
