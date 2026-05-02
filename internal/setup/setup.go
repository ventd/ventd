// Package setup implements the first-boot setup wizard: discovers fans,
// calibrates them, and generates an initial config. Used by both the CLI
// --setup flag (RunBlocking) and the web UI (Start + polling via Progress).
package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	hwmonpkg "github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/recovery"
)

// FanState describes a single fan during/after the setup process.
type FanState struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // "hwmon" or "nvidia"
	PWMPath       string `json:"pwm_path"`
	RPMPath       string `json:"rpm_path,omitempty"`
	ControlKind   string `json:"control_kind,omitempty"`   // "rpm_target" for fan*_target channels
	DetectPhase   string `json:"detect_phase"`             // "pending","detecting","found","none","n/a"
	PolarityPhase string `json:"polarity_phase,omitempty"` // "pending","testing","normal","inverted","phantom"
	CalPhase      string `json:"cal_phase"`                // "pending","calibrating","done","skipped","error"
	StartPWM      uint8  `json:"start_pwm,omitempty"`
	StopPWM       uint8  `json:"stop_pwm,omitempty"`
	MaxRPM        int    `json:"max_rpm,omitempty"`
	IsPump        bool   `json:"is_pump,omitempty"`
	CalProgress   int    `json:"cal_progress"` // 0–100, live during calibrate phase
	Error         string `json:"error,omitempty"`
}

// Progress is the JSON payload returned by GET /api/setup/status.
type Progress struct {
	Needed        bool           `json:"needed"`                   // true until applied
	Running       bool           `json:"running"`                  // goroutine active
	Done          bool           `json:"done"`                     // goroutine finished
	Applied       bool           `json:"applied"`                  // config written to disk
	Error         string         `json:"error,omitempty"`          // plain-English fatal error
	RebootNeeded  bool           `json:"reboot_needed,omitempty"`  // boot config was patched; user must reboot
	RebootMessage string         `json:"reboot_message,omitempty"` // explanation shown in the reboot panel
	Phase         string         `json:"phase,omitempty"`          // detecting | installing_driver | scanning_fans | detecting_rpm | calibrating
	PhaseMsg      string         `json:"phase_msg,omitempty"`      // human-readable description of current phase
	Board         string         `json:"board,omitempty"`          // "Gigabyte B550M AORUS PRO"
	ChipName      string         `json:"chip_name,omitempty"`      // "IT8688E" — set when installing driver
	InstallLog    []string       `json:"install_log,omitempty"`    // streamed during installing_driver phase
	Fans          []FanState     `json:"fans"`
	Config        *config.Config `json:"config,omitempty"`
	Profile       *HWProfile     `json:"profile,omitempty"`

	// v0.5.9 wizard recovery (#800). When Error is non-empty,
	// FailureClass classifies the error and Remediation lists
	// actionable cards the UI renders above the existing error
	// banner buttons. Both fields are empty when Error is empty
	// or when the classifier returns ClassUnknown — the UI
	// falls back to the generic "Send diagnostic bundle" card.
	//
	// The shape mirrors the cross-cutting recovery package
	// (internal/recovery) so the doctor surface can reuse it
	// for runtime issues post-install. JSON tag string-typed for
	// schema stability.
	FailureClass string                 `json:"failure_class,omitempty"`
	Remediation  []recovery.Remediation `json:"remediation,omitempty"`
}

// GPUProfile holds per-GPU hardware metadata for one NVML or AMD GPU.
type GPUProfile struct {
	Index    int     `json:"index"`
	Model    string  `json:"model,omitempty"`
	PowerW   int     `json:"power_w,omitempty"`   // power limit in watts; 0=unknown
	ThermalC float64 `json:"thermal_c,omitempty"` // slowdown/crit threshold in °C; 0=unknown
}

// HWProfile contains hardware metadata gathered during setup. Used to explain
// curve design choices to the user and to tune curve parameters per-device.
type HWProfile struct {
	CPUModel    string       `json:"cpu_model,omitempty"`
	CPUTDPW     int          `json:"cpu_tdp_w,omitempty"`     // watts from RAPL; 0=unknown
	CPUThermalC float64      `json:"cpu_thermal_c,omitempty"` // TjMax/Tcrit in °C; 0=unknown
	GPUModel    string       `json:"gpu_model,omitempty"`     // GPU 0 model (kept for compat)
	GPUPowerW   int          `json:"gpu_power_w,omitempty"`   // GPU 0 power limit; 0=unknown
	GPUThermalC float64      `json:"gpu_thermal_c,omitempty"` // GPU 0 slowdown/crit; 0=unknown
	GPUs        []GPUProfile `json:"gpus,omitempty"`          // all GPUs, indexed
	CurveNotes  []string     `json:"curve_notes,omitempty"`   // human-readable curve design explanations
}

// Manager owns all setup wizard state.
type Manager struct {
	mu      sync.Mutex
	fans    []FanState
	running bool
	done    bool
	applied bool
	errMsg  string
	// failureClass is the recovery.FailureClass set DIRECTLY by
	// failure paths whose root cause maps cleanly without
	// text-classification (preflight Reason → class bridge,
	// in-tree-conflict detector → ClassInTreeConflict, etc.).
	// When non-empty, Status() prefers it over the text-based
	// recovery.Classify(errMsg) fallback. Without this bridge
	// the wizard relied on regex-matching the operator-facing
	// error string, which silently fell through to ClassUnknown
	// (bundle-only card) for every preflight-classified failure
	// — caught on Phoenix's HIL where ReasonStaleDKMSState fired
	// but the recovery cards stayed empty because no text rule
	// matched "DKMS already tracks" verbatim.
	failureClass   recovery.FailureClass
	rebootNeeded   bool
	rebootMessage  string
	phase          string
	phaseMsg       string
	board          string
	chipName       string
	installLog     []string
	result         *config.Config
	profile        *HWProfile
	cal            *calibrate.Manager
	logger         *slog.Logger
	cancel         context.CancelFunc // fired by Abort; nil until Start wires it
	diagStore      *hwdiag.Store      // optional; when non-nil, preflight blockers are emitted here
	polarityProber polarity.Prober    // nil = skip polarity probe (tests); set by SetPolarityProber
	reprobeFn      ReProber           // nil = skip post-install re-probe; set by SetReProber

	// vendorDaemonProbe overrides the production vendor-daemon detection
	// for tests. nil = use recovery.DetectVendorDaemon with the live
	// systemctl probe (production). Tests inject a stub via
	// SetVendorDaemonProbe to drive the wizard's monitor-only short-
	// circuit path without spawning systemctl.
	vendorDaemonProbe func(context.Context) recovery.VendorDaemon

	// settleAfterModprobe is the wait between a successful modprobe and the
	// reprobe call so kernel hwmon class registration completes. Production
	// uses defaultSettleAfterModprobe; tests inject 0 via newWithSettle.
	settleAfterModprobe time.Duration

	// Sysfs/procfs roots. Defaults resolve to the production paths; tests use
	// NewWithRoots to inject a t.TempDir()-rooted fixture tree. Keeping these
	// on the Manager rather than as free-function args means run() can call
	// the discovery methods without threading a config through every step.
	hwmonRoot    string // e.g. "/sys/class/hwmon"
	procRoot     string // e.g. "/proc"
	powercapRoot string // e.g. "/sys/class/powercap"

	// appliedMarkerPath is the path to a zero-byte sentinel file created
	// by MarkApplied so that "setup has been completed" survives a daemon
	// restart even when the persisted config has no Controls (the
	// monitor-only / no-fans path). Empty in tests — NewWithRoots leaves
	// it unset so unit tests don't write to /var/lib/ventd/. Production
	// code uses New, which sets it to defaultAppliedMarkerPath.
	appliedMarkerPath string
}

// Default path roots used when the Manager is constructed via New. Exported
// so tests that need to assert production defaults don't have to duplicate
// the strings.
const (
	defaultHwmonRoot         = "/sys/class/hwmon"
	defaultProcRoot          = "/proc"
	defaultPowercapRoot      = "/sys/class/powercap"
	defaultAppliedMarkerPath = "/var/lib/ventd/.setup-applied"

	// defaultSettleAfterModprobe is the wait between a successful modprobe
	// and the reprobe call. The kernel's hwmon class registration is
	// synchronous from module_init on every chip family observed in the
	// wild, but the platform-device → hwmon binding can take a few hundred
	// milliseconds on slower hardware (e.g. Z690-A NCT6687D); 2 s leaves
	// generous headroom without making the wizard feel sluggish.
	defaultSettleAfterModprobe = 2 * time.Second
)

// ReProber re-runs the daemon-level hardware probe and persists the
// updated outcome to the state KV after a successful driver install or
// kernel module load. Wired by cmd/ventd/main.go so the wizard is
// decoupled from the state package internals.
//
// A nil ReProber is a no-op; tests that don't care about the persisted
// outcome leave it unset. Errors returned by the ReProber are logged at
// WARN but do not fail the wizard step — the sysfs probe done by Phase 4
// produces the wizard's local decision regardless of the persisted KV.
type ReProber func(ctx context.Context) error

// SetDiagnosticStore attaches the process-wide hwdiag store. When set, the
// setup manager emits OOT-preflight blockers (Secure Boot, kernel headers,
// DKMS, kernel-too-new) into the store before attempting driver install.
func (m *Manager) SetDiagnosticStore(s *hwdiag.Store) {
	m.mu.Lock()
	m.diagStore = s
	m.mu.Unlock()
}

// SetPolarityProber attaches a polarity.Prober used to classify each fan
// channel as normal/inverted/phantom during the probing_polarity wizard phase.
// When nil (the default), the polarity probe phase is skipped and PolarityPhase
// is left empty — existing tests rely on this skip behaviour.
func (m *Manager) SetPolarityProber(p polarity.Prober) {
	m.mu.Lock()
	m.polarityProber = p
	m.mu.Unlock()
}

// SetReProber wires the post-install / post-load-module re-probe hook
// (#766). Production code (cmd/ventd/main.go) sets this to a closure that
// re-runs probe.New(...).Probe and PersistOutcome to KV, so a freshly-
// loaded driver flips wizard.initial_outcome from monitor_only to control
// without waiting for the next daemon restart.
func (m *Manager) SetReProber(rp ReProber) {
	m.mu.Lock()
	m.reprobeFn = rp
	m.mu.Unlock()
}

// SetVendorDaemonProbe overrides the wizard's vendor-daemon detection
// callback for tests. Production code leaves this unset; run() falls back
// to recovery.DetectVendorDaemon with the live systemctl probe. Tests use
// this hook to drive the monitor-only short-circuit path without spawning
// systemctl or relying on a vendor unit being installed in the sandbox.
func (m *Manager) SetVendorDaemonProbe(fn func(context.Context) recovery.VendorDaemon) {
	m.mu.Lock()
	m.vendorDaemonProbe = fn
	m.mu.Unlock()
}

