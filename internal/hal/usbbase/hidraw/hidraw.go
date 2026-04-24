// Package hidraw provides a pure-Go Linux hidraw substrate for ventd USB
// backends. It enumerates /dev/hidrawN devices via sysfs, issues feature
// reports via ioctl, and watches hotplug events via NETLINK_KOBJECT_UEVENT.
//
// See specs/spec-02-hidraw.md for design rationale and invariant bindings.
// All six RULE-HIDRAW-* invariants are enforced by tests in this package.
package hidraw

import "errors"

// DeviceInfo describes a hidraw device discovered via /sys/class/hidraw/.
type DeviceInfo struct {
	Path            string // /dev/hidrawN
	VendorID        uint16
	ProductID       uint16
	InterfaceNumber int    // -1 if not available
	SerialNumber    string // "" if device does not expose one
	BusType         uint32 // BUS_USB=0x03, BUS_BLUETOOTH=0x05, BUS_VIRTUAL=0x06
}

// Matcher filters hidraw devices by vendor, product list, and USB interface number.
type Matcher struct {
	VendorID   uint16
	ProductIDs []uint16 // empty means any PID
	Interface  int      // -1 means any interface
}

func (m Matcher) matches(vid, pid uint16, iface int) bool {
	if m.VendorID != 0 && m.VendorID != vid {
		return false
	}
	if len(m.ProductIDs) > 0 {
		found := false
		for _, p := range m.ProductIDs {
			if p == pid {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if m.Interface >= 0 && iface >= 0 && m.Interface != iface {
		return false
	}
	return true
}

func matchesAny(ms []Matcher, vid, pid uint16, iface int) bool {
	for _, m := range ms {
		if m.matches(vid, pid, iface) {
			return true
		}
	}
	return false
}

// EventKind identifies the direction of a hotplug event.
type EventKind int

const (
	Add    EventKind = iota
	Remove EventKind = iota
)

// Event is a hidraw hotplug notification emitted by Watch.
// For Add events, Info is fully populated. For Remove events, only Info.Path is set.
type Event struct {
	Kind EventKind
	Info DeviceInfo
}

var (
	// ErrUnsupported is returned on non-Linux platforms.
	ErrUnsupported = errors.New("hidraw: not supported on this OS")
	// ErrReadOnly is returned by Write/SendFeature on a read-only Device.
	ErrReadOnly = errors.New("hidraw: device opened read-only")
	// ErrTimeout is returned by Read when the deadline set via SetReadDeadline passes.
	ErrTimeout = errors.New("hidraw: read deadline exceeded")
	// ErrDeviceGone is returned by Read when the device is yanked (ENODEV).
	ErrDeviceGone = errors.New("hidraw: device disconnected")
)
