package main

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

// runCalibrateAcoustic implements `ventd calibrate --acoustic <mic_device>`.
//
// The subcommand spawns ffmpeg to capture 30 s of mic audio at 48 kHz
// mono 16-bit PCM, parses the WAV in pure Go, computes raw dBFS +
// A-weighted dBFS, and writes a calibration record. The .wav file is
// deleted immediately after parsing — raw audio is NEVER persisted
// (RULE-DIAG-PR2C-11 architectural denylist for /tmp/ventd-acoustic-*.wav).
//
// Wider PR-D scope (per-fan PWM sweep + ChannelCalibration persistence)
// lands in a follow-up; v0.5.12 ships the capture + K_cal calculation
// path so the downstream cost gate (PR-E) has the data shape it needs.
//
// Flags:
//
//	--acoustic <device>   ALSA device (e.g. hw:CARD=USB,DEV=0). Required.
//	--ref-spl <dB>        Reference-tone SPL at the mic (dB, default 94).
//	                       The standard pistonphone tone is 94 dB SPL @ 1 kHz;
//	                       cheap calibrators often emit 114 dB. Operator must
//	                       know which.
//	--seconds <n>         Capture duration in seconds (default 30).
//	--out <path>          Override calibration JSON output path. When unset,
//	                       prints the calculated K_cal + dBFS values to stdout
//	                       and exits 0; the daemon's PhaseGate writes the
//	                       full ChannelCalibration alongside the per-fan
//	                       sweep (follow-up PR).
func runCalibrateAcoustic(args []string, logger *slog.Logger) error {
	var (
		acousticFlag string
		refSPL       = 94.0
		seconds      = 30
		outPath      string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--acoustic" && i+1 < len(args):
			i++
			acousticFlag = args[i]
		case strings.HasPrefix(arg, "--acoustic="):
			acousticFlag = strings.TrimPrefix(arg, "--acoustic=")
		case arg == "--ref-spl" && i+1 < len(args):
			i++
			if _, err := fmt.Sscanf(args[i], "%f", &refSPL); err != nil {
				return fmt.Errorf("calibrate: parse --ref-spl %q: %w", args[i], err)
			}
		case strings.HasPrefix(arg, "--ref-spl="):
			s := strings.TrimPrefix(arg, "--ref-spl=")
			if _, err := fmt.Sscanf(s, "%f", &refSPL); err != nil {
				return fmt.Errorf("calibrate: parse --ref-spl %q: %w", s, err)
			}
		case arg == "--seconds" && i+1 < len(args):
			i++
			if _, err := fmt.Sscanf(args[i], "%d", &seconds); err != nil {
				return fmt.Errorf("calibrate: parse --seconds %q: %w", args[i], err)
			}
		case strings.HasPrefix(arg, "--seconds="):
			s := strings.TrimPrefix(arg, "--seconds=")
			if _, err := fmt.Sscanf(s, "%d", &seconds); err != nil {
				return fmt.Errorf("calibrate: parse --seconds %q: %w", s, err)
			}
		case arg == "--out" && i+1 < len(args):
			i++
			outPath = args[i]
		case strings.HasPrefix(arg, "--out="):
			outPath = strings.TrimPrefix(arg, "--out=")
		case arg == "--help" || arg == "-h":
			printCalibrateAcousticUsage()
			return nil
		default:
			return fmt.Errorf("calibrate: unknown flag %q (try --help)", arg)
		}
	}

	if acousticFlag == "" {
		printCalibrateAcousticUsage()
		return errors.New("calibrate: --acoustic <mic_device> is required")
	}
	if seconds < 5 || seconds > capture.MaxCaptureSeconds {
		return fmt.Errorf("calibrate: --seconds=%d out of range [5..%d]", seconds, capture.MaxCaptureSeconds)
	}
	if refSPL < 50 || refSPL > 130 {
		return fmt.Errorf("calibrate: --ref-spl=%.1f out of plausible range [50..130] dB", refSPL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds+10)*time.Second)
	defer cancel()

	wavPath, err := captureWAVViaFFmpeg(ctx, acousticFlag, seconds, logger)
	if err != nil {
		return fmt.Errorf("calibrate: capture: %w", err)
	}
	// RULE-DIAG-PR2C-11: raw audio NEVER persists. Delete on every
	// exit path including err returns from Parse / RMS below.
	defer func() {
		if rmErr := os.Remove(wavPath); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Warn("calibrate: temp wav removal failed", "path", wavPath, "err", rmErr)
		}
	}()

	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		return fmt.Errorf("calibrate: read wav: %w", err)
	}

	samples, err := capture.Parse(wavBytes)
	if err != nil {
		return fmt.Errorf("calibrate: parse wav: %w", err)
	}

	rawDBFS := capture.RMSdBFS(samples)
	weightedDBFS := capture.AWeightedDBFS(samples)
	// K_cal = SPL_ref - dBFS_ref. Add K_cal to AWeightedDBFS to get dBA SPL
	// at any future capture from this same mic.
	kCal := refSPL - rawDBFS

	micID := guessMicID(acousticFlag)

	result := acousticCalibrationResult{
		MicDevice:     acousticFlag,
		MicID:         micID,
		RefSPL:        refSPL,
		Seconds:       seconds,
		RawDBFS:       rawDBFS,
		AWeightedDBFS: weightedDBFS,
		KCalOffset:    kCal,
		CapturedAt:    time.Now().UTC(),
	}

	if outPath != "" {
		if err := writeCalibrationJSON(outPath, result); err != nil {
			return fmt.Errorf("calibrate: write %s: %w", outPath, err)
		}
		fmt.Printf("Acoustic calibration written to %s\n", outPath)
	}

	fmt.Printf("Mic device:     %s\n", acousticFlag)
	fmt.Printf("Mic ID:         %s\n", micID)
	fmt.Printf("Reference SPL:  %.1f dB\n", refSPL)
	fmt.Printf("Capture:        %d s @ 48 kHz mono 16-bit\n", seconds)
	fmt.Printf("Raw dBFS:       %.2f\n", rawDBFS)
	fmt.Printf("A-weighted dBFS: %.2f\n", weightedDBFS)
	fmt.Printf("K_cal offset:   %.2f dB  (add to A-weighted dBFS at any future capture to get dBA SPL)\n", kCal)
	return nil
}

