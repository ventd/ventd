// Package stall implements R31's acoustic fan-stall detector. The
// detector is advisory only — it consumes a window of mic samples
// from the post-calibration soak phase and reports whether a 2-of-3
// stall signature is present. It NEVER refuses fan writes or alters
// control behaviour; the result is informational, surfaced in
// `ventd doctor` output and via the polarity classifier's
// AcousticStallSuspected flag.
//
// The three signals (R31 §3):
//
//   - Broadband rise: the captured window's RMS dB level rises by ≥ 6 dB
//     compared to the channel's healthy reference for the same RPM bucket.
//     Fan-stall typically introduces broadband whoosh / hiss above the
//     normal aerodynamic noise floor.
//
//   - Crest factor excess: peak / RMS ratio rises by ≥ 2 above the
//     healthy reference. Healthy fans produce nearly-Gaussian noise
//     with crest factor ≈ √2 to ≈ 4; stalled fans add transient
//     bursts that push crest higher.
//
//   - Kurtosis excess: 4th-moment kurtosis rises by ≥ 1.5 above the
//     healthy reference. Same logic — bursty transients create
//     heavy-tailed amplitude distributions.
//
// Trigger when at least 2 of the 3 fire within the same window.
//
// v0.5.12 simplification: the broadband measurement uses full-spectrum
// RMS rather than the R31-specified 1–2 kHz band-passed RMS. The
// 2-of-3 gate logic is structurally identical; the band-pass refinement
// can land in a follow-up that adds a Butterworth IIR (similar in
// shape to the A-weighting cascade in `internal/acoustic/capture`).
// The simplification is documented in RULE-STALL-01.
//
// Validation: synthetic-fixture-driven tests + Phoenix's HIL on a
// blocked-impeller chassis fan. MIMII dataset validation (AUC ≥ 0.7
// acceptance criterion) is deferred to a separate PR.
//
// Design source: docs/research/r-bundle/R31-fan-stall-acoustic.md.
package stall

import "math"

// Config holds the thresholds + gate parameters. Defaults via
// DefaultConfig; the wiring layer can override individual fields when
// HIL telemetry justifies it.
type Config struct {
	// SampleRate is the capture rate in Hz. Must match the upstream
	// WAV decoder (canonical 48 kHz from cmd/ventd/calibrate_acoustic.go).
	SampleRate float64

	// WindowSeconds is the per-window soak duration. The detector
	// fires only when the 2-of-3 gate trips on a single window.
	WindowSeconds float64

	// BroadbandRiseDB is the dB threshold for the broadband-rise
	// criterion: current_dB - healthy_dB ≥ this value fires.
	// R31 §3.1: 6 dB.
	BroadbandRiseDB float64

	// CrestFactorExcess is the absolute increase above healthy that
	// trips the crest-factor criterion. R31 §3.2: 2.0.
	CrestFactorExcess float64

	// KurtosisExcess is the absolute increase above healthy that
	// trips the kurtosis criterion. R31 §3.3: 1.5.
	KurtosisExcess float64

	// GateThreshold is the number of criteria that must fire to
	// declare a stall suspected. R31: 2-of-3.
	GateThreshold int
}

// DefaultConfig returns the canonical R31 thresholds.
func DefaultConfig() Config {
	return Config{
		SampleRate:        48000,
		WindowSeconds:     3.0,
		BroadbandRiseDB:   6.0,
		CrestFactorExcess: 2.0,
		KurtosisExcess:    1.5,
		GateThreshold:     2,
	}
}

// silenceDBFloor matches RMSdBFS's clamp in internal/acoustic/capture.
// A signal whose RMS is below 1e-6 (~ -120 dBFS) is treated as silent
// rather than diverging to -∞ in the dB conversion.
const silenceDBFloor = -120.0

