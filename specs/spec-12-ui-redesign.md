# spec-12 — Web UI redesign + first-run flow

**Status:** draft, target v0.5.0 (PRs 1-4) and v0.5.x patches (PRs 5-8).
**Supersedes:** spec-11 (first-run wizard) — wizard rationale folded into §6 setup flow.
**Predecessor:** spec-10 (doctor JSON consumed by setup flow), spec-06 (web.listen 127.0.0.1 default).
**Memory anchor:** Phoenix received finished mockup bundle from Claude design 2026-04-26 — 11 pages, shared/ token system, canon.md reconciled. This spec converts the static mockup bundle into shipped web UI across three patch releases.

---

## 1 · Why this supersedes spec-11

spec-11 designed a 5-step wizard (`doctor → hardware → calibrate → preview → complete`) at `/wizard/` as a separate frontend bundle from the steady-state UI. The mockup bundle Phoenix received instead delivers:

- A unified design system (`shared/tokens.css`, `shared/shell.css`, `shared/brand.css`, `shared/brand.js`, `shared/sidebar.html`) used by every page including setup.
- `setup.html` as a single token-paste auth screen, not a 5-step wizard. The "doctor + calibrate + preview" flow needs a new home.
- A finished aesthetic — propeller logo, dark/light token system, sticky sidebar with version+uptime footer, status pills, sparklines — that the spec-11 wizard CSS (`web/wizard/wizard.css`, "minimal styling, follow existing web UI palette") was going to under-deliver.

Continuing spec-11 in parallel would mean two design systems, two CSS bundles, two sidebar implementations. spec-12 absorbs spec-11's invariants (RULE-WIZARD-01..10) into UI-namespaced equivalents and routes them through the new design system.

**Wizard rationale that survives spec-12:**
- `RULE-WIZARD-02` blocker gate → `RULE-UI-SETUP-02` doctor blocker prevents progression past doctor step.
- `RULE-WIZARD-04` state file 0600 → `RULE-UI-SETUP-04` setup-state.json 0600.
- `RULE-WIZARD-05` doctor schema_version pin → `RULE-UI-SETUP-05` schema_version "1" pinned.
- `RULE-WIZARD-06` no external CDN → `RULE-UI-01` (applies globally, not setup-specific).
- `RULE-WIZARD-07` calibration shared → `RULE-UI-SETUP-07`.
- `RULE-WIZARD-09` override validation shared → `RULE-UI-SETUP-09`.
- `RULE-WIZARD-10` pivoted commit → `RULE-UI-SETUP-10`.

**Wizard rationale that gets retired:**
- `RULE-WIZARD-01` forward-only state — replaced by setup-flow design (single-page with internal step state, not a separate URL space).
- `RULE-WIZARD-03` no control-loop writes pre-PR2 — moved to setup-flow's calibration substep, same effect.
- `RULE-WIZARD-08` curve defaults shared — moved to RULE-UI-SETUP-08.

**Decision recorded:** the 5-step doctor/calibrate/preview/apply flow lives inside the setup page, not at a separate URL. Setup expands to a multi-step modal/accordion within the existing `setup.html` shell. PR 4 (setup flow) is where this is built; PR 4 working notes explore modal-vs-accordion-vs-route tradeoffs.

---

## 2 · Design system foundation

The mockup bundle ships a complete token system. Treat it as immutable input — PR 1 installs it, no edits.

**Files (verbatim from mockup bundle):**
- `shared/tokens.css` — color tokens (dark default + light variant), font stacks, no semantic styles.
- `shared/shell.css` — sidebar, topbar, buttons, cards, sparklines, status pills, page primitives.
- `shared/brand.css` — propeller mark + brand text styles.
- `shared/brand.js` — DOM-injects propeller SVG into every `.brand-mark` element. Pure DOM, CSP-safe, no inline event handlers.
- `shared/sidebar.html` — canonical sidebar markup. Each page embeds a copy; no SSI.
- `shared/canon.md` — canonical data values reconciled across all 11 mockups.

