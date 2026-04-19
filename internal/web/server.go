package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/monitor"
	"github.com/ventd/ventd/internal/nvidia"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// SetupTokenTTL bounds how long the first-boot setup token stays valid.
// After this window the token is wiped from memory; an operator must
// restart the daemon to mint a new one.
const SetupTokenTTL = 15 * time.Minute

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
	cfg            *atomic.Pointer[config.Config]
	configPath     string
	logger         *slog.Logger
	mux            *http.ServeMux
	handler        http.Handler
	httpSrv        *http.Server
	cal            *calibrate.Manager
	setup          *setupmgr.Manager
	restartCh      chan<- struct{}
	sessions       *sessionStore
	setupMu        sync.Mutex
	setupToken     string    // one-time first-boot token; empty once consumed or expired
	setupExp       time.Time // zero when no token
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
}

// New constructs the web server. setupToken is the one-time first-boot token
// printed to the terminal; pass "" if a password is already configured.
func New(ctx context.Context, cfg *atomic.Pointer[config.Config], configPath string, logger *slog.Logger, cal *calibrate.Manager, sm *setupmgr.Manager, restartCh chan<- struct{}, setupToken string, diag *hwdiag.Store) *Server {
	live := cfg.Load()
	if diag == nil {
		diag = hwdiag.NewStore()
	}
	s := &Server{
		cfg:            cfg,
		configPath:     configPath,
		logger:         logger,
		mux:            http.NewServeMux(),
		cal:            cal,
		setup:          sm,
		restartCh:      restartCh,
		sessions:       newSessionStore(ctx, live.Web.SessionTTL.Duration),
		setupToken:     setupToken,
		diag:           diag,
		ctx:            ctx,
		loginLim:       newLoginLimiter(ctx, loginThresholdOrDefault(live.Web.LoginFailThreshold), loginCooldownOrDefault(live.Web.LoginLockoutCooldown.Duration)),
		trustedProxies: parseTrustedProxies(live.Web.TrustProxy, logger),
		sseInterval:    defaultSSEInterval,
		history:        NewHistoryStore(defaultSSEInterval, historyDefaultWindow),
		schedWake:      make(chan struct{}, 1),
	}
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
	if setupToken != "" {
		s.setupExp = time.Now().Add(SetupTokenTTL)
		// Auto-invalidate on expiry so a leaked token stops working even
		// if the operator never logs in. One-shot — the daemon can't
		// silently re-mint it without restart.
		go s.expireSetupToken(ctx)
	}

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
		{name: "hwdiag/mok-enroll", handler: s.handleMOKEnroll, auth: true},
		{name: "system/watchdog", handler: s.handleSystemWatchdog, auth: true},
		{name: "system/recovery", handler: s.handleSystemRecovery, auth: true},
		{name: "system/security", handler: s.handleSystemSecurity, auth: true},
		{name: "system/diagnostics", handler: s.handleSystemDiagnostics, auth: true},
	})

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

// expireSetupToken wipes the first-boot token from memory when its TTL
// lapses or the daemon shuts down. Safe against races with consumption —
// consumeSetupToken holds the same mutex.
func (s *Server) expireSetupToken(ctx context.Context) {
	s.setupMu.Lock()
	exp := s.setupExp
	s.setupMu.Unlock()
	if exp.IsZero() {
		return
	}
	timer := time.NewTimer(time.Until(exp))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupToken == "" {
		return
	}
	s.setupToken = ""
	s.setupExp = time.Time{}
	s.logger.Warn("web: first-boot setup token expired; restart ventd to mint a new one")
}

