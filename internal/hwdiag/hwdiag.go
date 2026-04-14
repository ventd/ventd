// Package hwdiag is the canonical type + store for hardware diagnostics that
// the web UI surfaces to the user. Every tier emits into this package; the UI
// consumes via /api/hwdiag.
//
// Stability: field names are part of the wire format (JSON) and the ID values
// are part of the UX contract (the UI keys off them). Change only with a
// compatibility plan.
package hwdiag

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Component groups diagnostics by which subsystem produced them. The UI
// renders one section per component.
type Component string

const (
	ComponentCalibration Component = "calibration"
	ComponentHwmon       Component = "hwmon"
	ComponentOOT         Component = "oot"         // out-of-tree modules (NCT6687D, it87 forks, …)
	ComponentDMI         Component = "dmi"         // DMI-triggered candidate modules
	ComponentGPU         Component = "gpu"         // NVIDIA / AMD GPU probes
	ComponentBoot        Component = "boot"        // bootloader detection (GRUB, systemd-boot, UKI, rEFInd, syslinux/extlinux)
	ComponentSecureBoot  Component = "secureboot"  // Secure Boot + MOK enrollment
	ComponentNixOS       Component = "nixos"       // NixOS configuration snippet panel
	ComponentARM         Component = "arm"         // ARM SBC device-tree overlay
	ComponentIPMI        Component = "ipmi"        // IPMI / BMC override cycle
	ComponentBIOS        Component = "bios"        // BIOS-managed fan headers
	ComponentNvidia      Component = "nvidia"      // NVML-specific (Wayland WriteFanSpeed self-test etc.)
	ComponentHardware    Component = "hardware"    // generic hardware-change detection (Tier 0.3)
)

// Severity labels the urgency. "info" is a badge; "warn" draws attention;
// "error" blocks a workflow.
type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// AutoFixID identifies a remediation the UI can surface as a button. Empty
// means the diagnostic is informational or the fix is manual (rendered as
// markdown Detail instead).
type AutoFixID string

const (
	AutoFixRecalibrate       AutoFixID = "RECALIBRATE_REQUIRED"
	AutoFixReRunSetup        AutoFixID = "RERUN_SETUP"
	AutoFixInstallKernelHdrs AutoFixID = "INSTALL_KERNEL_HEADERS"
	AutoFixInstallDKMS       AutoFixID = "INSTALL_DKMS"
	AutoFixMOKEnroll         AutoFixID = "MOK_ENROLL"
	AutoFixNvidiaPersistence AutoFixID = "NVIDIA_PERSISTENCE_MODE"
	AutoFixIPMIFanFull       AutoFixID = "IPMI_FAN_MODE_FULL"
	AutoFixTryModuleLoad     AutoFixID = "TRY_MODULE_LOAD"
)

// Remediation describes the structured action attached to a diagnostic. The
// UI renders it as a button labelled Label; clicking POSTs Endpoint (when
// non-empty). AutoFixID is a stable machine key the server uses to dispatch.
type Remediation struct {
	AutoFixID AutoFixID `json:"auto_fix_id,omitempty"`
	Label     string    `json:"label,omitempty"`
	// Endpoint is the server-relative POST target the UI button hits. When
	// empty the UI still shows the button but disables it with a TODO tooltip
	// — useful for emitters that land before their fix endpoint does.
	Endpoint string `json:"endpoint,omitempty"`
}

// Entry is a single diagnostic. ID is stable (dot-separated "component.key")
// and is the replacement key in the store: emitting the same ID twice
// replaces the first entry rather than creating a duplicate.
type Entry struct {
	ID          string         `json:"id"`
	Component   Component      `json:"component"`
	Severity    Severity       `json:"severity"`
	Summary     string         `json:"summary"`
	Detail      string         `json:"detail,omitempty"` // markdown; multi-line OK
	Timestamp   time.Time      `json:"timestamp"`
	Remediation *Remediation   `json:"remediation,omitempty"`
	Affected    []string       `json:"affected,omitempty"` // sysfs paths, fan names, GPU indices, etc.
	Context     map[string]any `json:"context,omitempty"`  // free-form tier-specific data
}

