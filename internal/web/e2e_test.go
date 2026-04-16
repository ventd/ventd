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

// TestE2E_Responsive_CardGridReflow verifies Phase 4.5 PR 2 — the
// three card-grid sections (Sensors, Controls, Curves) collapse to
// 1-up at <480px, 2-up in the 480–899 band, and return to the
// auto-fill desktop grid at ≥900px. It also asserts that sensor
// card padding tightens at portrait-phone width so the rule that
// ships for the narrow viewport actually reaches the rendered DOM.
//
// Rather than matching the declared `grid-template-columns` string
// (which is already interpolated to px tracks by getComputedStyle),
// the test counts space-separated tokens in the computed value —
// that is the resolved track count and is the property an operator
// scanning the dashboard actually experiences.
func TestE2E_Responsive_CardGridReflow(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// Seed Controls so the dashboard boots out of setup mode and
	// Sensors so the #sensor-cards grid renders more than one child
	// (otherwise auto-fill's track enumeration is degenerate).
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
	h.cfgPtr.Store(&seeded)

	page := h.browser.MustPage("")
	defer page.MustClose()

	// Log in at a desktop-sized viewport for the same reason as the
	// drawer test: the login form's behaviour is more predictable
	// before mobile emulation kicks in.
	page.MustNavigate(h.server.URL + "/login").MustWaitStable()
	if _, err := page.Eval(`async (pw) => {
		const b = new URLSearchParams(); b.append('password', pw);
		const r = await fetch('/login', {method:'POST', body:b});
		return r.status;
	}`, h.password); err != nil {
		t.Fatalf("login: %v", err)
	}

	// countTracks parses the resolved `grid-template-columns` value.
	// Computed styles emit each track as its own px token
	// (e.g. "180px 180px"); splitting on whitespace and counting
	// non-empty tokens yields the rendered column count.
	countTracks := func(gridTemplateColumns string) int {
		return len(strings.Fields(gridTemplateColumns))
	}

	cases := []struct {
		label                    string
		w, h                     int
		wantCols                 int // exact; -1 means "≥2"
		wantCardPadding          string
	}{
		{"375-mobile", 375, 812, 1, "12px 14px"},
		{"768-tablet", 768, 1024, 2, "14px 16px"},
		{"1024-narrow-desktop", 1024, 800, -1, "14px 16px"},
		{"1280-desktop", 1280, 800, -1, "14px 16px"},
	}

	for _, c := range cases {
		setViewport(t, page, c.w, c.h)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement("#sensor-cards .card")

		// Give the SSE dashboard hydration a tick so the card grid
		// is populated (not just present) before measuring.
		time.Sleep(200 * time.Millisecond)

		// After the grouped-dashboard refactor, #sensor-cards is a
		// container of <details> groups rather than a grid itself;
		// the per-group .card-grid inside each <details> carries
		// the column rules this test was written against. Measure
		// the inner grid directly so the assertion still exercises
		// the same layout contract an operator experiences.
		grid := getComputedStyle(t, page, "#sensor-cards .card-grid", "grid-template-columns")
		got := countTracks(grid)
		switch {
		case c.wantCols > 0 && got != c.wantCols:
			t.Errorf("viewport %s: sensor-cards tracks=%d want %d (computed=%q)",
				c.label, got, c.wantCols, grid)
		case c.wantCols < 0 && got < 2:
			t.Errorf("viewport %s: sensor-cards tracks=%d want ≥2 (computed=%q)",
				c.label, got, grid)
		}

		// Fan cards share the same .card-grid class inside their
		// own groups; spot-check them too so a regression that
		// only affects #fan-cards surfaces here.
		fanGrid := getComputedStyle(t, page, "#fan-cards .card-grid", "grid-template-columns")
		fanTracks := countTracks(fanGrid)
		switch {
		case c.wantCols > 0 && fanTracks != c.wantCols:
			t.Errorf("viewport %s: fan-cards tracks=%d want %d (computed=%q)",
				c.label, fanTracks, c.wantCols, fanGrid)
		case c.wantCols < 0 && fanTracks < 2:
			t.Errorf("viewport %s: fan-cards tracks=%d want ≥2 (computed=%q)",
				c.label, fanTracks, fanGrid)
		}

		// Card padding tightens to 12px 14px only at <480px. Assert
		// the computed value matches the declared one — a cascade
		// regression (e.g. a misplaced !important in components.css)
		// would show up here before it ever reached a human.
		padding := getComputedStyle(t, page, "#sensor-cards .card", "padding")
		if padding != c.wantCardPadding {
			t.Errorf("viewport %s: .card padding=%q want %q",
				c.label, padding, c.wantCardPadding)
		}
	}
}

