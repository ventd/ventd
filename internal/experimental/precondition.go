package experimental

import (
	"github.com/ventd/ventd/internal/experimental/checks"
)

// Precondition describes whether an experimental feature's prerequisites are met.
type Precondition struct {
	Met    bool
	Detail string
}

// Check returns the precondition status for a named experimental flag.
// For "amd_overdrive" the real check parses /proc/cmdline for
// amdgpu.ppfeaturemask. Unknown flags return Met=false.
func Check(flag string) Precondition {
	switch flag {
	case "amd_overdrive":
		met, detail := checks.CheckAMDOverdrivePrecondition("/proc/cmdline")
		return Precondition{Met: met, Detail: detail}
	default:
		return Precondition{
			Met:    false,
			Detail: "precondition check not yet implemented",
		}
	}
}
