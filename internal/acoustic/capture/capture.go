// Package capture implements R30's microphone calibration primitives:
// WAV-header parsing, RMS dBFS computation, and the A-weighting filter
// (IEC 61672-1:2013 Class 1, 6th-order IIR via canonical bilinear
// transform) used to convert dBFS → dBA when paired with R30's K_cal
// reference-tone offset.
//
// The package is pure Go — no CGO, no audio library dependencies. It
// consumes 16-bit PCM mono WAV bytes (the shape ffmpeg emits with
// `-ar 48000 -ac 1`) and never opens an audio device itself. The CLI
// subcommand (cmd/ventd/calibrate_acoustic.go) is responsible for
// spawning ffmpeg and shelling its WAV output into Parse + RMS + AWeight.
//
// Threat-model invariants:
//   - No raw audio bytes are persisted by this package. The caller deletes
//     the .wav file immediately after extracting RMS dBFS and the K_cal
//     offset (RULE-DIAG-PR2C-11).
//   - No network or external process state. The package is hermetically
//     testable from byte slices.
//
// Design source: docs/research/r-bundle/R30-mic-calibration.md.
package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/cmplx"
)

// SampleRate is the canonical capture rate (Hz). The CLI subcommand
// invokes ffmpeg with `-ar 48000`; downstream Parse rejects anything
// else so the A-weighting IIR coefficients (computed for 48 kHz) stay
// load-bearing.
const SampleRate = 48000

// Channels is the canonical channel count. Mono only — A-weighting +
// RMS dBFS are scalar; stereo would have to pre-mix or pick a channel,
// adding ambiguity that R30 explicitly avoids.
const Channels = 1

// BitsPerSample is the canonical depth. 16-bit PCM is what every
// USB mic emits in default mode and what ffmpeg defaults to without
// an explicit codec flag. 24/32-bit could be supported via a wider
// Parse path but yields no meaningful precision for the dBFS-only
// math used downstream.
const BitsPerSample = 16

// MaxCaptureSeconds caps the WAV duration we'll accept. Calibration
// captures are 30s per R30 §3.2; rejecting anything longer than 60s
// bounds memory usage and prevents an operator-supplied .wav from
// exhausting the daemon's heap.
const MaxCaptureSeconds = 60

// ErrFormat is returned for a WAV header we can't parse (wrong magic,
// wrong sample rate, wrong bit depth, wrong channel count).
var ErrFormat = errors.New("capture: WAV format not supported (need 16-bit PCM mono 48 kHz)")

// ErrTooLong is returned when the data chunk represents more than
// MaxCaptureSeconds of audio.
var ErrTooLong = fmt.Errorf("capture: WAV exceeds %d s cap", MaxCaptureSeconds)

// Parse decodes a 16-bit PCM mono 48 kHz WAV byte slice into a
// []float64 of normalised samples in [-1, 1]. Returns ErrFormat for
// any header mismatch and ErrTooLong if the data chunk represents
// more than MaxCaptureSeconds of audio.
//
// The parser is permissive about chunk ordering (some encoders
// interleave LIST/INFO chunks before the data chunk) but strict
// about format codes — silently truncating or up-converting would
// invalidate the dBFS math.
func Parse(b []byte) ([]float64, error) {
	if len(b) < 44 {
		return nil, fmt.Errorf("%w: header truncated (%d bytes)", ErrFormat, len(b))
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%w: not a RIFF/WAVE file", ErrFormat)
	}

	// Walk chunks to find "fmt " and "data". Some encoders insert a
	// LIST/INFO chunk between them; the strict-12-byte-offset shape
	// some libraries assume falls over there.
	var fmtSeen, dataSeen bool
	var sampleRate uint32
	var channels uint16
	var bits uint16
	var dataOff int
	var dataLen int

	off := 12
	for off+8 <= len(b) {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		if body+size > len(b) {
			break
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("%w: fmt chunk truncated", ErrFormat)
			}
			format := binary.LittleEndian.Uint16(b[body : body+2])
			if format != 1 {
				return nil, fmt.Errorf("%w: format=%d (need 1=PCM)", ErrFormat, format)
			}
			channels = binary.LittleEndian.Uint16(b[body+2 : body+4])
			sampleRate = binary.LittleEndian.Uint32(b[body+4 : body+8])
			bits = binary.LittleEndian.Uint16(b[body+14 : body+16])
			fmtSeen = true
		case "data":
			dataOff = body
			dataLen = size
			dataSeen = true
		}
		// Pad to even size per RIFF spec.
		off = body + size
		if size%2 == 1 {
			off++
		}
		if dataSeen && fmtSeen {
			break
		}
	}
	if !fmtSeen {
		return nil, fmt.Errorf("%w: no fmt chunk", ErrFormat)
	}
	if !dataSeen {
		return nil, fmt.Errorf("%w: no data chunk", ErrFormat)
	}
	if sampleRate != SampleRate {
		return nil, fmt.Errorf("%w: sampleRate=%d (need %d)", ErrFormat, sampleRate, SampleRate)
	}
	if channels != Channels {
		return nil, fmt.Errorf("%w: channels=%d (need %d)", ErrFormat, channels, Channels)
	}
	if bits != BitsPerSample {
		return nil, fmt.Errorf("%w: bits=%d (need %d)", ErrFormat, bits, BitsPerSample)
	}

	nSamples := dataLen / (int(bits) / 8)
	if nSamples > SampleRate*MaxCaptureSeconds {
		return nil, ErrTooLong
	}

	out := make([]float64, nSamples)
	scale := 1.0 / 32768.0
	for i := 0; i < nSamples; i++ {
		v := int16(binary.LittleEndian.Uint16(b[dataOff+i*2 : dataOff+i*2+2]))
		out[i] = float64(v) * scale
	}
	return out, nil
}