// Features are the per-window descriptors the gate consumes. The
// detector takes one Features for the healthy reference and one for
// the candidate and applies the 2-of-3 logic to their differences.
type Features struct {
	// BroadbandDB is 20·log10(RMS) of the window. Full-spectrum in
	// v0.5.12; band-passed (1–2 kHz Butterworth) in a follow-up.
	BroadbandDB float64

	// CrestFactor is peak / RMS. Bursty transients raise crest;
	// pure tones have crest ≈ √2 ≈ 1.414; pure white noise has crest
	// ≈ 3 to 4 over a 1-second window.
	CrestFactor float64

	// Kurtosis is the standard "excess kurtosis" — fourth-central-
	// moment divided by variance², minus 3. Gaussian distribution has
	// excess kurtosis = 0. Heavier-tailed distributions (bursty
	// transients on top of fan noise) push kurtosis positive.
	Kurtosis float64
}

// Result is the gate's decision plus per-criterion details for
// surfacing in `ventd doctor` output.
type Result struct {
	// StallSuspected is true iff at least Config.GateThreshold of
	// the three criteria fired. Advisory only — does not affect
	// fan writes or any control path.
	StallSuspected bool

	// BroadbandRise is current.BroadbandDB - healthy.BroadbandDB.
	BroadbandRise float64

	// CrestExcess is current.CrestFactor - healthy.CrestFactor.
	CrestExcess float64

	// KurtosisExcess is current.Kurtosis - healthy.Kurtosis.
	KurtosisExcess float64

	// FiredBroadband / FiredCrest / FiredKurtosis report whether
	// each individual criterion crossed its threshold. Useful for
	// the doctor card to explain WHICH signal tripped.
	FiredBroadband bool
	FiredCrest     bool
	FiredKurtosis  bool
}

// Extract computes Features from a window of normalised samples. The
// samples are expected to be already-band-limited and amplitude-
// normalised (the caller passes the output of the acoustic.capture
// Parse pipeline). An empty slice returns Features{BroadbandDB: -120}
// — the silence-floor sentinel.
func Extract(samples []float64) Features {
	if len(samples) == 0 {
		return Features{BroadbandDB: silenceDBFloor}
	}

	// First pass: sumSq + peak for RMS and crest factor; sum for mean.
	var sumSq, peak, sum float64
	for _, x := range samples {
		sumSq += x * x
		if a := math.Abs(x); a > peak {
			peak = a
		}
		sum += x
	}
	n := float64(len(samples))
	rms := math.Sqrt(sumSq / n)
	mean := sum / n

	broadband := silenceDBFloor
	if rms > 1e-6 {
		broadband = 20 * math.Log10(rms)
	}

	crest := 0.0
	if rms > 1e-9 {
		crest = peak / rms
	}

	// Second pass: 2nd and 4th central moments for kurtosis.
	var sumSqDev, sum4 float64
	for _, x := range samples {
		d := x - mean
		d2 := d * d
		sumSqDev += d2
		sum4 += d2 * d2
	}
	variance := sumSqDev / n
	kurtosis := 0.0
	if variance > 1e-12 {
		kurtosis = (sum4/n)/(variance*variance) - 3.0
	}

	return Features{
		BroadbandDB: broadband,
		CrestFactor: crest,
		Kurtosis:    kurtosis,
	}
}

// Evaluate applies the 2-of-3 stall-detection gate. Returns a Result
// with the per-criterion firing state and the overall verdict.
//
// healthy is the channel × RPM-bucket reference captured during a
// known-good calibration run; current is the post-calibration soak
// window under analysis. The wiring layer is responsible for picking
// the right healthy reference based on the live RPM.
//
// The gate is purely a comparator — no I/O, no state, no side effects.
// Repeated calls with the same inputs return identical Results.
func Evaluate(healthy, current Features, cfg Config) Result {
	rise := current.BroadbandDB - healthy.BroadbandDB
	crestEx := current.CrestFactor - healthy.CrestFactor
	kurtEx := current.Kurtosis - healthy.Kurtosis

	fb := rise >= cfg.BroadbandRiseDB
	fc := crestEx >= cfg.CrestFactorExcess
	fk := kurtEx >= cfg.KurtosisExcess

	fires := 0
	if fb {
		fires++
	}
	if fc {
		fires++
	}
	if fk {
		fires++
	}

	return Result{
		StallSuspected: fires >= cfg.GateThreshold,
		BroadbandRise:  rise,
		CrestExcess:    crestEx,
		KurtosisExcess: kurtEx,
		FiredBroadband: fb,
		FiredCrest:     fc,
		FiredKurtosis:  fk,
	}
}
