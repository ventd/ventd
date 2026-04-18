# Cowork Throughput Tracker

Append-only. One row per Cowork session. Two metrics tracked:

1. **PR/hr** — PRs merged per hour of session wall-clock. The headline number.
2. **TPM** — tool calls per merged PR. The efficiency number. Lower = less waste.

Baseline computed 2026-04-18 from `list_pull_requests` merge history
on ventd/ventd. Method: active sessions delimited by >2h gap in
`merged_at` timestamps.

## TPM — how to count

TPM measures Cowork's own MCP invocations from "CC opens PR" to "PR merged on main."

**What counts:** every `claude github:*` or `spawn-mcp:*` call Cowork makes between the PR appearing in `list_pull_requests` and the `merge_pull_request` succeeding. Includes: search/list, get_diff, get_check_runs, update_pull_request, merge_pull_request, any rebase orchestration.

**What does not count:** initial prompt authoring (push_files, create_or_update_file on `.cowork/prompts/`), ultrareview dispatches, memory_user_edits, session-end commits, CC sessions themselves (those are accounted for under PR/hr as the unit of work).

**How to log:** rough count is fine. Over-by-one doesn't matter. Under-count is the risk — err toward over-counting if unsure.

**Target:** 4 TPM for routine PRs (search → update_pull_request → merge → done, plus one buffer). Anything >6 TPM means I read/verified something I should have trusted.

## Baseline sessions (pre-tracker)

| session | date        | duration | PRs | PR/hr | TPM | notes |
|---------|-------------|----------|-----|-------|-----|-------|
| S1 (human-driven) | 2026-04-15→16 | 13.2 h | 69 | **5.2** | n/a | pre-Cowork; mostly docs/tier-1.5 |
| S2 (burst) | 2026-04-16 eve | 0.8 h |  6 | **7.6** | n/a | likely batched auto-fixes |
| S3 | 2026-04-17 am  | 2.2 h | 10 | **4.5** | n/a | mixed |
| S4 (Cowork-orch) | 2026-04-17 pm  | 4.8 h | 13 | **2.7** | n/a | spawn-mcp deploy + Phase 1 firsts |

## Cowork sessions (tracker active)

| session | date | duration | PRs | PR/hr | TPM | blocker | lesson ref |
|---------|------|----------|-----|-------|-----|---------|------------|
| S5 | 2026-04-18 | ~5h | 9 | **1.8** | ~10 | PAT rotation + prompt-model trap + buffered-log diagnostics + CHANGELOG conflict mis-diagnosis | #11, #12, #13, #14, #15 |
| S6 | 2026-04-18 | in-progress | tbd | tbd | tbd | TPM tracker introduced mid-session | #17 |

## S5 summary

Merged: #251 spawn-mcp-user-collapse, #252 spawn-mcp-print-mode, #253 P10-PERMPOL-01, #254 T0-META-02, #255 T-WD-01, #256 settings-allowlist-fix, #257 P1-FP-02, #258 T-HAL-01, #259 P1-MOD-01.

Open at session end: #260 P1-HOT-01 (CHANGELOG conflict, fix-260-rebase queued).

Real masterplan/testplan progress: 6 PRs (#253, #254, #255, #257, #258, #259). Other 3 PRs were unblocking infra.

Throughput gap vs human baseline (5 PR/hr): factor of 2.8x slower. Causes:
- 90 min lost to PAT rotation diagnostic + fix (memory #8 predicted 0 PR/hr while blocked; actual was worse due to diagnostic overhead)
- 20 min lost to P1-HAL-02 model-mismatch-abort + CDN cache wait (lessons #12, #13)
- 15 min lost to #257 CHANGELOG conflict MCP mis-resolution (lesson #14)
- ~60 min of useful parallel work (4 concurrent CC dispatches produced 4 merges at 5.3 PR/hr — at human baseline)

The parallel-dispatch architecture works. When it works. The tax to reach "it works" consumed 60% of the session.

## S6 in-progress notes

TPM tracker introduced after user feedback that context-limit hits were forcing mid-conversation restarts. Culprit patterns observed:

1. `list_pull_requests` when `search_pull_requests` suffices — list returns full PR bodies (often 30k tokens each from CC reporting blocks); search returns metadata only.
2. `get_diff` on mechanical PRs where CI-green + ultrareview-binding is sufficient signal.
3. `get_check_runs` when `get_status` returns the same gate-result in 1/10 the tokens.
4. Re-reading files just committed via `create_or_update_file`.
5. Polling `list_sessions` repeatedly — one call per session-wait turn, not one per minute.

Protocol adjustments for S6+:
- Default to `search_pull_requests` for existence/state queries; `list_pull_requests` only when PR bodies are needed.
- Skip `get_diff` unless PR touches safety-critical paths (controller, watchdog, calibrate) or CI reports failures.
- Trust `get_status` (single "state" string) over `get_check_runs` (full check array) when the question is just "can I merge?".
- Never re-read content I committed this session; track state in response text.
- `list_sessions` max once per turn; prefer `search_pull_requests` for real progress signal (memory #22).

## Rule

Session END commit appends one row here + one LESSONS.md entry.
If PR/hr < prior session for equivalent/heavier work, LESSONS.md
must name the specific regression cause. If PR/hr >= prior session,
LESSONS.md names the specific improvement.

If TPM > 6 for any individual PR, LESSONS.md names which calls were
skippable and why they were made.
