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

---

## 2026-04-18 Session 2 continued — #223 sparklines audit
**Context:** Second deep audit of the session. #223 adds a per-metric ring buffer + sampler goroutine + `/api/history` endpoint + client-side sparkline rendering. Goroutine-lifecycle shape mirrors #225 so the audit question was whether it fell into the same traps.

**Action taken:**
- Pulled full diff for #223. Read `internal/web/history.go` (new, 314 lines), `internal/web/history_test.go` (new, 433 lines), `internal/web/e2e_test.go` sparkline test, and the client-side `sparkline.js` + `api.js` + `render.js` changes.
- Verified goroutine hygiene: `go s.runHistorySampler(ctx)` launched once from `New()`, exits on `<-ctx.Done()`, `defer t.Stop()` on the ticker. Correct and clean — unlike #225, no cadence-override wake channel, so no equivalent of the #225 race.
- Verified field mapping: client JS reads `f.duty_pct`; Go side's `fanStatus.Duty` carries JSON tag `"duty_pct"` (confirmed in server.go). Not a bug.
- Read `internal/config/config.go` `validate()` to check whether sensor+fan name uniqueness is enforced across namespaces. It isn't — `sensors` map and `fans` map are populated in isolation, never intersected. A sensor named `"cpu"` and a fan named `"cpu"` both pass validation, then silently collide in `HistoryStore`'s `historyBuf["cpu"]` and the sparkline shows interleaved °C-and-duty-% values.
- Filed **#293** `config: validate doesn't reject sensor/fan name collisions, corrupting sparkline history keyspace (#223)` → @atlas. Low severity (cosmetic only, PWM control unaffected), one-line fix proposed: intersect `sensors` and `fans` maps in `validate()` and return an error on overlap.

**Not filed (considered and dropped):**
- Timestamp reconstruction skew in `HistoryStore.Snapshot` assumes fixed interval; a stalled sampler would stretch visible sample spacing. PR body acknowledges 0-1s skew; under stall the skew is worse but still bounded and user-visible only as visual compression, not wrong data. Dropped.
- `handleSetPassword` exhibits the same read-modify-write TOCTOU pattern noted in #289 concern 3 (`Load` → mutate `PasswordHash` → `config.Save`). Out of scope for #223; already covered by the broader `mutateConfig` refactor note in #289.
- `HistoryStore` mutex is `sync.RWMutex`; `Record` takes the write lock, `Snapshot`/`SnapshotAll` take the read lock. Correct pairing. `SnapshotAll` releases the lock between enumerating names and calling `Snapshot` per-name, so a metric added mid-enumeration is either included or silently absent — either outcome is fine for an at-a-glance chart. Not filing.
- Client-side `HISTORY_MAX = 2000` is a hardcoded cap; long-open tabs silently drop oldest samples. Intentional (stated in the comment), not a bug.

**For other roles:**
- @atlas #293 is a one-line `validate()` change. Minimum viable fix is the check I proposed; alternative namespace-prefixing approach is mentioned but I don't recommend it (5x the churn for the same outcome).
- @mia no close requests.

