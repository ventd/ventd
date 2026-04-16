package controller

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// writeTempC writes a temperature (in °C) to a fake hwmon temp input.
// hwmon expresses temps in millidegrees, so we scale ×1000.
func writeTempC(t *testing.T, ff fakeFan, tempC float64) {
	t.Helper()
	millis := int(math.Round(tempC * 1000))
	if err := os.WriteFile(ff.tempPath, []byte(fmt.Sprintf("%d\n", millis)), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
}

// makeHysteresisCfg builds a linear-curve config with the given
// hysteresis and smoothing values. The fan / curve / sensor shapes
// mirror makeLinearCurveCfg but expose per-curve tuning knobs the
// baseline helper doesn't need to carry.
func makeHysteresisCfg(ff fakeFan, fanMin, fanMax uint8, hysteresis float64, smoothing time.Duration) *config.Config {
	return &config.Config{
		PollInterval: config.Duration{Duration: time.Second},
		Sensors:      []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: fanMin, MaxPWM: fanMax,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp:    40,
			MaxTemp:    80,
			MinPWM:     0,
			MaxPWM:     255,
			Hysteresis: hysteresis,
			Smoothing:  config.Duration{Duration: smoothing},
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}
}

// TestTick_Hysteresis_RampUpNotDelayed pins the safety invariant from
// hwmon-safety.md: hysteresis MAY delay ramp-down, but NEVER delays
// ramp-up. A sudden heat spike must reach the fan on the first tick
// that observes it.
func TestTick_Hysteresis_RampUpNotDelayed(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeHysteresisCfg(ff, 0, 255, 5.0, 0)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	// Seed lastPWM with a moderate value at 50°C.
	writeTempC(t, ff, 50)
	c.tick()
	initial := readPWMByte(t, ff.pwmPath)

	// Temperature spike: 80°C → curve outputs MaxPWM (255). The ramp-up
	// must land on this tick — hysteresis deadband applies to ramp-down
	// only.
	writeTempC(t, ff, 80)
	c.tick()

	if got := readPWMByte(t, ff.pwmPath); got != 255 {
		t.Errorf("after heat spike: PWM = %d, want 255 (ramp-up ignores hysteresis). initial was %d", got, initial)
	}
}

// TestTick_Hysteresis_SuppressesRampDown confirms that a small
// temperature drop below the last-applied temp, smaller than the
// hysteresis deadband, does NOT result in a ramp-down write.
func TestTick_Hysteresis_SuppressesRampDown(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeHysteresisCfg(ff, 0, 255, 5.0, 0)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	// Warm start at 70°C → curve PWM ~191.
	writeTempC(t, ff, 70)
	c.tick()
	warm := readPWMByte(t, ff.pwmPath)
	if warm == 0 {
		t.Fatalf("warm-start PWM unexpectedly 0")
	}

	// Drop 3°C (inside the 5°C deadband). Tick should NOT write — PWM
	// remains at warm value.
	writeTempC(t, ff, 67)
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != warm {
		t.Errorf("small drop inside deadband: PWM = %d, want %d (no ramp-down)", got, warm)
	}
}

// TestTick_Hysteresis_AllowsRampDownBeyondDeadband confirms that a
// temperature drop bigger than the hysteresis deadband does allow the
// ramp-down. This is the complement of the suppresses test — proving the
// gate opens again past the threshold.
func TestTick_Hysteresis_AllowsRampDownBeyondDeadband(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeHysteresisCfg(ff, 0, 255, 5.0, 0)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	writeTempC(t, ff, 70)
	c.tick()
	warm := readPWMByte(t, ff.pwmPath)

	// Drop 7°C (past the 5°C deadband) → ramp-down permitted.
	writeTempC(t, ff, 63)
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got >= warm {
		t.Errorf("large drop past deadband: PWM = %d, expected < %d (ramp-down allowed)", got, warm)
	}
}

// TestTick_Hysteresis_OscillationPrevention drives a sine-wave temp
// around the mid-point of the linear curve and counts PWM transitions.
// With hysteresis on, transitions drop dramatically compared to without.
// This is the feature's whole purpose — damping physical fan oscillation
// when a sensor is bouncing at the threshold.
func TestTick_Hysteresis_OscillationPrevention(t *testing.T) {
	countTransitions := func(hysteresis float64) int {
		ff := newFakeFan(t)
		cfg := makeHysteresisCfg(ff, 0, 255, hysteresis, 0)
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

		// Sinusoid around 60°C, ±3°C, 20 steps per cycle, 3 cycles.
		var last uint8
		firstTick := true
		transitions := 0
		for i := 0; i < 60; i++ {
			temp := 60 + 3*math.Sin(2*math.Pi*float64(i)/20)
			writeTempC(t, ff, temp)
			c.tick()
			cur := readPWMByte(t, ff.pwmPath)
			if firstTick {
				last = cur
				firstTick = false
				continue
			}
			if cur != last {
				transitions++
				last = cur
			}
		}
		return transitions
	}

	without := countTransitions(0)
	with := countTransitions(7.0)

	// 7°C deadband is larger than the ±3°C sweep amplitude, so after
	// the initial half-cycle climb to the first peak, every subsequent
	// cycle's ramp-down is suppressed. "with" still registers ramp-up
	// transitions on the first quarter-cycle, but later cycles are
	// silent because each peak hits the same PWM as lastPWM. A 5×
	// reduction is the conservative floor — 60 ticks of sine will
	// otherwise produce 40+ transitions in the "without" case.
	if with*5 >= without {
		t.Errorf("hysteresis did not suppress oscillation enough: without=%d, with=%d (expected with*5 < without)", without, with)
	}
}

// TestTick_Smoothing_ReducesVolatility drives a noisy temperature and
// compares the RMS of the PWM output with smoothing on vs off. EMA with
// a time constant several times the poll interval must reduce output
// variance significantly.
func TestTick_Smoothing_ReducesVolatility(t *testing.T) {
	rmsDelta := func(smoothing time.Duration) float64 {
		ff := newFakeFan(t)
		cfg := makeHysteresisCfg(ff, 0, 255, 0, smoothing)
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

		// Noisy signal: 60°C ± 5°C, pseudo-random.
		offsets := []float64{0, 4, -5, 3, -2, 5, -4, 2, -3, 1, 4, -4, 5, -5, 3, -2, 1, -3, 4, -1}
		samples := make([]float64, 0, len(offsets))
		var prev uint8
		firstTick := true
		for _, off := range offsets {
			writeTempC(t, ff, 60+off)
			c.tick()
			cur := readPWMByte(t, ff.pwmPath)
			if firstTick {
				prev = cur
				firstTick = false
				continue
			}
			d := float64(cur) - float64(prev)
			samples = append(samples, d*d)
			prev = cur
		}
		if len(samples) == 0 {
			return 0
		}
		sum := 0.0
		for _, s := range samples {
			sum += s
		}
		return math.Sqrt(sum / float64(len(samples)))
	}

	noSmooth := rmsDelta(0)
	// 8s smoothing with 1s poll → α ≈ 0.11, heavy damping.
	smooth := rmsDelta(8 * time.Second)

	if smooth >= noSmooth {
		t.Errorf("smoothing did not reduce PWM delta RMS: none=%.2f, smooth=%.2f", noSmooth, smooth)
	}
}

// TestTick_ZeroHysteresisZeroSmoothing_Regression pins the compatibility
// contract: a curve with Hysteresis=0 and Smoothing=0 must produce the
// same PWM sequence as the pre-3a code — EMA is a passthrough and the
// hysteresis gate never fires. This is what preserves backwards-compat
// for every pre-3a config on disk.
//
// Curve MinPWM=30 / fan MinPWM=30 keeps the floor off the PWM=0 refuse
// path — that path's "skip the write" behaviour is its own invariant
// and is not what this regression pins.
func TestTick_ZeroHysteresisZeroSmoothing_Regression(t *testing.T) {
	ff := newFakeFan(t)
	cfg := &config.Config{
		PollInterval: config.Duration{Duration: time.Second},
		Sensors:      []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 30, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_curve", Type: "linear", Sensor: "cpu",
			MinTemp:    40,
			MaxTemp:    80,
			MinPWM:     30,
			MaxPWM:     255,
			Hysteresis: 0,
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	// Walk the curve: 40 → 80 → 40 over 9 samples, comparing each
	// tick's PWM with the pure linear formula.
	for _, temp := range []float64{40, 50, 60, 70, 80, 70, 60, 50, 40} {
		writeTempC(t, ff, temp)
		c.tick()
		got := readPWMByte(t, ff.pwmPath)
		var want uint8
		switch {
		case temp <= 40:
			want = 30
		case temp >= 80:
			want = 255
		default:
			ratio := (temp - 40) / 40
			want = uint8(math.Round(30 + ratio*225))
		}
		if got != want {
			t.Errorf("zero-config regression at %.0f°C: PWM = %d, want %d", temp, got, want)
		}
	}
}

// TestTick_Smoothing_DoesNotBypassMinPWMFloor pins the key safety claim
// from hwmon-safety.md: smoothing cannot drive the actual PWM write
// below the fan's MinPWM floor. Even if the curve + smoothed sensor
// output is driving toward PWM=0, clamp holds the write at MinPWM.
//
// Setup: fan MinPWM=80, curve can produce 0 at low temp. Drive temp low
// with smoothing on and confirm PWM never goes below 80.
func TestTick_Smoothing_DoesNotBypassMinPWMFloor(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeHysteresisCfg(ff, 80, 255, 0, 4*time.Second)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	// Tick the fan at low temp for many ticks (well past the smoothing
	// time constant). Each tick the smoothed temp is approaching the
	// raw temp asymptotically; the curve output trends to 0, the clamp
	// to MinPWM=80.
	writeTempC(t, ff, 20)
	for i := 0; i < 30; i++ {
		c.tick()
		got := readPWMByte(t, ff.pwmPath)
		if got < 80 {
			t.Fatalf("tick %d: PWM = %d, below MinPWM floor 80 — smoothing bypassed clamp", i, got)
		}
	}
}

// TestTick_Smoothing_EMAConvergence pins the EMA math. With α = 1 / 5 =
// 0.2 (smoothing=4s, poll=1s → α = 1/5 = 0.2), a unit step on the input
// converges toward the new value at a predictable rate. After ≥3τ the
// error should be <5%.
func TestTick_Smoothing_EMAConvergence(t *testing.T) {
	ff := newFakeFan(t)
	cfg := makeHysteresisCfg(ff, 0, 255, 0, 4*time.Second)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	// Seed at 40°C (curve outputs 0 → clamped to MinPWM=0).
	writeTempC(t, ff, 40)
	c.tick()

	// Step up to 80°C. Without smoothing this would hit 255 immediately;
	// with smoothing the EMA ramps.
	writeTempC(t, ff, 80)

	// One tick alone should be well below the target — the key signature
	// of smoothing. After many ticks (several time constants) it
	// approaches the target.
	c.tick()
	after1 := readPWMByte(t, ff.pwmPath)
	if after1 > 200 {
		t.Errorf("EMA not damping: one tick after step, PWM = %d, expected < 200", after1)
	}

	for i := 0; i < 20; i++ {
		c.tick()
	}
	settled := readPWMByte(t, ff.pwmPath)
	if settled < 240 {
		t.Errorf("EMA not converging: after 20 ticks, PWM = %d, expected ≥ 240", settled)
	}
}

// TestTick_PointsCurveEndToEnd pins the full points-curve path through
// the controller: buildCurve resolves "points" to a *curve.Points, the
// tick evaluates against the configured anchors, and the clamp lands
// within the fan's PWM range. Companion to TestPointsEvaluate which
// covers the curve in isolation.
func TestTick_PointsCurveEndToEnd(t *testing.T) {
	ff := newFakeFan(t)
	// 70°C should land between anchors (60, 150) and (80, 250) at the
	// midpoint → 200. Fan clamp is [50, 255] so 200 passes through.
	writeTempC(t, ff, 70)
	cfg := &config.Config{
		PollInterval: config.Duration{Duration: time.Second},
		Sensors:      []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 50, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{{
			Name: "cpu_points", Type: "points", Sensor: "cpu",
			Points: []config.CurvePoint{
				{Temp: 40, PWM: 50},
				{Temp: 60, PWM: 150},
				{Temp: 80, PWM: 250},
			},
		}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_points"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_points")
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 200 {
		t.Errorf("points curve @ 70°C = %d, want 200", got)
	}
}

// TestTick_Hysteresis_IgnoredForFixedAndMix pins behaviour: non-scalar
// curves (fixed, mix) must not enter the hysteresis gate. Ramp-down on
// those curves is treated like ramp-up — pwm change applies immediately.
// Without this exemption, a mix curve binding a hot CPU and a cold GPU
// could never ramp back down after the CPU cooled because mix curves
// don't have a single "sensor" temperature to compare against.
func TestTick_Hysteresis_IgnoredForFixedAndMix(t *testing.T) {
	ff := newFakeFan(t)
	cfg := &config.Config{
		PollInterval: config.Duration{Duration: time.Second},
		Sensors:      []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 0, MaxPWM: 255,
		}},
		Curves: []config.CurveConfig{
			// Fixed curve with Hysteresis tagged on; controller must
			// ignore the tag because Sensor is empty.
			{Name: "cpu_curve", Type: "fixed", Value: 200, Hysteresis: 10},
		},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")

	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 200 {
		t.Fatalf("initial fixed write: PWM = %d, want 200", got)
	}

	// Change the fixed value (smaller → ramp-down) and confirm the next
	// tick writes it. If hysteresis were active on fixed curves, the
	// write would be suppressed.
	cfg.Curves[0].Value = 100
	c.cfg.Store(cfg)
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != 100 {
		t.Errorf("fixed ramp-down: PWM = %d, want 100 (hysteresis must not apply to fixed)", got)
	}
}
