package setup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/recovery"
)

// TestRULE_WIZARD_GATE_CALIBRATE_ACOUSTIC_01 binds RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01:
// the calibrate_acoustic PhaseGate is opt-in (no-op when MicDevice empty),
// non-fatal on runner failure, and OnFailCleanup unconditionally sweeps
// /tmp for stray ventd-acoustic-*.{wav,raw} files even on the no-op path.
func TestRULE_WIZARD_GATE_CALIBRATE_ACOUSTIC_01(t *testing.T) {
	t.Run("noop_when_mic_device_empty", func(t *testing.T) {
		// Without a --mic device, the gate must be a clean no-op:
		// Body returns nil without invoking the runner; Post skips
		// the persistence check.
		var ranRunner bool
		opts := AcousticGateOptions{
			MicDevice: "",
			Runner: func(ctx context.Context, _ AcousticGateOptions) error {
				ranRunner = true
				return nil
			},
			TempDir: t.TempDir(),
			Logger:  slog.New(slog.DiscardHandler),
		}
		gate := CalibrateAcousticGate(opts)
		if err := RunGate(context.Background(), gate, opts.Logger); err != nil {
			t.Fatalf("noop gate: %v", err)
		}
		if ranRunner {
			t.Error("Runner was invoked despite empty MicDevice")
		}
	})

	t.Run("happy_path_runner_invoked_post_verifies", func(t *testing.T) {
		// With a MicDevice + OutPath, the runner runs and Post
		// confirms the JSON is on disk.
		tmp := t.TempDir()
		out := filepath.Join(tmp, "acoustic.json")
		var ranRunner bool
		runner := func(ctx context.Context, o AcousticGateOptions) error {
			ranRunner = true
			// Stub runner writes the expected JSON.
			return os.WriteFile(o.OutPath, []byte(`{"ok":true}`), 0o600)
		}
		opts := AcousticGateOptions{
			MicDevice: "hw:CARD=USB,DEV=0",
			OutPath:   out,
			Runner:    runner,
			TempDir:   tmp,
			Logger:    slog.New(slog.DiscardHandler),
		}
		gate := CalibrateAcousticGate(opts)
		if err := RunGate(context.Background(), gate, opts.Logger); err != nil {
			t.Fatalf("happy path: %v", err)
		}
		if !ranRunner {
			t.Error("Runner was not invoked")
		}
		if _, err := os.Stat(out); err != nil {
			t.Errorf("calibration JSON missing after gate: %v", err)
		}
	})

	t.Run("body_error_triggers_cleanup_returns_gate_error", func(t *testing.T) {
		// Runner failure must propagate as *GateError + invoke
		// OnFailCleanup. Stage a stray .wav so we can verify
		// cleanup actually swept it.
		tmp := t.TempDir()
		stray := filepath.Join(tmp, "ventd-acoustic-12345.wav")
		if err := os.WriteFile(stray, []byte("RIFFstub"), 0o600); err != nil {
			t.Fatalf("seed stray wav: %v", err)
		}
		runner := func(ctx context.Context, _ AcousticGateOptions) error {
			return errors.New("simulated ffmpeg failure")
		}
		opts := AcousticGateOptions{
			MicDevice: "hw:CARD=USB,DEV=0",
			OutPath:   filepath.Join(tmp, "acoustic.json"),
			Runner:    runner,
			TempDir:   tmp,
			Logger:    slog.New(slog.DiscardHandler),
		}
		gate := CalibrateAcousticGate(opts)
		err := RunGate(context.Background(), gate, opts.Logger)
		if err == nil {
			t.Fatal("body error did not surface as GateError")
		}
		var ge *GateError
		if !errors.As(err, &ge) {
			t.Fatalf("err is not *GateError: %T", err)
		}
		if ge.Class != recovery.ClassUnknown {
			t.Errorf("Class = %v, want ClassUnknown", ge.Class)
		}
		if _, statErr := os.Stat(stray); !os.IsNotExist(statErr) {
			t.Errorf("OnFailCleanup left stray .wav at %s (stat err: %v)", stray, statErr)
		}
	})

	t.Run("post_is_nonfatal_when_outpath_missing", func(t *testing.T) {
		// Runner returns nil but the OutPath JSON is missing. Post
		// must NOT refuse the wizard — acoustic calibration is
		// opt-in and the daemon falls back to R33 proxy-only when
		// the JSON record is absent. Post logs a warning and
		// returns ClassUnknown so the wizard proceeds.
		tmp := t.TempDir()
		runner := func(ctx context.Context, _ AcousticGateOptions) error {
			return nil // silent success, but no JSON written
		}
		opts := AcousticGateOptions{
			MicDevice: "hw:CARD=USB,DEV=0",
			OutPath:   filepath.Join(tmp, "missing.json"),
			Runner:    runner,
			TempDir:   tmp,
			Logger:    slog.New(slog.DiscardHandler),
		}
		gate := CalibrateAcousticGate(opts)
		if err := RunGate(context.Background(), gate, opts.Logger); err != nil {
			t.Fatalf("post must be non-fatal when JSON missing; got %v", err)
		}
	})

	t.Run("cleanup_only_removes_acoustic_temp_pattern", func(t *testing.T) {
		// OnFailCleanup must NOT touch unrelated /tmp files — only
		// our own ventd-acoustic-*.{wav,raw} prefix/suffix combo.
		tmp := t.TempDir()
		ours := filepath.Join(tmp, "ventd-acoustic-abc.wav")
		alsoOurs := filepath.Join(tmp, "ventd-acoustic-xyz.raw")
		notOurs1 := filepath.Join(tmp, "ventd-other-foo.wav")    // wrong prefix
		notOurs2 := filepath.Join(tmp, "ventd-acoustic-foo.txt") // wrong suffix
		notOurs3 := filepath.Join(tmp, "totally-unrelated.log")
		for _, p := range []string{ours, alsoOurs, notOurs1, notOurs2, notOurs3} {
			if err := os.WriteFile(p, []byte("seed"), 0o600); err != nil {
				t.Fatalf("seed %s: %v", p, err)
			}
		}
		runner := func(ctx context.Context, _ AcousticGateOptions) error {
			return errors.New("force cleanup")
		}
		opts := AcousticGateOptions{
			MicDevice: "hw:CARD=USB,DEV=0",
			OutPath:   filepath.Join(tmp, "acoustic.json"),
			Runner:    runner,
			TempDir:   tmp,
			Logger:    slog.New(slog.DiscardHandler),
		}
		gate := CalibrateAcousticGate(opts)
		_ = RunGate(context.Background(), gate, opts.Logger)

		// ours + alsoOurs should be gone.
		for _, p := range []string{ours, alsoOurs} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("expected %s removed, stat err: %v", p, err)
			}
		}
		// the three NOT ours should still exist.
		for _, p := range []string{notOurs1, notOurs2, notOurs3} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("expected %s preserved, stat err: %v", p, err)
			}
		}
	})
}

