package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/grub"
	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/monitor"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/opportunistic"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/web/authpersist"
	webstatic "github.com/ventd/ventd/web"
)

// The per-IP brute-force guard thresholds live on config.Web so operators
// can tune them without a rebuild. See config.DefaultLoginFailThreshold
// and config.DefaultLoginLockoutCooldown for the baked-in defaults.

// loginThresholdOrDefault / loginCooldownOrDefault apply the config
// defaults when the daemon is running with a minimal (pre-validate)
// config, e.g. during first-boot before a config file exists.
func loginThresholdOrDefault(n int) int {
	if n <= 0 {
		return config.DefaultLoginFailThreshold
	}
	return n
}

func loginCooldownOrDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return config.DefaultLoginLockoutCooldown
	}
	return d
}

type Server struct {
	cfg        *atomic.Pointer[config.Config]
	configPath string
	authPath   string
	liveHash   atomic.Pointer[string] // current bcrypt hash; separate from config.yaml
	logger     *slog.Logger
	mux        *http.ServeMux
	handler    http.Handler
	httpSrv    *http.Server
	cal        *calibrate.Manager
	setup      *setupmgr.Manager
	opp        *opportunistic.Scheduler // v0.5.5 PR-B; nil until SetOpportunisticScheduler is called
	// v0.5.9 confidence-controller surfaces: aggregator + LayerA
	// estimator are read-only on the web side. nil until
	// SetConfidence is called (monitor-only mode).
	aggregator *aggregator.Aggregator
	layerA     *layer_a.Estimator
	// v0.5.12 #104: deeper smart-mode telemetry surfaces — coupling
	// (Layer B) RLS shards + marginal (Layer C) per-(channel,signature)
	// shards. Both expose SnapshotAll() with atomic.Pointer reads so
	// /api/v1/smart/channels never blocks the controller hot loop.
	// nil until SetSmartRuntimes is called.
	couplingRT *coupling.Runtime
	marginalRT *marginal.Runtime
	// decisions caches per-channel BlendedResult (the controller's
	// next-tick PWM target + refusal flags). Populated by main.go's
	// BlendFn after every controller.Compute. Read by the
	// /api/v1/smart/channels handler. Lock-free reads via atomic
	// pointer-swap; nil-safe (monitor-only mode skips wiring).
	decisions *controller.DecisionCache
	restartCh chan<- struct{}
	sessions  *sessionStore
	diag      *hwdiag.Store
	// doctorCache memoises the most recent doctor.Report; the runner
	// is constructed lazily on first /api/v1/doctor GET. See doctor.go.
	doctorCache    doctorRunnerCache
	ctx            context.Context // scoped to daemon lifetime; used by goroutines that outlive request handlers
	loginLim       *loginLimiter
	tlsActive      bool         // server serves TLS directly; gates HSTS
	trustedProxies []*net.IPNet // set at New-time from live.Web.TrustProxy
	// sseInterval bounds how often /api/events emits a status frame.
	// Matches the existing client-side poll cadence (2s) so the server
	// load profile stays unchanged when one SSE client displaces the
	// polling loop. Tests override this directly after New().
	sseInterval time.Duration

	// version is the build metadata served by /api/version. Populated by
	// main.go via SetVersionInfo before ListenAndServe. Zero value is safe:
	// tests that don't call the setter see an empty struct over /api/version.
	version VersionInfo
	// ready is the /healthz + /readyz snapshot. Populated by main.go via
	// SetReadyState. Nil-safe: the handlers return 503 with a clear reason
	// when ready is nil, so tests that skip readiness plumbing still work.
	ready *ReadyState
	// nowFn is a test seam for time-dependent code paths (/readyz
	// staleness, scheduler tick evaluation). Never set in production;
	// tests inject a deterministic clock via SetNowFn. Held as an
	// atomic.Pointer because production reads happen from the scheduler
	// goroutine concurrent with test writes, and the race detector
	// flags a plain field even when only one goroutine actually writes.
	nowFn atomic.Pointer[nowFnValue]

	// rescan holds the before/after/current snapshots from the most
	// recent /api/hardware/rescan call. Lazily initialised by the
	// handlers themselves — zero value is a ready-to-use mutex.
	rescan rescanState

	// panic holds the in-memory state for the Session C 2e panic
	// button. Zero value is a ready-to-use mutex; active=false means
	// the controllers run their normal tick loop.
	panic panicState

	// history keeps the last hour of sensor/fan samples so the
	// dashboard can render per-card sparklines without a round trip
	// per tick. Lost on daemon restart — the UI seeds from /api/history
	// once on page load and then appends from the existing SSE stream.
	history *HistoryStore

	// schedState tracks the scheduler goroutine's manual-override
	// flag and last observed scheduled winner. Zero value is ready
	// to use; see internal/web/schedule.go for the semantics.
	schedState scheduleState
	// schedTickMu serialises scheduleTick. Production has only one
	// scheduler goroutine so contention is zero; the mutex exists to
	// preserve the "one tick's load-compute-store sequence is atomic
	// relative to other ticks" invariant when tests drive ticks from
	// the test goroutine in parallel with the production goroutine
	// (issue #812 race).
	schedTickMu sync.Mutex
	// schedIntervalNS is the scheduler's tick period in nanoseconds,
	// accessed atomically because tests override it after New() has
	// already launched the scheduler goroutine. Zero falls back to
	// defaultScheduleInterval (60s) — see Server.schedulerInterval.
	schedIntervalNS atomic.Int64
	// schedWake is signalled by SetSchedulerInterval so the running
	// goroutine aborts its current wait and re-reads the new cadence
	// immediately. A buffered cap-1 channel is sufficient: the signal
	// is edge-triggered, not a queue of requests.
	schedWake chan struct{}

	// rebootBlocker is the test seam for handleSystemReboot's
	// container-detection guard. Nil in production; tests set it to
	// exercise the 409 path without needing to run under PID 1 or
	// touch /.dockerenv. See rebootEnvironmentBlocker for the real
	// detection logic.
	rebootBlocker func() string

	// rebootRestore is invoked before handleSystemReboot fires
	// systemctl reboot so the watchdog hands every controlled
	// channel back to firmware auto BEFORE the kernel-init sequence
	// kicks in (issue #1064 / RULE-HWMON-RESTORE-EXIT + RULE-WD-
	// RESTORE-BUDGET). Production wires this to watchdog.RestoreCtx
	// via SetRebootRestore; tests can swap a stub to observe ordering
	// without touching real sysfs. Nil-safe: a nil hook is skipped.
	rebootRestore func(ctx context.Context)

	// rebootExec is the test seam that runs the actual systemctl /
	// reboot command after the watchdog Restore. Production uses
	// exec.Command via runRebootCommands; tests can swap a stub to
	// observe call ordering. Nil falls back to runRebootCommands.
	rebootExec func()

	// kvWiper is called by handleSetupReset to wipe the wizard and probe
	// KV namespaces so the next daemon start treats the system as freshly
	// installed (RULE-PROBE-09). Nil in tests that don't need KV teardown;
	// set via SetKVWiper in main.go after Server construction.
	kvWiper func() error

	// polarityChannels carries the live probe.ControllableChannel slice
	// used by handlePanic's writeMaxPWMToAllFans so the PANIC, MAX
	// COOLING write routes through polarity.WritePWM (RULE-POLARITY-05).
	// Without this, an inverted-polarity fan flipped MaxPWM→MinPWM
	// (effectively turning the fan OFF) when the operator clicked the
	// panic button — the opposite of the safety intent. Issue #1037.
	//
	// Stored via atomic.Pointer so the daemon-startup SetPolarityChannels
	// call cannot race the SSE / handler goroutines that loop the slice
	// during panic handling. nil-load (no panicked write site has been
	// wired) returns an empty slice via the load-helper; tests that
	// don't supply channels read empty and skip the polarity-aware
	// branch in handlePanic.
	polarityChannels atomic.Pointer[[]*probe.ControllableChannel]
}

