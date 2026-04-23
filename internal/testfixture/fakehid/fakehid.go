// Package fakehid provides an in-memory USB HID device simulator for unit tests.
// It implements usbbase.HIDLayer and usbbase.RawDevice (via Layer and DeviceHandle)
// for Bus/Handle-level tests, and usbbase.Device (via Device) plus Hub for
// Enumerate/Watch-level tests.
package fakehid

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase"
)

// ── Low-level: Layer + DeviceHandle (implements HIDLayer / RawDevice) ────────

// Layer is an in-memory HID bus. Add devices with AddDevice before use.
// It implements usbbase.HIDLayer.
type Layer struct {
	mu      sync.Mutex
	devices []usbbase.DeviceInfo
	handles map[string]*DeviceHandle
}

// New returns an empty Layer with no registered devices.
func New() *Layer {
	return &Layer{handles: make(map[string]*DeviceHandle)}
}

// AddDevice registers a device so Enumerate returns it and OpenPath can open it.
// The same DeviceHandle is returned for every Open of that path.
func (l *Layer) AddDevice(d usbbase.DeviceInfo, h *DeviceHandle) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.devices = append(l.devices, d)
	l.handles[d.Path] = h
}

// Enumerate implements usbbase.HIDLayer.
func (l *Layer) Enumerate() ([]usbbase.DeviceInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]usbbase.DeviceInfo, len(l.devices))
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
	if d.closed {
		return 0, fmt.Errorf("fakehid: get_feature on closed device")
	}
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
	if d.closed {
		return 0, fmt.Errorf("fakehid: send_feature on closed device")
	}
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
	if d.closed {
		return 0, fmt.Errorf("fakehid: read on closed device")
	}
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
	if d.closed {
		return 0, fmt.Errorf("fakehid: write on closed device")
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	d.writeCapture = append(d.writeCapture, cp)
	return len(p), nil
}

// compile-time proof that *Layer implements usbbase.HIDLayer.
var _ usbbase.HIDLayer = (*Layer)(nil)

// compile-time proof that *DeviceHandle implements usbbase.RawDevice.
var _ usbbase.RawDevice = (*DeviceHandle)(nil)

// ── High-level: Device + Hub (implements usbbase.Device / event source) ──────

// Device is an in-memory USB HID device that implements usbbase.Device.
// Writes can be scripted by setting OnWrite; the function's return value
// is queued as the response to the next Read call.
type Device struct {
	mu      sync.Mutex
	vid     uint16
	pid     uint16
	serial  string
	iface   int
	onWrite func([]byte) []byte // nil means no response
	readBuf []byte
	closed  bool
}

// NewDevice returns a Device with the given VID, PID, serial, and interface
// number. Pass -1 for iface if the device has no specific interface binding.
func NewDevice(vid, pid uint16, serial string, iface int) *Device {
	return &Device{vid: vid, pid: pid, serial: serial, iface: iface}
}

// SetOnWrite registers a scripted response function. Each call to Write
// invokes fn with the written bytes; the returned slice is queued for the
// next Read. Passing nil disables scripted responses.
func (d *Device) SetOnWrite(fn func([]byte) []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onWrite = fn
}

// VendorID implements usbbase.Device.
func (d *Device) VendorID() uint16 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.vid
}

// ProductID implements usbbase.Device.
func (d *Device) ProductID() uint16 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pid
}

// SerialNumber implements usbbase.Device.
func (d *Device) SerialNumber() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.serial
}

// Iface returns the device's interface number used for Matcher.Interface
// filtering. This is not part of usbbase.Device; it is needed by Hub.
func (d *Device) Iface() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.iface
}

// Read implements usbbase.Device. Returns the next scripted response queued
// by OnWrite, or an error if the buffer is empty.
func (d *Device) Read(buf []byte, _ time.Duration) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("fakehid: read on closed device")
	}
	if len(d.readBuf) == 0 {
		return 0, fmt.Errorf("fakehid: read buffer empty")
	}
	n := copy(buf, d.readBuf)
	d.readBuf = d.readBuf[n:]
	return n, nil
}

