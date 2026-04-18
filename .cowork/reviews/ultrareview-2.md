# ultrareview-2.md — ventd architectural review

**Reviewer:** Cassidy
**Date:** 2026-04-18
**Scope:** Top-down read of `cmd/ventd/main.go`, `internal/controller`, `internal/watchdog`, `internal/hal` (interface + hwmon backend), `internal/curve`, plus cross-cutting concerns surfaced during PR-by-PR audits of #223, #225, #260, #247, #263, #262.
**Extends:** `ultrareview-1.md` (previous full-program review).
**Status:** Findings filed as issues where actionable; this doc is the reasoning trail Atlas would want before dispatching the follow-ups.

---

## 1. What the architecture gets right

The spine of ventd holds. Six things are load-bearing and well-built:

1. **`atomic.Pointer[config.Config]` as the single concurrency primitive for cfg state.** Every reader does `cfg.Load()` once per tick and uses that snapshot for the full operation. Writers go through `config.Save → atomic.Store`. No mutexes on the read path. Correct pattern, correctly applied.

2. **Designed safety envelope, not improvised.** `watchdog.go`'s godoc enumerates what it covers (graceful exit, recovered panics) and what it doesn't (SIGKILL, kernel panic, power loss). The matching `RULE-WD-*` invariants in `.claude/rules/watchdog-safety.md` are each bound by name to a specific subtest, and `tools/rulelint` enforces the binding. When #263 updated rule prose to mention `RestoreOne`/`restoreOne` without extending the bound subtest (see #287), rulelint caught the syntactic binding but not the semantic gap — the binding mechanism has a weakness, but the mechanism exists, which is already more discipline than most safety-adjacent projects.

3. **Defence in depth on PWM writes.** Three layers, each self-sufficient:
   - `config.validate()` rejects `min_pwm=0 && !allow_stop` at load.
   - `controller.tick()` refuses `pwm==0 && !fan.AllowStop` at write time.
   - `watchdog.Restore` writes captured `origEnable` or hardware-specific fallback (PWM=255 for pwm*, `fan*_max` for rpm_target, auto for NVML).

   A bypass at any one layer doesn't disable the others.

4. **HAL abstraction is well-drawn.** Six methods (`Enumerate`, `Read`, `Write`, `Restore`, `Close`, `Name`). `Channel.Opaque any` carries backend-private state through the controller and watchdog without either reaching into backend internals. Capability bitset (`CapRead`, `CapWritePWM`, `CapWriteRPMTarget`, `CapRestore`) lets callers check before calling — no "try-op-catch" anti-pattern.

5. **Hot-path performance taken seriously.** Controller has Opt-1 through Opt-6 numbered in comments, each with invariants documented (pre-allocated maps + `clear()`, `curveSig` fingerprint for cache invalidation, single `cfg.Load()` per tick, cached `fan*_max`, binary search in Points, `sync.Pool` for Mix intermediate slice). This is post-hoc optimisation against benchmarks, not speculation. The `curveSig` design explicitly guards against the test-only same-pointer mutation bug.

6. **First-boot story is done right.** Self-signed TLS auto-gen with loopback fallback if gen fails. Setup token via `/run/ventd/setup-token` (tmpfs, 0600) plus best-effort TTY plus structured log. `RequireTransportSecurity()` refuses plaintext on LAN at startup. No plaintext ever touches journald. Restart semantics via `errRestart` → `syscall.Exec` let defers fire first so PWM is restored and NVML released before the fresh process comes up.

---

## 2. Architectural concerns (not regressions, structural drift)

Listed in rough priority.

### 2.1 `internal/web/server.go` is becoming a god-package

53 KB, one `Server` struct carrying 20+ fields across five distinct subsystems: auth, sessions, panic, scheduler, history. `New()` launches three goroutines directly (`runHistorySampler`, `runScheduler`, `expireSetupToken`); tests must avoid racing all three.

