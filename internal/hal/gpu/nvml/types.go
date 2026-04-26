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

// ErrWriteGated is returned when a write is attempted without --enable-gpu-write
// or when the capability probe returned CapROSensorOnly.
var ErrWriteGated = errors.New("gpu write gated: enable with --enable-gpu-write and a supported driver")

// ErrLaptopDgpuRequiresEC is returned when a write is attempted on a dGPU
// in a laptop chassis where the EC manages fans (see RULE-GPU-PR2D-06).
var ErrLaptopDgpuRequiresEC = errors.New("laptop dGPU fan control requires userspace EC backend (spec-09 NBFC)")
