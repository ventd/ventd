package nvml

import (
	"github.com/ventd/ventd/internal/nvidia"
)

// ProbeCapability attempts to determine the write capability of the GPU at
// index by probing nvmlDeviceSetFanControlPolicy. The probe is non-destructive:
// it reads the current policy and writes the same value back.
//
// Returns:
//   - CapRWFull      — R520+, SetFanControlPolicy succeeded
//   - CapRWQuirk     — R515..R519, SetFanSpeed_v2 available but no policy API
//   - CapROSensorOnly — pre-Maxwell or no fan write support
func ProbeCapability(index uint) Capability {
	if !nvidia.Available() {
		return CapROSensorOnly
	}
	if !nvidia.HasFans(index) {
		return CapROSensorOnly
	}
	// Probe with the "auto" policy (restoring current state): a non-destructive
	// call that returns NOT_SUPPORTED on pre-Maxwell and FUNCTION_NOT_FOUND
	// (resolved in nvidia package as symbol-absent → false) on pre-R520.
	ok, err := nvidia.SetFanControlPolicy(index, 0, nvidia.FanPolicyTemperatureContinuos)
	if err != nil {
		return CapROSensorOnly
	}
	if ok {
		return CapRWFull
	}
	// Symbol absent (pre-R520) but SetFanSpeed_v2 may still work.
	return CapRWQuirk
}
