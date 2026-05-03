package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/setup"
)

// TestAcousticOptionsFromFlags pins the flag → struct mapping. A
// regression here would mean operator-supplied --mic / --mic-ref-spl /
// --mic-seconds / --mic-out values silently fail to reach the wizard.
func TestAcousticOptionsFromFlags(t *testing.T) {
	got := acousticOptionsFromFlags("hw:CARD=USB,DEV=0", 114.0, 45, "/var/lib/ventd/acoustic.json")
	want := acousticOptions{
		MicDevice: "hw:CARD=USB,DEV=0",
		RefSPL:    114.0,
		Seconds:   45,
		OutPath:   "/var/lib/ventd/acoustic.json",
	}
	if got != want {
		t.Errorf("acousticOptionsFromFlags: got %+v, want %+v", got, want)
	}
}

// TestMakeAcousticRunner_DispatchesToRunnerRun verifies the adapter
// satisfies setup.AcousticRunner and routes the gate's
// AcousticGateOptions into runner.Run with field-for-field fidelity.
//
// We can't drive runner.Run end-to-end without ffmpeg + a real mic, but
// we can confirm the validator path: passing an empty MicDevice through
// the adapter must surface as runner.ErrNoDevice.
func TestMakeAcousticRunner_DispatchesToRunnerRun(t *testing.T) {
	r := makeAcousticRunner()
	if r == nil {
		t.Fatal("makeAcousticRunner returned nil")
	}

	// Empty MicDevice → adapter passes it to runner.Run → ErrNoDevice.
	err := r(context.Background(), setup.AcousticGateOptions{
		MicDevice: "",
		Logger:    slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, runner.ErrNoDevice) {
		t.Errorf("empty MicDevice through adapter: err = %v, want runner.ErrNoDevice", err)
	}
}

// TestMakeAcousticRunner_ForwardsOptions verifies the adapter lifts the
// MicDevice / RefSPL / Seconds / OutPath fields from the gate's opts
// onto runner.Options. We exercise the validation path: out-of-range
// Seconds in the gate's opts must surface as runner.Run's
// "out of range" error.
func TestMakeAcousticRunner_ForwardsOptions(t *testing.T) {
	r := makeAcousticRunner()
	err := r(context.Background(), setup.AcousticGateOptions{
		MicDevice: "hw:CARD=fake",
		Seconds:   3, // below runner's [5, 60] floor
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err == nil {
		t.Fatal("Seconds=3 should fail runner.Run validation")
	}
	if msg := err.Error(); !contains(msg, "seconds") || !contains(msg, "out of range") {
		t.Errorf("err = %q, expected to mention 'seconds' and 'out of range'", msg)
	}
}

// contains is a minimal strings.Contains-equivalent for this test file.
func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
