// Package gpu selects the correct HAL backend for each discovered GPU at
// startup. NVIDIA GPUs are routed to the nvml backend via internal/nvidia.
// AMD GPUs are routed to the amdgpu sysfs backend. Intel Arc GPUs are
// not registered — fan control is firmware-managed on Intel discrete
// GPUs (no userspace pwm sysfs path). The catalogue at
// internal/hwdb/catalog/drivers/{xe,i915}.yaml carries the
// fan_control_capable=false declaration that the matcher consults.
//
// GPU writes ship enabled by default. The remaining safety gates are
// load-bearing driver/firmware constraints, not opt-in policy:
//   - NVIDIA: per-device capability probe (RULE-GPU-PR2D-01) — refuses
//     write on pre-Maxwell / pre-R515 where the NVML symbols are missing.
//   - NVIDIA: laptop dGPU detection (RULE-GPU-PR2D-06) — fans routed via
//     EC, NBFC backend required.
//   - AMD: --enable-amd-overdrive experimental gate (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01).
package gpu

import (
	"log/slog"

	"github.com/ventd/ventd/internal/hal/gpu/amdgpu"
	gpunvml "github.com/ventd/ventd/internal/hal/gpu/nvml"
	"github.com/ventd/ventd/internal/nvidia"
)

// ProbeOptions carries runtime flags that affect GPU backend registration.
type ProbeOptions struct {
	// AMDOverdrive mirrors the --enable-amd-overdrive experimental flag.
	// AMD GPU writes are blocked unless this is true
	// (RULE-EXPERIMENTAL-AMD-OVERDRIVE-01).
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
		writable := opts.AMDOverdrive
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

		if cap == gpunvml.CapROSensorOnly {
			logger.Info("gpu: NVIDIA GPU registered read-only",
				"gpu_index", i, "cap", cap,
				"reason", "capability probe refused (pre-Maxwell or pre-R515 driver)")
		} else {
			logger.Info("gpu: NVIDIA GPU registered writable",
				"gpu_index", i, "cap", cap)
		}
		// The existing internal/hal/nvml backend handles channel registration;
		// registry.go records the capability and write-gate decision so callers
		// can query it without re-probing.
	}
}
