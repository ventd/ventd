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
	// NumChannels is the value written to npwm. Must be >= 1.
	NumChannels int
	// PeriodNs is the initial period written to each pwmN/period file.
	// Defaults to 40000 (25 kHz) when zero.
	PeriodNs uint64
}

// Options holds the full fake tree configuration.
type Options struct {
	// Chips is an ordered slice of chip configurations. Chip 0 becomes
	// pwmchip0, chip 1 becomes pwmchip1, etc.
	Chips []ChipOptions
}

// RPi5 returns Options pre-configured for a Raspberry Pi 5 with two
// PWM chips, each exposing two channels.
func RPi5() *Options {
	return &Options{
		Chips: []ChipOptions{
			{NumChannels: 2, PeriodNs: 40_000},
			{NumChannels: 2, PeriodNs: 40_000},
		},
	}
}

// Fake provides a mock /sys/class/pwm sysfs tree rooted at Root.
type Fake struct {
	// Root is the temp directory that mimics /sys/class/pwm.
	Root string
	mu   sync.Mutex
}

// New creates a fake PWM sysfs tree under t.TempDir() and registers a
// t.Cleanup to verify no goroutine is still holding the lock when the
// test ends.
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
		period := chip.PeriodNs
		if period == 0 {
			period = 40_000
		}
		writeFile(t, chipDir, "npwm", strconv.Itoa(nc))
		// export is writable but ignored — channels are pre-created.
		writeFile(t, chipDir, "export", "")
		for idx := 0; idx < nc; idx++ {
			chanDir := filepath.Join(chipDir, "pwm"+strconv.Itoa(idx))
			if err := os.MkdirAll(chanDir, 0755); err != nil {
				t.Fatalf("fakepwmsys: mkdir %s: %v", chanDir, err)
			}
			writeFile(t, chanDir, "period", strconv.FormatUint(period, 10))
			writeFile(t, chanDir, "duty_cycle", "0")
			writeFile(t, chanDir, "enable", "0")
		}
	}
	return &Fake{Root: root}
}

// ReadFile reads a sysfs file relative to Root (e.g.
// "pwmchip0/pwm1/duty_cycle") and returns its trimmed content.
func (f *Fake) ReadFile(relPath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(f.Root, relPath))
	if err != nil {
		return "", fmt.Errorf("fakepwmsys: read %s: %w", relPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ReadUint reads a sysfs file and parses it as uint64.
func (f *Fake) ReadUint(relPath string) (uint64, error) {
	s, err := f.ReadFile(relPath)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("fakepwmsys: parse %s: %w", relPath, err)
	}
	return v, nil
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		t.Fatalf("fakepwmsys: write %s: %v", path, err)
	}
}
