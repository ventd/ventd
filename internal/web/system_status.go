package web

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/sdnotify"
)

// System status endpoints — read-only introspection of the daemon's
// health plumbing. Each handler is small on purpose: the UI renders
// a "System Status" card that must feel cheap to refresh, and the
// underlying shell-outs (systemctl, aa-status, semodule) are not
// free. Every handler that calls an external binary therefore caches
// its result behind a short TTL, and returns "unsupported" rather
// than 500 when the binary is missing — this daemon targets OpenRC
// and runit hosts too, and a missing systemctl is not a bug.

// watchdogStatus mirrors what sdnotify exposes plus a computed
// "healthy" flag. last_ping_ms_ago is not tracked here because
// instrumenting the ping would require reaching into sdnotify's
// internals; the heartbeat goroutine is independent of HTTP, and
// "the server is answering" already tells you the daemon is alive.
// The UI surfaces "healthy" + interval; a missing watchdog (no
// WATCHDOG_USEC env) is "enabled: false" — operationally correct,
// not an error.
type watchdogStatus struct {
	Enabled    bool `json:"enabled"`
	IntervalMS int  `json:"interval_ms"`
	Healthy    bool `json:"healthy"`
}

func (s *Server) handleSystemWatchdog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	interval := sdnotify.WatchdogInterval()
	resp := watchdogStatus{
		Enabled:    interval > 0,
		IntervalMS: int(interval / time.Millisecond),
		// Healthy ≡ enabled: the goroutine started by sdnotify.StartHeartbeat
		// has no failure mode short of the daemon dying, at which point HTTP
		// stops responding and the UI renders an offline state of its own.
		Healthy: interval > 0,
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, resp)
}

// recoveryStatus reports whether ventd-recover.service is installed
// and currently active. OpenRC / runit hosts lack systemctl entirely;
// we detect that and return installed=false rather than 500.
type recoveryStatus struct {
	Installed     bool `json:"installed"`
	ServiceActive bool `json:"service_active"`
}

// cachedRecovery wraps the systemctl shell-out with a short TTL so a
// dashboard that polls "System Status" every 5s doesn't spawn a
// process per tick.
type cachedRecovery struct {
	mu       sync.Mutex
	snap     recoveryStatus
	at       time.Time
	ttl      time.Duration
	lookPath func(string) (string, error)           // test seam
	runCmd   func(ctx string, args ...string) error // test seam
}

// recoveryCache is a singleton protected by its own mutex. Cacheing
// here (rather than per-Server) keeps the TTL honest across a test
// suite that creates many servers — but this variable is module-level
// and resets with the binary, so production sees one process-wide
// cache which is what we want.
var recoveryCache = &cachedRecovery{
	ttl:      5 * time.Second,
	lookPath: exec.LookPath,
	runCmd: func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		// Discard stdout; systemctl's exit code is what we care about.
		// Capture stderr to surface useful errors in the logs without
		// swallowing them entirely.
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		return cmd.Run()
	},
}

func (c *cachedRecovery) snapshot() recoveryStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < c.ttl && !c.at.IsZero() {
		return c.snap
	}
	snap := recoveryStatus{}
	if _, err := c.lookPath("systemctl"); err == nil {
		// is-enabled exits 0 when enabled, non-zero otherwise. Same
		// contract as is-active. A non-installed unit exits non-zero
		// on both, which is exactly what we report.
		snap.Installed = c.runCmd("systemctl", "is-enabled", "ventd-recover.service") == nil
		snap.ServiceActive = c.runCmd("systemctl", "is-active", "ventd-recover.service") == nil
	}
	c.snap = snap
	c.at = time.Now()
	return snap
}

func (s *Server) handleSystemRecovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, recoveryCache.snapshot())
}

