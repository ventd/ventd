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
| S5 | 2026-04-18 | in-progress | 0 | **0.0** | spawn-mcp perms cascade (5 failed dispatches) | #9, #10 |

## Rule

Session END commit appends one row here + one LESSONS.md entry.
If PR/hr < prior session for equivalent/heavier work, LESSONS.md
must name the specific regression cause. If PR/hr >= prior session,
LESSONS.md names the specific improvement.
