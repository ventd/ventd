// Package cooling provides a spike-quality chassis-cooling-capacity
// estimator (#1285). The model treats each admitted fan as a unit of
// airflow whose dissipation potential scales with RPM and diameter²,
// modulated by per-class efficiency. The reference unit — a
// 120 mm NF-A12x25 at 1500 RPM — is calibrated at
// dissipationReferenceW (30 W of 70 °C-ΔT dissipation per unit).
//
// The estimate is "is this chassis up to the job", not "how loud
// will it get". Treat the absolute number as ±30 % accurate; the
// per-fan ranking and the chassis-vs-CPU-TDP comparison are the
// load-bearing outputs.
//
// All inputs are operator-resolved before calling: fan MaxRPM from
// the calibrate sweep, diameter from the catalog or 120 mm default,
// class from the hwdb fan profile or the name-hint heuristic.
package cooling

// FanInput describes one admitted fan's airflow potential. Pure
// data — no I/O is performed by EstimateChassisCapacityW.
type FanInput struct {
	// Class is the acoustic-proxy FanClass string (lowercased).
	// Used to pick the per-class efficiency factor; an unrecognised
	// class falls through to the "case_120_140" default (1.0).
	Class string
	// DiameterMM is the fan blade diameter in millimetres. 0 means
	// unknown → falls through to the 120 mm reference.
	DiameterMM float64
	// MaxRPM is the fan's measured top RPM from the calibrate
	// sweep. 0 means uncalibrated → contributes zero airflow.
	MaxRPM int
}

// ChassisCapacityW returns an estimate of the chassis's heat-
// dissipation capacity in watts at a 70 °C ΔT operating point.
// Designed to answer the wizard's "is this chassis up to the job"
// question, not predict exact wattage; treat the output as ±30 %.
//
// Empty input slice → 0. A pump fan contributes ~0 W (it moves
// coolant, not air, on this side of the loop). A laptop blower
// contributes much less than a 140 mm case fan at the same RPM —
// the per-class efficiency reflects that.
func ChassisCapacityW(fans []FanInput) float64 {
	const (
		referenceDiameterMM = 120.0
		referenceRPM        = 1500.0
		// dissipationReferenceW is the W of 70 °C-ΔT heat the
		// reference 120 mm fan at 1500 RPM dissipates. Calibrated
		// against the NF-A12x25 datasheet (102.1 m³/h airflow,
		// 22.6 dBA). 30 W is the figure the original spike used.
		dissipationReferenceW = 30.0
	)
	var totalW float64
	for _, f := range fans {
		if f.MaxRPM <= 0 {
			continue
		}
		d := f.DiameterMM
		if d <= 0 {
			d = referenceDiameterMM
		}
		eff, ok := classEfficiency[f.Class]
		if !ok {
			eff = classEfficiency["case_120_140"]
		}
		unit := (float64(f.MaxRPM) / referenceRPM) *
			(d / referenceDiameterMM) * (d / referenceDiameterMM) *
			eff
		totalW += unit * dissipationReferenceW
	}
	return totalW
}

// classEfficiency is the per-class dissipation multiplier relative
// to the 120 mm case axial reference (1.0). Numbers are spike-
// quality:
//
//   - case_120_140 : 1.0 — the calibration reference
//   - case_80_92   : 0.9 — slightly worse static-pressure-vs-airflow
//     profile, but otherwise similar
//   - case_200     : 1.1 — slow-spinner with high airflow at low RPM
//   - aio_radiator_120 : 1.2 — high static pressure, optimised for
//     radiator core ΔT
//   - aio_pump     : 0.0 — moves coolant, not chassis air
//   - gpu_shroud_axial : 0.6 — most air recirculates within the
//     shroud / chassis pocket
//   - nuc_blower   : 0.4 — small, radial, choked-throat
//   - laptop_blower: 0.3 — even smaller + warmer ambient
//   - server_high_rpm : 0.7 — high RPM but small + designed for
//     1U airflow not chassis ΔT
var classEfficiency = map[string]float64{
	"case_120_140":     1.0,
	"case_80_92":       0.9,
	"case_200":         1.1,
	"aio_radiator_120": 1.2,
	"aio_pump":         0.0,
	"gpu_shroud_axial": 0.6,
	"nuc_blower":       0.4,
	"laptop_blower":    0.3,
	"server_high_rpm":  0.7,
}

// CapacityAdequate reports whether the estimated chassis capacity
// safely exceeds the CPU's TDP plus a 25 % headroom margin. The
// margin absorbs the estimator's ±30 % accuracy and a typical 10-15 %
// real-world derating from chassis intake/exhaust path losses.
//
// Returns (false, true) when cpuTDPW > 0 AND capacityW > 0 AND
// capacityW < cpuTDPW * 1.25 — the doctor's "chassis tops out at
// X W; CPU draws Y W under load" warning condition. Returns
// (true, _) when there's no signal in either input (calibration not
// run yet, AMD without RAPL, virtualised host) — the doctor stays
// silent rather than firing a "no signal" warning per #1285's
// "graceful no-data" intent.
func CapacityAdequate(capacityW, cpuTDPW float64) (adequate, hasSignal bool) {
	if capacityW <= 0 || cpuTDPW <= 0 {
		return true, false
	}
	return capacityW >= cpuTDPW*1.25, true
}
