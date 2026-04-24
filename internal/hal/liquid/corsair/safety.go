package corsair

import (
	"fmt"
	"log/slog"
)

// restoreAllSafe calls each restorer function in order. A panic in restorer N
// is recovered per-entry so the loop continues with entries N+1..end.
//
// RULE-LIQUID-04: Restore completes even on panic. The per-entry recover
// mirrors the hwmon watchdog's per-entry recover loop (RULE-WD-RESTORE-PANIC).
func restoreAllSafe(restorers []func() error, logger *slog.Logger) {
	for i, restore := range restorers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("corsair: panic during channel restore",
						"channel_index", i,
						"panic", fmt.Sprintf("%v", r),
					)
				}
			}()
			if err := restore(); err != nil {
				logger.Error("corsair: restore failed", "channel_index", i, "err", err)
			}
		}()
	}
}

// restoreAll returns all channels of b to firmware curve mode.
// A panic in any individual channel's restore does not prevent the
// remaining channels from being restored (RULE-LIQUID-04).
func (b *corsairBackend) restoreAll(logger *slog.Logger) {
	restorers := make([]func() error, len(b.channels))
	for i, ce := range b.channels {
		restorers[i] = func() error {
			b.inner.mu.Lock()
			defer b.inner.mu.Unlock()
			if err := doWake(b.inner.hid); err != nil {
				return fmt.Errorf("wake before restore ch%d: %w", ce.index, err)
			}
			return doRestoreChannel(b.inner.hid, ce.index)
		}
	}
	restoreAllSafe(restorers, logger)
}

// reconnectFloor writes the pump channel to pumpMin after a USB reconnect.
// Called as the first action after the HID link is re-established.
//
// RULE-LIQUID-02: on reconnect, write pump to safe floor before any other
// command sequence resumes. This ensures the pump speed is at least pumpMin
// regardless of what duty cycle the firmware defaulted to on USB reset.
//
// channel 0 is always the pump when hasPump is true.
func (b *corsairBackend) reconnectFloor() error {
	if !b.inner.hasPump {
		return nil
	}
	b.inner.mu.Lock()
	defer b.inner.mu.Unlock()
	if err := doWake(b.inner.hid); err != nil {
		return fmt.Errorf("corsair: reconnect floor wake: %w", err)
	}
	return doWriteDuty(b.inner.hid, 0, b.pumpMin)
}
