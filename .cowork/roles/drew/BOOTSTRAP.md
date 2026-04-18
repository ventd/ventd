# Drew bootstrap context — first-session briefing

**Generated:** 2026-04-18 by Atlas, concurrent with Drew role creation.
**Purpose:** short-circuit the "what's the current state of Phase 10 and releases" discovery phase so the first Drew session can jump to proposing a dispatch order instead of re-reading the repo.

Everything here is accurate as of 2026-04-18 ~12:30 UTC. Verify the specifics yourself at session start if actions have landed since.

---

## Phase 10 status (as of role creation)

| Task | Status | PR | Notes |
|------|--------|----|----|
| **P10-PERMPOL-01** | **DONE** | #253 (merged 2026-04-17) | Permissions-Policy header + ETag on embedded UI. `internal/web/security.go` is the carry-forward file for future hardening. |
| **P10-SBOM-01** | open | — | CycloneDX + SPDX SBOMs on every release. `.goreleaser.yml` is the carry-forward file. No existing scaffold. |
| **P10-SIGN-01** | open | — | Cosign keyless + SLSA L3 provenance. `.goreleaser.yml` + `.github/workflows/release.yml`. No existing scaffold. |
| **P10-REPRO-01** | open | — | Reproducible builds, rebuild-and-diff action. Depends on P10-SIGN-01 per masterplan §8. `.goreleaser.yml` + `Makefile`. |

**Summary:** 1/4 complete. Three remaining, all Sonnet 4.6, all non-safety-critical, all isolated allowlists. Recommended dispatch order: **P10-SBOM-01 → P10-SIGN-01 → P10-REPRO-01** (dependency order). Dispatch one at a time, not in parallel — they share allowlist files (`.goreleaser.yml`).

## Release history

**Latest tag cut / attempted:** v0.3.0 tag work drafted at `.cowork/prompts/tag-v030.md` but never dispatched. CHANGELOG's `## [Unreleased]` block contains ~10 entries covering Phase 1 completion (P1-HAL-01, P1-HAL-02, P1-MOD-01, P1-MOD-02, P1-HOT-01, P1-HOT-02, P1-FP-01, P1-FP-02), Phase 2 Wave 1 (P2-USB-BASE, P2-ASAHI-01, P2-IPMI-01, P2-CROSEC-01, P2-PWMSYS-01), P10-PERMPOL-01, Go toolchain bump (1.25.0 → 1.25.9 closing 17 stdlib CVEs), and several Cassidy-surfaced fixes.

The v0.3.0 cut has been blocked by: (1) ultrareview-1 findings now resolved, and (2) no owner driving the release cadence. You are now that owner.

**Previous tags:** read `git ls-remote --tags origin ventd/ventd | tail -5` or `list_pull_requests sort:updated` scanning for release-related PRs. I did not pre-check this at bootstrap time.

## Current Unreleased-block coherence

Recommend you read `CHANGELOG.md` on `main` in your first session and assess: is the current Unreleased block a coherent v0.3.0 story, or is it a grab-bag that would confuse operators? My read (not authoritative): it IS coherent — Phase 1 HAL refactor + Phase 2 backend portfolio + supply-chain polish — but the volume is high enough that a v0.3.0 cut now would ship behaviour users should know about individually. Consider whether v0.3.0 is the right tag or if this is closer to a minor bump (v0.3 → v0.4).

## Open `role:atlas` backlog (supply-chain-adjacent items only)

None currently. Cassidy has filed recent `role:atlas` concerns (#288 controller perm-err, #293 config collision, #296 mutateConfig umbrella, #298 cache hardening) but these are code-quality regressions, not supply-chain issues — they belong to Atlas's dispatch queue, not Drew's.

If Cassidy's next ultrareview (pending — trigger #302 filed 2026-04-18) flags any supply-chain-adjacent findings, those become Drew's focus first.

## Open `role:drew` backlog

Zero. Clean start.

## Release-blocker issues

None currently labelled. Check `search_issues(query="repo:ventd/ventd is:issue is:open label:release-blocker")` at session start to verify — my pre-check may be stale.

## What I want from your first session

1. Verify the Phase 10 status table above against current state.
2. Read `CHANGELOG.md` `## [Unreleased]` block; write your assessment of whether the current cut-candidate is coherent for v0.3.0 or should wait.
3. Draft `role:atlas` issue bodies for the three remaining Phase 10 tasks in dependency order. Do NOT file them yet — present the drafts in your session output so the operator can review the dispatch sequencing.
4. Write the first entry to your worklog summarising findings + proposed dispatch order.

That's the first session. Don't file anything to GitHub until the operator confirms — this is your calibration session.

## Existing release-related prompts you can reference

- `.cowork/prompts/tag-v030.md` (alias `tag-v030`) — the template for a tag-cut CC session. Adapt for v0.3.x / v0.4.
- `.cowork/prompts/permpol.md` and `.cowork/prompts/P10-PERMPOL-01.md` — prior P10 dispatch examples for prompt-shape reference.
- `.cowork/prompts/tag-v0.3.0.md` — earlier variant of the v0.3.0 tag prompt.

All on `cowork/state`.

## Infrastructure available

- **Self-hosted GHA runner** on phoenix-desktop with labels `self-hosted,linux,x64,hil,phoenix-desktop,hwmon,nvml,rtx4090`. Service: `actions.runner.ventd-ventd.phoenix-desktop.service`. Smoke-tested via `.github/workflows/runner-smoke.yml` (PR #249). Available for reproducible-build verification jobs once P10-REPRO-01 lands.
- **Cosign keyless** not yet configured. OIDC identity for GitHub Actions is the intended path per masterplan §8 P10-SIGN-01.
- **.goreleaser.yml** exists at repo root (I didn't read it; inspect at session start). SBOM/sign/SLSA sections are the additive territory.

## You are not alone

Atlas handles dispatch and merge. Cassidy audits diffs post-merge. You file `role:atlas` issues with prompts; Atlas dispatches them; Cassidy audits the results; you verify compliance. If Atlas dispatches something Phase 10 related, Cassidy's audit comes to both your queue and Atlas's — you own the artifact-verification follow-up.

The ensemble has been running Phase 2 (Atlas + Cassidy + Drew) since 2026-04-18. Phase 1 (Atlas + Cassidy + Mia) ran for ~5h same day before Mia sunset; see `.cowork/roles/_archive/mia/HEADSTONE.md` if you want the backstory.
