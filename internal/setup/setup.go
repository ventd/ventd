// Package setup implements the first-boot setup wizard: discovers fans,
// calibrates them, and generates an initial config. Used by both the CLI
// --setup flag (RunBlocking) and the web UI (Start + polling via Progress).
package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/state"
)

// FanState describes a single fan during/after the setup process.
type FanState struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // "hwmon" or "nvidia"
	PWMPath       string `json:"pwm_path"`
	RPMPath       string `json:"rpm_path,omitempty"`
	ControlKind   string `json:"control_kind,omitempty"`   // "rpm_target" for fan*_target channels
	DetectPhase   string `json:"detect_phase"`             // "pending","detecting","found","none","n/a"
	PolarityPhase string `json:"polarity_phase,omitempty"` // "pending","testing","normal","inverted","phantom","probational"
	CalPhase      string `json:"cal_phase"`                // "pending","calibrating","done","skipped","error"
	StartPWM      uint8  `json:"start_pwm,omitempty"`
	StopPWM       uint8  `json:"stop_pwm,omitempty"`
	MaxRPM        int    `json:"max_rpm,omitempty"`
	IsPump        bool   `json:"is_pump,omitempty"`
	CalProgress   int    `json:"cal_progress"` // 0–100, live during calibrate phase

	// CurrentPWM + CurrentRPM are live readings during the
	// `calibrating` phase only. They're populated by the
	// progressInternal merge from calibrate.Status.CurrentPWM and a
	// just-in-time sysfs read of RPMPath. Zero on every other phase.
	// JS uses these to show real numbers in the per-fan strips
	// instead of placeholder em-dashes (Phoenix's HIL feedback:
	// "their pwm and rpm dont actually have numbers its just -").
	CurrentPWM int `json:"current_pwm,omitempty"`
	CurrentRPM int `json:"current_rpm,omitempty"`

	Error string `json:"error,omitempty"`
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
	cal            CalibrationBackend
	logger         *slog.Logger
	cancel         context.CancelFunc // fired by Abort; nil until Start wires it
	diagStore      *hwdiag.Store      // optional; when non-nil, preflight blockers are emitted here
	polarityProber polarity.Prober    // nil = skip polarity probe (tests); set by SetPolarityProber
	reprobeFn      ReProber           // nil = skip post-install re-probe; set by SetReProber

	// stateKV is the calibration-namespace KVDB used by the polarity
	// persistence call after Phase 5b (RULE-POLARITY-08). nil = skip
	// persistence (tests; the polarity probe result still lives on the
	// FanState slice for the wizard's own flow). Set by SetStateKV from
	// cmd/ventd/main.go.
	stateKV *state.KVDB

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

	// acousticGateOpts configures the v0.5.12 calibrate_acoustic
	// PhaseGate (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01). Empty
	// MicDevice (the default) → the gate is a no-op during run();
	// the wizard proceeds straight to the finalising phase.
	// SetAcousticGateOptions is the production wiring hook.
	acousticGateOpts AcousticGateOptions

	// calibrationCompleteFn is the v0.6.0 wiring hook into the
	// confidence aggregator's cold-start hard pin (RULE-AGG-WIRING-01
	// + RULE-AGG-COLDSTART-01). When set, it is invoked with the
	// wall-clock of phantom-verify completion — the canonical
	// "calibration is done, w_pred should be hard-pinned to 0 for the
	// next 5 minutes" boundary. nil = no aggregator wired (tests +
	// monitor-only systems); a nil hook is a clean no-op.
	calibrationCompleteFn func(time.Time)

	// applyConfigPathOverride redirects ApplyPhase's writeConfigAtomic
	// target away from the production /etc/ventd/config.yaml so tests
	// (which can run as root on dev hosts) never stomp the operator's
	// live config. Empty in production. Set via
	// SetApplyConfigPathOverride which exists solely as a test seam.
	applyConfigPathOverride string

	// reloadTriggerFn signals the daemon's main loop that config.yaml
	// has changed and controllers should reload against it. Wired from
	// cmd/ventd/main.go to push to restartCh — the same channel
	// handleSetupReset uses (#1229). Called by runOrchestrator after
	// ApplyPhase Success + polarity persist so a fresh-install operator
	// sees the dashboard reflect the wizard-emitted config and active
	// controllers spawn against the newly-discovered fans without a
	// manual `systemctl restart ventd`. A nil fn (tests + monitor-only
	// short-circuit paths) is a clean no-op.
	reloadTriggerFn func()

	// events is the in-memory ring buffer for the activity-feed SSE.
	// Capped at maxEventsRingSize entries; appendEventLocked drops the
	// oldest on overflow. Reads happen via EventsSince(cursor) which
	// returns a copy so the caller can render without holding m.mu.
	// The SSE handler in internal/web polls this every 250ms and
	// re-flushes only the new entries since the last cursor — there's
	// no goroutine plumbing or per-subscriber channel; the bounded
	// ring + cursor poll is the whole transport.
	events []Event
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

