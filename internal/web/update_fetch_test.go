package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// fetchTestServer is a tiny httptest harness: when the fetcher
// composes https://github.com/<repoSlug>/releases/download/<tag>/install.sh,
// we redirect that URL space to the test server by temporarily
// replacing updateHTTPClient with a Transport that rewrites the
// host. Cleaner than monkey-patching the whole URL builder.
type fetchTestServer struct {
	srv      *httptest.Server
	repoSlug string
}

func newTestHTTPServer(t *testing.T, body []byte) *fetchTestServer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &fetchTestServer{srv: srv, repoSlug: "test/repo"}
}

func (f *fetchTestServer) client() *http.Client {
	parsed, _ := url.Parse(f.srv.URL)
	rewrite := &rewriteTransport{rt: http.DefaultTransport, target: parsed}
	return &http.Client{Transport: rewrite}
}

type rewriteTransport struct {
	rt     http.RoundTripper
	target *url.URL
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return r.rt.RoundTrip(req)
}

// TestResolveInstallScript_FetchSucceeds — when the fetch hook
// returns a path, resolveInstallScript returns it directly without
// falling back to disk/embedded. This is the load-bearing happy
// path: every in-UI update from a v0.5.20+ daemon will exercise it.
func TestResolveInstallScript_FetchSucceeds(t *testing.T) {
	tmp, err := os.CreateTemp("", "ventd-test-fetch-*.sh")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_ = tmp.Close()

	got := resolveInstallScript("v0.5.99",
		func(version string) (string, error) {
			if version != "v0.5.99" {
				t.Errorf("fetch hook called with %q, want v0.5.99", version)
			}
			return tmp.Name(), nil
		},
		silentLogger(t))
	if got != tmp.Name() {
		t.Errorf("resolveInstallScript = %q, want %q (fetched path)", got, tmp.Name())
	}
}

// TestResolveInstallScript_FetchFailsFallsBack — when the fetch
// hook errors (network down, release-asset 404, body too short),
// resolveInstallScript falls through to findInstallScript() which
// covers the on-disk + embedded paths. The operator's update keeps
// working offline; only the "newest install.sh fixes" benefit is
// lost when the fetch fails.
func TestResolveInstallScript_FetchFailsFallsBack(t *testing.T) {
	if len(installShEmbedded) == 0 {
		t.Skip("no embedded install.sh in this build (need it for fallback)")
	}
	prevCands := updateInstallScriptCandidates
	updateInstallScriptCandidates = []string{"/nonexistent/ventd/install.sh"}
	t.Cleanup(func() { updateInstallScriptCandidates = prevCands })

	got := resolveInstallScript("v0.5.99",
		func(version string) (string, error) {
			return "", errors.New("synthetic fetch failure")
		},
		silentLogger(t))
	if got == "" {
		t.Fatal("resolveInstallScript returned empty; expected embed fallback path")
	}
	t.Cleanup(func() { _ = os.Remove(got) })

	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	if len(body) != len(installShEmbedded) {
		t.Errorf("fallback body bytes=%d, want %d (matches embedded)", len(body), len(installShEmbedded))
	}
}

// TestFetchInstallScriptForVersion_RejectsShortBody — sub-256-byte
// responses are almost certainly an error page (404 release-asset-
// missing, GitHub maintenance page, etc). The fetch must reject
// them so the apply handler falls back to the embedded copy
// instead of materialising garbage to disk + executing it.
func TestFetchInstallScriptForVersion_RejectsShortBody(t *testing.T) {
	srv := newTestHTTPServer(t, []byte("not really install.sh\n"))
	prev := updateHTTPClient
	updateHTTPClient = srv.client()
	t.Cleanup(func() { updateHTTPClient = prev })
	prevSlug := updateRepoSlug
	updateRepoSlug = srv.repoSlug
	t.Cleanup(func() { updateRepoSlug = prevSlug })

	_, err := fetchInstallScriptForVersion(srv.repoSlug, "v0.5.99")
	if err == nil {
		t.Fatal("expected error for short body, got nil")
	}
	if !strings.Contains(err.Error(), "suspiciously short") {
		t.Errorf("err = %v, want contains 'suspiciously short'", err)
	}
}

