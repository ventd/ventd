// SPDX-License-Identifier: GPL-3.0-or-later
package msiec

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// seedSysfs builds a hermetic fixture under t.TempDir() that mirrors
// the msi-ec driver's sysfs layout for a CONF_G2_6-style MSI laptop
// (the firmware group covering Hudson's MS-16R8 / Thin GF63 12UDX in
// issue #1154). Returns the temp-root path; the caller seeds with the
// desired fan_mode / available_fan_modes / temperature.
func seedSysfs(t *testing.T, fanMode, available, cpuTemp string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cpu"), 0o755); err != nil {
		t.Fatalf("mkdir cpu: %v", err)
	}
	if fanMode != "" {
		if err := os.WriteFile(filepath.Join(root, "fan_mode"), []byte(fanMode), 0o644); err != nil {
			t.Fatalf("seed fan_mode: %v", err)
		}
	}
	if available != "" {
		if err := os.WriteFile(filepath.Join(root, "available_fan_modes"), []byte(available), 0o644); err != nil {
			t.Fatalf("seed available_fan_modes: %v", err)
		}
	}
	if cpuTemp != "" {
		if err := os.WriteFile(filepath.Join(root, "cpu", "realtime_temperature"), []byte(cpuTemp), 0o644); err != nil {
			t.Fatalf("seed cpu/realtime_temperature: %v", err)
		}
	}
	return root
}

// newBackendWithRoot returns a Backend with its DefaultSysfsRoot
// overridden via the channel's Opaque state. Avoids monkey-patching a
// package-level var.
func newBackendWithRoot(_ *testing.T) *Backend {
	return NewBackend(slog.New(slog.DiscardHandler))
}

func TestReadAvailableModes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		// Hudson's MS-16R8 / Thin GF63 12UDX firmware group CONF_G2_6
		// exposes auto + silent + advanced (no basic — that's reserved
		// for the manual-duty groups).
		{"conf_g2_6 newline", "auto\nsilent\nadvanced\n", []string{"auto", "silent", "advanced"}},
		// Space-separated tolerance — historical tooling format.
		{"space separated", "auto silent advanced", []string{"auto", "silent", "advanced"}},
		// All four modes (CONF_G1 style with manual basic mode).
		{"four modes", "auto\nbasic\nsilent\nadvanced\n", []string{"auto", "silent", "basic", "advanced"}},
		// Forward-compat: unknown mode appended after the canonical
		// set, sorted alphabetically.
		{"unknown mode last", "auto\nsilent\nfuturemode\n", []string{"auto", "silent", "futuremode"}},
		// Duplicate handling.
		{"duplicates collapsed", "auto\nauto\nsilent\n", []string{"auto", "silent"}},
		// Only auto — handled at filter step; readAvailableModes
		// still returns it honestly.
		{"only auto", "auto\n", []string{"auto"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := seedSysfs(t, "", tc.raw, "")
			got, err := readAvailableModes(root)
			if err != nil {
				t.Fatalf("readAvailableModes: %v", err)
			}
			if !equalStrings(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}

	t.Run("missing file errors", func(t *testing.T) {
		root := t.TempDir()
		_, err := readAvailableModes(root)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("want fs.ErrNotExist, got %v", err)
		}
	})
}

func TestFilterWritableModes(t *testing.T) {
	cases := []struct {
		in, out []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"auto"}, []string{}},
		{[]string{"auto", "silent", "advanced"}, []string{"silent", "advanced"}},
		{[]string{"auto", "silent", "basic", "advanced"}, []string{"silent", "basic", "advanced"}},
		// Defence: filter is order-preserving — silent stays before
		// advanced regardless of where auto sits.
		{[]string{"silent", "auto", "advanced"}, []string{"silent", "advanced"}},
	}
	for _, tc := range cases {
		got := filterWritableModes(tc.in)
		if !equalStrings(got, tc.out) {
			t.Errorf("in=%v: got %v want %v", tc.in, got, tc.out)
		}
	}
}

