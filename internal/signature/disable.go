package signature

// DisableReason categorises why the library was started in the
// permanent-disabled state. Surfaced in logs and (eventually) in
// v0.5.10 doctor output. The library itself doesn't act on the
// reason — it just emits FallbackLabelDisabled — but the reason
// helps an operator understand "why is signature learning off?"
type DisableReason int

const (
	// DisableReasonNone — library is enabled.
	DisableReasonNone DisableReason = iota
	// DisableReasonContainer — running inside an unprivileged
	// container per R1 Tier-2 BLOCK. Library refuses because
	// /proc/PID/comm reflects either the host PID namespace
	// (--pid=host) or the container's; either way the labels
	// have no operational meaning for the host's fan curves.
	DisableReasonContainer
	// DisableReasonHardwareRefused — running on a platform R3
	// classifies as hardware-refused (Steam Deck etc.).
	DisableReasonHardwareRefused
	// DisableReasonOperatorToggle — Config.SignatureLearningDisabled
	// is true.
	DisableReasonOperatorToggle
)

// String renders the reason for log lines.
func (r DisableReason) String() string {
	switch r {
	case DisableReasonNone:
		return "enabled"
	case DisableReasonContainer:
		return "container_or_vm"
	case DisableReasonHardwareRefused:
		return "hardware_refused"
	case DisableReasonOperatorToggle:
		return "operator_toggle_off"
	default:
		return "unknown"
	}
}

// ApplyDisableGate sets cfg.Disabled when any of the disable paths
// applies. Helper for daemon wiring; tests inject Config directly.
func ApplyDisableGate(cfg *Config, reason DisableReason) {
	if reason != DisableReasonNone {
		cfg.Disabled = true
	}
}