// TestFetchInstallScriptForVersion_RejectsNoShebang — a 256+ byte
// body that doesn't start with #! is almost certainly HTML (the
// release page itself, served by mistake). Reject before
// materialising-and-executing.
func TestFetchInstallScriptForVersion_RejectsNoShebang(t *testing.T) {
	body := []byte("<!DOCTYPE html><html><body>Release page would render here…\n" +
		strings.Repeat("filler ", 50) + "</body></html>\n")
	srv := newTestHTTPServer(t, body)
	prev := updateHTTPClient
	updateHTTPClient = srv.client()
	t.Cleanup(func() { updateHTTPClient = prev })
	prevSlug := updateRepoSlug
	updateRepoSlug = srv.repoSlug
	t.Cleanup(func() { updateRepoSlug = prevSlug })

	_, err := fetchInstallScriptForVersion(srv.repoSlug, "v0.5.99")
	if err == nil {
		t.Fatal("expected error for non-script body, got nil")
	}
	if !strings.Contains(err.Error(), "shebang") {
		t.Errorf("err = %v, want contains 'shebang'", err)
	}
}

// TestRealUpdateRun_PassesSkipChecksEnv pins the contract that the
// daemon's apply handler tells install.sh which preflight checks to
// skip via VENTD_SKIP_PREFLIGHT_CHECKS. We can't intercept env
// across exec.Command's nohup spawn easily, so this test asserts on
// the constant + the formatting of the command line.
func TestRealUpdateRun_PassesSkipChecksEnv(t *testing.T) {
	// The constant lists the build-only checks; the apply handler
	// should pass them. Spot-check the most important ones (DKMS +
	// build tools) since those are the ones that fired on Phoenix's
	// HIL grid.
	for _, must := range []string{"dkms_missing", "gcc_missing", "kernel_headers_missing", "make_missing"} {
		if !strings.Contains(inUIUpdateSkipChecks, must) {
			t.Errorf("inUIUpdateSkipChecks missing %q (operator HIL hosts blocked on it)", must)
		}
	}
}

// silentLogger returns a *slog.Logger that discards everything so
// tests don't print extraneous "fetched from release" log lines.
func silentLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// TestFetchLatestRelease_RetriesTransient5xx — first 2 attempts
// return 503; third returns 200 with a valid body. fetchLatestRelease
// must retry and eventually succeed, exercising the #974 hardening.
func TestFetchLatestRelease_RetriesTransient5xx(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.5.27","published_at":"2026-05-08T00:00:00Z","html_url":"https://example/r"}`))
	}))
	t.Cleanup(srv.Close)

	parsed, _ := url.Parse(srv.URL)
	prevClient := updateHTTPClient
	updateHTTPClient = &http.Client{Transport: &rewriteTransport{rt: http.DefaultTransport, target: parsed}}
	t.Cleanup(func() { updateHTTPClient = prevClient })
	prevSlug := updateRepoSlug
	updateRepoSlug = "test/repo"
	t.Cleanup(func() { updateRepoSlug = prevSlug })
	prevSleep := fetchRetrySleep
	fetchRetrySleep = func(time.Duration) {} // no-op so the test runs fast
	t.Cleanup(func() { fetchRetrySleep = prevSleep })

	tag, _, _, err := fetchLatestRelease("test/repo")
	if err != nil {
		t.Fatalf("expected success after 3 attempts, got err: %v", err)
	}
	if tag != "v0.5.27" {
		t.Errorf("tag = %q, want v0.5.27", tag)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (2 transient + 1 success)", attempts)
	}
}

// TestFetchLatestRelease_DoesNotRetryOn4xx — a 404 (release missing)
// is terminal; the retry loop must NOT spin on it. Catches the
// regression where a recent enough release.yml hasn't been published
// and we'd otherwise hammer GitHub for nothing.
func TestFetchLatestRelease_DoesNotRetryOn4xx(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	parsed, _ := url.Parse(srv.URL)
	prevClient := updateHTTPClient
	updateHTTPClient = &http.Client{Transport: &rewriteTransport{rt: http.DefaultTransport, target: parsed}}
	t.Cleanup(func() { updateHTTPClient = prevClient })
	prevSlug := updateRepoSlug
	updateRepoSlug = "test/repo"
	t.Cleanup(func() { updateRepoSlug = prevSlug })
	prevSleep := fetchRetrySleep
	fetchRetrySleep = func(time.Duration) {}
	t.Cleanup(func() { fetchRetrySleep = prevSleep })

	_, _, _, err := fetchLatestRelease("test/repo")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (4xx is terminal, no retry)", attempts)
	}
}

