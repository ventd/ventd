package hal

// CurvePoint is one anchor of a hardware-followed fan curve: a temperature
// in whole degrees Celsius mapped to a duty-cycle percentage (0-100). A
// CurveSink consumes an ordered, ascending-by-TempC slice of these and
// programs the hardware to follow it, replacing ventd's per-tick PWM loop
// for that channel.
//
// The curve is applied against the HARDWARE's own temperature sensor, not
// any sensor ventd is configured to read: an AMD GPU's fan_curve follows the
// GPU junction temperature, an EC fancurve follows the EC's thermal zone.
// The (temperature → percent) SHAPE is what the daemon hands over; the axis
// is reinterpreted as the device's own temperature. For a GPU that is the
// correct sensor — the fan should respond to GPU temperature — but consumers
// must not assume the curve is driven by a ventd Sensor.
type CurvePoint struct {
	// TempC is the anchor temperature in whole degrees Celsius.
	TempC int
	// Pct is the fan duty at TempC, in percent (0-100).
	Pct int
}

// CurveSink is an OPTIONAL capability bolted onto a FanBackend for hardware
// that follows a programmed fan curve on its own — GPU gpu_od fan_curve
// firmware (AMD RDNA3/4), vendor-EC fancurve nodes (Legion debugfs, HP Omen,
// Razer, Alienware) — rather than accepting a per-tick duty byte through
// FanBackend.Write.
//
// Consumers discover this surface via channel.Caps & CapWriteCurve and a
// runtime type-assertion on the backend
// (`if cs, ok := backend.(hal.CurveSink); ok { ... }`). A channel that
// advertises CapWriteCurve MUST have a backend satisfying CurveSink, and a
// backend satisfying CurveSink MUST advertise CapWriteCurve on the channels
// it can program — the bit and the interface travel together, the same
// contract CapWritePowerProfile / PowerProfileBackend hold.
//
// Lifecycle. The controller programs a CurveSink channel ONCE when control is
// applied (daemon startup, and again whenever the bound curve changes on
// hot-reload) and then leaves the hardware to run the loop. It does NOT call
// FanBackend.Write per tick for such a channel — WriteCurve replaces the
// per-tick duty write entirely. Restore-on-exit returns the hardware to its
// firmware-default curve through the existing FanBackend.Restore path; no
// separate curve-reset entry point is needed.
//
// Implementations MUST be safe to call from multiple goroutines and MUST
// treat WriteCurve as idempotent — re-programming the same curve is a no-op
// from the operator's perspective, so callers re-program freely when they
// cannot cheaply prove the hardware curve is already current (spec-17 PR-1b).
type CurveSink interface {
	FanBackend

	// WriteCurve programs the channel's hardware fan curve from points,
	// which the caller supplies ascending by TempC. The backend resamples
	// or clamps the points to whatever fixed anchor count its hardware
	// requires (AMD RDNA3/4 takes exactly five), enforces the hardware's
	// temperature and percentage ranges, and returns a descriptive error
	// when the write is gated off (e.g. AMD's --enable-amd-overdrive) or
	// the hardware rejects the curve.
	WriteCurve(ch Channel, points []CurvePoint) error
}
