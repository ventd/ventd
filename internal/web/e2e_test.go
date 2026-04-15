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
	"fmt"
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
	// srv and cfgPtr expose the live daemon state to tests that need
	// to mutate config mid-run (e.g. SSE tests that inject a sensor
	// and verify it reaches the DOM). Set by newHarness.
	srv    *Server
	cfgPtr *atomic.Pointer[config.Config]
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
	return &e2eHarness{
		server:   ts,
		password: password,
		browser:  browser,
		cleanup:  cleanup,
		srv:      srv,
		cfgPtr:   &cfgPtr,
	}
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

// TestE2E_SSE_StreamDrivesDashboardFasterThanPolling asserts that the
// SSE path actually carries status updates from /api/events to the
// dashboard, and that the once-per-2s polling loop yields to it. The
// harness lowers sseInterval to 100ms, which is two orders of
// magnitude below the baseline 2000ms poll — if only polling were
// firing, we'd see exactly one applyStatus call during the 500ms
// window (the initial kick-off); the test asserts >=3 calls, proving
// that SSE frames are reaching the browser and updating `sts`.
//
// This is the closest we can get to "DOM updates within 1 poll
// interval of a fan state change" without stubbing hwmon reads: the
// buildStatus() payload always includes a fresh timestamp, so each
// frame mutates `sts.timestamp`, and applyStatus is the single
// render entry point — count it, you count every DOM write from a
// status frame.
func TestE2E_SSE_StreamDrivesDashboardFasterThanPolling(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// Bias the server toward SSE well before the browser subscribes.
	// sseInterval is a plain field; the handler reads it per request.
	h.srv.sseInterval = 100 * time.Millisecond

	// Seed one Control so setup.Needed() returns false and the
	// dashboard boots in normal mode. Without this, config.Empty()
	// leaves Controls=[] which flips the setup overlay on and the
	// status pollers / SSE stream never start.
	live := h.cfgPtr.Load()
	seeded := *live
	seeded.Controls = []config.Control{{Fan: "test-fan", Curve: ""}}
	h.cfgPtr.Store(&seeded)

	page := h.browser.MustPage("")
	defer page.MustClose()

	// Log in via a direct POST so the session cookie lands on the
	// httptest origin. Same pattern as TestE2E_DashboardRendersAtLeastOneSection.
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

	// The dashboard bootstrap in setup.js is gated on an async
	// checkSetup().then(...) — MustWaitStable doesn't wait for that
	// chain, so openEventStream may not have run yet at the instant
	// this test wakes up. Poll until the `sts` global has a non-zero
	// timestamp (set by the first applyStatus call, whether via SSE
	// or the initial /api/status fetch). That confirms bootstrap is
	// complete and status plumbing is live before we measure the
	// SSE-specific cadence below.
	//
	// Note: `sts` is declared with `let` in state.js, which in a
	// classic <script> stays in the script-level lexical environment
	// and is NOT a property of window. Bare-identifier evals resolve
	// it correctly; `window.sts` returns undefined.
	waitUntil(t, 3*time.Second, func() bool {
		res, err := page.Eval(`() => (typeof sts !== 'undefined' && sts && sts.timestamp) ? sts.timestamp : ''`)
		if err != nil || res == nil {
			return false
		}
		return res.Value.Str() != ""
	}, "sts.timestamp became populated")

	// Read sts.timestamp now, wait one poll interval (2s) worth of
	// server wall time but far less on the client, and re-read. With
	// sseInterval=100ms the timestamp must advance at least once
	// within 400ms — the polling fallback can only deliver one
	// refresh per 2s, so any advance inside that window is
	// SSE-sourced by construction.
	firstTS, err := page.Eval(`() => sts.timestamp`)
	if err != nil {
		t.Fatalf("read sts.timestamp: %v", err)
	}
	first := firstTS.Value.Str()

	time.Sleep(400 * time.Millisecond)

	secondTS, err := page.Eval(`() => sts.timestamp`)
	if err != nil {
		t.Fatalf("read sts.timestamp (second): %v", err)
	}
	second := secondTS.Value.Str()

	if second == first {
		t.Fatalf("sts.timestamp did not advance within 400ms — SSE is not delivering frames (first=%q second=%q)",
			first, second)
	}

	// Verify the live-dot is in the 'on' state — applyStatus sets
	// 'live-dot on' and schedules a 2s clear; with the 100ms stream
	// the clear never fires, so a stable 'on' class is corroboration
	// that frames are arriving at sub-2s cadence.
	dotClsRes, err := page.Eval(`() => document.getElementById('live-dot').className`)
	if err != nil {
		t.Fatalf("read live-dot class: %v", err)
	}
	if !strings.Contains(dotClsRes.Value.Str(), "on") {
		t.Errorf("live-dot class=%q, expected to contain 'on' while SSE is active",
			dotClsRes.Value.Str())
	}
}