// New constructs the web server. authPath is the path to auth.json; pass ""
// to skip dedicated auth-file persistence (used in tests that set the hash
// directly on the config pointer).
//
// First-boot mode (no admin password set yet) is auto-detected from the
// liveHash; the wizard's password-set step is open to anyone on the LAN
// during this window. After the wizard completes, normal auth applies.
// Issue #765 documents the trade-off and the one-line recovery path
// (`rm /etc/ventd/auth.json && systemctl restart ventd`).
func New(ctx context.Context, cfg *atomic.Pointer[config.Config], configPath, authPath string, logger *slog.Logger, cal *calibrate.Manager, sm *setupmgr.Manager, restartCh chan<- struct{}, diag *hwdiag.Store) *Server {
	live := cfg.Load()
	if diag == nil {
		diag = hwdiag.NewStore()
	}
	s := &Server{
		cfg:            cfg,
		configPath:     configPath,
		authPath:       authPath,
		logger:         logger,
		mux:            http.NewServeMux(),
		cal:            cal,
		setup:          sm,
		restartCh:      restartCh,
		sessions:       newSessionStore(ctx, live.Web.SessionTTL.Duration),
		diag:           diag,
		ctx:            ctx,
		loginLim:       newLoginLimiter(ctx, loginThresholdOrDefault(live.Web.LoginFailThreshold), loginCooldownOrDefault(live.Web.LoginLockoutCooldown.Duration)),
		trustedProxies: parseTrustedProxies(live.Web.TrustProxy, logger),
		sseInterval:    defaultSSEInterval,
		history:        NewHistoryStore(defaultSSEInterval, historyDefaultWindow),
		schedWake:      make(chan struct{}, 1),
	}
	// Initialise liveHash: auth.json takes precedence over the config hash.
	// The config hash fallback keeps tests working (they set live.Web.PasswordHash
	// directly on the atomic pointer rather than writing an auth.json).
	var initialHash string
	if authPath != "" {
		if auth, loadErr := authpersist.Load(authPath); loadErr == nil && auth != nil {
			initialHash = auth.Admin.BcryptHash
		}
	}
	if initialHash == "" {
		initialHash = live.Web.PasswordHash
	}
	s.liveHash.Store(&initialHash)

	// Construct the http.Server at New-time rather than ListenAndServe-time
	// so Shutdown() can safely be called from another goroutine without
	// racing on the httpSrv field. Handler is immutable after New; Addr is
	// filled in by ListenAndServe before actually binding.
	s.httpSrv = &http.Server{
		// Bounded timeouts: blunt slowloris and header-exhaustion DoS.
		// ReadTimeout covers the full request body; WriteTimeout covers
		// response. Calibration is driven by background goroutines and
		// only polled over HTTP, so these bounds don't clip long-running
		// work — they only cap how long a socket can sit idle mid-request.
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		// Route stdlib http server logs (including TLS handshake errors)
		// through slog. tlsHandshakeWatcher promotes the
		// browser-cached-cert pattern to a one-shot WARN (#1169).
		ErrorLog: stdlog.New(newTLSHandshakeWatcher(logger), "", 0),
	}
	// First-boot detection (#765): when no auth hash is configured yet,
	// the wizard's password-set step is open without a token. Once
	// MarkApplied has fired and auth.json holds a hash, normal auth
	// gates every request.

	// Non-/api/ unauthenticated endpoints — these stay outside the
	// registerAPIRoutes helper because /api/v1 aliasing does not apply.
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)
	// Liveness and readiness probes. Unauthenticated so container
	// orchestrators and reverse proxies can scrape them without a session
	// cookie. /healthz flips 200 once main() has reached post-init;
	// /readyz requires the watchdog to have pinged and a sensor read
	// within the last 5s, so it stays 503 during stalls.
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)
	// Static UI assets (HTML/CSS/JS) live under the embedded `ui/` tree.
	// Authentication is intentionally NOT required: these are the same
	// bytes the unauth'd /login page already depends on, and withholding
	// the dashboard HTML behind auth gains nothing — the /api/* endpoints
	// that return real data are auth-gated separately.
	s.mux.Handle("/ui/", http.StripPrefix("/ui/", uiStaticHandler(uiSubFS())))

	// Design system: /shared/* serves tokens, shell, brand assets with a
	// 1-hour cache ceiling. sidebar.html and canon.md are test fixtures —
	// they are not embedded so requests for them return 404.
	s.registerSharedAssets()

	// Per-page design system pages. Each call wires /<name>, /<name>.css,
	// /<name>.js to web/<name>.* embedded assets. Pages are unauthenticated
	// at the route layer; the page's own JS gates display via the auth
	// state endpoint where appropriate.
	s.registerWebPage("setup")
	s.registerWebPage("calibration")
	s.registerWebPage("dashboard")
	s.registerWebPage("devices")
	s.registerWebPage("hardware")
	s.registerWebPage("smart")
	s.registerWebPage("curve-editor")
	s.registerWebPage("schedule")
	s.registerWebPage("sensors")
	s.registerWebPage("settings")
	s.registerWebPage("doctor")
	s.registerWebPage("health")

	// /login is served by handleLogin (which also handles POST), so the
	// HTML route is already wired. Only the /login.js asset needs its own
	// route — register it explicitly so we don't collide with the existing
	// HandleFunc("/login", ...) above.
	if loginJS, err := fs.ReadFile(webstatic.FS, "login.js"); err == nil {
		s.mux.HandleFunc("/login.js", staticAssetHandler(loginJS, "application/javascript; charset=utf-8"))
	}

	// Every /api/* route is registered twice: once under /api/<name> for
	// existing clients and once under /api/v1/<name> so future clients can
	// pin to a version. The helper wraps auth once and shares the wrapped
	// handler between the two paths, so a single request never triggers
	// the session check twice.
	s.registerAPIRoutes([]apiRoute{
		// Unauthenticated.
		{name: "ping", handler: s.handlePing, auth: false},
		// Lightweight unauthenticated probe so the login page can decide
		// which form to render (first-boot setup vs. normal password)
		// without POSTing an empty password and tripping the per-IP login
		// rate limiter. See audit finding S2.
		{name: "auth/state", handler: s.handleAuthState, auth: false},
		// Build metadata — same shape as `ventd --version --json`, exposed
		// so operators can identify a running daemon without shell access.
		{name: "version", handler: s.handleVersion, auth: false},
		// Self-update endpoints. /check polls GitHub for the latest tag;
		// /apply spawns install.sh with VENTD_VERSION set and exits 202
		// (daemon dies during the install's systemctl restart, comes
		// back under the new binary). /var/lib/ventd state persists.
		{name: "update/check", handler: s.handleUpdateCheck, auth: true},
		{name: "update/apply", handler: s.handleUpdateApply, auth: true},
		// Patch-notes endpoint — frontend reads on every page load and
		// shows a modal when the daemon's current version is newer than
		// the browser's last-seen-version (localStorage). #48.
		{name: "release-notes", handler: s.handleReleaseNotes, auth: true},

		// Authenticated routes.
		{name: "status", handler: s.handleStatus, auth: true},
		{name: "events", handler: s.handleEvents, auth: true},
		{name: "config", handler: s.handleConfig, auth: true},
		{name: "config/dryrun", handler: s.handleConfigDryrun, auth: true},
		{name: "hardware", handler: s.handleHardware, auth: true},
		{name: "hardware/inventory", handler: s.handleHardwareInventory, auth: true},
		{name: "hardware/rescan", handler: s.handleHardwareRescan, auth: true},
		{name: "debug/hwmon", handler: s.handleHwmonDebug, auth: true},
		{name: "panic", handler: s.handlePanic, auth: true},
		{name: "panic/state", handler: s.handlePanicState, auth: true},
		{name: "panic/cancel", handler: s.handlePanicCancel, auth: true},
		{name: "profile", handler: s.handleProfile, auth: true},
		{name: "profile/active", handler: s.handleProfileActive, auth: true},
		{name: "history", handler: s.handleHistory, auth: true},
		{name: "profile/schedule", handler: s.handleProfileSchedule, auth: true},
		{name: "schedule/status", handler: s.handleScheduleStatus, auth: true},
		{name: "calibrate/start", handler: s.handleCalibrateStart, auth: true},
		{name: "calibrate/status", handler: s.handleCalibrateStatus, auth: true},
		// v0.5.5: opportunistic-probe live status — read by the dashboard
		// to render the probe-in-flight pill.
		{name: "probe/opportunistic/status", handler: s.handleOpportunisticStatus, auth: true},
		{name: "calibrate/results", handler: s.handleCalibrateResults, auth: true},
		{name: "calibrate/abort", handler: s.handleCalibrateAbort, auth: true},
		{name: "detect-rpm", handler: s.handleDetectRPM, auth: true},
		{name: "setup/status", handler: s.handleSetupStatus, auth: true},
		{name: "setup/events", handler: s.handleSetupEvents, auth: true},
		{name: "setup/start", handler: s.handleSetupStart, auth: true},
		{name: "setup/apply", handler: s.handleSetupApply, auth: true},
		{name: "setup/apply-monitor-only", handler: s.handleSetupApplyMonitorOnly, auth: true},
		{name: "setup/reset", handler: s.handleSetupReset, auth: true},
		{name: "admin/factory-reset", handler: s.handleFactoryReset, auth: true},
		{name: "setup/calibrate/abort", handler: s.handleSetupCalibrateAbort, auth: true},
		{name: "setup/load-module", handler: s.handleSetupLoadModule, auth: true},
		{name: "system/reboot", handler: s.handleSystemReboot, auth: true},
		{name: "set-password", handler: s.handleSetPassword, auth: true},
		{name: "hwdiag", handler: s.handleHwdiag, auth: true},
		{name: "hwdiag/install-kernel-headers", handler: s.handleInstallKernelHeaders, auth: true},
		{name: "hwdiag/install-dkms", handler: s.handleInstallDKMS, auth: true},
		{name: "hwdiag/load-apparmor", handler: s.handleLoadAppArmor, auth: true},
		{name: "hwdiag/mok-enroll", handler: s.handleMOKEnroll, auth: true},
		{name: "hwdiag/grub-cmdline-add", handler: s.handleGrubCmdlineAdd, auth: true},
		{name: "hwdiag/modprobe-options-write", handler: s.handleModprobeOptionsWrite, auth: true},
		{name: "hwdiag/reset-and-reinstall", handler: s.handleResetAndReinstall, auth: true},
		{name: "system/watchdog", handler: s.handleSystemWatchdog, auth: true},
		{name: "system/recovery", handler: s.handleSystemRecovery, auth: true},
		{name: "system/security", handler: s.handleSystemSecurity, auth: true},
		{name: "system/diagnostics", handler: s.handleSystemDiagnostics, auth: true},
		// #792 wizard recovery surface — generate a redacted diag bundle on
		// demand so the calibration error banner can offer the operator a
		// download instead of a Go error string. Download path is registered
		// separately (trailing-slash route) because registerAPIRoutes only
		// expresses exact paths.
		{name: "diag/bundle", handler: s.handleDiagBundle, auth: true},
		// v0.5.12 #64: outbound bundle ingest. Refused with HTTP 412
		// when diag.upstream_ingest.enabled=false in config (default).
		// Operator opt-in surface for the maintainer-side support flow.
		{name: "diag/send", handler: s.handleDiagSend, auth: true},
		// v0.5.9 confidence-controller surfaces (PR-B). status returns
		// per-channel aggregator + LayerA snapshots for the 5-state
		// dashboard pill; preset GET/PUT exposes the smart-mode
		// aggressiveness selector.
		{name: "confidence/status", handler: s.handleConfidenceStatus, auth: true},
		{name: "confidence/preset", handler: s.handleConfidencePreset, auth: true},
		// v0.5.12 #104: deeper smart-mode telemetry. /smart/status is
		// a one-line aggregate ("are we converged, what preset, how
		// many channels"); /smart/channels is the per-channel deep dive
		// covering Layer-B coupling RLS state + Layer-C marginal RLS
		// state + signature label.
		{name: "smart/status", handler: s.handleSmartStatus, auth: true},
		{name: "smart/channels", handler: s.handleSmartChannels, auth: true},
		// v0.5.19 #50: doctor surface (spec-10). Read-only Report poll
		// against the runtime detector pack; cached for ~5s so a
		// multi-tab dashboard doesn't fan out into N detector re-runs
		// per tick.
		{name: "doctor", handler: s.handleDoctorReport, auth: true},
	})

	dlHandler := s.requireAuth(s.handleDiagDownload)
	s.mux.HandleFunc("/api/diag/download/", dlHandler)
	s.mux.HandleFunc("/api/v1/diag/download/", dlHandler)

	// Root document is served from the catch-all handler, authenticated.
	s.mux.HandleFunc("/", s.requireAuth(s.handleUI))

	// Compose middleware around the mux: security headers → origin check → mux.
	// Origin check runs after headers so rejected responses still carry
	// nosniff / CSP. CSRF/Origin wraps the mux so every mutation path
	// (config PUT, setup apply/reset, reboot, set-password, …) inherits it
	// without per-handler opt-in. tlsActive is detected per-request via r.TLS.
	s.handler = securityHeaders()(originCheck(logger)(s.mux))
	s.httpSrv.Handler = s.handler

	// Kick off the per-metric history sampler. Goroutine exits when
	// the daemon-scoped ctx is cancelled (same pattern as
	// expireSetupToken) so Shutdown() doesn't leak the ticker.
	go s.runHistorySampler(ctx)
	// Kick the scheduler goroutine off at construction rather than in
	// ListenAndServe so tests that exercise /api/schedule/status (and
	// the mock-clock tick path) don't need to stand up the HTTP
	// listener. The loop exits when ctx cancels.
	go s.runScheduler()

	return s
}

