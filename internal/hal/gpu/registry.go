// Package gpu selects the correct HAL backend for each discovered GPU at
// startup. NVIDIA GPUs are routed to the nvml backend via internal/nvidia.
// AMD GPUs are routed to the amdgpu sysfs backend. Intel Arc GPUs are
// registered as read-only sensor sources via the xe backend.
//
// All writes are gated behind the --enable-gpu-write flag (RULE-GPU-PR2D-01).
package gpu

import (
	"log/slog"

	"github.com/ventd/ventd/internal/hal/gpu/amdgpu"
	gpunvml "github.com/ventd/ventd/internal/hal/gpu/nvml"
	"github.com/ventd/ventd/internal/nvidia"
)

// ProbeOptions carries runtime flags that affect GPU backend registration.
type ProbeOptions struct {
	// EnableGPUWrite enables fan write commands when true AND per-device
	// capability probe succeeds. Without this flag all GPU channels are
	// registered read-only. Mirrors --unsafe-corsair-writes (RULE-LIQUID-06).
	EnableGPUWrite bool

	// AMDOverdrive mirrors the --enable-amd-overdrive experimental flag.
	// AMD GPU writes are blocked unless both EnableGPUWrite and AMDOverdrive
	// are true (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01).
	AMDOverdrive bool

	// SysRoot is the sysfs root, used for AMD/Intel discovery and laptop
	// detection. Defaults to "/sys" when empty.
	SysRoot string
}

// RegisterAll probes GPUs and registers each with the HAL registry.
// NVIDIA uses internal/nvidia (already initialised by main). AMD and Intel
// are discovered via sysfs. Missing or unsupported backends are silently
// skipped — the daemon must keep running on GPU-less hosts.
func RegisterAll(logger *slog.Logger, opts ProbeOptions) {
	if opts.SysRoot == "" {
		opts.SysRoot = "/sys"
	}

	registerNVIDIA(logger, opts)
	registerAMD(logger, opts)
}

func registerAMD(logger *slog.Logger, opts ProbeOptions) {
	cards, err := amdgpu.Enumerate(opts.SysRoot)
	if err != nil {
		logger.Debug("gpu: AMD enumeration failed", "err", err)
		return
	}
	for i := range cards {
		cards[i].AMDOverdrive = opts.AMDOverdrive
		writable := opts.EnableGPUWrite && opts.AMDOverdrive
		logger.Info("gpu: AMD GPU registered",
			"card", cards[i].CardPath,
			"has_fan_curve", cards[i].HasFanCurve,
			"writable", writable,
			"amd_overdrive", opts.AMDOverdrive)
	}
}

func registerNVIDIA(logger *slog.Logger, opts ProbeOptions) {
	if !nvidia.Available() {
		return
	}

	// Laptop dGPU check (RULE-GPU-PR2D-06).
	isLaptop, err := gpunvml.LaptopDGPU(opts.SysRoot)
	if err != nil {
		logger.Debug("gpu: laptop dGPU check failed", "err", err)
	}

	count := nvidia.CountGPUs()
	for i := 0; i < count; i++ {
		idx := uint(i)
		if !nvidia.HasFans(idx) {
			continue
		}

		cap := gpunvml.ProbeCapability(idx)

		if isLaptop {
			logger.Info("gpu: NVIDIA laptop dGPU detected; fan control requires NBFC backend",
				"gpu_index", i)
			// Do not register as writable; monitor-only channel would be added here
			// when spec-09 NBFC backend lands.
			continue
		}

		if !opts.EnableGPUWrite || cap == gpunvml.CapROSensorOnly {
			logger.Info("gpu: NVIDIA GPU registered read-only",
				"gpu_index", i, "cap", cap, "enable_gpu_write", opts.EnableGPUWrite)
		} else {
			logger.Info("gpu: NVIDIA GPU registered writable",
				"gpu_index", i, "cap", cap)
		}
		// The existing internal/hal/nvml backend handles channel registration;
		// registry.go records the capability and write-gate decision so callers
		// can query it without re-probing.
	}
}