// TestFetchLatestRelease_ExhaustsAttemptsOnPersistentTransient — all
// 3 attempts return 503; we surface the last error after exhausting
// the budget.
func TestFetchLatestRelease_ExhaustsAttemptsOnPersistentTransient(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	parsed, _ := url.Parse(srv.URL)
	prevClient := updateHTTPClient
	updateHTTPClient = &http.Client{Transport: &rewriteTransport{rt: http.DefaultTransport, target: parsed}}
	t.Cleanup(func() { updateHTTPClient = prevClient })
	prevSleep := fetchRetrySleep
	fetchRetrySleep = func(time.Duration) {}
	t.Cleanup(func() { fetchRetrySleep = prevSleep })

	_, _, _, err := fetchLatestRelease("test/repo")
	if err == nil {
		t.Fatal("expected error after 3 transient failures, got nil")
	}
	if attempts != fetchRetryAttempts {
		t.Errorf("attempts = %d, want %d", attempts, fetchRetryAttempts)
	}
}

// TestBuildUpdateCmd_SystemdRun_IncludesRuntimeMaxSec pins the #975
// hardening: when systemd-run is the spawn shape, the unit must
// declare RuntimeMaxSec=600 so a wedged install.sh is killed by
// systemd rather than holding /usr/local/bin under install
// indefinitely.
func TestBuildUpdateCmd_SystemdRun_IncludesRuntimeMaxSec(t *testing.T) {
	prev := systemdRunPath
	systemdRunPath = func() string { return "/usr/bin/systemd-run" }
	t.Cleanup(func() { systemdRunPath = prev })
	prevAvail := systemdAvailable
	systemdAvailable = func() bool { return true }
	t.Cleanup(func() { systemdAvailable = prevAvail })

	cmd := buildUpdateCmd("v0.5.99", "/tmp/install.sh")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--property=RuntimeMaxSec=600") {
		t.Errorf("systemd-run args missing --property=RuntimeMaxSec=600: %s", args)
	}
}

// TestBuildUpdateCmd_SystemdRun_IncludesPrivateTmp pins the Fedora
// in-UI-update fix: the transient unit must run with PrivateTmp=yes
// so install.sh gets a fresh /tmp namespace, isolating it from any
// stale host-side debris at a fixed temp path. The original bug was
// a 0-byte phoenix-owned /tmp/ventd-preflight.json that, under SELinux
// Enforcing, caused install.sh's redirect to fail with EACCES and the
// daemon to wedge at v0.9.0 indefinitely.
func TestBuildUpdateCmd_SystemdRun_IncludesPrivateTmp(t *testing.T) {
	prev := systemdRunPath
	systemdRunPath = func() string { return "/usr/bin/systemd-run" }
	t.Cleanup(func() { systemdRunPath = prev })
	prevAvail := systemdAvailable
	systemdAvailable = func() bool { return true }
	t.Cleanup(func() { systemdAvailable = prevAvail })

	cmd := buildUpdateCmd("v0.5.99", "/tmp/install.sh")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--property=PrivateTmp=yes") {
		t.Errorf("systemd-run args missing --property=PrivateTmp=yes: %s", args)
	}
}

// TestBuildUpdateCmd_NohupFallback_IncludesTimeout pins that the
// non-systemd fallback wraps install.sh in `timeout 600` so a
// wedged update doesn't leave the daemon offline forever on OpenRC
// / runit hosts (#975 mirror).
func TestBuildUpdateCmd_NohupFallback_IncludesTimeout(t *testing.T) {
	prev := systemdRunPath
	systemdRunPath = func() string { return "" } // forces fallback
	t.Cleanup(func() { systemdRunPath = prev })

	cmd := buildUpdateCmd("v0.5.99", "/tmp/install.sh")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "timeout 600 bash") {
		t.Errorf("nohup fallback missing 'timeout 600 bash' wrap: %s", args)
	}
}
