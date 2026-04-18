// Package pwmsys is the /sys/class/pwm sysfs implementation of hal.FanBackend.
// It targets ARM SBCs (Raspberry Pi 5 primary; also Rockchip / Allwinner /
// Amlogic boards) that expose PWM GPIO lines via the generic Linux PWM sysfs
// ABI described in Documentation/ABI/testing/sysfs-class-pwm.
package pwmsys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag for channels produced by this backend.
const BackendName = "pwmsys"

// DefaultPWMRoot is the canonical sysfs location of the PWM subsystem.
const DefaultPWMRoot = "/sys/class/pwm"

// defaultPeriodNs is written to a freshly-exported channel whose period
// is still 0. 40 000 ns = 25 kHz, the standard 4-wire PWM fan frequency.
const defaultPeriodNs = 40_000

// State is the per-channel payload carried in hal.Channel.Opaque.
type State struct {
	// ChanDir is the absolute path to the pwmchipN/pwmM directory,
	// e.g. /sys/class/pwm/pwmchip0/pwm1.
	ChanDir string
	// PeriodNs is the channel's period in nanoseconds, cached at Enumerate
	// time so Write does not need a sysfs read every tick.
	PeriodNs uint64
}

// Backend is the pwmsys implementation of hal.FanBackend.
type Backend struct {
	root     string
	logger   *slog.Logger
	acquired sync.Map // key: ChanDir (string), value: struct{}
}

// NewBackend constructs a Backend rooted at DefaultPWMRoot.
func NewBackend(logger *slog.Logger) *Backend {
	return newBackend(DefaultPWMRoot, logger)
}

func newBackend(root string, logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{root: root, logger: logger}
}

// Name returns the registry tag.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op; pwmsys holds no process-level resources.
func (b *Backend) Close() error { return nil }

// Enumerate walks b.root for pwmchipN directories, exports every channel
// index 0..npwm-1, and returns one hal.Channel per PWM line. It is safe
// to call multiple times; re-export of an already-exported channel is a
// no-op.
//
// If b.root does not exist or contains no chips, Enumerate returns an empty
// slice and a nil error — the backend is silently inactive on non-SBC hosts.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	entries, err := os.ReadDir(b.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("pwmsys: readdir %s: %w", b.root, err)
	}

	var out []hal.Channel
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "pwmchip") {
			continue
		}
		chipDir := filepath.Join(b.root, e.Name())
		chs, err := b.enumerateChip(ctx, chipDir)
		if err != nil {
			b.logger.Warn("pwmsys: skipping chip", "dir", chipDir, "err", err)
			continue
		}
		out = append(out, chs...)
	}
	return out, nil
}

func (b *Backend) enumerateChip(ctx context.Context, chipDir string) ([]hal.Channel, error) {
	npwm, err := readUint(filepath.Join(chipDir, "npwm"))
	if err != nil {
		return nil, fmt.Errorf("read npwm: %w", err)
	}
	if npwm == 0 {
		return nil, nil
	}

	var out []hal.Channel
	for idx := uint64(0); idx < npwm; idx++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		chanDir := filepath.Join(chipDir, "pwm"+strconv.FormatUint(idx, 10))
		if err := b.exportChannel(chipDir, chanDir, idx); err != nil {
			b.logger.Warn("pwmsys: export failed, skipping channel",
				"chip", chipDir, "idx", idx, "err", err)
			continue
		}
		period, err := b.ensurePeriod(chanDir)
		if err != nil {
			b.logger.Warn("pwmsys: cannot read period, skipping channel",
				"chan", chanDir, "err", err)
			continue
		}
		out = append(out, hal.Channel{
			ID:   chanDir,
			Role: hal.RoleUnknown,
			Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
			Opaque: State{
				ChanDir:  chanDir,
				PeriodNs: period,
			},
		})
	}
	return out, nil
}

// exportChannel writes idx to chipDir/export. If the channel directory
// already exists (EBUSY or a pre-populated fake), the write error is
// ignored and we verify the directory is accessible.
func (b *Backend) exportChannel(chipDir, chanDir string, idx uint64) error {
	exportPath := filepath.Join(chipDir, "export")
	data := strconv.FormatUint(idx, 10)
	err := os.WriteFile(exportPath, []byte(data), 0200)
	if err != nil {
		// Tolerate EBUSY (already exported) and any write error if the
		// channel directory is already present (fake sysfs in tests).
		if _, statErr := os.Stat(chanDir); statErr == nil {
			return nil
		}
		return fmt.Errorf("export pwm%d: %w", idx, err)
	}
	return nil
}

// ensurePeriod reads the channel's period. If period is 0 (freshly exported),
// it writes defaultPeriodNs so the channel is immediately usable.
func (b *Backend) ensurePeriod(chanDir string) (uint64, error) {
	period, err := readUint(filepath.Join(chanDir, "period"))
	if err != nil {
		return 0, fmt.Errorf("read period: %w", err)
	}
	if period == 0 {
		if err := writeUint(filepath.Join(chanDir, "period"), defaultPeriodNs); err != nil {
			return 0, fmt.Errorf("write default period: %w", err)
		}
		period = defaultPeriodNs
	}
	return period, nil
}

// Read samples the current duty cycle and converts it to a 0-255 PWM byte.
// RPM is always 0; most SBC fans lack a tachometer on the GPIO path.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	duty, err := readUint(filepath.Join(st.ChanDir, "duty_cycle"))
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	period := st.PeriodNs
	if period == 0 {
		return hal.Reading{OK: false}, nil
	}
	pwm := uint8(math.Round(float64(duty) / float64(period) * 255))
	return hal.Reading{PWM: pwm, RPM: 0, OK: true}, nil
}

// Write converts the 0-255 duty byte to nanoseconds and writes it to the
// channel, then enables the channel. The first Write to each channel
// sets enable=1; subsequent writes skip the redundant enable write only
// if the channel is already marked acquired.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	period := st.PeriodNs
	if period == 0 {
		return fmt.Errorf("pwmsys: channel %s has period=0, cannot write", st.ChanDir)
	}
	duty := uint64(math.Round(float64(pwm) / 255.0 * float64(period)))
	if duty > period {
		duty = period
	}
	if err := writeUint(filepath.Join(st.ChanDir, "duty_cycle"), duty); err != nil {
		return fmt.Errorf("pwmsys: write duty_cycle %s: %w", st.ChanDir, err)
	}
	if _, loaded := b.acquired.LoadOrStore(st.ChanDir, struct{}{}); !loaded {
		if err := writeUint(filepath.Join(st.ChanDir, "enable"), 1); err != nil {
			b.acquired.Delete(st.ChanDir)
			return fmt.Errorf("pwmsys: enable %s: %w", st.ChanDir, err)
		}
		b.logger.Info("pwmsys: channel enabled", "chan", st.ChanDir)
	}
	return nil
}

// Restore disables the channel (enable=0), handing control back to the
// kernel or the board's PWM controller. It is safe to call on a channel
// that was never Written to.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	b.acquired.Delete(st.ChanDir)
	if err := writeUint(filepath.Join(st.ChanDir, "enable"), 0); err != nil {
		b.logger.Error("pwmsys: restore failed to disable channel",
			"chan", st.ChanDir, "err", err)
		return fmt.Errorf("pwmsys: restore %s: %w", st.ChanDir, err)
	}
	b.logger.Info("pwmsys: channel restored (disabled)", "chan", st.ChanDir)
	return nil
}

func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("pwmsys: nil opaque state")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("pwmsys: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

func writeUint(path string, v uint64) error {
	return os.WriteFile(path, []byte(strconv.FormatUint(v, 10)), 0644)
}