**Where they live in the repo:** `web/shared/` (parallel to existing `web/` static assets).

**What "no edits" means:** PR 1 copies these files byte-for-byte. If a token is wrong (wrong shade, missing variant), the fix is a follow-up PR that updates the mockup bundle first, then re-syncs. Keeps the design system single-source.

**What's NOT in the bundle and needs to be authored:**
- Per-page JS that hits the real API (mockups have stub `dashboard.js`, `devices.js`, etc. with hard-coded data).
- Server-side handlers for any new endpoints (most page data needs API surface).
- Tests.

---

## 3 · PR map

Eight PRs total across three releases. Each PR is independently shippable — if a release tag has to cut early, the trailing PRs slip to the next patch.

### v0.5.0 — install-experience headline (4 PRs)

| PR | Title | Pages | Cost target | Subagent |
|----|-------|-------|------|------|
| PR 1 | Token system + sidebar + index landing | `index.html` + `shared/*` | $5-8 | Sonnet |
| PR 2 | Devices page | `devices.html` | $8-12 | Sonnet |
| PR 3 | Dashboard | `dashboard.html` + stale-state | $12-18 | Sonnet |
| PR 4 | Setup flow (folds spec-11) | `setup.html` + multi-step | $15-25 | Sonnet |

v0.5.0 total target: **$40-63**. Pads to $60-95 for surprises.

PR 1 is the foundation everyone else depends on. PR 2 is the simplest data page (read-only chip+fan list, exercises tokens end-to-end without complex API work). PR 3 is the highest-visibility page (what users see most). PR 4 is the install-experience climax — folds wizard into the new design.

### v0.5.1 — control surfaces (3 PRs)

| PR | Title | Pages | Cost target | Subagent |
|----|-------|-------|------|------|
| PR 5 | Curve editor | `curve-editor.html` | $12-18 | Sonnet |
| PR 6 | Sensors | `sensors.html` | $8-12 | Sonnet |
| PR 7 | Schedule | `schedule.html` | $10-15 | Sonnet |

v0.5.1 total target: **$30-45**.

### v0.5.2 — operations surfaces (1 PR + 1 follow-up)

| PR | Title | Pages | Cost target | Subagent |
|----|-------|-------|------|------|
| PR 8 | Calibration + Logs + Settings | `calibration.html`, `logs.html`, `settings.html` | $15-25 | Sonnet |

PR 8 batches three pages because they share patterns (read-mostly admin surfaces, similar API shape). v0.5.2 total target: **$15-25**.

**Total spec-12 cost: $85-133.** Spread across 4-6 weeks of release cadence.

---

## 4 · Per-PR file lists

### PR 1 — Token system + sidebar + index

**Files (new):**
- `web/shared/tokens.css` — copy from mockup bundle.
- `web/shared/shell.css` — copy from mockup bundle.
- `web/shared/brand.css` — copy from mockup bundle.
- `web/shared/brand.js` — copy from mockup bundle.
- `web/shared/sidebar.html` — reference markup (not served, fixture for tests).
- `web/index.html` — copy from mockup bundle, wire to existing API for the version+uptime footer.
- `web/index.css` — copy from mockup bundle.
- `internal/web/ui_tokens_test.go` — RULE-UI-01 no-CDN static analysis.
- `.claude/rules/ui.md` — RULE-UI-01..04 invariants for PR 1 scope.

**Files (modified):**
- `internal/web/router.go` — serve `/shared/*` static asset path.
- `internal/web/server.go` — add `Cache-Control: max-age=3600, public` for `/shared/*` assets (long-cacheable design system) and `Cache-Control: no-cache` for HTML pages.
- `CHANGELOG.md` — v0.5.0 entry: `feat(web): new design system and landing page`.

### PR 2 — Devices