**Impact:** every new feature adds a field. In the last week: `schedState`, `schedIntervalNS`, `schedWake` (from #225); `history` (from #223); `panic` (earlier); `rebootBlocker` (test seam). The pattern doesn't self-arrest.

**Recommendation:** factor to sub-structs. `Server` gets `auth *authState`, `panic *panicState`, `scheduler *schedulerState`, `history *historyState`. Each sub-struct owns its own mutex and goroutines. The current `panicState` / `scheduleState` types already suggest this direction but live as fields, not separate subsystems. Not urgent; compound cost rises steeply if left.

**Not filed as issue** — this is a refactor, not a regression. If Atlas wants to action it, the right trigger is "next time the web package grows by ≥2 fields" or a dedicated Phase 2 task.

### 2.2 `mutateConfig` helper missing — filed as #296

Every cfg mutation path in `internal/web/` has the same load-copy-mutate-Save-Store shape with no mutex. Six known sites (`handleProfileActive`, `handleProfileSchedule`, `handleSetPassword`, `handleConfigPut`, `handleFirstBootLogin`, `handleSetupApply`). Single-operator: never fires. Multi-tab / automation / malicious: real. Last writer silently discards the first's mutation.

Filed as **#296** with the helper shape proposed, deep-copy gotchas flagged, migration order suggested. This is the highest-leverage single fix in the codebase — one helper eliminates an entire bug class.

### 2.3 Goroutine lifecycle inconsistent

Outer daemon pattern (`main.go`) uses `sync.WaitGroup` + ctx + errCh. Rigorous.

`web.Server` pattern: bare `go` statements with nothing tracking exit. `Shutdown()` waits only on `httpSrv.Shutdown(ctx)` — the history sampler, scheduler, and setup-token expiry goroutines keep running until the daemon-level ctx cancels. In tests that construct a Server without cancelling ctx, they leak.

**Recommendation:** `web.Server` should own a `sync.WaitGroup` internally for its goroutines. `Shutdown()` should wait on it. Not a live bug — production shutdown cancels the daemon ctx — but test hygiene concern.

**Not filed** — one-line severity, folds naturally into the web-package restructure in 2.1.

### 2.4 `SetSchedulerInterval` is production API but test-only usage

`Server.SetSchedulerInterval(d time.Duration)` is exported. Nothing in production calls it (scheduler is meant to run at 60s cadence). Anyone with access to the Server can lock the scheduler into a 1-nanosecond busy loop.

**Recommendation:** move to a test-only package or behind a build tag. Cosmetic, not a security issue (the Server is in-process only), but invites misuse.

**Not filed** — trivial.

### 2.5 `Linear.Evaluate` has a latent underflow

```go
pwm := float64(c.MinPWM) + ratio*float64(c.MaxPWM-c.MinPWM)
```

`c.MaxPWM-c.MinPWM` runs in uint8 first. If `MaxPWM < MinPWM`, underflow. `validate()` rejects this, so production is safe. But any direct `cfg.Store` bypassing validation (test paths, future plugin mechanisms) produces garbage PWM.

**Fix:** `float64(int(c.MaxPWM) - int(c.MinPWM))`. Defensive, free at runtime.

**Not filed** — one-line fix, low probability, bundle with any controller change.

### 2.6 `validate()` growing unbounded

200+ lines, 12 distinct rule classes, no scaffolding for adding new ones. Sequence matters (sensors before curves). Next big feature (e.g. v0.4 profile bindings overhaul) will push past 500 lines.

**Recommendation:** split into `validateSensors`, `validateFans`, `validateCurves`, `validateControls`, `validateProfiles` as private methods called in order. Preemptive, not reactive.

**Not filed** — refactor, not a regression.

### 2.7 Cross-namespace name collision (#293) is a symptom

`sensors[name]` and `fans[name]` validated as independent keyspaces but unified downstream in `HistoryStore`, UI data attributes, and probably more. #293 fixes the sparkline consequence; the deeper "two namespaces treated as one downstream" pattern may have more consumers.

**Recommended follow-up:** once #293 lands, a repo-wide grep for `historyBuf[name]`-like patterns (single-string identifier expected to be globally unique across sensor+fan). Cheap; may find nothing, may find three.

### 2.8 SSE fan-out does N sweeps per N clients

`handleEvents` ticks per-client. Each connected client runs its own `s.buildStatus()` loop, meaning 10 SSE clients cause 10× hwmon sweeps every 2s. `hwmon.ReadValue` is cheap, but the architecture invites the bug.

**Recommendation:** single shared ticker inside the Server with fan-out. Or, since `HistoryStore` already caches, let SSE clients subscribe to history events rather than running independent read loops.

**Not filed** — deploy scale (1-2 clients per daemon) makes this irrelevant today. Revisit if a fleet deployment scenario arises.

### 2.9 Three "live state" mechanisms don't share an abstraction

`hwdiag.Store`, `web.ReadyState`, `web.panicState`. All answer "what is happening right now" but with different shapes. Today: virtue (local, simple). Tomorrow: refactor target if someone wants unified "system status."

**Not filed** — flag, not fix.

### 2.10 /run/ventd token file permissions

Dir 0700, file 0600, no group. Correct for root-sudo retrieval; operators without sudo need explicit group setup. The godoc says "any journal reader" can't retrieve it — true, but the filesystem permissions don't directly enforce the stated group boundary.

**Not filed** — doc nit, not a vulnerability.

---

## 3. Cross-cutting patterns worth calling out

### 3.1 CC-session PRs disclose deviations honestly but don't always file follow-up issues

#247 disclosed deviation 2 (fatal-on-permission semantics removed for `pwm_enable`) in its PR body. No follow-up issue opened until I audited and filed #288.
#225's PR body covered the override semantics well but didn't call out the cfg-swap-vs-override race; filed as #289 concern 1 only after audit.

**Implication:** the CC/review loop catches what's in the diff but not what's in the author's own "CONCERNS:" section in the PR body. Atlas's merge workflow needs either:
- An explicit "scan PR body for CONCERNS/deviations → file role:atlas issue" step, OR
- Cassidy audits every merged PR body AND diff (current state, load-bearing).

Option 1 is cheaper and makes the arrangement a designed property rather than an accident.

**Not filed** — process observation for Atlas to action at their discretion.

### 3.2 PR velocity is outpacing audit capacity

40 PRs merged in 24h since ensemble bootstrap. At ~1 deep-audit per turn, the gap widens indefinitely. Realistic steady-state: 2-3 deep audits per session + skim-pass the rest. Audit hit rate stays high (~43% issues-per-audit on the 7 audits so far) so skipping is real opportunity cost, not just reviewer skittishness.

**Recommendation (not filed):** revisit after 1-2 weeks. If the rate sustains, either velocity needs to drop OR Atlas's per-PR review depth needs to increase OR a second reviewer role is warranted. Premature to decide now.

### 3.3 Audit yield suggests prioritisation is correct

7 audits → 7 issues → 2 real bugs (#289 concern 1, #293) + 5 drift/brittleness/doc concerns + 1 umbrella refactor (#296). The 2 real bugs are both in concurrent-state code (scheduler race, validate-vs-downstream-consumer gap). Safety-critical paths and goroutine-heavy features are the right priority — every deep audit so far has found something worth filing.

---

## 4. Filed issues summary (from this review session + preceding audits)

| # | Title | Severity | Status |
|---|---|---|---|
| #286 | cassidy: SYSTEM.md references missing masterplan files | low (config) | open |
| #287 | watchdog: RULE-WD-RESTORE-EXIT prose names RestoreOne but bound subtest doesn't cover it | medium (rule-binding drift) | open |
| #288 | controller: #247 removed fatal-on-permission for pwm_enable write | medium (semantic regression) | open |
| #289 | web: scheduler↔override race + restart clobber + schedule-edit TOCTOU | medium (3 concerns) | open |
| #293 | config: validate doesn't reject sensor/fan name collisions | low (cosmetic consequence) | open |
| #296 | web: introduce mutateConfig helper to eliminate TOCTOU races | medium (umbrella refactor) | open |
| #298 | controller: Opt-2 curveSig misses Points/Sources + Opt-4 maxRPM cache locks in 2000 RPM fallback | low (brittleness) | open |

No issue blocks a release. Ranked by what I'd dispatch first if I were Atlas:

1. **#287** — rule binding drift is a safety-correctness issue; fix is mechanical.
2. **#289 concern 1** — 2-line reorder in `handleProfileActive`; eliminates a real race.
3. **#296** — umbrella refactor, highest ROI; once landed, #289 concern 3 closes for free and future cfg mutations are race-proof by default.
4. **#288** — restore fatal-on-permission semantics for `pwm_enable` so sysfs perm errors become operator-visible.
5. **#298** — two controller cache-invalidation nits; small PR.
6. **#293** — one-line validate() addition.
7. **#286** — Cassidy's own SYSTEM.md doc fix; low urgency.

---

## 5. Open questions for Atlas

1. **Is there an appetite for the `internal/web` sub-struct refactor before next feature?** Every feature shipped in the last week added fields to `Server`. If the answer is "yes, before Phase 2 Wave 2," I'd propose filing it as a tracking issue and dispatching the factor-out as one CC session.

2. **PR-body CONCERNS → follow-up issue**: should this become a merge-time step in Atlas's workflow, or remain Cassidy's responsibility? Current state is load-bearing on my audit coverage — if I miss a session, CONCERNS drop off.

3. **Backlog burn-down strategy**: 30 merged PRs remain unaudited. At 2-3/session, that's ~10 sessions. Is the priority "catch up" or "stay current on new merges"? Default behavior is the latter; happy to flip to burn-down for a session if instructed.

---

*End ultrareview-2. Pointer to this doc posted in Atlas's worklog as a one-line comment.*
