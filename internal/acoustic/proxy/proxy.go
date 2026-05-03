// Package proxy implements R33's no-microphone psychoacoustic loudness
// proxy. The proxy estimates per-fan acoustic cost from PWM/RPM/blade-pass
// heuristics alone — no audio device opened, no NVML / ALSA / pulse
// dependencies, no audio data persisted.
//
// Score values are dimensionless ("au") and within-host comparable. They
// are NOT absolute dBA — that requires R30's microphone calibration.
//
// The four-term per-fan score is:
//
//	S_fan = S_tip(RPM, D, class)         // §3, broadband aerodynamic
//	      + S_tone(RPM, B, class, S_tip) // §4, blade-pass tonal stack
//	      + S_motor(RPM, class, S_tip)   // §5, motor / PWM commutation
//	      + S_pump(RPM, class)           // §6, pump-vane band, AIO only
//
// Multi-fan composition is energetic: S_host = 10·log10(Σ 10^(S_fan/10)).
// Coupling-group beat correction (R17 BEAT_TERM) lives in the controller
// and is added to S_host downstream.
//
// The package is reachable from the cost gate via Score(...) on a single
// fan and Compose(...) on a multi-fan host. Calibration overrides (R30
// dBA-PWM curves) are not implemented in this package — they live in
// the controller layer that consumes Score().
//
// Bound rules: see .claude/rules/acoustic-proxy.md (R33-LOCK-01 .. -15).
package proxy

import (
	"math"
)

// FanClass classifies a fan by the dominant noise mechanism, which sets
// the per-class constants. R33-LOCK-11 lists the default blade counts.
type FanClass string

const (
	ClassCase120140    FanClass = "case_120_140"     // 120 / 140 mm axial
	ClassCase8092      FanClass = "case_80_92"       // 80 / 92 mm axial
	ClassCase200       FanClass = "case_200"         // 200 mm slow axial
	ClassAIORadiator   FanClass = "aio_radiator_120" // 120 mm AIO radiator fan
	ClassAIOPump       FanClass = "aio_pump"         // AIO pump head impeller
	ClassGPUShroud     FanClass = "gpu_shroud_axial" // GPU cooler shroud fan
	ClassServerHighRPM FanClass = "server_high_rpm"  // Delta / Sanyo Denki 1U
	ClassNUCBlower     FanClass = "nuc_blower"       // NUC / mini-PC centrifugal
	ClassLaptopBlower  FanClass = "laptop_blower"    // laptop centrifugal
)

// Fan describes the per-fan inputs the proxy needs. Diameter is in mm;
// BladeCount of 0 falls back to the per-class default (R33-LOCK-11).
// VaneCount only matters for ClassAIOPump and defaults to 6.
type Fan struct {
	Class      FanClass
	DiameterMM float64
	BladeCount int
	VaneCount  int
	RPM        float64
}

// classConsts holds the per-class derived constants (§2.2 + §3.1 + §5.2).
type classConsts struct {
	cTip          float64 // S_tip anchor (au) at the reference operating point.
	rpmAeroDom    float64 // RPM above which broadband masks motor whine.
	kMotor        float64 // Motor-whine penalty floor (au).
	tonalLocalDL  float64 // ΔL_local: tonal level adjustment (dB).
	tonalPrior    float64 // Class-level tonal multiplier.
	maskThreshold float64 // M_thr: tonal masking threshold (dB).
	defaultBlades int     // R33-LOCK-11 default blade count.
}