**Files (new):**
- `web/devices.html` — copy from mockup, replace stub data with template variables or fetch calls.
- `web/devices.css` — copy from mockup bundle.
- `web/devices.js` — replace mockup stub with real API calls to `/api/v1/chips`, `/api/v1/fans`.
- `internal/web/devices.go` — handler that reads from existing HAL backend registry, returns JSON.
- `internal/web/devices_test.go` — handler tests, RULE-UI-DEVICES-01..03 bindings.

**Files (modified):**
- `internal/web/router.go` — register `/devices`, `/api/v1/chips`, `/api/v1/fans`.
- `.claude/rules/ui.md` — extend with RULE-UI-DEVICES-01..03.

### PR 3 — Dashboard

**Files (new):**
- `web/dashboard.html` — copy from mockup.
- `web/dashboard-stale.html` — copy from mockup (offline-state UI).
- `web/dashboard.css` — copy.
- `web/dashboard-stale.css` — copy.
- `web/dashboard.js` — real API + sparkline rendering + 1Hz polling.
- `internal/web/dashboard.go` — handler aggregating sensor + fan + control-loop state.
- `internal/web/dashboard_test.go` — handler tests + RULE-UI-DASHBOARD-01..04 bindings.

**Files (modified):**
- `internal/web/router.go` — register `/dashboard`, `/api/v1/dashboard`.
- `.claude/rules/ui.md` — extend with RULE-UI-DASHBOARD-01..04.
- `CHANGELOG.md` — v0.5.0 entry update.

### PR 4 — Setup flow (folds spec-11)

**Files (new):**
- `web/setup.html` — base from mockup, expanded to a 5-step internal flow:
  - Step 1: Token authentication (mockup as-is).
  - Step 2: Doctor preflight (consumes spec-10 `ventd doctor --json`).
  - Step 3: Hardware confirmation (resolved EffectiveControllerProfile).
  - Step 4: Calibration with progress streaming.
  - Step 5: Curve preview + apply.
- `web/setup.css` — copy from mockup, extend for steps 2-5.
- `web/setup.js` — token validation, step state machine, doctor/calibrate/preview API calls.
- `internal/web/setup.go` — handlers for setup state transitions + API wiring to doctor/calibration/control packages.
- `internal/web/setup_state.go` — setup-state.json tracker (`needed`/`in_progress`/`complete`).
- `internal/web/setup_calibrate.go` — calibration trigger + progress streaming (SSE).
- `internal/web/setup_preview.go` — curve plan generator (reuses internal/control defaults).
- `internal/web/setup_complete.go` — pivoted commit handler (reuses natefinch/atomic pattern from spec-11 RULE-WIZARD-10).
- `internal/web/setup_test.go` — full handler suite.
- `cmd/ventd/main.go` — `--skip-setup` flag for bare-metal debugging.

**Files (modified):**
- `internal/web/router.go` — register `/setup/*`, `/api/v1/setup/*`, plus setup-needed redirect middleware (`/` → `/setup/` when state ≠ complete; exempts `/api/*`, `/healthz`, `/setup/*`, `/shared/*`).
- `internal/calibration/probe.go` — add optional progress callback channel (backward-compatible).
- `internal/config/defaults.go` — `setup.enabled: true`, `setup.force_complete: false`.
- `.claude/rules/ui.md` — extend with RULE-UI-SETUP-01..10.
- `docs/setup.md` — user-facing setup reference.
- `CHANGELOG.md` — v0.5.0 entry update.

### PRs 5-8 — file lists deferred to per-release planning

For PRs 5-8 the file pattern is the same: copy mockup HTML/CSS, author new JS for real API, add backend handler + test, register route, extend `.claude/rules/ui.md`. Detailed file lists drafted at the time of each release to avoid drift.

---

## 5 · Invariant bindings

All bindings live in `.claude/rules/ui.md` (single file, sectioned by PR). Each rule binds 1:1 to a subtest per `tools/rulelint`.

### Global UI rules (PR 1)

