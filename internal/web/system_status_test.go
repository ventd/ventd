package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hwdiag"
)

// TestSystemWatchdog exercises the three reachable states: unset env
// (no systemd / WATCHDOG_USEC absent), set env (watchdog enabled), and
// a bogus env value (unparseable, treated as absent). Running under
// the real t.Setenv so the env mutation is automatically restored.
func TestSystemWatchdog(t *testing.T) {
	cases := []struct {
		name        string
		env         string
		setEnv      bool
		wantEnabled bool
		wantMinMS   int
	}{
		{"unset env → disabled", "", false, false, 0},
		{"2_000_000 usec → 1000 ms (half)", "2000000", true, true, 1000},
		{"unparseable env → disabled", "not-a-number", true, false, 0},
		{"zero env → disabled", "0", true, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("WATCHDOG_USEC", tc.env)
			} else {
				t.Setenv("WATCHDOG_USEC", "")
			}
			srv := newSysStatusServer(t)
			rr := httptest.NewRecorder()
			srv.handleSystemWatchdog(rr, httptest.NewRequest(http.MethodGet, "/api/system/watchdog", nil))
			if rr.Code != 200 {
				t.Fatalf("status %d want 200: %s", rr.Code, rr.Body)
			}
			var got watchdogStatus
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Enabled != tc.wantEnabled {
				t.Errorf("Enabled=%v want %v", got.Enabled, tc.wantEnabled)
			}
			if got.IntervalMS != tc.wantMinMS {
				t.Errorf("IntervalMS=%d want %d", got.IntervalMS, tc.wantMinMS)
			}
			if got.Healthy != tc.wantEnabled {
				t.Errorf("Healthy=%v want %v", got.Healthy, tc.wantEnabled)
			}
		})
	}
}

// TestSystemRecovery_CacheAndMissingSystemctl covers the two paths
// that matter: a host with systemctl where the service is absent, and
// a host without systemctl at all (OpenRC / runit / container). Both
// must return installed=false without 500-ing.
func TestSystemRecovery_CacheAndMissingSystemctl(t *testing.T) {
	restoreCache := saveRecoveryCache()
	defer restoreCache()

	t.Run("missing systemctl reports installed=false", func(t *testing.T) {
		recoveryCache.lookPath = func(string) (string, error) { return "", errors.New("exec: not found") }
		recoveryCache.runCmd = func(string, ...string) error { t.Fatal("runCmd called with missing systemctl"); return nil }
		recoveryCache.at = time.Time{}
		snap := recoveryCache.snapshot()
		if snap.Installed || snap.ServiceActive {
			t.Errorf("expected zero-value snap, got %+v", snap)
		}
	})

	t.Run("caches within ttl — one shell-out per window", func(t *testing.T) {
		calls := 0
		recoveryCache.lookPath = func(string) (string, error) { return "/bin/systemctl", nil }
		recoveryCache.runCmd = func(name string, args ...string) error {
			calls++
			// is-enabled and is-active both succeed → installed + active.
			return nil
		}
		recoveryCache.at = time.Time{}
		recoveryCache.snapshot()
		recoveryCache.snapshot()
		recoveryCache.snapshot()
		// First snapshot fires both is-enabled and is-active (2 calls),
		// second and third are cached → total 2 not 6.
		if calls != 2 {
			t.Errorf("expected 2 systemctl invocations, got %d", calls)
		}
	})

	t.Run("stale cache re-queries", func(t *testing.T) {
		calls := 0
		recoveryCache.lookPath = func(string) (string, error) { return "/bin/systemctl", nil }
		recoveryCache.runCmd = func(string, ...string) error { calls++; return nil }
		recoveryCache.at = time.Now().Add(-time.Minute) // force stale
		recoveryCache.snapshot()
		if calls != 2 {
			t.Errorf("expected 2 calls on stale cache, got %d", calls)
		}
	})
}

// TestSystemSecurity_MissingBinariesAreUnsupported asserts that a
// host with neither SELinux nor AppArmor in its kernel reports both
// as "unsupported" rather than erroring. The Settings-modal UI then
// renders no row at all, keeping the section free of noise.
func TestSystemSecurity_MissingBinariesAreUnsupported(t *testing.T) {
	restoreCache := saveSecurityCache()
	defer restoreCache()

	securityCache.statFn = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
	securityCache.runWithOutput = func(string, ...string) (string, error) {
		t.Fatal("runWithOutput should not be called when the LSM is absent")
		return "", nil
	}
	securityCache.at = time.Time{}
	snap := securityCache.snapshot()
	if snap.SELinuxModule != "unsupported" {
		t.Errorf("SELinuxModule=%q want unsupported", snap.SELinuxModule)
	}
	if snap.AppArmorProfile != "unsupported" {
		t.Errorf("AppArmorProfile=%q want unsupported", snap.AppArmorProfile)
	}
}

// TestSystemSecurity_LoadedViaModuleList covers the positive case:
// /sys/fs/selinux/enforce exists and `semodule -l` names ventd.
// Mirrors AppArmor via aa-status --profiles.
func TestSystemSecurity_LoadedViaModuleList(t *testing.T) {
	restoreCache := saveSecurityCache()
	defer restoreCache()

	securityCache.statFn = func(path string) (os.FileInfo, error) {
		switch path {
		case "/sys/fs/selinux/enforce", "/sys/kernel/security/apparmor":
			return fakeStat{name: path}, nil
		}
		return nil, fs.ErrNotExist
	}
	securityCache.runWithOutput = func(name string, args ...string) (string, error) {
		if name == "semodule" {
			return "container\nventd\nvirt\n", nil
		}
		if name == "aa-status" {
			return "/usr/bin/ventd (enforce)\n", nil
		}
		return "", nil
	}
	securityCache.at = time.Time{}
	snap := securityCache.snapshot()
	if snap.SELinuxModule != "loaded" {
		t.Errorf("SELinuxModule=%q want loaded", snap.SELinuxModule)
	}
	if snap.AppArmorProfile != "loaded" {
		t.Errorf("AppArmorProfile=%q want loaded", snap.AppArmorProfile)
	}
}