func TestPWMToMode(t *testing.T) {
	// CONF_G2_6 case (Hudson's box): 2 writable modes after filtering
	// out auto — silent and advanced. Split at PWM 128.
	t.Run("two modes split at 128", func(t *testing.T) {
		modes := []string{"silent", "advanced"}
		cases := []struct {
			pwm  uint8
			want string
		}{
			{0, "silent"},
			{127, "silent"},
			{128, "advanced"},
			{255, "advanced"},
		}
		for _, tc := range cases {
			if got := pwmToMode(tc.pwm, modes); got != tc.want {
				t.Errorf("pwm=%d got=%q want=%q", tc.pwm, got, tc.want)
			}
		}
	})

	// Three-mode case (silent / basic / advanced from CONF_G1).
	// Bands fall where floor(pwm * 3 / 256) flips: [0,85] silent,
	// [86,170] basic, [171,255] advanced. The exact boundary is at
	// pwm=86 (86*3/256 = 258/256 = 1) and pwm=171 (171*3/256 = 513/256 = 2).
	t.Run("three modes band thirds", func(t *testing.T) {
		modes := []string{"silent", "basic", "advanced"}
		cases := []struct {
			pwm  uint8
			want string
		}{
			{0, "silent"},
			{85, "silent"},
			{86, "basic"},
			{170, "basic"},
			{171, "advanced"},
			{255, "advanced"},
		}
		for _, tc := range cases {
			if got := pwmToMode(tc.pwm, modes); got != tc.want {
				t.Errorf("pwm=%d got=%q want=%q", tc.pwm, got, tc.want)
			}
		}
	})

	t.Run("empty modes returns empty string", func(t *testing.T) {
		if got := pwmToMode(128, nil); got != "" {
			t.Errorf("want empty string, got %q", got)
		}
	})
}