// RMSdBFS returns the root-mean-square level of the sample slice in
// decibels relative to full scale (dBFS). Full-scale sine wave →
// approximately -3.01 dBFS; silence → -inf (clamped to -120 dBFS).
//
// The calculation is the IEC-standard sqrt(mean(x²)) with reference
// 1.0 (the float64 normalisation in Parse). dBFS is what R30 uses
// as the raw "before A-weighting + K_cal" measurement.
func RMSdBFS(samples []float64) float64 {
	if len(samples) == 0 {
		return -120.0
	}
	var sumSq float64
	for _, s := range samples {
		sumSq += s * s
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))
	if rms <= 0 || math.IsNaN(rms) {
		return -120.0
	}
	dbfs := 20.0 * math.Log10(rms)
	if dbfs < -120.0 {
		return -120.0
	}
	return dbfs
}

// aWeightCoeffs holds one biquad section's transfer-function constants.
// The denominator is normalised so a0 = 1 (implicit); only a1, a2, and
// the three numerator coefficients are stored.
type aWeightCoeffs struct {
	b0, b1, b2 float64
	a1, a2     float64
}

// aWeightingStages is the digital cascade for the IEC 61672-1:2013 Class 1
// A-weighting filter at the canonical SampleRate (48 kHz). Computed once at
// package init from the analogue prototype via the standard bilinear
// transform — see deriveAWeightingStages.
//
// The analogue prototype is:
//
//	R_A(s) = (2π·f4)² · s⁴ /
//	          [(s + 2π·f1)² · (s + 2π·f2) · (s + 2π·f3) · (s + 2π·f4)²]
//
// where f1=20.598997 Hz, f2=107.65265 Hz, f3=737.86223 Hz,
// f4=12194.217 Hz, and the leading constant is chosen so the magnitude
// response is exactly 0 dB at 1 kHz (the standard normalisation).
//
// The 6th-order filter splits naturally into three biquad sections:
//
//	Stage 1: 1 / (s + 2π·f1)²              — low-frequency double-pole
//	Stage 2: s² / [(s + 2π·f2)(s + 2π·f3)] — mid-band shaping
//	Stage 3: s² / (s + 2π·f4)²              — high-frequency double-pole
//
// The 1 kHz normalisation gain is folded into stage 3's b coefficients
// after the bilinear transform so each stage is independently scaled.
//
// Replaces the v0.5.11 hand-rolled coefficients (issue #884), where the
// first stage's a1=-1.42857 placed an IIR pole at z=+1.42857 — outside
// the unit circle — and the filter diverged on every input.
var aWeightingStages = deriveAWeightingStages(SampleRate)

// AWeightSamples applies the IEC 61672-1:2013 Class 1 A-weighting filter
// to the sample slice in place via a 6th-order canonical IIR. Returns the
// same slice for convenient chaining.
//
// The A-weighting filter is the perceptual model that makes "30 dB at
// 100 Hz" and "30 dB at 1 kHz" map to comparable loudness — human ears
// are dramatically less sensitive to low frequencies, and the curve is
// the international standard for that compensation. R30 §4.1 specifies
// this filter as the dBFS → dBA bridge alongside K_cal.
//
// Implementation is direct-form-II transposed per stage. Stage state
// (z1, z2) is local to each call so AWeightSamples is safe under
// concurrent invocation across distinct slices.
func AWeightSamples(samples []float64) []float64 {
	type state struct{ z1, z2 float64 }
	states := make([]state, len(aWeightingStages))
	for i, x := range samples {
		v := x
		for j := range aWeightingStages {
			c := &aWeightingStages[j]
			st := &states[j]
			y := c.b0*v + st.z1
			st.z1 = c.b1*v + st.z2 - c.a1*y
			st.z2 = c.b2*v - c.a2*y
			v = y
		}
		samples[i] = v
	}
	return samples
}

