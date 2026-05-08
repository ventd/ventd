// Package fakehwmon provides a deterministic /sys/class/hwmon tree for unit tests.
package fakehwmon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ventd/ventd/testutil"
)

// ChipOptions defines a single hwmonN chip directory to create.
type ChipOptions struct {
	Name  string
	PWMs  []PWMOptions
	Fans  []FanOptions
	Temps []TempOptions
	Extra map[string]string
}

// PWMOptions defines one pwm channel within a chip.
type PWMOptions struct {
	Index  int  // 1-based; produces pwm1, pwm1_enable, etc.
	PWM    int  // initial value written to pwmN (0–255)
	Enable int  // initial value written to pwmN_enable
	Mode   *int // optional; if non-nil writes pwmN_mode
	Max    *int // optional; if non-nil writes pwmN_max

	// Quirk knobs (v0.5.32) — opt-in chip misbehaviours mirroring
	// the four canonical real-world classes the rule catalogue
	// guards against. The fields document intent; firing is driven
	// by explicit Fake helper methods (InjectSentinelRPM,
	// SimulateBIOSRevert, SetInvertedPolarity, ReassertPWMEnable)
	// because the fake is file-backed and the production hwmon
	// backend reads / writes via os.ReadFile / os.WriteFile
	// directly — there is no interception point. See the doc
	// comments on each helper for the trigger semantics.

	// EmitSentinelRPMEvery, when > 0, advises tests to inject a
	// 65535-RPM nct6687-style sentinel (RULE-HWMON-SENTINEL-FAN /
	// RULE-SENTINEL-FAN-IMPLAUSIBLE) on every Nth simulated read.
	// The Fake.InjectSentinelRPM helper is the firing primitive;
	// tests that follow this knob loop call the helper themselves.
	EmitSentinelRPMEvery int

	// BIOSRevertAfter, when non-zero, models the it8689e BIOS-
	// override pattern (RULE-CALIB-PR2B-06): writes accept at
	// <50ms, revert at >200ms. Tests call Fake.SimulateBIOSRevert
	// between the daemon's first and second readback.
	BIOSRevertAfter int // milliseconds; 0 = disabled

	// InvertedPolarity, when true, advises tests to use
	// Fake.SimulateFanResponse with inverted=true so reads from
	// fan*_input track the inverse of the daemon's PWM writes.
	// Validates RULE-POLARITY-02 / RULE-CALIB-PR2B-02 / RULE-OPP-
	// PROBE-04 against synthetic inverted fans.
	InvertedPolarity bool

	// EBUSYReassertEvery, when > 0, models the Gigabyte Q-Fan /
	// Smart Fan Control reassertion pattern (RULE-HWMON-MODE-
	// REACQUIRE): the BIOS periodically forces pwm*_enable back
	// to firmware-auto. Tests call Fake.ReassertPWMEnable on
	// every Nth backend write.
	EBUSYReassertEvery int
}

// FanOptions defines one fan input within a chip.
type FanOptions struct {
	Index int
	RPM   int
}

// TempOptions defines one temperature input within a chip.
type TempOptions struct {
	Index  int
	MilliC int
	Label  string // empty means skip tempN_label
}

// Options holds the configuration for the fake hwmon tree.
type Options struct {
	Chips []ChipOptions
}

// Fake provides a mock hwmon sysfs tree rooted at Root.
type Fake struct {
	// Root is the tempdir mimicking /sys/class/hwmon; hwmonN/ subdirs live under it.
	Root string
	rec  *testutil.CallRecorder
	mu   sync.Mutex
}

