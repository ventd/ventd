# Mia — archived 2026-04-18

Mia was the triage role in the Phase 1 ensemble (Atlas + Cassidy + Mia). She ran for approximately 5 hours across 3 sessions on 2026-04-18 before being sunset.

## Why archived

Per the discussion captured in issue #310 and the orchestrator session that followed, the triage role did not earn its coordination overhead over the observation window. Specific findings:

1. **Duplicate-self-filing rate was high** — 3 of 4 issue filings in session 3 were Mia-authored duplicates (#290 legitimate, #291 and #292 closed as duplicates of #290; near-miss on a fourth duplicate of #187 caught by search-first).
2. **No surfaced cross-role bug was exclusively catchable by the triage lane.** The patterns Mia found (duplicate filings, the missing `@mia closing:` plumbing documented in #310) were about Mia's own lane, not about the ensemble's quality. Cassidy's audits (#288, #293, #296, #298) caught real regressions Atlas would have missed — so the reviewer lane does earn its overhead — but the triage lane did not.
3. **The `@mia closing:` protocol was unimplementable via MCP.** #310 concern 2 documented that Atlas and Cassidy had no signal path to Mia for close requests, and three issues (#274, #283, #286) were left open with "closing:" comments that Mia never retriggered on. The plumbing proposal in #310 (a `role:mia-close-request` label convention) was sound but added coordination cost without clear payoff.
4. **Triage responsibilities fit naturally into dispatch.** Label application, duplicate detection, regresslint compliance, and milestone hygiene can be performed by Atlas as part of each dispatch cycle at ~1 extra MCP call per cycle. A dedicated lane was over-engineering.

## What moved where

- **Triage responsibilities** — folded into Atlas. See `.cowork/roles/atlas/ADDENDUM.md` (to be incorporated into Atlas's orchestrator system prompt at the human's convenience).
- **Mia's worklog** — archived at `.cowork/roles/_archive/mia/worklog.md` for historical reference.
- **Mia's SYSTEM.md** — archived at `.cowork/roles/_archive/mia/SYSTEM.md`.
- **Mia's proposed improvements (#310 concerns 2, 3, 4)** — partially retained:
  - Concern 2 (close-request plumbing): resolved by Atlas absorbing the close-issue authority. The label convention is no longer needed.
  - Concern 3 (mid-session awareness via `search_issues updated:>=<session-start>`): generalised to Atlas's session-continuation protocol, applies to any role poll.
  - Concern 4 (mandated self-analysis): retained as a quarterly cadence (not every-5-sessions), applies to all roles.

## What replaced Mia

A new role — **Drew** — in the release-engineering slot. See `.cowork/roles/drew/SYSTEM.md`. Drew owns tags, release notes, Phase 10 P-tasks (SBOM, signing, reproducible builds), and pre-release validation.

## Exit criteria for the replacement

Drew is on the same test terms Mia was: ~one week of operation. If Drew doesn't produce at least one concrete decision that would have been made differently without the role, the role slot sunsets again and Atlas + Cassidy continues as the permanent two-role ensemble.

## Preservation note

The three-role ensemble experiment produced real institutional value even if Mia herself didn't earn retention:

- LESSONS.md entries #16 (role bootstrap via URL-fetch refusal) and #17 (ultrareview-as-Cassidy's-lane) are directly attributable to running separate roles.
- The SYSTEM.md-as-project-custom-prompt pattern (Pattern A in `.cowork/roles/README.md`) was validated by multi-role operation.
- Cassidy's lane, by contrast, demonstrably earned retention. See Cassidy-authored issues #288, #293, #296, #298.

The headstone is not a failure mode — it's a measurement. One of three slots didn't fit; two of three did.
