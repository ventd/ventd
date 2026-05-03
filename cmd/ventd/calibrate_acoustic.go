package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/acoustic/capture"
	"github.com/ventd/ventd/internal/acoustic/runner"
)

// runCalibrateAcoustic implements `ventd calibrate --acoustic <mic_device>`.
//
// This is a thin CLI wrapper over internal/acoustic/runner.Run — the
// runner package owns the actual ffmpeg → WAV → dBFS → K_cal pipeline,
// extracted in PR-W2 so the wizard's PhaseGate
// (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01) and any future web handler
// can share the same logic without depending on cmd/ventd's CLI shape.
//
// Flags:
//
//	--acoustic <device>   ALSA device (e.g. hw:CARD=USB,DEV=0). Required.
//	--ref-spl <dB>        Reference-tone SPL at the mic (dB, default 94).
//	                       The standard pistonphone tone is 94 dB SPL @ 1 kHz;
//	                       cheap calibrators often emit 114 dB. Operator must
//	                       know which.
//	--seconds <n>         Capture duration in seconds (default 30, max 60).
//	--out <path>          Override calibration JSON output path. When unset,
//	                       prints the calculated K_cal + dBFS values to stdout
//	                       and exits 0.
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

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(seconds+10)*time.Second)
	defer cancel()

	res, err := runner.Run(ctx, runner.Options{
		MicDevice: acousticFlag,
		RefSPL:    refSPL,
		Seconds:   seconds,
		OutPath:   outPath,
		Logger:    logger,
	})
	if err != nil {
		return err
	}

	if outPath != "" {
		fmt.Printf("Acoustic calibration written to %s\n", outPath)
	}

	fmt.Printf("Mic device:     %s\n", res.MicDevice)
	fmt.Printf("Mic ID:         %s\n", res.MicID)
	fmt.Printf("Reference SPL:  %.1f dB\n", res.RefSPL)
	fmt.Printf("Capture:        %d s @ 48 kHz mono 16-bit\n", res.Seconds)
	fmt.Printf("Raw dBFS:       %.2f\n", res.RawDBFS)
	fmt.Printf("A-weighted dBFS: %.2f\n", res.AWeightedDBFS)
	fmt.Printf("K_cal offset:   %.2f dB  (add to A-weighted dBFS at any future capture to get dBA SPL)\n", res.KCalOffset)
	return nil
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
	fmt.Fprintf(os.Stderr, "  --seconds <n>         Capture duration in seconds (default 30, max %d)\n", capture.MaxCaptureSeconds)
	fmt.Fprintln(os.Stderr, "  --out <path>          Write calibration JSON to <path>; otherwise stdout-only")
	fmt.Fprintln(os.Stderr, "  --help                Show this message and exit")
}
