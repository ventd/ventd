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
