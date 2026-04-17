# Cowork Throughput Tracker

Append-only. One row per Cowork session. Throughput is the single
metric that matters — PRs merged per hour of session wall-clock.

Baseline computed 2026-04-18 from `list_pull_requests` merge history
on ventd/ventd. Method: active sessions delimited by >2h gap in
`merged_at` timestamps.

## Baseline sessions (pre-tracker)

| session | date        | duration | PRs | PR/hr | notes |
|---------|-------------|----------|-----|-------|-------|
| S1 (human-driven) | 2026-04-15→16 | 13.2 h | 69 | **5.2** | pre-Cowork; mostly docs/tier-1.5 |
| S2 (burst) | 2026-04-16 eve | 0.8 h |  6 | **7.6** | likely batched auto-fixes |
| S3 | 2026-04-17 am  | 2.2 h | 10 | **4.5** | mixed |
| S4 (Cowork-orch) | 2026-04-17 pm  | 4.8 h | 13 | **2.7** | spawn-mcp deploy + Phase 1 firsts |

## Cowork sessions (tracker active)

| session | date | duration | PRs | PR/hr | blocker | lesson ref |
|---------|------|----------|-----|-------|---------|------------|
| S5 | 2026-04-18 | ~5h | 9 | **1.8** | PAT rotation + prompt-model trap + buffered-log diagnostics + CHANGELOG conflict mis-diagnosis | #11, #12, #13, #14, #15 |

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

## Rule

Session END commit appends one row here + one LESSONS.md entry.
If PR/hr < prior session for equivalent/heavier work, LESSONS.md
must name the specific regression cause. If PR/hr >= prior session,
LESSONS.md names the specific improvement.
