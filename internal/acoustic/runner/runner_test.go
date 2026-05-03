package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGuessMicID_HashesCardName(t *testing.T) {
	cases := []struct {
		name string
		dev  string
		// Same CARD=, different device suffix → same MicID.
		// Different CARD= → different MicID. Long alias goes through
		// SHA-256 to bound the output length to 16 hex chars.
		matchesDev string
		differs    bool
	}{
		{
			name:       "card_with_device_suffix",
			dev:        "hw:CARD=USB,DEV=0",
			matchesDev: "hw:CARD=USB,DEV=1",
			differs:    false, // Same CARD= prefix → same hash.
		},
		{
			name:       "card_only_no_comma",
			dev:        "hw:CARD=Microphones",
			matchesDev: "hw:CARD=Microphones,DEV=0",
			differs:    false,
		},
		{
			name:       "different_cards_differ",
			dev:        "hw:CARD=AlphaMic",
			matchesDev: "hw:CARD=BetaMic",
			differs:    true,
		},
		{
			name:       "no_card_field_passes_full_string",
			dev:        "default",
			matchesDev: "",
			differs:    true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			id := GuessMicID(tc.dev)
			if len(id) != 16 {
				t.Errorf("GuessMicID(%q) length = %d, want 16", tc.dev, len(id))
			}
			if tc.matchesDev == "" {
				return
			}
			id2 := GuessMicID(tc.matchesDev)
			if tc.differs {
				if id == id2 {
					t.Errorf("expected GuessMicID(%q) != GuessMicID(%q); both = %q", tc.dev, tc.matchesDev, id)
				}
			} else {
				if id != id2 {
					t.Errorf("expected GuessMicID(%q) == GuessMicID(%q); got %q vs %q", tc.dev, tc.matchesDev, id, id2)
				}
			}
		})
	}
}

func TestWriteResultJSON_AtomicAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "calibration.json") // forces MkdirAll

	in := Result{
		MicDevice:     "hw:CARD=USB,DEV=0",
		MicID:         "0123456789abcdef",
		RefSPL:        94.0,
		Seconds:       30,
		RawDBFS:       -45.0,
		AWeightedDBFS: -42.0,
		KCalOffset:    139.0,
		CapturedAt:    time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := WriteResultJSON(path, in); err != nil {
		t.Fatalf("WriteResultJSON: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out Result
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}

	// Atomic write: no leftover .tmp file.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file still present after WriteResultJSON: %v", err)
	}
}

func TestRun_RejectsEmptyMicDevice(t *testing.T) {
	_, err := Run(context.Background(), Options{
		MicDevice: "",
		Logger:    slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, ErrNoDevice) {
		t.Errorf("Run(MicDevice=\"\"): err = %v, want ErrNoDevice", err)
	}
}

func TestRun_RejectsOutOfRangeSeconds(t *testing.T) {
	cases := []struct {
		seconds int
		want    bool // want error
	}{
		{4, true},   // below floor
		{5, false},  // boundary OK (note: ffmpeg won't actually run in test, but validation passes first)
		{60, false}, // boundary OK
		{61, true},  // above ceiling
	}
	for _, tc := range cases {
		_, err := Run(context.Background(), Options{
			MicDevice: "hw:CARD=fake",
			Seconds:   tc.seconds,
			Logger:    slog.New(slog.DiscardHandler),
		})
		// We expect either the seconds-validation error (if seconds is
		// out of range) OR an ffmpeg-related error (if seconds is OK
		// and we proceed to capture). The test asserts only that the
		// seconds-validation path fires for out-of-range.
		hadErr := err != nil
		if hadErr != tc.want && tc.want {
			t.Errorf("Run(seconds=%d): err = %v, expected validation error", tc.seconds, err)
		}
		// Specific assertion for out-of-range: error should mention "seconds".
		if tc.want && err != nil {
			msg := err.Error()
			if !contains(msg, "seconds") {
				t.Errorf("Run(seconds=%d): err = %q, expected to mention 'seconds'", tc.seconds, msg)
			}
		}
	}
}

func TestRun_RejectsOutOfRangeRefSPL(t *testing.T) {
	cases := []struct {
		refSPL float64
		want   bool
	}{
		{49, true},  // below floor
		{50, false}, // boundary OK
		{130, false},
		{131, true},
	}
	for _, tc := range cases {
		_, err := Run(context.Background(), Options{
			MicDevice: "hw:CARD=fake",
			Seconds:   30,
			RefSPL:    tc.refSPL,
			Logger:    slog.New(slog.DiscardHandler),
		})
		hadErr := err != nil
		if hadErr != tc.want && tc.want {
			t.Errorf("Run(refSPL=%v): err = %v, expected validation error", tc.refSPL, err)
		}
		if tc.want && err != nil && !contains(err.Error(), "ref-spl") {
			t.Errorf("Run(refSPL=%v): err = %q, expected to mention 'ref-spl'", tc.refSPL, err.Error())
		}
	}
}

func TestRun_AppliesDefaults(t *testing.T) {
	// MicDevice is required (validated first); seconds and refSPL get
	// defaults when zero. We can't run the full pipeline (no ffmpeg),
	// but we can confirm the seconds=0 / refSPL=0 paths don't trip
	// the range validators.
	_, err := Run(context.Background(), Options{
		MicDevice: "hw:CARD=fake",
		Seconds:   0, // → defaults to 30
		RefSPL:    0, // → defaults to 94
		Logger:    slog.New(slog.DiscardHandler),
	})
	// Expect ffmpeg to fail (no ffmpeg in test env, OR ALSA device
	// doesn't exist) — but the validation must pass first. The
	// "out of range" string must NOT be in the error.
	if err != nil && contains(err.Error(), "out of range") {
		t.Errorf("zero-defaults tripped validator: %v", err)
	}
}

// contains is a stripped-down strings.Contains helper avoiding the
// strings import for this small test file.
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