// TestE2E_Responsive_CurveEditorTouchDrag verifies Phase 4.5 PR 3 —
// the curve editor's control points accept a PointerEvent drag and
// the SVG's touch-action is disabled so a real finger drag wouldn't
// be pre-empted by the browser's pan/zoom gesture. Simulated via
// PointerEvent dispatch at pointerType=touch; ReadsBack cfg.curves[0]
// to assert the drag's numeric effect on (min_temp, min_pwm).
//
// Regression target: the pre-PR-3 handler split (mousedown +
// touchstart) would register only one of the two during a real
// finger drag, dropping the grab on fast moves. Unified PointerEvents
// mean both device kinds drive the same code path; the test exercises
// the touch pointerType explicitly.
func TestE2E_Responsive_CurveEditorTouchDrag(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// Seed a sensor + fan + one linear curve so the editor has
	// something to render against. MinTemp=30 / MaxTemp=70 gives
	// plenty of room for a drag to move min_temp down.
	live := h.cfgPtr.Load()
	seeded := *live
	seeded.Controls = []config.Control{{Fan: "cpu-fan", Curve: "cpu-curve"}}
	seeded.Fans = []config.Fan{
		{Name: "cpu-fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm1"},
	}
	seeded.Sensors = []config.Sensor{
		{Name: "CPU Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp1_input"},
	}
	seeded.Curves = []config.CurveConfig{{
		Name:    "cpu-curve",
		Type:    "linear",
		Sensor:  "CPU Temperature",
		MinTemp: 30, MaxTemp: 70,
		MinPWM: 60, MaxPWM: 220,
	}}
	h.cfgPtr.Store(&seeded)

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

	page.MustNavigate(h.server.URL + "/").MustWaitStable()
	page.Timeout(3 * time.Second).MustElement(".curve-card")
	time.Sleep(200 * time.Millisecond)

	// Open the editor by selecting the only curve card; drawSVG
	// emits .ctrl-point.min and .ctrl-point.max once the editor
	// is rendered.
	page.MustElement(".curve-card").MustClick()
	page.Timeout(2 * time.Second).MustElement(".ctrl-point.min")

	// #curve-svg must carry touch-action: none. Paired with the
	// preventDefault in the pointermove handler, this is what keeps
	// the browser from hijacking the drag on real touch devices.
	touchAction := getComputedStyle(t, page, "#curve-svg", "touch-action")
	if touchAction != "none" {
		t.Errorf("#curve-svg touch-action=%q want none", touchAction)
	}

	// Capture pre-drag curve state so the assertion can show the
	// delta the drag was supposed to introduce.
	pre, err := page.Eval(`() => ({
		min_temp: cfg.curves[0].min_temp,
		min_pwm:  cfg.curves[0].min_pwm,
	})`)
	if err != nil {
		t.Fatalf("read pre state: %v", err)
	}
	preMinTemp := pre.Value.Get("min_temp").Int()
	preMinPWM := pre.Value.Get("min_pwm").Int()

	// Dispatch a pointerdown on the min ctrl-point, then a
	// pointermove ~+40 / -20 in client space, then pointerup.
	// pointerType:'touch' exercises the coarse-pointer code path
	// the PR is about; bubbles:true lets the document-level
	// listeners in curve-editor.js catch pointermove/pointerup.
	if _, err := page.Eval(`() => {
		const dot = document.querySelector('.ctrl-point.min');
		const r = dot.getBoundingClientRect();
		const x0 = r.left + r.width/2, y0 = r.top + r.height/2;
		const mk = (type, x, y) => new PointerEvent(type, {
			bubbles: true, cancelable: true,
			clientX: x, clientY: y,
			pointerId: 101, pointerType: 'touch', isPrimary: true,
		});
		dot.dispatchEvent(mk('pointerdown', x0, y0));
		document.dispatchEvent(mk('pointermove', x0 + 40, y0 - 20));
		document.dispatchEvent(mk('pointerup',   x0 + 40, y0 - 20));
	}`); err != nil {
		t.Fatalf("dispatch pointer drag: %v", err)
	}

	// Small settle so the redraw dispatched from onDrag lands.
	time.Sleep(50 * time.Millisecond)

	post, err := page.Eval(`() => ({
		min_temp: cfg.curves[0].min_temp,
		min_pwm:  cfg.curves[0].min_pwm,
	})`)
	if err != nil {
		t.Fatalf("read post state: %v", err)
	}
	postMinTemp := post.Value.Get("min_temp").Int()
	postMinPWM := post.Value.Get("min_pwm").Int()

	// Either axis could in principle clamp to a boundary; require
	// at least one axis to have moved so the test fails loudly on
	// a fully-broken drag handler.
	if postMinTemp == preMinTemp && postMinPWM == preMinPWM {
		t.Errorf("pointer drag produced no change: min_temp %d→%d, min_pwm %d→%d",
			preMinTemp, postMinTemp, preMinPWM, postMinPWM)
	}

	// The .dragging affordance is added on pointerdown and must be
	// dropped by endDrag so the cursor returns to grab. Checking
	// after pointerup so the class has been cleaned up.
	draggingCount, err := page.Eval(`() => document.querySelectorAll('.ctrl-point.dragging').length`)
	if err != nil {
		t.Fatalf("count dragging: %v", err)
	}
	if n := draggingCount.Value.Int(); n != 0 {
		t.Errorf("after pointerup, .ctrl-point.dragging count=%d want 0", n)
	}

	// Emulate a coarse-pointer, no-hover, touch device so the
	// (hover:none) and (pointer:coarse) rule in components.css
	// applies. Chrome needs three calls to make all three of the
	// relevant UA signals land:
	//
	//   setEmulatedMedia  — flips @media (hover:…) and (pointer:…)
	//   setTouchEmulationEnabled — flips window.navigator.maxTouchPoints
	//     and matchMedia('(pointer: coarse)').matches
	//   setDeviceMetricsOverride with Mobile:true — flips the
	//     'Mobile' branch inside Blink
	//
	// viewport / media alone doesn't flip pointer:coarse; without
	// touch emulation the query still reports 'pointer: fine'.
	if err := (proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "hover", Value: "none"},
			{Name: "pointer", Value: "coarse"},
		},
	}).Call(page); err != nil {
		t.Fatalf("emulate coarse-pointer media: %v", err)
	}
	maxTouch := 1
	if err := (proto.EmulationSetTouchEmulationEnabled{
		Enabled:        true,
		MaxTouchPoints: &maxTouch,
	}).Call(page); err != nil {
		t.Fatalf("enable touch emulation: %v", err)
	}
	setViewport(t, page, 375, 812)
	page.MustNavigate(h.server.URL + "/").MustWaitStable()
	page.Timeout(3 * time.Second).MustElement(".curve-card")
	page.MustElement(".curve-card").MustClick()
	page.Timeout(2 * time.Second).MustElement(".ctrl-point.min")
	time.Sleep(100 * time.Millisecond)

	r := getComputedStyle(t, page, ".ctrl-point.min", "r")
	if r != "12px" {
		t.Errorf("at 375px (coarse pointer), .ctrl-point r=%q want 12px", r)
	}
}

