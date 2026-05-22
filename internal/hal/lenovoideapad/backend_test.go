// SPDX-License-Identifier: GPL-3.0-or-later
package lenovoideapad

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// fixture stages temp files for the platform_profile + choices sysfs
// nodes plus the ideapad / legion module-presence sentinels. Tests
// inspect file contents after Write to verify the exact byte sequence.
type fixture struct {
	dir       string
	profile   string
	choices   string
	ideapad   string // empty when the test wants ideapad_laptop "not loaded"
	legionMod string // empty when test wants legion_laptop "not loaded"
}

func newFixture(t *testing.T, initialProfile, choicesContent string, ideapadLoaded, legionLoaded bool) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{
		dir:     dir,
		profile: filepath.Join(dir, "platform_profile"),
		choices: filepath.Join(dir, "platform_profile_choices"),
	}
	if err := os.WriteFile(f.profile, []byte(initialProfile), 0o644); err != nil {
		t.Fatalf("fixture: write profile: %v", err)
	}
	if err := os.WriteFile(f.choices, []byte(choicesContent), 0o644); err != nil {
		t.Fatalf("fixture: write choices: %v", err)
	}
	if ideapadLoaded {
		f.ideapad = filepath.Join(dir, "module_ideapad_laptop")
		if err := os.Mkdir(f.ideapad, 0o755); err != nil {
			t.Fatalf("fixture: mkdir ideapad: %v", err)
		}
	}
	if legionLoaded {
		f.legionMod = filepath.Join(dir, "module_legion_laptop")
		if err := os.Mkdir(f.legionMod, 0o755); err != nil {
			t.Fatalf("fixture: mkdir legion: %v", err)
		}
	}
	return f
}

func (f *fixture) content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: read %s: %v", path, err)
	}
	return string(b)
}

func (f *fixture) makeChannel() hal.Channel {
	b, _ := os.ReadFile(f.choices)
	choices := make(map[string]bool)
	for _, c := range strings.Fields(string(b)) {
		choices[c] = true
	}
	return hal.Channel{
		ID:   f.profile,
		Role: hal.RoleCPU,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			PlatformProfilePath: f.profile,
			Choices:             choices,
		},
	}
}