// deriveAWeightingStages constructs the three-biquad digital cascade
// from the IEC 61672-1:2013 Class 1 A-weighting analogue prototype via
// the standard bilinear transform at sample rate fs.
//
// fs is the sample rate in Hz; for ventd this is always 48000 (the
// canonical SampleRate constant). The function is not parameterised at
// runtime — the var-init pattern is purely cosmetic, keeping the
// canonical constants and bilinear math visible in source rather than
// shipping opaque hard-coded coefficients.
func deriveAWeightingStages(fs int) []aWeightCoeffs {
	// IEC 61672-1:2013 Class 1 pole frequencies (Hz). These are exact
	// per the standard's Annex E (also given in the body for Type 0
	// reference).
	const (
		f1 = 20.598997
		f2 = 107.65265
		f3 = 737.86223
		f4 = 12194.217
	)
	fsf := float64(fs)
	w1 := 2 * math.Pi * f1
	w2 := 2 * math.Pi * f2
	w3 := 2 * math.Pi * f3
	w4 := 2 * math.Pi * f4

	stages := []aWeightCoeffs{
		// Stage 1: H_a(s) = 1/(s+w1)². Analogue numerator is constant 1
		// (no zeros), denominator is s² + 2·w1·s + w1².
		bilinearBiquad(0, 0, 1, 2*w1, w1*w1, fsf),
		// Stage 2: H_b(s) = s²/((s+w2)(s+w3)). Two zeros at the origin,
		// distinct real poles at -w2 and -w3.
		bilinearBiquad(1, 0, 0, w2+w3, w2*w3, fsf),
		// Stage 3: H_c(s) = s²/(s+w4)². Two zeros at the origin, double
		// pole at -w4. The 1 kHz normalisation gain is folded into the
		// numerator below.
		bilinearBiquad(1, 0, 0, 2*w4, w4*w4, fsf),
	}

	// Compute the cascade's magnitude response at 1 kHz and fold the
	// inverse into stage 3's numerator so the cascade has exactly 0 dB
	// gain at 1 kHz (the standard normalisation point).
	z := cmplx.Exp(complex(0, 2*math.Pi*1000/fsf))
	gain := complex(1, 0)
	for _, c := range stages {
		gain *= evalBiquad(c, z)
	}
	norm := 1.0 / cmplx.Abs(gain)
	stages[2].b0 *= norm
	stages[2].b1 *= norm
	stages[2].b2 *= norm
	return stages
}

// bilinearBiquad converts an analogue biquad
//
//	H(s) = (b0a·s² + b1a·s + b2a) / (s² + a1a·s + a2a)
//
// to its digital equivalent at sample rate fs via the bilinear transform
// s = c·(z-1)/(z+1) with c = 2·fs (no prewarping — the 0 dB
// normalisation at 1 kHz absorbs the small mid-band warping at 48 kHz;
// the worst-case error stays within IEC Class 1 tolerance up through
// 10 kHz, which is the documented bound of ventd's mic-calibration
// scope).
func bilinearBiquad(b0a, b1a, b2a, a1a, a2a, fs float64) aWeightCoeffs {
	c := 2 * fs
	c2 := c * c
	d := c2 + a1a*c + a2a
	return aWeightCoeffs{
		b0: (b0a*c2 + b1a*c + b2a) / d,
		b1: (-2*b0a*c2 + 2*b2a) / d,
		b2: (b0a*c2 - b1a*c + b2a) / d,
		a1: (-2*c2 + 2*a2a) / d,
		a2: (c2 - a1a*c + a2a) / d,
	}
}

// evalBiquad evaluates the digital biquad transfer function
// H(z) = (b0 + b1·z⁻¹ + b2·z⁻²) / (1 + a1·z⁻¹ + a2·z⁻²) at the given z.
func evalBiquad(c aWeightCoeffs, z complex128) complex128 {
	z2 := z * z
	num := complex(c.b0, 0)*z2 + complex(c.b1, 0)*z + complex(c.b2, 0)
	den := z2 + complex(c.a1, 0)*z + complex(c.a2, 0)
	return num / den
}

// AWeightedDBFS returns the A-weighted RMS level in dB(A)FS — the
// dBFS of an A-weighted copy of the input. Combined with R30's K_cal
// offset (caller's responsibility to add), this yields dBA SPL.
//
// AWeightedDBFS is destructive on the input slice (in-place A-weighting).
// Callers who need both raw and weighted measurements should call
// RMSdBFS first, then this.
func AWeightedDBFS(samples []float64) float64 {
	weighted := AWeightSamples(samples)
	return RMSdBFS(weighted)
}

// EnsureWAVRead reads the entire WAV bytes from r into memory. Bounded
// at SampleRate*MaxCaptureSeconds*2 bytes plus a 4 KB header headroom
// so a malicious or runaway producer can't OOM the daemon.
func EnsureWAVRead(r io.Reader) ([]byte, error) {
	const cap = SampleRate*MaxCaptureSeconds*2 + 4096
	limited := io.LimitReader(r, cap+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("capture: read wav: %w", err)
	}
	if len(b) > cap {
		return nil, ErrTooLong
	}
	return b, nil
}
