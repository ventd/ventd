//go:build e2e

// Package web end-to-end tests. These drive a real headless Chromium
// against the ventd HTTP handler chain to catch exactly the class of
// bug the unit tests cannot: "the server claims it sent the right bytes
// but the browser refuses to run them." Audit finding S1 was that shape
// — CSP self-blocked every inline <script> the daemon embedded — and
// the existing httptest-based suite gave it a clean bill of health
// because it never actually executed the UI.
//
// Build tag: only runs when explicitly requested via
//   go test -tags=e2e ./internal/web/...
// so `go test ./...` for contributors without a Chromium runtime stays
// cheap and hermetic. CI adds the tag on a dedicated job. rod downloads
// its own Chromium on first run, so the only apt-get requirement is
// the standard Chromium runtime libs (libnss3, libatk1.0-0, libxkbcommon0,
// etc.) which the CI workflow installs in one shot.
//
// Why an in-process server rather than a subprocess of the ventd
// binary as originally sketched: the daemon's first-boot code path
// walks /sys/class/hwmon at startup, which is a read-only sysfs mount
// that cannot be faked in a test environment. Standing up the Server
// struct directly against an httptest.NewServer exercises exactly the
// handler chain (securityHeaders → originCheck → mux → handlers) that
// ships in the binary, with the same embedded UI assets loaded through
// the same uiFS. What we lose is the main.go startup sequence — which
// is PR #21's territory and is covered by the separate 0e check in
// scripts/check_startup_latency.sh.

package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// e2eHarness wraps a live httptest.Server serving the full ventd
// middleware stack (CSP, origin check, sessions, embedded UI) plus a
// headless browser pointed at it. The password is pre-configured so
// tests exercise the happy login path without needing the setup-token
// flow.
type e2eHarness struct {
	server   *httptest.Server
	password string
	browser  *rod.Browser
	cleanup  func()
}

func newHarness(t *testing.T) *e2eHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	password := "testpass123!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	live := config.Empty()
	live.Web.PasswordHash = hash

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)

	ctx, cancel := context.WithCancel(context.Background())
	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	sm := setupmgr.New(cal, logger)
	restart := make(chan struct{}, 1)
	srv := New(ctx, &cfgPtr, t.TempDir()+"/config.yaml", logger, cal, sm, restart, "", hwdiag.NewStore())

	ts := httptest.NewServer(srv.handler)
	browser := newBrowser(t)

	cleanup := func() {
		browser.MustClose()
		ts.Close()
		cancel()
	}
	return &e2eHarness{server: ts, password: password, browser: browser, cleanup: cleanup}
}

// TestE2E_LoginFlowUnderDefaultCSP is the verification gate for audit
// finding S1. Before the extraction fix, this test fails: the daemon
// serves inline <script> blocks that its own CSP refuses to execute,
// so the login button handler never attaches and the click does
// nothing. The fix moves all JS into /ui/scripts/*.js files which are
// 'self'-origin and satisfy the CSP. This test asserts that the login
// page actually works in a browser that is doing nothing unusual — no
// CSP bypass flags, no custom headers.
func TestE2E_LoginFlowUnderDefaultCSP(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	page := h.browser.MustPage("")
	defer page.MustClose()

	// Install violation/error listeners BEFORE navigating so we don't
	// miss any events fired during initial asset load. rod's EachEvent
	// returns a `wait func()` that blocks until the callback returns
	// true or the page context cancels — we want a non-terminating
	// observer, so run each loop on its own goroutine and cancel on
	// test exit by closing the page.
	var (
		cspMu         sync.Mutex
		cspViolations []string
		consoleErrs   []string
	)
	go page.EachEvent(func(e *proto.LogEntryAdded) {
		if e.Entry.Source == proto.LogLogEntrySourceSecurity ||
			e.Entry.Source == proto.LogLogEntrySourceViolation {
			cspMu.Lock() //nolint:gocritic // observer writes accumulate under mutex
			cspViolations = append(cspViolations, e.Entry.Text)
			cspMu.Unlock()
		}
	})()
	go page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) {
		if e.Type == proto.RuntimeConsoleAPICalledTypeError {
			cspMu.Lock() //nolint:gocritic // observer writes accumulate under mutex
			consoleErrs = append(consoleErrs, rodSerializeArgs(e.Args))
			cspMu.Unlock()
		}
	})()
	// Enable the Log protocol domain that feeds the violation
	// listener; rod enables Runtime implicitly on first use.
	_ = proto.LogEnable{}.Call(page)

	page.MustNavigate(h.server.URL + "/login").MustWaitStable()

	// If the extracted scripts are not loading (wrong path, wrong MIME,
	// blocked by CSP) the login form's handlers never attach and the
	// #loginBtn click below silently no-ops.
	page.Timeout(3 * time.Second).MustElement("#password").MustInput(h.password)

	wait := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	page.MustElement("#loginBtn").MustClick()
	// Wait at most 5s for login.js to POST and redirect us to "/".
	// Running this in a goroutine and racing it against a timer keeps
	// the fail mode informative — a hang here means the click handler
	// never fired, which is exactly the CSP-blocked symptom we're
	// guarding against.
	done := make(chan struct{}, 1)
	go func() { wait(); done <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("no navigation after login click; console_errs=%v csp=%v",
			consoleErrs, cspViolations)
	}

	// After redirect we should be at "/" and the dashboard container
	// should have rendered. If app.js failed to load, the static HTML
	// still reaches the browser but the initial fetch cycle never
	// fires — asserting the section header is a narrow, stable check.
	finalURL := page.MustInfo().URL
	if !strings.HasSuffix(strings.TrimRight(finalURL, "/"), h.server.URL) {
		// strict-trailing-slash matching: httptest URL has no trailing
		// slash, post-login URL may or may not (browsers normalize).
		if finalURL != h.server.URL+"/" && finalURL != h.server.URL {
			t.Errorf("final URL=%q want %s/", finalURL, h.server.URL)
		}
	}
	page.Timeout(3 * time.Second).MustElement(".section")

	if len(cspViolations) != 0 {
		t.Errorf("CSP violations detected: %v", cspViolations)
	}
	// Filter console errors: a single favicon 404 is routine and
	// unrelated to CSP. The assertion is specifically about CSP-shaped
	// errors, which are the S1 regression signal.
	for _, e := range consoleErrs {
		if strings.Contains(e, "Content Security Policy") ||
			strings.Contains(e, "Refused to") {
			t.Errorf("CSP-ish console error: %s", e)
		}
	}
}

