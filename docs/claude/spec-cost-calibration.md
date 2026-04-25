# Spec Cost Calibration

Tracks Claude Code spend per PR for ventd, used to estimate future spec PRs.
**Update this file every time a spec PR ships.**

## How to use
1. Before drafting a CC prompt, find the closest analogue PR below
2. Use its actual cost as the baseline
3. Pad 50% only if the new PR touches existing committed scaffolding (sysusers, units, install scripts, packaging, release pipeline)
4. Tight specs with `.claude/rules` bindings + drift triage in chat collapse CC ~10× vs exploratory specs

## Calibration data

| Date       | Spec / PR                    | Model  | Estimate  | Actual  | Δ      | Notes                                              |
|------------|------------------------------|--------|-----------|---------|--------|----------------------------------------------------|
| 2026-04-24 | spec-01 IPMI polish (4 PRs)  | Sonnet | $20–30    | ~$25    | on     | First IPMI work, baseline for hardware backends    |
| 2026-04-25 | spec-02 PR 1.5               | Sonnet | $15–25    | $5.18   | -3×    | Tight spec + rules bindings, chat-driven drift     |
| 2026-04-25 | spec-02 (full Corsair AIO)   | Sonnet | varies    | ~$7–12  | under  | v0.4.0 ship, multiple PRs                          |
| 2026-04-25 | spec-06 PR 1                 | Sonnet | $5–8      | $1.30   | -4×    | Mechanical install-contract fixes                  |
| 2026-04-25 | spec-06 PR 2 (AppArmor HIL)  | Sonnet | TBD       | TBD     | —      | Includes Proxmox VM provisioning                   |
| 2026-04-25 | v0.4.1 release total         | mixed  | $10–30    | $7–12   | under  | Under budget, no rework                            |
| 2026-04-25 | spec-03 PR 1 (schema v1)     | Sonnet | $5–10     | ~$3.50  | -2×    | Renames + parallel schema, no breaking change      |
| 2026-04-23 | Claude stack setup           | mixed  | —         | ~$3.50  | —      | MCP wiring, plugins, hooks, skills                 |
| 2026-04-26 | claude-tooling-bundle #632   | Haiku  | $3–6      | ~$8*    | over   | 11 files, scripts+templates+skills, no Go. *block-level approx, not isolated |

## Rules of thumb (extracted)
- **Tight Sonnet PR** (spec written + rules bindings + chat drift triage) → **$1–5 actual**
- **Spec-touches-scaffolding PR** (install scripts, units, packaging) → pad 50%, expect **$3–8**
- **Docs/spec-only PR** (Haiku) → **$0.50–$3**
- **Release PR** (CHANGELOG + version bump) → **$0.50–$2 Haiku**
- **Exploratory PR** (no spec, CC discovers shape) → **5–10× tight PR cost** — avoid

## Anti-patterns observed
- Iterating `gh run view` to debug CI → eats tokens, replaced by `triage-run.sh` (one-shot)
- WSL nano → clip.exe mangles UTF-8, costs a redo
- Scrollback chasing on Termius mobile → use `claude --resume` instead
- Pushing PR direct to `main` (admin bypass) → linear history intact but invariant violated, costs nothing in tokens but in trust

## Open questions (track here)
- spec-05 predictive thermal Phase 0 — no analogue, expect 5–10× variance
- spec-05-prep trace harness — drafted estimate $84–131, no actual yet

## Spend velocity (rolling, ccnow block-level)

Tracks total CC spend per day to surface burn rate vs $300/mo target ($10/day avg).

| Date       | Spend  | Notes                                              |
|------------|--------|----------------------------------------------------|
| 2026-04-23 | $11.75 | Claude stack setup day                             |
| 2026-04-24 | $30.62 | spec-01 IPMI polish + scaffolding (3 blocks)       |
| 2026-04-25 | $27.62 | v0.4.0 + v0.4.1 ships + spec-03 PR 1 (3 blocks)    |
| 2026-04-26 | $8.15* | spec-03 PR 2 chat work + #632 tooling bundle       |
| **4-day**  | **~$78** | **~$19.5/day** — 2× target rate                |

*Active block at time of logging; final daily total may differ.

**Drivers of the spike:** compressed ship week (two releases + tooling bundle in 4 days). Not structural, but worth noting:
- High-density ship weeks burn 2–3× target rate
- Plan rest days between ships to amortize spend
- spec-03/04/05 are larger scope — pace expectations accordingly

**Update cadence:** end of each ship day, paste ccnow daily total into table.
