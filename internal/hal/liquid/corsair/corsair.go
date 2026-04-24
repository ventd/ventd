// Package corsair implements the ventd HAL backend for Corsair Commander Core,
// Commander Core XT, and Commander ST USB AIO liquid-cooler controllers.
//
// See specs/spec-02-corsair-aio.md and specs/spec-02-amendment.md for design
// rationale. Safety invariants are defined in .claude/rules/liquid-safety.md
// and enforced by tests in safety_test.go.
package corsair

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/liquid"
)

// BackendName is the short stable identifier for this backend in the HAL registry.
const BackendName = "corsair"

// channelEntry holds per-channel identity within a corsairBackend.
type channelEntry struct {
	index  int
	role   hal.ChannelRole
	isPump bool
}

// corsairBackend implements hal.FanBackend for a single probed Corsair device.
//
// writable is true only when BOTH conditions hold (RULE-LIQUID-06):
//   - ProbeOptions.UnsafeCorsairWrites was set to true, AND
//   - the device firmware version is in firmwareAllowList.
//
// In v0.4.0 firmwareAllowList is empty, so writable is always false.
//
// reconnecting marks that the device just reconnected after a USB disconnect.
// The next Write call will first write pumpMin to the pump before the
// requested duty cycle (RULE-LIQUID-02).
type corsairBackend struct {
	inner        *probedDevice
	writable     bool
	pumpMin      uint8
	channels     []channelEntry
	reconnecting bool
	logger       *slog.Logger
}

// Name implements hal.FanBackend.
func (b *corsairBackend) Name() string { return BackendName }

// Close implements hal.FanBackend.
// Restores all channels to firmware curve mode (RULE-LIQUID-04), sends the sleep
// command, then closes the HID handle. hidraw.Device.Close is idempotent.
func (b *corsairBackend) Close() error {
	l := b.logger
	if l == nil {
		l = slog.Default()
	}
	b.restoreAll(l) // RULE-LIQUID-04: restore before closing the HID handle.
	b.inner.mu.Lock()
	_, _ = sendCommand(b.inner.hid, cmdSleepFrame)
	b.inner.mu.Unlock()
	return b.inner.hid.Close()
}

// Enumerate implements hal.FanBackend.
func (b *corsairBackend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	out := make([]hal.Channel, len(b.channels))
	for i, ce := range b.channels {
		out[i] = hal.Channel{
			ID:   fmt.Sprintf("%s-%d", b.inner.info.Path, ce.index),
			Role: ce.role,
			Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		}
	}
	return out, nil
}

// Read implements hal.FanBackend.
// Reads fan speeds (RPM) and, for the pump channel, coolant temperature.
func (b *corsairBackend) Read(ch hal.Channel) (hal.Reading, error) {
	ce, ok := b.findChannel(ch.ID)
	if !ok {
		return hal.Reading{}, fmt.Errorf("corsair: Read: unknown channel %q", ch.ID)
	}

	b.inner.mu.Lock()
	defer b.inner.mu.Unlock()

	if err := doWake(b.inner.hid); err != nil {
		return hal.Reading{}, fmt.Errorf("corsair: Read wake: %w", err)
	}

	speeds, err := doReadSpeeds(b.inner.hid)
	if err != nil {
		l := b.logger
		if l == nil {
			l = slog.Default()
		}
		l.Warn("corsair: read speeds failed", "channel", ch.ID, "err", err)
		return hal.Reading{OK: false}, nil
	}

	var rpm uint16
	if ce.index < len(speeds) {
		rpm = speeds[ce.index]
	}

	var temp float64
	if ce.isPump {
		temps, tErr := doReadTemps(b.inner.hid)
		if tErr == nil && len(temps) > 0 {
			// Temperature is in hundredths of degrees Celsius.
			temp = float64(temps[0]) / 100.0
		}
	}

	return hal.Reading{
		RPM:  rpm,
		Temp: temp,
		OK:   true,
	}, nil
}

// Write implements hal.FanBackend.
//
// RULE-LIQUID-01: pump channels are floored at pumpMin before writing.
// RULE-LIQUID-02: if reconnecting, the pump floor is written first.
// RULE-LIQUID-03 / RULE-LIQUID-06: returns ErrReadOnlyUnvalidatedFirmware
// when writable is false.
func (b *corsairBackend) Write(ch hal.Channel, pwm uint8) error {
	if !b.writable {
		return fmt.Errorf("corsair: %w", liquid.ErrReadOnlyUnvalidatedFirmware)
	}

	ce, ok := b.findChannel(ch.ID)
	if !ok {
		return fmt.Errorf("corsair: Write: unknown channel %q", ch.ID)
	}

	// RULE-LIQUID-02: after USB reconnect, write pump to safe floor before any
	// other command sequence. reconnectFloor acquires its own mutex, so this must
	// be called before b.inner.mu.Lock().
	if b.reconnecting {
		b.reconnecting = false
		if err := b.reconnectFloor(); err != nil {
			return err
		}
	}

	b.inner.mu.Lock()
	defer b.inner.mu.Unlock()

	// RULE-LIQUID-01: pump floor enforced in the HAL write path.
	if ce.isPump && pwm < b.pumpMin {
		pwm = b.pumpMin
	}

	if err := doWake(b.inner.hid); err != nil {
		return fmt.Errorf("corsair: Write wake: %w", err)
	}
	return doWriteDuty(b.inner.hid, ce.index, pwm)
}

// Restore implements hal.FanBackend.
// Returns one channel to firmware curve mode.
func (b *corsairBackend) Restore(ch hal.Channel) error {
	ce, ok := b.findChannel(ch.ID)
	if !ok {
		return fmt.Errorf("corsair: Restore: unknown channel %q", ch.ID)
	}

	b.inner.mu.Lock()
	defer b.inner.mu.Unlock()

	if err := doWake(b.inner.hid); err != nil {
		return fmt.Errorf("corsair: Restore wake: %w", err)
	}
	return doRestoreChannel(b.inner.hid, ce.index)
}

// findChannel looks up the channelEntry for the given channel ID.
func (b *corsairBackend) findChannel(id string) (channelEntry, bool) {
	for _, ce := range b.channels {
		if fmt.Sprintf("%s-%d", b.inner.info.Path, ce.index) == id {
			return ce, true
		}
	}
	return channelEntry{}, false
}
