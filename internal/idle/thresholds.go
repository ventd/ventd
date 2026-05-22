package idle

import "github.com/ventd/ventd/internal/sysclass"

// SoftIdleThresholds is the per-class set of soft-mode workload ceilings
// for OpportunisticGate. Different system classes tolerate different
// steady-state "idle" load — a laptop user-session that creeps above
// 10 % CPU PSI is no longer idle from the operator's perspective, but
// a homelab server running k3s + Plex + nightly scrapers happily sits
// at 30-40 % avg60 as its normal idle. A single global ceiling either
// blocks every probe on the homelab (the 10 % laptop number) or
// admits probes mid-user-work on a laptop (the 40 % server number);
// per-class lookup is the only way both behaviours are correct.
//
// All thresholds are PSI percentages on the "some" channel (any task
// stalled) for CPU/IO and the "full" channel (every task stalled) for
// memory; loadavg is the per-CPU fallback used on kernels < 4.20
// where PSI is absent.
type SoftIdleThresholds struct {
	// PSICpuAvg60 is the ceiling for /proc/pressure/cpu some avg60 (%).
	PSICpuAvg60 float64
	// PSIIoAvg60 is the ceiling for /proc/pressure/io some avg60 (%).
	PSIIoAvg60 float64
	// PSIMemAvg60 is the ceiling for /proc/pressure/memory full avg60 (%).
	// Memory pressure is a physical signal; workload class doesn't shift
	// it, so this stays at the strict value across all classes.
	PSIMemAvg60 float64
	// LoadAvgPerCPU is the ceiling for loadavg[0] / ncpus on PSI-less
	// kernels.
	LoadAvgPerCPU float64
}

// classSoftIdleThresholds maps each SystemClass to its soft-idle
// thresholds. Calibration notes per class are inline; the broad shape
// is: tighter ceilings for user-facing classes (laptop), wider
// ceilings for steady-load classes (server, NAS).
var classSoftIdleThresholds = map[sysclass.SystemClass]SoftIdleThresholds{
	// Laptop: user is present at a keyboard. The keypress / trackpad
	// activity that would make a fan ramp jarring is already
	// detected — and refused — explicitly by RULE-OPP-IDLE-02
	// (recent_input_irq), and an active SSH session by
	// RULE-OPP-IDLE-03 (active_ssh_session). PSI is for background
	// workload (snapd updates, dnf metadata refresh, browser tabs),
	// which doesn't correspond to user-perceptible moments where a
	// fan ramp would be noticeable. The original 10 % laptop
	// ceiling was redundant with the IRQ check on the "user is
	// actually typing" axis and over-tight on the background-load
	// axis — it refused every probe on any box doing routine
	// userspace work. 20 % matches mid-desktop; the IRQ + SSH
	// checks remain the load-bearing user-protection signals.
	sysclass.ClassLaptop: {
		PSICpuAvg60:   20.0,
		PSIIoAvg60:    20.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.0,
	},
	// Mid-desktop: workstation with intermittent multitasking; the
	// operator tolerates more background load before it qualifies as
	// "not idle". Mid-point between laptop and HEDT.
	sysclass.ClassMidDesktop: {
		PSICpuAvg60:   20.0,
		PSIIoAvg60:    20.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.0,
	},
	// HEDT-Air / HEDT-AIO: workstation with regular heavy multitasking
	// (compile farms, render previews, VM hosts); steady-state
	// background load is the norm.
	sysclass.ClassHEDTAir: {
		PSICpuAvg60:   30.0,
		PSIIoAvg60:    25.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.5,
	},
	sysclass.ClassHEDTAIO: {
		PSICpuAvg60:   30.0,
		PSIIoAvg60:    25.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.5,
	},
	// Server: 24/7 services (DBs, k8s nodes, reverse proxies); steady
	// 30-40 % CPU PSI is the normal idle baseline. Tight thresholds
	// here mean the probe never runs, defeating opportunistic learning.
	sysclass.ClassServer: {
		PSICpuAvg60:   40.0,
		PSIIoAvg60:    30.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 2.0,
	},
	// Mini-PC: home media box / lightweight server (Plex, Jellyfin,
	// HA); moderate steady background load typical.
	sysclass.ClassMiniPC: {
		PSICpuAvg60:   25.0,
		PSIIoAvg60:    25.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.0,
	},
	// NAS-HDD: background scrubs, RAID syncs, scheduled snapshots
	// produce sustained IO and CPU PSI; the gate should treat that as
	// steady idle, not refuse.
	sysclass.ClassNASHDD: {
		PSICpuAvg60:   40.0,
		PSIIoAvg60:    35.0,
		PSIMemAvg60:   0.5,
		LoadAvgPerCPU: 1.5,
	},
}

// LookupSoftIdleThresholds returns the soft-mode workload ceilings for
// the given class. ClassUnknown and any unrecognised value fall
// through to ClassMidDesktop — the safe consumer default that matches
// the envelope-thresholds fallback for the same case.
func LookupSoftIdleThresholds(cls sysclass.SystemClass) SoftIdleThresholds {
	if t, ok := classSoftIdleThresholds[cls]; ok {
		return t
	}
	return classSoftIdleThresholds[sysclass.ClassMidDesktop]
}
