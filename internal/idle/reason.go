package idle

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
