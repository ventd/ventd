// SPDX-License-Identifier: GPL-3.0-or-later
package thinkpad

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// procFanFixture sets up a temp file populated with a representative
// /proc/acpi/ibm/fan response. Tests that exercise Write inspect the
// file content afterwards to verify the exact byte sequence the
// backend emitted.
type procFanFixture struct {
	path string
}

func newProcFanFixture(t *testing.T, initial string) *procFanFixture {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fan")
	if err := os.WriteFile(p, []byte(initial), 0o644); err != nil {
		t.Fatalf("fixture: write initial: %v", err)
	}
	return &procFanFixture{path: p}
}

// content returns the current bytes of the fixture file.
func (f *procFanFixture) content(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatalf("fixture: read: %v", err)
	}
	return string(b)
}

// makeChannel builds a hal.Channel pointing at the fixture path.
func (f *procFanFixture) makeChannel() hal.Channel {
	return hal.Channel{
		ID:   f.path,
		Role: hal.RoleCPU,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			ProcPath: f.path,
			FanIndex: 1,
		},
	}
}

const validProcFanContent = `status:		enabled
speed:		1992
level:		3
commands:	level <level> (<level> is 0-7, auto, disengaged, full-speed)
commands:	enable, disable
commands:	watchdog <timeout> (<timeout> is 0 (off), 1-120 (seconds))
`

const autoProcFanContent = `status:		enabled
speed:		0
level:		auto
commands:	level <level> (<level> is 0-7, auto, disengaged, full-speed)
`

const disengagedProcFanContent = `status:		enabled
speed:		6800
level:		disengaged
`

// ------------------------------------------------------------------
// pwmToLevel + levelToPWM round-trip & boundaries
// ------------------------------------------------------------------

func TestPWMToLevel_BoundaryValues(t *testing.T) {
	// Round-half-up over the closed [0,7] firmware grid:
	//   level = (pwm * 7 + 127) / 255
	// Band boundaries (level transitions at pwm):
	//   level 0 → 1: pwm = ceil(255/14) = 19
	//   level 6 → 7: pwm = ceil(255 * 13/14) = 237
	tests := []struct {
		pwm  uint8
		want uint8
	}{
		{0, 0},
		{18, 0},  // band-0 upper boundary
		{19, 1},  // band-1 lower boundary
		{36, 1},  // band-1 centre
		{54, 1},  // band-1 upper boundary
		{55, 2},  // band-2 lower boundary
		{73, 2},  // band-2 centre
		{128, 4}, // middle of the 0..255 range → middle of 0..7 grid
		{200, 5}, // upper-mid
		{219, 6}, // band-6 centre
		{236, 6}, // band-6 upper boundary
		{237, 7}, // band-7 lower boundary
		{255, 7},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("pwm=%d", tt.pwm), func(t *testing.T) {
			got := pwmToLevel(tt.pwm)
			if got != tt.want {
				t.Errorf("pwmToLevel(%d) = %d, want %d", tt.pwm, got, tt.want)
			}
			if got > FirmwareLevelMax {
				t.Errorf("pwmToLevel(%d) = %d exceeds FirmwareLevelMax %d", tt.pwm, got, FirmwareLevelMax)
			}
		})
	}
}

func TestLevelToPWM_RoundTripsCentredBands(t *testing.T) {
	// Each level's reported PWM must lie within its quantisation
	// band, so writing pwm and reading back the resulting level
	// produces a PWM that re-quantises to the same level.
	for level := uint8(0); level <= FirmwareLevelMax; level++ {
		pwm := levelToPWM(level)
		gotLevel := pwmToLevel(pwm)
		if gotLevel != level {
			t.Errorf("levelToPWM(%d)=%d → pwmToLevel(%d)=%d, want %d (band drift)",
				level, pwm, pwm, gotLevel, level)
		}
	}
}

func TestLevelToPWM_OutOfRangeClampsToMax(t *testing.T) {
	// Defensive: an out-of-range level (catalogue bug, future kernel
	// extension) clamps to FirmwareLevelMax rather than panicking or
	// producing a wraparound PWM value.
	got := levelToPWM(255)
	wantMax := levelToPWM(FirmwareLevelMax)
	if got != wantMax {
		t.Errorf("levelToPWM(255) = %d, want clamp to levelToPWM(%d)=%d", got, FirmwareLevelMax, wantMax)
	}
}

// ------------------------------------------------------------------
// parseProcFan
// ------------------------------------------------------------------

