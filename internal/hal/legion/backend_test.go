// SPDX-License-Identifier: GPL-3.0-or-later
package legion

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

// fixture stages temp files for the platform_profile + choices + optional
// powermode sysfs nodes, plus the legion_laptop module marker (a placeholder
// file standing in for /sys/module/legion_laptop). Tests inspect file
// contents after Write to verify the exact byte sequence.
type fixture struct {
	dir          string
	profile      string
	choices      string
	powermode    string // empty when test wants Enumerate to NOT include powermode
	legionModule string // empty when test wants Enumerate to NOT see the legion module
}

func newFixture(t *testing.T, initialProfile, choicesContent string, withPowermode bool) *fixture {
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
	if withPowermode {
		f.powermode = filepath.Join(dir, "powermode")
		if err := os.WriteFile(f.powermode, []byte("1\n"), 0o644); err != nil {
			t.Fatalf("fixture: write powermode: %v", err)
		}
	}
	// Default to "legion_laptop is loaded" so every existing test that
	// expected a happy-path Enumerate keeps passing. Tests that want to
	// simulate a non-Legion host (#1410) clear f.legionModule before
	// running Enumerate.
	f.legionModule = filepath.Join(dir, "sys-module-legion_laptop")
	if err := os.WriteFile(f.legionModule, []byte{}, 0o644); err != nil {
		t.Fatalf("fixture: write legion_laptop marker: %v", err)
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
			PowermodePath:       f.powermode,
			Choices:             choices,
		},
	}
}

const choicesAll = "quiet balanced performance\n"
const choicesNoPerf = "quiet balanced\n"