// apiRoute describes one /api/<name> endpoint. The registerAPIRoutes helper
// expands each entry into both "/api/<name>" and "/api/v1/<name>" handlers
// pointing at the same (optionally auth-wrapped) closure.
type apiRoute struct {
	name    string // trailing path after "/api/" — no leading slash
	handler http.HandlerFunc
	auth    bool // wrap with requireAuth before dual-registering
}

// registerAPIRoutes wires each route under both "/api/" and "/api/v1/"
// prefixes from a single slice. Middleware is applied once per route;
// the v1 alias shares the wrapped handler so a single request never
// pays the session check twice, and adding a new endpoint requires
// exactly one new slice entry rather than two HandleFunc calls.
//
// Middleware composition (outermost → innermost; runs in this order):
//
//  1. requireAuth (auth-only routes) — rejects unauthenticated requests
//     with 401 before any state-changing work happens. Public routes
//     (auth:false) skip this step entirely.
//  2. requireCSRF (auth-only routes) — rejects state-changing
//     requests without a valid X-CSRF-Token header (RULE-WEB-CSRF-
//     TOKEN-REQUIRED-ON-STATE-CHANGE). Safe methods (GET / HEAD /
//     OPTIONS) bypass the check inside the middleware so read-only
//     handlers aren't affected.
//  3. requireMaxBody (every route) — caps the request body at
//     defaultMaxBody (1 MiB) on POST / PUT / PATCH / DELETE so an
//     oversized payload surfaces as MaxBytesError on first read
//     rather than exhausting memory.
func (s *Server) registerAPIRoutes(routes []apiRoute) {
	for _, r := range routes {
		h := r.handler
		// Body cap applies to every route — the middleware no-ops
		// on safe methods, so wrapping public GETs costs nothing.
		h = requireMaxBody(defaultMaxBody, h)
		if r.auth {
			// CSRF check before the auth check: the auth check
			// reads the session cookie, the CSRF check reads the
			// session's bound CSRF token. Either fails-fast.
			h = s.requireCSRF(h)
			h = s.requireAuth(h)
		}
		s.mux.HandleFunc("/api/"+r.name, h)
		s.mux.HandleFunc("/api/v1/"+r.name, h)
	}
}

// nowFnValue wraps a clock function so Server.nowFn can be an
// atomic.Pointer without Go complaining about storing bare func values.
type nowFnValue struct {
	fn func() time.Time
}

// SetNowFn installs a test clock that replaces time.Now() in
// readyNow() and the scheduler goroutine. Safe to call concurrently
// with handlers and the scheduler tick.
func (s *Server) SetNowFn(fn func() time.Time) {
	if fn == nil {
		s.nowFn.Store(nil)
		return
	}
	s.nowFn.Store(&nowFnValue{fn: fn})
}

// SetSchedulerInterval overrides the scheduler tick cadence. Tests
// drop this to milliseconds so transitions land within the test's
// patience window. Production never calls it — the default 60s
// cadence matches minute-granularity schedule grammar. Atomic so
// calls from a test goroutine race-safely with the scheduler loop.
//
// A wake signal is raised so the goroutine aborts its current (long)
// wait and re-reads the shorter interval on the next iteration
// instead of sleeping through the old cadence.
func (s *Server) SetSchedulerInterval(d time.Duration) {
	s.schedIntervalNS.Store(int64(d))
	select {
	case s.schedWake <- struct{}{}:
	default:
	}
}

// schedulerInterval returns the current tick cadence, falling back
// to defaultScheduleInterval when unset.
func (s *Server) schedulerInterval() time.Duration {
	if v := s.schedIntervalNS.Load(); v > 0 {
		return time.Duration(v)
	}
	return defaultScheduleInterval
}

// SetVersionInfo populates the build metadata served by /api/version. Must
// be called before ListenAndServe; safe to skip in tests (handler returns
// the zero VersionInfo).
func (s *Server) SetVersionInfo(v VersionInfo) { s.version = v }

// SetReadyState wires the /healthz + /readyz probes to main.go's readiness
// tracking. Must be called before ListenAndServe; nil-safe in tests.
func (s *Server) SetReadyState(r *ReadyState) { s.ready = r }

// SetKVWiper registers the function called by handleSetupReset to wipe the
// wizard and probe KV namespaces on "Reset to initial setup" (RULE-PROBE-09).
// Pass probe.WipeNamespaces(st.KV) from main.go.
func (s *Server) SetKVWiper(fn func() error) { s.kvWiper = fn }

// SetPolarityChannels wires the live probe.ControllableChannel slice
// so handlePanic's MaxPWM writes route through polarity.WritePWM
// (RULE-POLARITY-05). Without this, an inverted-polarity fan flipped
// MaxPWM→MinPWM (effectively turning OFF) when the operator clicked
// the PANIC, MAX COOLING button — the opposite of the safety intent.
// nil-safe for tests. Issue #1037.
//
// atomic.Pointer store; safe to call from any goroutine and at any
// point in the server lifecycle. Production callers wire this once at
// daemon-startup; the API exists for future post-wizard re-probe
// reconfiguration so the lock-free reads are right by construction.
func (s *Server) SetPolarityChannels(channels []*probe.ControllableChannel) {
	if channels == nil {
		// Store an empty slice so the load helper never returns a
		// nil pointer; readers iterate the empty slice with no
		// special-case branch.
		empty := []*probe.ControllableChannel{}
		s.polarityChannels.Store(&empty)
		return
	}
	s.polarityChannels.Store(&channels)
}

// loadPolarityChannels is the lock-free read side. Returns an empty
// slice when SetPolarityChannels hasn't fired yet (tests, pre-wizard
// daemon-startup window).
func (s *Server) loadPolarityChannels() []*probe.ControllableChannel {
	if p := s.polarityChannels.Load(); p != nil {
		return *p
	}
	return nil
}

// SetRebootRestore installs the watchdog-restore hook called before
// systemctl reboot fires. Issue #1064 / RULE-HWMON-RESTORE-EXIT: the
// pre-reboot path MUST hand every controlled channel back to firmware
// auto before the kernel-init sequence so a slow systemd shutdown
// doesn't leave fans at the daemon's last manual-mode write. Nil-safe:
// a nil hook is skipped (legacy behaviour for callers that don't pass a
// watchdog).
func (s *Server) SetRebootRestore(fn func(ctx context.Context)) { s.rebootRestore = fn }

// writeJSON sets Content-Type to application/json, encodes v as JSON, and
// logs any encode failure at warn level with the request path. Callers that
// need a non-200 status must call w.WriteHeader before invoking writeJSON.
func (s *Server) writeJSON(r *http.Request, w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("web: encode response failed", "path", r.URL.Path, "err", fmt.Errorf("encode %s: %w", r.URL.Path, err))
	}
}

// requireAuth wraps h so that only authenticated requests pass through.
// Unauthenticated API requests get 401 JSON; unauthenticated page requests
// redirect to /login.
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessions.valid(sessionToken(r)) {
			h(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			s.writeJSON(r, w, map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// handlePing is an unauthenticated health-check used by the UI to detect when
// the daemon is back up after a restart.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(r, w, map[string]string{"status": "ok"})
}

// handleAuthState reports whether the daemon is in first-boot mode (no
// password configured yet). Deliberately cheap and untouchable by the
// login rate limiter: the login page calls this once on load to decide
// which form to show, and must not burn an attempt doing so.
//
// GET only; any other method is rejected. No caller-supplied input is
// read, so there is nothing to validate. The response leaks one bit
// (whether a password is set), which is the same bit a normal login
// with a wrong password would already reveal — no new information
// surface vs. the pre-fix behaviour.
func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, map[string]bool{"first_boot": s.authHashValue() == ""})
}

// computeUIEtags walks root and returns a map from request-relative path →
// strong ETag. ETags are computed once at boot (embedded assets are
// immutable per binary) as the first 16 bytes of SHA-256 in hex, quoted.
func computeUIEtags(root fs.FS) map[string]string {
	etags := make(map[string]string)
	_ = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		etags[path] = fmt.Sprintf(`"%x"`, sum[:16])
		return nil
	})
	return etags
}