1. `RULE-UI-01` — No external CDN dependencies. All CSS, JS, fonts, images served from ventd's filesystem. Static analysis greps for `https://` and `http://` in `web/**/*.{html,css,js}`. Allowlist: comments, doctype declarations.
   **Binds to:** `TestUI_NoExternalCDN`.

2. `RULE-UI-02` — Token-only color references. Page-specific CSS (e.g. `dashboard.css`) MUST NOT contain literal hex colors, rgb(), or hsl() — every color comes from a `var(--*)` reference defined in `tokens.css`. Static analysis greps for color literals outside `web/shared/tokens.css`.
   **Binds to:** `TestUI_TokenOnlyColors`.

3. `RULE-UI-03` — Sidebar markup matches `web/shared/sidebar.html` byte-for-byte across all pages with a sidebar. Test reads `shared/sidebar.html`, then for each page checks that the substring between `<aside class="sidebar">` and `</aside>` matches. Whitespace-tolerant via normalisation.
   **Binds to:** `TestUI_SidebarConsistency`.

4. `RULE-UI-04` — Canonical data values from `shared/canon.md` are the source of truth for fixture data. Tests parse `canon.md` and assert mockup-stub values in any test fixture match. (Real runtime data is unconstrained — the rule is about test fixtures matching the design canon to avoid drift.)
   **Binds to:** `TestUI_CanonFixtureSync`.

### Devices page rules (PR 2)

5. `RULE-UI-DEVICES-01` — Devices page consumes `/api/v1/chips` and `/api/v1/fans` only. No direct HAL imports in the frontend, no parallel device-listing endpoints.
   **Binds to:** `TestUIDevices_APIBoundary`.

6. `RULE-UI-DEVICES-02` — Chips and fans count must match HAL backend registry. The handler MUST NOT cache stale results — every `/api/v1/chips` call hits the registry directly. (Caching is a future optimisation behind RULE-UI-DEVICES-04 not yet defined.)
   **Binds to:** `TestUIDevices_NoStaleCache`.

7. `RULE-UI-DEVICES-03` — Devices page renders without JS execution (progressive enhancement). HTML contains a `<noscript>` warning + server-rendered table. JS upgrades to live updates.
   **Binds to:** `TestUIDevices_NoScriptFallback`.

### Dashboard rules (PR 3)

8. `RULE-UI-DASHBOARD-01` — Dashboard polling interval ≥ 1000ms. No sub-second polling. WebSockets/SSE deferred to v0.6.0.
   **Binds to:** `TestUIDashboard_PollInterval`.

9. `RULE-UI-DASHBOARD-02` — Stale-state UI activates when `/api/v1/dashboard` returns 5xx OR last successful response > 30s ago. Stale state shows last-known values dimmed + warning banner.
   **Binds to:** `TestUIDashboard_StaleActivation`.

10. `RULE-UI-DASHBOARD-03` — Sparkline data ≤ 60 points. Bounded ring buffer client-side. No memory growth on long-running tabs.
    **Binds to:** `TestUIDashboard_SparklineBounded`.

11. `RULE-UI-DASHBOARD-04` — Dashboard never issues writes. All buttons that look like writes (e.g. "Apply curves") link to other pages, do not POST from dashboard.
    **Binds to:** `TestUIDashboard_NoWrites`.

### Setup flow rules (PR 4) — absorbs spec-11 wizard rules

12. `RULE-UI-SETUP-01` — Setup state forward-only (was RULE-WIZARD-01). Once `complete`, requires explicit `ventd setup reset` CLI to revert.
    **Binds to:** `TestUISetup_ForwardOnly`.

13. `RULE-UI-SETUP-02` — Doctor blocker (severity from spec-10) prevents progression past step 2 (was RULE-WIZARD-02). Override via `--i-know-what-im-doing` checkbox + typed confirmation.
    **Binds to:** `TestUISetup_BlockerGate`.