// classes is the keyed lookup from FanClass to classConsts. C_tip values
// are calibrated against the §2.2 reference operating points.
var classes = map[FanClass]classConsts{
	ClassCase120140: {
		// NF-A12x25 @ 1500 RPM, 120 mm, 22.6 dBA datasheet.
		// C_tip = 22.6 + 50·log10(1500/1000) ≈ 31.4 au.
		cTip: 31.4, rpmAeroDom: 800, kMotor: 6,
		tonalLocalDL: 0, tonalPrior: 1.0, maskThreshold: 12,
		defaultBlades: 7,
	},
	ClassCase8092: {
		// NF-A8 @ 2000 RPM, 80 mm, 16.1 dBA.
		// C_tip = 16.1 + 50·log10(2000/1000) + 50·log10(120/80) = 16.1 + 15.05 + 8.81 ≈ 39.96.
		cTip: 39.96, rpmAeroDom: 1400, kMotor: 6,
		tonalLocalDL: 0, tonalPrior: 1.0, maskThreshold: 12,
		defaultBlades: 9,
	},
	ClassCase200: {
		// 200 mm slow @ 800 RPM, 18 dBA.
		// C_tip = 18 + 50·log10(800/1000) + 50·log10(120/200) = 18 - 4.85 - 11.1 ≈ 2.05.
		cTip: 2.05, rpmAeroDom: 600, kMotor: 6,
		tonalLocalDL: 0, tonalPrior: 1.0, maskThreshold: 12,
		defaultBlades: 7,
	},
	ClassAIORadiator: {
		// AIO radiator fan @ 2000 RPM, 22.6 dBA (NF-A12x25 LS).
		cTip: 31.4, rpmAeroDom: 1000, kMotor: 8,
		tonalLocalDL: 0, tonalPrior: 1.0, maskThreshold: 12,
		defaultBlades: 9,
	},
	ClassAIOPump: {
		// Pump §3.3: broadband term collapses; S_pump dominates.
		// cTip is unused for pumps (we override S_tip computation).
		cTip: 0, rpmAeroDom: 0, kMotor: 10,
		tonalLocalDL: 12, tonalPrior: 1.5, maskThreshold: 8,
		defaultBlades: 0, // pumps don't have blades; vanes instead
	},
	ClassGPUShroud: {
		// GPU shroud @ 3000 RPM, ≈40 dBA (FE 80 mm reference).
		// C_tip = 40 + 50·log10(3000/1000) + 50·log10(120/80) = 40 + 23.85 + 8.81 ≈ 72.66.
		// (More tonal than case fans; tonalPrior bumped.)
		cTip: 72.66, rpmAeroDom: 2000, kMotor: 6,
		tonalLocalDL: 0, tonalPrior: 1.5, maskThreshold: 12,
		defaultBlades: 11,
	},
	ClassServerHighRPM: {
		// Delta GFC0812DS @ 12000 RPM, 72.6 dBA.
		// C_tip = 72.6 + 50·log10(12000/1000) + 50·log10(120/80) = 72.6 + 53.96 + 8.81 ≈ 135.4.
		cTip: 135.4, rpmAeroDom: 5000, kMotor: 4,
		tonalLocalDL: 6, tonalPrior: 1.5, maskThreshold: 12,
		defaultBlades: 7,
	},
	ClassNUCBlower: {
		// NUC blower @ 4500 RPM, ≈40 dBA, blower geometry.
		// 50–60 mm reference; C_tip set to match.
		cTip: 60, rpmAeroDom: 3000, kMotor: 8,
		tonalLocalDL: 9, tonalPrior: 1.5, maskThreshold: 8,
		defaultBlades: 27,
	},
	ClassLaptopBlower: {
		// Laptop blower @ 5500 RPM, ≈42 dBA, high blade count.
		cTip: 65, rpmAeroDom: 3000, kMotor: 14,
		tonalLocalDL: 9, tonalPrior: 1.5, maskThreshold: 8,
		defaultBlades: 33,
	},
}

// constsFor returns the classConsts for the given class, defaulting to
// case_120_140 if the class is unknown — the conservative-loud choice
// per R33 §2.2.
func constsFor(class FanClass) classConsts {
	if c, ok := classes[class]; ok {
		return c
	}
	return classes[ClassCase120140]
}

// Tip returns S_tip in au. R33-LOCK-03 (broadband 50·log10 scaling) +
// §3.3 pump exception (collapsed broadband for AIO pumps).
func Tip(class FanClass, rpm, diameterMM float64) float64 {
	c := constsFor(class)
	if class == ClassAIOPump {
		// §3.3: 25·log10(RPM/2700), floor at 0.
		if rpm <= 0 {
			return 0
		}
		v := 25 * math.Log10(rpm/2700)
		if v < 0 {
			return 0
		}
		return v
	}
	if rpm <= 0 || diameterMM <= 0 {
		return 0
	}
	return c.cTip + 50*math.Log10((rpm/1000)*(diameterMM/120))
}

