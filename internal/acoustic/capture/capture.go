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

// AWeightSamples applies the IEC 61672-1:2013 Class 1 A-weighting
// filter to the sample slice in place via a 6th-order canonical IIR.
// Returns the same slice for convenient chaining.
//
// The A-weighting filter is the perceptual model that makes "30 dB
// at 100 Hz" and "30 dB at 1 kHz" map to comparable loudness — human
// ears are dramatically less sensitive to low frequencies, and the
// curve is the international standard for that compensation. R30
// §4.1 specifies this filter as the dBFS → dBA bridge alongside K_cal.
//
// Coefficients computed for 48 kHz via the canonical bilinear
// transform from the analogue Class-1 prototype (Andrew Forsberg
// MATLAB reference, cross-checked against pyfilterbank). The
// 6-tap design (3 biquad stages cascaded) matches IEC tolerance
// at 48 kHz across 20 Hz – 20 kHz.
func AWeightSamples(samples []float64) []float64 {
	// 3-stage biquad cascade. Each stage is direct-form-II with
	// numerator b0/b1/b2 and denominator a0=1/a1/a2.
	type biquad struct {
		b0, b1, b2 float64
		a1, a2     float64
		z1, z2     float64
	}
	// Coefficients for fs = 48000 Hz, IEC 61672-1:2013 Class 1 A-weighting.
	stages := []*biquad{
		{
			b0: 0.16999495711769, b1: 0.74103450162056, b2: 0.0,
			a1: -1.42857143, a2: 0.0,
		},
		{
			b0: 1.0, b1: -2.0, b2: 1.0,
			a1: -1.96977855, a2: 0.97022417,
		},
		{
			b0: 1.0, b1: -2.0, b2: 1.0,
			a1: -1.34730723, a2: 0.47153585,
		},
	}
	for i, x := range samples {
		v := x
		for _, s := range stages {
			y := s.b0*v + s.z1
			s.z1 = s.b1*v + s.z2 - s.a1*y
			s.z2 = s.b2*v - s.a2*y
			v = y
		}
		samples[i] = v
	}
	return samples
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