14. `RULE-UI-SETUP-03` — Setup never starts control loop until step 5 commit (was RULE-WIZARD-03).
    **Binds to:** `TestUISetup_NoCurveWritesPreCommit`.

15. `RULE-UI-SETUP-04` — `setup-state.json` mode 0600, owner ventd:ventd, post-write stat verification (was RULE-WIZARD-04).
    **Binds to:** `TestUISetup_StateFileMode`.

16. `RULE-UI-SETUP-05` — Doctor schema_version "1" pinned (was RULE-WIZARD-05).
    **Binds to:** `TestUISetup_DoctorSchemaPin`.

17. `RULE-UI-SETUP-06` — Setup-needed redirect activates when state ≠ complete. Exempts `/api/*`, `/healthz`, `/setup/*`, `/shared/*`. Deactivates on state = complete without daemon restart.
    **Binds to:** `TestUISetup_RedirectMiddleware`.

18. `RULE-UI-SETUP-07` — Calibration step uses `internal/calibration` directly (was RULE-WIZARD-07). No reimplementation.
    **Binds to:** `TestUISetup_CalibrationShared`.

19. `RULE-UI-SETUP-08` — Curve preview uses `internal/control` defaults (was RULE-WIZARD-08).
    **Binds to:** `TestUISetup_CurveDefaultsShared`.

20. `RULE-UI-SETUP-09` — Override validation reuses `internal/config/validate.go` (was RULE-WIZARD-09).
    **Binds to:** `TestUISetup_OverrideValidationShared`.

21. `RULE-UI-SETUP-10` — Step 5 commit is pivoted (was RULE-WIZARD-10 post-pivot edit). Strict 1→2→3 order with `setup-state.json` as the durable signal. All consumers (control loop, main UI, doctor) gate on `setup-state.json == complete` before reading any setup-output artifact.
    **Binds to:** `TestUISetup_PivotedCommit`.

PRs 5-8 will add 3-5 rules each in the PR-12-30 range, drafted at release-planning time.

---

## 6 · Setup flow integration — design decision

The mockup bundle's `setup.html` is a single token-paste card. Spec-11's wizard was 5 steps. PR 4 must reconcile.

**Three options considered:**

**A. Modal stack inside setup.html.** Token paste reveals doctor card, doctor reveals hardware card, etc. Single URL `/setup/`, internal state machine. Pros: matches mockup aesthetic, single page-load, no route plumbing. Cons: heavy JS state machine, browser back-button broken, accessibility tricky.

**B. Multi-route `/setup/step1`, `/setup/step2`, etc.** Each step is a separate HTML page sharing the design system. Pros: server-side state survives reloads, browser navigation works, simpler JS per step. Cons: 5 HTML files instead of 1, more routes to register, more handler code.

**C. Single-page accordion.** Like A but progressive disclosure — completed steps collapse, current step is open, future steps disabled. Token paste at top, doctor below it once token validated. Pros: visible progress, single page. Cons: tall page, mobile awkward (already out-of-scope so OK), state machine still needed.

**Recommendation: Option C.** Accordion matches the mockup's "card stack" aesthetic, gives visible progress without routing complexity, keeps state-machine code bounded (5 fixed steps). Browser back-button is "back to dashboard" not "back one step" — acceptable UX.

PR 4 implementation prompt locks Option C. If CC hits friction, escalation path is to switch to Option B (multi-route) before declaring failure.

---

## 7 · API surface

Existing endpoints (already shipped in v0.4.x):
- `/healthz` — health check.
- `/api/v1/version` — version info.
- (Other endpoints — read repo before PR 1 to inventory current API surface.)

New endpoints by PR:

**PR 2 (devices):**
- `GET /api/v1/chips` — list HAL backend chips.
- `GET /api/v1/fans` — list registered fans + calibration status.

**PR 3 (dashboard):**
- `GET /api/v1/dashboard` — aggregated sensor + fan + control-loop state for the dashboard cards.