// TestSystemSecurity_PresentButUnloaded asserts we distinguish "kernel
// supports SELinux but our policy is not in the list" from
// "unsupported". The former is actionable (an operator can install
// the policy); the latter is informational.
func TestSystemSecurity_PresentButUnloaded(t *testing.T) {
	restoreCache := saveSecurityCache()
	defer restoreCache()

	securityCache.statFn = func(path string) (os.FileInfo, error) {
		if path == "/sys/fs/selinux/enforce" {
			return fakeStat{name: path}, nil
		}
		return nil, fs.ErrNotExist
	}
	securityCache.runWithOutput = func(name string, args ...string) (string, error) {
		if name == "semodule" {
			return "container\nvirt\n", nil // no ventd
		}
		return "", nil
	}
	securityCache.at = time.Time{}
	snap := securityCache.snapshot()
	if snap.SELinuxModule != "unloaded" {
		t.Errorf("SELinuxModule=%q want unloaded", snap.SELinuxModule)
	}
	if snap.AppArmorProfile != "unsupported" {
		t.Errorf("AppArmorProfile=%q want unsupported (no apparmor dir)", snap.AppArmorProfile)
	}
}

// TestSystemDiagnostics_ShapeAndCounts verifies the wire shape the UI
// banner relies on — specifically the counts map, which drives the
// "X warning(s) / Y error(s)" text without making the client count
// entries itself.
func TestSystemDiagnostics_ShapeAndCounts(t *testing.T) {
	srv := newSysStatusServer(t)
	srv.diag.Set(hwdiag.Entry{
		ID:        "ventd.test/info",
		Component: hwdiag.Component("hwmon"),
		Severity:  hwdiag.SeverityInfo,
		Summary:   "all good",
	})
	srv.diag.Set(hwdiag.Entry{
		ID:        "ventd.test/warn",
		Component: hwdiag.Component("hwmon"),
		Severity:  hwdiag.SeverityWarn,
		Summary:   "dkms not installed",
		Detail:    "OOT modules will not survive a kernel upgrade",
	})

	rr := httptest.NewRecorder()
	srv.handleSystemDiagnostics(rr, httptest.NewRequest(http.MethodGet, "/api/system/diagnostics", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got diagnosticsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Errorf("entries=%d want 2", len(got.Entries))
	}
	if got.Counts["info"] != 1 || got.Counts["warn"] != 1 {
		t.Errorf("counts=%v want info=1 warn=1", got.Counts)
	}
	found := false
	for _, e := range got.Entries {
		if e.Severity == "warn" && strings.Contains(e.Summary, "dkms") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warn entry for dkms, got %+v", got.Entries)
	}
}

// TestSystemStatus_MethodNotAllowed asserts each endpoint rejects
// non-GET. The UI only ever issues GET; a rogue client should get a
// 405 rather than drifting through to the GET body.
func TestSystemStatus_MethodNotAllowed(t *testing.T) {
	srv := newSysStatusServer(t)
	handlers := map[string]http.HandlerFunc{
		"watchdog":    srv.handleSystemWatchdog,
		"recovery":    srv.handleSystemRecovery,
		"security":    srv.handleSystemSecurity,
		"diagnostics": srv.handleSystemDiagnostics,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h(rr, httptest.NewRequest(http.MethodPost, "/api/system/"+name, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("POST /api/system/%s: %d want 405", name, rr.Code)
			}
		})
	}
}

// ─── helpers ────────────────────────────────────────────────────────

type fakeStat struct{ name string }

func (f fakeStat) Name() string       { return f.name }
func (fakeStat) Size() int64          { return 0 }
func (fakeStat) Mode() os.FileMode    { return 0 }
func (fakeStat) ModTime() (t time.Time) {
	return
}
func (fakeStat) IsDir() bool      { return false }
func (fakeStat) Sys() interface{} { return nil }

func newSysStatusServer(t *testing.T) *Server {
	t.Helper()
	return newVersionTestServer(t)
}

// saveRecoveryCache / saveSecurityCache snapshot the mutable fields of
// the module-level singletons so a test can overwrite shell-out seams
// and restore the production values in defer. We copy the specific
// fields rather than the whole struct so `go vet`'s copylocks check
// doesn't fire on the embedded sync.Mutex.
func saveRecoveryCache() func() {
	lookPath, runCmd, ttl, at, snap := recoveryCache.lookPath, recoveryCache.runCmd, recoveryCache.ttl, recoveryCache.at, recoveryCache.snap
	return func() {
		recoveryCache.mu.Lock()
		defer recoveryCache.mu.Unlock()
		recoveryCache.lookPath = lookPath
		recoveryCache.runCmd = runCmd
		recoveryCache.ttl = ttl
		recoveryCache.at = at
		recoveryCache.snap = snap
	}
}
func saveSecurityCache() func() {
	statFn, runWithOutput, ttl, at, snap := securityCache.statFn, securityCache.runWithOutput, securityCache.ttl, securityCache.at, securityCache.snap
	return func() {
		securityCache.mu.Lock()
		defer securityCache.mu.Unlock()
		securityCache.statFn = statFn
		securityCache.runWithOutput = runWithOutput
		securityCache.ttl = ttl
		securityCache.at = at
		securityCache.snap = snap
	}
}
