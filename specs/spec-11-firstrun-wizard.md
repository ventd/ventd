# spec-11 — First-run web UI wizard

**Status:** draft, target v0.5.0.
**Predecessor:** spec-10 (doctor — wizard's first step), spec-06 (web.listen 127.0.0.1 default), spec-03 PR 2b (calibration probe).
**Memory anchor:** install-experience polish for v0.5.0 enthusiast-hardware first-light. Phoenix dual-boot install on 13900K/RTX 4090/Phanteks 14-fan/Arctic LFII is target user-zero.

---

## Why this ships v0.5.0

ventd's web UI today is steady-state. It assumes the daemon is already calibrated and running curves. There is no path for the user who just ran `apt install ventd` and `systemctl start ventd` and now has 14 fans spinning at 100% because no calibration exists yet.

Concrete first-run failure modes the wizard closes:

1. **No calibration exists → fans run at default (likely 100% or BIOS curve).** User has to know to run `ventd calibrate` from CLI. That command does not exist as a primary discovery surface; it is buried in docs.
2. **Calibration failed mid-probe → no useful feedback in the web UI.** User sees "0 calibrated channels" and does not know whether to retry, stop fancontrol, or disable Q-Fan.
3. **doctor says blockers → user never runs doctor → starts ventd anyway → silent degradation.** The wizard makes doctor non-skippable on first run.
4. **User has no idea what fans got matched to what curves.** Default curve assignments are reasonable but invisible. Wizard previews what ventd will do before the user clicks "Apply".

The wizard is opt-in for power users (`--skip-wizard` config flag, `?wizard=off` URL param) but ON BY DEFAULT for the homelab/NAS audience who installed ventd because they want it to Just Work.

This belongs in v0.5.0 alongside spec-10 because: (a) the wizard's first step is `ventd doctor --json`, no point shipping the wizard before doctor exists; (b) v0.5.0 is the install-experience release across the board (catalog seeding + doctor + wizard); (c) shipping wizard in a later release means v0.5.0 users get a rough edge that the next release fixes — bad first impression.

---

## Scope — what this session produces

Two PRs. PR 1 is the wizard infrastructure + doctor integration. PR 2 is the calibration step + curve preview. Cost target Sonnet $40-60 total.

### PR 1 — Wizard frame, doctor integration, hardware step

**Files (new):**
- `web/wizard/` — frontend assets directory (HTML, CSS, JS — match existing `web/` style and toolchain).
- `web/wizard/index.html` — wizard shell with step navigation.
- `web/wizard/step1_doctor.html` — embeds `ventd doctor --json` output, renders severity badges, blocker-gate progression.
- `web/wizard/step2_hardware.html` — shows resolved EffectiveControllerProfile, match tier, channel inventory.
- `web/wizard/wizard.css` — minimal styling, follow existing web UI palette.
- `web/wizard/wizard.js` — step navigation, fetch calls to `/api/wizard/*` endpoints, no framework.
- `internal/web/wizard.go` — HTTP handlers under `/wizard/*` and `/api/wizard/*`.
- `internal/web/wizard_state.go` — wizard state tracker (`needed`, `in_progress`, `complete`) persisted to `/var/lib/ventd/wizard-state.json`.
- `internal/web/wizard_test.go` — handler tests, state-transition tests.
- `.claude/rules/wizard.md` — RULE-WIZARD-01..06 invariants for PR 1.
- `docs/wizard.md` — user-facing wizard reference.

**Files (modified):**
- `internal/web/router.go` — register wizard routes; add wizard-needed redirect middleware (see below).
- `internal/web/server.go` — read `wizard.enabled` config option (default `true`).
- `internal/config/defaults.go` — add `wizard.enabled: true` and `wizard.force_complete: false` config keys.
- `cmd/ventd/main.go` — add `--skip-wizard` runtime flag for bare-metal debugging.
- `CHANGELOG.md` — v0.5.0 entry.

**Wizard-needed redirect logic:**

When wizard is enabled AND wizard-state is not `complete`, all GET requests to non-wizard, non-API paths redirect to `/wizard/`. Exceptions:
- `/api/*` — programmatic clients always get the real API.
- `/healthz` — load-balancer health checks unaffected.
- `/wizard/*` — wizard itself.
- Static assets needed by the wizard (`/static/*`).

Once wizard-state flips to `complete`, redirect middleware deactivates and the redirect path returns to normal.

### PR 1 invariant bindings (`.claude/rules/wizard.md`)

1. `RULE-WIZARD-01` — Wizard state is forward-only. Once `complete`, it does not revert to `in_progress` or `needed` automatically. Recovery from `complete` to `needed` requires explicit `ventd wizard reset` CLI command (out of scope — file as v0.5.x follow-up). **Binds to:** `TestWizardState_ForwardOnly`.

2. `RULE-WIZARD-02` — Doctor blocker (severity `blocker` from spec-10) prevents wizard progression. The "Continue" button is disabled until either the blocker is resolved (re-run doctor returns no blockers) or the user explicitly overrides via `--i-know-what-im-doing` checkbox + typed confirmation. **Binds to:** `TestWizard_BlockerGate`.

3. `RULE-WIZARD-03` — Wizard never starts the daemon's control loop. Calibration step (PR 2) is the only path that issues writes; until that step runs, ventd is in idle mode. **Binds to:** `TestWizard_NoCurveWritesPrePR2`.

4. `RULE-WIZARD-04` — `wizard-state.json` mode 0600, owner ventd:ventd. Verified post-write per RULE-DIAG-PR2C-10 pattern. **Binds to:** `TestWizard_StateFileMode`.

5. `RULE-WIZARD-05` — Wizard pins doctor JSON `schema_version: "1"` (per spec-10 RULE-DOCTOR-08). On schema mismatch, wizard refuses to start with a clear error directing user to update either ventd or run `ventd doctor --json` to verify schema. **Binds to:** `TestWizard_DoctorSchemaPin`.

6. `RULE-WIZARD-06` — Wizard frontend has no external CDN dependencies. All CSS, JS, fonts ship in `web/wizard/` and are served from ventd. ventd does not phone home. **Binds to:** `TestWizard_NoCDN` (static analysis grepping for `https://` in wizard assets).

### PR 2 — Calibration step + curve preview + finalisation

**Files (new):**
- `web/wizard/step3_calibrate.html` — calibration trigger, live progress (per-channel probe results streamed via SSE or polling).
- `web/wizard/step4_preview.html` — preview default curve assignments per fan, allow user override before commit.
- `web/wizard/step5_complete.html` — summary, "Apply and start control loop" button.
- `internal/web/wizard_calibrate.go` — handler that triggers calibration via existing `internal/calibration` package; streams progress.
- `internal/web/wizard_preview.go` — handler that renders default curve plan as JSON for the preview UI.
- `internal/web/wizard_complete.go` — handler that flips wizard-state to `complete`, signals daemon to begin control loop.

**Files (modified):**
- `internal/calibration/probe.go` — add an optional progress callback channel (callers can subscribe to per-channel results as they complete). Backward-compatible: existing CLI invocation still works without callback.
- `internal/web/wizard.go` (from PR 1) — register PR 2 routes.
- `.claude/rules/wizard.md` (from PR 1) — extend with RULE-WIZARD-07..10.

### PR 2 invariant bindings (extension of `.claude/rules/wizard.md`)

7. `RULE-WIZARD-07` — Calibration step uses `internal/calibration` directly. No reimplementation. Progress streaming is a new channel, not a new probe path. **Binds to:** `TestWizard_CalibrationShared`.

8. `RULE-WIZARD-08` — Curve-preview step generates assignments from existing curve-default logic in `internal/control`. No parallel default-picker. **Binds to:** `TestWizard_CurveDefaultsShared`.

9. `RULE-WIZARD-09` — User overrides in step 4 are validated against same constraints as the rest of the config (no curve below stall_pwm + safety margin, no curve above pwm_unit_max, etc.). Validation reuses `internal/config/validate.go`. **Binds to:** `TestWizard_OverrideValidationShared`.

10. `RULE-WIZARD-10` — Wizard finalisation uses **sequenced single-file atomicity with `wizard-state.json` as the commit pivot**. Writes execute in strict order using `natefinch/atomic` per file: (1) calibration JSON to `/var/lib/ventd/calibration/<fp>-<bios>.json`, (2) curve assignments to `/etc/ventd/config.yaml.d/wizard-output.yaml`, (3) `wizard-state.json` flipped to `complete`. The state file is the only durable "wizard done" signal. If interrupted before write 3, the wizard is re-runnable on next start; prior calibration JSON is reused (step 3 detects existing file, offers "use or recalibrate"), prior wizard-output.yaml is overwritten if the user changes assignments. The system never enters an inconsistent state because no consumer reads wizard outputs until `wizard-state.json == complete`. **Binds to:** `TestWizard_PivotedCommit` — subtest force-kills the process between writes 1↔2 and 2↔3, restarts wizard, verifies (a) wizard re-enters at the appropriate step, (b) prior artifacts are detected and offered for reuse, (c) final state after successful re-run is identical to uninterrupted run.

   **Why pivoted instead of true multi-file atomicity:** Go has no native cross-file transaction primitive. `natefinch/atomic` provides single-file rename-into-place. A true 3-file transaction would require either (a) a write-ahead log + recovery on startup, or (b) a single archive file containing all three artifacts. Both add complexity disproportionate to the failure mode being protected against. The pivot pattern relies on consumers (control loop, web UI) reading `wizard-state.json` before reading any other wizard artifact — this contract is enforced by the redirect middleware (nothing reads `wizard-output.yaml` while state ≠ complete) and by the daemon startup sequence.

### Wizard step flow

```
START
  │
  ├─ wizard-state == complete? ──> redirect to /index, normal UI
  │
  ▼
Step 1 — Doctor preflight
  Run `ventd doctor --json`. Render severity table.
  ├─ blockers? ──> "Resolve and retry" (doctor re-run on click)
  │                "Override (advanced)" hidden behind expander
  └─ no blockers ──> "Continue"
  │
  ▼
Step 2 — Hardware confirmation
  Show: matched board, match tier (1/2/3), channel inventory,
        BIOS warnings from doctor.
  ├─ tier-3 fallback ──> show contribute-a-board-profile link
  └─ "Continue" or "Back to step 1"
  │
  ▼
Step 3 — Calibration
  Trigger probe via /api/wizard/calibrate.
  Stream progress: per-channel probe → result.
  ├─ all channels phantom or bios_overridden ──> "Calibration unproductive — review BIOS settings"
  ├─ partial calibration ──> "Continue with N controllable channels"
  └─ full success ──> "Continue"
  │
  ▼
Step 4 — Curve preview
  Render proposed default curve per controllable channel.
  Allow override: per-channel curve picker (existing curve presets list).
  ├─ user customises ──> validate per RULE-WIZARD-09
  └─ "Apply" or "Back to step 3"
  │
  ▼
Step 5 — Complete
  Atomic commit per RULE-WIZARD-10.
  Show: summary of what was committed, link to main UI.
  Wizard-state → complete.
END
```

### Out-of-scope (explicit)

- **No reset path in v0.5.0.** Once wizard is `complete`, it stays complete. CLI `ventd wizard reset` is filed as v0.5.x follow-up issue.
- **No multi-user wizard.** Wizard assumes one user driving the install. Multiple browsers hitting the wizard simultaneously is not handled gracefully (last-write-wins, no row locking).
- **No translation/i18n.** English only in v0.5.0.
- **No accessibility audit beyond keyboard navigation + semantic HTML.** Screen-reader compatibility is best-effort, not certified.
- **No hardware add/remove during wizard.** If the user hot-plugs a USB AIO mid-wizard, behaviour is undefined.
- **No skip-and-resume.** User cannot abandon wizard at step 3 and pick up at step 3 later. They restart from step 1.
- **No telemetry.** Wizard does not record which steps users got stuck on. Future spec.
- **No mobile-first design.** Desktop browser is the primary target. Mobile is best-effort.

---

## Definition of done

PR 1:
- [ ] `web/wizard/` assets render correctly in Chromium and Firefox without console errors.
- [ ] All 6 RULE-WIZARD-01..06 rules bound to subtests. `tools/rulelint` returns 0 for PR 1 scope.
- [ ] Wizard-needed redirect middleware activates correctly when state ≠ complete and deactivates when state = complete.
- [ ] `/api/wizard/doctor` calls `ventd doctor --json` (not a parallel implementation), parses output, returns it to the frontend.
- [ ] Schema-version pin enforced: simulate doctor returning `schema_version: "2"` in a fixture, wizard refuses with clear error.
- [ ] Static analysis subtest verifies no external CDN URLs in wizard frontend.
- [ ] `wizard-state.json` file mode 0600, owner ventd:ventd, verified post-write.
- [ ] Wizard accessible at `/wizard/` regardless of redirect state (so users can re-enter manually if confused).
- [ ] PR 1 description notes: "PR 2 ships calibration + preview + finalisation; PR 1 alone is preview-only and does not commit anything."

PR 2:
- [ ] Calibration step streams per-channel progress to the frontend.
- [ ] Curve preview renders matching the visual style of the main web UI's curve editor.
- [ ] Override validation rejects invalid curves with clear error messages.
- [ ] Atomic commit: kill ventd mid-commit (force-kill), restart, verify either all-applied or all-not-applied. Subtest with synthetic interrupt.
- [ ] All 4 PR 2 rules (RULE-WIZARD-07..10) bound to subtests. `tools/rulelint` returns 0 for full v0.5.0 scope.
- [ ] End-to-end test: fresh install on dev container, wizard runs all 5 steps, control loop starts, basic curve write succeeds.
- [ ] CHANGELOG entry: `feat(wizard): first-run web UI wizard with doctor preflight, calibration, and curve preview`.
- [ ] Conventional commits at PR boundaries.

---

## Explicit non-goals

- No "advanced setup" mode. Wizard is opinionated; advanced users skip via `--skip-wizard` and use CLI directly.
- No drag-and-drop curve editing in the preview. Existing curve preset list is the override surface.
- No real-time fan RPM display in the wizard. That's the main UI's job.
- No notification system. Wizard does not email/push when complete.
- No undo. Once "Apply" is clicked at step 5, the user can change config via the main UI like normal — there is no wizard-specific revert.
- No reusing wizard for major upgrades. v0.6.0 schema migration is its own UX problem; wizard is install-only.

---

## Red flags — stop and page Phoenix

- Wizard frontend needs a JS framework (React, Vue, Alpine, htmx) — surface, the existing web UI is vanilla JS and we don't want to introduce a framework just for the wizard. If something is genuinely impossible without a framework, redesign the step.
- Calibration progress streaming requires WebSockets — surface, SSE or polling is preferred. WS adds infrastructure.
- Pivot pattern (RULE-WIZARD-10) cannot be made the single durable commit signal — surface, design needs rethink. Specifically: if any consumer of wizard outputs (control loop, main web UI, doctor's calibration check) reads `wizard-output.yaml` or the calibration JSON without first checking `wizard-state.json == complete`, the pivot is broken. Audit consumers in PR 2 before declaring done.
- Wizard-state file conflicts with concurrent ventd processes (e.g. user runs `ventd calibrate` CLI mid-wizard) — surface, lock or refuse.
- Total CC spend across PR 1 + PR 2 crosses $50 — surface progress, request continuation.
- BIOS known-bad data flows from doctor through wizard correctly but the rendering is confusing in the UI — surface UX issue, this is design not code.

---

## CC session prompts — copy/paste these

### PR 1 prompt

```
spec-11 PR 1 implementation. Read /mnt/project/spec-11-firstrun-wizard.md (full spec) and /mnt/project/spec-10-doctor.md (predecessor — doctor JSON schema is wizard's contract).

Sonnet only. No subagents. Conventional commits.

Branch: spec-11/pr-1-wizard-frame

Files per spec PR 1 list. No PR 2 work in this PR.

Key invariants:
- RULE-WIZARD-02 blocker gate (default-deny progression)
- RULE-WIZARD-04 wizard-state.json 0600
- RULE-WIZARD-05 doctor schema_version "1" pinned
- RULE-WIZARD-06 no external CDN

Stop and surface to Phoenix if:
- Frontend needs a JS framework (we're vanilla JS only)
- Wizard-needed redirect breaks /api/* or /healthz
- Total spend crosses $25

Verification:
1. go test ./internal/web/... -v -count=1
2. go test ./... -count=1
3. golangci-lint run ./...
4. tools/rulelint
5. curl http://127.0.0.1:9999/ → redirect to /wizard/ when state != complete
6. curl http://127.0.0.1:9999/api/healthz → 200 OK regardless of wizard state
7. Manual test in Chromium + Firefox

PR body must call out: "PR 2 ships calibration + preview + commit; PR 1 alone shows steps 1-2 only."

Estimate: $20-30, 30-45 minutes.
```

### PR 2 prompt

```
spec-11 PR 2 implementation. PR 1 merged. Read /mnt/project/spec-11-firstrun-wizard.md.

Sonnet only. No subagents. Conventional commits.

Branch: spec-11/pr-2-calibration-preview-commit

Files per spec PR 2 list. RULE-WIZARD-07..10 binding.

Key invariants:
- RULE-WIZARD-07 calibration shared with internal/calibration (no fork)
- RULE-WIZARD-09 override validation shared with internal/config/validate.go
- RULE-WIZARD-10 pivoted commit: writes 1→2→3 strict order, state.json is pivot. Audit all consumers of wizard outputs to confirm they check state.json first.

Stop and surface to Phoenix if:
- Calibration progress needs WebSockets (use SSE or polling)
- Pivot consumer-audit reveals a reader that bypasses state.json check (control loop, main UI, doctor) — surface, fix consumer first
- Total spend crosses $30 (this PR alone)

Verification:
1. go test ./internal/web/... ./internal/calibration/... -v -count=1
2. End-to-end: dev container, wizard runs 1-5, control loop starts, fan write works
3. Force-kill between writes 1↔2 and 2↔3 in commit phase, restart wizard, verify re-entry at correct step + prior-artifact reuse offered
3a. Confirm consumer-audit: grep for readers of wizard-output.yaml and calibration JSON — every reader must gate on wizard-state.json == complete
4. golangci-lint, tools/rulelint
5. Manual test in Chromium + Firefox

PR body: "Wizard now ships end-to-end. spec-11 v0.5.0 work complete."

Estimate: $20-30, 30-45 minutes.
```

---

## Why this is cheap

Wizard is bounded because:
- Frontend is vanilla HTML/CSS/JS, no framework, no build step. ventd already serves static assets.
- All backend logic reuses existing packages — `internal/doctor` (PR 1), `internal/calibration` (PR 2), `internal/config/validate` (PR 2), `internal/control` curve defaults (PR 2). Zero new core logic.
- 10 invariants split across two PRs is moderate.
- No translation, no a11y certification, no mobile-first — those are v1.x or later if at all.
- Synthetic-fixture testing for everything except final dev-container e2e in PR 2 DoD.

Risks that could inflate cost:
- Wizard UX iteration. Mitigation: single design pass in this spec, no UX-back-and-forth in CC.
- Calibration progress streaming complexity. Mitigation: polling is acceptable, SSE is the upgrade if polling proves choppy.
- "But what about edge case X" creep — wizards attract scope creep. Mitigation: aggressive non-goals list, defer to v0.5.x patches.

Total target: $40-60 across both PRs. Combined with spec-10 doctor ($25-40), v0.5.0 install-experience polish costs $65-100. spec-03 PR 3 catalog seeding sits separately.

---

**End of spec-11-firstrun-wizard.md.**
