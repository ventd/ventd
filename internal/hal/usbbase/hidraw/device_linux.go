//go:build linux

package hidraw

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Device is an open hidraw handle. Concurrent Read from multiple goroutines
// is not supported; callers must serialise per-device I/O.
type Device struct {
	f        *os.File
	info     DeviceInfo
	readOnly bool
	mu       sync.Mutex
	closed   bool
}

// newDeviceFromFile constructs a Device from an already-open *os.File.
// Used by tests to inject a pipe pair without opening a real /dev/hidrawN.
func newDeviceFromFile(f *os.File, info DeviceInfo, readOnly bool) *Device {
	return &Device{f: f, info: info, readOnly: readOnly}
}

// Open opens /dev/hidrawN for read-write access and populates DeviceInfo
// from the HIDIOCGRAWINFO ioctl.
func Open(path string) (*Device, error) {
	return openDevice(path, false)
}

// OpenReadOnly opens /dev/hidrawN for read-only access. Write and SendFeature
// return ErrReadOnly. GetFeature and Read proceed normally.
func OpenReadOnly(path string) (*Device, error) {
	return openDevice(path, true)
}

func openDevice(path string, readOnly bool) (*Device, error) {
	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("hidraw: open %s: %w", path, err)
	}

	info, err := ioctlGetRawInfo(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("hidraw: HIDIOCGRAWINFO %s: %w", path, err)
	}

	return &Device{
		f:        f,
		readOnly: readOnly,
		info: DeviceInfo{
			Path:      path,
			VendorID:  uint16(info.vendor),
			ProductID: uint16(info.product),
			BusType:   info.bustype,
			// InterfaceNumber and SerialNumber are populated by enumerateFrom;
			// Open alone does not have access to sysfs to read them.
			InterfaceNumber: -1,
		},
	}, nil
}

// Info returns the device metadata populated at open time.
func (d *Device) Info() DeviceInfo { return d.info }

// SendFeature sends a feature report via HIDIOCSFEATURE(len).
// Returns ErrReadOnly if the device was opened with OpenReadOnly (RULE-HIDRAW-02).
func (d *Device) SendFeature(report []byte) error {
	if d.readOnly {
		return ErrReadOnly
	}
	return ioctlSendFeature(d.f, report)
}

// GetFeature retrieves a feature report via HIDIOCGFEATURE(len).
// buf[0] must be set to the report ID by the caller; the full response
// (including the report ID byte) is written into buf.
func (d *Device) GetFeature(reportID byte, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, fmt.Errorf("hidraw: GetFeature: empty buffer")
	}
	buf[0] = reportID
	return ioctlGetFeature(d.f, buf)
}

// Write sends an output report via write(2).
// Returns ErrReadOnly if the device was opened with OpenReadOnly (RULE-HIDRAW-02).
func (d *Device) Write(report []byte) (int, error) {
	if d.readOnly {
		return 0, ErrReadOnly
	}
	return d.f.Write(report)
}

// Read reads an input report, respecting the deadline set by SetReadDeadline.
// Returns ErrTimeout if the deadline passes before data arrives (RULE-HIDRAW-03).
// Returns ErrDeviceGone if the device is yanked (ENODEV).
func (d *Device) Read(buf []byte) (int, error) {
	n, err := d.f.Read(buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return 0, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		if errors.Is(err, unix.ENODEV) {
			return 0, fmt.Errorf("%w: %w", ErrDeviceGone, err)
		}
		return 0, err
	}
	return n, nil
}

// SetReadDeadline sets the read deadline for subsequent Read calls.
// A zero time clears any deadline.
func (d *Device) SetReadDeadline(t time.Time) error {
	return d.f.SetReadDeadline(t)
}

// Close closes the device. Idempotent: subsequent calls return nil (RULE-HIDRAW-04).
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.f.Close()
}

// ── ioctl helpers ─────────────────────────────────────────────────────────────

// ioctlGetRawInfo issues HIDIOCGRAWINFO to retrieve device metadata.
func ioctlGetRawInfo(f *os.File) (hidrawDevinfo, error) {
	var info hidrawDevinfo
	conn, err := f.SyscallConn()
	if err != nil {
		return hidrawDevinfo{}, err
	}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, HIDIOCGRAWINFO,
			uintptr(unsafe.Pointer(&info)))
		if errno != 0 {
			ioctlErr = errno
		}
	}); err != nil {
		return hidrawDevinfo{}, err
	}
	return info, ioctlErr
}

// ioctlSendFeature issues HIDIOCSFEATURE(len(report)) to send a feature report.
func ioctlSendFeature(f *os.File, report []byte) error {
	if len(report) == 0 {
		return fmt.Errorf("hidraw: SendFeature: empty report")
	}
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd,
			hidiocsfeature(uintptr(len(report))),
			uintptr(unsafe.Pointer(&report[0])))
		if errno != 0 {
			ioctlErr = errno
		}
	}); err != nil {
		return err
	}
	return ioctlErr
}

// ioctlGetFeature issues HIDIOCGFEATURE(len(buf)) to retrieve a feature report.
func ioctlGetFeature(f *os.File, buf []byte) (int, error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		n        int
		ioctlErr error
	)
	if err := conn.Control(func(fd uintptr) {
		r1, _, errno := unix.Syscall(unix.SYS_IOCTL, fd,
			hidiocgfeature(uintptr(len(buf))),
			uintptr(unsafe.Pointer(&buf[0])))
		if errno != 0 {
			ioctlErr = errno
		} else {
			n = int(r1)
		}
	}); err != nil {
		return 0, err
	}
	return n, ioctlErr
}
