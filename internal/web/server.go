package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
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
	cfg        *atomic.Pointer[config.Config]
	configPath string
	logger     *slog.Logger
	mux        *http.ServeMux
	handler    http.Handler
	httpSrv    *http.Server
	cal        *calibrate.Manager
	setup      *setupmgr.Manager
	restartCh  chan<- struct{}
	sessions   *sessionStore
	setupMu    sync.Mutex
	setupToken string    // one-time first-boot token; empty once consumed or expired
	setupExp   time.Time // zero when no token
	diag       *hwdiag.Store
	ctx        context.Context // scoped to daemon lifetime; used by goroutines that outlive request handlers
	loginLim       *loginLimiter
	tlsActive      bool          // server serves TLS directly; gates HSTS
	trustedProxies []*net.IPNet  // set at New-time from live.Web.TrustProxy
}

// New constructs the web server. setupToken is the one-time first-boot token
// printed to the terminal; pass "" if a password is already configured.
func New(ctx context.Context, cfg *atomic.Pointer[config.Config], configPath string, logger *slog.Logger, cal *calibrate.Manager, sm *setupmgr.Manager, restartCh chan<- struct{}, setupToken string, diag *hwdiag.Store) *Server {
	live := cfg.Load()
	if diag == nil {
		diag = hwdiag.NewStore()
	}
	s := &Server{
		cfg:        cfg,
		configPath: configPath,
		logger:     logger,
		mux:        http.NewServeMux(),
		cal:        cal,
		setup:      sm,
		restartCh:  restartCh,
		sessions:   newSessionStore(ctx, live.Web.SessionTTL.Duration),
		setupToken: setupToken,
		diag:       diag,
		ctx:        ctx,
		loginLim:       newLoginLimiter(ctx, loginThresholdOrDefault(live.Web.LoginFailThreshold), loginCooldownOrDefault(live.Web.LoginLockoutCooldown.Duration)),
		trustedProxies: parseTrustedProxies(live.Web.TrustProxy, logger),
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

	// Unauthenticated endpoints.
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)
	s.mux.HandleFunc("/api/ping", s.handlePing)

	// All other routes require a valid session.
	auth := s.requireAuth
	s.mux.HandleFunc("/api/status", auth(s.handleStatus))
	s.mux.HandleFunc("/api/config", auth(s.handleConfig))
	s.mux.HandleFunc("/api/hardware", auth(s.handleHardware))
	s.mux.HandleFunc("/api/calibrate/start", auth(s.handleCalibrateStart))
	s.mux.HandleFunc("/api/calibrate/status", auth(s.handleCalibrateStatus))
	s.mux.HandleFunc("/api/calibrate/results", auth(s.handleCalibrateResults))
	s.mux.HandleFunc("/api/calibrate/abort", auth(s.handleCalibrateAbort))
	s.mux.HandleFunc("/api/detect-rpm", auth(s.handleDetectRPM))
	s.mux.HandleFunc("/api/setup/status", auth(s.handleSetupStatus))
	s.mux.HandleFunc("/api/setup/start", auth(s.handleSetupStart))
	s.mux.HandleFunc("/api/setup/apply", auth(s.handleSetupApply))
	s.mux.HandleFunc("/api/setup/reset", auth(s.handleSetupReset))
	s.mux.HandleFunc("/api/setup/calibrate/abort", auth(s.handleSetupCalibrateAbort))
	s.mux.HandleFunc("/api/system/reboot", auth(s.handleSystemReboot))
	s.mux.HandleFunc("/api/set-password", auth(s.handleSetPassword))
	s.mux.HandleFunc("/api/hwdiag", auth(s.handleHwdiag))
	s.mux.HandleFunc("/api/hwdiag/install-kernel-headers", auth(s.handleInstallKernelHeaders))
	s.mux.HandleFunc("/api/hwdiag/install-dkms", auth(s.handleInstallDKMS))
	s.mux.HandleFunc("/api/hwdiag/mok-enroll", auth(s.handleMOKEnroll))
	s.mux.HandleFunc("/", auth(s.handleUI))

	// Compose middleware around the mux: security headers → origin check → mux.
	// Origin check runs after headers so rejected responses still carry
	// nosniff / CSP. CSRF/Origin wraps the mux so every mutation path
	// (config PUT, setup apply/reset, reboot, set-password, …) inherits it
	// without per-handler opt-in. tlsActive is detected per-request via r.TLS.
	s.handler = securityHeaders()(originCheck(logger)(s.mux))
	s.httpSrv.Handler = s.handler
	return s
}

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

// handleLogin handles GET (serve login page) and POST (authenticate).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(loginHTML))

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

		// First-boot: no password set yet — return first_boot flag so the UI
		// can switch to the setup-token + new-password form.
		if live.Web.PasswordHash == "" {
			// If the client sent a setup token, process first-boot login.
			// Otherwise just tell the UI we're in first-boot mode.
			if r.FormValue("setup_token") != "" {
				s.handleFirstBootLogin(w, r, live, ipKey)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			s.writeJSON(r, w, map[string]interface{}{"error": "first boot", "first_boot": true})
			return
		}

		// Normal login: check password.
		password := r.FormValue("password")
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

func (s *Server) ListenAndServe(addr, tlsCert, tlsKey string) error {
	s.tlsActive = tlsCert != "" && tlsKey != ""
	s.httpSrv.Addr = addr
	if s.tlsActive {
		s.logger.Info("web: server listening (TLS)", "addr", "https://"+addr)
		if err := s.httpSrv.ListenAndServeTLS(tlsCert, tlsKey); err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	s.logger.Info("web: server listening", "addr", "http://"+addr)
	if err := s.httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
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
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type fanStatus struct {
	Name string  `json:"name"`
	PWM  uint8   `json:"pwm"`
	Duty float64 `json:"duty_pct"`
	RPM  *int    `json:"rpm"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
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
		if err != nil {
			s.logger.Warn("web: sensor read failed", "sensor", sensor.Name, "err", err)
		}
		resp.Sensors = append(resp.Sensors, sensorStatus{
			Name:  sensor.Name,
			Value: val,
			Unit:  sensorUnit(sensor),
		})
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
			if rpmErr == nil {
				fs.RPM = &rpm
			}
		}
		resp.Fans = append(resp.Fans, fs)
	}

	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, resp)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(dashboardHTML))
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


// handleSystemReboot POST /api/system/reboot
// Sends a response immediately then issues a system reboot.
// Used after the setup wizard auto-patches GRUB and needs a reboot to continue.
func (s *Server) handleSystemReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
