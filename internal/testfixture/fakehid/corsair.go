// Package fakehid — corsair.go provides a scripted Commander Core / Core XT /
// Commander ST response fixture for unit tests. It implements the hidIO surface
// that the corsair package depends on entirely in memory, so tests never touch
// /dev/hidraw*.
//
// Protocol references:
//   - specs/spec-02-framing-review.md
//   - liquidctl/docs/developer/protocol/commander_core.md
package fakehid

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
)

// ── corsair frame constants (mirrors corsair/protocol.go) ─────────────────────

const (
	corsairFrameSize = 1024
	corsairRespID    = 0x00
	corsairWriteCmd  = 0x08

	corsairCmdWake          = 0x01
	corsairCmdGetFirmware   = 0x02
	corsairCmdCloseEndpoint = 0x05
	corsairCmdWrite         = 0x06
	corsairCmdRead          = 0x08
	corsairCmdOpenEndpoint  = 0x0d

	corsairModeGetTemps       = 0x10
	corsairModeGetSpeeds      = 0x17
	corsairModeHWFixedPercent = 0x1a
	corsairModeHWCurve        = 0x1f
)

// CorsairConfig configures the scripted device.
type CorsairConfig struct {
	// VID / PID match a knownDevices entry in the corsair package.
	VID uint16
	PID uint16

	// Firmware version reported in response to cmd_get_firmware.
	FirmwareMajor uint8
	FirmwareMinor uint8
	FirmwarePatch uint8

	// Speeds[i] is the RPM returned for channel i (0 = pump or first fan).
	Speeds []uint16

	// Temps[i] is coolant temperature in hundredths of Celsius.
	Temps []uint16

	// HasPump marks whether channel 0 is a pump.
	HasPump bool

	// HandleDelay, if non-zero, is applied inside handleCommand to simulate
	// slow HID hardware. Used by SerialisedWrites tests.
	HandleDelay time.Duration
}

// CorsairDevice is a scripted in-memory Commander Core/XT/ST device.
type CorsairDevice struct {
	mu  sync.Mutex
	cfg CorsairConfig

	// currentMode tracks the last opened endpoint mode.
	currentMode byte

	// DutiesWritten captures (channel, duty) pairs sent by cmd_write in fixed-percent mode.
	DutiesWritten []DutyCapture

	// RestoresReceived tracks channels that received a cmd_write in hw-curve mode.
	RestoresReceived []int
}

// DutyCapture records one channel duty write.
type DutyCapture struct {
	Channel int
	Duty    uint8
}

// NewCorsairDevice returns a scripted Commander Core/XT/ST fixture.
func NewCorsairDevice(cfg CorsairConfig) *CorsairDevice {
	return &CorsairDevice{cfg: cfg}
}

// handleCommand parses a 1024-byte outgoing frame and returns a 1024-byte
// response mimicking Commander Core firmware behaviour.
// Caller holds no lock; this method acquires d.mu internally.
func (d *CorsairDevice) handleCommand(frame []byte) []byte {
	if d.cfg.HandleDelay > 0 {
		time.Sleep(d.cfg.HandleDelay)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	resp := make([]byte, corsairFrameSize)
	resp[0] = corsairRespID

	if len(frame) < 3 {
		return resp
	}
	if frame[0] != corsairRespID || frame[1] != corsairWriteCmd {
		return resp
	}

	opcode := frame[2]
	switch opcode {
	case corsairCmdWake:
		resp[1] = 0x00

	case corsairCmdGetFirmware:
		resp[1] = 0x00
		resp[2] = d.cfg.FirmwareMajor
		resp[3] = d.cfg.FirmwareMinor
		resp[4] = d.cfg.FirmwarePatch

	case corsairCmdOpenEndpoint:
		if len(frame) > 3 {
			d.currentMode = frame[3]
		}
		resp[1] = 0x00

	case corsairCmdCloseEndpoint:
		d.currentMode = 0
		resp[1] = 0x00

	case corsairCmdRead:
		switch d.currentMode {
		case corsairModeGetSpeeds:
			count := len(d.cfg.Speeds)
			resp[1] = byte(count)
			for i, spd := range d.cfg.Speeds {
				resp[2+i*2] = byte(spd)
				resp[3+i*2] = byte(spd >> 8)
			}
		case corsairModeGetTemps:
			count := len(d.cfg.Temps)
			resp[1] = byte(count)
			for i, tmp := range d.cfg.Temps {
				resp[2+i*2] = byte(tmp)
				resp[3+i*2] = byte(tmp >> 8)
			}
		}

	case corsairCmdWrite:
		// frame[3]=channel, frame[4]=duty_lo, frame[5]=duty_hi
		if len(frame) >= 5 {
			ch := int(frame[3])
			duty := frame[4] // LE uint16 low byte = 0–255 duty
			switch d.currentMode {
			case corsairModeHWFixedPercent:
				d.DutiesWritten = append(d.DutiesWritten, DutyCapture{Channel: ch, Duty: duty})
			case corsairModeHWCurve:
				d.RestoresReceived = append(d.RestoresReceived, ch)
			}
		}
		resp[1] = 0x00
	}

	return resp
}

// ── CorsairPipe — the hidIO shim used by corsair package tests ─────────────────
//
// CorsairPipe wraps CorsairDevice to satisfy the hidIO interface that
// probedDevice.hid requires. Tests inject it via probeWith's openFn.

// CorsairPipe bridges CorsairDevice to the hidIO interface.
type CorsairPipe struct {
	mu   sync.Mutex
	dev  *CorsairDevice
	resp []byte // pending response bytes for Read

	deadline time.Time

	// ConcurrencyViolated is set true if Write is called while another Write's
	// handleCommand is still executing. The corsair backend's per-device mutex
	// must prevent this (RULE-LIQUID-05).
	ConcurrencyViolated bool
	inFlight            bool

	closed bool
}

// NewCorsairPipe returns a CorsairPipe backed by dev.
func NewCorsairPipe(dev *CorsairDevice) *CorsairPipe {
	return &CorsairPipe{dev: dev}
}

// Write accepts a 1024-byte frame, drives handleCommand, and queues the response
// for the next Read. Detects concurrent callers via inFlight.
func (p *CorsairPipe) Write(frame []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, errors.New("corsairPipe: write on closed pipe")
	}
	if p.inFlight {
		p.ConcurrencyViolated = true
	}
	p.inFlight = true
	p.mu.Unlock()

	// Drive the device handler without holding p.mu so concurrent callers can
	// detect the inFlight flag collision (RULE-LIQUID-05 violation detection).
	resp := p.dev.handleCommand(frame)

	p.mu.Lock()
	p.inFlight = false
	p.resp = append(p.resp, resp...)
	p.mu.Unlock()

	return len(frame), nil
}

// Read returns the next queued response. Respects the deadline set by
// SetReadDeadline.
func (p *CorsairPipe) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errors.New("corsairPipe: read on closed pipe")
	}
	if !p.deadline.IsZero() && time.Now().After(p.deadline) {
		return 0, fmt.Errorf("%w: deadline exceeded", hidraw.ErrTimeout)
	}
	if len(p.resp) == 0 {
		return 0, fmt.Errorf("corsairPipe: no response queued")
	}
	n := copy(buf, p.resp)
	p.resp = p.resp[n:]
	return n, nil
}

// SetReadDeadline sets the deadline for subsequent Read calls.
func (p *CorsairPipe) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deadline = t
	return nil
}

// Close marks the pipe as closed. Idempotent.
func (p *CorsairPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}