// TestManager_runAcousticGate_NoOpWhenMicEmpty verifies that the
// wizard's hook into the calibrate_acoustic gate is a clean no-op when
// SetAcousticGateOptions hasn't been called (zero MicDevice). The
// Runner is never invoked, no setPhase happens, no error is returned.
func TestManager_runAcousticGate_NoOpWhenMicEmpty(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}

	var ranRunner bool
	m.acousticGateOpts = AcousticGateOptions{
		MicDevice: "",
		Runner: func(ctx context.Context, _ AcousticGateOptions) error {
			ranRunner = true
			return nil
		},
	}

	m.runAcousticGate(context.Background())

	if ranRunner {
		t.Error("Runner invoked despite empty MicDevice; runAcousticGate should no-op")
	}
	if m.phase == "calibrate_acoustic" {
		t.Errorf("phase advanced to calibrate_acoustic on no-op path: phase=%q", m.phase)
	}
}

// TestManager_runAcousticGate_RunsRunnerWhenMicSet verifies the
// happy-path: SetAcousticGateOptions with a non-empty MicDevice + stub
// Runner causes runAcousticGate to invoke the runner and advance the
// phase to "calibrate_acoustic".
func TestManager_runAcousticGate_RunsRunnerWhenMicSet(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}
	tmp := t.TempDir()

	var ranRunner bool
	m.SetAcousticGateOptions(AcousticGateOptions{
		MicDevice: "hw:CARD=USB,DEV=0",
		Runner: func(ctx context.Context, _ AcousticGateOptions) error {
			ranRunner = true
			return nil
		},
		TempDir: tmp,
		Logger:  logger,
	})

	m.runAcousticGate(context.Background())

	if !ranRunner {
		t.Error("Runner not invoked despite non-empty MicDevice")
	}
	if m.phase != "calibrate_acoustic" {
		t.Errorf("phase = %q, want \"calibrate_acoustic\"", m.phase)
	}
}

// TestManager_runAcousticGate_NonFatalOnRunnerError verifies that a
// runner-returned error is logged and discarded — the wizard MUST NOT
// abort just because optional mic calibration failed. Soft
// fall-through-to-proxy-only contract.
func TestManager_runAcousticGate_NonFatalOnRunnerError(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}
	tmp := t.TempDir()

	m.SetAcousticGateOptions(AcousticGateOptions{
		MicDevice: "hw:CARD=USB,DEV=0",
		Runner: func(ctx context.Context, _ AcousticGateOptions) error {
			return errors.New("simulated ffmpeg failure")
		},
		TempDir: tmp,
		Logger:  logger,
	})

	// Must NOT panic, must NOT propagate the error.
	m.runAcousticGate(context.Background())

	if m.phase != "calibrate_acoustic" {
		t.Errorf("phase = %q, want \"calibrate_acoustic\" (set even on failure)", m.phase)
	}
}

// TestManager_SetAcousticGateOptions_RoundTrip pins the setter's
// thread-safety + value preservation. The setter takes the lock,
// stores the opts, and runAcousticGate reads them under the same lock.
func TestManager_SetAcousticGateOptions_RoundTrip(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	m := &Manager{logger: logger}
	opts := AcousticGateOptions{
		MicDevice: "hw:CARD=Test",
		RefSPL:    94.0,
		Seconds:   30,
		OutPath:   "/tmp/test.json",
	}
	m.SetAcousticGateOptions(opts)

	m.mu.Lock()
	got := m.acousticGateOpts
	m.mu.Unlock()

	if got.MicDevice != opts.MicDevice ||
		got.RefSPL != opts.RefSPL ||
		got.Seconds != opts.Seconds ||
		got.OutPath != opts.OutPath {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, opts)
	}
}

// TestCalibrateAcousticGate_NilRunnerReturnsError binds the misconfiguration
// path: when MicDevice is set but Runner is nil, Body returns an error so
// the wizard wiring layer surfaces the bug instead of silently no-op'ing.
func TestCalibrateAcousticGate_NilRunnerReturnsError(t *testing.T) {
	opts := AcousticGateOptions{
		MicDevice: "hw:CARD=USB,DEV=0",
		Runner:    nil,
		TempDir:   t.TempDir(),
		Logger:    slog.New(slog.DiscardHandler),
	}
	gate := CalibrateAcousticGate(opts)
	err := RunGate(context.Background(), gate, opts.Logger)
	if err == nil {
		t.Fatal("nil Runner with non-empty MicDevice should error")
	}
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is not *GateError: %T", err)
	}
}
