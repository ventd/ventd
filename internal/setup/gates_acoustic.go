package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/recovery"
)

// AcousticRunner is the runtime entry point for the wizard's optional
// post-thermal mic-calibration step. The CLI subcommand
// (cmd/ventd/calibrate_acoustic.go::runCalibrateAcoustic) is the
// canonical implementation; tests inject a stub runner to exercise the
// gate without spawning ffmpeg or touching a real audio device.
//
// Implementations MUST:
//   - Honour ctx.Done() — the gate driver may cancel mid-capture.
//   - Delete any captured .wav file before returning, on every exit
//     path including error returns (RULE-DIAG-PR2C-11).
//   - Write the calibration JSON to opts.OutPath when OutPath is
//     non-empty, atomically (tempfile + rename).
type AcousticRunner func(ctx context.Context, opts AcousticGateOptions) error

// AcousticGateOptions configures CalibrateAcousticGate.
//
// MicDevice is the canonical "is this gate enabled" gate. When empty
// the gate is a clean no-op — Body returns nil without touching the
// runner, Post skips the persistence check, and OnFailCleanup still
// runs (defensive: a previous run may have left stray /tmp .wav files).
//
// Runner is the implementation hook. Production wires the CLI's
// runCalibrateAcoustic; tests pass a stub that records invocation +
// returns the test's chosen outcome.
//
// TempDir is where OnFailCleanup sweeps for stray ventd-acoustic-*
// files. Defaults to "/tmp" via tempDirOrDefault. Tests pass a t.TempDir().
type AcousticGateOptions struct {
	MicDevice string  // empty = gate is a no-op
	RefSPL    float64 // reference-tone SPL at the mic (dB)
	Seconds   int     // capture duration
	OutPath   string  // calibration JSON output path; empty = stdout-only
	Runner    AcousticRunner
	TempDir   string // override /tmp for test isolation
	Logger    *slog.Logger
}

// CalibrateAcousticGate wraps the optional post-thermal-calibration mic
// calibration step (R30) in a PhaseGate. The wizard's eventual
// PhaseGate-driven Manager.run (#67) will insert this gate after the
// thermal-calibration gate; until that wiring lands the constructor is
// callable in isolation and exercised entirely by gates_acoustic_test.go.
//
// Failure semantics: acoustic calibration is opt-in and orthogonal to
// the rest of the wizard. A missing ffmpeg / failed runner / failed
// persistence MUST NOT abort the install — Body still returns nil after
// logging and the daemon falls back to R33 proxy-only loudness
// estimation. Only a runner-returned error that is explicitly
// non-tolerable (Runner contract violation) propagates as a *GateError.
//
// In practice the runner returns nil for all "soft" failures
// (ffmpeg-missing, mic-not-found) and reserves error returns for
// runner-internal contract violations (e.g. WAV parse on the runner's
// own ffmpeg output). The gate then surfaces those via the standard
// recovery banner with ClassUnknown.
//
// RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01.
func CalibrateAcousticGate(opts AcousticGateOptions) PhaseGate {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return PhaseGate{
		Name:        "calibrate_acoustic",
		Description: "Calibrating microphone for acoustic measurement",

		Body: func(ctx context.Context) error {
			if opts.MicDevice == "" {
				opts.Logger.Info("calibrate_acoustic: skipped — no --mic device supplied")
				return nil
			}
			if opts.Runner == nil {
				return errors.New("calibrate_acoustic: nil Runner; gate misconfigured")
			}
			if err := opts.Runner(ctx, opts); err != nil {
				return fmt.Errorf("calibrate_acoustic: %w", err)
			}
			return nil
		},

		Post: func(ctx context.Context) (recovery.FailureClass, string, error) {
			// Acoustic calibration is opt-in and non-fatal — the
			// daemon falls back to R33 proxy-only loudness when the
			// JSON record is missing, so Post never refuses the
			// install. Verify the runner did what it claimed and
			// log a warning when the JSON is missing or empty;
			// always return ClassUnknown so the wizard proceeds.
			if opts.MicDevice == "" || opts.OutPath == "" {
				return recovery.ClassUnknown, "", nil
			}
			info, err := os.Stat(opts.OutPath)
			if err != nil {
				opts.Logger.Warn("calibrate_acoustic: runner returned nil but JSON missing — daemon will use R33 proxy-only fallback",
					"path", opts.OutPath, "err", err)
				return recovery.ClassUnknown, "", nil
			}
			if info.Size() == 0 {
				opts.Logger.Warn("calibrate_acoustic: JSON written but empty — daemon will use R33 proxy-only fallback",
					"path", opts.OutPath)
			}
			return recovery.ClassUnknown, "", nil
		},

		OnFailCleanup: func(ctx context.Context) {
			tmp := tempDirOrDefault(opts.TempDir)
			entries, err := os.ReadDir(tmp)
			if err != nil {
				opts.Logger.Warn("calibrate_acoustic cleanup: read temp dir failed",
					"dir", tmp, "err", err)
				return
			}
			var removed int
			for _, e := range entries {
				name := e.Name()
				if !strings.HasPrefix(name, "ventd-acoustic-") {
					continue
				}
				if !strings.HasSuffix(name, ".wav") && !strings.HasSuffix(name, ".raw") {
					continue
				}
				path := filepath.Join(tmp, name)
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					opts.Logger.Warn("calibrate_acoustic cleanup: remove failed",
						"path", path, "err", err)
					continue
				}
				removed++
			}
			if removed > 0 {
				opts.Logger.Info("calibrate_acoustic cleanup: removed stray temp files",
					"count", removed, "dir", tmp)
			}
		},
	}
}

func tempDirOrDefault(dir string) string {
	if dir != "" {
		return dir
	}
	return "/tmp"
}

// runAcousticGate is the wizard's hook into the calibrate_acoustic
// PhaseGate. Called from Manager.run after thermal calibration's
// wg.Wait() and before the finalising phase. Reads acousticGateOpts
// (set via SetAcousticGateOptions) and invokes the gate when MicDevice
// is non-empty.
//
// Failures are non-fatal per the gate's contract
// (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01) — the daemon falls back to
// R33 proxy-only acoustic estimation when no K_cal record lands. The
// gate's GateError is logged at WARN level and discarded; the wizard
// proceeds to finalise.
//
// Method receiver is *Manager so test fixtures can drive it directly
// with a stub Runner without exercising the full Manager.run pipeline.
func (m *Manager) runAcousticGate(ctx context.Context) {
	m.mu.Lock()
	opts := m.acousticGateOpts
	m.mu.Unlock()

	if opts.MicDevice == "" {
		return
	}

	if opts.Logger == nil {
		opts.Logger = m.logger
	}

	m.setPhase("calibrate_acoustic",
		"Calibrating microphone for acoustic measurement...")

	gate := CalibrateAcousticGate(opts)
	if err := RunGate(ctx, gate, m.logger); err != nil {
		// Non-fatal: log and continue. RULE-WIZARD-GATE-CALIBRATE-
		// ACOUSTIC-01 explicitly designs the gate as opt-in /
		// soft-fall-through-to-proxy-only.
		m.logger.Warn("acoustic calibration gate failed; daemon will use R33 proxy-only fallback",
			"err", err)
	}
}
