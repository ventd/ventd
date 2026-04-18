# Wake-up brief — S7 start (session end 2026-04-18 S6)

Read this first. ~3 min to act, then S7 can run.

## Session S6 summary

**11 PRs merged to main** this session: fix-278/279/284 Wave 1 backends, #277 rebase, #294 scheduler race fix, #295 hal/contract gate, #281 P2-USB-BASE, #282 P2-CROSEC-01, #285 P2-IPMI-01, #299 regresslint magic-comment (closes #290), #300 watchdog RestoreOne binding (closes #287).

**1 PR merged to cowork/state**: #301 docs(roles): move ultrareview ownership Atlas → Cassidy.

**1 PR closed without merge**: #303 P4-PI-01 v1 — branch-base drift (138 files / 132 commits from stale main SHA). Production code was correct; re-dispatched as P4-PI-01-v2 with hardened `git fetch origin main && git checkout -B ... origin/main` preamble. See memory #30.

**Wave 1 Phase 2 closed.** Five backend tracks landed: USB-BASE, CROSEC, IPMI, ASAHI, PWMSYS, plus supporting rebases and test fixes.

**Ultrareview-2 triggered.** Issue #302 filed with `role:cassidy` label. 11 PRs since ultrareview-1. Cassidy picks up next time human opens her project conversation.

## Immediate action on wake (S7)

**1. Check P4-PI-01-v2 CC session status.**

```
spawn-mcp:list_sessions
```

If `cc-P4-PI-01-v2-a710ec` still running: let it cook. Budget 60-90 min; dispatched at ~11:58 UTC 2026-04-18. Poll `search_pull_requests query:"repo:ventd/ventd is:pr is:open"` every 3-5 min.

If session exited: check for PR. If PR opened, verify BRANCH_CLEANLINESS section shows `git log --oneline origin/main..HEAD` = ≤3 commits. If yes, review + merge. If history is still polluted, close + redispatch with tighter prompt.

