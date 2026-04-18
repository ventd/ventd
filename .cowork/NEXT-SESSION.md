# Wake-up brief — S6 start (session end 2026-04-18 S5)

Read this first. ~3 min to act, then S6 can run.

## Session S5 summary

**9 PRs merged**: #251 spawn-mcp-user-collapse, #252 spawn-mcp-print-mode, #253 P10-PERMPOL-01, #254 T0-META-02, #255 T-WD-01, #256 settings-allowlist-fix, #257 P1-FP-02, #258 T-HAL-01, #259 P1-MOD-01.

**Real masterplan/testplan progress**: 6 (everything except the 3 infra PRs).

**Throughput**: 1.8 PR/hr across full session; 5.3 PR/hr in pure parallel-dispatch mode (60 min window). Parallel-dispatch pattern works; the tax to reach it consumed most of the session.

**Ultrareview counter**: 6/10 real-task merges since last ultrareview. Next fire at 4 more real-task merges OR Phase 1 close. Phase 1 has P1-HOT-01 (in flight #260), P1-HOT-02, P1-MOD-02, P1-HAL-02 remaining. P1-HAL-02 blocks T-CAL-01. Likely ultrareview fires at Phase 1 boundary ~4-6 PRs from now.

## Immediate action on wake

**1. Merge #260.** It's open with CHANGELOG conflict. Prompt `.cowork/prompts/fix-260-rebase.md` is queued.

```
spawn_cc("fix-260-rebase")
```

Wait for completion (40 min buffering expected). Check `list_pull_requests state=open`. When #260 has 16/16 green CI, merge it.

**2. Investigate lingering session.** `list_sessions` at session end showed `cc-hal2-682f5c` — unclear provenance. Either kill it or wait for it to emit a PR. Check `tail_session` first to diagnose.

**3. P1-HAL-02 still not dispatched.** After clearing #260 and hal2-session, dispatch P1-HAL-02 (prompt at `.cowork/prompts/P1-HAL-02.md`, already model-mismatch-abort-free per lesson #12). CDN cache is cold now (7+ hours since edit).

## Queue (Phase 1 remaining)

After #260 merges, these are the next dispatches. All non-overlapping allowlists, safe to parallel-dispatch in one turn (MAX_PARALLEL=4):

- `P1-HAL-02` — calibrate via hal.FanBackend. Prompt ready. Depends on P1-HAL-01 ✓.
- `P1-HOT-02` — symmetric PWM write error handling. Allowlist: `internal/controller/controller.go`. **Conflicts with #260 #P1-HOT-01**; wait until #260 merges, then dispatch.
- `P1-MOD-02` — append-not-overwrite in persistModule. Prompt not yet written. Depends on P1-MOD-01 ✓ (just merged #259).
- `T-CAL-01` — calibrate safety invariants. Prompt not yet written. **Depends on P1-HAL-02 merge**.
- `T-HOT-01` — bench + alloc assertions. Prompt not yet written. **Depends on P1-HOT-01 merge** (#260).

Optimal sequencing: dispatch P1-HAL-02 and P1-MOD-02 (after writing its prompt) immediately after #260 merges. Once P1-HAL-02 merges, unblocks T-CAL-01. Once #260 merges, P1-HOT-02 + T-HOT-01 unblock.

## Hot lessons to apply at start of S6

- **#11** parallel-dispatch pattern works. Fire all 4 slots in one turn when allowlists don't overlap.
- **#12** never include model-mismatch-abort in prompts.
- **#13** after editing a prompt on cowork/state, wait 5 min before re-dispatch OR rename the alias to dodge raw.githubusercontent.com CDN.
- **#14** on update_pull_request_branch 422 conflict: dispatch fix-<PR>-rebase, never MCP-edit the CHANGELOG.
- **#15** empty tail_session is not "stuck"; poll `list_pull_requests` instead. Use session time for prep work (next prompts, documentation).

## Ultrareview watch

At 4 more real-task merges OR Phase 1 close: halt new dispatches, `spawn_cc("ultrareview")`, address blockers before resuming. Spec at `.cowork/ULTRAREVIEW.md`.

## Outstanding cleanup

- `cc-hal2-682f5c` session of unknown provenance (investigate first turn).
- `/var/log/spawn-mcp/sessions/cc-*.log` files accumulating on phoenix-desktop. No rotation. Not urgent.
- PR #260 has a Cowork-authored CHANGELOG commit (`390c7ad8`) that the rebase will drop. Expected; fix-260-rebase prompt documents this.

## Deferred items (not blocking S6, revisit later)

- spawn-mcp per-alias model selection (lesson #12 future fix).
- spawn-mcp cache-bust on raw.githubusercontent.com fetch (lesson #13 future fix).
- spawn-mcp line-buffered stdout (lesson #15 future fix).
- All three deferred per memory #13/#14 stop rules — don't touch MCP infra mid-session.
- `T-HAL-01` already merged as #258 despite being model-mismatch-tagged Opus 4.7 in the original prompt. Sonnet-compatible execution of safety-critical rule-file work is an empirical data point in favor of lesson #12's premise.