// Write implements usbbase.Device. If OnWrite is set the response is queued
// for the next Read call.
func (d *Device) Write(buf []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("fakehid: write on closed device")
	}
	if d.onWrite != nil {
		resp := d.onWrite(buf)
		if len(resp) > 0 {
			d.readBuf = append(d.readBuf, resp...)
		}
	}
	return len(buf), nil
}

// Close implements usbbase.Device.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// IsClosed reports whether Close has been called.
func (d *Device) IsClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// compile-time proof that *Device implements usbbase.Device.
var _ usbbase.Device = (*Device)(nil)

// Hub is an in-memory USB HID bus for Enumerate/Watch tests. Add and remove
// Devices to simulate hotplug events; Watch subscribers receive the events.
type Hub struct {
	mu   sync.Mutex
	devs []*Device
	subs []chan usbbase.Event
}

// NewHub returns an empty Hub with no registered Devices.
func NewHub() *Hub {
	return &Hub{}
}

// Add registers d and broadcasts an Add event to all Watch subscribers.
func (h *Hub) Add(d *Device) {
	h.mu.Lock()
	h.devs = append(h.devs, d)
	subs := h.copySubs()
	h.mu.Unlock()

	ev := usbbase.Event{Kind: usbbase.Add, Device: d}
	for _, sub := range subs {
		select {
		case sub <- ev:
		default: // drop if subscriber buffer is full
		}
	}
}

// Remove deregisters the first Device with the given serial number and
// broadcasts a Remove event. No-op if no such device is registered.
func (h *Hub) Remove(serial string) {
	h.mu.Lock()
	var removed *Device
	var remaining []*Device
	for _, d := range h.devs {
		if removed == nil && d.SerialNumber() == serial {
			removed = d
		} else {
			remaining = append(remaining, d)
		}
	}
	h.devs = remaining
	subs := h.copySubs()
	h.mu.Unlock()

	if removed == nil {
		return
	}
	ev := usbbase.Event{Kind: usbbase.Remove, Device: removed}
	for _, sub := range subs {
		select {
		case sub <- ev:
		default:
		}
	}
}

// Enumerate returns all registered Devices matching any Matcher.
// An empty matchers slice returns nothing.
func (h *Hub) Enumerate(matchers []usbbase.Matcher) ([]usbbase.Device, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(matchers) == 0 {
		return nil, nil
	}
	var out []usbbase.Device
	for _, d := range h.devs {
		if usbbase.MatchesAny(matchers, d.VendorID(), d.ProductID(), d.Iface()) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Watch streams hotplug events to the returned channel, filtered by matchers.
// If matchers is empty, all events are forwarded. The channel is closed when
// ctx is cancelled.
func (h *Hub) Watch(ctx context.Context, matchers []usbbase.Matcher) (<-chan usbbase.Event, error) {
	raw := make(chan usbbase.Event, 16)
	out := make(chan usbbase.Event, 16)

	h.mu.Lock()
	h.subs = append(h.subs, raw)
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			for i, s := range h.subs {
				if s == raw {
					h.subs = append(h.subs[:i], h.subs[i+1:]...)
					break
				}
			}
			h.mu.Unlock()
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-raw:
				if !ok {
					return
				}
				d, ok2 := ev.Device.(*Device)
				if !ok2 {
					continue
				}
				if len(matchers) > 0 && !usbbase.MatchesAny(matchers, d.VendorID(), d.ProductID(), d.Iface()) {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// copySubs returns a snapshot of h.subs. Caller must hold h.mu.
func (h *Hub) copySubs() []chan usbbase.Event {
	if len(h.subs) == 0 {
		return nil
	}
	cp := make([]chan usbbase.Event, len(h.subs))
	copy(cp, h.subs)
	return cp
}