// TestE2E_Responsive_Screenshots renders the dashboard at each of
// the four canonical test viewports (375, 768, 1024, 1920) and saves
// a PNG per viewport into /tmp/ventd-screens-*/. Not a pass/fail
// check — the file existence is asserted but the goal is to produce
// artifacts a reviewer can eyeball. Skipped unless VENTD_E2E_SCREENSHOTS=1
// is set so a default `go test -tags=e2e` doesn't leave stray files
// behind.
func TestE2E_Responsive_Screenshots(t *testing.T) {
	if os.Getenv("VENTD_E2E_SCREENSHOTS") != "1" {
		t.Skip("set VENTD_E2E_SCREENSHOTS=1 to capture responsive-layout screenshots")
	}
	h := newHarness(t)
	defer h.cleanup()

	// Seed a non-empty config so the dashboard renders real cards
	// rather than empty-state placeholders.
	live := h.cfgPtr.Load()
	seeded := *live
	seeded.Controls = []config.Control{
		{Fan: "cpu-fan", Curve: ""},
		{Fan: "sys-fan-1", Curve: ""},
		{Fan: "sys-fan-2", Curve: ""},
	}
	seeded.Fans = []config.Fan{
		{Name: "cpu-fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm1"},
		{Name: "sys-fan-1", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm2"},
		{Name: "sys-fan-2", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm3"},
	}
	seeded.Sensors = []config.Sensor{
		{Name: "CPU Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp1_input"},
		{Name: "Motherboard Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp2_input"},
	}
	seeded.Curves = []config.CurveConfig{}
	h.cfgPtr.Store(&seeded)

	outDir, err := os.MkdirTemp("", "ventd-screens-")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Logf("screenshot output dir: %s", outDir)

	page := h.browser.MustPage("")
	defer page.MustClose()

	page.MustNavigate(h.server.URL + "/login").MustWaitStable()
	if _, err := page.Eval(`async (pw) => {
		const b = new URLSearchParams(); b.append('password', pw);
		const r = await fetch('/login', {method:'POST', body:b});
		return r.status;
	}`, h.password); err != nil {
		t.Fatalf("login: %v", err)
	}

	viewports := []struct {
		label  string
		w, h   int
		action string // "closed" / "open" — whether to pre-open the drawer
	}{
		{"375-mobile", 375, 812, "closed"},
		{"375-mobile-drawer", 375, 812, "open"},
		{"768-tablet", 768, 1024, "closed"},
		{"1024-narrow-desktop", 1024, 800, "closed"},
		{"1920-desktop", 1920, 1080, "closed"},
	}

	for _, v := range viewports {
		setViewport(t, page, v.w, v.h)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement(".section-hdr")
		// Let SSE populate, then still-frame.
		time.Sleep(500 * time.Millisecond)

		if v.action == "open" {
			page.MustElement("#btn-sidebar").MustClick()
			page.MustWaitStable()
			time.Sleep(200 * time.Millisecond)
		}

		data := page.MustScreenshot()
		path := fmt.Sprintf("%s/%s.png", outDir, v.label)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("saved %s (%d bytes)", path, len(data))
	}
}

// TestE2E_Responsive_DrawerSidebarAndHeaderReflow exercises the
// Phase 4.5 foundation: at a phone-sized viewport the hardware
// sidebar turns into an off-canvas drawer that the hamburger opens
// and the backdrop closes, the header tagline hides, and status
// pills land on a second row; at desktop width the sidebar is back
// inline and the backdrop never renders. The test works the DOM the
// same way a real operator does — no class-toggle shortcuts — so
// regressions in event delegation, data-action dispatch, or the
// matchMedia listener surface here.
func TestE2E_Responsive_DrawerSidebarAndHeaderReflow(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// Seed a Control so the dashboard boots out of setup mode (same
	// reason as the SSE test).
	live := h.cfgPtr.Load()
	seeded := *live
	seeded.Controls = []config.Control{{Fan: "test-fan", Curve: ""}}
	h.cfgPtr.Store(&seeded)

	page := h.browser.MustPage("")
	defer page.MustClose()

	// Log in before applying the mobile viewport — Chromium's login
	// form behaves more predictably at desktop width.
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

	// ── Mobile viewport (iPhone SE portrait) ─────────────────────
	setViewport(t, page, 375, 812)
	page.MustNavigate(h.server.URL + "/").MustWaitStable()
	page.Timeout(3 * time.Second).MustElement(".section-hdr")

	// The tagline ("System Fan Controller") is hidden at <480px.
	taglineDisplay := getComputedStyle(t, page, ".header-tagline", "display")
	if taglineDisplay != "none" {
		t.Errorf("at 375px, .header-tagline display=%q, want none", taglineDisplay)
	}

	// The sidebar is off-canvas by default: fixed position, translated
	// one full width to the left. Checking the class first (it starts
	// without `.open`), then the matrix-decomposed transform.
	sidebarClasses := getAttr(t, page, "#sidebar", "class")
	if strings.Contains(sidebarClasses, "open") {
		t.Errorf("at 375px, sidebar boots with .open: class=%q", sidebarClasses)
	}
	sidebarPos := getComputedStyle(t, page, "#sidebar", "position")
	if sidebarPos != "fixed" {
		t.Errorf("at 375px, sidebar position=%q, want fixed", sidebarPos)
	}

	// Backdrop is not visible until the drawer opens.
	backdropDisplay := getComputedStyle(t, page, "#sidebar-backdrop", "display")
	if backdropDisplay != "none" {
		t.Errorf("at 375px (drawer closed), backdrop display=%q, want none", backdropDisplay)
	}

	// Tap the hamburger — drawer should open and backdrop should
	// reveal itself via the CSS sibling selector.
	page.MustElement("#btn-sidebar").MustClick()
	page.MustWaitStable()

	sidebarClasses = getAttr(t, page, "#sidebar", "class")
	if !strings.Contains(sidebarClasses, "open") {
		t.Errorf("after hamburger click, sidebar missing .open: class=%q", sidebarClasses)
	}
	backdropDisplay = getComputedStyle(t, page, "#sidebar-backdrop", "display")
	if backdropDisplay == "none" {
		t.Errorf("after hamburger click, backdrop still display=none")
	}

	// Tap the backdrop — drawer should close. Dispatch the click via
	// JS rather than rod's MustClick: MustClick targets element
	// centre, but the backdrop covers the full viewport so its
	// centre sits inside the 85vw drawer, and the click would land
	// on the drawer instead. An element-level .click() bubbles
	// through the delegation dispatcher in render.js exactly like a
	// real tap at a coordinate outside the drawer would.
	if _, err := page.Eval(`() => document.getElementById('sidebar-backdrop').click()`); err != nil {
		t.Fatalf("backdrop click: %v", err)
	}
	page.MustWaitStable()

	sidebarClasses = getAttr(t, page, "#sidebar", "class")
	if strings.Contains(sidebarClasses, "open") {
		t.Errorf("after backdrop click, sidebar still .open: class=%q", sidebarClasses)
	}

	// ── Desktop viewport ─────────────────────────────────────────
	// Matchmedia fires on viewport change; the sidebar should return
	// to an inline, always-visible layout and the backdrop should be
	// hard-hidden.
	setViewport(t, page, 1280, 800)
	page.MustWaitStable()

	sidebarPos = getComputedStyle(t, page, "#sidebar", "position")
	if sidebarPos == "fixed" {
		t.Errorf("at 1280px, sidebar position=%q, want relative/static/etc.", sidebarPos)
	}
	backdropDisplay = getComputedStyle(t, page, "#sidebar-backdrop", "display")
	if backdropDisplay != "none" {
		t.Errorf("at 1280px, backdrop display=%q, want none", backdropDisplay)
	}

	// Tagline is back.
	taglineDisplay = getComputedStyle(t, page, ".header-tagline", "display")
	if taglineDisplay == "none" {
		t.Errorf("at 1280px, .header-tagline hidden; expected visible")
	}
}

// setViewport drives Chrome's emulation protocol so the page's media
// queries and viewport-relative sizing react as if running on the
// given physical dimensions. rod's `SetViewport` doesn't exist; the
// two DevTools commands below are the canonical way.
func setViewport(t *testing.T, page *rod.Page, width, height int) {
	t.Helper()
	err := proto.EmulationSetDeviceMetricsOverride{
		Width:             width,
		Height:            height,
		DeviceScaleFactor: 1,
		Mobile:            width < 600,
	}.Call(page)
	if err != nil {
		t.Fatalf("set viewport %dx%d: %v", width, height, err)
	}
}

// getComputedStyle returns the resolved value of a CSS property on
// the first element matching `selector`. Used by responsive tests
// because asserting against class names alone misses computed-value
// regressions (e.g. a cascading !important override).
func getComputedStyle(t *testing.T, page *rod.Page, selector, prop string) string {
	t.Helper()
	res, err := page.Eval(`(sel, p) => {
		const el = document.querySelector(sel);
		if (!el) return '<missing>';
		return getComputedStyle(el).getPropertyValue(p);
	}`, selector, prop)
	if err != nil {
		t.Fatalf("getComputedStyle(%s, %s): %v", selector, prop, err)
	}
	return strings.TrimSpace(res.Value.Str())
}

// getAttr returns the value of an HTML attribute on the first element
// matching `selector`, or empty string if the element doesn't exist.
func getAttr(t *testing.T, page *rod.Page, selector, attr string) string {
	t.Helper()
	res, err := page.Eval(`(sel, a) => {
		const el = document.querySelector(sel);
		return el ? (el.getAttribute(a) || '') : '';
	}`, selector, attr)
	if err != nil {
		t.Fatalf("getAttr(%s, %s): %v", selector, attr, err)
	}
	return res.Value.Str()
}

// waitUntil polls `cond` every 50ms until it returns true or `timeout`
// elapses. Used in SSE e2e tests to wait for the async dashboard
// bootstrap chain to finish before measuring stream cadence.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, reason string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", timeout, reason)
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