// uiStaticHandler wraps http.FileServer(fs) with ETag-based conditional GET
// and a 1-hour cache ceiling. ETags are computed at boot from the embedded
// asset contents, so they track binary releases without relying on mod-times
// (which embed.FS always returns as zero).
//
// Content-Type is derived from extension by the stdlib FileServer.
func uiStaticHandler(root fs.FS) http.Handler {
	etags := computeUIEtags(root)
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is relative to the FS root (leading slash stripped by
		// http.StripPrefix before the request reaches this handler).
		path := strings.TrimPrefix(r.URL.Path, "/")
		h := w.Header()
		if etag, ok := etags[path]; ok {
			h.Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		h.Set("Cache-Control", "public, max-age=3600, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})
}

// handleLogin handles GET (serve login page) and POST (authenticate).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// New design-system login page, served from webstatic.FS. The
		// JS at /login.js redirects to /setup if first_boot is true,
		// so the GET path stays simple.
		if body, err := fs.ReadFile(webstatic.FS, "login.html"); err == nil {
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write(readUI("login.html"))

	case http.MethodPost:
		// Cap login form bodies before parsing so a huge payload cannot
		// exhaust memory on an unauthenticated endpoint.
		limitBody(w, r, 64<<10)
		if err := r.ParseForm(); err != nil {
			if isMaxBytesErr(err) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		ipKey := resolveClientIP(r, s.trustedProxies)
		if ok, retryAfter := s.loginLim.allow(ipKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			s.writeJSON(r, w, map[string]string{"error": "too many failed attempts; try again later"})
			return
		}

		live := s.cfg.Load()

		// First-boot: no password set yet. POST any new_password to set
		// the initial admin password and start a session — no token
		// gate (#765). An empty POST gets a 400 with first_boot:true so
		// the wizard can render the password-set form.
		if s.authHashValue() == "" {
			if r.FormValue("new_password") != "" {
				s.handleFirstBootLogin(w, r, live, ipKey)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(r, w, map[string]interface{}{"error": "first boot: use /api/auth/state to check status, then POST new_password to set the initial admin password", "first_boot": true})
			return
		}

		// Normal login: check password. Reject empty passwords early without
		// consuming a rate-limiter slot — a UI probe asking "is this daemon
		// in first-boot?" should already have used /api/auth/state, and we
		// do not want a typo'd form submission to count against the brute-
		// force budget.
		password := r.FormValue("password")
		if password == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(r, w, map[string]string{"error": "password required"})
			return
		}
		if !checkPassword(s.authHashValue(), password) {
			locked := s.loginLim.recordFailure(ipKey)
			if locked {
				s.logger.Warn("web: login lockout", "remote", r.RemoteAddr, "ip", ipKey, "cooldown", live.Web.LoginLockoutCooldown.Duration)
			} else {
				s.logger.Warn("web: failed login attempt", "remote", r.RemoteAddr)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			s.writeJSON(r, w, map[string]string{"error": "incorrect password"})
			return
		}

		s.loginLim.recordSuccess(ipKey)
		tok, err := s.sessions.create()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		csrf, _ := s.sessions.csrfFor(tok)
		setSessionCookie(w, tok, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
		setCSRFCookie(w, csrf, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
		s.logger.Info("web: login successful", "remote", r.RemoteAddr)
		// `csrf_token` is also exposed in the response JSON so a
		// frontend that doesn't read cookies (curl-based tooling,
		// E2E test harness) can pick it up directly. Production JS
		// reads from the `ventd_csrf` cookie via the fetch monkey-
		// patch in web/shared/brand.js.
		s.writeJSON(r, w, map[string]string{"status": "ok", "csrf_token": csrf})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFirstBootLogin processes the new-password submission on the
// first-boot wizard. Issue #765 eliminated the setup-token gate: when
// no admin password is configured yet, any LAN client hitting POST
// /login can complete the wizard. Once the password is set, normal
// auth applies — every subsequent request must present the session
// cookie obtained via /login.
func (s *Server) handleFirstBootLogin(w http.ResponseWriter, r *http.Request, live *config.Config, ipKey string) {
	newPassword := r.FormValue("new_password")

	if len(newPassword) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(r, w, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Persist the password hash. When authPath is set (production) the hash
	// goes exclusively to auth.json and never touches config.yaml, so no
	// config-write path can accidentally overwrite it. The legacy path
	// (authPath == "") is retained for test harnesses that pre-set the hash
	// on the config atomic pointer and do not use auth.json.
	if s.authPath != "" {
		if err := s.storeAuthHash(hash); err != nil {
			s.logger.Error("web: failed to persist password hash to auth.json", "err", err)
			http.Error(w, "could not save password", http.StatusInternalServerError)
			return
		}
	} else {
		// Legacy path: write to config.yaml (tests / no authPath).
		live.Web.PasswordHash = hash
		if len(live.Controls) > 0 {
			if _, err := config.Save(live, s.configPath); err != nil {
				s.logger.Error("web: failed to persist password hash", "err", err)
				http.Error(w, "could not save password", http.StatusInternalServerError)
				return
			}
		} else {
			if err := s.writePasswordHash(hash); err != nil {
				s.logger.Error("web: failed to persist password hash", "err", err)
				http.Error(w, "could not save password", http.StatusInternalServerError)
				return
			}
		}
		s.liveHash.Store(&hash)
	}

	s.loginLim.recordSuccess(ipKey)
	s.logger.Info("web: first-boot password set", "remote", r.RemoteAddr)

	tok, err := s.sessions.create()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf, _ := s.sessions.csrfFor(tok)
	setSessionCookie(w, tok, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
	setCSRFCookie(w, csrf, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
	s.writeJSON(r, w, map[string]string{"status": "ok", "csrf_token": csrf})
}

// writePasswordHash writes a minimal config file containing just the password
// hash. Used only in the legacy code path (authPath == "") for backward
// compatibility with tests. In production authPath is always set and auth
// is written to auth.json via storeAuthHash.
func (s *Server) writePasswordHash(hash string) error {
	return config.SavePasswordHash(hash, s.configPath)
}

// authHashValue returns the current in-memory bcrypt hash.
// It reads from s.liveHash first; if that is empty it falls back to
// s.cfg.Load().Web.PasswordHash so tests that set the hash directly on the
// config atomic pointer continue to work without an auth.json on disk.
func (s *Server) authHashValue() string {
	if p := s.liveHash.Load(); p != nil && *p != "" {
		return *p
	}
	return s.cfg.Load().Web.PasswordHash
}

// storeAuthHash persists hash to auth.json (when s.authPath is non-empty)
// and updates the in-memory liveHash pointer. It is the single write path
// for admin credentials in production.
func (s *Server) storeAuthHash(hash string) error {
	if s.authPath != "" {
		a := &authpersist.Auth{
			Admin: authpersist.AdminCreds{
				Username:   "admin",
				BcryptHash: hash,
				CreatedAt:  time.Now(),
			},
		}
		if err := authpersist.Save(s.authPath, a); err != nil {
			return err
		}
	}
	s.liveHash.Store(&hash)
	return nil
}

// handleLogout clears the session cookie and destroys the server-side session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	tok := sessionToken(r)
	if tok != "" {
		s.sessions.delete(tok)
	}
	clearSessionCookie(w)
	clearCSRFCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ListenAndServe starts the HTTP(S) listener.
//
// The `!= http.ErrServerClosed` comparison relies on stdlib returning
// that sentinel by identity, not wrapped. Today this holds — if it
// ever changes, switch to `errors.Is(err, http.ErrServerClosed)`. The
// fmt.Errorf wrap that follows is reachable only when err is non-nil
// and not ErrServerClosed; stdlib always returns non-nil from
// ListenAndServe, so we never wrap nil here.
func (s *Server) ListenAndServe(addr, tlsCert, tlsKey string) error {
	s.tlsActive = tlsCert != "" && tlsKey != ""
	s.httpSrv.Addr = addr
	if s.tlsActive {
		s.logger.Info("web: server listening (TLS)", "addr", "https://"+addr)
		// Bind the socket ourselves so we can wrap the listener in a
		// TLS-sniffing shim that 301-redirects plaintext HTTP requests
		// to the https:// equivalent. See #200 — without this, a
		// browser that autocompletes `host:9999` to `http://` hits
		// stdlib's "client sent an HTTP request to an HTTPS server"
		// and the operator has no idea what went wrong.
		rawListener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("web: listen tls: %w", err)
		}
		sniff := newTLSSniffListener(rawListener, addr, s.logger)
		if err := s.httpSrv.ServeTLS(sniff, tlsCert, tlsKey); err != http.ErrServerClosed {
			return fmt.Errorf("web: serve tls: %w", err)
		}
		return nil
	}
	s.logger.Info("web: server listening", "addr", "http://"+addr)
	if err := s.httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("web: serve: %w", err)
	}
	return nil
}

// triggerReload signals the main loop to reload config in-process (#466).
// The main loop handles the signal without exiting the daemon: it re-reads the
// config file, swaps liveCfg atomically, and starts controllers if needed.
// Buffered to one so callers never block; duplicate signals are dropped.
func (s *Server) triggerReload() {
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
}

// Shutdown gracefully drains in-flight requests and closes the listening
// socket. Safe to call before ListenAndServe has been invoked: the
// http.Server is constructed at New-time and Shutdown on an unstarted
// server is a no-op at the net/http level.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(ctx)
}

type statusResponse struct {
	Timestamp time.Time      `json:"timestamp"`
	Sensors   []sensorStatus `json:"sensors"`
	Fans      []fanStatus    `json:"fans"`
}

type sensorStatus struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value"` // null when the reading is a sentinel or implausible
	Unit  string   `json:"unit"`
}

type fanStatus struct {
	Name string  `json:"name"`
	PWM  uint8   `json:"pwm"`
	Duty float64 `json:"duty_pct"`
	RPM  *int    `json:"rpm"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, s.buildStatus())
}

// buildStatus snapshots sensor values and fan PWM/RPM for the live
// config. Shared between the /api/status one-shot handler and the
// /api/events SSE loop so both endpoints see the same payload shape.
// Errors from hwmon or NVML reads are logged but not surfaced — a
// missing sensor appears as value=0 rather than breaking the whole
// response, same behaviour as the original handler.
func (s *Server) buildStatus() statusResponse {
	live := s.cfg.Load()

	resp := statusResponse{
		Timestamp: time.Now().UTC(),
		Sensors:   make([]sensorStatus, 0, len(live.Sensors)),
		Fans:      make([]fanStatus, 0, len(live.Fans)),
	}

	for _, sensor := range live.Sensors {
		var val float64
		var err error
		switch sensor.Type {
		case "nvidia":
			idx, _ := strconv.ParseUint(sensor.Path, 10, 32)
			val, err = nvidia.ReadMetric(uint(idx), sensor.Metric)
		default:
			val, err = hwmon.ReadValue(sensor.Path)
		}
		ss := sensorStatus{
			Name: sensor.Name,
			Unit: sensorUnit(sensor),
		}
		if err != nil {
			s.logger.Warn("web: sensor read failed", "sensor", sensor.Name, "err", err)
			// Value remains nil → JSON null → UI renders "—"
		} else if sensor.Type != "nvidia" && halhwmon.IsSentinelSensorVal(sensor.Path, val) {
			s.logger.Warn("web: sensor returned sentinel value, suppressing from UI",
				"sensor", sensor.Name, "path", sensor.Path, "value", val)
			// Value remains nil → JSON null → UI renders "—"
		} else {
			ss.Value = &val
		}
		resp.Sensors = append(resp.Sensors, ss)
	}

	for _, fan := range live.Fans {
		var pwm uint8
		var rpm int
		var rpmKnown bool
		var err error
		switch fan.Type {
		case "nvidia":
			idx, _ := strconv.ParseUint(fan.PWMPath, 10, 32)
			pwm, err = nvidia.ReadFanSpeed(uint(idx))
		case "hwmon", "":
			pwm, err = hwmon.ReadPWM(fan.PWMPath)
			// Hwmon RPM is read separately below for backwards compat
			// with chips that expose tach on a different path than pwm.
		default:
			// Non-hwmon HAL backends (msiec, thinkpad, ipmi, nbfc,
			// crosec, asahi, pwmsys, legion, corsair). Defer to the
			// backend's Read so the dashboard reflects whatever each
			// backend reports — for msi-ec that's the centred-PWM
			// equivalent of the current fan_mode; for thinkpad it's
			// the procfs level mapped to PWM; backends that don't
			// expose RPM (msi-ec, NBFC EC bus) report Reading.RPM=0
			// and the dashboard renders "—" rather than fabricating
			// a number.
			if be, ok := hal.Backend(fan.Type); ok {
				chs, enumErr := be.Enumerate(context.Background())
				if enumErr != nil {
					err = fmt.Errorf("hal: enumerate %s: %w", fan.Type, enumErr)
				} else {
					found := false
					for _, ch := range chs {
						if ch.ID == fan.PWMPath {
							r, readErr := be.Read(ch)
							if readErr != nil {
								err = fmt.Errorf("hal: read %s: %w", fan.Type, readErr)
							} else if r.OK {
								pwm = r.PWM
								rpm = int(r.RPM)
								rpmKnown = r.RPM > 0
							}
							found = true
							break
						}
					}
					if !found {
						err = fmt.Errorf("hal: %s channel %q not enumerated (driver loaded?)", fan.Type, fan.PWMPath)
					}
				}
			} else {
				err = fmt.Errorf("hal: backend %q not registered", fan.Type)
			}
		}
		if err != nil {
			s.logger.Warn("web: fan read failed", "fan", fan.Name, "err", err)
		}
		fs := fanStatus{
			Name: fan.Name,
			PWM:  pwm,
			Duty: float64(pwm) / 255.0 * 100.0,
		}
		switch fan.Type {
		case "nvidia":
			// nvidia exposes RPM via the same nvml call as fan speed; no
			// separate read needed and nvml-driven channels don't have an
			// RPM-tach sysfs path.
		case "hwmon", "":
			var rpmErr error
			var hrpm int
			if fan.RPMPath != "" {
				hrpm, rpmErr = hwmon.ReadRPMPath(fan.RPMPath)
			} else {
				hrpm, rpmErr = hwmon.ReadRPM(fan.PWMPath)
			}
			// Reject sentinel RPM values — they must not appear in the UI
			// as if the fan is spinning at 65535 RPM.
			if rpmErr == nil && !halhwmon.IsSentinelRPM(hrpm) {
				fs.RPM = &hrpm
			}
		default:
			// HAL-backend RPM was already populated above (or left 0 when
			// the backend doesn't measure RPM — e.g. msi-ec reports a
			// percentage we deliberately don't fabricate as RPM).
			if rpmKnown {
				fs.RPM = &rpm
			}
		}
		resp.Fans = append(resp.Fans, fs)
	}

	return resp
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Cache-Control", "no-store")
		s.writeJSON(r, w, s.cfg.Load())
	case http.MethodPut:
		s.handleConfigPut(w, r)
	case http.MethodPatch:
		s.handleConfigPatch(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	// 1 MiB is well above any realistic ventd config (fan/sensor lists plus
	// curves are measured in KB). Anything beyond that is pathological.
	limitBody(w, r, defaultMaxBody)
	var incoming config.Config
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "config too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Auth credentials live in auth.json; never persist them via the config
	// write path, even if the client sent a password_hash field.
	incoming.Web.PasswordHash = ""

	validated, err := config.Save(&incoming, s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.cfg.Store(validated)
	s.logger.Info("config updated via web UI", "controls", len(validated.Controls))

	// Return the validated config so the UI can rehydrate the form from the
	// server's canonical state (#483: prevents stale field values after apply).
	s.writeJSON(r, w, validated)
}

// sensorUnit returns the display unit for a configured sensor.
func sensorUnit(s config.Sensor) string {
	if s.Type == "nvidia" {
		switch s.Metric {
		case "", "temp":
			return "°C"
		case "util", "mem_util", "fan_pct":
			return "%"
		case "power":
			return "W"
		case "clock_gpu", "clock_mem":
			return "MHz"
		}
	}
	// hwmon: derive from sysfs path filename
	base := filepath.Base(s.Path)
	switch {
	case strings.HasPrefix(base, "temp"):
		return "°C"
	case strings.HasPrefix(base, "in"):
		return "V"
	case strings.HasPrefix(base, "power"):
		return "W"
	case strings.HasPrefix(base, "fan"):
		return "RPM"
	}
	return ""
}

func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, monitor.Scan())
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	// Only the root document is served from this handler. The mux pattern
	// "/" matches every unclaimed path, so we have to guard against an
	// authenticated user typo-navigating to /foo and getting the dashboard
	// HTML for a path that shouldn't exist.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// Pick the right destination by setup state and serve a minimal
	// HTML page that meta-refreshes there. Returning 200 + HTML rather
	// than a 302 keeps the security/cache invariants identical to the
	// pre-redesign /, while still routing the user to the correct page.
	//   no admin password set         → /setup       (first-boot wizard)
	//   setup wizard pending          → /calibration (probe + apply)
	//   wizard applied + no controls  → /health      (monitor-only outcome — #784)
	//   otherwise                     → /dashboard
	dest := "/dashboard"
	if s.authHashValue() == "" {
		dest = "/setup"
	} else if s.setup != nil {
		p := s.setup.ProgressNeeded(s.cfg.Load())
		switch {
		case p.Needed && !p.Applied:
			dest = "/calibration"
		case p.Applied && len(s.cfg.Load().Controls) == 0:
			// Monitor-only outcome: wizard ran, persisted the
			// applied marker, but the probe found zero controllable
			// fan channels (BIOS-locked PWMs / EC-locked laptop /
			// BMC-managed server / mini-PC class). The dashboard's
			// control affordances are mostly empty for these hosts;
			// /health is the right default landing page (#793, #784).
			dest = "/health"
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// meta http-equiv="refresh" performs the redirect without JS; an
	// inline <script>window.location.replace(...)</script> here would
	// be blocked by the page's own CSP (script-src 'self', no
	// 'unsafe-inline') and surface as a console-level CSP violation
	// on every navigation through /. The visible <a> is the no-JS,
	// no-meta-refresh fallback for the rare client that disables both.
	body := `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` + dest + `"><title>ventd</title></head><body><a href="` + dest + `">Continue to ` + dest + `</a></body></html>`
	_, _ = w.Write([]byte(body))
}

// handleCalibrateStart POST /api/calibrate/start?fan=<pwmPath>
func (s *Server) handleCalibrateStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pwmPath := r.URL.Query().Get("fan")
	if pwmPath == "" {
		http.Error(w, "fan query param required", http.StatusBadRequest)
		return
	}
	live := s.cfg.Load()
	var fan *config.Fan
	for i := range live.Fans {
		if live.Fans[i].PWMPath == pwmPath {
			fan = &live.Fans[i]
			break
		}
	}
	if fan == nil {
		http.Error(w, "fan not found", http.StatusNotFound)
		return
	}
	if err := s.cal.Start(fan); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeJSON(r, w, map[string]string{"status": "started"})
}

// handleCalibrateStatus GET /api/calibrate/status
func (s *Server) handleCalibrateStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, s.cal.AllStatus())
}

// handleOpportunisticStatus GET /api/probe/opportunistic/status (v0.5.5).
// Returns the live status of the opportunistic-probe scheduler so the
// dashboard can render a probe-in-flight pill. When the scheduler has
// not been wired (e.g., monitor-only mode with no controllable
// channels), responds with running=false and a stable empty struct so
// the frontend never sees a 404.
func (s *Server) handleOpportunisticStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opp == nil {
		s.writeJSON(r, w, opportunistic.Status{Running: false})
		return
	}
	s.writeJSON(r, w, s.opp.Status())
}

// SetOpportunisticScheduler wires the v0.5.5 scheduler so the live
// status endpoint can report on probe-in-flight state. Called by
// cmd/ventd/main.go after the scheduler is constructed and launched.
// Safe to call once at daemon start; subsequent calls overwrite.
func (s *Server) SetOpportunisticScheduler(sched *opportunistic.Scheduler) {
	s.opp = sched
}

// SetSmartRuntimes wires the v0.5.7 coupling and v0.5.8 marginal
// runtimes so /api/v1/smart/{status,channels} can report per-channel
// RLS state. Either may be nil (monitor-only / disabled). Safe to
// call once at daemon start; subsequent calls overwrite.
func (s *Server) SetSmartRuntimes(c *coupling.Runtime, m *marginal.Runtime) {
	s.couplingRT = c
	s.marginalRT = m
}

// SetConfidence wires the v0.5.9 aggregator + LayerA estimator so the
// /api/v1/confidence/status endpoint can return per-channel snapshots.
// Both arguments are nil-safe: monitor-only mode skips construction.
func (s *Server) SetConfidence(agg *aggregator.Aggregator, est *layer_a.Estimator) {
	s.aggregator = agg
	s.layerA = est
}

// SetDecisions wires the controller's per-channel BlendedResult cache
// so /api/v1/smart/channels can show the next-tick PWM target +
// refusal flags + predicted dBA alongside the upstream RLS state.
// nil-safe (monitor-only mode skips wiring); safe to call once at
// daemon start. (#790)
func (s *Server) SetDecisions(d *controller.DecisionCache) {
	s.decisions = d
}

// confidenceChannel is the JSON wire shape for one channel's

// handleCalibrateResults GET /api/calibrate/results
func (s *Server) handleCalibrateResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, s.cal.AllResults())
}

// handleCalibrateAbort POST /api/calibrate/abort?fan=<pwmPath>
// Idempotent: returns 204 whether or not a calibration is currently in flight
// for the given fan. The runSync defer is responsible for restoring PWM.
func (s *Server) handleCalibrateAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pwmPath := r.URL.Query().Get("fan")
	if pwmPath == "" {
		http.Error(w, "fan query param required", http.StatusBadRequest)
		return
	}
	s.cal.Abort(pwmPath)
	w.WriteHeader(http.StatusNoContent)
}