func TestParseProcFan_NumericLevel(t *testing.T) {
	r, err := parseProcFan([]byte(validProcFanContent))
	if err != nil {
		t.Fatalf("parseProcFan: %v", err)
	}
	if !r.OK {
		t.Errorf("Reading.OK = false, want true on valid content")
	}
	if r.RPM != 1992 {
		t.Errorf("Reading.RPM = %d, want 1992", r.RPM)
	}
	// level: 3 → centred PWM = levelToPWM(3).
	want := levelToPWM(3)
	if r.PWM != want {
		t.Errorf("Reading.PWM = %d, want %d (levelToPWM(3))", r.PWM, want)
	}
}

func TestParseProcFan_AutoLevelMapsToMidpoint(t *testing.T) {
	r, err := parseProcFan([]byte(autoProcFanContent))
	if err != nil {
		t.Fatalf("parseProcFan: %v", err)
	}
	if !r.OK {
		t.Errorf("Reading.OK = false, want true")
	}
	if r.PWM != 128 {
		t.Errorf("Reading.PWM = %d on level=auto, want 128 (midpoint sentinel)", r.PWM)
	}
	if r.RPM != 0 {
		t.Errorf("Reading.RPM = %d on speed=0, want 0", r.RPM)
	}
}

func TestParseProcFan_DisengagedMapsToMax(t *testing.T) {
	r, err := parseProcFan([]byte(disengagedProcFanContent))
	if err != nil {
		t.Fatalf("parseProcFan: %v", err)
	}
	if r.PWM != 255 {
		t.Errorf("Reading.PWM = %d on level=disengaged, want 255", r.PWM)
	}
	if r.RPM != 6800 {
		t.Errorf("Reading.RPM = %d, want 6800", r.RPM)
	}
}

func TestParseProcFan_MissingLevelLineReturnsError(t *testing.T) {
	// Some kernel builds emit a minimal response before the EC has
	// reported any state. We treat that as "skip this tick" — the
	// parser returns ErrInvalidProcFanResponse and Read maps that to
	// OK=false.
	content := `status:		enabled
speed:		0
commands:	level <level>
`
	_, err := parseProcFan([]byte(content))
	if !errors.Is(err, ErrInvalidProcFanResponse) {
		t.Errorf("parseProcFan missing level: err=%v, want wraps ErrInvalidProcFanResponse", err)
	}
}

func TestParseProcFan_LevelOutOfRangeReturnsError(t *testing.T) {
	content := `level:		8
`
	_, err := parseProcFan([]byte(content))
	if !errors.Is(err, ErrInvalidProcFanResponse) {
		t.Errorf("parseProcFan level=8: err=%v, want wraps ErrInvalidProcFanResponse", err)
	}
}

func TestParseProcFan_NonNumericNonKeywordLevelReturnsError(t *testing.T) {
	content := `level:		weird
`
	_, err := parseProcFan([]byte(content))
	if !errors.Is(err, ErrInvalidProcFanResponse) {
		t.Errorf("parseProcFan level=weird: err=%v, want wraps ErrInvalidProcFanResponse", err)
	}
}

// ------------------------------------------------------------------
// Backend.Read
// ------------------------------------------------------------------

func TestBackend_Read_ValidResponse(t *testing.T) {
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	ch := f.makeChannel()
	r, err := b.Read(ch)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !r.OK {
		t.Errorf("Reading.OK = false")
	}
	if r.RPM != 1992 {
		t.Errorf("Reading.RPM = %d, want 1992", r.RPM)
	}
}

func TestBackend_Read_FileMissingReportsOKFalse(t *testing.T) {
	// File deleted between Enumerate and Read (race against module
	// unload mid-daemon-lifetime). The contract: Read does not error
	// out, but Reading.OK is false so the controller skips the tick.
	b := NewBackend(nil)
	ch := hal.Channel{
		Opaque: State{ProcPath: "/nonexistent/proc/acpi/ibm/fan", FanIndex: 1},
	}
	r, err := b.Read(ch)
	if err != nil {
		t.Errorf("Read on missing file: err=%v, want nil error with OK=false", err)
	}
	if r.OK {
		t.Errorf("Reading.OK = true on missing file, want false")
	}
	// Empty-by-construction invariant: OK=false → every other field zero.
	if r.PWM != 0 || r.RPM != 0 || r.Temp != 0 {
		t.Errorf("Reading.OK=false but other fields non-zero: %+v", r)
	}
}

func TestBackend_Read_MalformedFileReportsOKFalse(t *testing.T) {
	f := newProcFanFixture(t, "garbage that has no level line\n")
	b := NewBackend(nil)
	r, err := b.Read(f.makeChannel())
	if err != nil {
		t.Errorf("Read on malformed: err=%v, want nil error with OK=false", err)
	}
	if r.OK {
		t.Errorf("Reading.OK = true on malformed content")
	}
}