**PR 4 (setup):**
- `GET /api/v1/setup/state` — current setup state.
- `POST /api/v1/setup/token` — validate setup token.
- `POST /api/v1/setup/doctor` — trigger doctor run, return JSON.
- `POST /api/v1/setup/calibrate` — trigger calibration, returns SSE stream.
- `GET /api/v1/setup/preview` — curve plan.
- `POST /api/v1/setup/preview` — submit user overrides, returns validated plan.
- `POST /api/v1/setup/complete` — pivoted commit handler.

**PR 5 (curve editor):**
- `GET /api/v1/curves` — list curve definitions.
- `PUT /api/v1/curves/:id` — update curve.
- `GET /api/v1/curves/:id/preview` — preview curve against current sensors.

**PR 6 (sensors):**
- `GET /api/v1/sensors` — list sensors with current readings.
- `GET /api/v1/sensors/:id/history` — recent reading history (bounded).

**PR 7 (schedule):**
- `GET /api/v1/schedules` — list scheduled overrides (e.g. quiet hours).
- `PUT /api/v1/schedules` — update schedule.

**PR 8 (calibration / logs / settings):**
- `POST /api/v1/calibration/start` — kick off calibration (re-cal flow, distinct from setup).
- `GET /api/v1/calibration/status` — calibration progress.
- `GET /api/v1/logs` — paginated structured logs.
- `GET /api/v1/settings` — current config snapshot.
- `PUT /api/v1/settings` — update config (validation gated by `internal/config/validate.go`).

---

## 8 · Out-of-scope

- **Mobile-first design.** Desktop browser is the target. Sidebar collapses below 900px to a hamburger but layouts are not optimised for phones.
- **i18n.** English only through v1.0.
- **Theme switcher hookup.** The icon is in the mockups but the theme-toggle JS is deferred to v0.6.0. PR 1 ships dark default, light variant present in tokens but unused.
- **Real-time WebSockets.** All live-update paths use polling or SSE. Deferred to v0.6.0+.
- **A11y certification.** Best-effort semantic HTML + keyboard nav, not WCAG-audited.
- **Rich curve editor interactions.** PR 5 ships read + numeric edit. Drag-and-drop curve point editing deferred to v0.6.0.
- **Multi-host UI.** `host-switcher-mockup.html` is in `/mnt/project/` but isn't in the new bundle. Multi-host is post-v1.0.
- **No critique.html in shipped output.** It's an internal design-review artifact, not a user-facing page. Skip during PR 1 copy.
- **No dashboard-stale.css drift from dashboard.css.** dashboard-stale.html shares dashboard.css; dashboard-stale.css in the bundle is the override layer, copy as-is.

---

## 9 · Failure modes

**Design system drift between PRs.** PR 1 lands `tokens.css`, PR 5 wants a new color, edits `tokens.css` directly. Mitigation: RULE-UI-02 enforces token-only references; new tokens require updating the mockup bundle first, then re-syncing to repo. PR description must call out any tokens.css change.

**Sidebar drift between pages.** PR 2 copies mockup sidebar with one extra nav item, PR 3 copies a different version. Mitigation: RULE-UI-03 byte-for-byte check via subtest. CI fails if drift detected.

**Backend API surface explosion.** Each PR adds endpoints, ventd ends up with 30+ routes by v0.5.2. Mitigation: API surface listed §7 is the contract. Adding endpoints outside the list requires spec amendment. Versioned at `/api/v1/*` so v2 can break later.

**Setup flow gets stuck mid-step on real hardware.** Calibration takes 60-180s; SSE connection drops on flaky networks. Mitigation: PR 4 includes resume support — `setup-state.json` tracks substep progress, page reload picks up where left off (within current step).

**Polling overload.** 1Hz dashboard polling × N tabs = backend hammered. Mitigation: RULE-UI-DASHBOARD-01 sets ≥1000ms minimum; backend handler is read-only against in-memory state, cheap. If still problematic, v0.6.0 SSE moves us off polling.

