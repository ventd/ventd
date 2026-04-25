package calibration

import (
	"context"
	"fmt"
	"time"
)

// ProbeStall finds stall_pwm and min_responsive_pwm for a duty_0_255 (or
// percentage_0_100) channel using a descending sweep with step size 16.
// Returns (stallPWM, minResponsivePWM, maxObservedRPM, samplesCollected, error).
// RULE-CALIB-PR2B-04, RULE-CALIB-PR2B-05.
func ProbeStall(ctx context.Context, p ChannelProber, pwmUnitMax int, latency time.Duration) (stallPWM, minResp *int, maxRPM, samples int, err error) {
	step := 16
	if pwmUnitMax < 32 {
		step = 1
	}

	// Establish max RPM at full speed.
	if wErr := p.WritePWM(ctx, pwmUnitMax); wErr != nil {
		err = fmt.Errorf("stall probe write max: %w", wErr)
		return
	}
	if sErr := p.Settle(ctx, latency*3); sErr != nil {
		err = fmt.Errorf("stall probe settle: %w", sErr)
		return
	}
	rpm, rErr := p.ReadRPM(ctx)
	if rErr != nil {
		err = fmt.Errorf("stall probe read rpm at max: %w", rErr)
		return
	}
	samples++
	if rpm > maxRPM {
		maxRPM = rpm
	}
	if rpm == 0 {
		// Fan never spins at max PWM → phantom channel.
		return
	}

	// Descend to find the stall point.
	prevNonZero := pwmUnitMax
	for v := pwmUnitMax - step; v >= 0; v -= step {
		if wErr := p.WritePWM(ctx, v); wErr != nil {
			err = fmt.Errorf("stall probe write %d: %w", v, wErr)
			return
		}
		if sErr := p.Settle(ctx, latency*3); sErr != nil {
			err = fmt.Errorf("stall probe settle at %d: %w", v, sErr)
			return
		}
		rpm, rErr = p.ReadRPM(ctx)
		if rErr != nil {
			err = fmt.Errorf("stall probe read rpm at %d: %w", v, rErr)
			return
		}
		samples++
		if rpm > maxRPM {
			maxRPM = rpm
		}
		if rpm == 0 {
			stall := v
			stallPWM = &stall
			minR := prevNonZero
			minResp = &minR
			return
		}
		prevNonZero = v
	}

	// Never stalled: stall is below the lowest sweep step.
	zero := 0
	stallPWM = &zero
	minR := min(step, pwmUnitMax)
	minResp = &minR
	return
}

// ProbeStallStep finds stall_pwm and min_responsive_pwm for a step_0_N channel
// using binary search. Returns (stallPWM, minResponsivePWM, maxObservedRPM, samplesCollected, error).
// RULE-CALIB-PR2B-10.
func ProbeStallStep(ctx context.Context, p ChannelProber, pwmUnitMax int, latency time.Duration) (stallPWM, minResp *int, maxRPM, samples int, err error) {
	lo, hi := 0, pwmUnitMax

	for lo < hi {
		mid := (lo + hi) / 2
		if wErr := p.WritePWM(ctx, mid); wErr != nil {
			err = fmt.Errorf("step stall probe write %d: %w", mid, wErr)
			return
		}
		if sErr := p.Settle(ctx, latency*3); sErr != nil {
			err = fmt.Errorf("step stall probe settle: %w", sErr)
			return
		}
		rpm, rErr := p.ReadRPM(ctx)
		if rErr != nil {
			err = fmt.Errorf("step stall probe read rpm at %d: %w", mid, rErr)
			return
		}
		samples++
		if rpm > maxRPM {
			maxRPM = rpm
		}
		if rpm > 0 {
			hi = mid // might be able to go lower
		} else {
			lo = mid + 1
		}
	}

	// lo == hi is the minimum step where RPM > 0.
	minR := lo
	minResp = &minR
	stall := lo - 1
	stallPWM = &stall
	return
}