// New creates a Manager with the production sysfs/procfs path roots. NVML
// lifecycle is managed via the refcount-safe nvidia.Init/Shutdown pair;
// setup does not need to know whether the daemon already initialised NVML.
//
// New does NOT wire the persistent applied-marker path — production
// callers (cmd/ventd/main.go) must call SetAppliedMarkerPath explicitly
// so unit tests that construct via New(t) don't write to
// /var/lib/ventd/.setup-applied or pick up a marker left by a sibling
// test (which would make ProgressNeeded test cases bleed into each
// other on hosts where the directory is writable).
func New(cal *calibrate.Manager, logger *slog.Logger) *Manager {
	return NewWithRoots(cal, logger, defaultHwmonRoot, defaultProcRoot, defaultPowercapRoot)
}

// SetAppliedMarkerPath wires the path to the persistent setup-applied
// sentinel. Production code (cmd/ventd/main.go) calls this with
// DefaultAppliedMarkerPath. Tests typically leave the path unset so
// MarkApplied is a process-local flip and IsApplied falls back to the
// in-memory value only.
func (m *Manager) SetAppliedMarkerPath(path string) {
	m.mu.Lock()
	m.appliedMarkerPath = path
	m.mu.Unlock()
}

// DefaultAppliedMarkerPath is the production default for the sentinel
// file written by MarkApplied. Exported so cmd/ventd/main.go can pass
// it to SetAppliedMarkerPath without duplicating the string literal.
const DefaultAppliedMarkerPath = defaultAppliedMarkerPath

// NewWithRoots is the test constructor: it takes explicit roots for
// /sys/class/hwmon, /proc, and /sys/class/powercap so a fixture tree can
// stand in for the real kernel interfaces. Production code uses New; this
// exists so hardware-discovery code paths remain testable.
func NewWithRoots(cal *calibrate.Manager, logger *slog.Logger, hwmonRoot, procRoot, powercapRoot string) *Manager {
	return &Manager{
		cal:                 cal,
		logger:              logger,
		hwmonRoot:           hwmonRoot,
		procRoot:            procRoot,
		powercapRoot:        powercapRoot,
		settleAfterModprobe: defaultSettleAfterModprobe,
	}
}

// Needed reports whether setup should be presented to the user.
// It returns true when the config has no controls defined (empty/first-boot state).
//
// This is the "config alone" predicate; for the full live-with-marker
// answer, callers should use Manager.ProgressNeeded which combines this
// with the persistent applied marker so a host that completed setup with
// no controllable fans (monitor-only) is not bounced back to the wizard
// on every restart.
func Needed(cfg *config.Config) bool {
	return len(cfg.Controls) == 0
}

// Start launches the setup goroutine. Returns an error if already running.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return fmt.Errorf("setup: already running")
	}
	if m.done {
		return fmt.Errorf("setup: already completed; restart the daemon for a fresh run")
	}
	m.running = true
	m.done = false
	m.errMsg = ""
	m.failureClass = ""
	m.rebootNeeded = false
	m.rebootMessage = ""
	m.phase = ""
	m.phaseMsg = ""
	m.board = ""
	m.chipName = ""
	m.installLog = nil
	m.result = nil
	m.fans = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx)
	return nil
}

// setPhase updates the current phase and its human-readable description.
func (m *Manager) setPhase(phase, msg string) {
	m.mu.Lock()
	m.phase = phase
	m.phaseMsg = msg
	m.mu.Unlock()
	m.logger.Info("setup: " + msg)
}

// appendInstallLog adds a line to the driver installation log.
func (m *Manager) appendInstallLog(line string) {
	m.mu.Lock()
	m.installLog = append(m.installLog, line)
	m.mu.Unlock()
}

// afterDriverInstall waits for hwmon class registration to complete after a
// successful modprobe, then re-runs the daemon-level probe so the persisted
// wizard.initial_outcome reflects the post-install kernel state (#766).
//
// Without this, a fresh install whose driver populates pwm channels mid-
// wizard leaves the persisted outcome at "monitor_only" until the next
// daemon restart — so a daemon that re-reads its KV would still think the
// system is monitor-only even though /sys/class/hwmon now shows writable
// PWMs. The wizard's Phase 4 sysfs walk is unaffected by this; the
// re-probe is purely about keeping the persisted KV outcome in sync.
//
// reason is a short tag identifying the trigger ("InstallDriver:nct6687"
// or "LoadModule:nct6683") logged alongside the re-probe error, if any.
func (m *Manager) afterDriverInstall(ctx context.Context, reason string) {
	m.mu.Lock()
	settle := m.settleAfterModprobe
	rp := m.reprobeFn
	m.mu.Unlock()

	if settle > 0 {
		select {
		case <-time.After(settle):
		case <-ctx.Done():
			return
		}
	}
	if rp == nil {
		return
	}
	if err := rp(ctx); err != nil {
		m.logger.Warn("setup: post-install reprobe failed; persisted wizard outcome may be stale until next daemon restart",
			"trigger", reason, "err", err)
	}
}

// RunBlocking runs setup synchronously (for the CLI --setup flag).
func (m *Manager) RunBlocking() error {
	if err := m.Start(); err != nil {
		return err
	}
	// Wait for the goroutine to finish by polling done.
	for {
		time.Sleep(200 * time.Millisecond)
		m.mu.Lock()
		done := m.done
		errMsg := m.errMsg
		m.mu.Unlock()
		if done {
			if errMsg != "" {
				return fmt.Errorf("%s", errMsg)
			}
			return nil
		}
	}
}

// Progress returns a snapshot of the current wizard state.
// It merges live calibration progress from the calibrate.Manager.
func (m *Manager) Progress() Progress {
	// Resolve persistent applied state outside the manager lock so the
	// stat() call doesn't serialise against everything else on the lock.
	applied := m.IsApplied()

	m.mu.Lock()
	fans := make([]FanState, len(m.fans))
	copy(fans, m.fans)
	installLog := make([]string, len(m.installLog))
	copy(installLog, m.installLog)
	p := Progress{
		Needed:        !applied,
		Running:       m.running,
		Done:          m.done,
		Applied:       applied,
		Error:         m.errMsg,
		RebootNeeded:  m.rebootNeeded,
		RebootMessage: m.rebootMessage,
		Phase:         m.phase,
		PhaseMsg:      m.phaseMsg,
		Board:         m.board,
		ChipName:      m.chipName,
		InstallLog:    installLog,
		Fans:          fans,
		Config:        m.result,
		Profile:       m.profile,
	}

	// v0.5.9 wizard recovery classification (#800). When an error is
	// present, hand it to the recovery package along with the
	// wizard's current phase + the install log captured so far +
	// recent kernel ring-buffer lines. The kernel lines are required
	// for ClassACPIResourceConflict (#817) — modprobe's stdout only
	// shows ENODEV, the actual ACPI conflict stamp lives in the
	// kernel journal. The classifier returns ClassUnknown when no
	// rule matches, which the UI handles by showing only the
	// generic diag-bundle card.
	if m.errMsg != "" {
		// Prefer the directly-set failureClass (set by paths
		// that classify the failure root-cause without needing
		// text matching — preflight Reason → class bridge,
		// modprobe-failure detection, etc.). Fall back to
		// regex-matching the operator-facing error string +
		// installLog + kernel journal when no class was set
		// directly. Without this preference, every preflight-
		// classified failure silently fell through to
		// ClassUnknown because no Classify() rule matched the
		// detail-string verbatim — caught on Phoenix's HIL.
		var class recovery.FailureClass
		if m.failureClass != "" {
			class = m.failureClass
		} else {
			journal := installLog
			if klog := readKernelJournal(200); len(klog) > 0 {
				journal = append(append([]string{}, installLog...), klog...)
			}
			class = recovery.Classify(m.phase, errors.New(m.errMsg), journal)
		}
		p.FailureClass = string(class)
		p.Remediation = recovery.RemediationFor(class)
	}
	m.mu.Unlock()

	// Merge live calibration progress.
	calStatus := m.cal.AllStatus()
	calByPath := make(map[string]calibrate.Status, len(calStatus))
	for _, cs := range calStatus {
		calByPath[cs.PWMPath] = cs
	}
	for i, f := range p.Fans {
		if f.CalPhase == "calibrating" {
			if cs, ok := calByPath[f.PWMPath]; ok && cs.Running {
				p.Fans[i].CalProgress = cs.Progress
			}
		}
	}

	return p
}

// ProgressNeeded returns a Progress with Needed computed correctly against
// a real config. Use this instead of Progress() when the caller has access
// to the live config.
func (m *Manager) ProgressNeeded(liveCfg *config.Config) Progress {
	p := m.Progress()
	p.Needed = !m.IsApplied() && Needed(liveCfg)
	return p
}

// IsApplied reports whether the wizard has been completed for this host.
// True when MarkApplied has fired in the current process, OR when the
// persistent applied-marker file exists from a prior daemon run. The
// marker is what lets a no-fans / monitor-only host stay out of the
// wizard across restarts even though Needed(cfg) keeps returning true
// (the persisted config has no Controls).
func (m *Manager) IsApplied() bool {
	m.mu.Lock()
	inMem := m.applied
	path := m.appliedMarkerPath
	m.mu.Unlock()
	if inMem {
		return true
	}
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

// GeneratedConfig returns the generated config after a successful run,
// or nil if the run hasn't completed or failed.
func (m *Manager) GeneratedConfig() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result
}

// MarkApplied records that the generated config has been written to disk.
// After this, Progress.Needed will be false. Also creates a persistent
// marker file so the wizard stays dismissed across daemon restarts even
// for hosts whose post-setup config has no Controls (the monitor-only
// "no controllable fans" path). Marker write is best-effort — a write
// failure leaves the in-memory flag set so the current session is fine,
// and the operator will only see the wizard on the next daemon restart.
func (m *Manager) MarkApplied() {
	m.mu.Lock()
	m.applied = true
	path := m.appliedMarkerPath
	m.mu.Unlock()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
		if f, err := os.Create(path); err == nil {
			_ = f.Close()
		}
	}
}

