package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/grub"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/monitor"
	"github.com/ventd/ventd/internal/nvidia"
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
	aggregator     *aggregator.Aggregator
	layerA         *layer_a.Estimator
	restartCh      chan<- struct{}
	sessions       *sessionStore
	diag           *hwdiag.Store
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

	// kvWiper is called by handleSetupReset to wipe the wizard and probe
	// KV namespaces so the next daemon start treats the system as freshly
	// installed (RULE-PROBE-09). Nil in tests that don't need KV teardown;
	// set via SetKVWiper in main.go after Server construction.
	kvWiper func() error
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
	s.registerWebPage("curve-editor")
	s.registerWebPage("schedule")
	s.registerWebPage("sensors")
	s.registerWebPage("settings")

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

		// Authenticated routes.
		{name: "status", handler: s.handleStatus, auth: true},
		{name: "events", handler: s.handleEvents, auth: true},
		{name: "config", handler: s.handleConfig, auth: true},
		{name: "config/dryrun", handler: s.handleConfigDryrun, auth: true},
		{name: "hardware", handler: s.handleHardware, auth: true},
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
		{name: "setup/start", handler: s.handleSetupStart, auth: true},
		{name: "setup/apply", handler: s.handleSetupApply, auth: true},
		{name: "setup/reset", handler: s.handleSetupReset, auth: true},
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
		// v0.5.9 confidence-controller surfaces (PR-B). status returns
		// per-channel aggregator + LayerA snapshots for the 5-state
		// dashboard pill; preset GET/PUT exposes the smart-mode
		// aggressiveness selector.
		{name: "confidence/status", handler: s.handleConfidenceStatus, auth: true},
		{name: "confidence/preset", handler: s.handleConfidencePreset, auth: true},
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
// prefixes from a single slice. Auth middleware is applied once per route;
// the v1 alias shares the wrapped handler so a single request never pays
// the session check twice, and adding a new endpoint requires exactly one
// new slice entry rather than two HandleFunc calls.
func (s *Server) registerAPIRoutes(routes []apiRoute) {
	for _, r := range routes {
		h := r.handler
		if r.auth {
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
		setSessionCookie(w, tok, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
		s.logger.Info("web: login successful", "remote", r.RemoteAddr)
		s.writeJSON(r, w, map[string]string{"status": "ok"})

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
	setSessionCookie(w, tok, live.Web.SessionTTL.Duration, live.Web.UseSecureCookies())
	s.writeJSON(r, w, map[string]string{"status": "ok"})
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
		var err error
		if fan.Type == "nvidia" {
			idx, _ := strconv.ParseUint(fan.PWMPath, 10, 32)
			pwm, err = nvidia.ReadFanSpeed(uint(idx))
		} else {
			pwm, err = hwmon.ReadPWM(fan.PWMPath)
		}
		if err != nil {
			s.logger.Warn("web: fan read failed", "fan", fan.Name, "err", err)
		}
		fs := fanStatus{
			Name: fan.Name,
			PWM:  pwm,
			Duty: float64(pwm) / 255.0 * 100.0,
		}
		if fan.Type != "nvidia" {
			var rpmErr error
			var rpm int
			if fan.RPMPath != "" {
				rpm, rpmErr = hwmon.ReadRPMPath(fan.RPMPath)
			} else {
				rpm, rpmErr = hwmon.ReadRPM(fan.PWMPath)
			}
			// Reject sentinel RPM values — they must not appear in the UI
			// as if the fan is spinning at 65535 RPM.
			if rpmErr == nil && !halhwmon.IsSentinelRPM(rpm) {
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
	//   no admin password set → /setup       (first-boot wizard)
	//   setup wizard pending  → /calibration (probe + apply)
	//   otherwise             → /dashboard
	dest := "/dashboard"
	if s.authHashValue() == "" {
		dest = "/setup"
	} else if s.setup != nil {
		p := s.setup.ProgressNeeded(s.cfg.Load())
		if p.Needed && !p.Applied {
			dest = "/calibration"
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

// SetConfidence wires the v0.5.9 aggregator + LayerA estimator so the
// /api/v1/confidence/status endpoint can return per-channel snapshots.
// Both arguments are nil-safe: monitor-only mode skips construction.
func (s *Server) SetConfidence(agg *aggregator.Aggregator, est *layer_a.Estimator) {
	s.aggregator = agg
	s.layerA = est
}

// confidenceChannel is the JSON wire shape for one channel's
// confidence snapshot. UI renders this directly.
type confidenceChannel struct {
	ChannelID        string  `json:"channel_id"`
	Wpred            float64 `json:"w_pred"`
	UIState          string  `json:"ui_state"`
	ConfA            float64 `json:"conf_a"`
	ConfB            float64 `json:"conf_b"`
	ConfC            float64 `json:"conf_c"`
	Tier             uint8   `json:"tier"`
	Coverage         float64 `json:"coverage"`
	SeenFirstContact bool    `json:"seen_first_contact"`
	AgeSeconds       float64 `json:"age_seconds"`
}

type confidenceStatus struct {
	Enabled  bool                `json:"enabled"`
	Global   string              `json:"global_state"` // worst-of-channels collapse
	Preset   string              `json:"preset"`
	Channels []confidenceChannel `json:"channels"`
}

// handleConfidenceStatus GET /api/v1/confidence/status (v0.5.9).
// Returns the live aggregator + LayerA snapshots per channel for
// the dashboard's 5-state pill UI. Read-only; never blocks the
// controller hot loop (atomic.Pointer reads + a brief mutex on
// SnapshotAll).
func (s *Server) handleConfidenceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	live := s.cfg.Load()
	preset := "balanced"
	if live != nil {
		if name, _ := live.Smart.SmartPreset(); name != "" {
			preset = name
		}
	}
	if s.aggregator == nil || s.layerA == nil {
		s.writeJSON(r, w, confidenceStatus{Enabled: false, Preset: preset})
		return
	}

	aggSnaps := s.aggregator.SnapshotAll()
	out := confidenceStatus{
		Enabled:  true,
		Preset:   preset,
		Channels: make([]confidenceChannel, 0, len(aggSnaps)),
	}
	worst := "converged" // best state; downgrades below
	priority := map[string]int{
		"refused": 0, "drifting": 1, "cold-start": 2,
		"warming": 3, "converged": 4,
	}
	for _, a := range aggSnaps {
		la := s.layerA.Read(a.ChannelID)
		entry := confidenceChannel{
			ChannelID: a.ChannelID,
			Wpred:     a.Wpred,
			UIState:   a.UIState,
			ConfA:     a.ConfA,
			ConfB:     a.ConfB,
			ConfC:     a.ConfC,
		}
		if la != nil {
			entry.Tier = la.Tier
			entry.Coverage = la.Coverage
			entry.SeenFirstContact = la.SeenFirstContact
			entry.AgeSeconds = la.Age.Seconds()
		}
		out.Channels = append(out.Channels, entry)
		if priority[a.UIState] < priority[worst] {
			worst = a.UIState
		}
	}
	if len(out.Channels) > 0 {
		out.Global = worst
	} else {
		out.Global = "converged"
	}
	s.writeJSON(r, w, out)
}

// handleConfidencePreset GET/PUT /api/v1/confidence/preset (v0.5.9).
// GET returns the active preset; PUT mutates the live Config in
// memory + persists via Save. Recognised values: silent / balanced /
// performance. Unknown values produce 400.
func (s *Server) handleConfidencePreset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		live := s.cfg.Load()
		preset := "balanced"
		if live != nil {
			if name, _ := live.Smart.SmartPreset(); name != "" {
				preset = name
			}
		}
		s.writeJSON(r, w, map[string]string{"preset": preset})
		return
	}

	// PUT: read+validate body.
	var body struct {
		Preset string `json:"preset"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch body.Preset {
	case "silent", "balanced", "performance":
	default:
		http.Error(w, "preset must be silent|balanced|performance", http.StatusBadRequest)
		return
	}

	live := s.cfg.Load()
	if live == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}
	// Deep-copy via JSON so we don't mutate the live pointer's
	// state under a concurrent reader.
	var next config.Config
	raw, err := json.Marshal(live)
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	if err := json.Unmarshal(raw, &next); err != nil {
		http.Error(w, "unmarshal", http.StatusInternalServerError)
		return
	}
	next.Smart.Preset = body.Preset
	saved, err := config.Save(&next, s.configPath)
	if err != nil {
		s.logger.Warn("web: confidence preset save failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.cfg.Store(saved)
	s.logger.Info("web: confidence preset updated", "preset", body.Preset)
	s.writeJSON(r, w, map[string]string{"preset": body.Preset})
}

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

// handleSetupReset POST /api/setup/reset
// Deletes the config file and triggers a daemon restart, returning to first-boot mode.
func (s *Server) handleSetupReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := os.Remove(s.configPath); err != nil && !os.IsNotExist(err) {
		http.Error(w, "remove config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Wipe the wizard and probe KV namespaces so the next daemon start
	// treats this system as freshly installed (RULE-PROBE-09).
	if s.kvWiper != nil {
		if err := s.kvWiper(); err != nil {
			s.logger.Warn("setup reset: kv wipe failed", "err", err)
		}
	}
	// Clear the persistent applied-marker so a host that opted into
	// monitor-only via handleSetupApply's empty-fanset escape goes back
	// to the wizard on next start (otherwise the marker would suppress
	// the wizard even though config.yaml was just removed).
	if err := s.setup.ClearApplied(); err != nil {
		s.logger.Warn("setup reset: clear applied marker failed", "err", err)
	}
	s.logger.Info("setup: config removed; triggering reload (daemon continues until next restart)", "path", s.configPath)

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok"})
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
// Refuses with 409 Conflict when the daemon is running in an environment
// where "reboot the host" isn't the right action — containers, LXC, WSL,
// anywhere ventd is PID 1. The wizard surfaces the body verbatim so the
// user sees a human explanation, not a stdlib error string.
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
	s.logger.Info("web: system reboot requested via setup wizard")
	s.writeJSON(r, w, map[string]string{"status": "rebooting"})
	// Flush the response before rebooting so the browser receives it.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		// Ctx-aware delay so daemon shutdown isn't blocked by a pending reboot dispatch.
		select {
		case <-time.After(300 * time.Millisecond):
		case <-s.ctx.Done():
			return
		}
		// Try init-system reboot commands in order. All of these go through a
		// proper shutdown sequence that syncs filesystems before rebooting.
		for _, cmd := range [][]string{
			{"systemctl", "reboot"}, // systemd
			{"reboot"},              // OpenRC / runit / generic
		} {
			if err := exec.Command(cmd[0], cmd[1:]...).Run(); err == nil {
				return
			}
		}
	}()
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
func (s *Server) handleInstallKernelHeaders(w http.ResponseWriter, r *http.Request) {
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
func (s *Server) handleResetAndReinstall(w http.ResponseWriter, r *http.Request) {
	s.runInstallHandler(w, r, "", func(logFn func(string)) error {
		module := ""
		if r.Body != nil && r.ContentLength > 0 {
			var body struct {
				Module string `json:"module"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				module = body.Module
			}
		}
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
	param := "acpi_enforce_resources=lax"
	if r.Body != nil && r.ContentLength > 0 {
		var body struct {
			Param string `json:"param"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Param != "" {
			param = body.Param
		}
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
func (s *Server) handleInstallDKMS(w http.ResponseWriter, r *http.Request) {
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
