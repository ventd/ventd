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
