package corsair

import (
	"fmt"
	"sync"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
)

// ProbeOptions carries runtime flags that affect how a Corsair device is probed.
type ProbeOptions struct {
	// UnsafeCorsairWrites enables write commands when set to true AND the
	// device firmware is on the allow-list (empty in v0.4.0).
	// --unsafe-corsair-writes flag wires into this field.
	UnsafeCorsairWrites bool

	// PumpMinimum is the minimum duty cycle for pump channels (RULE-LIQUID-01).
	// Defaults to defaultPumpMinimum when zero.
	PumpMinimum uint8
}

// defaultPumpMinimum is the default minimum duty cycle for pump channels.
// Config-overridable per device but cannot be set to zero.
const defaultPumpMinimum uint8 = 50

// probedDevice is the concrete HID state shared by liveDevice and unknownFirmwareDevice.
// It holds the open HID handle and per-device protocol state.
//
// RULE-LIQUID-05: mu serialises all HID command transfers per device.
type probedDevice struct {
	hid     hidIO // I/O surface; *hidraw.Device in production, CorsairPipe in tests
	info    hidraw.DeviceInfo
	fw      firmwareVersion
	hasPump bool
	mu      sync.Mutex
}

// liveDevice wraps probedDevice for devices with an allow-listed firmware.
// Writable when ProbeOptions.UnsafeCorsairWrites is true AND firmware is listed.
// (v0.4.0 allow-list is empty, so no real device ever becomes liveDevice.)
type liveDevice struct{ *probedDevice }

// unknownFirmwareDevice wraps probedDevice for devices whose firmware is not
// on the allow-list. Read operations work; write commands return
// ErrReadOnlyUnvalidatedFirmware at the adapter boundary.
//
// RULE-LIQUID-03: unknown firmware is read-only.
type unknownFirmwareDevice struct{ *probedDevice }

// probeClass is the internal compile-time discriminant that drives corsairBackend
// construction. The concrete type (liveDevice vs unknownFirmwareDevice) encodes
// write access so it cannot be confused with a runtime boolean flag.
//
// RULE-LIQUID-06: writable() returns true only for liveDevice.
type probeClass interface {
	writable() bool
}

func (d liveDevice) writable() bool            { return true }
func (d unknownFirmwareDevice) writable() bool { return false }

// firmwareAllowList is the set of firmware versions approved for write access.
// Empty for v0.4.0 — every real device probes as unknownFirmwareDevice.
// RULE-LIQUID-06: writable mode requires BOTH the unsafe flag AND an
// allow-listed firmware. Because the list is empty in v0.4.0, no device
// ever gains write access regardless of the flag.
var firmwareAllowList = map[firmwareVersion]bool{}

// openResult carries the result of the openFn injected into probeWith.
type openResult struct {
	hid  hidIO
	info hidraw.DeviceInfo
}

// Probe opens the hidraw device at path, runs the Commander Core firmware
// handshake, and returns a FanBackend adapter.
//
// Decision tree:
//
//	(a) CheckAndUnbind(path) fails → return ErrKernelDriverOwnsDevice, no adapter.
//	(b) firmware NOT on allow-list OR UnsafeCorsairWrites==false → unknownFirmwareDevice (read-only)
//	(c) both (a) succeeded AND firmware on allow-list AND flag set → liveDevice (writable)
//
// RULE-LIQUID-07: CheckAndUnbind is called before opening the hidraw device.
// RULE-LIQUID-06: writable requires flag AND allow-listed firmware.
func Probe(path string, opts ProbeOptions) (hal.FanBackend, error) {
	return probeWith(path, opts, nil)
}

// probeWith is the testable inner implementation. When openFn is nil, the
// real hidraw.Open is used. Tests inject a CorsairPipe via a custom openFn.
func probeWith(path string, opts ProbeOptions, openFn func(string) (openResult, error)) (hal.FanBackend, error) {
	// RULE-LIQUID-07: check for and attempt to unbind any kernel driver BEFORE
	// opening the hidraw device.
	if err := CheckAndUnbind(path); err != nil {
		return nil, fmt.Errorf("corsair probe %s: %w", path, err)
	}

	if openFn == nil {
		openFn = realOpen
	}
	res, err := openFn(path)
	if err != nil {
		return nil, fmt.Errorf("corsair probe %s: open: %w", path, err)
	}

	entry, known := lookupDevice(res.info.ProductID)
	if !known {
		_ = res.hid.Close()
		return nil, fmt.Errorf("corsair probe %s: PID 0x%04x not in device table", path, res.info.ProductID)
	}

	pd := &probedDevice{
		hid:     res.hid,
		info:    res.info,
		hasPump: entry.hasPump,
	}

	// Firmware handshake: wake + query firmware version.
	pd.mu.Lock()
	major, minor, patch, fwErr := func() (uint8, uint8, uint8, error) {
		if err := doWake(res.hid); err != nil {
			return 0, 0, 0, err
		}
		return doGetFirmware(res.hid)
	}()
	pd.mu.Unlock()

	if fwErr != nil {
		_ = res.hid.Close()
		return nil, fmt.Errorf("corsair probe %s: firmware handshake: %w", path, fwErr)
	}
	pd.fw = firmwareVersion{major: major, minor: minor, patch: patch}

	pumpMin := opts.PumpMinimum
	if pumpMin == 0 {
		pumpMin = defaultPumpMinimum
	}

	// RULE-LIQUID-06: classify by compile-time type — liveDevice when both the
	// unsafe flag and an allow-listed firmware are present, unknownFirmwareDevice
	// otherwise. cls.writable() drives the corsairBackend writable field so the
	// type split is the single source of truth, not a stand-alone boolean.
	var cls probeClass
	if opts.UnsafeCorsairWrites && firmwareAllowList[pd.fw] {
		cls = liveDevice{pd}
	} else {
		cls = unknownFirmwareDevice{pd}
	}

	channels := buildChannels(entry)

	return &corsairBackend{
		inner:    pd,
		writable: cls.writable(),
		pumpMin:  pumpMin,
		channels: channels,
	}, nil
}

// realOpen opens a real hidraw device and returns an openResult.
func realOpen(path string) (openResult, error) {
	dev, err := hidraw.Open(path)
	if err != nil {
		return openResult{}, err
	}
	return openResult{hid: dev, info: dev.Info()}, nil
}

// buildChannels constructs the channel slice for an entry.
func buildChannels(entry deviceEntry) []channelEntry {
	var out []channelEntry
	if entry.hasPump {
		// Channel 0 is always the pump on Commander Core / ST.
		out = append(out, channelEntry{
			index:  0,
			role:   hal.RolePump,
			isPump: true,
		})
		// Channels 1-6 are case fans.
		for i := 1; i <= 6; i++ {
			out = append(out, channelEntry{
				index:  i,
				role:   hal.RoleCase,
				isPump: false,
			})
		}
	} else {
		// Commander Core XT: all 6 channels are case fans (indices 0-5).
		for i := 0; i < 6; i++ {
			out = append(out, channelEntry{
				index:  i,
				role:   hal.RoleCase,
				isPump: false,
			})
		}
	}
	return out
}
