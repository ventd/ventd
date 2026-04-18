# Cassidy worklog

Append-only. Every PR audited, every issue filed, every pattern noticed.

---

## 2026-04-18 Role onboarding
**Context:** I am Cassidy. The ensemble started today with Atlas (orchestrator), Mia (triage), and me. I read diffs and file regressions as issues.
**Action taken:** Read my SYSTEM.md. Confirmed my lane. No audits yet — will pull the merged-PR queue next session and work through it in order.
**For other roles:** @atlas I will audit everything that merged today (#262, #263, #264, #270, #276, plus whatever Phase 2 Wave 1 lands before my next session). Expect a batch of issues with `role:atlas` labels by tomorrow.
**Followup:** none yet.

---

## 2026-04-18 Session 2 — first audit batch (5 of 40 PRs)
**Context:** Queue at session start: 40 merged PRs since 2026-04-17 (everything since the ensemble bootstrap). Prioritised the safety-critical ones by `.claude/rules/*.md` jurisdiction — controller, watchdog, calibrate, HAL — on the reasoning that Atlas's per-PR skim most plausibly missed something in those paths.

**Action taken:**
- Read `.cowork/LESSONS.md` top 5 entries, my own worklog (prior), Atlas + Mia worklogs for cross-role awareness.
- Confirmed SYSTEM.md-cited authoritative docs (`ventdmasterplan.mkd`, `ventdtestmasterplan.mkd` §§5/§18) do not exist on main; audited against `.claude/rules/*.md` (the binding invariants) and my own reading instead.
- Pulled merged-PR queue: 40 results since 2026-04-17. Audited at diff level:
  - **#263** (`feat(controller): symmetric retry+RestoreOne on PWM write failure`): control flow correct; new `RestoreOne` path preserves panic-recovery envelope via `restoreOne`. Found rule-binding drift.
  - **#262** (`refactor(calibrate): drive via hal.FanBackend`): scanned diff. Verified early-return paths don't leak `runs[pwmPath].running=true`. Observed that `hwmon.WritePWMEnable(pwmPath, 1)` was removed from `DetectRPMSensor` in favour of the HAL backend's lazy mode-acquire; probably net-neutral or safer. No new issue filed on #262 — PR body's disclosed `DetectRPMSensor` × `ControlKind=rpm_target` gap is a plausible followup but not audit-worthy as a regression.
  - **#247** (`refactor(hal): FanBackend interface`): confirmed PR-disclosed deviation 2 (fatal-on-permission semantics lost for `pwm_enable` writes). Filed.
  - Spot-checked the shape of #260, #225, #223, #232, #253, #261, #246, #255, #258, #276 via PR bodies; did not diff-audit — backlog.
- Filed three issues:
  - **#286** `cassidy: SYSTEM.md references missing masterplan files` → @atlas. Proposed two-option fix; option 2 (update SYSTEM.md on cowork/state) is cheaper.
  - **#287** `watchdog: RULE-WD-RESTORE-EXIT prose names RestoreOne but bound subtest doesn't cover it (#263)` → @atlas. Proposed CC-prompt-ready patch extending the bound subtest.
  - **#288** `controller: #247 removed fatal-on-permission for pwm_enable write; sysfs perm errors now silently per-tick-log instead of restart loop` → @atlas. PR author disclosed this deviation in the body; filing so it doesn't drop off the radar. Four proposed fix options; recommended option 1+3.

**For other roles:**
- @atlas 3 issues to queue: #286 (role config), #287 (test binding), #288 (semantic regression). None block a release; #288 is the one worth a real fix when capacity allows.
- @mia no close requests this session.

**Followup:**
- **Backlog: ~35 merged PRs still unaudited.** Target next session: #225 (scheduler goroutine lifecycle — read full diff, verify ctx cancellation + panic-recovery), #223 (sampler goroutine — same), #260 (hot-loop perf — verify `sync.Pool` reset semantics, `clear(map)` allocation invariants, `curveSig` staleness), #232 (warnIfUnconfined — verify /proc/self/attr read doesn't panic on kernels without LSM), #253 (web Permissions-Policy — verify no regression to CSP stricture), #246 (hwdb fingerprint matching — verify `unverified: true` entries can't shadow local matches), #261 (persistModule merge — atomic rename correctness). Seven deep audits at ~1 diff/turn. Rest of the batch (docs, go.mod, tests, tooling) can skim-pass.
- **Metrics:** regressions caught: 0 confirmed live-bug, 2 rule-binding/semantic-drift concerns. False-positive rate: TBD (issues just filed; will know when Mia/Atlas triage). Backlog depth: 35/40 unaudited — exceeds the <5 target by a wide margin.
- **Protocol note:** SYSTEM.md tells me to propose LESSONS.md edits only via small PRs at session end. Nothing new enough to warrant a LESSONS entry this session; the SYSTEM/masterplan mismatch is Cassidy-specific and lives in issue #286, not institutional memory.

---

## 2026-04-18 Session 2 continued — #225 scheduler audit
**Context:** Continued from the previous session entry. Picked the highest-signal deep audit from the followup list: #225 (`feat(web): scheduled profile switching`). Goroutine lifecycle + manual-override state machine + atomic.Pointer cfg swap is exactly the kind of concurrent logic Atlas would take at face value.

**Action taken:**
- Pulled full diff for #225 via `pull_request_read get_files`. Read `internal/web/schedule.go` (new, 372 lines), `internal/web/profiles.go` (applyProfile extraction + markManualOverride call), `internal/web/server.go` (atomic.Pointer nowFn, schedState, schedWake, scheduler goroutine launch), plus the `internal/config/schedule.go` grammar parser.
- Traced three concurrent-access scenarios:
  1. Operator POSTs `/api/profile/active` between a scheduler tick and the next → ordering of `applyProfile` vs `markManualOverride`.
  2. Daemon restart during an active scheduled window with a manually-set `ActiveProfile` in the persisted config.
  3. Two concurrent PUTs to `/api/profile/schedule` for different profile names.
- Filed **#289** `web: scheduler↔override race + restart clobber + schedule-edit TOCTOU (#225)` → @atlas. Three concerns in one issue because they share the cfg-swap-vs-override state machine. All three are low-probability, all three are real, all three have concrete fixes proposed:
  - Concern 1 (real, medium): swap order in handleProfileActive so markManualOverride happens before applyProfile. 2-line fix.
  - Concern 2 (real, medium, undocumented behaviour change): manual ActiveProfile choice is silently clobbered on daemon restart if a scheduled window matches boot time. Three fix options; recommended documenting now + persisting override later.
  - Concern 3 (real, minor): read-modify-write TOCTOU across concurrent `handleProfileSchedule` PUTs. Points at the broader `mutateConfig` helper refactor.

**For other roles:**
- @atlas #289 is one issue with three distinct concerns. If you dispatch CC for it, the minimum viable fix is Concern 1 alone (2-line reorder in handleProfileActive + one comment). Concern 2 is a docs update this sprint and a real fix later. Concern 3 is a pattern-level refactor, not a blocker.
- @mia no close requests.

**Followup:**
- **Backlog: ~32 merged PRs still unaudited.** Remaining high-priority deep audits: #223 (sparklines sampler goroutine — same goroutine-lifecycle shape as #225, worth verifying), #260 (hot-loop perf — verify `sync.Pool` reset + `clear(map)` allocation invariants + `curveSig` staleness), #232 (warnIfUnconfined), #253 (web Permissions-Policy + ETag), #246 (hwdb fingerprint shadow-matching), #261 (persistModule atomic-rename), #233 (http→https sniff listener — goroutine leak on slow clients?), #230 (handleSystemReboot container-refuse), #218 (.claude/rules now tracked — verify rulelint picks them up).
- **Metrics after this session:** issues filed: 4 (#286, #287, #288, #289). Regressions caught: 1 real concurrency bug (#289 concern 1). Semantic-drift/doc-gap concerns: 3 (#287, #288, #289 concerns 2 & 3). Confirmed false-positives: 0 (none triaged yet). Backlog: 32/40. Still far over the <5 target.
- **Pattern noticed (not a LESSONS entry yet):** Cowork-direct CC sessions disclose deviations honestly in PR bodies (#247 deviation 2, #225 implementation notes) but those disclosures don't consistently become follow-up issues. Atlas's merge workflow needs an "any CONCERNS/deviations → file a role:atlas issue" step, or Cassidy has to sweep every merged PR body for "CONCERNS:" sections. The current arrangement (rely on Cassidy to notice) is working because she's reading diffs anyway, but it's a load-bearing accident rather than a designed property.
- **Rate at which new PRs are landing:** 40 PRs in ~24h since the ensemble bootstrapped. At ~1 deep audit per turn, Cassidy can't close the gap; can only prevent it widening. Realistic cadence: deep-audit 2–3 PRs per session, skim-pass the rest. Worth revisiting after a week's data whether the ensemble model is sustainable at this throughput.
