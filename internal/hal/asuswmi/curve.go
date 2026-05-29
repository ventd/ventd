// SPDX-License-Identifier: GPL-3.0-or-later
package asuswmi

import (
	"fmt"

	"github.com/ventd/ventd/internal/hal"
)

// fanCurvePoints is the fixed number of anchor points the asus_custom_fan_curve
// hwmon interface requires per fan: pwmN_auto_point1..8_{temp,pwm}. The kernel
// driver (drivers/platform/x86/asus-wmi.c, FAN_CURVE_POINTS == 8) rejects a
// curve that does not fill all eight points, so a CurveSink consumer that
// supplies a different count must be resampled to exactly eight.
const fanCurvePoints = 8

// anchor is one resolved asus_custom_fan_curve point: a temperature in whole
// degrees Celsius and a PWM byte in the kernel's 0-255 space. The kernel scales
// the byte to a percentage when it hands the curve to firmware
// (arg3 += (100 * pwm / 255) << shift), so the sysfs surface is 0-255 even
// though the firmware speaks percent.
type anchor struct {
	TempC int
	PWM   uint8
}

// resampleCurve normalises a caller-supplied hal.CurvePoint slice (ascending by
// TempC, percentages 0-100) into exactly fanCurvePoints anchors with
// strictly-increasing temperatures and non-decreasing PWM bytes — the shape the
// asus_custom_fan_curve hardware accepts. The input is resampled to eight
// anchors evenly spaced across its temperature span, interpolating the
// percentage at each anchor and converting it to the kernel's 0-255 PWM byte,
// so a CurveSink consumer need not know the hardware's anchor count (spec-17
// PR-3). Mirrors the amdgpu fanCurvePointsFromHAL contract (which resamples to
// five) so the two CurveSink backends share an interpolation model.
func resampleCurve(points []hal.CurvePoint) ([]anchor, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("asuswmi: fan curve: at least one source point required")
	}
	lo := points[0].TempC
	hi := points[len(points)-1].TempC
	if hi <= lo {
		// Degenerate / flat input: spread the anchors 1°C apart from lo so the
		// hardware still receives eight strictly-increasing temperatures.
		hi = lo + (fanCurvePoints - 1)
	}
	out := make([]anchor, fanCurvePoints)
	prevPct := 0
	for i := 0; i < fanCurvePoints; i++ {
		temp := lo + (hi-lo)*i/(fanCurvePoints-1)
		pct := clampInt(interpPct(points, temp), 0, 100)
		if pct < prevPct {
			// Fan curves are monotonic non-decreasing; ASUS firmware rejects a
			// curve whose duty falls as temperature rises.
			pct = prevPct
		}
		prevPct = pct
		out[i] = anchor{
			TempC: clampInt(temp, 0, 255),
			PWM:   pctToPWMByte(pct),
		}
	}
	// Integer temperature rounding can collide adjacent anchors; the firmware
	// requires strictly-increasing temperatures, so nudge any duplicate up.
	for i := 1; i < len(out); i++ {
		if out[i].TempC <= out[i-1].TempC {
			out[i].TempC = out[i-1].TempC + 1
		}
	}
	return out, nil
}

// pctToPWMByte converts a 0-100 duty percentage into the kernel's 0-255 PWM
// byte using round-half-up. The asus-wmi sysfs nodes are 0-255 even though the
// firmware ultimately runs on percent; the kernel performs the inverse scaling
// internally.
func pctToPWMByte(pct int) uint8 {
	pct = clampInt(pct, 0, 100)
	return uint8((pct*255 + 50) / 100)
}

// interpPct returns the percentage at temperature t from the piecewise-linear
// curve defined by points (ascending by TempC). Below the first anchor it holds
// the first percentage; above the last it holds the last.
func interpPct(points []hal.CurvePoint, t int) int {
	if t <= points[0].TempC {
		return points[0].Pct
	}
	last := points[len(points)-1]
	if t >= last.TempC {
		return last.Pct
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if t <= b.TempC {
			span := b.TempC - a.TempC
			if span <= 0 {
				return b.Pct
			}
			return a.Pct + (b.Pct-a.Pct)*(t-a.TempC)/span
		}
	}
	return last.Pct
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
