package calibration

import (
	"context"
	"fmt"
	"time"
)

// Polarity describes the PWM-to-RPM relationship for a fan channel.
type Polarity int

const (
	// PolarityUnknown is returned when an error prevents determination.
	PolarityUnknown Polarity = iota
	// PolarityNormal: higher PWM → higher RPM.
	PolarityNormal
	// PolarityInverted: lower PWM → higher RPM.
	PolarityInverted
	// PolarityAmbiguous: RPM delta < polarityRPMDelta — phantom or BIOS-locked.
	PolarityAmbiguous
)

// polarityRPMDelta is the minimum RPM difference required to classify polarity.
const polarityRPMDelta = 200

// ProbePolarity writes low (20%) then high (80%) of pwmUnitMax, reads RPM after
// each settle, and classifies the PWM→RPM relationship.
// Returns (polarity, rpmAtLow, rpmAtHigh, error).
// RULE-CALIB-PR2B-01, RULE-CALIB-PR2B-02, RULE-CALIB-PR2B-03.
func ProbePolarity(ctx context.Context, p ChannelProber, pwmUnitMax int, latency time.Duration) (Polarity, int, int, error) {
	lowPWM := pwmUnitMax * 20 / 100
	highPWM := pwmUnitMax * 80 / 100

	if err := p.WritePWM(ctx, lowPWM); err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe write low: %w", err)
	}
	if err := p.Settle(ctx, latency*3); err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe settle low: %w", err)
	}
	rpmAtLow, err := p.ReadRPM(ctx)
	if err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe read rpm at low: %w", err)
	}

	if err := p.WritePWM(ctx, highPWM); err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe write high: %w", err)
	}
	if err := p.Settle(ctx, latency*3); err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe settle high: %w", err)
	}
	rpmAtHigh, err := p.ReadRPM(ctx)
	if err != nil {
		return PolarityUnknown, 0, 0, fmt.Errorf("polarity probe read rpm at high: %w", err)
	}

	diff := rpmAtHigh - rpmAtLow
	switch {
	case diff >= polarityRPMDelta:
		return PolarityNormal, rpmAtLow, rpmAtHigh, nil
	case -diff >= polarityRPMDelta:
		return PolarityInverted, rpmAtLow, rpmAtHigh, nil
	default:
		return PolarityAmbiguous, rpmAtLow, rpmAtHigh, nil
	}
}