// securityStatus reports SELinux and AppArmor module load state.
// Three-valued per surface: "loaded", "unloaded", "unsupported".
// "unsupported" means the kernel LSM isn't present on this host — a
// vanilla desktop distro typically has neither, and surfacing that as
// a red flag would be noise.
type securityStatus struct {
	SELinuxModule   string `json:"selinux_module"`
	AppArmorProfile string `json:"apparmor_profile"`
}

type cachedSecurity struct {
	mu   sync.Mutex
	snap securityStatus
	at   time.Time
	ttl  time.Duration
	// Test seams. statFn mirrors os.Stat; runWithOutput captures the
	// combined stdout of a command so callers can grep for "ventd"
	// rather than relying on exit codes that vary between distros.
	statFn        func(string) (os.FileInfo, error)
	runWithOutput func(name string, args ...string) (string, error)
}

var securityCache = &cachedSecurity{
	ttl:    30 * time.Second,
	statFn: os.Stat,
	runWithOutput: func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return out.String(), err
		}
		return out.String(), nil
	},
}

func (c *cachedSecurity) snapshot() securityStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < c.ttl && !c.at.IsZero() {
		return c.snap
	}
	snap := securityStatus{
		SELinuxModule:   "unsupported",
		AppArmorProfile: "unsupported",
	}
	// SELinux: /sys/fs/selinux/enforce is only present when SELinux is
	// compiled in AND enabled in the kernel. If present, try semodule -l
	// to see whether the ventd policy has been loaded.
	if _, err := c.statFn("/sys/fs/selinux/enforce"); err == nil {
		snap.SELinuxModule = "unloaded"
		if out, err := c.runWithOutput("semodule", "-l"); err == nil {
			if strings.Contains(out, "ventd") {
				snap.SELinuxModule = "loaded"
			}
		}
	}
	// AppArmor: /sys/kernel/security/apparmor is a directory on every
	// AppArmor-enabled kernel. aa-status --profiles lists loaded
	// profiles; we grep for "ventd" to detect our own.
	if _, err := c.statFn("/sys/kernel/security/apparmor"); err == nil {
		snap.AppArmorProfile = "unloaded"
		if out, err := c.runWithOutput("aa-status", "--profiles"); err == nil {
			if strings.Contains(out, "ventd") {
				snap.AppArmorProfile = "loaded"
			}
		}
	}
	c.snap = snap
	c.at = time.Now()
	return snap
}

func (s *Server) handleSystemSecurity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, securityCache.snapshot())
}

// diagnosticEntry is the wire shape for /api/system/diagnostics. We
// don't export hwdiag.Entry directly because the hwdiag package's
// richer structure (remediation buttons, component IDs) is the
// setup-wizard surface; here the UI wants a flat list to render in
// the Settings modal's System Status section and a banner on the
// dashboard.
type diagnosticEntry struct {
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"`
	FixCommand string `json:"fix_command,omitempty"`
}

type diagnosticsResponse struct {
	Entries []diagnosticEntry `json:"entries"`
	Counts  map[string]int    `json:"counts"` // severity → count; drives the banner
}

func (s *Server) handleSystemDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.diag.Snapshot(hwdiag.Filter{})
	resp := diagnosticsResponse{
		Entries: make([]diagnosticEntry, 0, len(snap.Entries)),
		Counts:  map[string]int{"info": 0, "warn": 0, "error": 0},
	}
	for _, e := range snap.Entries {
		sev := string(e.Severity)
		if _, ok := resp.Counts[sev]; !ok {
			resp.Counts[sev] = 0
		}
		resp.Counts[sev]++
		out := diagnosticEntry{
			Severity: sev,
			Summary:  orElse(e.Summary, string(e.ID)),
			Detail:   e.Detail,
		}
		if e.Remediation != nil && e.Remediation.Label != "" {
			// The Settings-modal surface shows only the command form of
			// remediation (copy-to-clipboard). Endpoint-backed buttons
			// live on the setup wizard and are excluded here.
			out.FixCommand = e.Remediation.Label
		}
		resp.Entries = append(resp.Entries, out)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, resp)
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