// New creates a fake hwmon sysfs tree under a t.TempDir() directory.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	root := t.TempDir()
	for i, chip := range opts.Chips {
		chipDir := filepath.Join(root, "hwmon"+strconv.Itoa(i))
		if err := os.MkdirAll(chipDir, 0755); err != nil {
			t.Fatalf("fakehwmon: mkdir %s: %v", chipDir, err)
		}
		if chip.Name != "" {
			sysfsWrite(t, chipDir, "name", chip.Name)
		}
		for _, pwm := range chip.PWMs {
			if pwm.Index < 1 {
				t.Fatalf("fakehwmon: PWMOptions.Index must be >= 1, got %d", pwm.Index)
			}
			v := clamp(pwm.PWM, 0, 255)
			sysfsWrite(t, chipDir, "pwm"+strconv.Itoa(pwm.Index), strconv.Itoa(v))
			sysfsWrite(t, chipDir, "pwm"+strconv.Itoa(pwm.Index)+"_enable", strconv.Itoa(pwm.Enable))
			if pwm.Mode != nil {
				sysfsWrite(t, chipDir, "pwm"+strconv.Itoa(pwm.Index)+"_mode", strconv.Itoa(*pwm.Mode))
			}
			if pwm.Max != nil {
				sysfsWrite(t, chipDir, "pwm"+strconv.Itoa(pwm.Index)+"_max", strconv.Itoa(*pwm.Max))
			}
		}
		for _, fan := range chip.Fans {
			if fan.Index < 1 {
				t.Fatalf("fakehwmon: FanOptions.Index must be >= 1, got %d", fan.Index)
			}
			sysfsWrite(t, chipDir, "fan"+strconv.Itoa(fan.Index)+"_input", strconv.Itoa(fan.RPM))
		}
		for _, temp := range chip.Temps {
			if temp.Index < 1 {
				t.Fatalf("fakehwmon: TempOptions.Index must be >= 1, got %d", temp.Index)
			}
			sysfsWrite(t, chipDir, "temp"+strconv.Itoa(temp.Index)+"_input", strconv.Itoa(temp.MilliC))
			if temp.Label != "" {
				sysfsWrite(t, chipDir, "temp"+strconv.Itoa(temp.Index)+"_label", temp.Label)
			}
		}
		for name, content := range chip.Extra {
			sysfsWrite(t, chipDir, name, content)
		}
	}
	t.Cleanup(func() {})
	return &Fake{
		Root: root,
		rec:  testutil.NewCallRecorder(),
	}
}