If session exited but NO PR was opened: investigate via `tail_session` (likely auth failure to PhoenixDnB/* if the worktree got confused, though P4-PI-01-v2 targets ventd/ventd so that shouldn't apply here).

**2. Queue Phase 4 remainder serially.**

After P4-PI-01-v2 merges, dispatch these in order (all touch `internal/controller/controller.go` so serial only):
- `P4-HYST-01` — banded hysteresis. Prompt staged.
- `P4-DITHER-01` — per-curve dither. Prompt staged.

**3. Dispatch Phase 6 in parallel** (disjoint allowlists, safe to fire all in one turn).

All four prompts staged on cowork/state. **BUT** — all v1 prompts use the stale "work in /home/cc-runner/ventd" pattern. Per memory #30, need to add the `git fetch && git checkout -B ... origin/main` preamble to each before dispatch, or the same #303 incident repeats.

Fastest path: stage v2 variants of each (`P6-WIN-01-v2.md`, `P6-MAC-01-v2.md`, etc.) with the branch-base hardening block copied from `P4-PI-01-v2.md`. ~5 min of `push_files` work.

## Queue (Phases 4, 6, 8)

**Phase 4** (serial, one at a time, each touches controller.go):
- P4-PI-01 ← in flight (`cc-P4-PI-01-v2-a710ec`)
- P4-HYST-01 — hysteresis, staged, serial after PI-01
- P4-DITHER-01 — dither, staged, serial after HYST-01

**Phase 6** (parallel-safe, disjoint allowlists):
- P6-WIN-01 — WMI + ACPI, staged (needs v2 branch-base preamble)
- P6-MAC-01 — purego IOKit SMC, staged (needs v2)
- P6-BSD-01 — hw.sensors + superio, staged (needs v2)
- P6-OBSD-01 — read-only sysctl, staged (needs v2)

**Phase 8** (parallel-safe):
- P8-METRICS-01 — Prometheus /metrics, staged (needs v2)
- P8-HISTORY-01 — 30-min ring + /api/history, staged (needs v2)
- P8-CLI-01 — ventdctl socket, staged (needs v2; deploy/ventd.service touch halves MAX_PARALLEL)

**Atlas-queue quick wins still open**:
- `fix-293-config-sensor-fan-collision` — prompt staged. Sonnet, ~15 min. Closes #293.

## Hot lessons to apply at start of S7

- **Memory #30 (new this session)**: Every CC prompt targeting main MUST include `git fetch origin main && git checkout -B claude/<branch> origin/main` preamble + abort-if-.cowork/prompts/ sanity check + BRANCH_CLEANLINESS reporting section. Template at `.cowork/prompts/P4-PI-01-v2.md`.
- **Memory #18** parallel dispatch is the #1 throughput multiplier. Fire 4-6 concurrent slots per turn when allowlists don't overlap.
- **Memory #19** ultrareview is Cassidy's lane; file `role:cassidy` trigger issue at 10-PR gate. Do NOT `spawn_cc("ultrareview")`.
- **Memory #22** empty `tail_session` = running, not stuck. Poll GitHub every 3-5 min.
- **Memory #28** TPM target 4 routine. Use `search_pull_requests` over `list_pull_requests` (100x payload difference).

## Bridge project (deferred)

**Human signed off on this chat with bridge in parked state.** Phase 1 MVP CC prompt staged; `PhoenixDnB/cowork-bridge` repo exists empty. Full resume checklist at `.cowork/BRIDGE-PHASE1-RESUME.md` on cowork/state.

**Do not redispatch the bridge in S7 unless the human brings it up.** They explicitly said "we will come back to this, continue with ventd for now." The resume checklist is the authoritative handoff when they do.

## Ultrareview watch

- Issue #302 `ultrareview-2 trigger` is filed, `role:cassidy` labelled.
- Cassidy executes next time human opens her claude.ai project conversation.
- Human has ALREADY re-pasted Cassidy's SYSTEM.md per #301 merge (confirmed end of S6).
- Next ultrareview-3 trigger fires at session merge count 21 (11 + 10 more).

## Cassidy / Mia worklogs

S6 did not update role worklogs (Atlas does not write to them — that's each role's session-end protocol). Atlas-filed issues for each role to pick up:
- `role:cassidy`: #302 (ultrareview-2 trigger), plus whatever's already in her queue.
- `role:atlas`: per search, #269, #271, #272, #288, #293 open; most queued fix-287/290 resolved this session.
- `role:mia`: not audited this session; likely backlog cleanup needed.

## Throughput S6

- Merges: 11 to main, 1 to cowork/state = 12 total
- Wall-clock: ~9 hours (session start ~03:00 UTC, end ~12:00 UTC including overnight)
- Rate: ~1.2 PR/hr — below 5 PR/hr target due to:
  1. Bridge research detour (~2h artifact)
  2. Ultrareview ownership migration (~30 min)
  3. Phase 2 Wave 1 rebase cascade (~1h serial)
  4. #303 branch-base incident (~15 min diagnosis + redispatch)
- Positive signal: parallel-dispatch windows held 4-5 PR/hr when running.

## Outstanding cleanup

- `cc-P4-PI-01-v2-a710ec` session — check status first thing in S7.
- All Phase 6 + Phase 8 prompts need v2 re-stages with branch-base hardening.
- `fix-293-config-sensor-fan-collision` still staged, never dispatched.
- Session merge count: 11 → tracking toward next ultrareview gate at 21.

## Session end state

- **In flight**: `cc-P4-PI-01-v2-a710ec` (1 session, ~60-90 min budget).
- **Open PRs**: 0 at session end (everything merged or closed).
- **HALT signal**: RUN (no pause).
- **Memory at cap**: 30/30. Next replacement will have to evict another entry.

S6 done. S7 starts by listing sessions, checking P4-PI-01-v2 state, and proceeding from there.
