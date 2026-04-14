package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/monitor"
	"github.com/ventd/ventd/internal/nvidia"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

type Server struct {
	cfg        *atomic.Pointer[config.Config]
	configPath string
	logger     *slog.Logger
	mux        *http.ServeMux
	httpSrv    *http.Server
	cal        *calibrate.Manager
	setup      *setupmgr.Manager
	restartCh  chan<- struct{}
	sessions   *sessionStore
	setupToken string // one-time first-boot token; empty once password is set
}

// New constructs the web server. setupToken is the one-time first-boot token
// printed to the terminal; pass "" if a password is already configured.
func New(cfg *atomic.Pointer[config.Config], configPath string, logger *slog.Logger, cal *calibrate.Manager, sm *setupmgr.Manager, restartCh chan<- struct{}, setupToken string) *Server {
	live := cfg.Load()
	s := &Server{
		cfg:        cfg,
		configPath: configPath,
		logger:     logger,
		mux:        http.NewServeMux(),
		cal:        cal,
		setup:      sm,
		restartCh:  restartCh,
		sessions:   newSessionStore(live.Web.SessionTTL.Duration),
		setupToken: setupToken,
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
	s.mux.HandleFunc("/", auth(s.handleUI))
	return s
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
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// handlePing is an unauthenticated health-check used by the UI to detect when
// the daemon is back up after a restart.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleLogin handles GET (serve login page) and POST (authenticate).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(loginHTML))

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		live := s.cfg.Load()

		// First-boot: no password set yet — return first_boot flag so the UI
		// can switch to the setup-token + new-password form.
		if live.Web.PasswordHash == "" {
			// If the client sent a setup token, process first-boot login.
			// Otherwise just tell the UI we're in first-boot mode.
			if r.FormValue("setup_token") != "" {
				s.handleFirstBootLogin(w, r, live)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "first boot", "first_boot": true})
			return
		}

		// Normal login: check password.
		password := r.FormValue("password")
		if !checkPassword(live.Web.PasswordHash, password) {
			s.logger.Warn("web: failed login attempt", "remote", r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "incorrect password"})
			return
		}

		tok, err := s.sessions.create()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, tok, live.Web.SessionTTL.Duration)
		s.logger.Info("web: login successful", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFirstBootLogin processes the setup token + new password submission.
func (s *Server) handleFirstBootLogin(w http.ResponseWriter, r *http.Request, live *config.Config) {
	token := r.FormValue("setup_token")
	newPassword := r.FormValue("new_password")

	if s.setupToken == "" || token != s.setupToken {
		s.logger.Warn("web: invalid setup token attempt", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid setup token"})
		return
	}
	if len(newPassword) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "password must be at least 8 characters"})
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
	s.setupToken = ""
	s.logger.Info("web: first-boot password set", "remote", r.RemoteAddr)

	tok, err := s.sessions.create()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, tok, live.Web.SessionTTL.Duration)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	s.httpSrv = &http.Server{Addr: addr, Handler: s.mux}
	if tlsCert != "" && tlsKey != "" {
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

// Shutdown gracefully drains in-flight requests and closes the listening socket.
func (s *Server) Shutdown() {
	if s.httpSrv == nil {
		return
	}
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

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Warn("web: encode response failed", "err", err)
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(s.cfg.Load())
	case http.MethodPut:
		s.handleConfigPut(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var incoming config.Config
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(monitor.Scan())
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// handleCalibrateStatus GET /api/calibrate/status
func (s *Server) handleCalibrateStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.cal.AllStatus())
}

// handleCalibrateResults GET /api/calibrate/results
func (s *Server) handleCalibrateResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.cal.AllResults())
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	p := s.setup.ProgressNeeded(s.cfg.Load())
	json.NewEncoder(w).Encode(p)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
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

	// Ensure the config directory exists.
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0755); err != nil {
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

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rebooting"})
	// Flush the response before rebooting so the browser receives it.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleSetPassword POST /api/set-password
// Allows an authenticated user to change the dashboard password.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	live := s.cfg.Load()

	// Verify current password (if one is set).
	if live.Web.PasswordHash != "" && !checkPassword(live.Web.PasswordHash, req.Current) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "current password is incorrect"})
		return
	}
	if len(req.New) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "password must be at least 8 characters"})
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