// ClearApplied removes both the in-memory flag and the persistent marker.
// Used by the setup-reset path when an operator wants to wipe their setup
// and start over. Returns the marker-removal error so callers can log a
// resilient state if they care; missing marker is reported as nil.
func (m *Manager) ClearApplied() error {
	m.mu.Lock()
	m.applied = false
	path := m.appliedMarkerPath
	m.mu.Unlock()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Abort cancels an in-flight setup wizard run (including the parallel
// per-fan calibration sweeps). Idempotent: safe to call when no run is
// active or to call repeatedly. The setup goroutine and each calibration
// goroutine handle their own watchdog/PWM restore via deferred cleanup;
// this method only fires the context.
func (m *Manager) Abort() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// run is the setup goroutine. It detects hardware, installs any missing
// drivers, discovers fans, detects RPM sensors, and calibrates all fans.
func (m *Manager) run(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.running = false
		m.done = true
		c := m.cancel
		m.mu.Unlock()
		// Release the cancel func so the context is GC'd. Idempotent — Abort
		// may have already fired it.
		if c != nil {
			c()
		}
	}()

	// ── Phase 0: vendor-daemon deferral check ───────────────────────────────
	//
	// R28 Agent G's #1 architectural finding: Linux-first OEM laptops
	// (System76, ASUS ROG, Tuxedo, Slimbook) ship working vendor fan
	// daemons. ventd should defer rather than fight for control. Probe
	// before any hardware enumeration so the wizard short-circuits into
	// a friendly recovery card instead of running calibration that the
	// vendor daemon will then override.
	//
	// The probe respects ctx so an Abort during the systemctl checks
	// returns cleanly. VendorDaemonNone (the default + ctx-cancel
	// path) means proceed with the normal install flow.
	vendorProbe := m.vendorDaemonProbe
	if vendorProbe == nil {
		vendorProbe = func(ctx context.Context) recovery.VendorDaemon {
			return recovery.DetectVendorDaemon(ctx, nil)
		}
	}
	if v := vendorProbe(ctx); v != recovery.VendorDaemonNone {
		m.mu.Lock()
		m.errMsg = "Detected an active vendor fan daemon (" + string(v) + "). " +
			"That daemon already controls your fans correctly on this laptop. " +
			"Switch ventd to monitor-only mode rather than fight it for control."
		m.failureClass = recovery.ClassVendorDaemonActive
		m.mu.Unlock()
		return
	}

	// ── Phase 1: hardware detection ─────────────────────────────────────────
	m.setPhase("detecting", "Scanning your system for hardware...")

	diag := hwmonpkg.Diagnose()
	board := strings.TrimSpace(diag.BoardVendor + " " + diag.BoardName)
	m.mu.Lock()
	m.board = board
	m.mu.Unlock()

	if board != "" {
		m.setPhase("detecting", "Board detected: "+board)
	}

	// ── Phase 2: automatic driver installation ──────────────────────────────
	if diag.PWMCount == 0 && len(diag.DriverNeeds) > 0 {
		nd := diag.DriverNeeds[0]
		m.mu.Lock()
		m.chipName = nd.ChipName
		m.mu.Unlock()
		m.setPhase("installing_driver",
			"Installing "+nd.ChipName+" fan controller driver — this may take a minute...")

		// Preflight the OOT fallback chain. Each blocker produces a distinct
		// hwdiag entry with a one-click remediation, and aborts install so
		// we don't waste time on a build we know will fail.
		if pre := hwmonpkg.PreflightOOT(nd, hwmonpkg.DefaultProbes()); pre.Reason != hwmonpkg.ReasonOK {
			m.emitPreflightDiag(nd, pre)
			m.mu.Lock()
			m.errMsg = pre.Detail
			m.failureClass = preflightReasonToFailureClass(pre.Reason)
			m.mu.Unlock()
			return
		}

		err := hwmonpkg.InstallDriver(nd.Key, m.appendInstallLog, m.logger)
		if err != nil {
			m.mu.Lock()
			var rebootErr *hwmonpkg.ErrRebootRequired
			if errors.As(err, &rebootErr) {
				m.rebootNeeded = true
				m.rebootMessage = rebootErr.Message
			} else {
				m.errMsg = "Could not install the fan controller driver: " + err.Error()
				m.mu.Unlock()
				m.emitBuildFailedDiag(nd, err)
				m.mu.Lock()
			}
			m.mu.Unlock()
			return
		}
		m.setPhase("installing_driver", "Driver installed. Detecting fans...")
		m.afterDriverInstall(ctx, "InstallDriver:"+nd.ChipName)
	}

	// ── Phase 3: NVML init ──────────────────────────────────────────────────
	// Refcount-safe: a matching Shutdown decrements; real release only
	// happens on the final call (e.g. when main() shuts down).
	nvmlOK := false
	if err := nvidia.Init(m.logger); err == nil {
		defer nvidia.Shutdown()
		nvmlOK = true
	}

	// ── Phase 4: discover all PWM channels ─────────────────────────────────
	m.setPhase("scanning_fans", "Detecting fan controllers...")
	var initial []FanState

	hwmonCtrls := m.discoverHwmonControls()
	for _, ctrl := range hwmonCtrls {
		initial = append(initial, FanState{
			Name:        hwmonFanName(ctrl.path),
			Type:        "hwmon",
			PWMPath:     ctrl.path,
			ControlKind: ctrl.kind,
			DetectPhase: "pending",
			CalPhase:    "pending",
		})
	}

	// Tier 3 — DMI-triggered candidate modules. Only fires when the
	// capability-first pass produced no controllable hwmon fan. Emits
	// diagnostics only; never auto-modprobes.
	if len(hwmonCtrls) == 0 {
		m.emitDMICandidates()
	}

	if nvmlOK {
		gpuCount := nvidia.CountGPUs()
		for i := 0; i < gpuCount; i++ {
			if nvidia.HasFans(uint(i)) {
				initial = append(initial, FanState{
					Name:        fmt.Sprintf("gpu%d", i),
					Type:        "nvidia",
					PWMPath:     fmt.Sprintf("%d", i),
					DetectPhase: "found",
					CalPhase:    "pending",
				})
			}
		}
	}

	m.syncFans(initial)

	if len(initial) == 0 {
		m.mu.Lock()
		m.errMsg = "No fan controllers were found on this system. " +
			"Your motherboard may use a chip that requires manual configuration."
		m.mu.Unlock()
		return
	}

	fans := make([]FanState, len(initial))
	copy(fans, initial)

	// --- Phase 5: RPM sensor detection ---
	// DetectRPMSensor ramps one PWM channel and watches ALL fan*_input files
	// on that chip. Two channels on the same chip must be serialised (their
	// fan*_input files overlap). Channels on different chips are independent
	// and can be detected in parallel.
	//
	// Build a map: chip dir → indices of hwmon fans on that chip.
	chipFans := make(map[string][]int)
	for i, f := range fans {
		if f.Type != "hwmon" {
			continue
		}
		// RPM-target fans (fan*_target) use a different write path and cannot
		// be ramped with WritePWM — skip them during RPM sensor detection.
		if f.ControlKind == "rpm_target" {
			fans[i].DetectPhase = "n/a"
			continue
		}
		dir := filepath.Dir(f.PWMPath)
		chipFans[dir] = append(chipFans[dir], i)
	}

	// Sort chip dirs so goroutine launch order is deterministic across runs.
	chipDirs := make([]string, 0, len(chipFans))
	for dir := range chipFans {
		chipDirs = append(chipDirs, dir)
	}
	sortPathsNumerically(chipDirs)

	m.setPhase("detecting_rpm", fmt.Sprintf("Detecting RPM sensors for %d fan channel(s)...", len(initial)))

	var detWg sync.WaitGroup
	for _, dir := range chipDirs {
		indices := chipFans[dir]
		detWg.Add(1)
		go func(idxs []int) {
			defer detWg.Done()

			// Freeze all fans on this chip at their current PWM before detecting.
			// This prevents other BIOS-controlled fans from fluctuating and causing
			// false correlations when we ramp the target channel.
			for _, i := range idxs {
				if orig, err := hwmonpkg.ReadPWM(fans[i].PWMPath); err == nil {
					_ = hwmonpkg.WritePWMEnable(fans[i].PWMPath, 1)
					_ = hwmonpkg.WritePWM(fans[i].PWMPath, orig)
				}
			}
			// Leave channels in manual mode (pwm_enable=1) after detection so
			// the calibration stall probe can write pwm=0 without EBUSY.
			// On some chips (e.g. it8772) writing 0 in auto mode (pwm_enable=2)
			// returns EBUSY. The backend's acquired map already marks these
			// channels as manually acquired from the DetectRPMSensor sweep; if
			// we restore to auto mode here, ensureManualMode short-circuits on
			// the first calibration write and the chip refuses pwm=0. The
			// watchdog handles emergency restore if the wizard is aborted.

			// Within this chip, detect serially.
			for _, i := range idxs {
				m.mu.Lock()
				fans[i].DetectPhase = "detecting"
				snapshot := make([]FanState, len(fans))
				copy(snapshot, fans)
				m.fans = snapshot
				m.mu.Unlock()

				cfgFan := &config.Fan{
					Name:    fans[i].Name,
					Type:    fans[i].Type,
					PWMPath: fans[i].PWMPath,
					MinPWM:  0,
					MaxPWM:  255,
				}
				det, err := m.cal.DetectRPMSensor(cfgFan)

				m.mu.Lock()
				if err != nil || det.RPMPath == "" {
					if err != nil {
						m.logger.Warn("setup: rpm detect failed", "fan", fans[i].Name, "err", err)
					}
					if det.Delta > 0 {
						// Fan moved (RPM delta > 0) but below the correlation noise floor.
						// Try heuristic temperature-sensor binding so the fan is still
						// controllable via an open-loop curve.
						chips := loadHwmonChips(m.hwmonRoot)
						if hs := heuristicSensorBinding(chips); hs != nil {
							m.logger.Warn("setup: no RPM sensor correlated; heuristic binding applied",
								"fan", fans[i].Name, "best_delta", det.Delta, "sensor", hs.Path)
							fans[i].DetectPhase = "heuristic"
						} else {
							m.logger.Warn("setup: fan responded but heuristic binding found no sensor",
								"fan", fans[i].Name, "best_delta", det.Delta)
							fans[i].DetectPhase = "none"
						}
					} else {
						fans[i].DetectPhase = "none"
					}
				} else {
					fans[i].RPMPath = det.RPMPath
					fans[i].DetectPhase = "found"
				}
				snapshot = make([]FanState, len(fans))
				copy(snapshot, fans)
				m.fans = snapshot
				m.mu.Unlock()
			}
		}(indices)
	}
	detWg.Wait()

	// --- Phase 5b: polarity probe (spec-v0_5_2) ---
	// Run after RPM sensor detection so TachPath is available. Skipped when
	// no prober is wired (nil = tests / non-control-mode paths).
	m.mu.Lock()
	prober := m.polarityProber
	m.mu.Unlock()
	if prober != nil {
		m.setPhase("probing_polarity",
			fmt.Sprintf("Testing fan response on %d channel(s). This takes about 3 seconds per channel.", len(fans)))
		for i := range fans {
			if fans[i].Type != "hwmon" || fans[i].DetectPhase == "none" || fans[i].DetectPhase == "n/a" {
				fans[i].PolarityPhase = "phantom"
				continue
			}
			fans[i].PolarityPhase = "pending"
		}
		m.syncFans(fans)

		for i := range fans {
			if ctx.Err() != nil {
				break
			}
			if fans[i].PolarityPhase == "phantom" {
				continue
			}
			fans[i].PolarityPhase = "testing"
			m.syncFans(fans)

			ch := &probe.ControllableChannel{
				SourceID: fans[i].Name,
				PWMPath:  fans[i].PWMPath,
				TachPath: fans[i].RPMPath,
				Driver:   "hwmon",
				Polarity: "unknown",
			}
			res, err := prober.ProbeChannel(ctx, ch)
			if err != nil && ctx.Err() != nil {
				break
			}
			if err != nil {
				fans[i].PolarityPhase = "phantom"
			} else {
				fans[i].PolarityPhase = res.Polarity
			}
			m.syncFans(fans)
		}

		// Demote to monitor-only when every controllable channel is phantom
		// (Dell PE 14G firmware-locked, HPE iLO5 profile-only, etc.).
		allPhantom := true
		for _, f := range fans {
			if f.Type == "hwmon" && f.DetectPhase == "found" && f.PolarityPhase != "phantom" {
				allPhantom = false
				break
			}
		}
		if allPhantom {
			m.mu.Lock()
			m.errMsg = "All fan channels are firmware-locked or unresponsive. " +
				"Ventd will run in monitor-only mode — temperatures are visible " +
				"but fan speeds are managed by the system firmware."
			m.mu.Unlock()
			return
		}
	}

	// --- Phase 6: calibrate all fans in parallel ---
	// Each fan now reads from its own RPMPath (or NVML for nvidia), so
	// simultaneous ramps don't interfere with each other's readings.
	m.setPhase("calibrating", "Calibrating fans — finding minimum and maximum speeds...")
	var wg sync.WaitGroup
	for i := range fans {
		if fans[i].DetectPhase == "none" || fans[i].DetectPhase == "heuristic" || fans[i].ControlKind == "rpm_target" ||
			fans[i].PolarityPhase == "phantom" {
			// "heuristic" fans have no reliable RPM sensor so calibration cannot run;
			// phantom channels are firmware-locked or unresponsive.
			// Both are still included in the final config via doneFans below.
			fans[i].CalPhase = "skipped"
		} else {
			fans[i].CalPhase = "calibrating"
		}
	}
	m.syncFans(fans)

	for i := range fans {
		if ctx.Err() != nil {
			break
		}
		if fans[i].CalPhase == "skipped" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f := fans[idx]
			cfgFan := &config.Fan{
				Name:    f.Name,
				Type:    f.Type,
				PWMPath: f.PWMPath,
				RPMPath: f.RPMPath,
				MinPWM:  0,
				MaxPWM:  255,
			}
			result, err := m.cal.RunSync(ctx, cfgFan)

			m.mu.Lock()
			if err != nil {
				fans[idx].CalPhase = "error"
				fans[idx].Error = err.Error()
			} else if result.StartPWM == 0 && result.MaxRPM == 0 {
				fans[idx].CalPhase = "skipped"
			} else {
				fans[idx].CalPhase = "done"
				fans[idx].StartPWM = result.StartPWM
				fans[idx].StopPWM = result.StopPWM
				fans[idx].MaxRPM = result.MaxRPM
				fans[idx].IsPump = result.FanType == "pump"
			}
			snapshot := make([]FanState, len(fans))
			copy(snapshot, fans)
			m.fans = snapshot
			m.mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Signal the UI that calibration is done and we're generating the config.
	// Without this the UI appears frozen while gatherProfile/buildConfig run.
	m.setPhase("finalizing", "Calibration complete — building your configuration...")

	// --- Phase 4: collect successful fans and build config ---
	var doneFans []fanDiscovery
	for _, f := range fans {
		// Include calibrated fans, rpm_target fans, and fans bound by heuristic
		// (responded to PWM but no RPM sensor correlated — controlled open-loop).
		if f.CalPhase != "done" && f.ControlKind != "rpm_target" && f.DetectPhase != "heuristic" {
			continue
		}
		var chipName string
		if f.Type == "hwmon" {
			chipName = readTrimmed(filepath.Join(filepath.Dir(f.PWMPath), "name"))
		}
		doneFans = append(doneFans, fanDiscovery{
			name:        f.Name,
			fanType:     f.Type,
			chipName:    chipName,
			pwmPath:     f.PWMPath,
			rpmPath:     f.RPMPath,
			startPWM:    f.StartPWM,
			stopPWM:     f.StopPWM,
			maxRPM:      f.MaxRPM,
			isPump:      f.IsPump,
			controlKind: f.ControlKind,
		})
	}

	// RULE-SETUP-NO-ORPHANED-CHANNELS: every channel that was probed but did
	// NOT make it into doneFans (detection failed, calibration aborted on
	// sentinel, phantom, etc.) MUST be handed back to BIOS auto-curve before
	// the wizard returns. Without this the channel sits at pwm_enable=1 with
	// the calibration sweep's last-written PWM byte forever — fan stuck off,
	// no diagnostic surface (issue #753). The watchdog only restores on
	// daemon-exit; during normal operation it never fires.
	restoreExcludedChannels(fans, doneFans, m.logger)

	if len(doneFans) == 0 {
		m.mu.Lock()
		m.errMsg = setupFailMessage(fans)
		m.mu.Unlock()
		return
	}

	// Discover CPU/GPU temp sensors for curve generation.
	cpuSensorName, cpuSensorPath := m.discoverCPUTempSensor()
	var cpuSensorHeuristic bool
	var cpuCurrentTemp float64
	if cpuSensorPath != "" {
		cpuCurrentTemp, _ = hwmonpkg.ReadValue(cpuSensorPath)
	} else {
		// The wizard wanted a CPU sensor but /sys/class/hwmon exposed no
		// coretemp / k10temp / acpitz. Surface a remediation diag so the UI
		// can offer a one-click modprobe; doesn't block the wizard.
		m.emitCPUSensorModuleMissingDiag()
		// Last-resort heuristic: try the same binding logic used for uncorrelated
		// fans. This keeps heuristic-only rigs from getting a fixed-speed curve.
		chips := loadHwmonChips(m.hwmonRoot)
		if hs := heuristicSensorBinding(chips); hs != nil {
			cpuSensorPath = hs.Path
			cpuSensorName = "CPU Temperature"
			cpuSensorHeuristic = true
			cpuCurrentTemp, _ = hwmonpkg.ReadValue(cpuSensorPath)
			m.logger.Warn("setup: heuristic CPU temp sensor selected", "path", cpuSensorPath)
		}
	}

	// GPU temp: prefer NVML (NVIDIA), fall back to AMD GPU hwmon sensor.
	hasGPUTemp := false
	var gpuCurrentTemp float64
	var gpuTempPath string // empty = NVML; non-empty = hwmon sysfs path (AMD)

	if nvmlOK && nvidia.CountGPUs() > 0 {
		hasGPUTemp = true
		gpuCurrentTemp, _ = nvidia.ReadTemp(0)
		// gpuTempPath stays empty: sensor is emitted as type "nvidia"
	} else {
		_, amdPath := m.discoverAMDGPUTemp()
		if amdPath != "" {
			hasGPUTemp = true
			gpuTempPath = amdPath
			gpuCurrentTemp, _ = hwmonpkg.ReadValue(amdPath)
		}
	}

	// Build hardware profile: CPU/GPU model, TDP, thermal limits.
	// buildConfig uses the thermal limits to set curve max temps and appends
	// human-readable notes explaining the choices to profile.CurveNotes.
	profile := m.gatherProfile(cpuSensorPath, nvmlOK, gpuTempPath)

	cfg := buildConfig(doneFans, cpuSensorName, cpuSensorPath, cpuCurrentTemp, hasGPUTemp, gpuTempPath, gpuCurrentTemp, profile)

	// Mark the CPU temp sensor as heuristically assigned so users can identify
	// auto-selected bindings on the Curves page.
	if cpuSensorHeuristic {
		for i := range cfg.Sensors {
			if cfg.Sensors[i].Name == "cpu_temp" {
				cfg.Sensors[i].Heuristic = true
				break
			}
		}
	}

	// Pre-validate before exposing to the review screen. Catches any
	// future buildConfig regression that emits a config the Apply path
	// would reject — surfaces it as a wizard error (so the operator
	// sees it immediately) instead of a confusing error banner on the
	// Apply click that leaves no remediation path in the zero-terminal
	// UX.
	if err := validateGeneratedConfig(cfg); err != nil {
		m.logger.Error("setup: generated config failed validation", "err", err)
		m.mu.Lock()
		m.errMsg = "internal error generating configuration: " + err.Error()
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.result = cfg
	m.profile = profile
	m.mu.Unlock()
}

// setupFailMessage returns a human-readable error when no fans could be set up.
// It distinguishes truly absent fans (zero RPM delta) from fans that responded
// to PWM but failed heuristic sensor binding.
func setupFailMessage(fans []FanState) string {
	detected := 0
	for _, f := range fans {
		if f.DetectPhase == "found" || f.DetectPhase == "heuristic" {
			detected++
		}
	}
	if detected == 0 {
		return "setup: no fans detected (all fan headers show 0 RPM delta). " +
			"Verify fan connections and ensure fans are not already stopped by BIOS."
	}
	return fmt.Sprintf(
		"setup: detected %d fan(s) but could not identify a temperature sensor "+
			"(idle CPU / slow thermal response). "+
			"Ventd attempted heuristic binding but found no plausible sensor. "+
			"Check /etc/ventd/config.yaml and verify sensor assignment in the Curves page.",
		detected,
	)
}

// validateGeneratedConfig round-trips cfg through yaml.Marshal + config.Parse
// so any validation rule Apply would enforce (sensor/fan/curve/control
// reference integrity, type constraints, etc.) fails here instead of on the
// Apply click. It then runs config.CheckResolvable against the live hwmon
// root so any chip_name / hwmon_device mismatch that would make the daemon
// fatal on next boot surfaces as a wizard error now — closes the
// "wizard writes a config the daemon refuses to load" class of bug
// (see usability.md: "Never emit a config the resolver will reject on
// the next boot"). A passing result is not a guarantee the config is
// optimal for the hardware — only that it is internally consistent,
// resolvable against current sysfs, and will survive the Save path.
func validateGeneratedConfig(cfg *config.Config) error {
	buf, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal generated config: %w", err)
	}
	parsed, err := config.Parse(buf)
	if err != nil {
		return err
	}
	if err := config.CheckResolvable(parsed); err != nil {
		return fmt.Errorf("resolver would reject this config on daemon start: %w", err)
	}
	return nil
}

// syncFans atomically replaces m.fans with a full copy of fans.
func (m *Manager) syncFans(fans []FanState) {
	snapshot := make([]FanState, len(fans))
	copy(snapshot, fans)
	m.mu.Lock()
	m.fans = snapshot
	m.mu.Unlock()
}

// fanDiscovery is an internal struct for passing calibrated fan data to buildConfig.
type fanDiscovery struct {
	name        string
	fanType     string // "hwmon" or "nvidia"
	chipName    string // hwmon chip name (e.g. "amdgpu", "nct6687"); empty for nvidia
	pwmPath     string
	rpmPath     string
	startPWM    uint8
	stopPWM     uint8 // hysteresis: lowest PWM that keeps a spinning fan spinning
	maxRPM      int
	isPump      bool
	controlKind string // "rpm_target" for fan*_target channels; "" means pwm
}

// restoreExcludedChannels enforces RULE-SETUP-NO-ORPHANED-CHANNELS.
//
// Every channel in `fans` whose PWMPath does not appear in `doneFans`
// (i.e. detection or calibration could not classify it as a controlled
// fan) is left in pwm_enable=1 (manual mode) by the calibration sweep
// per the deliberate "leave manual to avoid EBUSY on pwm=0" pattern
// at line ~597 above. That pattern is correct DURING the sweep but
// must be undone for excluded channels before the wizard returns —
// otherwise the channel sits at the sweep's last-written PWM byte
// forever (issue #753). The daemon's watchdog only restores on exit;
// during normal operation, no restore ever fires.
//
// We write pwm_enable=2 (BIOS auto) for every excluded channel that
// has a hwmon-style path. NVML/IPMI paths are skipped (their restore
// surfaces live in their respective backends). Errors are logged at
// WARN level and never fatal — the wizard's success path is more
// important than perfect cleanup of every excluded sysfs node.
func restoreExcludedChannels(fans []FanState, doneFans []fanDiscovery, logger *slog.Logger) {
	included := make(map[string]struct{}, len(doneFans))
	for _, df := range doneFans {
		if df.pwmPath != "" {
			included[df.pwmPath] = struct{}{}
		}
	}
	for _, f := range fans {
		if f.Type != "hwmon" {
			continue // NVML / IPMI: handled by their own backends
		}
		if f.PWMPath == "" {
			continue
		}
		if _, ok := included[f.PWMPath]; ok {
			continue
		}
		if err := hwmonpkg.WritePWMEnable(f.PWMPath, 2); err != nil {
			// Some chips (e.g. nct6683 for NCT6687D) have no pwm_enable;
			// fall through silently — the channel never had manual mode
			// in the first place.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			logger.Warn("setup: restore excluded channel to BIOS auto failed",
				"pwm_path", f.PWMPath, "fan", f.Name, "err", err)
			continue
		}
		logger.Info("setup: excluded channel handed back to BIOS auto-curve",
			"pwm_path", f.PWMPath, "fan", f.Name,
			"reason", excludedReason(f))
	}
}

// excludedReason classifies why a fan was excluded from doneFans, for
// logging + downstream diagnostic surfaces (#757). The classification
// is best-effort and not load-bearing for control behaviour.
//
// Uses the DetectPhase / CalPhase values set by the wizard's detection
// + calibration loops — see lines ~640 / ~767 / ~770 of this file for
// the assignment sites.
func excludedReason(f FanState) string {
	switch {
	case f.DetectPhase == "none":
		return "detect_failed"
	case f.CalPhase == "error":
		return "calibration_aborted"
	case f.CalPhase == "skipped":
		return "phantom"
	default:
		return "unclassified"
	}
}

// buildConfig constructs a Config from calibrated fan data and discovered sensors.
// gpuTempPath is empty when the GPU sensor is NVML-based (nvidia type); non-empty
// when it is an AMD hwmon sysfs path.
// profile (may be nil) supplies hardware thermal limits for curve tuning; curve
// design decisions are appended to profile.CurveNotes.
func buildConfig(
	fans []fanDiscovery,
	cpuSensorName, cpuSensorPath string,
	cpuCurrentTemp float64,
	hasGPUTemp bool,
	gpuTempPath string,
	gpuCurrentTemp float64,
	profile *HWProfile,
) *config.Config {
	cfg := &config.Config{
		Version:      config.CurrentVersion,
		PollInterval: config.Duration{Duration: 2 * time.Second},
		Web:          config.Web{Listen: "0.0.0.0:9999"},
	}

	var hwmonFans, gpuFans []fanDiscovery
	for _, f := range fans {
		if f.fanType == "nvidia" || f.chipName == "amdgpu" {
			gpuFans = append(gpuFans, f)
		} else {
			hwmonFans = append(hwmonFans, f)
		}
	}

	// Sensors.
	hasCPUSensor := cpuSensorPath != ""
	if hasCPUSensor {
		cfg.Sensors = append(cfg.Sensors, config.Sensor{
			Name:        "cpu_temp",
			Type:        "hwmon",
			Path:        cpuSensorPath,
			HwmonDevice: hwmonpkg.StableDevice(cpuSensorPath),
			ChipName:    chipNameOf(cpuSensorPath),
		})
	}
	if hasGPUTemp {
		if gpuTempPath == "" {
			// NVML (NVIDIA)
			cfg.Sensors = append(cfg.Sensors, config.Sensor{
				Name:   "gpu_temp",
				Type:   "nvidia",
				Path:   "0",
				Metric: "temp",
			})
		} else {
			// AMD GPU hwmon
			cfg.Sensors = append(cfg.Sensors, config.Sensor{
				Name:        "gpu_temp",
				Type:        "hwmon",
				Path:        gpuTempPath,
				HwmonDevice: hwmonpkg.StableDevice(gpuTempPath),
				ChipName:    chipNameOf(gpuTempPath),
			})
		}
	}

	// Compute curve max temps from hardware thermal limits when available.
	// CPU: TjMax/Tcrit − 15°C safety margin (clamped [75, 95]).
	// GPU NVIDIA: slowdown threshold − 5°C (NVML already reports throttle onset).
	// GPU AMD: junction crit − 15°C.
	cpuMaxTemp := 85.0
	if profile != nil && profile.CPUThermalC > 0 {
		cpuMaxTemp = clampTemp(profile.CPUThermalC-15, 75, 95)
		profile.CurveNotes = append(profile.CurveNotes,
			fmt.Sprintf("cpu_curve max: %.0f°C  (TjMax %.0f°C − 15°C margin)", cpuMaxTemp, profile.CPUThermalC))
	} else {
		profile.CurveNotes = append(profile.CurveNotes,
			"cpu_curve max: 85°C  (default — CPU thermal limit not detected)")
	}

	gpuMaxTemp := 85.0
	if profile != nil && profile.GPUThermalC > 0 {
		margin := 15.0
		basis := "crit"
		if gpuTempPath == "" { // NVML — slowdown is already the throttle point
			margin = 5.0
			basis = "slowdown"
		}
		gpuMaxTemp = clampTemp(profile.GPUThermalC-margin, 75, 95)
		profile.CurveNotes = append(profile.CurveNotes,
			fmt.Sprintf("gpu_curve max: %.0f°C  (%s %.0f°C − %.0f°C margin)", gpuMaxTemp, basis, profile.GPUThermalC, margin))
	} else if hasGPUTemp || len(gpuFans) > 0 {
		profile.CurveNotes = append(profile.CurveNotes,
			"gpu_curve max: 85°C  (default — GPU thermal limit not detected)")
	}

	// Curves.
	// Use fixed temperature floors rather than current-temp + offset so the
	// generated config behaves identically regardless of system load at setup time.
	const cpuMinTemp = 40.0 // fans silent below 40°C
	const gpuMinTemp = 50.0 // GPU fans silent below 50°C
	if hasCPUSensor && len(hwmonFans) > 0 {
		minPWM := minStartPWM(hwmonFans)
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:    "cpu_curve",
			Type:    "linear",
			Sensor:  "cpu_temp",
			MinTemp: cpuMinTemp,
			MaxTemp: cpuMaxTemp,
			MinPWM:  minPWM,
			MaxPWM:  255,
		})
	} else if len(hwmonFans) > 0 {
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:  "cpu_curve",
			Type:  "fixed",
			Value: 153, // ~60%
		})
	}

	if hasGPUTemp && len(gpuFans) > 0 {
		minPWM := minStartPWM(gpuFans)
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:    "gpu_curve",
			Type:    "linear",
			Sensor:  "gpu_temp",
			MinTemp: gpuMinTemp,
			MaxTemp: gpuMaxTemp,
			MinPWM:  minPWM,
			MaxPWM:  255,
		})
	} else if len(gpuFans) > 0 {
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:  "gpu_curve",
			Type:  "fixed",
			Value: 153,
		})
	}

	// case_curve: max(cpu_curve, gpu_curve) for fans that should respond to
	// both CPU and GPU temperatures. Only created when both referenced curves
	// will actually be emitted — cpu_curve requires hasCPUSensor+hwmonFans,
	// gpu_curve requires len(gpuFans) > 0. Gating on len(gpuFans) > 0 keeps
	// case_curve from referencing a gpu_curve that was never created on
	// NVML-permission-fail rigs where the GPU sensor is detected but no GPU
	// fans are controllable (observed on phoenix-MS-7D25, 2026-04-15: Apply
	// rejected with `source "gpu_curve" is not defined`).
	hasCaseCurve := hasCPUSensor && hasGPUTemp && len(hwmonFans) > 0 && len(gpuFans) > 0
	if hasCaseCurve {
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:     "case_curve",
			Type:     "mix",
			Function: "max",
			Sources:  []string{"cpu_curve", "gpu_curve"},
		})
	}

	// Emit a pump_curve if any hwmon fan was identified as a pump.
	hasPumpFan := false
	for _, f := range hwmonFans {
		if f.isPump {
			hasPumpFan = true
			break
		}
	}
	if hasPumpFan {
		cfg.Curves = append(cfg.Curves, config.CurveConfig{
			Name:  "pump_curve",
			Type:  "fixed",
			Value: 204, // ~80% PWM — pumps run best at constant high speed
		})
	}

	// Fans and Controls.
	for _, f := range hwmonFans {
		// Use stop_pwm as the MinPWM floor for normal operation (fan stays spinning
		// above this threshold). Fall back to start_pwm, then a safe default.
		minPWM := f.stopPWM
		if minPWM == 0 {
			minPWM = f.startPWM
		}
		if minPWM == 0 {
			minPWM = 20
		}

		fanEntry := config.Fan{
			Name:        f.name,
			Type:        "hwmon",
			PWMPath:     f.pwmPath,
			RPMPath:     f.rpmPath,
			HwmonDevice: hwmonpkg.StableDevice(f.pwmPath),
			ChipName:    f.chipName,
			ControlKind: f.controlKind,
			MinPWM:      minPWM,
			MaxPWM:      255,
		}

		curveName := "cpu_curve"
		if f.isPump {
			pumpMin := uint8(config.MinPumpPWM)
			if minPWM > pumpMin {
				pumpMin = minPWM
			}
			fanEntry.IsPump = true
			fanEntry.PumpMinimum = pumpMin
			fanEntry.MinPWM = pumpMin
			curveName = "pump_curve"
		} else if hasCaseCurve && isCaseFan(f.name) {
			curveName = "case_curve"
		}

		cfg.Fans = append(cfg.Fans, fanEntry)
		cfg.Controls = append(cfg.Controls, config.Control{
			Fan:   f.name,
			Curve: curveName,
		})
	}
	for _, f := range gpuFans {
		minPWM := f.stopPWM
		if minPWM == 0 {
			minPWM = f.startPWM
		}
		if minPWM == 0 {
			minPWM = 20
		}
		fanEntry := config.Fan{
			Name:        f.name,
			Type:        f.fanType,
			PWMPath:     f.pwmPath,
			ControlKind: f.controlKind,
			MinPWM:      minPWM,
			MaxPWM:      255,
		}
		if f.fanType == "hwmon" {
			// AMD GPU: include RPM path, stable device, control kind, chip name.
			fanEntry.RPMPath = f.rpmPath
			fanEntry.HwmonDevice = hwmonpkg.StableDevice(f.pwmPath)
			fanEntry.ChipName = f.chipName
		}
		cfg.Fans = append(cfg.Fans, fanEntry)
		cfg.Controls = append(cfg.Controls, config.Control{
			Fan:   f.name,
			Curve: "gpu_curve",
		})
	}

	return cfg
}