// SetStateKV wires the calibration-namespace KV store used by Phase 5b
// to persist polarity results via polarity.Persist (RULE-POLARITY-08).
// Without this, the wizard classifies polarity correctly on its local
// FanState slice but the classification is lost on the next daemon
// restart — the controller path then runs every channel as "unknown"
// and polarity.WritePWM refuses every write on inverted-polarity hardware.
// Issue #1037. A nil db is a no-op; tests that don't care about
// persistence leave it unset.
func (m *Manager) SetStateKV(db *state.KVDB) {
	m.mu.Lock()
	m.stateKV = db
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

// SetAcousticGateOptions wires the v0.5.12 R30 mic-calibration gate
// (RULE-WIZARD-GATE-CALIBRATE-ACOUSTIC-01). Production code in
// cmd/ventd/main.go sets this when the operator passes a --mic flag (or
// equivalent web-UI selection); tests leave it unset (zero MicDevice)
// so the wizard's calibrate_acoustic phase is a clean no-op.
//
// The gate runs after thermal calibration completes and before the
// finalising phase. A failed runner is non-fatal — the daemon falls
// back to R33 proxy-only acoustic estimation when no K_cal record
// lands.
func (m *Manager) SetAcousticGateOptions(opts AcousticGateOptions) {
	m.mu.Lock()
	m.acousticGateOpts = opts
	m.mu.Unlock()
}

// SetApplyConfigPathOverride redirects ApplyPhase's writeConfigAtomic
// target so tests on root-owned dev hosts never stomp the operator's
// live /etc/ventd/config.yaml. Empty string = use the production
// default (DefaultConfigPath in the orchestrator package). The
// production wizard never calls this; it exists for in-tree tests that
// drive runOrchestrator end-to-end.
func (m *Manager) SetApplyConfigPathOverride(path string) {
	m.mu.Lock()
	m.applyConfigPathOverride = path
	m.mu.Unlock()
}

// SetReloadTrigger wires the daemon-reload hook called after ApplyPhase
// succeeds. Production code in cmd/ventd/main.go binds this to a closure
// that pushes to restartCh (the same channel handleSetupReset uses), so
// the daemon's main loop re-reads /etc/ventd/config.yaml and spawns
// controllers for the freshly-discovered fans on the same path that
// SIGHUP / web-UI reload follow.
//
// Without this, the wizard's atomic config write leaves the daemon
// running with the pre-wizard liveCfg until the next manual restart —
// every dashboard surface reads stale state and (more critically) no
// controller binds to the new fans, so a host with a stripped pre-
// wizard config sits in effective monitor-only with the CPU climbing
// unmitigated (#1232).
//
// A nil callback is a clean no-op (tests; monitor-only-only paths).
func (m *Manager) SetReloadTrigger(fn func()) {
	m.mu.Lock()
	m.reloadTriggerFn = fn
	m.mu.Unlock()
}

// fireReloadTrigger invokes the registered reload callback under
// release-the-lock-before-call discipline so a slow trigger (channel
// send blocked by a non-draining receiver) can't deadlock Manager
// operations. A nil hook is a clean no-op.
func (m *Manager) fireReloadTrigger() {
	m.mu.Lock()
	fn := m.reloadTriggerFn
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// SetCalibrationCompleteFn wires the v0.6.0 cold-start hard-pin hook
// (RULE-AGG-WIRING-01). The callback is invoked exactly once per
// wizard run, after Phase 6b's phantom-verification completes and
// before the wizard's finalising phase. Production code in
// cmd/ventd/main.go binds this to aggregator.SetEnvelopeCDoneAt so
// the v0.5.9 confidence-gated controller's 5-minute cold-start window
// (RULE-AGG-COLDSTART-01) anchors at a real wall-clock instead of the
// zero-value time.Time that left the pin structurally inert through
// every v0.5.x release (issue #1035 row 4).
//
// A nil callback (the test default) is a clean no-op.
func (m *Manager) SetCalibrationCompleteFn(fn func(time.Time)) {
	m.mu.Lock()
	m.calibrationCompleteFn = fn
	m.mu.Unlock()
}

// fireCalibrationComplete invokes the registered calibration-complete
// hook with the given wall-clock. Manager.run calls this from the
// post-phantom-verify / pre-finalising transition; the rule contract
// (RULE-AGG-WIRING-01) names the helper as the dispatch surface so a
// refactor that drops the call site from Manager.run requires actively
// deleting a named-method call rather than the inline read-and-invoke
// pattern that used to live there.
//
// Locking semantics match the original inline shape: read the field
// under m.mu, release the lock, invoke without holding the lock so a
// slow hook can't block other Manager operations. A nil hook is a
// clean no-op. (#1075)
func (m *Manager) fireCalibrationComplete(at time.Time) {
	m.mu.Lock()
	fn := m.calibrationCompleteFn
	m.mu.Unlock()
	if fn != nil {
		fn(at)
	}
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
// CalibrationBackend is the minimal calibrate.Manager surface that
// setup.Manager consumes. Extracted from the concrete type so tests
// can inject a fake that records calls, panics, returns synthetic
// errors, etc. without standing up a real calibration pipeline (#132).
// `*calibrate.Manager` satisfies it implicitly.
type CalibrationBackend interface {
	AllStatus() []calibrate.Status
	DetectRPMSensor(fan *config.Fan) (calibrate.DetectResult, error)
	RunSync(ctx context.Context, fan *config.Fan) (calibrate.Result, error)
}

func New(cal CalibrationBackend, logger *slog.Logger) *Manager {
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
//
// When hwmonRoot is anything other than the production default
// (defaultHwmonRoot), this is a test caller and applyConfigPathOverride
// is auto-set to a sentinel that makes ApplyPhase's writeConfigAtomic
// fail with a clear error rather than silently stomping the operator's
// live /etc/ventd/config.yaml — a real footgun on root-uid dev hosts
// (Phoenix's 13900K + RTX 4090 box) where NVMLPhase enumerates the
// real GPU even with a faked hwmonRoot. Test helpers that drive the
// orchestrator end-to-end should call SetApplyConfigPathOverride with
// a real t.TempDir() path so the phase succeeds.
func NewWithRoots(cal CalibrationBackend, logger *slog.Logger, hwmonRoot, procRoot, powercapRoot string) *Manager {
	m := &Manager{
		cal:                 cal,
		logger:              logger,
		hwmonRoot:           hwmonRoot,
		procRoot:            procRoot,
		powercapRoot:        powercapRoot,
		settleAfterModprobe: defaultSettleAfterModprobe,
	}
	if hwmonRoot != defaultHwmonRoot {
		m.applyConfigPathOverride = testApplyConfigSentinel
	}
	return m
}

// testApplyConfigSentinel is a path that does not exist and is not
// writable — when NewWithRoots is called with non-default roots (i.e.
// from a test), ApplyPhase writes here unless the test caller has
// supplied a real override via SetApplyConfigPathOverride. The sentinel
// fails the write with a clear error so a test that accidentally
// triggers the full orchestrator without isolation surfaces the
// missing-override mistake rather than corrupting the live config.
const testApplyConfigSentinel = "/var/lib/ventd/.test-apply-must-set-override-via-SetApplyConfigPathOverride"

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
//
// Issue #1060 (F3): AcquireWizardLock is called BEFORE the goroutine spawns
// so a concurrent wizard run on a sibling daemon (or a re-entry via
// `--setup`) is detected and the caller sees *ErrWizardAlreadyRunning. The
// release func is invoked from the run goroutine's defer so the lock file
// is removed on every exit path including panic.
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("setup: already running")
	}
	if m.done {
		m.mu.Unlock()
		return fmt.Errorf("setup: already completed; restart the daemon for a fresh run")
	}
	// AcquireWizardLock outside the goroutine so a sibling-already-running
	// failure surfaces synchronously to the caller (web handler, CLI).
	// RULE-WIZARD-GATE-LOCK-01..03 — defined in lock.go.
	release, lockErr := acquireWizardLockFn()
	if lockErr != nil {
		m.errMsg = lockErr.Error()
		m.failureClass = recovery.ClassConcurrentInstall
		m.mu.Unlock()
		return lockErr
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
	m.mu.Unlock()
	go m.run(ctx, release)
	return nil
}

// acquireWizardLockFn is the test seam for the wizard-lock primitive.
// Production points it at AcquireWizardLock; tests can swap in a stub to
// exercise the "lock already held" surface without writing real lock files.
var acquireWizardLockFn = AcquireWizardLock

// setPhase updates the current phase and its human-readable description.
// Also appends a phase-transition event to the in-memory ring buffer so
// subscribers to /api/v1/setup/events see the change without polling.
func (m *Manager) setPhase(phase, msg string) {
	m.mu.Lock()
	m.phase = phase
	m.phaseMsg = msg
	m.appendEventLocked("ok", "phase."+phase, msg)
	m.mu.Unlock()
	m.logger.Info("setup: " + msg)
}

// Event is one structured entry in the setup activity feed. Times are
// unix milliseconds so the JS-side renderer doesn't need to parse RFC
// timestamps. Level is one of {"info", "ok", "warn", "err"} matching
// the calibration UI's structured-event renderer (cal-state.js
// brand-voice taxonomy). Tag is a dotted namespace (e.g. "phase",
// "kmod.scan", "cal.start") that the renderer translates into a pill.
type Event struct {
	TS    int64  `json:"ts"`    // unix ms
	Level string `json:"level"` // info | ok | warn | err
	Tag   string `json:"tag"`   // dotted namespace
	Text  string `json:"text"`  // one-line free-form
}

// maxEventsRingSize bounds the in-memory event log. 500 entries
// covers a full ~50s calibration walk (the cal-state.js demo emits
// ~200 events end-to-end) with headroom for re-runs in the same
// daemon lifetime. Older entries drop on overflow.
const maxEventsRingSize = 500

// appendEventLocked appends one event to the ring buffer, dropping
// the oldest on overflow. Caller must hold m.mu. The TS is stamped
// from time.Now().UnixNano() so cursors stay strictly monotonic
// even when callers emit several events in the same millisecond
// (the calibration loop bursts ~5-10 events per fan transition).
// If two emits race in the same nanosecond — vanishingly rare on
// modern kernels but not impossible — the second one gets bumped
// by 1 ns so the cursor still advances and the SSE poll never
// silently drops a transition.
func (m *Manager) appendEventLocked(level, tag, text string) {
	ts := time.Now().UnixNano()
	if n := len(m.events); n > 0 && ts <= m.events[n-1].TS {
		ts = m.events[n-1].TS + 1
	}
	m.events = append(m.events, Event{
		TS:    ts,
		Level: level,
		Tag:   tag,
		Text:  text,
	})
	if len(m.events) > maxEventsRingSize {
		// Drop oldest by sliding window — copy is unavoidable for
		// the slice header reuse to keep the ring bounded.
		m.events = m.events[len(m.events)-maxEventsRingSize:]
	}
}

// EmitEvent is the exported event-emit hook for callers outside
// setup.Manager (e.g. the calibrate manager bridging fan-level
// transitions). Production code uses appendEventLocked from inside
// already-locked sections; this wrapper takes the lock for callers
// that don't already hold it. Also serves as a stable surface that
// tests can observe via EventsSince.
func (m *Manager) EmitEvent(level, tag, text string) {
	m.mu.Lock()
	m.appendEventLocked(level, tag, text)
	m.mu.Unlock()
}

// EventsSince returns events whose TS is strictly greater than
// sinceMs, along with the largest TS observed (for the caller to
// pass back as the next cursor). When the ring is empty or has
// nothing newer, returns (nil, sinceMs). Returned slice is a copy;
// safe for the caller to read without holding any lock.
func (m *Manager) EventsSince(sinceMs int64) ([]Event, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return nil, sinceMs
	}
	out := make([]Event, 0, len(m.events))
	latest := sinceMs
	for _, e := range m.events {
		if e.TS > sinceMs {
			out = append(out, e)
			if e.TS > latest {
				latest = e.TS
			}
		}
	}
	return out, latest
}

// readSysfsInt reads a single integer from a sysfs path. Used to
// surface live RPM during calibration into FanState.CurrentRPM.
// Returns -1 + error on read failure (caller treats negative as
// unknown so the JS shows em-dash, not a misleading 0).
func readSysfsInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	var v int
	_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &v)
	if err != nil {
		return -1, err
	}
	return v, nil
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

// afterFinalize re-runs the daemon-level probe so the persisted
// wizard.initial_outcome reflects the post-calibration kernel state. Called
// from Manager.run after the finalize phase has produced a non-empty
// doneFans set — at which point the calibration sweep + RPM correlation
// have proven controllable channels exist (RULE-PROBE-04 will classify the
// fresh probe as OutcomeControl).
//
// This complements afterDriverInstall (RULE-SETUP-REPROBE-01) which only
// fires when the wizard ran the installing_driver phase. Hosts whose
// driver is already loaded at first boot skip installing_driver entirely
// and would otherwise leave wizard.initial_outcome stuck at "monitor_only"
// from a stale daemon-startup probe forever — every smart-mode subsystem
// remains inert despite controllers actively driving PWM (issue #1108).
//
// reason is a short tag identifying the trigger ("FinalizeWithChannels")
// logged alongside the re-probe error, if any. Errors are non-fatal: the
// wizard's Phase 4 sysfs walk and the generated config are unaffected;
// only the persisted KV outcome misses the update, recoverable on the
// next driver-install / module-load / wizard-finalize event.
func (m *Manager) afterFinalize(ctx context.Context, reason string) {
	m.mu.Lock()
	rp := m.reprobeFn
	m.mu.Unlock()
	if rp == nil {
		return
	}
	if err := rp(ctx); err != nil {
		m.logger.Warn("setup: post-finalize reprobe failed; persisted wizard outcome may be stale until next daemon restart",
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
	// v0.8.x orchestrator: the legacy phase 0-7 inline body owned m.fans
	// directly; the orchestrator never writes there. To preserve the
	// wizard UI's fan roster + system cards during the multi-minute
	// calibrate window (#1230), synthesise FanState entries from the
	// orchestrator's checkpoint store when our local slice is empty.
	// Read once per Progress() call; the file is small (<20 KiB on an
	// 8-fan box) and the wizard UI polls at ~1 Hz, so the overhead is
	// trivial. m.mu held — synthesise inline rather than re-acquiring.
	if len(fans) == 0 {
		fans = synthesiseOrchestratorFans()
	}
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
				p.Fans[i].CurrentPWM = int(cs.CurrentPWM)
				if f.RPMPath != "" {
					if rpm, err := readSysfsInt(f.RPMPath); err == nil && rpm >= 0 {
						p.Fans[i].CurrentRPM = rpm
					}
				}
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
//
// release, when non-nil, is the wizard-lock release callback returned by
// AcquireWizardLock at Start time. run defers it so the lock file is
// removed on every exit path including panic.
func (m *Manager) run(ctx context.Context, release func()) {
	// Issue #1061 (F4): panic-recover so a crash in any phase doesn't take
	// down the daemon goroutine. The recover logs the panic + sets the
	// manager's terminal-state phase to "failed" so the web UI surfaces it
	// rather than the operator seeing a frozen wizard with no signal.
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			m.logger.Error("setup: wizard goroutine panicked; daemon survives, wizard transitions to failed",
				"panic", r, "stack", string(stack))
			m.mu.Lock()
			m.errMsg = fmt.Sprintf("wizard panic: %v", r)
			m.failureClass = recovery.ClassUnknown
			m.phase = "failed"
			m.phaseMsg = "Setup crashed unexpectedly. Send a diagnostic bundle so we can investigate."
			m.mu.Unlock()
		}
	}()
	defer func() {
		if release != nil {
			release()
		}
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

	// The v0.8.x orchestrator is the wizard. Manager.run drives it
	// unconditionally; on ApplyPhase success the wizard is "applied"
	// and the daemon proceeds to control loops, on any earlier-phase
	// failure m.errMsg + m.failureClass are set from the failing
	// outcome so the recovery card surfaces the right remediation.
	// Legacy phases 0-7 inline code (vendor-daemon, detect, install,
	// nvml-list, build-config, polarity, calibrate, finalize) was
	// removed in this PR — the orchestrator phase set is the single
	// source of truth.
	m.runOrchestrator(ctx)
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
