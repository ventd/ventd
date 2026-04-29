# Cost routing for ventd specs

Fill the **Estimated session cost** field in every spec using this
table. Numbers reflect Phoenix's calibration history; update when new
data lands in `docs/claude/spec-cost-calibration.md`.

## Model and session cost table

| Work type                              | Model  | Sessions | Cost est. |
|----------------------------------------|--------|----------|-----------|
| Pure Go, fixtures, no hardware         | Sonnet | 3–5      | $5–15     |
| New backend (USB HID, IPMI extension)  | Sonnet | 8–12     | $10–25    |
| Protocol design review                 | Opus¹  | 1        | ~$3       |
| PI/MPC math review                     | Opus¹  | 1–2      | ~$3–6     |
| HARDWARE-REQUIRED final DoD            | Sonnet | +2–3     | +$5–10    |
| Mechanical (lint fixes, test stubs)    | Haiku  | 1–2      | $1–3      |
| Ground-truth probe (pre-CC discovery)  | Haiku  | 1        | ~$0.20    |

¹ Opus is for consult sessions in claude.ai chat only — never in CC
terminals. Architecture, design tradeoffs, spec drafting happen in
chat at flat rate; implementation happens in CC at per-token cost.

## Routing rules

- **Never call Opus inside CC.** Opus tokens in CC blow the per-spec
  budget. Architecture decisions belong in claude.ai chat.
- **Never invoke subagents inside a spec session.** Session cap is 3
  per session. Subagents pattern-match the $600 weekend.
- **Ground-truth probe before CC prompt.** Q1-Q5 Haiku probe (4 greps,
  STOP-and-report) collapses CC cost ~10×. Skipping it is the single
  biggest cost regression observed.
- **Pad estimates 50% only when touching existing committed scaffolding.**
  sysusers, units, install scripts, docker workflows have hidden
  coupling. Pure new code does not get the pad.
- **Tight-probed PRs land at 50–65% of low estimate.** If a probed PR
  exceeds the low estimate, something drifted — surface and pause.

## HARDWARE-REQUIRED gating

If any DoD bullet requires a real device:

1. Mark the bullet with `[HARDWARE-REQUIRED]`
2. Add a `## Hardware gates` section listing exact rigs
3. Reference rigs by their actual identifiers from CLAUDE.md:
   - `192.168.7.222` MiniPC (Celeron, low-end Linux HIL)
   - `192.168.7.10` Proxmox host (5800X + RTX 3060)
   - Steam Deck, 3 laptops, 13900K dual-boot, 9900K planned

A spec without hardware gates that requires hardware is a spec that
will silently fail at integration. The gate is not optional.

## Budget context

- Per-spec target: $10–30 CC spend
- Monthly target: $300/month CC ceiling
- Spec cost log: `docs/claude/spec-cost-calibration.md`
- 4-day burn rate is monitored against monthly ceiling

If a spec's estimate exceeds $30, split it into PRs. If a single PR
exceeds $30, the spec is wrong, not the PR.

## Cost-by-spec history (calibration anchors)

Recent shipped specs and actual cost (for comparing new estimates):

- spec-12 PR 1 — $4.41 (UI redesign, mockups → React)
- Schema v1.2 — $5.17 (catalog YAML schema bump)
- spec-15 PR 1 framework — $9.78 (experimental feature scaffolding)
- spec-15 F1 amd_overdrive — $5.98 (HAL backend, ~50% under via probe pattern)

Pattern: probed PRs come in well under estimate. Unprobed PRs hit or
exceed estimate. Always probe first.