// isCaseFan returns true for fans whose names suggest they are chassis/system
// fans (SYS_FAN, CHA_FAN, CASE_FAN, etc.) rather than CPU or GPU fans.
func isCaseFan(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"sys", "cha", "case"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// testPWMWritable verifies that a pwmN sysfs path is actually writable by
// attempting to set manual mode (or writing the current value directly for
// drivers that lack pwm_enable). Restores the original mode on return.
// This is needed for AMD GPU hwmon entries where some driver versions expose
// pwmN files that are read-only or not controllable.
func testPWMWritable(pwmPath string) bool {
	current, err := hwmonpkg.ReadPWM(pwmPath)
	if err != nil {
		return false
	}
	origEnable, enableErr := hwmonpkg.ReadPWMEnable(pwmPath)
	if enableErr == nil {
		// Driver has pwm_enable — try taking manual control.
		if err := hwmonpkg.WritePWMEnable(pwmPath, 1); err != nil {
			return false
		}
		// Restore original enable mode.
		_ = hwmonpkg.WritePWMEnable(pwmPath, origEnable)
	} else {
		// No pwm_enable — try writing the current value directly.
		if err := hwmonpkg.WritePWM(pwmPath, current); err != nil {
			return false
		}
	}
	return true
}

// discoveredControl describes one writable fan control channel found during setup.
type discoveredControl struct {
	path string
	kind string // "" or "pwm" → standard pwm* duty-cycle; "rpm_target" → fan*_target RPM setpoint
}

// discoverHwmonControls returns all writable hwmon fan control channels, sorted
// numerically so hwmon2/pwm1 always comes before hwmon10/pwm2.
//
// Discovery is capability-first: hwmonpkg.EnumerateDevices classifies every
// hwmonN entry under m.hwmonRoot by what files it exposes, independent of chip
// name. This pass then promotes every Primary/OpenLoop PWM candidate that
// survives a writability probe, and every fanN_target channel whose companion
// pwmN is not already a writable candidate.
//
// Two control types are discovered:
//   - pwm[N]      — standard PWM duty-cycle (0–255), the common case
//   - fan[N]_target — RPM setpoint (pre-RDNA AMD amdgpu cards); only included
//     when no writable pwm[N] covers the same channel index
func (m *Manager) discoverHwmonControls() []discoveredControl {
	var ctrls []discoveredControl
	for _, dev := range hwmonpkg.EnumerateDevices(m.hwmonRoot) {
		// NVIDIA devices are routed through NVML; ClassNoFans has nothing to
		// contribute. ClassReadOnly is reported to diagnostics elsewhere but
		// cannot be driven, so skip it for control discovery.
		switch dev.Class {
		case hwmonpkg.ClassSkipNVIDIA, hwmonpkg.ClassNoFans, hwmonpkg.ClassReadOnly:
			continue
		}

		writablePWMIdx := make(map[string]bool)
		for _, ch := range dev.PWM {
			if ch.EnablePath == "" {
				continue
			}
			if !testPWMWritable(ch.Path) {
				continue
			}
			ctrls = append(ctrls, discoveredControl{path: ch.Path, kind: "pwm"})
			writablePWMIdx[ch.Index] = true
		}

		for _, t := range dev.RPMTargets {
			if writablePWMIdx[t.Index] {
				continue
			}
			if testFanTargetWritable(t.Path) {
				ctrls = append(ctrls, discoveredControl{path: t.Path, kind: "rpm_target"})
			}
		}
	}

	paths := make([]string, len(ctrls))
	for i, c := range ctrls {
		paths[i] = c.path
	}
	sortPathsNumerically(paths)

	sorted := make([]discoveredControl, len(ctrls))
	idx := make(map[string]discoveredControl, len(ctrls))
	for _, c := range ctrls {
		idx[c.path] = c
	}
	for i, p := range paths {
		sorted[i] = idx[p]
	}
	return sorted
}

// testFanTargetWritable verifies that a fan*_target sysfs path is writable
// by reading the current value and writing it back unchanged.
func testFanTargetWritable(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	current, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	d := strconv.AppendInt(nil, int64(current), 10)
	d = append(d, '\n')
	return os.WriteFile(path, d, 0) == nil
}

var numRe = regexp.MustCompile(`\d+`)

// sortPathsNumerically sorts sysfs paths by their embedded integers, so
// hwmon2/pwm1 < hwmon2/pwm2 < hwmon10/pwm1 instead of lexicographic order.
func sortPathsNumerically(paths []string) {
	key := func(p string) []int {
		var nums []int
		for _, m := range numRe.FindAllString(p, -1) {
			n, _ := strconv.Atoi(m)
			nums = append(nums, n)
		}
		return nums
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := key(paths[i]), key(paths[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

// hwmonFanName derives a friendly fan name from a pwm* or fan*_target sysfs path.
// It first tries the fan*_label sysfs file; if absent it falls back to
// "Fan N" or "<CHIP> Fan N" to avoid collisions across multiple chips.
func hwmonFanName(controlPath string) string {
	dir := filepath.Dir(controlPath)
	base := filepath.Base(controlPath)

	// Extract the channel number from either "pwmN" or "fanN_target".
	var channel string
	if strings.HasPrefix(base, "pwm") {
		channel = strings.TrimPrefix(base, "pwm")
	} else if strings.HasPrefix(base, "fan") && strings.HasSuffix(base, "_target") {
		channel = strings.TrimSuffix(strings.TrimPrefix(base, "fan"), "_target")
	}

	// Prefer the driver-provided fan label (e.g. "SYS FAN1", "CPU FAN").
	if channel != "" {
		if label := readTrimmed(filepath.Join(dir, "fan"+channel+"_label")); label != "" {
			return titleCaseWords(label)
		}
	}

	// Fall back to a short chip-prefixed name to avoid collisions.
	chip := strings.ToUpper(readTrimmed(filepath.Join(dir, "name")))
	if chip == "" {
		chip = strings.ToUpper(filepath.Base(dir))
	}
	if channel != "" {
		return chip + " Fan " + channel
	}
	return chip + " Fan"
}

// titleCaseWords title-cases each space-separated word without external deps.
func titleCaseWords(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

// discoverCPUTempSensor looks for a CPU temperature sensor.
//
// Pass 1: well-known CPU-native chip names — these always represent CPU temps.
// Pass 2: any non-GPU hwmon device with a label indicating CPU/package temp.
// Pass 3: ACPI thermal zone (acpitz) as a last resort — present on virtually
//
//	all x86 systems, though less accurate than coretemp/k10temp.
//
// Reads from m.hwmonRoot so tests can point at a fixture tree.
func (m *Manager) discoverCPUTempSensor() (name, path string) {
	entries, _ := os.ReadDir(m.hwmonRoot)

	// cpuChips are chip driver names that exclusively report CPU temperatures.
	// k8temp: older AMD K8/K10 (pre-Zen); zenpower/zenpower2: out-of-tree AMD Zen;
	// cpu_thermal: ARM SoC thermal driver; aml_thermal/mtk_thermal: ARM vendor thermals.
	cpuChips := map[string]bool{
		"coretemp":    true,
		"k10temp":     true,
		"k8temp":      true,
		"zenpower":    true,
		"zenpower2":   true,
		"cpu_thermal": true,
		"aml_thermal": true,
		"mtk_thermal": true,
	}

	// Pass 1: known CPU chip names — prefer package/die temp labels.
	for _, e := range entries {
		dir := filepath.Join(m.hwmonRoot, e.Name())
		chip := readTrimmed(filepath.Join(dir, "name"))
		if !cpuChips[chip] {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		for _, p := range matches {
			base := strings.TrimSuffix(filepath.Base(p), "_input")
			label := strings.ToLower(readTrimmed(filepath.Join(dir, base+"_label")))
			if strings.Contains(label, "package") ||
				strings.Contains(label, "tdie") ||
				strings.Contains(label, "tccd") {
				return chip + " Package", p
			}
		}
		if len(matches) > 0 {
			return chip, matches[0]
		}
	}

	// Pass 2: generic fallback — labeled CPU/package temp on any non-GPU chip.
	// acpitz is excluded here because it has no useful per-sensor labels;
	// it is tried as a last resort in pass 3.
	skipChips := map[string]bool{"amdgpu": true, "nvidia": true, "nouveau": true, "radeon": true, "acpitz": true}
	for _, e := range entries {
		dir := filepath.Join(m.hwmonRoot, e.Name())
		chip := readTrimmed(filepath.Join(dir, "name"))
		if skipChips[chip] {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(matches)
		for _, p := range matches {
			base := strings.TrimSuffix(filepath.Base(p), "_input")
			label := strings.ToLower(readTrimmed(filepath.Join(dir, base+"_label")))
			if strings.Contains(label, "package") ||
				strings.Contains(label, "tdie") ||
				strings.Contains(label, "tccd") ||
				strings.Contains(label, "cpu") {
				return chip + " " + label, p
			}
		}
	}

	// Pass 3: ACPI thermal zone — present on virtually all x86 systems.
	// Less accurate than coretemp/k10temp but better than nothing.
	for _, e := range entries {
		dir := filepath.Join(m.hwmonRoot, e.Name())
		if readTrimmed(filepath.Join(dir, "name")) != "acpitz" {
			continue
		}
		p := filepath.Join(dir, "temp1_input")
		if _, err := os.Stat(p); err == nil {
			return "ACPI Thermal", p
		}
	}

	return "", ""
}

// discoverAMDGPUTemp returns the best AMD GPU temperature sensor path.
// Prefers junction/hotspot (temp2_input) over edge (temp1_input).
//
// Reads from m.hwmonRoot so tests can point at a fixture tree.
func (m *Manager) discoverAMDGPUTemp() (name, path string) {
	entries, _ := os.ReadDir(m.hwmonRoot)
	for _, e := range entries {
		dir := filepath.Join(m.hwmonRoot, e.Name())
		if readTrimmed(filepath.Join(dir, "name")) != "amdgpu" {
			continue
		}
		// Prefer junction temperature (hotter, safer threshold) over edge.
		for _, candidate := range []string{"temp2_input", "temp1_input"} {
			p := filepath.Join(dir, candidate)
			if _, err := os.Stat(p); err == nil {
				base := strings.TrimSuffix(candidate, "_input")
				label := readTrimmed(filepath.Join(dir, base+"_label"))
				if label == "" {
					label = "GPU"
				}
				return "amdgpu " + label, p
			}
		}
	}
	return "", ""
}

// gatherProfile reads CPU/GPU hardware metadata for curve tuning and display.
// Reads from m.procRoot and m.powercapRoot so tests can point at a fixture
// tree; NVML and AMD GPU hwmon paths continue to use the caller-supplied
// absolute paths (those are already resolved against m.hwmonRoot upstream).
func (m *Manager) gatherProfile(cpuSensorPath string, nvmlOK bool, gpuTempPath string) *HWProfile {
	p := &HWProfile{}
	p.CPUModel = m.readCPUModel()
	p.CPUTDPW = m.readRAPLTDPW()
	if cpuSensorPath != "" {
		p.CPUThermalC = readHwmonCritC(cpuSensorPath)
	}
	if nvmlOK {
		gpuCount := nvidia.CountGPUs()
		for i := 0; i < gpuCount; i++ {
			gp := GPUProfile{
				Index:    i,
				Model:    nvidia.GPUName(uint(i)),
				PowerW:   nvidia.PowerLimitW(uint(i)),
				ThermalC: nvidia.SlowdownThreshold(uint(i)),
			}
			p.GPUs = append(p.GPUs, gp)
			if i == 0 {
				// Keep top-level fields pointing at GPU 0 so buildConfig
				// and existing web-UI consumers need no changes.
				p.GPUModel = gp.Model
				p.GPUPowerW = gp.PowerW
				p.GPUThermalC = gp.ThermalC
			}
		}
	} else if gpuTempPath != "" {
		dir := filepath.Dir(gpuTempPath)
		amd := GPUProfile{
			Index:    0,
			Model:    "AMD GPU",
			PowerW:   readAMDGPUPowerW(dir),
			ThermalC: readAMDGPUCritC(gpuTempPath),
		}
		p.GPUs = append(p.GPUs, amd)
		p.GPUModel = amd.Model
		p.GPUPowerW = amd.PowerW
		p.GPUThermalC = amd.ThermalC
	}
	return p
}

// readCPUModel returns the CPU model name from <procRoot>/cpuinfo.
func (m *Manager) readCPUModel() string {
	data, err := os.ReadFile(filepath.Join(m.procRoot, "cpuinfo"))
	if err != nil {
		return ""
	}
	for _, line := range strings.SplitN(string(data), "\n", 50) {
		if strings.HasPrefix(line, "model name") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// readRAPLTDPW reads the CPU package TDP from Intel RAPL in watts.
// Returns 0 if unavailable (e.g. AMD CPUs, no RAPL support). Reads from
// m.powercapRoot so tests can point at a fixture tree.
func (m *Manager) readRAPLTDPW() int {
	for _, p := range []string{
		filepath.Join(m.powercapRoot, "intel-rapl", "intel-rapl:0", "constraint_0_power_limit_uw"),
		filepath.Join(m.powercapRoot, "intel-rapl:0", "constraint_0_power_limit_uw"),
	} {
		if data := readTrimmed(p); data != "" {
			uw, err := strconv.ParseInt(data, 10, 64)
			if err == nil && uw > 0 {
				return int(uw / 1_000_000)
			}
		}
	}
	return 0
}

// readHwmonCritC returns the critical temperature (°C) for the sensor at sensorPath.
// Tries the matching _crit file first, then scans for a package/tdie-labeled _crit.
func readHwmonCritC(sensorPath string) float64 {
	// Try the direct _crit counterpart of the sensor file.
	critPath := strings.TrimSuffix(sensorPath, "_input") + "_crit"
	if v := parseMilliC(readTrimmed(critPath)); v > 0 {
		return v
	}
	// Scan all temp*_crit in the dir for a package/tdie label.
	dir := filepath.Dir(sensorPath)
	crits, _ := filepath.Glob(filepath.Join(dir, "temp*_crit"))
	for _, c := range crits {
		base := strings.TrimSuffix(filepath.Base(c), "_crit")
		label := strings.ToLower(readTrimmed(filepath.Join(dir, base+"_label")))
		if strings.Contains(label, "package") || strings.Contains(label, "tdie") {
			if v := parseMilliC(readTrimmed(c)); v > 0 {
				return v
			}
		}
	}
	return 0
}

// readAMDGPUPowerW returns the current AMD GPU power limit in watts from hwmon.
func readAMDGPUPowerW(hwmonDir string) int {
	data := readTrimmed(filepath.Join(hwmonDir, "power1_cap"))
	if data == "" {
		return 0
	}
	uw, err := strconv.ParseInt(data, 10, 64)
	if err != nil || uw <= 0 {
		return 0
	}
	return int(uw / 1_000_000)
}

// readAMDGPUCritC returns the critical temperature for an AMD GPU temp sysfs path.
func readAMDGPUCritC(sensorPath string) float64 {
	critPath := strings.TrimSuffix(sensorPath, "_input") + "_crit"
	return parseMilliC(readTrimmed(critPath))
}

// parseMilliC converts a millidegree Celsius string (as used in sysfs) to °C.
func parseMilliC(s string) float64 {
	if s == "" {
		return 0
	}
	mc, err := strconv.ParseFloat(s, 64)
	if err != nil || mc <= 0 {
		return 0
	}
	return mc / 1000.0
}

// minCurvePWM returns the lowest PWM to use as a curve floor across the given
// fans. It prefers stop_pwm (the minimum to keep a running fan spinning) over
// start_pwm (the kick needed from standstill), since during normal operation
// the fan is already running and the lower stop_pwm is sufficient.
func minStartPWM(fans []fanDiscovery) uint8 {
	best := uint8(255)
	found := false
	for _, f := range fans {
		// Prefer stop_pwm; fall back to start_pwm.
		candidate := f.stopPWM
		if candidate == 0 {
			candidate = f.startPWM
		}
		if candidate > 0 {
			if !found || candidate < best {
				best = candidate
				found = true
			}
		}
	}
	if !found {
		return 20
	}
	return best
}

func clampTemp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return math.Round(v)
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// chipNameOf reads the hwmon chip name attached to the given sysfs path.
// pwmPath / sensorPath both live under <hwmonN>/, and the chip's stable
// identifier (nct6687, it87, amdgpu, ...) is at <hwmonN>/name. Used by
// the config writer to populate ChipName so config.ResolveHwmonPaths can
// re-anchor paths after a hwmonN reshuffle.
//
// Returns "" for non-hwmon paths or when the name file is unreadable.
// An empty result is the documented signal that resolution should not
// attempt to rewrite this entry.
func chipNameOf(path string) string {
	if path == "" {
		return ""
	}
	return readTrimmed(filepath.Join(filepath.Dir(path), "name"))
}

// emitPreflightDiag pushes a hwdiag entry for a preflight blocker. Called
// before any attempt to build the driver so the user sees a one-click fix
// instead of a build failure.
func (m *Manager) emitPreflightDiag(nd hwmonpkg.DriverNeed, pre hwmonpkg.PreflightResult) {
	m.mu.Lock()
	store := m.diagStore
	m.mu.Unlock()
	if store == nil {
		return
	}

	var entry hwdiag.Entry
	switch pre.Reason {
	case hwmonpkg.ReasonKernelHeadersMissing:
		entry = hwdiag.Entry{
			ID:        hwdiag.IDOOTKernelHeadersMissing,
			Component: hwdiag.ComponentOOT,
			Severity:  hwdiag.SeverityError,
			Summary:   "Kernel headers are required to install the " + nd.ChipName + " driver",
			Detail:    pre.Detail,
			Affected:  []string{nd.Module},
			Remediation: &hwdiag.Remediation{
				AutoFixID: hwdiag.AutoFixInstallKernelHdrs,
				Label:     "Install kernel headers",
				Endpoint:  "/api/hwdiag/install-kernel-headers",
			},
		}
	case hwmonpkg.ReasonDKMSMissing:
		entry = hwdiag.Entry{
			ID:        hwdiag.IDOOTDKMSMissing,
			Component: hwdiag.ComponentOOT,
			Severity:  hwdiag.SeverityWarn,
			Summary:   "Install DKMS so the " + nd.ChipName + " driver survives kernel upgrades",
			Detail:    pre.Detail,
			Affected:  []string{nd.Module},
			Remediation: &hwdiag.Remediation{
				AutoFixID: hwdiag.AutoFixInstallDKMS,
				Label:     "Install DKMS",
				Endpoint:  "/api/hwdiag/install-dkms",
			},
		}
	case hwmonpkg.ReasonSecureBootBlocks:
		entry = hwdiag.Entry{
			ID:        hwdiag.IDOOTSecureBoot,
			Component: hwdiag.ComponentSecureBoot,
			Severity:  hwdiag.SeverityError,
			Summary:   "Secure Boot blocks the unsigned " + nd.ChipName + " driver",
			Detail:    pre.Detail,
			Affected:  []string{nd.Module},
			Remediation: &hwdiag.Remediation{
				AutoFixID: hwdiag.AutoFixMOKEnroll,
				Label:     "Show MOK enrollment steps",
				Endpoint:  "/api/hwdiag/mok-enroll",
			},
		}
	case hwmonpkg.ReasonKernelTooNew:
		entry = hwdiag.Entry{
			ID:        hwdiag.IDOOTKernelTooNew,
			Component: hwdiag.ComponentOOT,
			Severity:  hwdiag.SeverityError,
			Summary:   "Kernel is newer than the " + nd.ChipName + " driver supports",
			Detail:    pre.Detail,
			Affected:  []string{nd.Module},
		}
	default:
		return
	}
	store.Set(entry)
}

// emitDMICandidates is the Tier 3 entrypoint. Called only when the
// capability-first hwmon pass returned zero controllable fans. Consults
// /sys/class/dmi/id/* and emits one ComponentDMI entry per proposed driver,
// or a single IDDMINoMatch info entry when DMI doesn't match any seed.
//
// Never calls modprobe. The web UI surfaces each candidate as a
// TRY_MODULE_LOAD button the user explicitly clicks.
func (m *Manager) emitDMICandidates() {
	m.emitDMICandidatesFor(hwmonpkg.ReadDMI(""))
}

// emitDMICandidatesFor is the testable core of emitDMICandidates; the wrapper
// only handles the DMI read from the live root.
func (m *Manager) emitDMICandidatesFor(info hwmonpkg.DMIInfo) {
	m.mu.Lock()
	store := m.diagStore
	m.mu.Unlock()
	if store == nil {
		return
	}

	store.ClearComponent(hwdiag.ComponentDMI)

	candidates := hwmonpkg.ProposeModulesByDMI(info)
	if len(candidates) == 0 {
		store.Set(hwdiag.Entry{
			ID:        hwdiag.IDDMINoMatch,
			Component: hwdiag.ComponentDMI,
			Severity:  hwdiag.SeverityWarn,
			Summary:   "No fan controllers found and your board isn't in the known-hardware list",
			Detail: "Ventd couldn't detect a controllable fan chip and couldn't propose a driver " +
				"based on your board identifiers. You may need to load a kernel module manually " +
				"or configure fan control via the BIOS.",
			Context: map[string]any{
				"board_vendor": info.BoardVendor,
				"board_name":   info.BoardName,
				"product_name": info.ProductName,
				"sys_vendor":   info.SysVendor,
			},
		})
		return
	}

	for _, nd := range candidates {
		store.Set(hwdiag.Entry{
			ID:        hwdiag.IDDMICandidatePrefix + nd.Key,
			Component: hwdiag.ComponentDMI,
			Severity:  hwdiag.SeverityWarn,
			Summary:   "Try loading the " + nd.ChipName + " driver for your board",
			Detail:    nd.Explanation,
			Affected:  []string{nd.Module},
			Remediation: &hwdiag.Remediation{
				AutoFixID: hwdiag.AutoFixTryModuleLoad,
				Label:     "Try loading " + nd.ChipName,
				Endpoint:  "/api/setup/load-module",
			},
			Context: map[string]any{
				"driver_key":   nd.Key,
				"module":       nd.Module,
				"board_vendor": info.BoardVendor,
				"board_name":   info.BoardName,
			},
		})
	}
}

// emitCPUSensorModuleMissingDiag surfaces a remediation card when the wizard
// wanted a CPU temperature sensor but no enumerable chip was present in
// /sys/class/hwmon. The UI renders a "Load <module>" button that POSTs to
// /api/setup/load-module; the server-side allowlist (internal/setup/modprobe.go)
// is the choke point that decides what's actually permitted.
//
// Skipped when the DMI path already proposed the same module so the UI
// doesn't render two cards for the same fix. Also skipped when no vendor can
// be inferred from /proc/cpuinfo — we refuse to guess; "no CPU sensor" is
// better UX than a button that loads the wrong driver.
func (m *Manager) emitCPUSensorModuleMissingDiag() {
	m.mu.Lock()
	store := m.diagStore
	m.mu.Unlock()
	if store == nil {
		return
	}

	module, chipName := cpuSensorModuleForVendor(m.readCPUVendor())
	if module == "" {
		return
	}

	// Suppress when the DMI pass already proposed the same module — the
	// existing dmi.candidate.* entry is the canonical one in that case and
	// will carry the same /api/setup/load-module endpoint.
	dmiID := hwdiag.IDDMICandidatePrefix + module
	for _, e := range store.Snapshot(hwdiag.Filter{}).Entries {
		if e.ID == dmiID {
			return
		}
	}

	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDHwmonCPUModuleMissing,
		Component: hwdiag.ComponentHwmon,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "CPU temperature sensor needs the " + module + " kernel module",
		Detail: "No CPU temperature sensor was detected. Your CPU's thermal sensor " +
			"driver (" + module + ") is not loaded, so fan curves can't react to CPU " +
			"temperature. Click the button to load it now and persist it for future boots.",
		Affected: []string{module},
		Remediation: &hwdiag.Remediation{
			AutoFixID: hwdiag.AutoFixTryModuleLoad,
			Label:     "Load " + chipName + " driver",
			Endpoint:  "/api/setup/load-module",
		},
		Context: map[string]any{
			"module":    module,
			"chip_name": chipName,
		},
	})
}

// cpuSensorModuleForVendor maps a /proc/cpuinfo vendor_id to the kernel
// module that reports CPU temperatures for it. Returns "", "" for unknown
// vendors so the caller can skip emitting a remediation the user can't use.
func cpuSensorModuleForVendor(vendor string) (module, chipName string) {
	switch vendor {
	case "GenuineIntel":
		return "coretemp", "coretemp"
	case "AuthenticAMD", "HygonGenuine":
		return "k10temp", "k10temp"
	default:
		return "", ""
	}
}

// readCPUVendor returns the vendor_id from <procRoot>/cpuinfo (e.g.
// "GenuineIntel", "AuthenticAMD"), or "" if it can't be read. ARM systems
// don't expose vendor_id; the empty return ensures the CPU-module-missing
// emitter stays silent on those hosts.
func (m *Manager) readCPUVendor() string {
	data, err := os.ReadFile(filepath.Join(m.procRoot, "cpuinfo"))
	if err != nil {
		return ""
	}
	for _, line := range strings.SplitN(string(data), "\n", 50) {
		if strings.HasPrefix(line, "vendor_id") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// emitBuildFailedDiag surfaces a generic OOT build failure after every
// preflight check passed — the build still broke for a reason we didn't
// anticipate (e.g. compiler version mismatch, upstream regression).
func (m *Manager) emitBuildFailedDiag(nd hwmonpkg.DriverNeed, err error) {
	m.mu.Lock()
	store := m.diagStore
	m.mu.Unlock()
	if store == nil {
		return
	}
	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDOOTBuildFailed,
		Component: hwdiag.ComponentOOT,
		Severity:  hwdiag.SeverityError,
		Summary:   "Could not build the " + nd.ChipName + " driver",
		Detail:    err.Error(),
		Affected:  []string{nd.Module},
	})
}

// preflightReasonToFailureClass maps a hwmon.PreflightOOT Reason
// directly onto a recovery.FailureClass so the wizard's calibration-
// page recovery cards include the correct auto-fix actions without
// relying on regex-matching the operator-facing detail string. The
// previous text-classification path silently fell through to
// ClassUnknown (bundle-only card) for every preflight-classified
// failure — caught on Phoenix's HIL when ReasonStaleDKMSState
// fired but the wizard banner showed only the diag-bundle button.
//
// Each Reason maps to its canonical class. A Reason without a
// recovery class returns ClassUnknown explicitly — the caller
// continues with the regex fallback (which currently has no rules
// for these but the wiring is in place when classify.go grows
// matching ones).
func preflightReasonToFailureClass(r hwmonpkg.Reason) recovery.FailureClass {
	switch r {
	case hwmonpkg.ReasonKernelHeadersMissing:
		return recovery.ClassMissingHeaders
	case hwmonpkg.ReasonDKMSMissing:
		return recovery.ClassDKMSBuildFailed
	case hwmonpkg.ReasonSecureBootBlocks,
		hwmonpkg.ReasonSignFileMissing,
		hwmonpkg.ReasonMokutilMissing:
		return recovery.ClassSecureBoot
	case hwmonpkg.ReasonGCCMissing, hwmonpkg.ReasonMakeMissing:
		return recovery.ClassMissingBuildTools
	case hwmonpkg.ReasonContainerised:
		return recovery.ClassContainerised
	case hwmonpkg.ReasonNoSudoNoRoot:
		return recovery.ClassDaemonNotRoot
	case hwmonpkg.ReasonLibModulesReadOnly:
		return recovery.ClassReadOnlyRootfs
	case hwmonpkg.ReasonAptLockHeld:
		return recovery.ClassPackageManagerBusy
	case hwmonpkg.ReasonStaleDKMSState:
		return recovery.ClassDKMSStateCollision
	case hwmonpkg.ReasonInTreeDriverConflict:
		return recovery.ClassInTreeConflict
	case hwmonpkg.ReasonAnotherWizardRunning:
		return recovery.ClassConcurrentInstall
	case hwmonpkg.ReasonDiskFull:
		return recovery.ClassDiskFull
	}
	return recovery.ClassUnknown
}

// readKernelJournal pulls the most recent N kernel ring-buffer
// lines via `journalctl -k -n N`, falling back to `dmesg | tail`
// on systems without journald (Alpine without elogind, runit-only
// distros). Returns nil on any failure — the recovery classifier
// treats an empty journal as "use installLog only", so a missing
// kernel log just degrades the ACPI-conflict detection rather
// than blocking other classifications.
//
// Used by the wizard's classifier path to feed kernel-side
// "ACPI: resource ... conflicts" stamps into Classify so
// ClassACPIResourceConflict (#817) can fire on MSI Z690-class
// boards where the ACPI conflict only appears in the kernel
// journal, not in the install pipeline's stdout.
func readKernelJournal(n int) []string {
	if path, err := exec.LookPath("journalctl"); err == nil {
		cmd := exec.Command(path, "-k", "-n", fmt.Sprintf("%d", n), "--no-pager")
		out, err := cmd.Output()
		if err == nil {
			return splitLines(string(out))
		}
	}
	if path, err := exec.LookPath("dmesg"); err == nil {
		cmd := exec.Command(path)
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		lines := splitLines(string(out))
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return lines
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
