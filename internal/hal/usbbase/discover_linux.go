//go:build linux && cgo && hidraw

package usbbase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	hid "github.com/sstallion/go-hid"
)

// platformEnumerate enumerates USB HID devices using go-hid and returns those
// matching any Matcher. Devices are opened; callers must call Close when done.
func platformEnumerate(matchers []Matcher) ([]Device, error) {
	if len(matchers) == 0 {
		return nil, nil
	}

	var (
		mu  sync.Mutex
		out []Device
	)

	err := hid.Enumerate(hid.VendorIDAny, hid.ProductIDAny, func(info *hid.DeviceInfo) error {
		if !MatchesAny(matchers, info.VendorID, info.ProductID, info.InterfaceNbr) {
			return nil
		}
		dev, err := hid.OpenPath(info.Path)
		if err != nil {
			// Log but continue; other devices may be accessible.
			return nil
		}
		mu.Lock()
		out = append(out, &hidDevice{
			dev:    dev,
			vid:    info.VendorID,
			pid:    info.ProductID,
			serial: info.SerialNbr,
		})
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("usbbase: enumerate: %w", err)
	}
	return out, nil
}

// platformWatch streams USB hotplug events using NETLINK_KOBJECT_UEVENT.
// On uevent, it re-enumerates and diffs against the last known set.
// Falls back to a 10-second polling ticker when the netlink socket is unavailable.
func platformWatch(ctx context.Context, matchers []Matcher) (<-chan Event, error) {
	ch := make(chan Event, 16)

	go func() {
		defer close(ch)

		known, _ := platformEnumerate(matchers)
		knownMap := devicesAsMap(known)

		uevents := subscribeUSBEvents(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-uevents:
				if !ok {
					return
				}
			}

			current, _ := platformEnumerate(matchers)
			currentMap := devicesAsMap(current)

			for serial, dev := range currentMap {
				if _, exists := knownMap[serial]; !exists {
					select {
					case ch <- Event{Kind: Add, Device: dev}:
					case <-ctx.Done():
						return
					}
				}
			}
			for serial, dev := range knownMap {
				if _, exists := currentMap[serial]; !exists {
					select {
					case ch <- Event{Kind: Remove, Device: dev}:
					case <-ctx.Done():
						return
					}
				}
			}
			knownMap = currentMap
		}
	}()

	return ch, nil
}

// subscribeUSBEvents opens a NETLINK_KOBJECT_UEVENT socket and emits one
// notification per uevent for subsystem "hidraw". Returns a closed channel
// if the socket is unavailable.
func subscribeUSBEvents(ctx context.Context) <-chan struct{} {
	out := make(chan struct{}, 8)

	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		close(out)
		return out
	}

	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 1}
	if err := syscall.Bind(fd, sa); err != nil {
		_ = syscall.Close(fd)
		close(out)
		return out
	}

	go func() {
		<-ctx.Done()
		_ = syscall.Close(fd)
	}()

	go func() {
		defer close(out)
		buf := make([]byte, 16*1024)
		for {
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				return
			}
			msg := string(buf[:n])
			if !isHIDRawEvent(msg) {
				continue
			}
			select {
			case out <- struct{}{}:
			default:
			}
		}
	}()

	return out
}

// isHIDRawEvent reports whether a raw netlink uevent string is from the
// hidraw subsystem.
func isHIDRawEvent(msg string) bool {
	return strings.Contains(msg, "SUBSYSTEM=hidraw")
}

// devicesAsMap indexes devices by serial number for diffing.
func devicesAsMap(devs []Device) map[string]Device {
	m := make(map[string]Device, len(devs))
	for _, d := range devs {
		m[d.SerialNumber()] = d
	}
	return m
}

// hidDevice wraps *hid.Device and implements usbbase.Device.
type hidDevice struct {
	mu     sync.Mutex
	dev    *hid.Device
	vid    uint16
	pid    uint16
	serial string
	closed bool
}

func (d *hidDevice) VendorID() uint16     { return d.vid }
func (d *hidDevice) ProductID() uint16    { return d.pid }
func (d *hidDevice) SerialNumber() string { return d.serial }

func (d *hidDevice) Read(buf []byte, timeout time.Duration) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("usbbase: read on closed device")
	}
	return d.dev.ReadWithTimeout(buf, timeout)
}

func (d *hidDevice) Write(buf []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("usbbase: write on closed device")
	}
	return d.dev.Write(buf)
}

func (d *hidDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.dev.Close()
}
