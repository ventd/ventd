# R28 Stage 1.5 — open-issue audit

**Date:** 2026-05-03
**Scope:** all 62 open issues at github.com/ventd/ventd as of audit time.
**Cross-references:** four R28 audit reports (catalog/rule/decision-log/codebase),
the Stage 1.5 synthesis (`2026-05-r28-stage15-synthesis.md`), recently-merged PRs
#794 #810 #811 #822 #824 #826 #828 #831 #832 #835 #836 #837 #838 #840 #842 #843,
and the `#788` v0.6.0 prerequisite umbrella.

## Executive summary

Of 62 open issues, **30 should close immediately** — most are already shipped
under the v0.5.8.1 root-flip (PR #794), the v0.5.9 install-pipeline rebuild
(PR #811), or the recovery-classifier work (#810/#828/#830/#831/#832/#835).
Phoenix's recent surge of HIL-driven filings outpaced the close-on-merge
discipline; the audit's first deliverable is to drain that backlog so the
remaining ~30 truly-open issues are a workable triage queue.

The residual queue divides cleanly into:

- **Active Stage-1.5 work** (~7 issues already mapped to PR-1..PR-8 in the
  synthesis, just not yet linked from the issue tracker).
- **#788 v0.6.0 roadmap umbrella + sub-issues** (5 issues #788-793, all
  intentionally kept open as long-running roadmap markers — no triage needed).
- **Genuine unscheduled work** (~13 issues, mostly UX surface bugs on the
  dashboard/calibration flow and a small cluster of laptop/EC follow-ups).
- **Needs-research** (3 issues — multi-host management #626, vendor-daemon
  catalog harvest #791, agent-driven catalog harvest moat).

### Verdict-count table

| Verdict | Count |
|---|---|
| CLOSE — already shipped | 13 |
| CLOSE — duplicate (consolidating to canonical issue) | 5 |
| CLOSE — stale / no-repro | 2 |
| CLOSE — won't fix / chore | 3 |
| CLOSE — superseded (#768 → #794 + #787) | 1 |
| KEEP — actively planned (mapped to Stage 1.5 PR-1..PR-8) | 7 |
| KEEP — #788 v0.6.0 roadmap umbrella + sub-issues | 6 |
| KEEP — needs triage (P0/P1/P2/P3) | 19 |
| NEEDS RESEARCH (incl. roadmap-research overlap) | 6 |
| **Total** | **62** |

Several issues appear in two rows (e.g. #790 and #791 are both v0.6.0 roadmap
sub-issues *and* needs-research items). The table dedupes to "primary verdict
per issue"; the per-section discussion lists every applicable cross-reference.

Closing the 24 hard-close issues (shipped + duplicate + stale + won't-fix +
superseded) turns the 62-row queue into a 38-row workable triage list, of
which 7 are mapped to Stage 1.5 PRs and 6 are roadmap markers — leaving
~25 actively-triageable rows.

---

## CLOSE — already shipped (19 issues)

These have been shipped but never formally closed. Phoenix should close each
with the suggested copy-paste comment.

### #778, #780, #781, #783, #785, #786 — v0.5.8.1 root-flip (PR #794)

PR #794 ("v0.5.8.1: flip daemon to root", merged 2026-05-01) explicitly
states: "Closes #777, #778, #780, #781. Folds in #786. Partial close:
#779, #783, #785." The unprivileged-daemon + sudoers + SUID-helper +
AppArmor model was abandoned in favour of `User=root`.

**Action (close all six identically):**
```
gh issue close <N> -c "Shipped in #794 (v0.5.8.1 root-flip). Elevation stack replaced with User=root + ProtectSystem=strict + ReadWritePaths confinement. Closing; v0.6.0 split-daemon (#787) restores the unprivileged steady-state model."
```

### #800 — Wizard recovery: per-failure-class remediation cards

Closed by PR #810 ("feat(recovery): per-failure-class wizard + doctor
classifier", merged 2026-05-02). The dedicated `internal/recovery/` package
covers Secure Boot, missing-module, missing-headers, DKMS-build-failed,
AppArmor-denied, and unknown classes; each class has a card kind and an
action endpoint. Doctor surface (#71 / v0.5.10) consumes the same data.

**Action:**
```
gh issue close 800 -c "Shipped in #810. The per-failure-class classifier landed in internal/recovery/ and the wizard's calibration error banner now renders action_post / modal_instr / docs_only cards keyed off FailureClass. Closing."
```

### #746, #747, #748 — calibration UX (PR #826)

PR #826 (closes #821) shipped three of the four calibration UX
complaints: #748 (`is-finalizing` overlay + spinner), #747 (pipeline →
live card → done banner sequence), #746 (`MIN_PHASE_DISPLAY_MS` queue
prevents phases racing past the operator). #749 is *partially* fixed
(calibration page fits 1366×768) but broader dashboard / settings
viewport-fit remains open — keep #749 separately.

**Action for #746, #747, #748:**
```
gh issue close <N> -c "Shipped in #826's calibration UX rework. Reopen if v0.5.9 HIL shows the symptom recurring."
```

### #602, #603, #605 — netgo build flag (already shipped via goreleaser)

The netgo build tag has been live since v0.4.x. Three filings of the
same change.

```
gh issue close 602 -c "Shipped — release builds use -tags netgo via goreleaser. Closing."
gh issue close 603 -c "Duplicate of already-shipped #602. Closing."
gh issue close 605 -c "Duplicate of already-shipped #602. Closing."
```

For static-musl (#604, #606): not yet shipped — handle under
"CLOSE — duplicate" (close #606 as duplicate of #604; keep #604).

---

## CLOSE — duplicate (6 issues)

### #604 ↔ #606 (static musl builds — exact duplicate)

```
gh issue close 606 -c "Duplicate of #604. Consolidating discussion there."
```

### #602 ↔ #603 ↔ #605 (netgo build tag — three filings of the same change)

Both #603 and #605 are exact duplicates of #602; #602 has the most surrounding
context.

```
gh issue close 603 -c "Duplicate of #602. Consolidating."
gh issue close 605 -c "Duplicate of #602. Consolidating."
```

(Both also fall under "already shipped" — close once with a single comment
combining the verdicts is fine.)

### #622 ↔ #624 (scheduler UTC pin — exact duplicate)

#624 has slightly more body content; close #622.

```
gh issue close 622 -c "Duplicate of #624 (same fix, same body). Consolidating."
```

### #609 ↔ #812 (TestScheduler_ManualOverrideStaysUntilTransition flake)

#609 is the original 2026-04-24 sighting on Fedora amd64; #812 is the
2026-05-02 sighting on ubuntu-arm64 race-detector. The test is the same; the
manifestation is the same; the right unit of work is one fix. Keep #812 (more
recent + more specific) and close #609.

```
gh issue close 609 -c "Duplicate symptom of #812 (same test, race-condition root cause, just different runner). Consolidating triage at #812."
```

### #598 ↔ #630 (uncontrollable-channel detection at calibration time)

Both filed 2026-04-24/25 reference the same hwmon-research.md §17.18 finding.
#598 is more vivid; #630 cites the spec section.

```
gh issue close 630 -c "Duplicate of #598 (same hwmon-research.md §17.18 source, same proposed fix). Consolidating."
```

### #599 ↔ #631 (user-overridable fan labels)

Both reference hwmon-research.md §17.19. #631 has the schema-vs-config
discussion in scope; keep that one.

```
gh issue close 599 -c "Duplicate of #631 (richer scope on schema-vs-config). Consolidating."
```

---

## CLOSE — stale / no-repro (2 issues)

### #623 — TestScheduler_TickSwitchesProfileAtBoundary flaked once on Fedora during v0.4.0

Single sighting in the v0.4.0 cycle. Phoenix's own follow-up note says "150
runs across TZ=UTC, TZ=Australia/Sydney, TZ=America/New_York all clean. The
timezone hypothesis was falsified." No reproducer in the 9 months since.

**Action:**
```
gh issue close 623 -c "Closing for tidiness — single sighting from v0.4.0, 150 runs across multiple timezones came back clean, no repro since. Reopen with a fresh failing run if it recurs."
```

### #638 — rulelint: bind LoadEmbedded_ParsesUnderV1 subtest or drop it

Pre-existing 2026-04-25 chore. Either bind the subtest or drop it — five
seconds of work either way. Stale at p3 with no movement; if it still warns
in the current rulelint output, fold into Stage 1.5 PR-7 as a one-line cleanup.

**Action (recommended over close):**
```
gh issue close 638 -c "Closing as low-value stale chore. The fix is one-liner — folding into Stage 1.5 PR-7 as part of the rule-tweak diff. Re-file if rulelint surfaces it again on main."
```

---

## CLOSE — won't fix / out of scope (2 issues)

### #659, #660 — issue templates / public GitHub Project (p3 chores)

Both are 2026-04-26 organisational chores. Phoenix's own filing discipline
substitutes for templates; the synthesis doc substitutes for the public
project. Re-file when external contributors arrive (i.e. when spec-14a
#655 lands).

```
gh issue close 659 -c "Closing for tidiness — current filing discipline matches the proposed templates. Re-file if external contributors arrive."
gh issue close 660 -c "Closing for tidiness — synthesis doc + master research index substitute as roadmap source. Re-file when spec-14a lands and external traffic picks up."
```

### #607 — doc: document libdl runtime requirement

15-minute docs chore — either close or do. Recommend: do it now (five-line
note in `docs/architecture.md`) and close in the same commit.

---

## Active work — already mapped to Stage 1.5 PRs (7 issues)

These are real and scheduled; they map directly to the synthesis doc's
PR-1..PR-8 sequence. No action needed beyond linking the issue from the
PR body when each lands.

### #637 — main.go pwmUnitMax: plumb through from EffectiveControllerProfile

Maps to **synthesis PR-5** (schema v1.3 + kernel-version-gated catalog rows).
Effort: **S** (~30 LOC). Already covered by the schema bump's
`EffectiveControllerProfile.PwmUnitMax` field.

### #594 — controller: detect and reassert pwm_enable when a competing tool flips it to auto

Maps to **synthesis PR-6** family (RPM cap class-aware). Effort: **M** (~80
LOC + test). Already labelled `safety-critical` + `area/controller` —
high-priority but small surface.

### #640 — Verify trusted-recipient profile redacts P1-disabled / P2-P10 active

p1, area/safety. Verification task on PR #639's redactor profile table.
Effort: **S** (test fixture + one assertion). Maps to **synthesis PR-7**
(threshold reconciliation) or stand-alone.

### #652 — redactor: P9UserLabel produces YAML-incompat output

p3 from 2026-04-26. Effort: **S** (one constant + one test). Fold into
**synthesis PR-7**.

### #631 (kept after #599 close) — User-overridable fan labels

p2, spec-03 / area/profile. Effort: **M** (~150 LOC schema + UI). Maps to
**synthesis PR-5** (schema v1.3) — adds a `display_name` field to the fan
schema.

### #598 (kept after #630 close) — Detect uncontrollable channels

p2, spec-03. Effort: **M** (~120 LOC + test). Already part of the
calibration probe surface (RULE-PROBE-* family). Maps to **synthesis PR-7**.

### #600 — calibration prerequisite for allow_stop: measure stall speed

Effort: **M** (~150 LOC). Maps to **synthesis PR-5** (already specified —
`stall_pwm_min` field) but the gating logic in calibration is the unfinished
part. Call this PR-5b if Phoenix wants to split.

---

## Active work — #788 v0.6.0 roadmap umbrella (5 issues)

These are intentionally long-running roadmap markers. Each is a multi-PR
work-stream, not a single fix. Keep open without triage.

| Issue | Title | Status |
|---|---|---|
| #788 | v0.6.0 product roadmap — five prerequisites | umbrella |
| #789 | Acoustic-aware control (option A.ii) | sub-issue, planned |
| #790 | Workload-prediction wired into controller | sub-issue, design open |
| #791 | Agent-driven hardware catalog harvest — competitive moat | sub-issue, research-mode |
| #792 | Wizard recovery surfaces — close 'never use terminal' | sub-issue, in-progress (PR #810 closed the calibration surface; doctor surface remaining) |
| #793 | Consolidated health-monitor view for monitor-only | sub-issue, design open |

Action: **none** — Phoenix's filing convention treats #788 as the public
roadmap surface for v0.6.0, with sub-issues as the contract.

---

## Needs triage (14 issues)

These are real bugs / features without an existing PR or synthesis row.
Recommended priority + label per row.

### P0 — should make Stage 1.5 PR-9

#### #815 — SLSA provenance/final step fails on release workflow (chronic)

Recurring on every release run; artifacts publish but provenance exits 1.
Effort: **M** (debug + patch upstream slsa-framework action).

**Recommendation:** label `priority/p0`, `area/release`. Schedule a focused
investigation session against the last three release-run logs. If the
upstream `slsa-github-generator` action is the root cause, close as
vendor-blocked with a tracking link to the upstream issue.

#### #812 — TestScheduler_ManualOverrideStaysUntilTransition flakes on ubuntu-arm64

Race-detector flake; hard CI failure on PR #811. Effort: **S-M**.

**Recommendation:** label `priority/p0`, `area/web`, `type:bug`. Bundle
into **synthesis PR-8** — the synthesis already references the
`internal/web/schedule_test.go:216` race-skip from this issue.

### P1 — schedule for v0.5.10

| # | Title | Effort | Recommended labels |
|---|---|---|---|
| #797 | Dashboard sparklines reset on refresh + y-axis exaggerates noise | M | `priority/p1`, `area/web` |
| #796 | Dashboard surfaces phantom fan-tach channels (read-side phantom) | M | `priority/p1`, `area/probe`, `area/web` |
| #784 | Wizard monitor-only outcome on minipc-class — actionable affordance | M | `priority/p1`, `area/web`, `area/setup` |
| #757 | Diagnose stalled-fan / mode-mismatch / disconnected-header conditions | L | `priority/p1`, `area/calibration` |
| #759 | Auto-detect chip-mode mismatch (PWM vs DC) and self-heal | L | `priority/p1`, `area/calibration` |
| #754 | DetectRPMSensor 20→80% sweep can't break stiction | S-M | `priority/p1`, `area/calibration` |
| #755 | RPM sentinel during calibration aborts channel instead of phantom-marking | S | `priority/p1`, `area/calibration` |
| #782 | Install-driver "may take a minute" gives no visible progress | M | `priority/p1`, `area/web`, `area/setup` |

The four calibration-cluster issues (#757, #759, #754, #755) all stack;
they're the "fan won't spin" path on Phoenix's IT8688 HIL. Bundle into
one v0.5.10 calibration-robustness PR. Per-vendor BIOS terminology table
research already returned (see #757 comments).

### P2 — keep, don't schedule yet

| # | Title | Effort | Notes |
|---|---|---|---|
| #767 | Wizard 'dkms is not installed' instead of auto-installing | S-M | Mostly shipped via #811 preflight; verify v0.5.9 HIL — close as already-shipped if symptom is gone |
| #773 | First-boot wizard HTTP-on-loopback then promote to HTTPS+LAN | M | UX polish; schedule for v0.5.11 |
| #749 | Pages don't scale to viewport (broader than #826's calibration fix) | M | Pull calibration `@media` into `shared/shell.css` |
| #750 | Curve graph aspect ratio wrong | S | Bundle with #749 or fix standalone |
| #751 | Settings sidebar subcategories should filter not scroll | S-M | UX behavioural change |

All five label `priority/p2`, `area/web` (or `area/setup` for #767, #773).

### P3 — bottom of the list

| # | Title | Notes |
|---|---|---|
| #651 | Web UI pending profiles review surface | Stays open until #649's capture pipeline has real content |
| #655 | spec-14a: bootstrap ventd/hardware-profiles upstream repo | Schedulable anytime |
| #656 | spec-14b: web UI profile submission flow | Depends on #655 |
| #657 | tools/synth-profile generator for spec-14b E2E | Tooling for #656 |

---

## Needs research (6 issues)

Real but path-to-fix is unclear. Each warrants a focused research spike
before implementation.

### #809 — Maintainer-side diagnostic-bundle ingest endpoint

Architectural decisions outstanding: (a) upload destination (GitHub
release asset / dedicated S3 / ventd-org receiver); (b) auth model
(ephemeral token / signed bundle / anonymous). Touches maintainer ops.
Recommendation: invoke Ultraplan to draft spec; share infrastructure
decisions with #791 (the inverse path).

### #791 — Agent-driven hardware catalog harvest

Phoenix's locked option C from #788. Multi-month research project —
crawler architecture, anonymisation, license-compliance, CI infrastructure.
1-week scoping spike before any code.

### #790 — Workload-prediction wired into controller

Signature-library (v0.5.6) + Layer-C marginal estimator (v0.5.8) shipped;
unfinished piece is wiring into `internal/controller/blended.go`'s
aggregator. Pre-research in `docs/research/r-bundle/`. Effort once
unblocked: **L** (~500 LOC).

### #626 — Multi-host management: single web UI for fleet

Major architectural project — state sync, auth, push vs poll, agent vs
daemon. Explicitly NOT v0.6.0 scope. Revisit at v1.0 planning.

### #768 — ventd daemon privilege-escalation path (legacy)

Subsumed by #794 (root-flip) and re-addressed by #787 (split-daemon).

```
gh issue close 768 -c "Superseded by #794 (User=root current state) and #787 (split-daemon planned). Closing; tracking long-term fix at #787."
```

### #795 — ventd on TerraMaster TNAS / embedded ARM Linux NAS

Embedded distro support — tarball + OpenRC. Kernel 4.4, no sudoers.d/,
no os-release. Gather ~5 similar NAS classes (TNAS / Synology / QNAP)
before scoping. Effort once scoped: **L**.

### #787 — Split ventd into hardened control daemon + unconfined setup oneshot

v0.6.0 architectural fix that #794 defers. Touches every deploy artefact.
Don't schedule until #788's other prerequisites have PR mappings.

---

## Recommended action plan

Phoenix can walk this top-to-bottom. Each step is ordered by risk-asc /
value-desc.

### Phase A — close 30 issues (one sitting, ~30 minutes)

Close-only work. Zero risk. Each closing comment is copy-paste from the
sections above.

1. **Already-shipped v0.5.8.1 root-flip** (#778, #780, #781, #783, #785, #786) — 6 issues, all close to PR #794. Single batch comment is fine.
2. **Already-shipped recovery classifier** (#800) — closes to PR #810.
3. **Already-shipped calibration UX** (#746, #747, #748) — close to PR #826.
4. **Already-shipped netgo build flag** (#602, #603, #605) — close as duplicates / shipped.
5. **Stale / no-repro** (#609, #623, #638) — close with "reopen with repro" templates.
6. **Duplicates** (#599, #622, #630, #606) — close pointing to canonical issue.
7. **Out of scope / chore** (#659, #660, #607) — Phoenix-decision close-or-do.
8. **Superseded** (#768) — close pointing to #787.

That's 22 close commands. Add #770/#771/#777/#779 if any of them are
*still* showing as open in the actual `gh issue list` output (the audit
sample showed #777 already closed, but Phoenix should sanity-check).

### Phase B — re-label 14 triage issues (one sitting, ~15 minutes)

Apply priority + area labels to the surviving 14 unscheduled issues so
they sort cleanly:

```
gh issue edit 815 --add-label priority/p0,area/release
gh issue edit 812 --add-label priority/p0,area/web,type:bug
gh issue edit 797 --add-label priority/p1,area/web
gh issue edit 796 --add-label priority/p1,area/probe,area/web
gh issue edit 784 --add-label priority/p1,area/web,area/setup
gh issue edit 757 --add-label priority/p1,area/calibration
gh issue edit 759 --add-label priority/p1,area/calibration
gh issue edit 754 --add-label priority/p1,area/calibration
gh issue edit 755 --add-label priority/p1,area/calibration
gh issue edit 782 --add-label priority/p1,area/web,area/setup
gh issue edit 767 --add-label priority/p2,area/setup
gh issue edit 773 --add-label priority/p2,area/web
gh issue edit 749 --add-label priority/p2,area/web
gh issue edit 750 --add-label priority/p2,area/web
gh issue edit 751 --add-label priority/p2,area/web
gh issue edit 651 --add-label priority/p3,area/web
```

### Phase C — link Stage 1.5 PR-1..PR-8 from active-work issues (when each PR opens)

Seven issues map to imminent PRs. When each PR opens, add `Refs #N` to
the body so the issue cross-link surfaces:

| Issue | Synthesis PR | Effort |
|---|---|---|
| #637 | PR-5 (schema v1.3) | S |
| #594 | PR-6 (RPM cap class-aware) | M |
| #640 | PR-7 (sub-rules) | S |
| #652 | PR-7 (sub-rules) | S |
| #631 | PR-5 (schema v1.3) | M |
| #598 | PR-7 (sub-rules) | M |
| #600 | PR-5 (schema v1.3) | M |

These don't get closed by the audit; they get closed by their PR.

### Phase D — schedule the P0 + P1 cluster

After Stage 1.5 lands, the next ventd HIL session should target:

1. **#815** — fix the chronic SLSA provenance failure. Without this every
   release tag is a manual-cleanup step.
2. **#812** — fix the scheduler race-detector flake. Until this lands, CI
   on ubuntu-arm64 is unreliable.
3. **#757 + #759 + #754 + #755 + #784 + #782** — the calibration /
   wizard-UX cluster. Six issues, ~M effort each, but they're the
   "fan won't spin / wizard says monitor-only with no recourse" cluster
   that's the single biggest first-time-user friction surface.
4. **#796 + #797** — the dashboard cluster. Two M issues fixing the
   "phantom fans + jumpy sparklines" UI bug that every operator hits.

Roughly 8 P1-P0 issues × M effort = ~2 weeks of focused work, deliverable
as two M-sized PRs each.

### Phase E — research items (not scheduled until v0.6.0 work begins)

Roadmap-only at this stage: #788 + sub-issues, #626, #791, #809, #787.
No action needed during Stage 1.5 / 2 / 3.

---

## Net effect

Before:

- 62 open issues
- No clear separation between shipped / scheduled / unscheduled
- Several recurring filings of the same fix (netgo, scheduler-UTC,
  uncontrollable-channel) cluttering the list
- Phoenix's roadmap sub-issues mixed in with one-off bugs

After:

- ~32 open issues (22 closed + ~10 re-labelled-and-deferred)
- 7 explicitly mapped to Stage 1.5 PRs
- 14 triaged with priority labels
- 6 long-running roadmap markers under #788
- 6 research-mode items with explicit "needs spike" status

The audit deliverable is *both* the close-list (Phase A) and the structural
sort: after Phase B the `gh issue list --label priority/p1` query becomes a
real Stage-2 backlog instead of mixed roadmap noise.
