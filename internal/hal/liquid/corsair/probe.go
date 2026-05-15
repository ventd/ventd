package corsair

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
)

// RegisterAll probes all Corsair Commander devices visible on the system and
// registers each with the HAL under "corsair:<path>". Probe failures and
// missing hardware are logged and skipped — not fatal.
//
// v0.6.1 dropped the firmware allowlist + `--unsafe-corsair-writes` gate per
// the `feedback-dont-default-writes-off` directive — those were "ship code,
// wait for HIL evidence" gates. The genuine safety primitives stay: pump-
// minimum floor (RULE-LIQUID-01), USB-disconnect pump floor (RULE-LIQUID-02),
// restore-on-panic (RULE-LIQUID-04), per-device serialised writes
// (RULE-LIQUID-05), and conflicting-kernel-driver yield (RULE-LIQUID-07).
func RegisterAll(logger *slog.Logger, opts ProbeOptions) {
	m := DeviceMatcher()
	infos, err := hidraw.Enumerate([]hidraw.Matcher{{
		VendorID:   m.VendorID,
		ProductIDs: m.ProductIDs,
	}})
	if err != nil {
		logger.Warn("corsair: enumerate failed", "err", err)
		return
	}
	for _, info := range infos {
		b, err := Probe(info.Path, opts)
		if err != nil {
			logger.Warn("corsair: probe skipped", "path", info.Path, "err", err)
			continue
		}
		hal.Register(BackendName+":"+info.Path, b)
	}
}

// ProbeOptions carries runtime flags that affect how a Corsair device is probed.
type ProbeOptions struct {
	// PumpMinimum is the minimum duty cycle for pump channels (RULE-LIQUID-01).
	// Defaults to defaultPumpMinimum when zero.
	PumpMinimum uint8
}

// defaultPumpMinimum is the default minimum duty cycle for pump channels.
// Config-overridable per device but cannot be set to zero.
const defaultPumpMinimum uint8 = 50

// probedDevice is the concrete HID state for a Corsair device. Holds the
// open HID handle and per-device protocol state.
//
// RULE-LIQUID-05: mu serialises all HID command transfers per device.
type probedDevice struct {
	hid     hidIO // I/O surface; *hidraw.Device in production, CorsairPipe in tests
	info    hidraw.DeviceInfo
	fw      firmwareVersion
	hasPump bool
	mu      sync.Mutex
}

// openResult carries the result of the openFn injected into probeWith.
type openResult struct {
	hid  hidIO
	info hidraw.DeviceInfo
}

// Probe opens the hidraw device at path, runs the Commander Core firmware
// handshake, and returns a FanBackend adapter. The returned backend is
// always writable; the pump-minimum floor, USB-disconnect pump floor,
// restore-on-panic, serialised writes, and conflicting-kernel-driver yield
// are enforced by the corsairBackend regardless of firmware version.
//
// RULE-LIQUID-07: CheckAndUnbind is called before opening the hidraw device.
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

	if res.info.VendorID != corsairVID {
		_ = res.hid.Close()
		return nil, fmt.Errorf("corsair probe %s: VID 0x%04x is not Corsair (want 0x%04x)", path, res.info.VendorID, corsairVID)
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

	channels := buildChannels(entry)

	return &corsairBackend{
		inner:    pd,
		writable: true,
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