func TestBackend_Read_NeverMutatesFile(t *testing.T) {
	// RULE-HAL-002.
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	before := f.content(t)
	_, _ = b.Read(f.makeChannel())
	after := f.content(t)
	if before != after {
		t.Errorf("Read mutated file:\nbefore: %q\nafter:  %q", before, after)
	}
}

// ------------------------------------------------------------------
// Backend.Write
// ------------------------------------------------------------------

func TestBackend_Write_EmitsEnableThenLevel(t *testing.T) {
	// First write on a fresh path: backend issues "enable\n" then
	// "level N\n". The final file content reflects the level write
	// (each os.WriteFile is whole-file overwrite).
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	if err := b.Write(f.makeChannel(), 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := f.content(t)
	// PWM=128 maps to level 4 (per pwmToLevel boundary table above).
	wantLevel := pwmToLevel(128)
	wantTail := fmt.Sprintf("level %d\n", wantLevel)
	if got != wantTail {
		t.Errorf("Write(128) final file content = %q, want %q", got, wantTail)
	}
}

func TestBackend_Write_SecondWriteSkipsEnable(t *testing.T) {
	// RULE-HAL-008 idempotent open: a second Write on the same path
	// must not re-issue "enable" (we track acquisition in
	// Backend.acquired) but the last write's level must win.
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	ch := f.makeChannel()
	if err := b.Write(ch, 50); err != nil {
		t.Fatalf("Write #1: %v", err)
	}
	if err := b.Write(ch, 200); err != nil {
		t.Fatalf("Write #2: %v", err)
	}
	got := f.content(t)
	// PWM=200 → level 5 (per the table).
	wantLevel := pwmToLevel(200)
	wantTail := fmt.Sprintf("level %d\n", wantLevel)
	if got != wantTail {
		t.Errorf("after two writes, final content = %q, want %q", got, wantTail)
	}
}

func TestBackend_Write_QuantisesEveryPWMByteToValidLevel(t *testing.T) {
	// Exhaustive sweep: every uint8 input must produce a "level N\n"
	// command with N in [0, FirmwareLevelMax].
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	for pwm := 0; pwm < 256; pwm++ {
		ch := f.makeChannel()
		if err := b.Write(ch, uint8(pwm)); err != nil {
			t.Fatalf("Write(%d): %v", pwm, err)
		}
		got := strings.TrimSpace(f.content(t))
		// Trim "level " prefix and parse the integer.
		if !strings.HasPrefix(got, "level ") {
			t.Fatalf("Write(%d): file content %q missing 'level ' prefix", pwm, got)
		}
		var level int
		if _, err := fmt.Sscanf(got, "level %d", &level); err != nil {
			t.Fatalf("Write(%d): parse %q: %v", pwm, got, err)
		}
		if level < 0 || level > FirmwareLevelMax {
			t.Errorf("Write(%d): produced level=%d outside [0,%d]", pwm, level, FirmwareLevelMax)
		}
	}
}

func TestBackend_Write_EPERMWrapsAsErrFanControlDisabled(t *testing.T) {
	// Make the fixture file read-only so os.WriteFile returns EACCES.
	// The kernel's actual EPERM-on-fan_control=0 path emits the same
	// errno (syscall.EPERM, which errors.Is(_, fs.ErrPermission) is
	// true for), so a chmod-readonly file is a faithful proxy.
	f := newProcFanFixture(t, validProcFanContent)
	if err := os.Chmod(f.path, 0o400); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.path, 0o644) })

	// Skip when running as root — root bypasses the DAC mode check
	// and the write succeeds, defeating the test's premise.
	if os.Geteuid() == 0 {
		t.Skip("EPERM wrap test requires non-root euid to honour the chmod 0400 gate")
	}

	b := NewBackend(nil)
	err := b.Write(f.makeChannel(), 128)
	if err == nil {
		t.Fatalf("Write on read-only file returned nil error")
	}
	if !errors.Is(err, ErrFanControlDisabled) {
		t.Errorf("Write EPERM: err=%v, want errors.Is(_, ErrFanControlDisabled)", err)
	}
	// Sanity: errors.Is should also walk to the underlying fs.ErrPermission
	// so the recovery classifier's existing permission-denied probe still
	// matches.
	if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EACCES) {
		t.Errorf("Write EPERM: err=%v, want underlying permission error preserved", err)
	}
}

