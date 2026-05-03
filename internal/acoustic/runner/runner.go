// Package runner implements R30's microphone calibration runner —
// the ffmpeg → WAV → dBFS → K_cal pipeline that converts a live mic
// capture into a persistent calibration record.
//
// The package is extracted from cmd/ventd/calibrate_acoustic.go so that
// the wizard's PhaseGate (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01) can
// invoke the runner without the cmd/ventd CLI being on the import path.
//
// Threat-model invariants (RULE-DIAG-PR2C-11):
//
//   - The .wav temp file is deleted on every exit path including error
//     returns. Raw audio NEVER persists.
//   - Captured audio bytes are read into memory, parsed, and discarded
//     before the function returns.
//   - The persisted Result carries derived measurements (K_cal offset,
//     dBFS levels, mic identity hash) — no raw audio bytes.
//
// Design source: docs/research/r-bundle/R30-mic-calibration.md.
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/acoustic/capture"
)

// Options configures Run. MicDevice is the only required field; sensible
// defaults are applied for everything else (94 dB pistonphone reference,
// 30-second capture, no JSON output).
type Options struct {
	// MicDevice is the ALSA device string, e.g. "hw:CARD=USB,DEV=0".
	// Required. Empty MicDevice causes Run to return ErrNoDevice.
	MicDevice string

	// RefSPL is the reference-tone SPL at the mic in dB. Defaults to
	// 94.0 (the standard pistonphone). Validated against the
	// plausible-range [50, 130] dB.
	RefSPL float64

	// Seconds is the capture duration in seconds. Defaults to 30.
	// Validated against [5, capture.MaxCaptureSeconds].
	Seconds int

	// OutPath is the destination JSON path. Empty = no persistence;
	// the caller reads the returned Result directly.
	OutPath string

	// Logger is used for INFO logs during the capture. nil → slog.Default.
	Logger *slog.Logger
}

// Sentinel errors. Wrapping is via fmt.Errorf("%w") so callers can
// match with errors.Is.
var (
	ErrNoDevice      = errors.New("calibrate-acoustic: --acoustic <mic_device> is required")
	ErrFFmpegMissing = errors.New("calibrate-acoustic: ffmpeg is not on PATH; install ffmpeg or surface a remediation card")
)

// Result is the calibration record persisted to OutPath when set, and
// returned to the caller for in-memory use. JSON tags pin the on-disk
// shape so downstream consumers can decode the record without depending
// on this package.
type Result struct {
	MicDevice     string    `json:"mic_device"`
	MicID         string    `json:"mic_id"`
	RefSPL        float64   `json:"ref_spl_db"`
	Seconds       int       `json:"seconds"`
	RawDBFS       float64   `json:"raw_dbfs"`
	AWeightedDBFS float64   `json:"a_weighted_dbfs"`
	KCalOffset    float64   `json:"k_cal_offset_db"`
	CapturedAt    time.Time `json:"captured_at"`
}