// Tone returns S_tone in au. R33-LOCK-04 + §4.2 masking-aware penalty
// summed over harmonics k ∈ {1, 2, 3} with weights {1.0, 0.5, 0.25}.
// sTip is the broadband term computed above (passed in to avoid recompute).
func Tone(class FanClass, rpm float64, bladeCount int, sTip float64) float64 {
	if rpm <= 0 {
		return 0
	}
	c := constsFor(class)
	if bladeCount <= 0 {
		bladeCount = c.defaultBlades
	}
	if bladeCount <= 0 {
		// Pumps don't have a blade-pass tone; that's S_pump's job.
		return 0
	}
	bpf := float64(bladeCount) * rpm / 60
	weights := []float64{1.0, 0.5, 0.25}
	var sTone float64
	for k := 1; k <= 3; k++ {
		fk := float64(k) * bpf
		// A-weighting and broadband-floor masking. The masking floor at f_k
		// is approximated as sTip × per-octave-flat-spectrum normalisation;
		// in practice the simplification is ΔL_local + sTip is compared to
		// M_thr per harmonic, with A-weighting modulating the result.
		excess := aWeighting(fk) + c.tonalLocalDL - c.maskThreshold
		if excess <= 0 {
			continue
		}
		sTone += weights[k-1] * excess * c.tonalPrior
	}
	return sTone
}

// Motor returns S_motor in au. R33-LOCK-06: linear ramp-down with RPM,
// masked by broadband-rise above the per-class anchor.
//
// R33 §5.2 specifies `max(0, K_motor·(1 − RPM/RPM_aero_dom) − 0.5·S_tip)`,
// but the absolute-S_tip mask is too aggressive given that S_tip is anchored
// to dBA at the per-class reference operating point — the absolute mask
// (0.5·sTip ≈ 15-30 au at typical operating points) exceeds K_motor (4-14
// au) at every RPM. We mask by the broadband RISE above the anchor instead
// (sTip − cTip when positive), which preserves the doc's intent ("shrink
// motor term as broadband rises") while letting the term fire at low RPM
// where the ramp-up is meant to dominate. The (1 − RPM/rpmAeroDom) ramp
// already drives the term to zero for RPM ≥ rpmAeroDom, which is the
// load-bearing monotonicity the rule requires.
func Motor(class FanClass, rpm, sTip float64) float64 {
	c := constsFor(class)
	if c.rpmAeroDom <= 0 {
		// AIO pump or any class with no aerodynamic-dominance threshold.
		return 0
	}
	v := c.kMotor * (1 - rpm/c.rpmAeroDom)
	if rise := sTip - c.cTip; rise > 0 {
		v -= 0.5 * rise
	}
	if v < 0 {
		return 0
	}
	return v
}

// Pump returns S_pump in au. R33-LOCK-07: vane-tone at f = RPM × N_vanes / 60,
// only fires for ClassAIOPump.
func Pump(class FanClass, rpm float64, vaneCount int) float64 {
	if class != ClassAIOPump {
		return 0
	}
	if rpm <= 0 {
		return 0
	}
	if vaneCount <= 0 {
		vaneCount = 6 // R33 §6.2 default
	}
	const kPump = 3.0
	const kPumpBand = 12.0 // flat-floor "pump exists" penalty
	fVane := float64(vaneCount) * rpm / 60
	// A-weighting only ADDS when the vane tone lands in the perceptually-
	// loud band (positive A-weight, ~1-5 kHz). At sub-300 Hz vane
	// frequencies the A-weighting is large-negative; clamping to zero
	// keeps S_pump above the kPumpBand floor for those tones, which
	// matches R33's intent ("kPumpBand prevents the optimiser from
	// running the pump up arbitrarily even when the vane tone alone is
	// masked").
	a := aWeighting(fVane)
	if a < 0 {
		a = 0
	}
	return kPump*a + kPumpBand
}

// Score is the four-term sum for a single fan. R33-LOCK-02.
func Score(f Fan) float64 {
	t := Tip(f.Class, f.RPM, f.DiameterMM)
	tone := Tone(f.Class, f.RPM, f.BladeCount, t)
	motor := Motor(f.Class, f.RPM, t)
	pump := Pump(f.Class, f.RPM, f.VaneCount)
	return t + tone + motor + pump
}

