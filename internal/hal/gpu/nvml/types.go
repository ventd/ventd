package nvml

import "errors"

// Capability describes the write capability of an NVML device as determined
// by the capability probe.
type Capability int

const (
	CapROSensorOnly Capability = iota // pre-Maxwell or no NVML fan API
	CapRWQuirk                        // R515..R519: SetFanSpeed_v2 only, no policy
	CapRWFull                         // R520+: SetFanSpeed_v2 + SetFanControlPolicy
)

// ErrWriteGated is returned when a write is attempted on a GPU whose capability
// probe returned CapROSensorOnly (pre-Maxwell hardware or pre-R515 NVIDIA
// driver). The v0.8.x sweep removed the --enable-gpu-write opt-in gate;
// the per-device capability probe is the remaining load-bearing constraint
// because the NVML symbols nvmlDeviceSetFanSpeed_v2 / nvmlDeviceSetFanControlPolicy
// genuinely do not exist on earlier driver branches.
var ErrWriteGated = errors.New("gpu write gated: capability probe returned read-only (pre-Maxwell or pre-R515 NVIDIA driver)")

// ErrLaptopDgpuRequiresEC is returned when a write is attempted on a dGPU
// in a laptop chassis where the EC manages fans (see RULE-GPU-PR2D-06).
var ErrLaptopDgpuRequiresEC = errors.New("laptop dGPU fan control requires the NBFC backend (spec-09); run `ventd doctor` to see whether this laptop is in the upstream nbfc-linux catalogue")
