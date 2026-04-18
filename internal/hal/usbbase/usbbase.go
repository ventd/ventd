// Package usbbase provides a shared USB HID primitive layer used by USB-backed
// HAL backends (LIQUID, CROSEC). It wraps github.com/sstallion/go-hid behind
// an interface so backends can be tested with fakehid without real hardware.
//
// Production builds require CGO (the hidraw kernel interface is a C binding).
// Use New() in daemon code and NewWithLayer() in tests.
package usbbase

import (
	"fmt"
	"sync"
	"time"
)

// Device describes a USB HID device visible to the system.
type Device struct {
	VendorID     uint16
	ProductID    uint16
	Path         string
	Manufacturer string
	Product      string
	Serial       string
}

// RawDevice is the minimal per-device I/O surface required by usbbase.
// *hid.Device (go-hid) satisfies this interface when CGO is available;
// fakehid.DeviceHandle satisfies it in tests.
type RawDevice interface {
	Close() error
	GetFeatureReport(p []byte) (int, error)
	SendFeatureReport(p []byte) (int, error)
	ReadWithTimeout(p []byte, timeout time.Duration) (int, error)
	Write(p []byte) (int, error)
}

// HIDLayer is the discovery and open surface injected into Bus.
// The CGO implementation uses go-hid; tests inject fakehid.Layer.
type HIDLayer interface {
	Enumerate() ([]Device, error)
	OpenPath(path string) (RawDevice, error)
}

// Bus is the USB HID access surface. Obtain one via New() (production) or
// NewWithLayer() (tests / CGO-off environments).
type Bus struct {
	layer HIDLayer
}

// NewWithLayer returns a Bus backed by the given HIDLayer. Use this in tests
// by passing a *fakehid.Layer.
func NewWithLayer(l HIDLayer) *Bus {
	return &Bus{layer: l}
}

// Enumerate returns all HID devices visible to the system.
func (b *Bus) Enumerate() ([]Device, error) {
	return b.layer.Enumerate()
}

// Open opens the HID device at path and returns a Handle. The caller must
// call Handle.Close() when done.
func (b *Bus) Open(path string) (*Handle, error) {
	raw, err := b.layer.OpenPath(path)
	if err != nil {
		return nil, err
	}
	return &Handle{raw: raw}, nil
}

// Handle is an open USB HID device. All methods are safe for concurrent
// use: per-handle I/O is serialised by an internal mutex. Callers should
// still avoid holding their own lock across Handle method calls to prevent
// lock ordering issues.
type Handle struct {
	mu     sync.Mutex
	raw    RawDevice
	closed bool
}

// Close closes the handle. Subsequent calls are no-ops (idempotent).
func (h *Handle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	return h.raw.Close()
}

// GetFeature retrieves a feature report from the device. buf must be at least
// 1 byte; buf[0] is overwritten with reportID before the call.
func (h *Handle) GetFeature(reportID byte, buf []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("usbbase: get_feature on closed handle")
	}
	if len(buf) == 0 {
		return 0, fmt.Errorf("usbbase: GetFeature: buf must not be empty")
	}
	buf[0] = reportID
	return h.raw.GetFeatureReport(buf)
}

// SendFeature sends a feature report to the device. buf[0] must contain the
// report ID.
func (h *Handle) SendFeature(buf []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("usbbase: send_feature on closed handle")
	}
	_, err := h.raw.SendFeatureReport(buf)
	return err
}

// Read reads an input report with the given timeout.
func (h *Handle) Read(buf []byte, timeout time.Duration) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("usbbase: read on closed handle")
	}
	return h.raw.ReadWithTimeout(buf, timeout)
}

// Write sends an output report to the device.
func (h *Handle) Write(buf []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("usbbase: write on closed handle")
	}
	_, err := h.raw.Write(buf)
	return err
}
