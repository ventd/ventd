package calibration

import (
	"context"
	"fmt"
	"time"
)

// quickReadDelay is how long to wait before the first (immediate) readback.
const quickReadDelay = 50 * time.Millisecond

// delayedReadDelay is the additional wait before the second readback.
// Total: 200ms after write. BIOS typically reverts within this window.
const delayedReadDelay = 150 * time.Millisecond

// ProbeBIOSOverride detects whether the BIOS is actively overriding PWM writes.
// Writes targetPWM, reads back quickly (≈50ms) then again at ≈200ms. If the
// first read matches targetPWM but the second does not, the BIOS is reverting.
// Returns (overridden bool, error).
// RULE-CALIB-PR2B-06.
func ProbeBIOSOverride(ctx context.Context, p ChannelProber, targetPWM int) (bool, error) {
	if err := p.WritePWM(ctx, targetPWM); err != nil {
		return false, fmt.Errorf("bios override probe write: %w", err)
	}

	// Quick read: within 50ms.
	if err := p.Settle(ctx, quickReadDelay); err != nil {
		return false, fmt.Errorf("bios override probe settle quick: %w", err)
	}
	v1, err := p.ReadPWM(ctx)
	if err != nil {
		return false, fmt.Errorf("bios override probe read quick: %w", err)
	}

	// Delayed read: at ~200ms.
	if err := p.Settle(ctx, delayedReadDelay); err != nil {
		return false, fmt.Errorf("bios override probe settle delayed: %w", err)
	}
	v2, err := p.ReadPWM(ctx)
	if err != nil {
		return false, fmt.Errorf("bios override probe read delayed: %w", err)
	}

	// BIOS overridden: first read matches our write, second does not.
	return v1 == targetPWM && v2 != targetPWM, nil
}