// ------------------------------------------------------------------
// RULE-HAL-LEGION-01: pwmToProfile bucket boundaries
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_01_PWMToProfileBucketBoundaries(t *testing.T) {
	tests := []struct {
		pwm  uint8
		want string
	}{
		{0, ProfileQuiet},
		{42, ProfileQuiet},
		{84, ProfileQuiet}, // upper boundary of quiet
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
// RULE-HAL-LEGION-02: profileToPWM inverse mapping round-trips
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_02_ProfileToPWMRoundTrip(t *testing.T) {
	for _, p := range []string{ProfileQuiet, ProfileBalanced, ProfilePerformance} {
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

func TestRULE_HAL_LEGION_02_ProfileToPWMUnknownReturnsFalse(t *testing.T) {
	if _, ok := profileToPWM("turbo"); ok {
		t.Errorf("profileToPWM(turbo) returned ok=true; want false")
	}
	if _, ok := profileToPWM(""); ok {
		t.Errorf("profileToPWM(empty) returned ok=true; want false")
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-03: Enumerate happy path returns a single channel
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_03_EnumerateHappyPath(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true)
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
	if st.PowermodePath != f.powermode {
		t.Errorf("State.PowermodePath = %q; want %q", st.PowermodePath, f.powermode)
	}
	for _, want := range []string{ProfileQuiet, ProfileBalanced, ProfilePerformance} {
		if !st.Choices[want] {
			t.Errorf("Choices missing %q (got %v)", want, st.Choices)
		}
	}
}

func TestRULE_HAL_LEGION_03_EnumerateAbsentReturnsEmpty(t *testing.T) {
	// Point the backend at a non-existent path so statFile returns ENOENT.
	dir := t.TempDir()
	b := NewBackend(nil)
	b.profilePath = filepath.Join(dir, "nonexistent_platform_profile")
	b.choicesPath = filepath.Join(dir, "nonexistent_choices")
	b.powermodePath = ""
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("len(channels) = %d; want 0", len(channels))
	}
}

func TestRULE_HAL_LEGION_03_EnumerateNoPowermode(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len(channels) = %d; want 1", len(channels))
	}
	st, _ := stateFrom(channels[0])
	if st.PowermodePath != "" {
		t.Errorf("PowermodePath = %q; want empty", st.PowermodePath)
	}
}

func TestRULE_HAL_LEGION_03_EnumerateSingleChoiceRefuses(t *testing.T) {
	// platform_profile_choices with a single value is degenerate.
	f := newFixture(t, "balanced\n", "balanced\n", true)
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("single-choice enum: len(channels) = %d; want 0", len(channels))
	}
}

func TestRULE_HAL_LEGION_03_EnumerateNoLegionModuleReturnsEmpty(t *testing.T) {
	// Simulate a host where platform_profile + choices exist (Dell, HP,
	// ASUS, Framework, etc. — any vendor that wires up the kernel-generic
	// interface) but legion_laptop is NOT loaded. The backend must stand
	// down so it doesn't surface a phantom "Legion Fan" the controller
	// can't drive. Regression for #1410.
	f := newFixture(t, "balanced\n", choicesAll, true)
	// Remove the legion_laptop module marker so statFile returns ENOENT.
	if err := os.Remove(f.legionModule); err != nil {
		t.Fatalf("remove legion module marker: %v", err)
	}
	b := newBackendForFixture(f)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("no legion_laptop: len(channels) = %d; want 0 (#1410)", len(channels))
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-04: Read returns OK=false on missing file
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_04_ReadMissingFileReportsOKFalse(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	// Delete the file before Read.
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

func TestRULE_HAL_LEGION_04_ReadUnknownProfileReportsOKFalse(t *testing.T) {
	f := newFixture(t, "turbo\n", choicesAll, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.OK {
		t.Errorf("Reading.OK = true on unknown profile; want false")
	}
}

func TestRULE_HAL_LEGION_04_ReadHappyPath(t *testing.T) {
	for _, tc := range []struct {
		fileContent string
		wantPWM     uint8
	}{
		{"quiet\n", 42},
		{"balanced\n", 127},
		{"performance\n", 213},
		{"balanced", 127}, // no trailing newline
	} {
		f := newFixture(t, tc.fileContent, choicesAll, false)
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
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-05: Read never mutates observable state
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_05_ReadNeverMutatesFile(t *testing.T) {
	const initial = "performance\n"
	f := newFixture(t, initial, choicesAll, false)
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
// RULE-HAL-LEGION-06: Write dispatches both platform_profile and powermode
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_06_WriteDispatchesBothNodes(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, true)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := b.Write(ch, 213); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := f.content(t, f.profile); got != ProfilePerformance {
		t.Errorf("profile file: %q; want %q", got, ProfilePerformance)
	}
	if got := f.content(t, f.powermode); got != PowermodePerformance {
		t.Errorf("powermode file: %q; want %q", got, PowermodePerformance)
	}
}

func TestRULE_HAL_LEGION_06_WriteSkipsPowermodeWhenAbsent(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := b.Write(ch, 42); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := f.content(t, f.profile); got != ProfileQuiet {
		t.Errorf("profile file: %q; want %q", got, ProfileQuiet)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-07: Write bucketing covers every PWM byte to a valid profile
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_07_WriteEveryPWMByteProducesValidProfile(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	for pwm := 0; pwm <= 255; pwm++ {
		if err := b.Write(ch, uint8(pwm)); err != nil {
			t.Fatalf("Write(%d): %v", pwm, err)
		}
		profile := f.content(t, f.profile)
		switch profile {
		case ProfileQuiet, ProfileBalanced, ProfilePerformance:
			// OK
		default:
			t.Fatalf("Write(%d) produced unknown profile %q", pwm, profile)
		}
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-08: Write clamps to choices when target unavailable
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_08_WriteClampsToChoicesWhenTargetUnavailable(t *testing.T) {
	// Choices: only quiet + balanced. A PWM of 213 (which would
	// normally map to performance) must fall back to balanced.
	f := newFixture(t, "balanced\n", choicesNoPerf, false)
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
// RULE-HAL-LEGION-09: Write EPERM wraps ErrPlatformProfileRefused
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_09_WriteEPERMWrapsAsRefused(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	// Inject failing writeFile that returns EPERM.
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
// RULE-HAL-LEGION-10: Restore writes balanced + clears acquired flag
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_10_RestoreWritesBalanced(t *testing.T) {
	f := newFixture(t, "performance\n", choicesAll, true)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	// Write performance first to populate acquired.
	if err := b.Write(ch, 213); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := f.content(t, f.profile); got != ProfileBalanced {
		t.Errorf("restore profile: %q; want %q", got, ProfileBalanced)
	}
	if got := f.content(t, f.powermode); got != PowermodeBalanced {
		t.Errorf("restore powermode: %q; want %q", got, PowermodeBalanced)
	}
	if _, ok := b.acquired.Load(f.profile); ok {
		t.Errorf("Restore did not clear acquired flag")
	}
}

func TestRULE_HAL_LEGION_10_RestoreSafeOnUnwrittenChannel(t *testing.T) {
	f := newFixture(t, "balanced\n", choicesAll, false)
	b := newBackendForFixture(f)
	ch := f.makeChannel()
	if err := b.Restore(ch); err != nil {
		t.Errorf("Restore on unwritten channel: %v", err)
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-11: Close + Name + Backend identity
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_11_CloseIdempotent(t *testing.T) {
	b := NewBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("Close[1]: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close[2]: %v", err)
	}
}

func TestRULE_HAL_LEGION_11_NameStableConstant(t *testing.T) {
	b := NewBackend(nil)
	if got := b.Name(); got != BackendName {
		t.Errorf("Name() = %q; want %q", got, BackendName)
	}
	if BackendName != "legion" {
		t.Errorf("BackendName = %q; want %q", BackendName, "legion")
	}
}

// ------------------------------------------------------------------
// RULE-HAL-LEGION-12: stateFrom refuses wrong opaque type + empty path
// ------------------------------------------------------------------

func TestRULE_HAL_LEGION_12_StateFromRejectsBadOpaque(t *testing.T) {
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

func TestRULE_HAL_LEGION_12_StateFromAcceptsValueAndPointer(t *testing.T) {
	st := State{PlatformProfilePath: "/x"}
	if _, err := stateFrom(hal.Channel{Opaque: st}); err != nil {
		t.Errorf("value-form: %v", err)
	}
	if _, err := stateFrom(hal.Channel{Opaque: &st}); err != nil {
		t.Errorf("pointer-form: %v", err)
	}
}

// ------------------------------------------------------------------
// helper: a backend that uses the fixture's read/write/stat
// ------------------------------------------------------------------

func newBackendForFixture(f *fixture) *Backend {
	b := NewBackend(nil)
	b.profilePath = f.profile
	b.choicesPath = f.choices
	b.powermodePath = f.powermode
	b.legionModulePath = f.legionModule
	return b
}

// silence the unused-import linter for sync (Backend.acquired uses it
// transitively but the test file doesn't reference sync directly except
// via the *sync.Map type assertion that the linter sees through).
var _ = sync.Map{}
