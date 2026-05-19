// Package setup implements the first-boot setup wizard: discovers fans,
// calibrates them, and generates an initial config. Used by both the CLI
// --setup flag (RunBlocking) and the web UI (Start + polling via Progress).
package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
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
	PolarityPhase string `json:"polarity_phase,omitempty"` // "pending","testing","normal","inverted","phantom"
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