// handleSetupCalibrateAbort POST /api/setup/calibrate/abort
// Idempotent: cancels the setup wizard's current run (including its parallel
// per-fan calibration sweeps). Returns 204 whether or not setup is active.
func (s *Server) handleSetupCalibrateAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.setup.Abort()
	w.WriteHeader(http.StatusNoContent)
}

// handleSetupStatus GET /api/setup/status
// Returns the current setup wizard progress, or needed=false if setup is done.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p := s.setup.ProgressNeeded(s.cfg.Load())
	s.writeJSON(r, w, p)
}

// handleSetupStart POST /api/setup/start
// Kicks off the setup goroutine. 409 if already running.
func (s *Server) handleSetupStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.setup.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeJSON(r, w, map[string]string{"status": "started"})
}

// handleSetupApply POST /api/setup/apply
// Writes the generated config to disk. Does NOT update liveCfg — the user
// must restart the daemon so main.go can re-initialise NVML correctly.
//
// Empty-fanset escape: if the wizard finished but produced no generated
// config because no controllable fans were discoverable (VM, headless
// container, motherboard whose chip needs manual driver work, etc.),
// fall back to config.Empty() so the operator can opt into monitor-only
// mode without being permanently trapped on /calibration. Setup state
// is marked applied (with a persistent marker file) so the next daemon
// restart goes straight to the dashboard.
func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.setup.GeneratedConfig()
	monitorOnly := false
	if cfg == nil {
		p := s.setup.Progress()
		if p.Done && p.Error != "" && len(p.Fans) == 0 {
			// Preserve the live web settings (listen address, TLS paths,
			// session TTL, trust_proxy) so the operator who is mid-setup
			// over a LAN browser doesn't have the daemon silently
			// downgrade to 127.0.0.1 because config.Empty() defaults to
			// loopback. Everything else (sensors/fans/curves/controls)
			// comes back empty — that is the monitor-only intent.
			cfg = config.Empty()
			if live := s.cfg.Load(); live != nil {
				cfg.Web = live.Web
			}
			monitorOnly = true
		} else {
			http.Error(w, "setup not complete", http.StatusConflict)
			return
		}
	}

	// Auth credentials live in auth.json (written by handleFirstBootLogin /
	// handleSetPassword) and are not part of the fan-control config. The
	// generated config intentionally has no password_hash field.

	// Ensure the config directory exists. 0700 so only the daemon's user
	// can read credentials stored inside.
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0700); err != nil {
		http.Error(w, "create config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := config.Save(cfg, s.configPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.setup.MarkApplied()
	if monitorOnly {
		s.logger.Info("setup: monitor-only mode applied (no controllable fans found)",
			"path", s.configPath)
	} else {
		s.logger.Info("setup: config written via web UI", "path", s.configPath,
			"fans", len(cfg.Fans), "controls", len(cfg.Controls))
	}

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok"})
	s.triggerReload()
}

// handleSetupApplyMonitorOnly POST /api/setup/apply-monitor-only
//
// Writes a monitor-only config to disk regardless of wizard state and
// marks setup as applied. Used by the vendor-daemon recovery card
// (RULE-WIZARD-RECOVERY-11 / ClassVendorDaemonActive): when ventd
// detects an active vendor fan daemon (System76, ASUS ROG, Tuxedo,
// Slimbook), the operator clicks "Switch ventd to monitor-only" and
// this endpoint drops into the same monitor-only mode that the
// empty-fanset escape in handleSetupApply produces — but without
// requiring the wizard to first run + fail.
//
// Mirrors handleSetupApply's monitorOnly path: preserves live web
// settings (listen, TLS, session TTL, trust_proxy) so a LAN-mid-setup
// browser doesn't get downgraded to loopback; everything else
// (sensors / fans / curves / controls) is empty by design — the
// vendor daemon owns those.
func (s *Server) handleSetupApplyMonitorOnly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := config.Empty()
	if live := s.cfg.Load(); live != nil {
		cfg.Web = live.Web
	}
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0700); err != nil {
		http.Error(w, "create config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := config.Save(cfg, s.configPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.setup.MarkApplied()
	s.logger.Info("setup: monitor-only mode applied via vendor-daemon deferral",
		"path", s.configPath)

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok", "mode": "monitor_only"})
	s.triggerReload()
}

// handleSetupReset POST /api/setup/reset
// Deletes the config file and triggers a daemon restart, returning to first-boot mode.
//
// Issue #1063 (web H2): order is KV-wipe FIRST, then config delete. The
// previous order left the config wiped while stale wizard / probe /
// calibration KV state persisted on disk — on next start the daemon
// hit the wizard with stale outcomes the operator just tried to clear.
// If kvWiper fails we now return 500 with the error so the operator
// knows the reset is half-done rather than seeing a misleading "ok".
func (s *Server) handleSetupReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 1. Wipe the wizard and probe KV namespaces FIRST so RULE-PROBE-09
	// is honoured even if the subsequent config delete fails.
	if s.kvWiper != nil {
		if err := s.kvWiper(); err != nil {
			s.logger.Error("setup reset: kv wipe failed; config NOT removed",
				"err", err)
			http.Error(w, "kv wipe failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// 2. Now remove the config file. A failure here leaves us with
	// stale config + wiped KV — the next daemon start re-runs the
	// wizard cleanly (config-empty path), so this is the recoverable
	// failure ordering.
	if err := os.Remove(s.configPath); err != nil && !os.IsNotExist(err) {
		http.Error(w, "remove config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Clear the persistent applied-marker so a host that opted into
	// monitor-only via handleSetupApply's empty-fanset escape goes back
	// to the wizard on next start (otherwise the marker would suppress
	// the wizard even though config.yaml was just removed).
	if err := s.setup.ClearApplied(); err != nil {
		s.logger.Warn("setup reset: clear applied marker failed", "err", err)
	}
	// 3. v0.8.x: wipe /var/lib/ventd/setup/ — the canonical orchestrator
	// state dir holding calibration.json + checkpoint state.json. Failure
	// is logged but not fatal; the directory is recreated by the next
	// daemon start so this is a self-healing path. (Goal 3 of the
	// v0.8.x wizard rework: no stale state carried into the fresh wizard.)
	if err := wipeOrchestratorStateDir(s.logger); err != nil {
		s.logger.Warn("setup reset: orchestrator state dir wipe failed", "err", err)
	}
	s.logger.Info("setup: config removed; triggering reload (daemon continues until next restart)", "path", s.configPath)

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok"})
	s.triggerReload()
}

// handleFactoryReset POST /api/admin/factory-reset
//
// Like handleSetupReset, but ALSO removes auth.json so the next
// daemon start lands on /login's password-set form instead of
// the password-prompt form. Use this when transferring a host to
// a new operator or when restoring to true factory state.
//
// Wipes (in order):
//  1. The config file (s.configPath) — same as setup/reset.
//  2. The wizard / probe / calibration KV namespaces (via kvWiper).
//  3. The applied-marker so the wizard runs again on next start.
//  4. auth.json (s.authPath) — the password hash. Daemon's next
//     start sees no credentials and renders the password-set form
//     at /login per the v0.5.8.1 first-boot flow (#765, #794).
//
// The response includes redirect: "/login" so the UI can navigate
// the operator after the daemon reload — distinct from
// handleSetupReset's redirect: "/setup".
//
// DOES NOT wipe:
//   - Installed OOT driver under /lib/modules/<rel>/extra/ (use the
//     reset-and-reinstall endpoint).
//   - /etc/modprobe.d/ventd-*.conf (driver-cleanup endpoint owns those).
//   - /var/lib/ventd/blob/, /var/lib/ventd/log/ (durable telemetry
//     persists across factory reset by design — operators can clear
//     manually if desired).
//   - The signature salt (regenerates on next start anyway).
func (s *Server) handleFactoryReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Issue #1063 (web H2): KV wipe FIRST, then config delete. A kvWiper
	// failure now returns 500 so the operator knows the reset is
	// half-done; the previous order left the config wiped while stale
	// wizard / probe / polarity records persisted on disk.
	if s.kvWiper != nil {
		if err := s.kvWiper(); err != nil {
			s.logger.Error("factory reset: kv wipe failed; config + auth NOT removed",
				"err", err)
			http.Error(w, "kv wipe failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Config file.
	if err := os.Remove(s.configPath); err != nil && !os.IsNotExist(err) {
		http.Error(w, "remove config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 3. Applied marker.
	if err := s.setup.ClearApplied(); err != nil {
		s.logger.Warn("factory reset: clear applied marker failed", "err", err)
	}
	// 4. auth.json — the differentiator from setup/reset.
	if s.authPath != "" {
		if err := os.Remove(s.authPath); err != nil && !os.IsNotExist(err) {
			s.logger.Warn("factory reset: remove auth.json failed (continuing)", "err", err, "path", s.authPath)
		}
	}
	// 5. v0.8.x: wipe /var/lib/ventd/setup/ — same as handleSetupReset
	// step 3. Goal 3 of the wizard rework: stale state never carried
	// forward into the fresh wizard.
	if err := wipeOrchestratorStateDir(s.logger); err != nil {
		s.logger.Warn("factory reset: orchestrator state dir wipe failed", "err", err)
	}
	s.logger.Info("factory reset: full state wipe complete; triggering reload", "config", s.configPath, "auth", s.authPath)

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{
		"status":   "ok",
		"redirect": "/login",
	})
	s.triggerReload()
}

// handleSetupLoadModule POST /api/setup/load-module
// Loads a kernel module from the fixed ventd allowlist (coretemp, k10temp,
// nct6683, nct6687, it87, drivetemp) and persists it to
// /etc/modules-load.d/ventd-<name>.conf so it survives reboot. Surfaced via
// the setup wizard's remediation cards when a required driver isn't present.
//
// Request body: {"module": "coretemp"}. Response: installLogResponse.
// Rejects anything outside the allowlist or that doesn't match the kernel's
// module-name charset (a-z / 0-9 / _, 1-32 chars) before spawning modprobe.
//
// Deliberately does NOT re-run the setup wizard's scan phase — per the
// "client re-request" design the operator clicks "Re-run Setup" explicitly
// when they want new sensors folded into the generated config. The returned
// success clears the matching hwdiag entries so the UI's polling loop
// reflects the remediated state on its next tick.
func (s *Server) handleSetupLoadModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limitBody(w, r, 256)
	var req struct {
		Module string `json:"module"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Module == "" {
		http.Error(w, "module is required", http.StatusBadRequest)
		return
	}
	if !setupmgr.AllowedModule(req.Module) {
		// Don't leak the allowlist contents — just say no. The wizard's UI
		// only ever sends modules from hwdiag entries we emitted ourselves,
		// so hitting this path implies a hand-crafted request.
		http.Error(w, "module not in remediation allowlist", http.StatusBadRequest)
		return
	}

	log, err := s.setup.LoadModule(r.Context(), req.Module)
	resp := installLogResponse{Kind: "install_log", Log: log, Success: err == nil}
	if err != nil {
		resp.Error = err.Error()
		// Keep a 200 on allowlisted-but-failed loads so the UI renders the
		// log output and error string inline (matches install-kernel-headers
		// behaviour). Bad input still returns 4xx above.
	}
	s.writeJSON(r, w, resp)
}

// rebootEnvironmentBlocker returns a human-readable reason string when
// the current runtime environment cannot safely reboot the host — or ""
// when reboot is appropriate. Three signals are checked:
//   - os.Getpid() == 1: ventd is the init process (container)
//   - /.dockerenv exists: Docker marker file
//   - systemd-detect-virt --container returns non-"none"
//
// Any one hit is sufficient. Kept as a free function so tests can exercise
// it without standing up a Server.
func rebootEnvironmentBlocker() string {
	if os.Getpid() == 1 {
		return "ventd is running as PID 1 (container init)"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "Docker container detected (/.dockerenv present)"
	}
	// systemd-detect-virt prints the container type to stdout and exits 0,
	// or prints "none" and exits 1. Either "none" output or a non-zero exit
	// means "not a container" for our purposes.
	if out, err := exec.Command("systemd-detect-virt", "--container").Output(); err == nil {
		kind := strings.TrimSpace(string(out))
		if kind != "" && kind != "none" {
			return "container runtime detected: " + kind
		}
	}
	return ""
}

// handleSystemReboot POST /api/system/reboot
// Sends a response immediately then issues a system reboot.
// Used after the setup wizard auto-patches GRUB and needs a reboot to continue.
//
// Refuses with 409 Conflict when:
//   - The daemon is running in an environment where "reboot the host" isn't
//     the right action — containers, LXC, WSL, anywhere ventd is PID 1.
//   - At least one fan is pinned via a manual-override Control.ManualPWM
//     entry. Rebooting while an operator-pinned manual override is in flight
//     would silently discard that intent.
//
// Issue #1064 (web H3): the reboot goroutine now runs with defer recover()
// so a panic in exec.Command doesn't leave the daemon in an inconsistent
// state with no diagnostic surface; it calls the watchdog Restore hook with
// RULE-WD-RESTORE-BUDGET (1.8s) before invoking systemctl so fans are
// handed back to BIOS auto before the kernel-init sequence kicks in.
func (s *Server) handleSystemReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	blocker := s.rebootBlocker
	if blocker == nil {
		blocker = rebootEnvironmentBlocker
	}
	if reason := blocker(); reason != "" {
		s.logger.Warn("web: system reboot refused", "reason", reason)
		http.Error(w, "reboot not supported in this environment: "+reason, http.StatusConflict)
		return
	}
	// Refuse the reboot if the operator has any active manual-mode override
	// pinned in config. The wizard's auto-reboot path should never silently
	// discard an operator-pinned PWM value mid-flight; surface a 409 so the
	// UI can render an explanation.
	if names := activeManualOverrides(s.cfg.Load()); len(names) > 0 {
		reason := "active manual-mode override on fan(s): " + strings.Join(names, ", ")
		s.logger.Warn("web: system reboot refused", "reason", reason)
		http.Error(w, "reboot refused while manual override is active: "+reason, http.StatusConflict)
		return
	}
	s.logger.Info("web: system reboot requested via setup wizard")
	s.writeJSON(r, w, map[string]string{"status": "rebooting"})
	// Flush the response before rebooting so the browser receives it.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	restore := s.rebootRestore
	rebootFn := s.rebootExec
	if rebootFn == nil {
		rebootFn = runRebootCommands
	}
	go func() {
		// Issue #1064: recover any panic inside the reboot goroutine.
		// The HTTP handler has already replied "rebooting"; without this
		// recover an exec.Command panic would kill the goroutine while
		// the daemon's other state pointed at "reboot in flight".
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("web: reboot goroutine panicked; daemon survives",
					"panic", rec)
			}
		}()
		// Ctx-aware delay so daemon shutdown isn't blocked by a pending reboot dispatch.
		select {
		case <-time.After(300 * time.Millisecond):
		case <-s.ctx.Done():
			return
		}
		// Pre-reboot Restore: hand every controlled channel back to BIOS
		// auto BEFORE invoking systemctl, with the RULE-WD-RESTORE-BUDGET
		// 1.8s budget so a wedged sysfs write on one fan doesn't stall the
		// reboot indefinitely. Nil-safe in tests + legacy callers.
		if restore != nil {
			rctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
			restore(rctx)
			cancel()
		}
		rebootFn()
	}()
}

// runRebootCommands is the production reboot-exec hook. Tries systemd
// first, then the generic `reboot` binary; both go through a proper
// shutdown sequence that syncs filesystems.
func runRebootCommands() {
	for _, cmd := range [][]string{
		{"systemctl", "reboot"},
		{"reboot"},
	} {
		if err := execCmdRun(cmd[0], cmd[1:]...); err == nil {
			return
		}
	}
}

// execCmdRun wraps exec.Command(...).Run() so tests can intercept the
// command-spawn behaviour without monkey-patching the stdlib. Production
// uses exec.Command directly.
var execCmdRun = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// activeManualOverrides returns the names of fans pinned to a manual PWM
// value via Control.ManualPWM in the live config. Used by the reboot
// path to refuse reboots that would silently discard an operator's
// manual-mode intent.
func activeManualOverrides(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	for _, ctrl := range cfg.Controls {
		if ctrl.ManualPWM != nil {
			names = append(names, ctrl.Fan)
		}
	}
	return names
}

// handleDetectRPM POST /api/detect-rpm?fan=<pwmPath>
// Blocks ~5s while it ramps PWM and identifies the correlated fan*_input sensor.
func (s *Server) handleDetectRPM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pwmPath := r.URL.Query().Get("fan")
	if pwmPath == "" {
		http.Error(w, "fan query param required", http.StatusBadRequest)
		return
	}
	live := s.cfg.Load()
	var fan *config.Fan
	for i := range live.Fans {
		if live.Fans[i].PWMPath == pwmPath {
			fan = &live.Fans[i]
			break
		}
	}
	if fan == nil {
		http.Error(w, "fan not found", http.StatusNotFound)
		return
	}
	result, err := s.cal.DetectRPMSensor(fan)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(r, w, result)
}

// handleHwdiag GET /api/hwdiag
// Returns the current hardware-diagnostic set as {generated_at, revision,
// entries: [...]}. Optional query params ?component=<name>&severity=<level>
// filter the entries. The revision is a monotonic counter the UI can poll
// cheaply to decide whether to re-render.
func (s *Server) handleHwdiag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f := hwdiag.Filter{
		Component: hwdiag.Component(r.URL.Query().Get("component")),
		Severity:  hwdiag.Severity(r.URL.Query().Get("severity")),
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, s.diag.Snapshot(f))
}

// installLogResponse is the shape returned by install-kernel-headers and
// install-dkms. Kind discriminates it from instructionsResponse so the UI
// can switch rendering without inspecting other fields.
type installLogResponse struct {
	Kind          string   `json:"kind"` // always "install_log"
	Success       bool     `json:"success"`
	Log           []string `json:"log"`
	Error         string   `json:"error,omitempty"`
	RebootNeeded  bool     `json:"reboot_needed,omitempty"`
	RebootMessage string   `json:"reboot_message,omitempty"`
}

// validateInstallRequest enforces method + body shape on every install
// handler routed through runInstallHandler. Issue #1062 (web H1): the
// previous shape accepted any non-empty body without validating
// Content-Type or rejecting unknown fields, so a malformed JSON payload
// silently fell through to defaults (typically guessInstalledOOTModule
// for reset/reinstall, or the canonical acpi_enforce_resources=lax for
// grub-cmdline-add). The operator UI showed success even though the
// payload it sent was discarded.
//
// Now: when the request carries a body, Content-Type MUST start with
// "application/json" (parameters like charset are allowed) and dst (if
// non-nil) MUST decode cleanly with DisallowUnknownFields. Any failure
// returns a 400 to the caller — no silent fall-through.
//
// dst is optional: handlers that don't accept a body pass nil and an
// empty / missing body is accepted. The body cap is enforced upstream by
// requireMaxBody (RULE-WEB-BODY-SIZE-CAP-1MIB).
func validateInstallRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if r.Body == nil || r.ContentLength == 0 {
		// No body — handlers that accept an optional payload fall
		// through to their default values.
		return true
	}
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if ct == "" {
		http.Error(w, "Content-Type required when body is present", http.StatusBadRequest)
		return false
	}
	if mediaType := strings.SplitN(ct, ";", 2)[0]; strings.TrimSpace(mediaType) != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusBadRequest)
		return false
	}
	if dst == nil {
		// Handler doesn't accept any fields, but the caller sent a body.
		// Tolerate empty `{}`; refuse anything else as malformed input
		// rather than silently discarding it.
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var probe map[string]any
		if err := dec.Decode(&probe); err != nil {
			if isMaxBytesErr(err) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return false
			}
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return false
		}
		if len(probe) != 0 {
			http.Error(w, "this endpoint does not accept any body fields", http.StatusBadRequest)
			return false
		}
		return true
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// runInstallHandler is the common body for server-side package installs.
// It invokes fn with a logFn that appends to the returned log, formats the
// response consistently, and clears the corresponding hwdiag entry on success.
func (s *Server) runInstallHandler(w http.ResponseWriter, r *http.Request, clearID string, fn func(log func(string)) error) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var log []string
	logFn := func(line string) { log = append(log, line) }
	err := fn(logFn)

	resp := installLogResponse{Kind: "install_log", Log: log, Success: err == nil}
	if err != nil {
		resp.Error = err.Error()
	} else {
		s.diag.Remove(clearID)
	}
	s.writeJSON(r, w, resp)
}

// handleInstallKernelHeaders POST /api/hwdiag/install-kernel-headers
// Installs the distro's kernel-headers package for the running kernel.
// No body fields are accepted (issue #1062): a request that carries
// a body of any shape MUST present Content-Type: application/json AND
// MUST contain an empty JSON object — anything else returns 400.
func (s *Server) handleInstallKernelHeaders(w http.ResponseWriter, r *http.Request) {
	if !validateInstallRequest(w, r, nil) {
		return
	}
	s.runInstallHandler(w, r, hwdiag.IDOOTKernelHeadersMissing, hwmon.EnsureKernelHeaders)
}

// handleResetAndReinstall POST /api/hwdiag/reset-and-reinstall
//
// Clears stale OOT driver state — DKMS registration, .ko in
// /lib/modules/<rel>/extra, /etc/modules-load.d entry, build dirs
// under /tmp/ventd-driver-* — so a subsequent wizard run starts
// from a clean slate. Used as the recovery action when the
// wizard's first install attempt left half-finished state behind
// (DKMS state collision, module installed but binding failed,
// etc.). Without this endpoint, operators saw bundle-only
// remediation and had to clean up state by hand — the regression
// class Phoenix flagged on HIL as "ridiculous".
//
// Body: optional {"module": "it87"}; empty falls through to
// guessInstalledOOTModule which inspects /lib/modules/.../extra/.
//
// Issue #1062 (web H1): when a body is present it MUST be valid JSON
// with the documented `module` field; a malformed body returns 400
// rather than silently falling through to guessInstalledOOTModule.
func (s *Server) handleResetAndReinstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Module string `json:"module"`
	}
	if !validateInstallRequest(w, r, &body) {
		return
	}
	module := strings.TrimSpace(body.Module)
	s.runInstallHandler(w, r, "", func(logFn func(string)) error {
		if module == "" {
			module = guessInstalledOOTModule()
		}
		if module == "" {
			return fmt.Errorf("no installed OOT driver found to clean up under /lib/modules/<release>/extra/")
		}
		nd := hwmon.DriverNeed{Module: module}
		release := strings.TrimSpace(execOutput("uname", "-r"))
		logFn("Cleaning up prior driver state for module: " + module)
		report, err := hwmon.CleanupOrphanInstall(nd, release, s.logger)
		if err != nil {
			return err
		}
		for _, m := range report.ModulesRemoved {
			logFn("  ✓ removed module: " + m)
		}
		for _, d := range report.DKMSRemoved {
			logFn("  ✓ removed DKMS: " + d)
		}
		for _, p := range report.BuildDirsRemoved {
			logFn("  ✓ removed build dir: " + p)
		}
		if report.ModulesLoadDClean {
			logFn("  ✓ cleared /etc/modules-load.d entry")
		}
		for _, e := range report.NonFatalErrors {
			logFn("  ! non-fatal: " + e)
		}
		logFn("Cleanup complete. Re-run the wizard to trigger a fresh install.")
		return nil
	})
}

// guessInstalledOOTModule walks /lib/modules/<release>/extra/ and
// returns the first .ko basename, preferring "it87" when multiple
// are present. Returns "" when /extra/ is empty or inaccessible.
func guessInstalledOOTModule() string {
	release := strings.TrimSpace(execOutput("uname", "-r"))
	if release == "" {
		return ""
	}
	entries, err := os.ReadDir("/lib/modules/" + release + "/extra")
	if err != nil {
		return ""
	}
	var first string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".ko") {
			continue
		}
		mod := strings.TrimSuffix(name, ".ko")
		if mod == "it87" {
			return mod
		}
		if first == "" {
			first = mod
		}
	}
	return first
}

// execOutput runs cmd with args and returns trimmed stdout, or ""
// on error. Tiny helper for the reset-and-reinstall handler.
func execOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// handleGrubCmdlineAdd POST /api/hwdiag/grub-cmdline-add
//
// Adds a kernel command-line parameter to GRUB via a drop-in at
// /etc/default/grub.d/ventd-cmdline.cfg, then runs the per-distro
// bootloader-rebuild tool. Wired into the wizard-recovery
// classifier as the action_url for ClassACPIResourceConflict
// (issue #817) — the only param ventd currently writes is
// `acpi_enforce_resources=lax`, which unblocks the it87 / nct67xx
// drivers on MSI / ASUS Z690-class boards where BIOS reserves the
// SuperIO I/O region via ACPI.
//
// The handler accepts an optional JSON body {"param": "..."}; if
// absent, defaults to the canonical acpi_enforce_resources=lax.
// The grub package's validParam gate refuses anything containing
// shell-special chars so a future caller-controlled input source
// can't trigger shell injection in /etc/default/grub.
func (s *Server) handleGrubCmdlineAdd(w http.ResponseWriter, r *http.Request) {
	// Issue #1062 (web H1): validate body shape explicitly; a malformed
	// payload returns 400 rather than silently falling through to the
	// canonical acpi_enforce_resources=lax default.
	var body struct {
		Param string `json:"param"`
	}
	if !validateInstallRequest(w, r, &body) {
		return
	}
	param := strings.TrimSpace(body.Param)
	if param == "" {
		param = "acpi_enforce_resources=lax"
	}
	s.runInstallHandler(w, r, "", func(logFn func(string)) error {
		logFn("Writing GRUB drop-in to " + grub.DropInPath)
		logFn("  param: " + param)
		err := grub.AddParam(param, func() error {
			logFn("Rebuilding bootloader configuration...")
			if regenErr := grub.DefaultRegenerator(); regenErr != nil {
				logFn("Bootloader rebuild failed: " + regenErr.Error())
				return regenErr
			}
			logFn("Bootloader rebuild complete.")
			return nil
		})
		if err != nil {
			return err
		}
		logFn("Reboot to apply the new kernel parameter.")
		return nil
	})
}

// handleModprobeOptionsWrite POST /api/hwdiag/modprobe-options-write
//
// Writes a per-module modprobe drop-in to /etc/modprobe.d/ventd-<module>.conf
// in the form `options <module> <options>`, then reloads the module so the
// new option takes effect immediately. Wired into the wizard-recovery
// classifier as the action_url for ClassThinkpadACPIDisabled
// (RULE-WIZARD-RECOVERY-10): the classifier card asks the operator to
// flip thinkpad_acpi's fan_control parameter on, this endpoint applies it.
//
// Body: required JSON {"module": "...", "options": "..."}.
//
// The (module, options) pair is matched against
// `hwmon.IsAllowedModprobeOption` — a closed allowlist that today
// covers exactly thinkpad_acpi fan_control=1. Anything else returns
// 400. Future Stage-1 entries (it87 ignore_resource_conflict=1,
// it87 force_id=0xNNNN) extend the allowlist alongside their catalog
// rows in their own PRs.
//
// A reboot prompt is recommended in the log because some EC firmware
// re-arbitrates only on a full power cycle even when the kernel
// module reloads cleanly.
func (s *Server) handleModprobeOptionsWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Module  string `json:"module"`
		Options string `json:"options"`
	}
	if r.Body == nil || r.ContentLength <= 0 {
		http.Error(w, "request body required", http.StatusBadRequest)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !hwmon.IsAllowedModprobeOption(body.Module, body.Options) {
		http.Error(w, "module/options pair not in ventd's allowlist", http.StatusBadRequest)
		return
	}
	s.runInstallHandler(w, r, "", func(logFn func(string)) error {
		path := hwmon.ModprobeOptionsDropInPath(body.Module)
		logFn("Writing modprobe drop-in to " + path)
		logFn("  options: " + body.Options)
		if err := hwmon.WriteModprobeOptionsDropIn(path, body.Module, body.Options); err != nil {
			return err
		}
		logFn("Reloading kernel module " + body.Module + "...")
		if err := hwmon.ReloadModule(body.Module, logFn); err != nil {
			logFn("Reboot recommended; the option will be picked up on next boot.")
			return err
		}
		logFn("Drop-in applied. A reboot is still recommended so the EC re-arbitrates with the new option.")
		return nil
	})
}

// handleLoadAppArmor POST /api/hwdiag/load-apparmor
//
// Loads ventd's shipped AppArmor profile (`/etc/apparmor.d/ventd`)
// into the running kernel via `apparmor_parser -r`. Distros that
// enforce AppArmor at boot may not have parsed our profile yet —
// this endpoint wires it up so the wizard's helpers run unblocked.
//
// Wired into the v0.5.9 wizard-recovery classifier (#800) as the
// action_url for ClassApparmorDenied. Returns the same
// installLogResponse shape as the install endpoints so the UI can
// render the parser output uniformly.
func (s *Server) handleLoadAppArmor(w http.ResponseWriter, r *http.Request) {
	if !validateInstallRequest(w, r, nil) {
		return
	}
	s.runInstallHandler(w, r, "", loadAppArmorProfile)
}

// loadAppArmorProfile shells `apparmor_parser -r` against the
// shipped profile path. Returns nil on success; the parser's stdout
// + stderr are logged via logFn line-by-line for the response body.
//
// Failure modes:
//   - apparmor_parser binary missing: distro doesn't have apparmor
//     userspace installed. Surfaces a clear error to the operator.
//   - profile file missing: ventd install incomplete. Same.
//   - parser rejects the profile: shouldn't happen on a current
//     build (apparmor-parse-debian13 CI gates this), but possible
//     on a stale profile vs a newer parser.
func loadAppArmorProfile(log func(string)) error {
	const profilePath = "/etc/apparmor.d/ventd"
	if _, err := exec.LookPath("apparmor_parser"); err != nil {
		log("apparmor_parser binary not found in PATH")
		log("(install the apparmor / apparmor-utils package for your distro)")
		return fmt.Errorf("apparmor_parser missing: %w", err)
	}
	if _, err := os.Stat(profilePath); err != nil {
		log("profile file " + profilePath + " not found")
		log("(re-run the install — the profile ships in the .deb / .rpm)")
		return fmt.Errorf("stat %s: %w", profilePath, err)
	}
	cmd := exec.Command("sudo", "-n", "apparmor_parser", "-r", profilePath)
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			log(line)
		}
	}
	if err != nil {
		return fmt.Errorf("apparmor_parser -r %s: %w", profilePath, err)
	}
	log("OK — profile loaded")
	return nil
}

// handleInstallDKMS POST /api/hwdiag/install-dkms
// Installs the distro's dkms package so OOT modules survive kernel upgrades.
// Body is rejected (issue #1062): this endpoint takes no fields.
func (s *Server) handleInstallDKMS(w http.ResponseWriter, r *http.Request) {
	if !validateInstallRequest(w, r, nil) {
		return
	}
	s.runInstallHandler(w, r, hwdiag.IDOOTDKMSMissing, hwmon.EnsureDKMS)
}

// mokInstructionsResponse is the shape returned by mok-enroll — the user must
// run these commands themselves because MOK enrollment requires a reboot
// followed by interactive acknowledgement in the UEFI firmware.
type mokInstructionsResponse struct {
	Kind     string   `json:"kind"` // always "instructions"
	Commands []string `json:"commands"`
	Detail   string   `json:"detail"`
}

// handleMOKEnroll POST /api/hwdiag/mok-enroll
// Returns distro-specific commands plus a human-readable explanation. Does
// NOT execute anything server-side — MOK enrollment requires a reboot and
// interactive firmware step that cannot be automated.
func (s *Server) handleMOKEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	distro := hwmon.DetectDistro()
	cmds := []string{
		distro.MOKInstallCommand(),
		"sudo mkdir -p /var/lib/shim-signed/mok",
		"sudo openssl req -new -x509 -newkey rsa:2048 -keyout /var/lib/shim-signed/mok/MOK.priv -outform DER -out /var/lib/shim-signed/mok/MOK.der -days 36500 -nodes -subj \"/CN=ventd out-of-tree module signing/\"",
		"sudo mokutil --import /var/lib/shim-signed/mok/MOK.der",
		"# Set a one-time password when prompted.",
		"# Reboot, and at the blue MOK Manager screen choose \"Enroll MOK\"",
		"# → Continue → Yes → enter the password → Reboot.",
		"# After reboot, ventd will sign its module automatically.",
	}
	resp := mokInstructionsResponse{
		Kind:     "instructions",
		Commands: cmds,
		Detail: "Secure Boot requires every kernel module to be signed by a key " +
			"the firmware trusts. MOK (Machine Owner Key) enrollment lets you " +
			"register your own signing key. This must be done interactively at " +
			"boot time — we cannot automate it. After your MOK is enrolled, " +
			"re-run the driver install from the setup wizard.",
	}
	s.writeJSON(r, w, resp)
}

// handleSetPassword POST /api/set-password
// Allows an authenticated user to change the dashboard password.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limitBody(w, r, 64<<10)
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Verify current password (if one is set).
	if current := s.authHashValue(); current != "" && !checkPassword(current, req.Current) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		s.writeJSON(r, w, map[string]string{"error": "current password is incorrect"})
		return
	}
	if len(req.New) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(r, w, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	hash, err := HashPassword(req.New)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if s.authPath != "" {
		if err := s.storeAuthHash(hash); err != nil {
			s.logger.Error("web: failed to save new password hash to auth.json", "err", err)
			http.Error(w, "could not save password", http.StatusInternalServerError)
			return
		}
	} else {
		// Legacy path: write to config.yaml (tests / no authPath).
		live := s.cfg.Load()
		live.Web.PasswordHash = hash
		if len(live.Controls) > 0 {
			if _, err := config.Save(live, s.configPath); err != nil {
				s.logger.Error("web: failed to save new password hash", "err", err)
				http.Error(w, "could not save password", http.StatusInternalServerError)
				return
			}
		} else {
			if err := s.writePasswordHash(hash); err != nil {
				s.logger.Error("web: failed to save new password hash", "err", err)
				http.Error(w, "could not save password", http.StatusInternalServerError)
				return
			}
		}
		s.liveHash.Store(&hash)
	}

	s.logger.Info("web: password changed", "remote", r.RemoteAddr)
	s.writeJSON(r, w, map[string]string{"status": "ok"})
}
