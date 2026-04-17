# Cowork prompt index

Prompts live at `.cowork/prompts/<TASK-ID>.md` on the `cowork/state`
branch. Aliases for these prompts are in `.cowork/aliases.yaml`.

## Paste one of these into a fresh CC window

| Paste this | What it runs                                            | Model        |
|------------|---------------------------------------------------------|--------------|
| `hal`      | HAL interface refactor (critical path)                  | **Opus 4.7** |
| `fp`       | Hardware fingerprint DB                                 | Sonnet 4.6   |
| `rulelint` | CI lint — rule-to-subtest bindings                      | Sonnet 4.6   |
| `permpol`  | Permissions-Policy header + ETag (ready; queued)        | Sonnet 4.6   |

`regresslint` and `faketime` are currently unavailable (regresslint
blocked on rulelint merge; faketime already dispatched and its PR
is pending merge).

## If CC hasn't read CLAUDE.md yet

Fallback activation (self-contained paste, no reliance on CLAUDE.md):

```
Execute the task at .cowork/prompts/<TASK-ID>.md on origin/cowork/state exactly as written.
```

Replace `<TASK-ID>` with the task you want: `P1-HAL-01`,
`P1-FP-01`, `T0-META-01`, `T0-META-02`, `P10-PERMPOL-01`.

## Alias ↔ task-ID map

| Alias         | Task ID          |
|---------------|------------------|
| `hal`         | `P1-HAL-01`      |
| `fp` / `hwdb` | `P1-FP-01`       |
| `rulelint`    | `T0-META-01`     |
| `regresslint` | `T0-META-02`     |
| `permpol`     | `P10-PERMPOL-01` |

## Status legend

- **dispatched**: a CC terminal is presumed to be running this; don't start another on the same alias
- **ready**: no unmet deps; safe to start now
- **ready-queued**: ready but Cowork is holding it to manage terminal capacity
- **blocked**: has unmet dependency; waits for an upstream merge

## Auto-unlock chain

- `rulelint` merges → `regresslint` becomes `ready`
- `hal` merges → three new aliases appear (`hal-calibrate`, `hot`, etc. — Cowork publishes names when the time comes)
- `fp` merges → two new aliases appear (`hwdb-remote`, `modalias`)

## Recommended start order (single-threaded)

1. `hal` — longest, critical path
2. `fp` — parallel track to HAL
3. `rulelint` — unblocks `regresslint`
4. `permpol` — independent; fills a spare terminal

## This file is authoritative

The `cowork/state` branch's version of this file is the source of
truth. Local copies go stale when Cowork publishes new prompts.