**Mockup canon drifts from runtime data.** `canon.md` says 14 fans / 8 chips, real hardware reports 12/7. Tests must not bind runtime values to canon — RULE-UI-04 covers fixture data only. Real API responses are unconstrained.

**Critique.html copy-paste.** It's 46k lines of design-internal commentary. Easy to accidentally include. Mitigation: PR 1 prompt explicitly excludes `critique.html`.

**Theme toggle accidentally activated.** Light variant tokens present but no toggle wiring. If a user lands with `data-theme="light"` set somehow, tokens flip and rendering still works, but no UI exposes the switch. Acceptable degradation. v0.6.0 ships the toggle.

**Token system collides with existing CSS.** Current `web/` has its own styles. PR 1 must inventory existing CSS and decide: replace or namespace. Spec assumes **replace** — existing pages get migrated in PRs 2-8, no parallel design systems.

**`shared/sidebar.html` is a fixture, not served.** Easy to forget. Mitigation: PR 1 task list explicitly says "do not register a route for `/shared/sidebar.html`".

**`brand.js` runs before sidebar markup exists on page load.** brand.js painter runs on DOMContentLoaded and queries `.brand-mark` then. If a page lazy-loads sidebar via JS, painter never fires. Mitigation: every page embeds sidebar in initial HTML (not JS-injected). RULE-UI-03 already enforces this.

**setup.html state machine state lost on reload.** User refreshes mid-step, loses progress. Mitigation: server-side `setup-state.json` is source of truth; client polls `/api/v1/setup/state` on load and resumes. Documented in setup.md.

---

## 10 · CC prompts per PR

Each PR's prompt lives in its own file at the time of execution to keep them fresh. PR 1's prompt is in `cc-prompt-spec12-pr1.md` and ships now. PRs 2-4 prompts are written immediately before each PR's CC session. PRs 5-8 prompts written at v0.5.1 / v0.5.2 release-planning time.

This is intentional: CC prompts go stale fast (model behavior changes, repo state drifts, lessons accumulate). Writing all 8 prompts upfront is wasted work.

---

## 11 · Definition of done — spec-12 as a whole

- [ ] All 11 mockup pages shipped in tagged releases (v0.5.0/v0.5.1/v0.5.2).
- [ ] Token system installed once (PR 1), every page references it.
- [ ] `tools/rulelint` returns 0 across all 21+ RULE-UI-* invariants.
- [ ] Sidebar byte-for-byte consistent across pages.
- [ ] No external CDN references anywhere in `web/`.
- [ ] Setup flow runs end-to-end on dev container (token → doctor → calibrate → preview → commit → control loop alive).
- [ ] Force-kill mid-commit at step 5 verifies pivot rollback.
- [ ] Dashboard renders correctly in stale state (kill backend, reload page).
- [ ] Devices page lists actual HAL chips/fans on dev container.
- [ ] All API endpoints from §7 documented in `docs/api.md`.
- [ ] CHANGELOG entries per release.
- [ ] PR bodies link to their predecessor PRs and the parent spec.

---

## 12 · Why not ship it all in v0.5.0

You read this and might think: "I have the full mockup bundle, why not finish it all at once?"

Three reasons:

1. **Cost shape.** $85-133 across 8 PRs is fine when spread; concentrated in one release it's 30-45% of monthly budget on UI alone, leaving little room for the rest of v0.5.0 (doctor, catalog, possible bug fixes).
2. **Release cadence.** Patch releases let you ship the install-experience headline (PR 1-4) fast — homelab/NAS users see the new design within weeks, not after every page lands. Marketing momentum matters for an open-source project.
3. **Blast radius.** A bug in PR 6 sensors page shouldn't block PR 4 setup flow from shipping. Patch-release cadence localises blast radius.

If you disagree and want it all in v0.5.0, the spec mechanics are the same — only the release tags shift. PRs are independently shippable by design.

---

**End of spec-12-ui-redesign.md.**