// Store holds the current diagnostic set. Concurrent-safe. Entries are keyed
// by ID so re-running a probe is idempotent.
type Store struct {
	mu       sync.RWMutex
	byID     map[string]Entry
	revision uint64 // monotonic; bumped on any mutation that changes visible state
	now      func() time.Time
}

// NewStore constructs an empty store.
func NewStore() *Store {
	return &Store{
		byID: make(map[string]Entry),
		now:  time.Now,
	}
}

// Set adds or replaces an entry by ID. The timestamp is filled from the
// store's clock if the caller didn't supply one. Always bumps revision so
// UI pollers can detect the change even when the only diff is the timestamp
// (a probe re-running is a signal worth showing).
func (s *Store) Set(e Entry) {
	if e.ID == "" {
		panic("hwdiag: Set called with empty Entry.ID")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[e.ID] = e
	atomic.AddUint64(&s.revision, 1)
}

// Remove deletes the entry with this ID. No-op (and no revision bump) if
// absent — idempotent for callers that clear on every probe start.
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return
	}
	delete(s.byID, id)
	atomic.AddUint64(&s.revision, 1)
}

// ClearComponent deletes every entry for a given component. Used when a tier
// re-runs from scratch and wants to drop stale entries before re-emitting.
// No-op (no revision bump) if nothing matched.
func (s *Store) ClearComponent(c Component) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := false
	for id, e := range s.byID {
		if e.Component == c {
			delete(s.byID, id)
			removed = true
		}
	}
	if removed {
		atomic.AddUint64(&s.revision, 1)
	}
}

// Revision returns the current monotonic counter. Cheap; callers can poll
// this before fetching a full snapshot to avoid rerendering when nothing
// changed.
func (s *Store) Revision() uint64 {
	return atomic.LoadUint64(&s.revision)
}

// Filter narrows a Snapshot. Zero-value Filter returns everything.
type Filter struct {
	Component Component
	Severity  Severity
}

// Snapshot is the /api/hwdiag wire shape.
type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Revision    uint64    `json:"revision"`
	Entries     []Entry   `json:"entries"`
}

// Snapshot returns the current set as a stable, filterable view. Entries are
// sorted by (component, severity desc, id) so downstream JSON is stable for
// snapshot tests.
func (s *Store) Snapshot(f Filter) Snapshot {
	s.mu.RLock()
	entries := make([]Entry, 0, len(s.byID))
	for _, e := range s.byID {
		if f.Component != "" && e.Component != f.Component {
			continue
		}
		if f.Severity != "" && e.Severity != f.Severity {
			continue
		}
		entries = append(entries, e)
	}
	rev := atomic.LoadUint64(&s.revision)
	s.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Component != entries[j].Component {
			return entries[i].Component < entries[j].Component
		}
		if rank := sevRank(entries[j].Severity) - sevRank(entries[i].Severity); rank != 0 {
			return rank < 0
		}
		return entries[i].ID < entries[j].ID
	})

	return Snapshot{
		GeneratedAt: s.now(),
		Revision:    rev,
		Entries:     entries,
	}
}

func sevRank(s Severity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarn:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Stable seed IDs. New emitters should add their ID here so we have a single
// place to audit what the UI knows about.
const (
	IDCalibrationFutureSchema = "calibration.future_schema"

	// Tier 0.5 — out-of-tree module fallback chain.
	IDOOTKernelHeadersMissing = "oot.kernel_headers_missing"
	IDOOTDKMSMissing          = "oot.dkms_missing"
	IDOOTSecureBoot           = "oot.secureboot_blocks"
	IDOOTKernelTooNew         = "oot.kernel_too_new"
	IDOOTBuildFailed          = "oot.build_failed"

	// Tier 3 — DMI-triggered candidate modules. Only emitted when the
	// capability-first pass found no controllable hwmon device. Per-candidate
	// IDs are dmi.candidate.<driver-key> so the UI can render one button each.
	IDDMINoMatch         = "dmi.no_match"
	IDDMICandidatePrefix = "dmi.candidate." // append DriverNeed.Key

	// Tier 0.3 — hardware-change detection. Single rolled-up entry describing
	// the most recent add/remove across all hwmon devices; the UI surfaces it
	// with a "Re-run setup" button (AutoFixReRunSetup → /api/setup/start).
	IDHardwareChanged = "hardware.topology_changed"
)