// TestE2E_DashboardRendersAtLeastOneSection is the smoke gate for the
// dashboard's own JS. Loading / after a successful login should
// produce a .section-hdr — that confirms the HTML document reached
// the browser under CSP and the embedded asset handler returned the
// real bytes (not a 404 or CSP-blocked response).
func TestE2E_DashboardRendersAtLeastOneSection(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	page := h.browser.MustPage("")
	defer page.MustClose()

	// Drive login via a direct fetch from the browser so the session
	// cookie ends up on the correct origin.
	page.MustNavigate(h.server.URL + "/login").MustWaitStable()
	res, err := page.Eval(`async (pw) => {
		const body = new URLSearchParams();
		body.append('password', pw);
		const r = await fetch('/login', { method: 'POST', body });
		return r.status;
	}`, h.password)
	if err != nil {
		t.Fatalf("login fetch: %v", err)
	}
	if st := res.Value.Int(); st != 200 {
		t.Fatalf("login POST status=%d want 200", st)
	}

	page.MustNavigate(h.server.URL + "/").MustWaitStable()
	page.Timeout(3 * time.Second).MustElement(".section-hdr")
}

// TestE2E_AuthStateProbeDoesNotLockOut exercises the end-to-end path
// for audit finding S2 at the browser layer. The old login page would
// POST an empty password to detect first-boot mode; after the fix the
// probe is a GET /api/auth/state that does NOT touch the rate limiter.
// We simulate 10 page loads in rapid succession (twice the default
// threshold) and then assert the real login still succeeds.
func TestE2E_AuthStateProbeDoesNotLockOut(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	page := h.browser.MustPage("")
	defer page.MustClose()

	for i := 0; i < 10; i++ {
		page.MustNavigate(h.server.URL + "/login").MustWaitStable()
	}

	// Now do a real login; if the probe had consumed attempts, this
	// would 429 and the navigation would not occur.
	page.MustElement("#password").MustInput(h.password)
	wait := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	page.MustElement("#loginBtn").MustClick()
	done := make(chan struct{}, 1)
	go func() { wait(); done <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("real login blocked after 10 probe page loads — S2 regression")
	}
}

// --- plumbing --------------------------------------------------------------

func newBrowser(t *testing.T) *rod.Browser {
	t.Helper()
	l := launcher.New().Headless(true).Leakless(false)
	// Chromium refuses to run as root without --no-sandbox (see
	// crbug.com/638180). CI and local dev-inside-Docker are both root;
	// the security trade-off of --no-sandbox is irrelevant for an
	// ephemeral test runner that only ever navigates to 127.0.0.1.
	if os.Geteuid() == 0 {
		l = l.NoSandbox(true)
	}
	// Operators in air-gapped sandboxes where rod's auto-download is
	// blocked can point us at a pre-installed Chromium via env.
	if p := os.Getenv("VENTD_E2E_CHROMIUM"); p != "" {
		l = l.Bin(p)
	}
	u, err := l.Launch()
	if err != nil {
		t.Skipf("rod launch failed (no Chromium available for e2e): %v", err)
	}
	return rod.New().ControlURL(u).MustConnect()
}

func rodSerializeArgs(args []*proto.RuntimeRemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == nil {
			continue
		}
		if a.Value.Nil() {
			parts = append(parts, a.Description)
		} else {
			parts = append(parts, a.Value.String())
		}
	}
	return strings.Join(parts, " ")
}
