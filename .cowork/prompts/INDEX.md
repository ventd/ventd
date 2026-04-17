# Cowork prompt index

Prompts live at `.cowork/prompts/<TASK-ID>.md` on the `cowork/state`
branch. The `<TASK-ID>` is what you type in the CC activation
message.

## How to use

```
Read the prompt at .cowork/prompts/<TASK-ID>.md on the cowork/state branch of origin and execute it exactly as written.
```

Replace `<TASK-ID>` with any name in the "ready" column below.

## Current prompts

| Task ID          | Status       | Model         | Track | Goal (short)                                              |
|------------------|--------------|---------------|-------|-----------------------------------------------------------|
| T0-META-01       | dispatched   | Sonnet 4.6    | META  | CI lint enforcing rule-to-subtest bindings                |
| T0-META-02       | pre-drafted  | Sonnet 4.6    | META  | CI lint enforcing regression-test-per-closed-bug          |
| T0-INFRA-03      | dispatched   | Sonnet 4.6    | INFRA | Implement faketime fixture (monotonic clock, goroutine-safe) |
| P1-HAL-01        | dispatched   | **Opus 4.7**  | HAL   | Introduce FanBackend interface; hwmon + NVML behind it    |
| P1-FP-01         | dispatched   | Sonnet 4.6    | FP    | Fingerprint-keyed hardware profile DB (≥18 boards)        |
| P10-PERMPOL-01   | pre-drafted  | Sonnet 4.6    | SUPPLY| Permissions-Policy header + ETag on embedded UI           |

## Status legend

- **ready**: prompt exists, no unmet dependencies, safe to start now
- **dispatched**: prompt is presumed already running in a CC terminal
- **pre-drafted**: prompt exists but has an unmet dependency (noted at top of its file); do not start until unblocked
- **queued**: prompt exists and is ready, held only for CC terminal capacity
- **blocked**: has unmet dependency; waits for upstream merge

## Recommended start order (for single-threaded operation)

1. `P1-HAL-01` — Opus, longest, critical path (unblocks 3 downstream)
2. `P1-FP-01` — Sonnet, parallel track (unblocks FP-02 + MOD-01)
3. `T0-META-01` — Sonnet, unblocks META-02
4. `T0-INFRA-03` — Sonnet, infra maturity

For multi-threaded operation, any ordering works — they have no
allowlist intersections.

## Auto-unlock chain

- `T0-META-01` merges → `T0-META-02` becomes ready
- `P1-HAL-01` merges → `P1-HAL-02`, `P1-HOT-01`, `P1-HOT-02` become ready
- `P1-FP-01` merges → `P1-FP-02`, `P1-MOD-01` become ready

Cowork drafts the new prompts as soon as these merges land.

## Updating this index

This file is regenerated every time Cowork adds, dispatches, or
merges a prompt. Treat the `cowork/state` branch's version as
authoritative.
