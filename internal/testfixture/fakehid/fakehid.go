// Package fakehid provides an in-memory USB HID device simulator for unit tests.
// It implements usbbase.HIDLayer and usbbase.RawDevice so tests can exercise
// usbbase consumers without real hardware or CGO.
package fakehid

import (
	"fmt"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase"
)

// Layer is an in-memory HID bus. Add devices with AddDevice before use.
// It implements usbbase.HIDLayer.
type Layer struct {
	mu      sync.Mutex
	devices []usbbase.Device
	handles map[string]*DeviceHandle
}

// New returns an empty Layer with no registered devices.
func New() *Layer {
	return &Layer{handles: make(map[string]*DeviceHandle)}
}

// AddDevice registers a device so Enumerate returns it and OpenPath can open it.
// The same DeviceHandle is returned for every Open of that path.
func (l *Layer) AddDevice(d usbbase.Device, h *DeviceHandle) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.devices = append(l.devices, d)
	l.handles[d.Path] = h
}

// Enumerate implements usbbase.HIDLayer.
func (l *Layer) Enumerate() ([]usbbase.Device, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]usbbase.Device, len(l.devices))
	copy(out, l.devices)
	return out, nil
}

// OpenPath implements usbbase.HIDLayer.
func (l *Layer) OpenPath(path string) (usbbase.RawDevice, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.handles[path]
	if !ok {
		return nil, fmt.Errorf("fakehid: no device at path %q", path)
	}
	return h, nil
}

// DeviceHandle simulates a single open HID device.
// It supports script-driven read queues, feature report maps, and write capture.
// It implements usbbase.RawDevice.
type DeviceHandle struct {
	mu           sync.Mutex
	readQueue    [][]byte        // data returned by successive ReadWithTimeout calls
	writeCapture [][]byte        // data written via Write or SendFeatureReport
	featureResp  map[byte][]byte // feature report data keyed by report ID
	closed       bool
}

// NewDeviceHandle returns a DeviceHandle with no queued data.
func NewDeviceHandle() *DeviceHandle {
	return &DeviceHandle{featureResp: make(map[byte][]byte)}
}

// QueueRead enqueues a payload to be returned by the next ReadWithTimeout call.
// Multiple calls queue multiple reads in FIFO order.
func (d *DeviceHandle) QueueRead(data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	d.readQueue = append(d.readQueue, cp)
}

// SetFeatureReport registers the payload returned by GetFeatureReport for a
// given report ID. The first byte of the returned slice is set to reportID.
func (d *DeviceHandle) SetFeatureReport(reportID byte, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	d.featureResp[reportID] = cp
}

// Written returns a snapshot of all payloads captured by Write and
// SendFeatureReport in call order.
func (d *DeviceHandle) Written() [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]byte, len(d.writeCapture))
	for i, b := range d.writeCapture {
		cp := make([]byte, len(b))
		copy(cp, b)
		out[i] = cp
	}
	return out
}

// IsClosed reports whether Close has been called on this handle.
func (d *DeviceHandle) IsClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// Close implements usbbase.RawDevice.
func (d *DeviceHandle) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// GetFeatureReport implements usbbase.RawDevice.
// p[0] is the report ID; the registered payload is copied into p.
func (d *DeviceHandle) GetFeatureReport(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	reportID := p[0]
	data, ok := d.featureResp[reportID]
	if !ok {
		return 0, fmt.Errorf("fakehid: no feature report registered for id %d", reportID)
	}
	n := copy(p, data)
	return n, nil
}

// SendFeatureReport implements usbbase.RawDevice.
// The payload is appended to the write capture log.
func (d *DeviceHandle) SendFeatureReport(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	d.writeCapture = append(d.writeCapture, cp)
	return len(p), nil
}

// ReadWithTimeout implements usbbase.RawDevice.
// Returns the next queued payload, or an error if the queue is empty.
// The timeout parameter is accepted but ignored (no real blocking).
func (d *DeviceHandle) ReadWithTimeout(p []byte, _ time.Duration) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.readQueue) == 0 {
		return 0, fmt.Errorf("fakehid: read queue empty")
	}
	data := d.readQueue[0]
	d.readQueue = d.readQueue[1:]
	n := copy(p, data)
	return n, nil
}

// Write implements usbbase.RawDevice.
// The payload is appended to the write capture log.
func (d *DeviceHandle) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	d.writeCapture = append(d.writeCapture, cp)
	return len(p), nil
}

// compile-time proof that *Layer implements usbbase.HIDLayer.
var _ usbbase.HIDLayer = (*Layer)(nil)

// compile-time proof that *DeviceHandle implements usbbase.RawDevice.
var _ usbbase.RawDevice = (*DeviceHandle)(nil)
