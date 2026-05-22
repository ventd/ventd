package idle

import "strings"

// Reason is a structured refusal code returned by StartupGate and RuntimeCheck.
type Reason string

const (
	ReasonOK                     Reason = "ok"
	ReasonOnBattery              Reason = "on_battery"
	ReasonInContainer            Reason = "in_container"
	ReasonStorageMaintenance     Reason = "storage_maintenance"
	ReasonBootWarmup             Reason = "boot_warmup"
	ReasonPostResumeWarmup       Reason = "post_resume_warmup"
	ReasonBlockedProcess         Reason = "blocked_process"
	ReasonPSIPressure            Reason = "psi_pressure"
	ReasonCPUIdle                Reason = "cpu_idle_insufficient"
	ReasonDiskActivity           Reason = "disk_activity"
	ReasonNetActivity            Reason = "net_activity"
	ReasonGPUActivity            Reason = "gpu_activity"
	ReasonDurabilityInsufficient Reason = "durability_insufficient"
)

// WithDetail appends a colon-separated detail to the reason code, producing a
// structured string such as "blocked_process:rsync".
func (r Reason) WithDetail(detail string) Reason {
	return Reason(string(r) + ":" + detail)
}

// split returns the base reason code and the colon-suffix detail (if any).
// "blocked_process:rsync" → ("blocked_process", "rsync");
// "psi_pressure" → ("psi_pressure", "").
func (r Reason) split() (base, detail string) {
	s := string(r)
	if idx := strings.Index(s, ":"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

// Human returns a user-facing, sentence-length description of the
// reason suitable for dashboard / status-page rendering. Detail
// suffixes (e.g. "blocked_process:rsync", "recent_input_irq:irq=1")
// are folded into the sentence where they're informative and dropped
// where they're noise (the IRQ id of the keyboard isn't useful on a
// user-facing surface; the process name of a blocking sync IS).
//
// The raw Reason value remains the canonical form for logs, metrics,
// and operator tooling — Human() drives a sister last_reason_human
// field on the /api/v1/probe/opportunistic/status payload so the
// dashboard renders something readable without losing the
// machine-parseable code in the journal.
//
// Unknown reason codes (e.g. a future Reason added without a Human
// case) fall through to the raw string so operators can still see
// and debug them; the dashboard's worst case is the same string it
// used to render before this method existed.
func (r Reason) Human() string {
	base, detail := r.split()
	switch Reason(base) {
	case ReasonOK:
		return "All checks passed — probe eligible"
	case ReasonOnBattery:
		return "On battery — probes paused to preserve battery life"
	case ReasonInContainer:
		return "Running inside a container — probes paused"
	case ReasonStorageMaintenance:
		return "Storage maintenance (scrub or resync) in progress — probes paused"
	case ReasonBootWarmup:
		return "Boot warm-up window — probes deferred for the first few minutes"
	case ReasonPostResumeWarmup:
		return "Just resumed from sleep — probes deferred briefly"
	case ReasonBlockedProcess:
		if detail != "" {
			return "Blocking process running: " + detail
		}
		return "A blocking process is running — probes paused"
	case ReasonPSIPressure:
		return "System under load — waiting for a quiet moment"
	case ReasonCPUIdle:
		return "CPU not idle enough — waiting for a quiet moment"
	case ReasonDiskActivity:
		return "Disk activity — waiting for a quiet moment"
	case ReasonNetActivity:
		return "Network activity — waiting for a quiet moment"
	case ReasonGPUActivity:
		return "GPU activity — waiting for a quiet moment"
	case ReasonDurabilityInsufficient:
		return "Idle predicate hasn't held long enough yet"
	case ReasonRecentInputIRQ:
		return "Recent keyboard or mouse input — waiting for a quiet moment"
	case ReasonActiveSSHSession:
		return "Active SSH session — probes paused while you're working"
	case ReasonOpportunisticDisabled:
		return "Opportunistic probing disabled in config"
	case ReasonOpportunisticBootWindow:
		return "Just installed — opportunistic probing kicks in shortly"
	case ReasonProcInterruptsUnreadable:
		return "Can't read /proc/interrupts — opportunistic probing paused"
	}
	return string(r)
}
