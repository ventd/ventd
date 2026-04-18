// Package fakecrosec provides a deterministic Chrome EC command dispatcher
// for unit tests.  It implements crosec.SendFunc without requiring
// /dev/cros_ec or kernel ioctl support.
package fakecrosec

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
)

// Handler is a function that handles a single EC command.
// out is the host→EC payload; the return value is the EC→host response.
type Handler func(cmd, ver uint32, out []byte) ([]byte, error)

// Fake is a configurable in-process EC command dispatcher.
type Fake struct {
	t        *testing.T
	mu       sync.Mutex
	handlers map[uint32]Handler
}

// New returns a new Fake with an empty handler table.
func New(t *testing.T) *Fake {
	t.Helper()
	return &Fake{
		t:        t,
		handlers: make(map[uint32]Handler),
	}
}

// Handle registers h as the handler for EC command cmd.
// Overwrites any previous handler for the same cmd.
func (f *Fake) Handle(cmd uint32, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[cmd] = h
}

// Send dispatches the EC command, filling in with the response bytes.
// Returns an error if no handler is registered for cmd.
// Implements crosec.SendFunc.
func (f *Fake) Send(cmd, ver uint32, out, in []byte) error {
	f.mu.Lock()
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
		// Zero out any unpopulated portion of in.
		for i := n; i < len(in); i++ {
			in[i] = 0
		}
	}
	return nil
}

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

// RPMHandler returns a Handler for EC_CMD_PWM_GET_FAN_TARGET_RPM (0x0020)
// that always replies with the given rpm value.
func RPMHandler(rpm uint32) Handler {
	return func(cmd, ver uint32, out []byte) ([]byte, error) {
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
	return func(cmd, ver uint32, out []byte) ([]byte, error) {
		onRestore()
		return nil, nil
	}
}

// ErrorHandler returns a Handler that always returns the given error.
func ErrorHandler(err error) Handler {
	return func(cmd, ver uint32, out []byte) ([]byte, error) {
		return nil, err
	}
}
