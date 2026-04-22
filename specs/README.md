# ventd specs — CC session briefs

Four specs, drafted 2026-04-22. Each is a self-contained brief for a Claude Code session. Every spec is scoped, costed, and gated to prevent scope creep — the two failure modes of the previous team.

## Read order

1. **Spec 01 — IPMI polish** (`spec-01-ipmi-polish.md`)
   - Ships v0.3.x. Cheap, sub-1-week.
   - Sonnet only. No Opus.
   - Pure Go + fixtures. Zero HARDWARE-REQUIRED.
   - **Do this first.** It's already 90% done; this is closure.

2. **Spec 02 — Corsair USB AIO** (`spec-02-corsair-aio.md`)
   - First Phase 2 backend post-IPMI. v0.4.0 headline.
   - Sonnet + 1 Opus consult on protocol framing.
   - HARDWARE-REQUIRED for final DoD (Corsair Commander Core).
   - Split from masterplan P2-LIQUID-01 into Corsair-alone so each vendor ships independently.

3. **Spec 03 — Profile library** (`spec-03-profile-library.md`)
   - The highest-leverage feature in the plan. Ships v0.5–0.7 rolling.
   - Sonnet + 1 Opus consult on schema freeze.
   - Pure Go. Fingerprint data from real hardware.
   - Read this spec even if you don't start it soon — it shapes decisions in spec 01 and 02.

4. **Spec 04 — PI autotune** (`spec-04-pi-autotune.md`)
   - v0.6.0 headline. The "learning" story made real.
   - Sonnet + 2 Opus consults (PI stability, autotune safety).
   - HARDWARE-REQUIRED for final DoD (phoenix-desktop CPU fan + k10temp).
   - MPC is a separate spec built on this foundation.

## How to use these with Claude Code

**Each spec has a "CC session prompt — copy/paste this" block.** Use it verbatim. It tells CC:
- Which files to read first (spec + any Opus consult notes).
- Which PR to start with and what gates the next PR.
- Which tools to NOT use (no subagents, no Opus mid-session).
- When to pause for hardware verification.

**The cost-discipline pattern across all four:**
- Design in claude.ai (flat-rate on your Max plan) → commit notes to the spec folder.
- Build with Sonnet in CC → commit at every green-test boundary.
- Grind (test corpora, YAML blobs, regression cases) with Haiku if needed.
- NEVER let Opus run inside CC. It's the single biggest cost multiplier.

## Ordering rationale — why not just "do them in parallel"

You're one person. Parallel sessions against the same codebase produce merge conflicts and context fragmentation. Each of these specs wants 2–3 weeks of focused attention. Run them serially, ship the minor-version tag at the end of each, take the Reddit post, move on.

The only exception: Spec 03 PRs 1 and 2 can run in parallel. Spec 03 explicitly calls this out.

## What's NOT in this folder

- **MPC controller (P4-MPC-01).** Wait until PI is stable in production for ~30 days before specifying this. The PI fallback story in MPC is only meaningful if PI is battle-tested.
- **Windows port.** Separate product, later. See the 2026-04-22 conversation for rationale.
- **Acoustic health detection (P7-ACOUSTIC-01).** Phase 7. Needs research time upfront, not CC session time.
- **UEFI DXE stub (P9-UEFI-01).** Defer until post-1.0.

## If a spec is wrong

Each spec has "Explicit non-goals" and a "Definition of done." If during a CC session you find the spec is genuinely incomplete or incorrect, stop the session, edit the spec, commit the spec change, and restart. Do NOT let CC "improvise past" a spec ambiguity — that's exactly the autonomous-exploration pattern that burned $600 last weekend.