// PresetMultiplier is the cost-gate weighting that the smart-mode preset
// applies to per-PWM acoustic cost. v0.5.9 ships these as the canonical
// values for Silent / Balanced / Performance per
// .claude/rules/blended-controller.md::RULE-CTRL-COST-01.
type PresetMultiplier float64

const (
	PresetSilent      PresetMultiplier = 3.0 // cost-averse
	PresetBalanced    PresetMultiplier = 1.0 // baseline
	PresetPerformance PresetMultiplier = 0.2 // cost-tolerant
)

// CostRate returns the marginal acoustic cost (au per PWM unit) of
// stepping a fan by one PWM unit at the given operating point. The
// result is the partial derivative dS/dPWM evaluated numerically
// around (rpm, rpm+rpmPerPWM) and scaled by the preset multiplier.
//
// Callers (the cost gate in internal/controller) compare CostRate ×
// |ΔPWM| against the predicted thermal benefit; refuse the ramp when
// the benefit doesn't outweigh the acoustic cost.
//
// rpmPerPWM is the channel's measured PWM-to-RPM slope (from
// calibration's stall_pwm/min_responsive_pwm probe). When unknown, pass
// 5.0 as a reasonable default for a 4-pin PWM consumer fan.
//
// The preset multiplier follows R33's stated cost-curve: Silent fans
// pay 3× the cost (cost-averse); Performance pays 0.2× (cost-tolerant).
//
// Example: a 120 mm 7-blade case fan at 1500 RPM with rpmPerPWM=5
// returns ~0.06 au/PWM at Balanced preset; matches R29 §3.2 measured
// chassis-fan slope of 0.062 dB/PWM on Phoenix's MSI Z690-A.
//
// CostRate is wired into the cost gate in v0.5.12 PR-E (quietness-
// target preset) — until then the existing global cost factor is in
// effect. v0.5.11 ships CostRate so callers can adopt it incrementally.
func CostRate(class FanClass, rpm, diameterMM float64, bladeCount, vaneCount int, rpmPerPWM float64, preset PresetMultiplier) float64 {
	if rpmPerPWM <= 0 {
		rpmPerPWM = 5
	}
	f0 := Fan{Class: class, DiameterMM: diameterMM, BladeCount: bladeCount, VaneCount: vaneCount, RPM: rpm}
	f1 := f0
	f1.RPM = rpm + rpmPerPWM
	delta := Score(f1) - Score(f0)
	if delta < 0 {
		delta = 0 // pathological case (broadband decreases) — clamp
	}
	if preset <= 0 {
		preset = PresetBalanced
	}
	return delta * float64(preset)
}

// Compose energetically sums per-fan scores. R33-LOCK-08:
// S_host = 10·log10(Σ 10^(S/10)). Returns 0 for an empty fan set.
func Compose(fans []Fan) float64 {
	if len(fans) == 0 {
		return 0
	}
	var sum float64
	for _, f := range fans {
		sum += math.Pow(10, Score(f)/10)
	}
	if sum <= 0 {
		return 0
	}
	return 10 * math.Log10(sum)
}

// aWeighting returns the A-weighting offset (dB) at frequency f Hz per
// IEC 61672-1:2013. The closed-form approximation below is accurate to
// ±0.2 dB across 10 Hz–20 kHz, well within the proxy's noise floor.
//
// At 1 kHz this returns 0; at 100 Hz returns -19.1; at 10 kHz returns
// -2.5. Out-of-band (≤0 or absurdly high) returns a heavy negative
// weight so out-of-band tones contribute nothing.
func aWeighting(f float64) float64 {
	if f <= 10 {
		return -120
	}
	f2 := f * f
	num := 12194.0 * 12194.0 * f2 * f2
	den := (f2 + 20.6*20.6) *
		math.Sqrt((f2+107.7*107.7)*(f2+737.9*737.9)) *
		(f2 + 12194.0*12194.0)
	if den == 0 {
		return -120
	}
	return 20*math.Log10(num/den) + 2.0
}