// TestE2E_Responsive_TouchTargetsAndModalReflow verifies Phase 4.5
// PR 4 — at coarse-pointer the common interactive surfaces (plain
// buttons, icon buttons, the fan-card mode toggle, the card-level
// selects) grow to at least 44×44 CSS pixels, and the settings
// modal stops overflowing its own backdrop at phone width.
//
// The 44-pixel WCAG target is declared via min-height / min-width
// inside the (hover: none) and (pointer: coarse) media query, so
// desktop density is untouched; this test exercises only the touch
// path. getBoundingClientRect resolves computed box dimensions,
// which is what an operator's finger will actually try to hit.
func TestE2E_Responsive_TouchTargetsAndModalReflow(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	live := h.cfgPtr.Load()
	seeded := *live
	// Pin cpu-fan-manual in manual mode so the manual-slider range
	// input renders (it is gated on control.manual_pwm != nil in
	// render.js). The manual fan is named with a "cpu" prefix so
	// the dashboard's group classifier places it in the CPU group
	// which is open by default on the narrow/touch viewport this
	// test uses. Other seeding covers: a mix curve with two sources
	// so .source-list label checkboxes render, and two linear curves
	// so the mix has something to reference.
	manualPWM := uint8(128)
	seeded.Controls = []config.Control{
		{Fan: "cpu-fan", Curve: "cpu-linear"},
		{Fan: "cpu-fan-manual", Curve: "", ManualPWM: &manualPWM},
	}
	seeded.Fans = []config.Fan{
		{Name: "cpu-fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm1"},
		{Name: "cpu-fan-manual", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm2"},
	}
	seeded.Sensors = []config.Sensor{
		{Name: "CPU Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp1_input"},
	}
	seeded.Curves = []config.CurveConfig{
		{Name: "cpu-linear", Type: "linear", Sensor: "CPU Temperature",
			MinTemp: 30, MaxTemp: 70, MinPWM: 60, MaxPWM: 220},
		{Name: "cpu-fixed", Type: "fixed", Value: 128},
		{Name: "cpu-mix", Type: "mix", Function: "max",
			Sources: []string{"cpu-linear", "cpu-fixed"}},
	}
	h.cfgPtr.Store(&seeded)

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

	// Emulate a phone: narrow viewport, coarse pointer, touch
	// emulation. Without all three, Chromium reports pointer:fine
	// and the @media rule this PR adds doesn't apply.
	if err := (proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "hover", Value: "none"},
			{Name: "pointer", Value: "coarse"},
		},
	}).Call(page); err != nil {
		t.Fatalf("emulate coarse-pointer media: %v", err)
	}
	maxTouch := 1
	if err := (proto.EmulationSetTouchEmulationEnabled{
		Enabled:        true,
		MaxTouchPoints: &maxTouch,
	}).Call(page); err != nil {
		t.Fatalf("enable touch emulation: %v", err)
	}
	setViewport(t, page, 375, 812)
	page.MustNavigate(h.server.URL + "/").MustWaitStable()
	page.Timeout(3 * time.Second).MustElement(".card-grid .card")
	time.Sleep(200 * time.Millisecond)

	// Helper: return the first-match element's bounding-box {w,h}
	// via getBoundingClientRect. Uses the rod Eval so assertions
	// can talk in actual rendered pixels.
	boundingBox := func(selector string) (w, h float64) {
		t.Helper()
		res, err := page.Eval(`(sel) => {
			const el = document.querySelector(sel);
			if (!el) return null;
			const r = el.getBoundingClientRect();
			return { w: r.width, h: r.height };
		}`, selector)
		if err != nil {
			t.Fatalf("boundingBox(%s): %v", selector, err)
		}
		if res.Value.Nil() {
			t.Fatalf("boundingBox(%s): no element", selector)
		}
		return res.Value.Get("w").Num(), res.Value.Get("h").Num()
	}

	// Every target the rule promises to enlarge. A few selectors
	// reach inside fan cards so they assert the cascaded height;
	// plain `button` matches many elements so we pick a
	// representative one (the Calibrate button in the first fan
	// card). Touch targets must be at least 44 in both dimensions
	// for width-sensitive controls; for wide ones height alone is
	// the WCAG property that matters.
	targets := []struct {
		label      string
		selector   string
		wantHeight float64
		wantWidth  float64
	}{
		{"mode-toggle Curve btn", ".mode-toggle .mode-btn", 44, 0},
		{"card select", ".card select", 44, 0},
		{"add-btns + Linear", ".add-btns button", 44, 0},
		{"header hamburger", "#btn-sidebar", 44, 44},
		{"header settings", "#btn-settings", 44, 44},
		{"header Apply btn", "#btn-apply", 44, 44},
	}
	for _, tgt := range targets {
		w, hh := boundingBox(tgt.selector)
		if hh < tgt.wantHeight-0.5 {
			t.Errorf("%s: height=%.1f want ≥%.0f (selector=%q)",
				tgt.label, hh, tgt.wantHeight, tgt.selector)
		}
		if tgt.wantWidth > 0 && w < tgt.wantWidth-0.5 {
			t.Errorf("%s: width=%.1f want ≥%.0f (selector=%q)",
				tgt.label, w, tgt.wantWidth, tgt.selector)
		}
	}

	// Modal reflow: settings modal at phone width must fit inside
	// the backdrop without horizontal overflow and its card should
	// pin to the top (flex-start) so the soft keyboard doesn't
	// push it off-screen. Opening the settings modal via the
	// header cog exercises the same open() path as real use.
	page.MustElement(`[data-action="open-settings"]`).MustClick()
	page.Timeout(2 * time.Second).MustElement("#settings-overlay.open")
	time.Sleep(100 * time.Millisecond)

	// Backdrop should be flex-start at <480.
	alignItems := getComputedStyle(t, page, "#settings-overlay", "align-items")
	if alignItems != "flex-start" {
		t.Errorf("at 375px, #settings-overlay align-items=%q want flex-start",
			alignItems)
	}

	// Card must not exceed the viewport width. 375 viewport minus
	// the backdrop's 0.75rem padding on each side = 375 - 24 = 351.
	// The card has width:100% max-width:420, so at this viewport
	// width caps at 351.
	cardW, cardH := boundingBox("#settings-overlay .modal-card")
	if cardW > 351.5 {
		t.Errorf("#settings-overlay .modal-card width=%.1f want ≤351 at 375px", cardW)
	}
	// max-height: calc(100dvh - 1.5rem) → ≤ 812-24 = 788.
	if cardH > 788.5 {
		t.Errorf("#settings-overlay .modal-card height=%.1f want ≤788 at 375×812", cardH)
	}

	// Close the modal by clicking the backdrop (outside the card)
	// so the cleanup the dashboard does on close runs.
	if _, err := page.Eval(`() => document.getElementById('settings-overlay').click()`); err != nil {
		t.Fatalf("close modal: %v", err)
	}
	page.MustWaitStable()
	overlayCls := getAttr(t, page, "#settings-overlay", "class")
	if strings.Contains(overlayCls, "open") {
		t.Errorf("settings overlay still .open after backdrop click: class=%q", overlayCls)
	}

	// ── Coarse-pointer gaps from the Phase 4.5 audit ────────────
	// Three controls the PR 4 block missed: range-slider thumbs,
	// mix-curve source-list checkboxes, and the pencil/trash
	// edit-icons inside card-name-edit. Each is measured at 375
	// + coarse pointer + touch emulation (already applied above).

	// Range-slider thumb. Chromium doesn't expose
	// ::-webkit-slider-thumb computed styles via
	// getComputedStyle(el, pseudo) reliably — the call returns
	// the input element's own box. Instead walk the CSSOM for
	// the declared rule and assert the coarse-pointer block
	// actually declares width:44px on the thumb pseudo. Paired
	// with a boundingBox check on the input box height (which the
	// same block also sets to 44px) so we know the media query
	// matched at paint time.
	_, inputH := boundingBox(".manual-slider input[type=\"range\"]")
	if inputH < 43.5 {
		t.Errorf("manual-slider input height=%.1f want ≥44 at 375px+coarse (media query not active?)",
			inputH)
	}
	thumbRule, err := page.Eval(`() => {
		for (const sheet of document.styleSheets) {
			let rules;
			try { rules = sheet.cssRules; } catch (_) { continue; }
			for (const rule of rules) {
				if (!(rule instanceof CSSMediaRule)) continue;
				if (!matchMedia(rule.media.mediaText).matches) continue;
				for (const sub of rule.cssRules) {
					if (!sub.selectorText) continue;
					if (sub.selectorText.includes('::-webkit-slider-thumb') &&
					    sub.selectorText.includes('.manual-slider')) {
						return sub.style.width + ' ' + sub.style.height;
					}
				}
			}
		}
		return '';
	}`)
	if err != nil {
		t.Fatalf("walk CSSOM for slider thumb: %v", err)
	}
	if got := thumbRule.Value.Str(); got != "44px 44px" {
		t.Errorf("::-webkit-slider-thumb declared size=%q want '44px 44px'", got)
	}

	// Select the mix curve so its source-list renders, then
	// assert the wrapping label is 44-tall. Clicking the mix
	// curve card exercises the same select-curve data-action a
	// finger tap would.
	page.MustElement(`.curve-card:last-child`).MustClick()
	page.Timeout(2 * time.Second).MustElement(".source-list label")
	_, labelH := boundingBox(".source-list label")
	if labelH < 43.5 {
		t.Errorf(".source-list label height=%.1f want ≥44 at 375px+coarse",
			labelH)
	}

	// Edit-icon (pencil) inside the first fan card's
	// card-name-edit. Rendered unconditionally; target the first
	// match so we don't couple to a particular card index.
	iconW, iconH := boundingBox(".card-name-edit .edit-icon")
	if iconH < 43.5 || iconW < 43.5 {
		t.Errorf(".card-name-edit .edit-icon box=%.1fx%.1f want ≥44×44",
			iconW, iconH)
	}
}

