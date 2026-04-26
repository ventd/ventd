# spec-14b — Web UI profile submission flow

**Status**: Draft, 2026-04-26
**Targets**: v0.6.x or v0.7.x (after spec-04 PI controller, integrates with spec-12 UI)
**Pairs with**: spec-14a (upstream repo design)
**Related**: #650 (P5-PROF-03), spec-03 PR #649 (capture pipeline), spec-12 (UI redesign)

---

## Problem

The capture pipeline (PR #649) writes pending profile YAMLs after every calibration. There's no path from `profiles-pending/<fp>.yaml` to the upstream catalog. This spec defines that path.

This spec depends on **spec-14a being implemented first** — without per-board files in the upstream repo, the URL prefill flow doesn't fit.

---

## Constraints (non-negotiable)

1. **No daemon credentials.** ventd never holds a GitHub token, OAuth credential, browser cookie, or any other authentication artifact. The daemon does not call `api.github.com` for write operations.
2. **Browser-mediated submission.** The user's browser does the GitHub auth and PR creation. ventd's job ends at "open this URL".
3. **Visual review before submission.** User sees the redacted YAML in ventd's web UI before any external tab opens.
4. **Web UI only.** No CLI flag, no terminal command. The whole flow is clickable from `https://<host>:9999`.
5. **Headless-friendly.** Works when ventd is on a TrueNAS box accessed via SSH tunnel. The "browser" in the flow is the user's local browser, not the ventd host's.

These constraints are why we don't pursue full automation. Putting a GitHub PAT in the daemon would shorten the flow by one click but break #1, #2, and the safety story ventd has been built around.

---

## Architecture

```
┌────────────────────────────────┐
│ ventd web UI                   │
│ ┌────────────────────────────┐ │
│ │ Pending Profiles page      │ │
│ │  - lists profiles-pending/ │ │
│ │  - status badges           │ │
│ └────────┬───────────────────┘ │
│          │ click "Review"      │
│          ▼                     │
│ ┌────────────────────────────┐ │
│ │ Review modal               │ │
│ │  - YAML diff vs template   │ │
│ │  - redaction preview       │ │
│ │  - notes field             │ │
│ │  - [Submit] [Discard]      │ │
│ └────────┬───────────────────┘ │
└──────────┼─────────────────────┘
           │ click "Submit"
           ▼ window.open(url)
┌────────────────────────────────┐
│ GitHub web (user's browser)    │
│  - sign in if needed           │
│  - "Propose new file" page     │
│    (filename + content prefilled) │
│  - [Propose new file]          │
│  - PR creation page            │
│    (title + body prefilled)    │
│  - [Create pull request]       │
└────────────────────────────────┘
           │ on user click
           ▼
ventd marks local profile as "submitted"
(optimistic; reconciled later by polling main)
```

Total user clicks: **3** (Submit in ventd, Propose new file in GitHub, Create pull request in GitHub). Plus one if signed-out (GitHub login).

---

## URL prefill format

GitHub supports a "create new file" URL that prefills filename and content:

```
https://github.com/ventd/hardware-profiles/new/main/profiles/<vendor>/<slug>.yaml
  ?filename=<slug>.yaml
  &value=<URL-encoded YAML body>
  &message=<URL-encoded commit message>
  &description=<URL-encoded PR body>
```

Limits:
- Total URL length: ~8KB practical (some browsers reject longer)
- File content (`value` param) percent-encoded — YAML averages ~1.4× expansion
- A 4KB profile encodes to ~5.5KB of URL

The largest expected profile (lots of channels, full curves) ≈ 3-4KB raw → ~5KB encoded → fits comfortably under 8KB. spec-14a's per-board layout makes this work; vendor-grouped files would have broken it.

If a profile somehow exceeds the limit (pathological case: 30-channel server board with verbose labels), the UI falls back to **clipboard handoff** mode — see Failure modes.

---

## Data flow

### 1. Pending Profiles page

Reads `profiles-pending/` (or XDG fallback). Each file becomes a row:

| Fingerprint | Board | Captured | Status | Actions |
|---|---|---|---|---|
| a3f1b2c4… | MSI MEG X670E ACE | 2 hr ago | Pending | Review · Discard |
| b4e2c3d5… | (unknown) | 1 day ago | Submitted | View on GitHub · Mark merged |
| c5d3e4f6… | ASUS ROG STRIX… | 3 days ago | Merged | (auto-pruned in 7 days) |

**Status enum**:
- `pending`: just captured, never reviewed
- `reviewed`: user opened review modal and made edits, didn't submit
- `submitted`: user clicked Submit, ventd opened GitHub URL
- `merged`: reconciliation found this fingerprint in upstream `index.yaml`
- `abandoned`: 90 days in `submitted` without merging, or user marked manually

**Status persistence**: `profiles-pending/<fp>.yaml` gets a sidecar `<fp>.state.json`:

```json
{
  "status": "submitted",
  "submitted_at": "2026-04-26T13:00:00Z",
  "github_pr_url_observed": null,
  "transitions": [
    {"from": "pending", "to": "reviewed", "at": "2026-04-26T12:55:00Z"},
    {"from": "reviewed", "to": "submitted", "at": "2026-04-26T13:00:00Z"}
  ]
}
```

State files are local-only, not part of the YAML, never published.

### 2. Review modal

Loads the pending YAML. Shows three views:

- **Final YAML** (what will be submitted): the redacted form, after running redactor pipeline P1-P9
- **Diff against template**: highlights the calibration data + curves vs the boilerplate template structure, so user understands what's actually new
- **Redaction report**: small section at bottom showing "3 fields redacted: hostname, USB-physical, user label on fan2"

Editable fields:
- `metadata.contributor` (default: empty/anonymous, user can fill in their GitHub handle)
- `metadata.contributor_notes` (free text, becomes part of PR body)
- Per-channel `role` (cpu_fan / case_fan / pump / etc.) — guessed by heuristic but user-correctable
- Per-channel `label` (only if the captured label was redacted; user can re-type a vendor-style label)

Read-only fields (everything else): fingerprint, calibration data, kernel version, etc.

[Submit] button enabled only when validation passes. [Discard] removes the pending file (after confirmation).

### 3. Submit handler (in ventd's web backend)

When user clicks Submit:

1. Run the YAML through `internal/hwdb.ValidateProfile()` once more (defense in depth).
2. Run the redactor one final time, log any new redactions (should be zero — they were already done at capture; if they fire now, log a bug).
3. Compute the slug and filename: `profiles/<vendor-dir>/<slug>.yaml`.
4. Build the URL with prefilled value + message + description.
5. Write `<fp>.state.json` with `status: "submitted"`.
6. Return the URL to the browser via the API; browser does `window.open(url, '_blank')`.

Why the open happens browser-side: a redirect from the daemon would get blocked by popup blockers when initiated outside a click handler. The browser opens the tab in response to the user's click on Submit, satisfying the popup-blocker user-gesture requirement.

### 4. GitHub side (out of ventd's control)

User sees:
- New file editor with filename + content already filled
- Reads it again (third chance for them to bail)
- Clicks "Propose new file" → "Create pull request"
- GitHub opens PR creation page with prefilled title and body
- User clicks "Create pull request"

ventd has no signal that any of this happened. The `submitted` state is *intent*, not *fact*.

### 5. Reconciliation

A periodic task in ventd polls `https://raw.githubusercontent.com/ventd/hardware-profiles/main/index.yaml` (anonymous GET, no auth). Cadence: once per ventd startup + every 24 hours while running. Cheap.

For each entry in the local `profiles-pending/` directory in `submitted` state:
- If `fingerprint.hash` matches an entry in the upstream index → transition to `merged`, write timestamp.
- After 90 days in `submitted` without finding a match → transition to `abandoned`.
- Profiles in `merged` state for 7+ days → delete pending file and state file. They're upstream now, no local copy needed.

---

## Invariants (RULE-PROF-SUBMIT-NN)

These bind 1:1 to subtests in `internal/hwdb` and `internal/web`.

- `RULE-PROF-SUBMIT-01` — Submit button is disabled until `ValidateProfile()` returns nil error and redactor produces zero new redactions.
- `RULE-PROF-SUBMIT-02` — The YAML body shown to the user in the Review modal is byte-identical to the YAML body encoded into the URL `value` parameter.
- `RULE-PROF-SUBMIT-03` — Daemon makes no outbound HTTP request to `github.com` at submission time. Only `raw.githubusercontent.com` for reconciliation reads. Enforced by an HTTP allowlist test.
- `RULE-PROF-SUBMIT-04` — Status transitions follow the state machine: pending → reviewed → submitted → merged|abandoned. Any other transition is a bug.
- `RULE-PROF-SUBMIT-05` — `submitted` state is set only after backend's submit handler completes successfully, not on optimistic UI click.
- `RULE-PROF-SUBMIT-06` — Reconciliation polls `index.yaml` at most once per hour even under aggressive UI refresh; cached result reused.
- `RULE-PROF-SUBMIT-07` — Pending profiles in `merged` state for 7+ days are deleted by the GC task, including their state sidecar.
- `RULE-PROF-SUBMIT-08` — Pending profiles in `submitted` state for 90+ days are transitioned to `abandoned` by the GC task; YAML file kept (so user can re-review/re-submit), state sidecar updated.
- `RULE-PROF-SUBMIT-09` — Submitting a profile with `metadata.contributor: ""` rewrites the value to `"anonymous"` before URL build. Empty string is not a valid contributor.
- `RULE-PROF-SUBMIT-10` — URL length check: if encoded URL exceeds 7000 bytes, fall back to clipboard handoff (see Failure modes); never silently produce a URL that browsers might reject.

---

## UI surface (interacts with spec-12)

spec-12 is rewriting the ventd web UI. The Pending Profiles page should land in that work, not as a bolt-on to the current dashboard. Concretely:

- Add to spec-12's sidebar nav: **Pending Profiles** (badge with count if >0)
- Reuse spec-12's modal component for Review
- Reuse spec-12's diff/code-block renderer for the YAML preview

If spec-14b ships before spec-12, build the Pending Profiles page in the existing dashboard structure as a standalone tab; spec-12 absorbs it on its rewrite. This mirrors the existing pattern for Calibration and Fans tabs.

---

## Failure modes

### URL too long

**Trigger**: encoded URL > 7000 bytes.
**Detection**: backend computes URL length before returning to frontend.
**Behavior**: backend returns a different response shape: `{ "mode": "clipboard", "yaml": "<full>", "filename": "<slug>.yaml", "repo_url": "https://github.com/ventd/hardware-profiles/new/main/profiles/<vendor>/" }`.
**UI**: Submit modal shows two buttons instead of one — **Copy YAML** (writes to clipboard) and **Open GitHub** (opens the create-file folder). User pastes manually.

### GitHub auth wall

**Trigger**: user not signed in to GitHub when the new tab opens.
**Behavior**: GitHub redirects to login, then back to the new-file URL. The prefilled `value` param survives the round-trip in current GitHub behavior. If it ever stops surviving, fall back to clipboard mode (RULE-PROF-SUBMIT-10's threshold can be lowered to force this for everyone).

### User abandons in GitHub

**Trigger**: user closes the GitHub tab without clicking Create pull request.
**Behavior**: ventd state stays `submitted`. Reconciliation will find no matching entry upstream, profile stays `submitted` until 90-day GC kicks it to `abandoned`. User can manually re-trigger Submit from the Pending Profiles page (which re-opens the URL).

### Two users submit same fingerprint

**Trigger**: out of ventd's control — two different machines compute the same fingerprint hash and both contributors submit.
**Behavior**: maintainer dedupes at PR review time. ventd doesn't try to coordinate.

### Capture redactor missed something, CI catches it

**Trigger**: user submits, GitHub PR opens, CI runs `validate-pr.yml`, privacy regex sweep flags a leaked field.
**Behavior**: PR fails CI. Maintainer comments asking contributor to fix. User edits the file in GitHub or re-runs ventd capture with updated redactor. ventd logs nothing — this is a GitHub-side workflow.
**Mitigation**: file a ventd bug. The redactor should have caught it. Treat upstream CI failures as test cases for ventd's redactor.

---

## Out of scope

- OAuth-based fully-automated submission. Violates RULE-PROF-SUBMIT-03 and the no-credentials principle.
- Edit/delete of already-submitted PRs from ventd UI. Use GitHub directly.
- Multi-fingerprint profile contributions (one PR for several boards). Each profile is its own PR.
- Profile signing. Separate spec, post-v1.0.
- Maintainer-side review dashboard. Separate spec.
- Submission analytics ("how many profiles has ventd captured network-wide"). Privacy invariant: ventd doesn't phone home.

---

## PR plan (rough, write actual CC prompts later)

Estimated 4 PRs total, all Sonnet, roughly $5-15 each. Total: $20-60 for spec-14b implementation.

**Stage 0** (prerequisite): spec-14a bootstrap PR against `ventd/hardware-profiles`. ~$5-10.

**PR 1** — Pending Profiles state machine + GC
- `internal/hwdb/pending.go`: state types, sidecar JSON read/write, transition logic
- `internal/hwdb/pending_gc.go`: 90-day abandon, 7-day merged-prune
- Tests: state transition coverage, GC clock injection
- RULE-PROF-SUBMIT-04, 05, 07, 08

**PR 2** — Reconciliation polling
- `internal/hwdb/reconcile.go`: fetch index.yaml, match fingerprints, transition states
- HTTP client with 1hr cache, allowlist test
- RULE-PROF-SUBMIT-03, 06

**PR 3** — Submit URL builder + clipboard fallback
- `internal/hwdb/submit.go`: redact-validate-encode-buildURL, length check, fallback shape
- `internal/web/api/profiles.go`: HTTP handlers for list/review/submit
- RULE-PROF-SUBMIT-01, 02, 09, 10

**PR 4** — Web UI: Pending Profiles page + Review modal
- Frontend; pairs with spec-12 component conventions
- E2E test: capture → review → submit → state transitions

---

## Open questions

These don't block writing the spec but block implementation. Resolve before PR 1.

1. **Where does reconciliation run?** Options:
   - In-process goroutine started at daemon boot
   - Separate `ventd-reconcile` binary fired by a systemd timer
   - Web UI polls itself (no, this is wrong — a UI never being open means never reconciling)
   - **Tentative answer**: in-process goroutine. Cheap, already have the http.Client, no new binary.

2. **Where does the GC task run?** Same answer — same goroutine, same loop, runs daily.

3. **Does the Pending Profiles page show profiles that match the *current* host's fingerprint, or all profiles ever captured on this machine?** Probably "all", because users might calibrate a separate machine and submit from there. But surface a "this is for this machine" filter.

4. **What happens to pending profiles when the user upgrades ventd and the schema bumps to v2?** Migration logic on read; old `<fp>.state.json` files may need a schema_version field of their own. Defer until schema v2 is needed.

5. **Should we generate profile slugs deterministically and ban contributors from changing them, or treat the slug as a hint?** Hint. Maintainers may rename during PR review (e.g., to disambiguate revisions). ventd's reconciler matches on `fingerprint.hash`, not filename, so slug changes don't break consumption.

---

## Testing infrastructure (synthetic profile generator)

Before E2E-testing the submission flow against real users, we need a way to pump fake-but-valid profiles through the pipe to exercise the state machine, reconciliation, GC, and clipboard fallback paths.

**`tools/synth-profile/`** — small Go program (CC-writable, ~$3-5 in one Sonnet session):

- Reads a board entry from `internal/hwdb/catalog/boards/*.yaml`
- Generates a plausible profile YAML (random-but-realistic `start_pwm` 20-40, `stop_pwm` 15-30, `max_rpm` 1500-3000, monotonic 4-point curves)
- Computes a real fingerprint hash for the board (so reconciliation can find it later if you fake the upstream side too)
- Writes to `profiles-pending/<fp>.yaml` exactly as the capture pipeline would
- Optional flag `--count N` to generate N profiles for stress-testing the Pending Profiles UI

**Why this is a separate tool, not a test fixture:**

Tests should use deterministic seed data, not random. This tool is for *manual* exploratory testing — does the GC actually fire? Does the URL builder actually hit the 7KB threshold on a fat profile? Does the reconciliation handle 100 pending entries without UI lag? Those are operator questions, not assertions.

**Out of scope for `tools/synth-profile/`:**

- Faking calibration data that pretends to be real (don't pollute upstream).
- Fake hwmon sysfs tree (separate project — would unblock HIL-free calibration testing but has safety implications).
- Replaying real captures (a `tools/replay-profile/` would do that; deferred until needed).

**Bound RULE**: none. This is dev tooling, not production code. Excluded from rulelint via comment in the main file.

---

## Subtest mapping

Each RULE binds to a Go subtest (1:1 per `.claude/rules/profile-submit.md`):

| Rule | Subtest |
|---|---|
| RULE-PROF-SUBMIT-01 | `TestSubmit_DisabledOnInvalidProfile` |
| RULE-PROF-SUBMIT-02 | `TestSubmit_PreviewMatchesEncodedURL` |
| RULE-PROF-SUBMIT-03 | `TestSubmit_NoOutboundGithubAPI` |
| RULE-PROF-SUBMIT-04 | `TestPending_StateTransitions` |
| RULE-PROF-SUBMIT-05 | `TestSubmit_StateOnlyAfterBackendOK` |
| RULE-PROF-SUBMIT-06 | `TestReconcile_HourlyCacheBound` |
| RULE-PROF-SUBMIT-07 | `TestPendingGC_MergedPrune` |
| RULE-PROF-SUBMIT-08 | `TestPendingGC_SubmittedToAbandoned` |
| RULE-PROF-SUBMIT-09 | `TestSubmit_AnonymousFallback` |
| RULE-PROF-SUBMIT-10 | `TestSubmit_LongURLFallsBackToClipboard` |
