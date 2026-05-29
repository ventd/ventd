// Package asus vendors seerge/g-helper's default fan-curve presets as a Mode-C
// config corpus (spec-17 PR-3). ASUS ROG/TUF/Strix/Scar/Flow/Zephyrus/Zenbook/
// Vivobook/ProArt/ROG Ally notebooks expose an eight-point custom fan curve
// through the mainline asus-wmi driver (hwmon name "asus_custom_fan_curve" —
// see the asus-wmi.yaml driver catalog row), which ventd's internal/hal/asuswmi
// CurveSink backend programs directly.
//
// g-helper (github.com/seerge/g-helper, GPL-3.0) is the canonical open-source
// ASUS fan-control tool. It does NOT ship a per-model curve dictionary — ASUS
// stores per-model curves in the BIOS and g-helper reads them over WMI — so the
// only vendorable curve data it carries is AppConfig.GetDefaultCurve()'s
// model-agnostic fallback curves, keyed by performance mode (silent / balanced
// / turbo) and device (CPU / GPU). This package vendors exactly those, so ventd
// can recognise an ASUS host and surface proven fan curves an operator can
// adopt.
//
// These are reference curves only: ventd talks to the kernel asus-wmi sysfs
// directly (via the asuswmi backend) and never shells out to g-helper, asusctl,
// or any WMI tool. There is no EC-register or ACPI-method map here (so, unlike
// the nbfc corpus, no register allowlist) — only (temperature → duty) anchors.
package asus

// CurvePoint is one anchor of a g-helper fan curve: a temperature in whole
// degrees Celsius mapped to a fan-duty percentage (0-100). g-helper's 16-byte
// curve format stores eight temperature bytes followed by eight duty bytes; the
// vendored JSON pairs them into points. The asuswmi backend converts the
// percentage to the kernel's 0-255 PWM byte when programming the curve.
type CurvePoint struct {
	TempC int `json:"temp"`
	Pct   int `json:"pct"`
}

// Preset is one g-helper performance-mode fallback curve: the CPU (pwm1) and
// GPU (pwm2) eight-point curves for a single mode (silent / balanced / turbo).
type Preset struct {
	Mode string       `json:"mode"`
	CPU  []CurvePoint `json:"cpu"`
	GPU  []CurvePoint `json:"gpu"`
}

// Config is one vendored g-helper curve file. Source identifies the upstream
// tool; Note carries the provenance/format comment; Presets holds the mode
// curves.
type Config struct {
	Source  string   `json:"source"`
	Note    string   `json:"note"`
	Presets []Preset `json:"presets"`
}

// Mode names — the three performance modes g-helper baked default curves for,
// mapping to AsusACPI.PerformanceSilent / PerformanceBalanced (the C# `default`
// case) / PerformanceTurbo.
const (
	ModeSilent   = "silent"
	ModeBalanced = "balanced"
	ModeTurbo    = "turbo"
)

// pointsForDevice returns the CPU or GPU curve for a preset; device is "gpu"
// for the GPU curve and anything else (the CPU is the default fan) for CPU.
func (p Preset) pointsForDevice(device string) []CurvePoint {
	if device == "gpu" {
		return p.GPU
	}
	return p.CPU
}

// DutyAt returns the fan duty percentage the preset commands for the given
// device ("cpu"/"gpu") at temperature tempC, interpolating linearly between
// adjacent anchors (the same model g-helper and the EC firmware use). Below the
// first anchor it holds the first duty; above the last it holds the last. An
// empty curve returns 0.
//
// This lets a consumer (the doctor surface, a future wizard preset import)
// preview or adopt a vendored ASUS curve without re-implementing interpolation.
// Pure read over the vendored data.
func (p Preset) DutyAt(device string, tempC int) int {
	pts := p.pointsForDevice(device)
	if len(pts) == 0 {
		return 0
	}
	if tempC <= pts[0].TempC {
		return pts[0].Pct
	}
	last := pts[len(pts)-1]
	if tempC >= last.TempC {
		return last.Pct
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if tempC <= b.TempC {
			span := b.TempC - a.TempC
			if span <= 0 {
				return b.Pct
			}
			return a.Pct + (b.Pct-a.Pct)*(tempC-a.TempC)/span
		}
	}
	return last.Pct
}

// PeakDuty returns the highest duty percentage in the device's curve — the duty
// the fan reaches at the curve's top anchor. Useful as a compact summary of how
// aggressive a preset is.
func (p Preset) PeakDuty(device string) int {
	pts := p.pointsForDevice(device)
	peak := 0
	for _, pt := range pts {
		if pt.Pct > peak {
			peak = pt.Pct
		}
	}
	return peak
}