const choicesAll = "low-power balanced performance\n"
const choicesNoPerf = "low-power balanced\n"

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-01: pwmToProfile bucket boundaries
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_01_PWMToProfileBucketBoundaries(t *testing.T) {
	tests := []struct {
		pwm  uint8
		want string
	}{
		{0, ProfileLowPower},
		{42, ProfileLowPower},
		{84, ProfileLowPower}, // upper boundary of low-power
		{85, ProfileBalanced},
		{127, ProfileBalanced},
		{170, ProfileBalanced}, // upper boundary of balanced
		{171, ProfilePerformance},
		{213, ProfilePerformance},
		{255, ProfilePerformance},
	}
	for _, tc := range tests {
		if got := pwmToProfile(tc.pwm); got != tc.want {
			t.Errorf("pwmToProfile(%d) = %q; want %q", tc.pwm, got, tc.want)
		}
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-02: profileToPWM inverse mapping round-trips
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_02_ProfileToPWMRoundTrip(t *testing.T) {
	for _, p := range []string{ProfileLowPower, ProfileBalanced, ProfilePerformance} {
		pwm, ok := profileToPWM(p)
		if !ok {
			t.Fatalf("profileToPWM(%q) returned ok=false", p)
		}
		got := pwmToProfile(pwm)
		if got != p {
			t.Errorf("round-trip: profileToPWM(%q)=%d → pwmToProfile=%q; want %q",
				p, pwm, got, p)
		}
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_02_ProfileToPWMUnknownReturnsFalse(t *testing.T) {
	if _, ok := profileToPWM("quiet"); ok {
		t.Errorf("profileToPWM(quiet) returned ok=true; want false (legion's name, not ideapad's)")
	}
	if _, ok := profileToPWM("turbo"); ok {
		t.Errorf("profileToPWM(turbo) returned ok=true; want false")
	}
	if _, ok := profileToPWM(""); ok {
		t.Errorf("profileToPWM(empty) returned ok=true; want false")
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-03: Enumerate happy path + discovery exclusions
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateHappyPath(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true, false)
	b := newBackendForFixture(f)

	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len(channels) = %d; want 1", len(channels))
	}
	ch := channels[0]
	if ch.ID != f.profile {
		t.Errorf("Channel.ID = %q; want %q", ch.ID, f.profile)
	}
	if ch.Role != hal.RoleCPU {
		t.Errorf("Channel.Role = %q; want %q", ch.Role, hal.RoleCPU)
	}
	if (ch.Caps & hal.CapWritePWM) == 0 {
		t.Errorf("Channel.Caps missing CapWritePWM")
	}
	if (ch.Caps & hal.CapRestore) == 0 {
		t.Errorf("Channel.Caps missing CapRestore")
	}
	st, err := stateFrom(ch)
	if err != nil {
		t.Fatalf("stateFrom: %v", err)
	}
	for _, want := range []string{ProfileLowPower, ProfileBalanced, ProfilePerformance} {
		if !st.Choices[want] {
			t.Errorf("Choices missing %q (got %v)", want, st.Choices)
		}
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateAbsentPlatformProfileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	b := NewBackend(nil)
	b.profilePath = filepath.Join(dir, "nonexistent_platform_profile")
	b.choicesPath = filepath.Join(dir, "nonexistent_choices")
	b.ideapadPath = filepath.Join(dir, "module_ideapad_laptop")
	b.legionPath = filepath.Join(dir, "module_legion_laptop")
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("len(channels) = %d; want 0", len(channels))
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateNoIdeapadModuleReturnsEmpty(t *testing.T) {
	// platform_profile + choices present, but ideapad_laptop NOT loaded.
	// This is the "Lenovo Legion-style host" or "ThinkPad" case — defer.
	f := newFixture(t, "balanced\n", choicesAll, false, false)
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("ideapad_laptop absent: len(channels) = %d; want 0", len(channels))
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateLegionPresentReturnsEmpty(t *testing.T) {
	// Hybrid host: both ideapad_laptop AND legion_laptop loaded. Legion
	// owns the channel; we defer.
	f := newFixture(t, "balanced\n", choicesAll, true, true)
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("legion_laptop present: len(channels) = %d; want 0", len(channels))
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateSingleChoiceRefuses(t *testing.T) {
	f := newFixture(t, "balanced\n", "balanced\n", true, false)
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("single-choice enum: len(channels) = %d; want 0", len(channels))
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-04: Read enforces empty-by-construction Reading
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_04_ReadMissingFileReportsOKFalse(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := os.Remove(f.profile); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.OK {
		t.Errorf("Reading.OK = true; want false")
	}
	if r.PWM != 0 || r.RPM != 0 {
		t.Errorf("Reading non-zero on OK=false: %+v", r)
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_04_ReadUnknownProfileReportsOKFalse(t *testing.T) {
	f := newFixture(t, "turbo\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.OK {
		t.Errorf("Reading.OK = true on unknown profile; want false")
	}
	if r.PWM != 0 || r.RPM != 0 {
		t.Errorf("Reading non-zero on OK=false: %+v", r)
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_04_ReadHappyPath(t *testing.T) {
	for _, tc := range []struct {
		fileContent string
		wantPWM     uint8
	}{
		{"low-power\n", 42},
		{"balanced\n", 127},
		{"performance\n", 213},
		{"balanced", 127}, // no trailing newline
	} {
		f := newFixture(t, tc.fileContent, choicesAll, true, false)
		b := newBackendForFixture(f)
		ch := f.makeChannel()
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read(%q): %v", tc.fileContent, err)
		}
		if !r.OK {
			t.Errorf("Reading.OK = false for %q", tc.fileContent)
		}
		if r.PWM != tc.wantPWM {
			t.Errorf("Read(%q): PWM=%d; want %d", tc.fileContent, r.PWM, tc.wantPWM)
		}
		if r.RPM != 0 {
			t.Errorf("Read(%q): RPM=%d; want 0 (no RPM source on IdeaPad)",
				tc.fileContent, r.RPM)
		}
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-05: Read never mutates observable state
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_05_ReadNeverMutatesFile(t *testing.T) {
	const initial = "performance\n"
	f := newFixture(t, initial, choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	for i := 0; i < 5; i++ {
		if _, err := b.Read(ch); err != nil {
			t.Fatalf("Read[%d]: %v", i, err)
		}
	}
	if got := f.content(t, f.profile); got != initial {
		t.Errorf("Read mutated file: got %q; want %q", got, initial)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-06: Write writes the bucketed profile string
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_06_WriteWritesProfileString(t *testing.T) {
	for _, tc := range []struct {
		pwm  uint8
		want string
	}{
		{0, ProfileLowPower},
		{42, ProfileLowPower},
		{127, ProfileBalanced},
		{213, ProfilePerformance},
		{255, ProfilePerformance},
	} {
		f := newFixture(t, "balanced\n", choicesAll, true, false)
		b := newBackendForFixture(f)
		ch := f.makeChannel()
		if err := b.Write(ch, tc.pwm); err != nil {
			t.Fatalf("Write(%d): %v", tc.pwm, err)
		}
		if got := f.content(t, f.profile); got != tc.want {
			t.Errorf("Write(%d) wrote %q; want %q", tc.pwm, got, tc.want)
		}
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-07: Write exhaustive — every uint8 → valid profile
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_07_WriteEveryPWMByteProducesValidProfile(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	for pwm := 0; pwm <= 255; pwm++ {
		if err := b.Write(ch, uint8(pwm)); err != nil {
			t.Fatalf("Write(%d): %v", pwm, err)
		}
		profile := f.content(t, f.profile)
		switch profile {
		case ProfileLowPower, ProfileBalanced, ProfilePerformance:
			// OK
		default:
			t.Fatalf("Write(%d) produced unknown profile %q", pwm, profile)
		}
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-08: Write clamps to choices when target unavailable
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_08_WriteClampsToChoicesWhenTargetUnavailable(t *testing.T) {
	// Choices: only low-power + balanced. PWM=213 (would normally map to
	// performance) must fall back to balanced.
	f := newFixture(t, "balanced\n", choicesNoPerf, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := b.Write(ch, 213); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := f.content(t, f.profile); got != ProfileBalanced {
		t.Errorf("clamp fallback: got %q; want %q", got, ProfileBalanced)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-09: Write EPERM wraps ErrPlatformProfileRefused
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_09_WriteEPERMWrapsAsRefused(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	b.writeFile = func(string, []byte, os.FileMode) error {
		return &fs.PathError{Op: "write", Path: f.profile, Err: syscall.EPERM}
	}
	ch := f.makeChannel()
	err := b.Write(ch, 213)
	if err == nil {
		t.Fatalf("Write: nil error; want EPERM wrap")
	}
	if !errors.Is(err, ErrPlatformProfileRefused) {
		t.Errorf("err not ErrPlatformProfileRefused: %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err not fs.ErrPermission via wrap: %v", err)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-10: Restore writes balanced + clears acquired flag
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_10_RestoreWritesBalanced(t *testing.T) {
	f := newFixture(t, "performance\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	// Populate acquired by Writing performance first.
	if err := b.Write(ch, 213); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := f.content(t, f.profile); got != ProfileBalanced {
		t.Errorf("restore profile: %q; want %q", got, ProfileBalanced)
	}
	if _, ok := b.acquired.Load(f.profile); ok {
		t.Errorf("Restore did not clear acquired flag")
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_10_RestoreSafeOnUnwrittenChannel(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := b.Restore(ch); err != nil {
		t.Errorf("Restore on unwritten channel: %v", err)
	}
	// Channel is still balanced (Restore writes it again, idempotent).
	if got := f.content(t, f.profile); got != ProfileBalanced {
		t.Errorf("restore profile: %q; want %q", got, ProfileBalanced)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-11: Close idempotent; Name returns stable tag
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_11_CloseIdempotent(t *testing.T) {
	b := NewBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("Close[1]: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close[2]: %v", err)
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_11_NameStableConstant(t *testing.T) {
	b := NewBackend(nil)
	if got := b.Name(); got != BackendName {
		t.Errorf("Name() = %q; want %q", got, BackendName)
	}
	if BackendName != "lenovoideapad" {
		t.Errorf("BackendName = %q; want %q", BackendName, "lenovoideapad")
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LENOVO-IDEAPAD-12: stateFrom refuses bad opaque + accepts both forms
// ------------------------------------------------------------------

func TestRULE_HAL_LENOVO_IDEAPAD_12_StateFromRejectsBadOpaque(t *testing.T) {
	for _, tc := range []struct {
		name string
		ch   hal.Channel
	}{
		{"wrong-type-int", hal.Channel{ID: "x", Opaque: 42}},
		{"wrong-type-string", hal.Channel{ID: "x", Opaque: "balanced"}},
		{"nil-pointer", hal.Channel{ID: "x", Opaque: (*State)(nil)}},
		{"empty-path-value", hal.Channel{ID: "x", Opaque: State{}}},
		{"empty-path-pointer", hal.Channel{ID: "x", Opaque: &State{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stateFrom(tc.ch); err == nil {
				t.Errorf("stateFrom: nil error; want refusal for %s", tc.name)
			}
		})
	}
}

func TestRULE_HAL_LENOVO_IDEAPAD_12_StateFromAcceptsValueAndPointer(t *testing.T) {
	st := State{PlatformProfilePath: "/x"}
	if _, err := stateFrom(hal.Channel{Opaque: st}); err != nil {
		t.Errorf("value-form: %v", err)
	}
	if _, err := stateFrom(hal.Channel{Opaque: &st}); err != nil {
		t.Errorf("pointer-form: %v", err)
	}
}

// ------------------------------------------------------------------
// helper: a backend that uses the fixture's paths
// ------------------------------------------------------------------

func newBackendForFixture(f *fixture) *Backend {
	b := NewBackend(nil)
	b.profilePath = f.profile
	b.choicesPath = f.choices
	if f.ideapad != "" {
		b.ideapadPath = f.ideapad
	} else {
		b.ideapadPath = filepath.Join(f.dir, "module_ideapad_laptop")
	}
	if f.legionMod != "" {
		b.legionPath = f.legionMod
	} else {
		b.legionPath = filepath.Join(f.dir, "module_legion_laptop")
	}
	return b
}

// silence the unused-import linter for sync (Backend.acquired uses it
// transitively but the test file doesn't reference sync directly except
// via the *sync.Map type assertion that the linter sees through).
var _ = sync.Map{}