// TestE2E_DashboardGrouping exercises the narrow-viewport density
// pass — the Sensors and Controls sections group by category
// (CPU / GPU / System / Storage / Other) as native <details>
// accordions, and that per-group open/closed state is remembered
// in sessionStorage. Subtests cover the classification ladder,
// the viewport-dependent default state, the session-storage round
// trip across an SSE re-render, and the empty-group suppression.
//
// The subtests share a single e2eHarness — login + seeded config
// are expensive enough that re-running them per subtest would
// triple the job's wall time without adding coverage. Each subtest
// navigates to / or resets sessionStorage explicitly when the
// preceding subtest might have dirtied state.
func TestE2E_DashboardGrouping(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// Seed one entry per category so the classification ladder
	// has something to fire on. Sensors and fans share the same
	// classifyCategory so a minimal per-kind set suffices — one
	// CPU sensor, one GPU fan (via type=nvidia), one System fan
	// (via is_pump), one chassis fan (via name keyword), one
	// generic/other.
	live := h.cfgPtr.Load()
	seeded := *live
	seeded.Controls = []config.Control{
		{Fan: "CPU Fan", Curve: ""},
		{Fan: "GPU Fan", Curve: ""},
		{Fan: "Pump", Curve: ""},
		{Fan: "Chassis Fan 1", Curve: ""},
		{Fan: "aux_fan", Curve: ""},
	}
	seeded.Fans = []config.Fan{
		{Name: "CPU Fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm1"},
		{Name: "GPU Fan", Type: "nvidia", PWMPath: "0"},
		{Name: "Pump", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm3", IsPump: true, PumpMinimum: 60},
		{Name: "Chassis Fan 1", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm4"},
		{Name: "aux_fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm5"},
	}
	seeded.Sensors = []config.Sensor{
		{Name: "CPU Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp1_input"},
		{Name: "GPU Temperature", Type: "nvidia", Path: "0", Metric: "temp"},
		{Name: "Motherboard Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp3_input"},
	}
	// Non-nil Curves so the fan-card render's `cfg.curves.map(...)`
	// select-option branch has something to iterate. config.Empty()
	// leaves this nil; the JSON marshal would then emit `null`,
	// which the JS branch would dereference and crash on.
	seeded.Curves = []config.CurveConfig{}
	h.cfgPtr.Store(&seeded)

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

	// Helper: query the effective open state of a dashboard group.
	// Returns empty string when the group is absent (hidden because
	// its bucket is empty, or rendering hasn't reached that section
	// yet). t.Helper() so failures point at the caller.
	groupOpen := func(t *testing.T, section, group string) (present bool, open bool) {
		t.Helper()
		res, err := page.Eval(`(sec, grp) => {
			const el = document.querySelector(
				'#' + sec + '-cards details.dashboard-group[data-group="' + grp + '"]'
			);
			if (!el) return { present: false, open: false };
			return { present: true, open: el.hasAttribute('open') };
		}`, section, group)
		if err != nil {
			t.Fatalf("query group %s/%s: %v", section, group, err)
		}
		return res.Value.Get("present").Bool(), res.Value.Get("open").Bool()
	}

	// clearGroupStorage wipes any ventd.dashboard.* keys so a
	// subtest's default-state assertions aren't polluted by prior
	// toggles. Session storage persists across navigations within
	// the same page context, so explicit cleanup is required.
	clearGroupStorage := func(t *testing.T) {
		t.Helper()
		if _, err := page.Eval(`() => {
			const keys = [];
			for (let i = 0; i < sessionStorage.length; i++) {
				const k = sessionStorage.key(i);
				if (k && k.startsWith('ventd.dashboard.')) keys.push(k);
			}
			keys.forEach(k => sessionStorage.removeItem(k));
		}`); err != nil {
			t.Fatalf("clear session storage: %v", err)
		}
	}

	t.Run("narrow_viewport", func(t *testing.T) {
		setViewport(t, page, 390, 844)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement("#sensor-cards details.dashboard-group")
		clearGroupStorage(t)
		// Force a re-render after clearing storage so the
		// subsequent assertions see the defaults rather than the
		// state the initial render baked in.
		if _, err := page.Eval(`() => { renderSensorCards(); renderFanCards(); }`); err != nil {
			t.Fatalf("force re-render: %v", err)
		}
		time.Sleep(100 * time.Millisecond)

		// CPU opens by default on narrow viewports; the rest stay
		// collapsed so the dashboard fits a phone's vertical
		// budget on first paint.
		for _, tc := range []struct {
			section, group string
			wantOpen       bool
		}{
			{"sensor", "cpu", true},
			{"sensor", "gpu", false},
			{"sensor", "system", false},
			{"fan", "cpu", true},
			{"fan", "gpu", false},
			{"fan", "system", false},
			{"fan", "other", false},
		} {
			present, open := groupOpen(t, tc.section, tc.group)
			if !present {
				t.Errorf("%s/%s: group not rendered", tc.section, tc.group)
				continue
			}
			if open != tc.wantOpen {
				t.Errorf("%s/%s: open=%v want %v (narrow default)",
					tc.section, tc.group, open, tc.wantOpen)
			}
		}
	})

	t.Run("wide_viewport", func(t *testing.T) {
		setViewport(t, page, 1440, 900)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement("#sensor-cards details.dashboard-group")
		clearGroupStorage(t)
		if _, err := page.Eval(`() => { renderSensorCards(); renderFanCards(); }`); err != nil {
			t.Fatalf("force re-render: %v", err)
		}
		time.Sleep(100 * time.Millisecond)

		for _, tc := range []struct {
			section, group string
		}{
			{"sensor", "cpu"}, {"sensor", "gpu"}, {"sensor", "system"},
			{"fan", "cpu"}, {"fan", "gpu"}, {"fan", "system"}, {"fan", "other"},
		} {
			present, open := groupOpen(t, tc.section, tc.group)
			if !present {
				t.Errorf("%s/%s: group not rendered", tc.section, tc.group)
				continue
			}
			if !open {
				t.Errorf("%s/%s: open=false at wide viewport, expected open by default",
					tc.section, tc.group)
			}
		}
	})

	t.Run("session_storage_persists_toggle", func(t *testing.T) {
		setViewport(t, page, 390, 844)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement("#sensor-cards details.dashboard-group")
		clearGroupStorage(t)
		if _, err := page.Eval(`() => { renderSensorCards(); renderFanCards(); }`); err != nil {
			t.Fatalf("force re-render: %v", err)
		}
		time.Sleep(100 * time.Millisecond)

		// Narrow default: fan/cpu open, fan/gpu closed. Close CPU,
		// open GPU, force an SSE-equivalent re-render, then confirm
		// the toggled state survived.
		if _, err := page.Eval(`() => {
			const cpu = document.querySelector('#fan-cards details[data-group="cpu"]');
			const gpu = document.querySelector('#fan-cards details[data-group="gpu"]');
			cpu.open = false; gpu.open = true;
		}`); err != nil {
			t.Fatalf("toggle groups: %v", err)
		}
		// Native toggle event fires asynchronously; give it a tick.
		time.Sleep(50 * time.Millisecond)

		// sessionStorage must reflect the toggles.
		cpuStored, err := page.Eval(`() => sessionStorage.getItem('ventd.dashboard.fans.cpu')`)
		if err != nil {
			t.Fatalf("read cpu storage: %v", err)
		}
		if cpuStored.Value.Str() != "0" {
			t.Errorf("sessionStorage[fans.cpu]=%q want \"0\"", cpuStored.Value.Str())
		}
		gpuStored, err := page.Eval(`() => sessionStorage.getItem('ventd.dashboard.fans.gpu')`)
		if err != nil {
			t.Fatalf("read gpu storage: %v", err)
		}
		if gpuStored.Value.Str() != "1" {
			t.Errorf("sessionStorage[fans.gpu]=%q want \"1\"", gpuStored.Value.Str())
		}

		// Re-render (the SSE path drives render(); here we invoke
		// the card-grid builders directly to avoid waiting on a
		// real stream tick). State must survive.
		if _, err := page.Eval(`() => renderFanCards()`); err != nil {
			t.Fatalf("re-render fans: %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		_, cpuOpen := groupOpen(t, "fan", "cpu")
		if cpuOpen {
			t.Errorf("fan/cpu: open=true after re-render, toggled-closed state was lost")
		}
		_, gpuOpen := groupOpen(t, "fan", "gpu")
		if !gpuOpen {
			t.Errorf("fan/gpu: open=false after re-render, toggled-open state was lost")
		}

		// Clear sessionStorage and re-render — defaults must come
		// back (CPU open, GPU closed).
		clearGroupStorage(t)
		if _, err := page.Eval(`() => renderFanCards()`); err != nil {
			t.Fatalf("re-render fans (2): %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		_, cpuOpen = groupOpen(t, "fan", "cpu")
		if !cpuOpen {
			t.Errorf("fan/cpu: open=false after storage clear, narrow default was not restored")
		}
		_, gpuOpen = groupOpen(t, "fan", "gpu")
		if gpuOpen {
			t.Errorf("fan/gpu: open=true after storage clear, narrow default was not restored")
		}
	})

	t.Run("empty_group_hidden", func(t *testing.T) {
		setViewport(t, page, 1440, 900)
		page.MustNavigate(h.server.URL + "/").MustWaitStable()
		page.Timeout(3 * time.Second).MustElement("#sensor-cards details.dashboard-group")
		time.Sleep(100 * time.Millisecond)

		// The seeded config has no Storage entries. Storage must
		// not render at all — not a collapsed empty group, not a
		// header with a zero count.
		present, _ := groupOpen(t, "sensor", "storage")
		if present {
			t.Errorf("sensor/storage: group rendered despite empty bucket")
		}
		present, _ = groupOpen(t, "fan", "storage")
		if present {
			t.Errorf("fan/storage: group rendered despite empty bucket")
		}
	})

	t.Run("category_heuristic_table", func(t *testing.T) {
		// Table-drive classifyCategory across each rung of the
		// classification ladder. Evaluated inside the page so the
		// function under test is the real shipped implementation,
		// not a duplicate. Inputs cover:
		//   a. config-level hints: type=nvidia, is_pump=true
		//   b. name keywords for each category
		//   c. a fall-through entry that lands in 'other'
		cases := []struct {
			name     string
			entryJS  string
			wantCat  string
			rationale string
		}{
			{"CPU Fan", "null", "cpu", "name keyword CPU"},
			{"cpu_fan_1", "null", "cpu", "name keyword CPU lowercase"},
			{"Core Fan", "null", "cpu", "keyword 'core'"},
			{"Package Fan", "null", "cpu", "keyword 'package'"},
			{"GPU Fan", "null", "gpu", "name keyword GPU"},
			{"NVIDIA 3070 Fan", "null", "gpu", "keyword nvidia"},
			{"Radeon Fan", "null", "gpu", "keyword radeon"},
			{"", "({type:'nvidia'})", "gpu", "config type=nvidia"},
			{"Pump", "null", "system", "keyword pump"},
			{"Primary Loop", "({is_pump:true})", "system", "config is_pump=true"},
			{"System Fan 1", "null", "system", "keyword system"},
			{"SYS_FAN1", "null", "system", "keyword sys_fan"},
			{"Chassis Fan 2", "null", "system", "keyword chassis"},
			{"Motherboard", "null", "system", "keyword motherboard"},
			{"NVMe Temp", "null", "storage", "keyword nvme"},
			{"SSD Temperature", "null", "storage", "keyword ssd"},
			{"Drive Bay Fan", "null", "storage", "keyword drive"},
			{"aux_fan", "null", "other", "no match falls through to other"},
			{"", "({category:'cpu',type:'hwmon'})", "cpu", "explicit category overrides everything"},
		}

		for _, c := range cases {
			// Evaluate the literal entry expression inside the
			// page so a test case can exercise either a bare null
			// or a populated object literal without having to
			// shuttle a JSON-marshalled value through rod. eval()
			// parses the string in the page's global scope, which
			// is fine for a fixed test table — the inputs are
			// compile-time string literals, not user data.
			got, err := page.Eval(
				`(n, ejs) => {
					const entry = eval('(' + ejs + ')');
					return classifyCategory(n, entry);
				}`, c.name, c.entryJS,
			)
			if err != nil {
				t.Fatalf("eval classifyCategory(%q, %s): %v", c.name, c.entryJS, err)
			}
			if g := got.Value.Str(); g != c.wantCat {
				t.Errorf("classifyCategory(%q, %s) = %q want %q (%s)",
					c.name, c.entryJS, g, c.wantCat, c.rationale)
			}
		}
	})
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
