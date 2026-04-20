// Package fakepwmsys provides a deterministic /sys/class/pwm sysfs tree
// for unit tests. It creates a temp-dir tree pre-populated with pwmchipN
// directories so that the pwmsys backend can be exercised without a real
// PWM GPIO controller.
package fakepwmsys

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ChipOptions configures one pwmchipN directory.
type ChipOptions struct {
	// NumChannels is the number of pwmN channels under this chip. Must be >= 1.
	NumChannels int
	// DutyCycles holds the initial duty_cycle per channel (index 0 = pwm0).
	// Missing entries default to 0.
	DutyCycles []uint32
	// Period is the initial period in nanoseconds written to each pwmN/period.
	// Defaults to 40000 (25 kHz) when zero.
	Period uint64
	// Enabled holds the initial enable value per channel (index 0 = pwm0).
	// Missing entries default to false (0).
	Enabled []bool
}

// Options holds the full fake tree configuration.
type Options struct {
	// Chips is an ordered slice of chip configurations. Index 0 becomes
	// pwmchip0, index 1 becomes pwmchip1, etc.
	Chips []ChipOptions
}

// ChipRPi5 is a preset for a Raspberry Pi 5 with one PWM chip exposing
// two channels at 50 Hz (20 ms period).
var ChipRPi5 = Options{
	Chips: []ChipOptions{{
		NumChannels: 2,
		Period:      20_000_000, // 20 ms = 50 Hz
		DutyCycles:  []uint32{0, 0},
		Enabled:     []bool{false, false},
	}},
}

// Fake provides a mock /sys/class/pwm sysfs tree.
type Fake struct {
	root string
	mu   sync.Mutex
}

// New creates a fake PWM sysfs tree under t.TempDir() and registers
// cleanup via t.Cleanup.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	root := t.TempDir()
	for i, chip := range opts.Chips {
		chipDir := filepath.Join(root, "pwmchip"+strconv.Itoa(i))
		if err := os.MkdirAll(chipDir, 0755); err != nil {
			t.Fatalf("fakepwmsys: mkdir %s: %v", chipDir, err)
		}
		nc := chip.NumChannels
		if nc < 1 {
			t.Fatalf("fakepwmsys: NumChannels must be >= 1 for chip %d", i)
		}
		period := chip.Period
		if period == 0 {
			period = 40_000
		}
		writeFile(t, chipDir, "npwm", strconv.Itoa(nc))
		writeFile(t, chipDir, "export", "")
		for idx := 0; idx < nc; idx++ {
			chanDir := filepath.Join(chipDir, "pwm"+strconv.Itoa(idx))
			if err := os.MkdirAll(chanDir, 0755); err != nil {
				t.Fatalf("fakepwmsys: mkdir %s: %v", chanDir, err)
			}
			writeFile(t, chanDir, "period", strconv.FormatUint(period, 10))

			var duty uint32
			if idx < len(chip.DutyCycles) {
				duty = chip.DutyCycles[idx]
			}
			writeFile(t, chanDir, "duty_cycle", strconv.FormatUint(uint64(duty), 10))

			enabled := "0"
			if idx < len(chip.Enabled) && chip.Enabled[idx] {
				enabled = "1"
			}
			writeFile(t, chanDir, "enable", enabled)
			writeFile(t, chanDir, "polarity", "normal")
		}
	}
	return &Fake{root: root}
}

// Root returns the sysfs root path; pass to the pwmsys backend as the chip
// base path.
func (f *Fake) Root() string {
	return f.root
}

// ChipPath returns the absolute path of a chip directory
// (e.g. "<root>/pwmchip0").
func (f *Fake) ChipPath(chip int) string {
	return filepath.Join(f.root, "pwmchip"+strconv.Itoa(chip))
}

// ReadDutyCycle reads the current duty_cycle file for the given chip and
// channel and returns it as a uint32.
func (f *Fake) ReadDutyCycle(chip, channel int) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := filepath.Join(f.root,
		"pwmchip"+strconv.Itoa(chip),
		"pwm"+strconv.Itoa(channel),
		"duty_cycle",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("fakepwmsys: ReadDutyCycle(%d,%d): %w", chip, channel, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("fakepwmsys: ReadDutyCycle(%d,%d) parse: %w", chip, channel, err)
	}
	return uint32(v), nil
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		t.Fatalf("fakepwmsys: write %s: %v", path, err)
	}
}
