# Hardware catalog harvest candidates

Staging area for `internal/hwdb/profiles-v1.yaml` candidate entries
produced by the autonomous catalog-harvest sub-agent (#791). Entries
here are NOT embedded into the binary and don't affect runtime
hardware matching — they exist for human review and incremental
batched merge into `profiles-v1.yaml`.

## Files

| File | Contents | Merge timing |
|---|---|---|
| `profiles-high-confidence.yaml` | Entries with manufacturer manual + lm-sensors confirmation + community `sensors` paste evidence (10 boards). | Merge after HIL on a sample of 2-3 of these. |
| `profiles-medium-confidence.yaml` | Manual + chip family inferred (17 boards). | Merge after HIL or community `sensors` paste per board. |
| `profiles-low-confidence.yaml` | Generation-pattern guesses, no live evidence (5 boards). | Don't merge until elevated to medium via HIL. |
| `HARVEST-YYYY-MM-DD.md` | The full harvest report with sources, rationale, and confidence rubric. | Reference document, never merged. |

## Workflow

1. Pick a board from `profiles-high-confidence.yaml`.
2. HIL-test on a real machine of that model OR get a community-submitted
   `sensors` paste that matches the harvested layout.
3. Move the entry to `profiles-v1.yaml` (top-level catalog) with
   `verified: true` and a `verified_at: <date>` field.
4. Drop the staged copy from `candidates/`.

## Why staged here, not in the live catalog

A malformed entry in `profiles-v1.yaml` will fail `LoadCatalog()` at
daemon startup — every ventd instance refuses to load until the bad
entry is fixed. Shipping the harvest's machine-generated YAML directly
risks that failure mode. The staging files are read-only documents
shipped for review; nothing imports them.

## Cross-refs

- #788 v0.6.0 product roadmap umbrella
- #791 agent-driven catalog harvest sub-issue
- `tools/catalog-harvest/` (planned) — recurring harvest pipeline