// consumeSetupToken returns true iff provided matches the live token and
// the token has not expired. Compares in constant time against a padded
// copy to avoid leaking length via early-exit timing. Does NOT clear the
// token — caller decides when the first-boot flow has actually succeeded.
func (s *Server) consumeSetupToken(provided string) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupToken == "" {
		return false
	}
	if !s.setupExp.IsZero() && time.Now().After(s.setupExp) {
		s.setupToken = ""
		s.setupExp = time.Time{}
		return false
	}
	a := []byte(provided)
	b := []byte(s.setupToken)
	if len(a) != len(b) {
		// Length check first avoids the obvious length oracle; run a dummy
		// constant-time compare against a padded buffer so attackers cannot
		// distinguish length-mismatch rejects from content-mismatch ones by
		// timing the response path.
		pad := make([]byte, len(b))
		subtle.ConstantTimeCompare(pad, b)
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

func (s *Server) clearSetupToken() {
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupExp = time.Time{}
	s.setupMu.Unlock()
}

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
	live := s.cfg.Load()
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, map[string]bool{"first_boot": live.Web.PasswordHash == ""})
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

		// First-boot: no password set yet. If the client sent a setup token,
		// process the first-boot handshake. An empty POST is rejected with
		// 400 so it is impossible for a misbehaving or malicious caller to
		// use the /login endpoint as a zero-cost oracle for "is the daemon
		// still in first-boot mode" — that probe moved to GET /api/auth/state
		// which does not touch the rate limiter. See audit finding S2.
		if live.Web.PasswordHash == "" {
			if r.FormValue("setup_token") != "" {
				s.handleFirstBootLogin(w, r, live, ipKey)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(r, w, map[string]interface{}{"error": "first boot: use /api/auth/state to check status, then POST setup_token + new_password", "first_boot": true})
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
		if !checkPassword(live.Web.PasswordHash, password) {
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

// handleFirstBootLogin processes the setup token + new password submission.
func (s *Server) handleFirstBootLogin(w http.ResponseWriter, r *http.Request, live *config.Config, ipKey string) {
	token := r.FormValue("setup_token")
	newPassword := r.FormValue("new_password")

	if !s.consumeSetupToken(token) {
		locked := s.loginLim.recordFailure(ipKey)
		if locked {
			s.logger.Warn("web: login lockout after bad setup tokens", "remote", r.RemoteAddr, "ip", ipKey, "cooldown", live.Web.LoginLockoutCooldown.Duration)
		} else {
			s.logger.Warn("web: invalid setup token attempt", "remote", r.RemoteAddr)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		s.writeJSON(r, w, map[string]string{"error": "invalid setup token"})
		return
	}
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

	// Persist password hash. Try a full config save first (works when the
	// setup wizard has already generated a config with controls defined).
	// Fall back to a minimal config file during pure first-boot.
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

	// Invalidate the one-time setup token.
	s.clearSetupToken()
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
// hash. Used during first boot before the setup wizard has generated a full
// config. On the next daemon start the full config will be written by the wizard.
func (s *Server) writePasswordHash(hash string) error {
	return config.SavePasswordHash(hash, s.configPath)
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

// triggerRestart signals main to restart the daemon after the current request completes.
func (s *Server) triggerRestart() {
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

	validated, err := config.Save(&incoming, s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.cfg.Store(validated)
	s.logger.Info("config updated via web UI", "controls", len(validated.Controls))

	s.writeJSON(r, w, map[string]string{"status": "ok"})
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
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(readUI("index.html"))
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
func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.setup.GeneratedConfig()
	if cfg == nil {
		http.Error(w, "setup not complete", http.StatusConflict)
		return
	}

	// Carry over the existing password hash so login still works after apply.
	cfg.Web.PasswordHash = s.cfg.Load().Web.PasswordHash

	// Ensure the config directory exists. 0700 so only the daemon's user
	// can read the password hash stored inside.
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0700); err != nil {
		http.Error(w, "create config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := config.Save(cfg, s.configPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.setup.MarkApplied()
	s.logger.Info("setup: config written via web UI", "path", s.configPath,
		"fans", len(cfg.Fans), "controls", len(cfg.Controls))

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok"})
	s.triggerRestart()
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
	s.logger.Info("setup: config removed, restarting for fresh setup", "path", s.configPath)

	w.Header().Set("Connection", "close")
	s.writeJSON(r, w, map[string]string{"status": "ok"})
	s.triggerRestart()
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

	live := s.cfg.Load()

	// Verify current password (if one is set).
	if live.Web.PasswordHash != "" && !checkPassword(live.Web.PasswordHash, req.Current) {
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

	s.logger.Info("web: password changed", "remote", r.RemoteAddr)
	s.writeJSON(r, w, map[string]string{"status": "ok"})
}