// WritePWM rewrites pwmN for chip chipIndex (0-based). Value is clamped to [0,255].
func (f *Fake) WritePWM(chipIndex, pwmIndex, value int) error {
	if chipIndex < 0 {
		return fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if pwmIndex < 1 {
		return fmt.Errorf("fakehwmon: pwmIndex must be >= 1, got %d", pwmIndex)
	}
	value = clamp(value, 0, 255)
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("WritePWM", chipIndex, pwmIndex, value)
	if err := os.WriteFile(path, []byte(strconv.Itoa(value)+"\n"), 0644); err != nil {
		return fmt.Errorf("fakehwmon: WritePWM: %w", err)
	}
	return nil
}

// ReadPWM reads pwmN for chip chipIndex (0-based).
func (f *Fake) ReadPWM(chipIndex, pwmIndex int) (int, error) {
	if chipIndex < 0 {
		return 0, fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if pwmIndex < 1 {
		return 0, fmt.Errorf("fakehwmon: pwmIndex must be >= 1, got %d", pwmIndex)
	}
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("ReadPWM", chipIndex, pwmIndex)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("fakehwmon: ReadPWM: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("fakehwmon: ReadPWM parse: %w", err)
	}
	return v, nil
}

// Calls returns all recorded calls via the embedded CallRecorder.
func (f *Fake) Calls() []testutil.Call {
	return f.rec.Calls()
}

// ---------------------------------------------------------------
// Quirk helpers (v0.5.32)
//
// These four helpers each model one canonical real-world chip
// misbehaviour the rule catalogue guards against. They're explicit
// (test calls them between backend operations) rather than automatic
// because the fake is file-backed and the production hwmon backend
// reads / writes via os.ReadFile / os.WriteFile directly — there is
// no interception point on the read or write path.
//
// The matching `*Options` fields (EmitSentinelRPMEvery,
// BIOSRevertAfter, InvertedPolarity, EBUSYReassertEvery) document
// the intended firing cadence; tests loop over backend operations
// and invoke the helper at the cadence the option specifies.
// ---------------------------------------------------------------

// SentinelRPMValue is the 0xFFFF nct6687 sentinel — what the chip
// emits on a register mid-latch glitch. Real-world failure mode:
// fan*_input briefly returns 65535 before settling back to the
// real RPM. RULE-HWMON-SENTINEL-FAN + RULE-SENTINEL-FAN-IMPLAUSIBLE
// guard against this making it past the backend's plausibility
// filter; this constant lets tests inject the exact byte sequence.
const SentinelRPMValue = 65535

// InjectSentinelRPM writes the 0xFFFF sentinel to fan<fanIndex>_input
// on chip<chipIndex>. Tests call this between backend reads to
// simulate the chip glitching mid-run. The sentinel value is an
// implausible RPM (RULE-SENTINEL-FAN-IMPLAUSIBLE caps at 25 000)
// so the backend's IsSentinelRPM gate must reject it.
//
// The next backend ReadFile on the fan input file picks up the
// sentinel; tests assert the controller carries forward the last
// good PWM (RULE-HWMON-INVALID-CURVE-SKIP) instead of evaluating
// the curve against a 65 535 RPM "reading".
func (f *Fake) InjectSentinelRPM(chipIndex, fanIndex int) error {
	if chipIndex < 0 {
		return fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if fanIndex < 1 {
		return fmt.Errorf("fakehwmon: fanIndex must be >= 1, got %d", fanIndex)
	}
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "fan"+strconv.Itoa(fanIndex)+"_input")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("InjectSentinelRPM", chipIndex, fanIndex)
	if err := os.WriteFile(path, []byte(strconv.Itoa(SentinelRPMValue)+"\n"), 0644); err != nil {
		return fmt.Errorf("fakehwmon: InjectSentinelRPM: %w", err)
	}
	return nil
}

// SimulateBIOSRevert writes originalValue to pwm<pwmIndex> on
// chip<chipIndex>, simulating the it8689e / Gigabyte BIOS pattern
// where writes accept at <50 ms then revert to firmware value at
// >200 ms. Tests sequence as: backend writes target → readback
// confirms target → SimulateBIOSRevert(originalValue) → second
// readback returns originalValue → backend's RULE-CALIB-PR2B-06
// detector fires and marks the channel BIOSOverridden.
//
// originalValue is whatever the firmware would set the channel back
// to; commonly 128 (50%) for nct chips or whatever the BIOS curve's
// idle-temp output is. Tests pick this value to match their
// scenario.
func (f *Fake) SimulateBIOSRevert(chipIndex, pwmIndex, originalValue int) error {
	if chipIndex < 0 {
		return fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if pwmIndex < 1 {
		return fmt.Errorf("fakehwmon: pwmIndex must be >= 1, got %d", pwmIndex)
	}
	originalValue = clamp(originalValue, 0, 255)
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("SimulateBIOSRevert", chipIndex, pwmIndex, originalValue)
	if err := os.WriteFile(path, []byte(strconv.Itoa(originalValue)+"\n"), 0644); err != nil {
		return fmt.Errorf("fakehwmon: SimulateBIOSRevert: %w", err)
	}
	return nil
}

// SimulateFanResponse reads pwm<pwmIndex> and writes the corresponding
// RPM to fan<fanIndex>_input on chip<chipIndex>. When inverted is
// true, RPM = maxRPM × (255−pwm)/255 (high RPM at low PWM); when
// false, RPM = maxRPM × pwm/255 (linear normal response).
//
// Useful for closed-loop tests where the fan reading must follow
// the daemon's PWM writes. Validates RULE-POLARITY-02 (the polarity
// probe's hold-time logic) and RULE-CALIB-PR2B-02 (the inverted-
// polarity classifier's RPM-delta threshold).
//
// maxRPM defines the linear scaling; typical values are 2000 (case
// fan), 6500 (AIO pump), 18000 (server-class). Tests choose to
// match the synthetic chip class.
func (f *Fake) SimulateFanResponse(chipIndex, pwmIndex, fanIndex, maxRPM int, inverted bool) error {
	if chipIndex < 0 {
		return fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if pwmIndex < 1 || fanIndex < 1 {
		return fmt.Errorf("fakehwmon: pwmIndex/fanIndex must be >= 1")
	}
	if maxRPM <= 0 {
		return fmt.Errorf("fakehwmon: maxRPM must be > 0, got %d", maxRPM)
	}
	pwm, err := f.readPWMLocked(chipIndex, pwmIndex)
	if err != nil {
		return err
	}
	var rpm int
	if inverted {
		rpm = maxRPM * (255 - pwm) / 255
	} else {
		rpm = maxRPM * pwm / 255
	}
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "fan"+strconv.Itoa(fanIndex)+"_input")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("SimulateFanResponse", chipIndex, pwmIndex, fanIndex, maxRPM, inverted, rpm)
	if err := os.WriteFile(path, []byte(strconv.Itoa(rpm)+"\n"), 0644); err != nil {
		return fmt.Errorf("fakehwmon: SimulateFanResponse: %w", err)
	}
	return nil
}

// readPWMLocked is the lock-free internal read used by
// SimulateFanResponse. (The public ReadPWM acquires the lock.)
func (f *Fake) readPWMLocked(chipIndex, pwmIndex int) (int, error) {
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex))
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("fakehwmon: readPWMLocked: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("fakehwmon: readPWMLocked parse: %w", err)
	}
	return v, nil
}

// ReassertPWMEnable writes value to pwm<pwmIndex>_enable on
// chip<chipIndex>. Models the Gigabyte Q-Fan / Smart Fan Control
// pattern where the BIOS periodically forces pwm_enable back to 2
// (firmware auto), which on real hardware causes the next pwm
// write to return EBUSY. RULE-HWMON-MODE-REACQUIRE specifies the
// single-retry contract: detect EBUSY → re-write pwm_enable=1 →
// retry the duty-cycle write exactly once.
//
// Tests call ReassertPWMEnable(chipIndex, pwmIndex, 2) before the
// next backend write to flip the chip back to firmware auto, then
// assert the backend's WriteFile fails with EBUSY (or simulates the
// EBUSY surface via the writePWMFn / writePWMEnableFn seams in
// internal/setup), then re-acquires manual mode and retries.
//
// Real EBUSY simulation requires the test to observe the file's
// pwm_enable state and reject the next pwm write — fakehwmon
// doesn't have a write-blocker primitive. Use this helper to set
// up the precondition; tests pair it with their own EBUSY-injecting
// stub on the backend's writePWMFn seam.
func (f *Fake) ReassertPWMEnable(chipIndex, pwmIndex, value int) error {
	if chipIndex < 0 {
		return fmt.Errorf("fakehwmon: chipIndex must be >= 0, got %d", chipIndex)
	}
	if pwmIndex < 1 {
		return fmt.Errorf("fakehwmon: pwmIndex must be >= 1, got %d", pwmIndex)
	}
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex)+"_enable")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Record("ReassertPWMEnable", chipIndex, pwmIndex, value)
	if err := os.WriteFile(path, []byte(strconv.Itoa(value)+"\n"), 0644); err != nil {
		return fmt.Errorf("fakehwmon: ReassertPWMEnable: %w", err)
	}
	return nil
}

func sysfsWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		t.Fatalf("fakehwmon: write %s: %v", path, err)
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