func TestModeToPWM(t *testing.T) {
	t.Run("auto maps to 0 sentinel", func(t *testing.T) {
		got, err := modeToPWM(ModeAuto, []string{"silent", "advanced"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != 0 {
			t.Errorf("auto: got %d want 0", got)
		}
	})

	// CONF_G2_6 round-trip: write→read→compare for both writable modes.
	t.Run("two-mode band centres", func(t *testing.T) {
		modes := []string{"silent", "advanced"}
		// Centres at PWM 64 and PWM 192 (midpoints of the two 128-wide bands).
		cases := []struct {
			mode string
			want uint8
		}{
			{"silent", 64},
			{"advanced", 192},
		}
		for _, tc := range cases {
			got, err := modeToPWM(tc.mode, modes)
			if err != nil {
				t.Fatalf("modeToPWM(%q): %v", tc.mode, err)
			}
			if got != tc.want {
				t.Errorf("mode=%q got=%d want=%d", tc.mode, got, tc.want)
			}
		}
	})

	t.Run("write-then-read round-trip is stable", func(t *testing.T) {
		modes := []string{"silent", "advanced"}
		// For every PWM in [0, 255], pwmToMode then modeToPWM should
		// land on the same band centre.
		for pwm := 0; pwm <= 255; pwm++ {
			mode := pwmToMode(uint8(pwm), modes)
			roundtrip, err := modeToPWM(mode, modes)
			if err != nil {
				t.Fatalf("modeToPWM(%q): %v", mode, err)
			}
			// All PWMs in the silent band [0, 128) must round-trip to 64;
			// all PWMs in advanced [128, 256) to 192.
			want := uint8(64)
			if pwm >= 128 {
				want = 192
			}
			if roundtrip != want {
				t.Errorf("pwm=%d mode=%q roundtrip=%d want=%d", pwm, mode, roundtrip, want)
			}
		}
	})

	t.Run("unknown mode returns ErrInvalidFanMode", func(t *testing.T) {
		_, err := modeToPWM("nonsense", []string{"silent", "advanced"})
		if !errors.Is(err, ErrInvalidFanMode) {
			t.Errorf("want ErrInvalidFanMode, got %v", err)
		}
	})
}

func TestEnumerate(t *testing.T) {
	t.Run("absent sysfs returns nil nil", func(t *testing.T) {
		b := newBackendWithRoot(t)
		// Hardcoded DefaultSysfsRoot won't exist on the CI runner.
		// Confirm Enumerate handles fs.ErrNotExist as a non-error
		// "not present" so the registry's fan-out admits it.
		got, err := b.Enumerate(context.Background())
		if err != nil {
			t.Fatalf("Enumerate on absent host: %v", err)
		}
		if got != nil {
			t.Errorf("want nil channels on absent host, got %d", len(got))
		}
	})

	t.Run("context cancel surfaces", func(t *testing.T) {
		b := newBackendWithRoot(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := b.Enumerate(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled, got %v", err)
		}
	})
}

func TestReadWriteRestoreRoundtrip(t *testing.T) {
	root := seedSysfs(t, "auto\n", "auto\nsilent\nadvanced\n", "56\n")
	b := newBackendWithRoot(t)
	ch := hal.Channel{
		ID:   root,
		Role: hal.RoleCPU,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			SysfsRoot:     root,
			WritableModes: []string{"silent", "advanced"},
		},
	}

	// Initial read: fan_mode=auto → PWM 0 sentinel + cpu temp 56.
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read#1: %v", err)
	}
	if !r.OK {
		t.Fatal("Read#1: OK=false")
	}
	if r.PWM != 0 {
		t.Errorf("Read#1 PWM=%d want 0 (auto sentinel)", r.PWM)
	}
	if r.Temp != 56 {
		t.Errorf("Read#1 Temp=%v want 56", r.Temp)
	}
	if r.RPM != 0 {
		t.Errorf("Read#1 RPM=%d — backend must NOT fabricate RPM from percentage", r.RPM)
	}

	// Write PWM=200 → quantises to "advanced" in 2-mode split.
	if err := b.Write(ch, 200); err != nil {
		t.Fatalf("Write 200: %v", err)
	}
	modeBytes, _ := os.ReadFile(filepath.Join(root, "fan_mode"))
	if got := strings.TrimSpace(string(modeBytes)); got != "advanced" {
		t.Errorf("after Write(200) fan_mode=%q want advanced", got)
	}

	// Read back: "advanced" → centre 192.
	r2, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read#2: %v", err)
	}
	if r2.PWM != 192 {
		t.Errorf("Read#2 PWM=%d want 192 (advanced centre)", r2.PWM)
	}

	// Restore → "auto".
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	modeBytes, _ = os.ReadFile(filepath.Join(root, "fan_mode"))
	if got := strings.TrimSpace(string(modeBytes)); got != "auto" {
		t.Errorf("after Restore fan_mode=%q want auto", got)
	}
}

func TestWriteReturnsErrorOnSysfsFailure(t *testing.T) {
	root := seedSysfs(t, "auto\n", "auto\nsilent\nadvanced\n", "")
	b := newBackendWithRoot(t)
	stubErr := errors.New("simulated sysfs write failure")
	b.writeFile = func(_ string, _ []byte, _ os.FileMode) error { return stubErr }
	ch := hal.Channel{
		ID: root,
		Opaque: State{
			SysfsRoot:     root,
			WritableModes: []string{"silent", "advanced"},
		},
	}
	err := b.Write(ch, 200)
	if !errors.Is(err, stubErr) {
		t.Errorf("want wrapped stubErr, got %v", err)
	}
}

func TestStateFromRejectsBadOpaque(t *testing.T) {
	cases := []struct {
		name string
		ch   hal.Channel
	}{
		{"nil opaque", hal.Channel{ID: "x"}},
		{"wrong type", hal.Channel{ID: "x", Opaque: "not a state"}},
		{"value with empty root", hal.Channel{ID: "x", Opaque: State{}}},
		{"pointer with empty root", hal.Channel{ID: "x", Opaque: &State{}}},
		{"nil pointer", hal.Channel{ID: "x", Opaque: (*State)(nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stateFrom(tc.ch); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestBackendNameAndClose(t *testing.T) {
	b := NewBackend(nil) // nil logger → slog.Default
	if b.Name() != BackendName {
		t.Errorf("Name=%q want %q", b.Name(), BackendName)
	}
	// Close is idempotent.
	if err := b.Close(); err != nil {
		t.Errorf("Close#1: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close#2: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