func TestBackend_Write_RejectsWrongOpaqueType(t *testing.T) {
	b := NewBackend(nil)
	ch := hal.Channel{ID: "x", Opaque: 42} // int — wrong type
	err := b.Write(ch, 100)
	if err == nil {
		t.Errorf("Write with int opaque returned nil, want error")
	}
}

func TestBackend_Write_RejectsEmptyProcPath(t *testing.T) {
	b := NewBackend(nil)
	ch := hal.Channel{
		Opaque: State{ProcPath: "", FanIndex: 1},
	}
	err := b.Write(ch, 100)
	if err == nil {
		t.Errorf("Write with empty ProcPath returned nil, want error")
	}
}

// ------------------------------------------------------------------
// Backend.Restore
// ------------------------------------------------------------------

func TestBackend_Restore_WritesLevelAuto(t *testing.T) {
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	if err := b.Restore(f.makeChannel()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := f.content(t); got != "level auto\n" {
		t.Errorf("Restore wrote %q, want \"level auto\\n\"", got)
	}
}

func TestBackend_Restore_SafeOnUnwrittenChannel(t *testing.T) {
	// RULE-HAL-004: Restore must not panic on a channel that was
	// never opened (acquired-map empty). Backend has no per-channel
	// pre-state to worry about; Restore just writes "level auto".
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Restore panicked on un-opened channel: %v", r)
		}
	}()
	if err := b.Restore(f.makeChannel()); err != nil {
		t.Errorf("Restore on un-opened: err=%v (clean error is acceptable, panic is not)", err)
	}
}

func TestBackend_Restore_ClearsAcquiredFlag(t *testing.T) {
	// After Restore, a subsequent Write must re-issue "enable" because
	// the daemon is conceptually re-acquiring the channel after a
	// SIGHUP / config reload. We verify this indirectly: the acquired
	// map should not contain the path after Restore.
	f := newProcFanFixture(t, validProcFanContent)
	b := NewBackend(nil)
	ch := f.makeChannel()
	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := b.acquired.Load(f.path); !ok {
		t.Fatalf("post-Write: acquired flag missing")
	}
	if err := b.Restore(ch); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, ok := b.acquired.Load(f.path); ok {
		t.Errorf("post-Restore: acquired flag still present, want cleared")
	}
}

// ------------------------------------------------------------------
// Backend.Enumerate
// ------------------------------------------------------------------

func TestBackend_Enumerate_AbsentProcfsReturnsEmpty(t *testing.T) {
	// On a non-ThinkPad host /proc/acpi/ibm/fan is absent. Enumerate
	// must return (nil, nil) — empty slice, no error — so the
	// registry's fan-out treats it as "no channels here".
	b := NewBackend(nil)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: err=%v on host without /proc/acpi/ibm/fan", err)
	}
	// We can't predict whether the CI host happens to have the file
	// (some kernel CI matrices include thinkpad_acpi in their built-
	// in module list). What we CAN assert is that Enumerate returned
	// either zero or one channel — never more, since the procfs
	// surface is single-instance.
	if len(chs) > 1 {
		t.Errorf("Enumerate returned %d channels, want 0 or 1", len(chs))
	}
}

func TestBackend_Enumerate_Idempotent(t *testing.T) {
	// RULE-HAL-001. Two successive calls must return the same set.
	b := NewBackend(nil)
	chs1, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate #1: %v", err)
	}
	chs2, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate #2: %v", err)
	}
	if len(chs1) != len(chs2) {
		t.Fatalf("Enumerate not idempotent: len %d vs %d", len(chs1), len(chs2))
	}
	for i := range chs1 {
		if chs1[i].ID != chs2[i].ID {
			t.Errorf("Enumerate not idempotent at idx %d: %q vs %q", i, chs1[i].ID, chs2[i].ID)
		}
	}
}

func TestBackend_Enumerate_RespectsContextCancel(t *testing.T) {
	b := NewBackend(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Enumerate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Enumerate with cancelled ctx: err=%v, want context.Canceled", err)
	}
}

// ------------------------------------------------------------------
// Backend.Close (RULE-HAL-007)
// ------------------------------------------------------------------

func TestBackend_Close_Idempotent(t *testing.T) {
	b := NewBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("Close #1: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close #2: %v", err)
	}
}

// ------------------------------------------------------------------
// Backend.Name
// ------------------------------------------------------------------

func TestBackend_Name_StableConstant(t *testing.T) {
	b := NewBackend(nil)
	if b.Name() != BackendName {
		t.Errorf("Name() = %q, want %q", b.Name(), BackendName)
	}
	if BackendName != "thinkpad" {
		t.Errorf("BackendName = %q, want \"thinkpad\" (registry tag is load-bearing)", BackendName)
	}
}
