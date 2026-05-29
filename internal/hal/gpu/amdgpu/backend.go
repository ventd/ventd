package amdgpu

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by this
// backend. config.Fan.Type == "amdgpu" routes here via the controller's
// registry-lookup dispatch.
const BackendName = "amdgpu"

// State is the per-channel payload carried in hal.Channel.Opaque. It holds
// the fully-probed CardInfo (sysfs paths + RDNA-gen + the amd_overdrive
// flag) so Read / Write / Restore operate without re-enumerating.
type State struct {
	Card CardInfo
}

// Backend is the amdgpu-sysfs implementation of hal.FanBackend. It wraps the
// package's CardInfo primitives (WritePWM / ReadFanRPM / RestoreAuto); it
// never re-implements sysfs access, so the RDNA-gen gating and the
// amd_overdrive experimental gate stay in one place.
//
// Control surface, by card:
//   - RDNA1/2 (no fan_curve) WITH amd_overdrive  → CapRead|CapWritePWM|CapRestore
//   - RDNA1/2 without amd_overdrive               → CapRead|CapRestore (monitor)
//   - RDNA3+ (fan_curve interface)                → CapRead|CapRestore (monitor)
//
// RDNA3+ per-tick duty control needs the gpu_od/fan_ctrl/fan_curve model
// (set-a-curve, hardware-follows) rather than per-tick WritePWM, so v1
// exposes those cards as monitor-only; the curve model is a follow-up.
type Backend struct {
	logger       *slog.Logger
	sysRoot      string
	amdOverdrive bool
}

// NewBackend constructs a Backend rooted at sysRoot ("/sys" in production).
// amdOverdrive mirrors the --enable-amd-overdrive experimental flag and gates
// whether enumerated channels advertise CapWritePWM. A nil logger falls
// through to slog.Default.
func NewBackend(logger *slog.Logger, sysRoot string, amdOverdrive bool) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	return &Backend{logger: logger, sysRoot: sysRoot, amdOverdrive: amdOverdrive}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op: amdgpu control is pure sysfs, no handles to release.
func (b *Backend) Close() error { return nil }

// Enumerate returns one Channel per discovered AMD GPU. The channel ID is the
// card's sysfs path (/sys/class/drm/card*), which the config stores as the
// fan's pwm_path so channelFor can match by ID. Returns an empty slice (not an
// error) on a host with no AMD GPUs — the daemon must keep running.
func (b *Backend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	cards, err := Enumerate(b.sysRoot)
	if err != nil {
		// A missing /sys/class/drm (containers, exotic hosts) is "no AMD GPUs",
		// not a fatal backend error.
		b.logger.Debug("amdgpu: enumerate failed; treating as no AMD GPUs", "err", err)
		return nil, nil
	}
	out := make([]hal.Channel, 0, len(cards))
	for i := range cards {
		card := cards[i]
		card.AMDOverdrive = b.amdOverdrive
		caps := hal.CapRead | hal.CapRestore
		if b.amdOverdrive && !card.HasFanCurve {
			caps |= hal.CapWritePWM
		}
		out = append(out, hal.Channel{
			ID:     card.CardPath,
			Role:   hal.RoleGPU,
			Caps:   caps,
			Opaque: State{Card: card},
		})
	}
	return out, nil
}

// Read samples the GPU fan RPM. amdgpu exposes no direct duty read-back, so
// PWM is left zero (the controller tracks its own commanded duty); RPM is the
// useful signal for the dashboard and stuck-fan detection.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	rpm, err := st.Card.ReadFanRPM()
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	if rpm < 0 {
		rpm = 0
	}
	if rpm > 65535 {
		rpm = 65535
	}
	return hal.Reading{RPM: uint16(rpm), OK: true}, nil
}

// Write commands a 0-255 duty cycle. Only valid on channels that advertised
// CapWritePWM (RDNA1/2 + amd_overdrive); WritePWM itself re-checks the
// amd_overdrive and RDNA-gen gates and returns a descriptive error otherwise.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	return st.Card.WritePWM(pwm)
}

// Restore returns the GPU fan to firmware/auto control (pwm1_enable=2 on
// RDNA1/2, fan_curve reset on RDNA3+). Idempotent.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	return st.Card.RestoreAuto()
}

// stateFrom coerces a Channel's Opaque payload into the amdgpu State shape.
func stateFrom(ch hal.Channel) (State, error) {
	return hal.StateFrom[State](ch, "hal/amdgpu", func(s State) error {
		if s.Card.HwmonPath == "" {
			return fmt.Errorf("hal/amdgpu: channel %q has no hwmon path", ch.ID)
		}
		return nil
	})
}