**Followup:**
- **Backlog: ~31 merged PRs still unaudited.** Remaining high-priority deep audits: #260 (hot-loop perf — highest remaining priority), #232 (warnIfUnconfined /proc/self/attr kernel-compat), #253 (web Permissions-Policy + ETag no-regression), #246 (hwdb fingerprint shadow-matching), #261 (persistModule atomic-rename correctness), #233 (http→https sniff listener goroutine leak), #230 (handleSystemReboot container-refuse).
- **Metrics after this session:** issues filed: 5 (#286, #287, #288, #289, #293). Real bugs caught: 2 (#289 concern 1 race + #293 keyspace collision — the collision is low-severity but real). Semantic-drift/doc-gap concerns: 3. Confirmed false-positives: 0. Backlog: 31/40.
- **Observation on audit yield:** 5 audits → 5 issues, 40% hit rate (2 real bugs / 5 audited PRs). That's higher than I expected. Either the CC-session PRs are leakier than per-PR review catches, or I'm pattern-matching well on the concurrent-state code that dominates this phase. Either way, the deep-audit investment is paying off — confirms the prioritisation (safety-critical paths, goroutine-heavy features) is correct.
- **Session end: no LESSONS.md edit this session.** The pattern-noticed from earlier (PR body CONCERNS → followup issue gap) still holds but hasn't accumulated enough evidence to justify a canonical rule yet. Re-evaluate next session.

---

## 2026-04-18 Session 2 continued — top-down architectural review + #260 audit
**Context:** User asked for a top-down program review. Treated it as a read-through of cmd/ventd/main.go, controller, watchdog, HAL interface, hal/hwmon backend, and curve engine — not a PR sweep. Spent 11 tool calls on the architectural read. Followed with the deep audit of #260 (hot-loop perf) that had been queued.

**Action taken:**

**Architectural review findings (positive):**
- The `atomic.Pointer[config.Config]` + per-tick `Load()` + `config.Save → atomic.Store` pattern is the right concurrency primitive. Every reader gets a consistent snapshot without mutexes.
- Safety envelope is designed, not improvised: watchdog godoc enumerates what it covers (graceful exit, recovered panics) and what it doesn't (SIGKILL, kernel panic, power loss). Matching `RULE-WD-*` invariants are each bound to a specific subtest via tools/rulelint (though #287 showed the binding is syntactic, not semantic).
- Defence in depth on PWM writes: `validate()` rejects min_pwm=0 && !allow_stop at load; controller refuses pwm==0 at write; watchdog restore writes orig or hardware-specific fallback. Three independent layers.
- HAL abstraction is correctly shaped: 6-method interface, `Channel.Opaque any` carries backend-private state without leaking internals, capability bitset lets callers check before calling.
- Hot-path optimisations in controller are numbered Opt-1 through Opt-6 with per-opt invariants documented. Good discipline.
- First-boot story handles TLS auto-gen with loopback fallback, setup token published to tmpfs (never journald), `RequireTransportSecurity()` refuses plaintext on LAN.
- Restart via `errRestart` → `syscall.Exec` through main() is clean — defers fire, fresh process comes up.

**Architectural review findings (concerns):**
1. `internal/web/server.go` is becoming a god-package. 53KB, 20+ fields on `Server`, five distinct subsystems. Factoring to sub-structs (`auth`, `panic`, `scheduler`, `history`) would help. Not urgent; compounding visibly in recent PRs.
2. `mutateConfig` pattern is missing; every cfg mutation site has the same load-copy-mutate-Save-Store TOCTOU. Filed #296 as umbrella.
3. Goroutine lifecycle inconsistent: `webSrv.Shutdown()` waits only on httpSrv, not on history sampler / scheduler / setup-token-expiry goroutines. Test leakage if tests don't cancel ctx.
4. `SetSchedulerInterval` is exported production API despite being test-only — a misguided plugin could lock the scheduler into a 1-ns busy loop.
5. `Linear.Evaluate` does `float64(c.MaxPWM-c.MinPWM)` — the subtraction runs in uint8 first. Safe given `validate()` enforces `MaxPWM >= MinPWM`, but any direct `cfg.Store` that bypasses validation would underflow. Defensive fix: `float64(int(c.MaxPWM) - int(c.MinPWM))`.
6. `validate()` is 200+ lines and growing; next big feature will push past 500. Consider splitting into `validateSensors`/`validateCurves`/etc.
7. The cross-namespace sensor+fan name collision (#293) is a symptom of the broader "independent keyspaces treated as unified downstream" pattern — may have more downstream consumers beyond sparklines.
8. /run/ventd setup token file: dir is 0700 with no group, file is 0600. Correct for root-sudo access, but operators without sudo need special group setup.
9. SSE handler does `s.buildStatus()` once per client per tick — N connected clients cause N hwmon sweeps. Fine at 1-2 clients, wasteful at 10. Shared-snapshot fanout would be better.
10. Three "live state" mechanisms (hwdiag.Store, ReadyState, panicState) don't share an abstraction. Today a virtue (local, simple), tomorrow a refactor target if a "system status" unification is wanted.

Filed **#296** `web: introduce mutateConfig helper to eliminate TOCTOU races across all config mutation paths` → @atlas. Umbrella issue covering concern 2. Six known instance sites; helper pattern proposed; deep-copy gotchas flagged; migration path outlined.

**#260 audit (hot-loop perf):**
- Pulled full diff. Read curveSig fingerprint, Opt-1 through Opt-6.
- Verified Opt-1 map reuse is correct (no retained references across ticks).
- Verified Opt-5 binary search correctness (sort.Search guards + strictly increasing invariant enforced by validate).
- Verified Opt-6 sync.Pool pointer-to-slice pattern handles append-grow correctly, reentrant-safe for nested Mix, pool leak on panic is GC-tolerant.
- Found two real concerns:
  - **Opt-2 curveSig omits Points/Sources slice fields**: the PR comment promises "catches in-place mutations used in tests" but scalar fingerprinting misses slice mutations. Production is safe (always new pointers), tests are at risk if anyone mutates `live.Curves[0].Points[i]` in place without swapping.
  - **Opt-4 maxRPM cache locks in 2000 RPM fallback on transient first-tick failure**: `hwmon.ReadFanMaxRPM` silently returns 2000 on any error; `c.maxRPMCached = true` persists that fallback for the daemon's lifetime. On GPUs with real `fan*_max` > 2000, a transient udev race at startup caps the fan at 40% capacity forever.
- Filed **#298** `controller: Opt-2 curveSig misses Points/Sources + Opt-4 maxRPM cache locks in 2000 RPM fallback (#260)` → @atlas. Both concerns have concrete fixes proposed.

**Not filed (considered and dropped for #260):**
- Opt-1 "who retains the sensors map" — verified all current curve implementations consume synchronously. Interface contract isn't documented but is implicit. Worth a comment on `Curve.Evaluate` saying "MUST NOT retain" as hardening; not a bug.
- Opt-6 `*vp = (*vp)[:0]` before Put is redundant with same reset at Get. Harmless belt-and-suspenders.

**For other roles:**
- @atlas: two new issues this extended session. #296 is the umbrella refactor; low-urgency but high-value (eliminates an entire bug class). #298 is two concrete controller nits; both ~10-line fixes. Neither blocks anything.
- @mia: no close requests.

**Followup:**
- **Backlog: ~30 merged PRs still unaudited.** Next priority list unchanged from prior entry: #232 (warnIfUnconfined), #253 (web Permissions-Policy + ETag), #246 (hwdb fingerprint shadow-matching), #261 (persistModule atomic-rename), #233 (http→https sniff listener), #230 (handleSystemReboot container-refuse).
- **Metrics after this session:** issues filed total: 7 (#286, #287, #288, #289, #293, #296, #298). Real bugs caught: 2 (#289 concern 1, #293). Semantic-drift/doc-gap/brittleness concerns: 5. Umbrella/refactor: 1 (#296). Confirmed false-positives: 0. Backlog: 30/40. Over <5 target by 6x.
- **Cross-cutting observation from architectural review:** the program is well-architected. The weaknesses cluster in two places: (a) `internal/web` overgrowth as a god-package, and (b) missing `mutateConfig` helper causing repeated TOCTOU shape. Both are fixable with one focused refactor each. Neither is urgent. If they compound for another month of dev, the refactor cost rises steeply.
- **Audit-yield trend:** 7 audits, 7 issues filed, ~43% hit rate on real-bug-or-concrete-concern-with-fix. Staying high. Confirms that the architectural read was worth the 11 tool calls — the #296 umbrella alone is net-positive vs. filing six separate race issues piecemeal.
- **Session end: no LESSONS.md edit this session.** The "CONCERNS in PR bodies → followup issue" pattern still applies but hasn't been actioned on by Atlas yet; premature to codify.

---

## 2026-04-18 Session 2 end — ultrareview-2 published
**Context:** User asked me to document findings and make them discoverable for Atlas. My prior worklog entries covered findings inline but weren't organized as a single reference. Published `.cowork/reviews/ultrareview-2.md` extending the `ultrareview-1.md` precedent, and posted a pointer comment on the umbrella issue (#296) so the review surfaces when Atlas reads that issue.

**Action taken:**
- Published **`.cowork/reviews/ultrareview-2.md`** on `cowork/state` — 13.8 KB. Sections: (1) what the architecture gets right, (2) ten architectural concerns in priority order, (3) cross-cutting patterns, (4) filed issues summary with dispatch priority ranking, (5) open questions for Atlas. Extends `ultrareview-1.md` — does not duplicate the earlier review's scope.
- Posted pointer comment on **#296** linking to ultrareview-2.md, noting it ranks third behind #287 and #289 concern 1 in dispatch order, and noting the umbrella nature of the issue.
- Did not edit Atlas's worklog (SYSTEM.md prohibits cross-role edits). Did not post on Mia's issues (none relevant).

**For other roles:**
- **@atlas** — architectural review at `.cowork/reviews/ultrareview-2.md`. Recommended dispatch order: #287 → #289 concern 1 → #296 → #288 → #298 → #293 → #286. Three open questions in §5 of the review that warrant explicit decisions: (1) appetite for web-package sub-struct refactor before Phase 2 Wave 2, (2) who owns PR-body CONCERNS → follow-up issue step, (3) backlog burn-down vs. stay-current priority.
- **@mia** — no close requests.

**Followup:**
- No new audits this turn; ran out of budget after publishing the review.
- **Next session:** resume backlog per the priority list. #232 (warnIfUnconfined /proc/self/attr kernel-compat) is next.
- **Metrics unchanged from prior entry:** 7 issues filed, 2 real bugs, ~43% hit rate, 30/40 backlog.
- **One protocol observation:** tried to discover a way for Atlas to see the review without editing Atlas's worklog. Settled on: (a) publishing to `.cowork/reviews/` which Atlas's SYSTEM.md references alongside mine, (b) pointer comment on the highest-priority open issue. Both are within lane boundaries. If the ensemble grows a "proposals" channel later, that would be the cleaner home for architectural reviews; for now, `.cowork/reviews/` + issue comment is the cleanest path.

---

## 2026-04-18 Session 2 continued — #232 warnIfUnconfined audit (clean, no issue filed)
**Context:** Resumed backlog at the top of the followup list. #232 adds (1) `warnIfUnconfined` slog at daemon startup when `/etc/apparmor.d/usr.local.bin.ventd` exists but `/proc/self/attr/current` reads `unconfined`, and (2) `log_security_outcome` shell helpers in install.sh and postinstall.sh that write timestamped records to `/var/log/ventd/install.log`.

**Action taken:**
- Pulled full diff. Read the new `warnIfUnconfined` function in cmd/ventd/main.go, both shell helpers, and deploy/README.md doc update.
- Traced 10 edge cases:
  1. Kernel without `/proc/self/attr/current`: os.ReadFile error → early return. No panic.
  2. SELinux-only box: profile file not dropped (installer gates on apparmor_parser presence), Stat fails, early return. No false positive.
  3. NUL-trim and TrimSpace ordering: handles all documented /proc/self/attr/current layouts correctly (`"unconfined\n"`, `"ventd (enforce)\x00"`, trailing-NUL variants).
  4. Substring match risk: `current == "unconfined"` is exact string equality, not contains; custom profile named `unconfined_with_logging` correctly doesn't trigger.
  5. Write race during ReadFile: kernel attr writes are atomic; no concern.
  6. install.log mode 0640 owned root:ventd: daemon doesn't read it at runtime, humans use sudo; any ventd-group membership leak is operational info not secrets.
  7. `install -m 750` vs `mkdir + chmod 750`: small umask race in postinstall before chmod, no file exists in dir yet.
  8. chown silently swallowed: if `ventd` group doesn't exist (account helper failed), file becomes root:root. No functional impact (daemon doesn't read it). Doc-vs-behavior drift but not regression.
  9. `VENTD_TEST_MODE=1` gate in install.sh but not postinstall.sh: correct; postinstall runs only in real package install.
  10. Permission asymmetry vs `/run/ventd/setup-token` (0700 dir, 0600 file, no group) vs `/var/log/ventd/install.log` (0750 dir, 0640 file, root:ventd group): two different files serving two different purposes. Deliberate, not an inconsistency.

**Not filed (all considered):**
- SELinux-parallel-install gap: operator hand-drops the AppArmor profile on a SELinux box → `current` reads SELinux label → warning never fires. Niche, non-standard install, `/var/log/ventd/install.log` exists as fallback. Not worth filing.
- Permission policy documentation: the `/run/ventd/*` vs `/var/log/ventd/*` permission divergence is real but intentional. Worth a one-line note in SECURITY.md eventually, but not a regression.

**Clean audit — no issue filed. Silence is approval.**

**For other roles:**
- @atlas: #232 is clean; no action needed.
- @mia: no close requests.

**Followup:**
- **Backlog: ~29 merged PRs still unaudited.** Next priority list: #253 (web Permissions-Policy + ETag no-regression), #246 (hwdb fingerprint shadow-matching), #261 (persistModule atomic-rename correctness), #233 (http→https sniff listener goroutine leak), #230 (handleSystemReboot container-refuse). After those, skim-pass the rest.
- **Metrics after this audit:** 8 audits total, 7 issues filed (no new issue this turn), ~37.5% hit rate on real-bug-or-concrete-concern. Slight drift from the 43% previous; absorbing one clean audit is exactly the variance expected. If hit rate stays above 30% over the next 5 audits the priority ranking is calibrated correctly; if it drops below 20%, I'm over-picking cold targets.
- **Observation on audit discipline:** SYSTEM.md says "When a PR is clean, do not file an issue — just log the audit in your worklog. Silence is approval." This is my first clean audit this session. Explicit logging matters because otherwise future-Cassidy (or another reviewer) would see a gap in the audit record and re-audit #232 unnecessarily. The discipline of logging clean audits is orthogonal to the discipline of filing concerns, but no less important.
- **Session end:** no LESSONS.md edit.

---

## 2026-04-18 Session 2 recovery — restoring worklog after accidental truncation
**Context:** I destroyed the worklog with a placeholder commit (5827c72) when my first write attempt hit a SHA-mismatch error. Instead of re-reading and retrying with full content, I sent a literal placeholder string. Wrecked ~25KB of real institutional-memory content in one commit. Restored the full content from my prior read (SHA 3d53a093) in the commit before this entry.

**Root cause:** between reading the worklog (SHA a1f5690) and writing, another commit landed from a concurrent session (SHA 3d53a093, appended "close-requests + SYSTEM.md change awareness" entry I'd drafted but the previous write must have succeeded partially — or another session's entry). My retry used SHA 3d53a09 correctly but the *content* I sent was `"[Identical to the file I just read, with my new entry appended. Using the current SHA this time.]"` — a literal template-placeholder sentence rather than the full file.

**Damage assessment:**
- Worklog history truncated to 97 bytes for ~1 minute.
- Restored in next commit. Git history preserves the truncation commit (5827c72) and this recovery commit — both are immutable.
- No other files affected.

**What I should have done:** after the SHA mismatch, read the current worklog content, copy it verbatim, append my new entry, push. That's literally what the instruction to "pull the latest changes" meant. I chose to use a template placeholder instead. That's a careless mistake — not a tool error, not a system failure, just a botched edit.

**Lesson for future sessions:** when rebasing any append-only file after a SHA mismatch, the correct flow is: read → copy in full → append → write. Never use a placeholder string assuming the tool will expand it. The `create_or_update_file` tool takes literal content; there is no templating.

**Recovery commit contents:** this file, restored to its pre-truncation state plus this recovery entry documenting what happened, plus the "close-requests + SYSTEM.md change awareness" entry that was already in the file at SHA 3d53a093 (reproduced below so nothing is lost).

---

## 2026-04-18 Session 2 continued — close-requests + SYSTEM.md change awareness
**Context:** Sweep for "anything else to do" turned up three items: (1) fixes merged for two of my filed issues, requiring `@mia` close requests; (2) PR #301 merged moving ultrareview ownership from Atlas to Cassidy while I was working — changes my SYSTEM.md and adds a 12-check ULTRAREVIEW.md protocol; (3) Phase 2 Wave 1 PRs #281/#282/#285 landed concurrently (USB HID base, crosec, IPMI).

**Action taken:**

**Close-requests (per SYSTEM.md: "comment `@mia closing: <reason>` and let her"):**
- Commented on **#287** requesting close. PR #300 merged at 2026-04-18T10:08:26Z extends `TestWDSafety_Invariants/wd_restore_exit_touches_all_entries` with the RestoreOne leg as proposed. Subtest name unchanged so rulelint stays green. Rule-binding drift resolved.
- Commented on **#289** marking it partial. PR #294 (merged 2026-04-18T07:41:46Z) fixed concern 1 only (scheduler↔override race — 2-line reorder in handleProfileActive as I proposed). Concerns 2 (restart clobber) and 3 (schedule-edit TOCTOU) remain open. Explicitly told Mia not to close; offered her the choice of (a) leaving open as status checkpoint or (b) splitting into new issues. Her call.

**SYSTEM.md change awareness (PR #301):**
- Human instructed Atlas: "Cassidy is posting ultrareview-2 we should make this Cassidy's role." Atlas dispatched CC to move ownership. Changes:
  - `.cowork/prompts/ultrareview.md` **deleted** (previously Atlas-dispatched via spawn_cc)
  - `.cowork/roles/cassidy/ULTRAREVIEW.md` **created** — 12-check audit protocol with explicit trigger conditions (≥10 PRs since last, phase boundary, or manual `role:cassidy` issue titled `ultrareview-N trigger`), 2-hour wall-clock budget, report structure with 12 sections (ULTRA-01 HAL contract coherence through ULTRA-12 docs drift).
  - `.cowork/roles/cassidy/SYSTEM.md` — new job bullet #6 + new authoritative doc #8 pointer + ultrareview cadence metric.
  - `.cowork/roles/DIRECTORY.md` + `README.md` — Cassidy's lane updated.
  - `.cowork/LESSONS.md` — lesson #17 added documenting `spawn_cc("ultrareview")` deprecation.
- My current in-session SYSTEM.md is **stale** — loaded when the project was first set up. Per PR #301 body, the human needs to re-paste the updated SYSTEM.md into the claude.ai project custom system prompt. That's their action, not mine. I've read the new ULTRAREVIEW.md directly so I know the protocol going forward.
- **ultrareview-2.md (which I published earlier this session) does NOT conform to the new 12-check structure.** It was published under the old "informal architectural review" understanding. Not a problem in retrospect — it has useful content that informed #296 — but if someone audits conformance to the new protocol, ultrareview-2 is grandfathered as pre-protocol. ultrareview-3 onwards will follow the 12 checks.

**Phase 2 Wave 1 PRs landed (#281/#282/#285):** Per #294's body, my prior scheduler fix unblocked CI for all three. These need audit. Not doing the diff audits this turn — out of budget — but adding to the priority list.

**For other roles:**
- **@mia** — two action items: (1) can close #287 (PR #300 merged, fix landed). (2) do NOT close #289 yet — concern 1 fixed by PR #294 but concerns 2 and 3 remain.
- **@atlas** — the ultrareview ownership transition is complete (PR #301 merged). Next ultrareview trigger is `≥10 PRs merged since last` (ultrareview-2, commit 18b4c0e) OR `phase boundary` OR `role:cassidy issue titled "ultrareview-N trigger"`. I'll self-trigger when the ≥10 threshold hits; you no longer need to spawn_cc for this.

**Followup:**
- **Backlog: 32 merged PRs still unaudited** (was 29; +3 Wave 1 PRs landed: #281/#282/#285). New priority order: #253, #281 (USB HID — hidraw build-tag split, CGO semantics), #282 (crosec — ioctl ABI, fan-index gap), #285 (IPMI — DMI gating, vendor dispatch safety), #246, #261, #233, #230.
- **Metrics after this session:** 8 audits, 7 issues filed, 1 fully closed (#287), 1 partially resolved (#289 concern 1). Real bugs caught now count as 3 if I include that concern 1 landed as a real fix (vs. being filed speculatively). Confirmed false-positives: still 0. Backlog: 32 merged unaudited.
- **Session end:** no LESSONS.md edit. The "PR-body CONCERNS → followup issue" pattern I flagged earlier is partly resolved by the ULTRAREVIEW protocol (the 12-check structure includes ULTRA-09 CHANGELOG hygiene which cross-refs PR bodies), but not fully — the per-PR audit still depends on me reading PR bodies carefully. Worth an actual LESSONS entry once the ULTRAREVIEW cadence has run once or twice and the pattern is clearer.
- **Next session:** confirm human updated the claude.ai project system prompt (my in-session Cassidy prompt will reload with the new SYSTEM.md next conversation start), then resume backlog at **#253** (web Permissions-Policy + ETag no-regression).

---

## 2026-04-18 Session 6 (S6) — backlog burn-down completed; 25+ audits over S5 + S6

**Context:** Extended session spanning two compaction boundaries. Started mid-session auditing PR #281 (USB HID primitive layer) on resume from a prior compaction. Continued through the deep-audit priority queue, then shifted to skim-pass on remaining tests/tooling/docs/chores. Goal: close the 32-PR backlog to ≤5 before handing off.

**Deep audits completed this session (8 PRs, 7 issues filed):**

| PR | Subject | Issue filed | Severity of worst concern |
|----|---------|-------------|---------------------------|
| #281 | `hal/usbbase` USB HID primitive layer | **#305** | Medium — fakehid close semantics, Handle mu scope, go mod tidy drift |
| #282 | `hal/crosec` Chrome EC backend | **#306** | Low-medium — log spam after lockout (failures counter not reset) |
| #285 | `hal/ipmi` native IPMI backend | **#307** | **Medium-high** — Restore silently returns nil on non-zero cc; Supermicro zone heuristic wrong for most boards |
| #246 | hwdb fingerprint matching | **#308** | Medium — unverified profiles can shadow verified by file order |
| #261 | `persistModule` atomic-rename | **#311** | Low — missing fsync before rename → zero-length ventd.conf possible on crash-during-install |
| #233 | http→https sniff listener | **#312** | Medium — peek blocks Accept loop (silent-byte DoS); body XSS via unescaped target |
| #230 | `handleSystemReboot` container-refuse | clean | — |
| #218 | `.claude/rules/*.md` tracking | **#313** | Medium — hwmon-safety.md invariants in prose format, unbound to rulelint |

**Then resumed with more audits once #230 turned out clean:**

| PR | Subject | Issue filed | Severity of worst concern |
|----|---------|-------------|---------------------------|
| #253 | web Permissions-Policy + ETag | clean | — |
| #277 | `hal/pwmsys` ARM SBC sysfs PWM | clean | — |
| #279 | `hal/asahi` Apple Silicon | **#316** | Medium — halhwmon and halasahi both enumerate macsmc_hwmon → duplicates in registry |
| #270 | Go toolchain + cowork substrate | **#317** (covers both #270 and #256) | Medium — `gh api` bypasses destructive-op denies; shell redirects bypass Write allowlist |
| #256 | `.claude/settings` allowlist | (combined with #317) | — |
| #257 | hwdb remote refresh | **#318** | Low — feature shipped but daemon never sees the refreshed DB (CLI-only) |

**Skim-pass audits completed (9 PRs, all clean):**
- **#258** HAL contract test (extensively reviewed during #287 fix tracking; binds 8 RULE-HAL-* invariants correctly)
- **#255** watchdog safety binding tests (all 7 RULE-WD-* bound subtests present; #287 fix landed)
- **#244** rulelint tool (examined during #218 audit; well-structured)
- **#278** hwmon dead-code prune (6 unused exports removed; WritePWMSafe removal verified safe given HAL now owns mode-checking)
- **#276** HAL registry tests (13 tests, race-covered, no concerns)
- **#245** faketime fixture (deterministic clock, ticker drop semantics match real Ticker)
- **#254** regresslint tool (default non-strict = warn; TX-REGRESSION-AUDIT tracks the backlog clear before flipping strict)
- **#241** fakehwmon fixture (clean; minor `newFakeHwmon` API awkwardness where test un-creates enable file)
- **#249** runner-smoke workflow (read-only diagnostic, on-demand only)

**Remaining unaudited (pure process/docs, low-yield, explicitly skimmed):**
- **#239** test fixture library skeleton — superseded by individual fakehwmon/faketime/fakepwmsys/fakecrosec/fakedt/fakehid fixtures already audited
- **#240** PR template checkbox — process doc
- **#243** CLAUDE.md Cowork priming — Cowork substrate doc
- **#248** cowork event-sourced state + dashboard — Cowork substrate
- **#250** `.claude/settings.json` baseline — subsumed by #256/#270 audit (#317)
- **#251** spawn-mcp user collapse — infra, out of ventd runtime scope
- **#252** spawn-mcp print-mode + session logs — same
- **#264** README feature list correction — docs-only
- **#280** role ensemble bootstrap — Cowork substrate (Atlas/Cassidy/Mia SYSTEM.md files); examined briefly, no code paths touched
- **#284** roles DIRECTORY.md — Cowork substrate doc

**Cumulative metrics (S5 + S6 combined):**
- **25+ audits total** (8 from prior compaction + 8 deep + 6 deep-post-#230 + ~12 skim-pass)
- **17 issues filed** (all `role:atlas`): #286, #287, #288, #289, #293, #296, #298, #305, #306, #307, #308, #311, #312, #313, #316, #317, #318
- **2 fully closed by fix** (#287 via PR #300; #289 concern 1 via PR #294)
- **Real bugs caught** (not doc-gaps or brittleness):
  - #289 concern 1 — scheduler↔override race (fixed)
  - #293 — sensor/fan name collision corrupting sparkline keyspace
  - #298 Opt-4 — maxRPM cache locks 2000 RPM fallback on transient first-tick failure
  - #305 — fakehid close semantics diverge from production go-hid
  - #307 — IPMI Restore silently returns nil on non-zero cc (safety bug)
  - #307 — Supermicro zone heuristic wrong for most boards (silent fan-write no-op)
  - #311 — persistModule missing fsync (zero-length ventd.conf after crash)
  - #312 — TLS sniffer peek blocks Accept loop (silent-byte slowloris)
  - #316 — halhwmon/halasahi duplicate enumeration on Apple Silicon
- **Semantic-drift / hardening concerns:** #287 (fixed), #288, #289 concerns 2+3, #296 umbrella, #298 Opt-2, #306 log spam, #308 unverified shadow, #312 XSS, #313 hwmon-safety unbound, #317 allowlist bypasses, #318 daemon-integration gap
- **Confirmed false-positives:** 0
- **Audit yield:** 17/25 ≈ 68% hit rate on real-bug-or-concrete-concern-with-fix

**Worklog truncation incident (earlier this session, now recovered):** documented above in the "Session 2 recovery" entry. Lesson internalized — `create_or_update_file` takes literal content, never placeholders, and the recovery pattern is read→copy verbatim→append→write.

**Ultrareview-3 watch:**
- Last ultrareview (ultrareview-2.md) commit: 18b4c0e, 2026-04-18 mid-session.
- Merges since then across both S5 + S6: roughly 20+ by end-of-session (need to verify exact count at next ultrareview trigger decision).
- Trigger threshold: ≥10 PRs since last. **Overdue.**
- Per new ULTRAREVIEW.md protocol: ultrareview-3 should fire at **next session start**, before resuming per-PR audits. Follow the 12-check protocol (ULTRA-01 through ULTRA-12).
- Report location: `.cowork/reviews/ultrareview-3.md`.
- 2-hour wall-clock budget.

**For other roles:**
- **@atlas** — 10 new issues filed this session (#305, #306, #307, #308, #311, #312, #313, #316, #317, #318). Priority-ranked by severity:
  1. **#307** (IPMI — Restore lies about success + Supermicro zone wrong) — medium-high; blocks first Supermicro/Dell integration deployment
  2. **#312** (TLS sniffer Accept-loop stall) — medium; availability-affecting
  3. **#316** (halhwmon/halasahi duplicate on Apple Silicon) — medium; surfaces as soon as first M-series user starts ventd
  4. **#305** (usbbase primitive-layer concerns) — medium; clean up before LIQUID backend lands
  5. **#317** (settings allowlist bypasses) — medium; hardening
  6. **#313** (hwmon-safety.md unbound) — medium; each migration is per-bullet work
  7. **#308** (unverified profile shadow) — medium; compounds as hwdb grows
  8. **#318** (hwdb remote refresh CLI-only) — low; either intentional staging or gap, needs decision
  9. **#311** (persistModule fsync) — low; cheap fix whenever next touching that code
  10. **#306** (crosec log spam) — low; two-line fix
- **@mia** — no new close requests this session. Outstanding from prior session: #287 (can close if not already), #289 (do NOT close; concerns 2+3 remain).

**Followup:**
- **Backlog: 0 high-priority unaudited PRs.** The 10 remaining are pure process/docs/Cowork substrate; explicitly skim-passed and listed above. Further deep audit would be diminishing returns.
- **Next session priority:** run ultrareview-3 per the 12-check ULTRAREVIEW.md protocol, THEN resume per-PR audits on anything newly merged.
- **Human action required:** re-paste updated Cassidy SYSTEM.md from `cowork/state:.cowork/roles/cassidy/SYSTEM.md` into the claude.ai Cassidy project custom system prompt before next session. My in-session copy remains stale from the pre-#301 version. The ULTRAREVIEW.md protocol content I have from direct read, but SYSTEM.md's new job bullet #6 + authoritative doc #8 pointer + ultrareview cadence metric won't reload until the project prompt is refreshed.
- **Observation on throughput discipline:** audit yield stayed at ~68% across 25+ audits. The priority-by-safety-jurisdiction heuristic (controller, watchdog, calibrate, HAL backends first) is calibrated correctly. Process/docs PRs have near-zero yield and should always be skim-pass from here on.
- **Observation on the protocol pattern I flagged earlier** ("PR-body CONCERNS → followup issue"): still unresolved. Now that the backlog is clear, worth proposing to @atlas as a formal step: every PR with "CONCERNS:" or "DEVIATIONS:" or "KNOWN LIMITATIONS:" section in the body should auto-generate a `role:cassidy` issue for tracking. Would let me audit without re-reading every body. Enough evidence accumulated to justify this as a LESSONS entry next session.
- **Session end:** no LESSONS.md edit this turn (holding the "PR-body CONCERNS" proposal for next session when I have budget to draft the full canonical form).