// Run executes the full calibration pipeline:
//  1. Validate options.
//  2. Spawn ffmpeg to capture mic audio to a temp .wav.
//  3. Parse the .wav, compute raw dBFS + A-weighted dBFS.
//  4. Derive K_cal = RefSPL - rawDBFS.
//  5. Delete the .wav (RULE-DIAG-PR2C-11).
//  6. If OutPath is set, atomically write the Result JSON.
//
// Returns the Result on success. On error the .wav is still deleted; the
// caller is responsible for surfacing the error via the wizard's recovery
// banner or the CLI exit code.
//
// The ctx parameter is honoured by the ffmpeg subprocess (via
// exec.CommandContext) so the wizard can cancel mid-capture.
func Run(ctx context.Context, opts Options) (Result, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if opts.MicDevice == "" {
		return Result{}, ErrNoDevice
	}
	if opts.RefSPL == 0 {
		opts.RefSPL = 94.0
	}
	if opts.Seconds == 0 {
		opts.Seconds = 30
	}
	if opts.Seconds < 5 || opts.Seconds > capture.MaxCaptureSeconds {
		return Result{}, fmt.Errorf("calibrate-acoustic: --seconds=%d out of range [5..%d]",
			opts.Seconds, capture.MaxCaptureSeconds)
	}
	if opts.RefSPL < 50 || opts.RefSPL > 130 {
		return Result{}, fmt.Errorf("calibrate-acoustic: --ref-spl=%.1f out of plausible range [50..130] dB",
			opts.RefSPL)
	}

	wavPath, err := captureWAV(ctx, opts.MicDevice, opts.Seconds, logger)
	if err != nil {
		return Result{}, fmt.Errorf("calibrate-acoustic: capture: %w", err)
	}
	// RULE-DIAG-PR2C-11: raw audio NEVER persists. Delete on every
	// exit path including err returns from Parse / RMS below.
	defer func() {
		if rmErr := os.Remove(wavPath); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Warn("calibrate-acoustic: temp wav removal failed",
				"path", wavPath, "err", rmErr)
		}
	}()

	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		return Result{}, fmt.Errorf("calibrate-acoustic: read wav: %w", err)
	}

	samples, err := capture.Parse(wavBytes)
	if err != nil {
		return Result{}, fmt.Errorf("calibrate-acoustic: parse wav: %w", err)
	}

	rawDBFS := capture.RMSdBFS(samples)
	weightedDBFS := capture.AWeightedDBFS(samples)
	// K_cal = SPL_ref - dBFS_ref. Add K_cal to AWeightedDBFS to get
	// dBA SPL at any future capture from this same mic.
	kCal := opts.RefSPL - rawDBFS

	res := Result{
		MicDevice:     opts.MicDevice,
		MicID:         GuessMicID(opts.MicDevice),
		RefSPL:        opts.RefSPL,
		Seconds:       opts.Seconds,
		RawDBFS:       rawDBFS,
		AWeightedDBFS: weightedDBFS,
		KCalOffset:    kCal,
		CapturedAt:    time.Now().UTC(),
	}

	if opts.OutPath != "" {
		if err := WriteResultJSON(opts.OutPath, res); err != nil {
			return res, fmt.Errorf("calibrate-acoustic: write %s: %w", opts.OutPath, err)
		}
	}
	return res, nil
}

// captureWAV spawns ffmpeg to capture mic audio to a temp WAV file.
// Uses ALSA input by default; PulseAudio / PipeWire is out of scope —
// operators with those run `arecord` first and feed a WAV through a
// future --from-wav flag.
//
// The CGO_ENABLED=0 invariant forbids linking ALSA directly; ffmpeg is
// the only practical path to a 48 kHz mono 16-bit PCM WAV from a
// pure-Go binary.
func captureWAV(ctx context.Context, device string, seconds int, logger *slog.Logger) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", ErrFFmpegMissing
	}

	tmp, err := os.CreateTemp("/tmp", "ventd-acoustic-*.wav")
	if err != nil {
		return "", fmt.Errorf("temp wav: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()

	args := []string{
		"-y", // overwrite
		"-loglevel", "error",
		"-f", "alsa",
		"-i", device,
		"-ar", "48000",
		"-ac", "1",
		"-sample_fmt", "s16",
		"-t", fmt.Sprintf("%d", seconds),
		tmpPath,
	}
	logger.Info("calibrate-acoustic: capturing",
		"device", device, "seconds", seconds, "out", tmpPath)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("ffmpeg: %w", err)
	}
	return tmpPath, nil
}

// GuessMicID derives a stable identity for the mic from the ALSA device
// string. For real USB mics the canonical form is hw:CARD=NAME or
// hw:VendorProductName,deviceN — we lift the CARD= chunk as the
// identity. Hashed so the persisted record doesn't expose the raw ALSA
// card name (which can include the user's chosen alias).
//
// Returns the first 16 hex chars of SHA-256(<id>) — long enough to be
// collision-free across a fleet, short enough to fit in a calibration
// JSON without bloat.
func GuessMicID(device string) string {
	id := device
	if i := strings.Index(device, "CARD="); i >= 0 {
		rest := device[i+5:]
		if comma := strings.IndexAny(rest, ","); comma >= 0 {
			id = rest[:comma]
		} else {
			id = rest
		}
	}
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])[:16]
}

// WriteResultJSON does an atomic tempfile-and-rename write of the result
// to path. The parent directory is created with MkdirAll if absent.
func WriteResultJSON(path string, r Result) error {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}
