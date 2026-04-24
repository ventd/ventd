//go:build linux

package hidraw

import (
	"errors"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// newPipeDevice creates a Device backed by the read end of an os.Pipe.
// readOnly controls whether write/sendFeature are accepted.
// Callers must close the returned write end when done to avoid goroutine leaks.
func newPipeDevice(t *testing.T, readOnly bool) (dev *Device, writeEnd *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	dev = newDeviceFromFile(r, DeviceInfo{Path: "/test/hidraw0"}, readOnly)
	return dev, w
}

// TestDevice_ReadOnlyRejectsWrite verifies RULE-HIDRAW-02:
// a Device opened read-only must return ErrReadOnly for Write and SendFeature.
func TestDevice_ReadOnlyRejectsWrite(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev, w := newPipeDevice(t, true)
	defer func() { _ = w.Close() }()

	if _, err := dev.Write([]byte{0x01}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Write on read-only: got %v, want ErrReadOnly", err)
	}
	if err := dev.SendFeature([]byte{0x00, 0x01}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("SendFeature on read-only: got %v, want ErrReadOnly", err)
	}
	// GetFeature and Read must still proceed (or return a different error,
	// but not ErrReadOnly).
	buf := make([]byte, 4)
	if _, err := dev.GetFeature(0x01, buf); errors.Is(err, ErrReadOnly) {
		t.Error("GetFeature on read-only must not return ErrReadOnly")
	}
}

// TestDevice_ReadDeadlineReturnsTimeout verifies RULE-HIDRAW-03:
// Read must return ErrTimeout when the deadline passes before data arrives.
func TestDevice_ReadDeadlineReturnsTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev, w := newPipeDevice(t, false)
	defer func() { _ = w.Close() }()

	// Set deadline in the near past to force immediate timeout.
	if err := dev.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	buf := make([]byte, 64)
	_, err := dev.Read(buf)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("Read after deadline: got %v, want ErrTimeout (wrapping os.ErrDeadlineExceeded)", err)
	}
}

// TestDevice_CloseIdempotent verifies RULE-HIDRAW-04:
// Close must be safe to call multiple times; subsequent calls return nil.
func TestDevice_CloseIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev, w := newPipeDevice(t, false)
	defer func() { _ = w.Close() }()

	if err := dev.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Errorf("second Close: got %v, want nil", err)
	}
}