// captureWAVViaFFmpeg spawns ffmpeg to capture mic audio to a temp
// WAV file. Uses ALSA input by default; PulseAudio / PipeWire is out
// of scope (operators with those run `arecord` first and pipe a WAV
// instead — future PR may add a `--from-wav <path>` flag).
//
// The CGO_ENABLED=0 invariant forbids linking ALSA directly; ffmpeg
// is the only practical path to a 48 kHz mono 16-bit PCM WAV from a
// pure-Go binary.
func captureWAVViaFFmpeg(ctx context.Context, device string, seconds int, logger *slog.Logger) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", errors.New("ffmpeg is not on PATH; install ffmpeg or run via the wizard's PhaseGate which surfaces a remediation card")
	}

	tmp, err := os.CreateTemp("/tmp", "ventd-acoustic-*.wav")
	if err != nil {
		return "", fmt.Errorf("temp wav: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()

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
	logger.Info("calibrate: capturing", "device", device, "seconds", seconds, "out", tmpPath)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("ffmpeg: %w", err)
	}
	return tmpPath, nil
}

// guessMicID derives a stable identity for the mic from the ALSA
// device string. For real USB mics, the canonical form is hw:CARD=NAME
// or hw:VendorProductName,deviceN — we lift the CARD= chunk as the
// identity. Hashed so the persisted record doesn't expose the raw
// ALSA card name (which can include the user's chosen alias).
func guessMicID(device string) string {
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

// acousticCalibrationResult is the JSON shape written when --out is
// supplied. The daemon's PhaseGate (follow-up PR) will populate the
// full ChannelCalibration record by combining this with per-fan
// dBA-vs-PWM sweep data.
type acousticCalibrationResult struct {
	MicDevice     string    `json:"mic_device"`
	MicID         string    `json:"mic_id"`
	RefSPL        float64   `json:"ref_spl_db"`
	Seconds       int       `json:"seconds"`
	RawDBFS       float64   `json:"raw_dbfs"`
	AWeightedDBFS float64   `json:"a_weighted_dbfs"`
	KCalOffset    float64   `json:"k_cal_offset_db"`
	CapturedAt    time.Time `json:"captured_at"`
}

// writeCalibrationJSON does an atomic tempfile-and-rename write of the
// result alongside the existing per-host calibration JSON shape.
func writeCalibrationJSON(path string, r acousticCalibrationResult) error {
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

func printCalibrateAcousticUsage() {
	fmt.Fprintln(os.Stderr, "Usage: ventd calibrate --acoustic <mic_device> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Captures 30 s (default) of mic audio, computes raw dBFS + A-weighted dBFS, and")
	fmt.Fprintln(os.Stderr, "derives R30's K_cal offset (K_cal = SPL_ref - dBFS_ref). The .wav is deleted")
	fmt.Fprintln(os.Stderr, "immediately after parsing — raw audio is NEVER persisted.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  --acoustic <device>   ALSA device (required), e.g. hw:CARD=USB,DEV=0")
	fmt.Fprintln(os.Stderr, "  --ref-spl <dB>        Reference-tone SPL at the mic (default 94, the standard pistonphone)")
	fmt.Fprintln(os.Stderr, "  --seconds <n>         Capture duration in seconds (default 30, max 60)")
	fmt.Fprintln(os.Stderr, "  --out <path>          Write calibration JSON to <path>; otherwise stdout-only")
	fmt.Fprintln(os.Stderr, "  --help                Show this message and exit")
}
