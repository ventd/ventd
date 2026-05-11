// Package nvml is the NVIDIA/NVML implementation of hal.FanBackend.
// It wraps internal/nvidia — it never re-implements NVML access, so
// the runtime dlopen + refcount-safe Init/Shutdown lifecycle stays
// exactly as the rest of the daemon already relies on it.
package nvml

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/nvidia"
)

// NVMLResetDeadline bounds the NVML fan-reset call from the watchdog
// restore path. NVML is dlopen'd at process start and exposes no
// per-call timeout; the wrapper here runs the reset in a goroutine
// and abandons it on deadline so a hung driver does not block the
// daemon's exit past the watchdog's restore budget. Per
// RULE-WD-PER-SYSCALL-DEADLINE / issue #1040.
//
// 500 ms is loose relative to typical NVML reset latency (10-50 ms)
// but tight enough to fail-fast on a wedged driver. The constant is
// exported so a future operator-tunable knob can adjust it without
// re-wiring every call site.
var NVMLResetDeadline = 500 * time.Millisecond

// nvmlResetFn is the swappable seam used by Restore so tests can
// inject a fake reset that blocks past the deadline, exercising the
// abandonment branch without needing real NVML. Production falls
// through to nvidia.ResetFanSpeed.
var nvmlResetFn func(idx uint) error = nvidia.ResetFanSpeed

// BackendName is the registry tag applied to channels produced by
// this backend.
const BackendName = "nvml"

// State is the per-channel payload carried in hal.Channel.Opaque.
// For NVML, every channel is scoped to a GPU device index; fan
// enumeration per GPU is handled inside internal/nvidia because NVML
// expresses "set all fans to X percent" more ergonomically than a
// per-fan channel does.
type State struct {
	// Index is the GPU device index as a numeric string ("0", "1", …).
	// Matches the pwm_path string used for nvidia fans elsewhere in
	// the daemon, so resolving a config.Fan to a hal.Channel doesn't
	// need a format conversion.
	Index string
}

// Backend is the NVML implementation of hal.FanBackend.
type Backend struct {
	logger *slog.Logger
}

// NewBackend constructs a Backend that logs through the given slog
// logger. A nil logger falls through to slog.Default.
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{logger: logger}
}

// Name returns the registry tag for this backend.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op. NVML shutdown is refcounted in internal/nvidia
// and is driven by whoever paired Init with Shutdown (main.go owns
// the daemon-lifetime pair).
func (b *Backend) Close() error { return nil }

// Enumerate returns one Channel per NVML GPU that has at least one
// controllable fan. Returns an empty slice (not an error) when NVML
// is unavailable — the daemon must keep running on GPU-less hosts.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if !nvidia.Available() {
		return nil, nil
	}
	count := nvidia.CountGPUs()
	var out []hal.Channel
	for i := 0; i < count; i++ {
		idx := uint(i)
		if !nvidia.HasFans(idx) {
			continue
		}
		out = append(out, hal.Channel{
			ID:     strconv.FormatUint(uint64(i), 10),
			Role:   hal.RoleGPU,
			Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
			Opaque: State{Index: strconv.FormatUint(uint64(i), 10)},
		})
	}
	return out, nil
}

// Read samples the current fan speed (as PWM 0-255) for the GPU.
// NVML's own temperature/utilisation readings are exposed through
// internal/nvidia.ReadMetric and remain outside the fan backend's
// scope — the FanBackend interface intentionally narrows to "fan".
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}
	idx, err := parseIndex(st.Index)
	if err != nil {
		return hal.Reading{}, err
	}
	pwm, err := nvidia.ReadFanSpeed(idx)
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	return hal.Reading{PWM: pwm, OK: true}, nil
}

// Write sets the PWM duty cycle on every fan of the target GPU.
// internal/nvidia.WriteFanSpeed converts to the 0-100 percentage
// NVML expects.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	idx, err := parseIndex(st.Index)
	if err != nil {
		return err
	}
	return nvidia.WriteFanSpeed(idx, pwm)
}

// Restore hands fan control back to the NVIDIA driver's autonomous
// curve. The actual reset is dispatched via nvmlResetWithDeadline so
// a hung NVML driver cannot block the watchdog's restore budget
// (RULE-WD-PER-SYSCALL-DEADLINE / issue #1040). On deadline the
// underlying goroutine is abandoned and a non-nil error is returned;
// the watchdog logs + proceeds to the next channel.
//
// Pre-refactor log lines are preserved so operator diagnostics don't
// shift.
func (b *Backend) Restore(ch hal.Channel) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}
	idx, err := parseIndex(st.Index)
	if err != nil {
		b.logger.Error("hal/nvml: restore gpu index parse failed",
			"gpu_index", st.Index, "err", err)
		return err
	}
	if err := nvmlResetWithDeadline(idx, NVMLResetDeadline); err != nil {
		b.logger.Error("watchdog: nvidia fan reset failed",
			"gpu_index", st.Index, "err", err)
		return err
	}
	b.logger.Info("watchdog: nvidia fan restored to auto",
		"gpu_index", st.Index)
	return nil
}

// nvmlResetWithDeadline wraps the NVML reset call (typically
// nvidia.ResetFanSpeed → nvmlDeviceSetDefaultFanSpeed_v2) in a
// goroutine + deadline pattern. NVML is dlopen'd and exposes no
// per-call cancellation; the wrapper is the only safe way to bound
// the call without crashing the daemon. On deadline the goroutine
// is abandoned (the call keeps running inside the kernel) and the
// caller sees a wrapped context.DeadlineExceeded.
//
// nvmlResetFn is the package-level seam tests inject — production
// resolves to nvidia.ResetFanSpeed at package init.
func nvmlResetWithDeadline(idx uint, deadline time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- nvmlResetFn(idx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("hal/nvml: reset gpu=%d abandoned: %w", idx, ctx.Err())
	}
}

func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		return v, nil
	case *State:
		if v == nil {
			return State{}, fmt.Errorf("hal/nvml: nil opaque state")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("hal/nvml: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}

func parseIndex(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("hal/nvml: parse gpu index %q: %w", s, err)
	}
	return uint(v), nil
}
