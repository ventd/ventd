// Package experimental manages the set of opt-in experimental feature flags
// that unlock vendor-locked or kernel-tainting control paths. All flags default
// to false; none are active unless the operator explicitly enables them via CLI
// flag or config-file entry. See spec-15.
package experimental

// Flags is the resolved set of experimental feature flags for a running daemon.
// Fields are additive: true means the feature is enabled; false means disabled.
type Flags struct {
	AMDOverdrive    bool
	NVIDIACoolbits  bool
	ILO4Unlocked    bool
	IDRAC9LegacyRaw bool
}

// all is the canonical ordered list of flag names. Order is stable; consumers
// that iterate this list (doctor output, diag bundle JSON) must not re-sort.
var all = []string{
	"amd_overdrive",
	"nvidia_coolbits",
	"ilo4_unlocked",
	"idrac9_legacy_raw",
}

// All returns the canonical ordered list of flag names.
func All() []string {
	out := make([]string, len(all))
	copy(out, all)
	return out
}

// Active returns the names of flags currently set to true, in canonical order.
func (f Flags) Active() []string {
	var out []string
	for _, name := range all {
		v, ok := f.Get(name)
		if ok && v {
			out = append(out, name)
		}
	}
	return out
}

// Get returns the bool value for a flag name and ok=true for known names.
// Returns (false, false) for unknown names.
func (f Flags) Get(name string) (value, ok bool) {
	switch name {
	case "amd_overdrive":
		return f.AMDOverdrive, true
	case "nvidia_coolbits":
		return f.NVIDIACoolbits, true
	case "ilo4_unlocked":
		return f.ILO4Unlocked, true
	case "idrac9_legacy_raw":
		return f.IDRAC9LegacyRaw, true
	}
	return false, false
}
