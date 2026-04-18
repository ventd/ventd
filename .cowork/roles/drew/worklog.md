# Drew worklog

Append-only. Most recent at bottom. New sessions read the last 20 entries.

---

## 2026-04-18 — role bootstrap

**Context:** Drew role spun up on 2026-04-18 to replace the sunset Mia slot in the ensemble. See `.cowork/roles/_archive/mia/HEADSTONE.md` for why Mia was sunset and `.cowork/roles/drew/SYSTEM.md` for the full charter.

**Action taken:** none — this is the bootstrap record. SYSTEM.md written by Atlas; Drew's first operating session has not yet occurred. Atlas also pre-seeded `.cowork/roles/drew/BOOTSTRAP.md` with Phase 10 status, release history, and first-session calibration tasks so Drew can jump to proposing a dispatch order instead of doing cold discovery.

**For other roles:** Atlas — when you next invoke a Drew session, expect Drew's first output to be a proposed Phase 10 dispatch order + assessment of the current v0.3.0 cut-candidate coherence. Do not dispatch anything Drew proposes until the operator confirms the sequencing. Cassidy — Drew's supply-chain audits do not overlap with your diff-regression audits; no lane conflict.

**Followup:** none. Next Drew session is the first operating session; starts with BOOTSTRAP.md read.

---

## 2026-04-18 — first operating session: Phase 10 dispatch queue + v0.3.0 blocker coordination

**Context:** first operational session. Operator cleared me to file directly ("do what you have to do"). Session read BOOTSTRAP.md, LESSONS.md (18 entries), roles/README.md, the full `[Unreleased]` block of CHANGELOG.md on `main`, and verified open release-blockers via `search_issues`. Phase 10 state confirmed: 1/4 complete (P10-PERMPOL-01 landed via #253).

**Action taken:** filed five `role:atlas` dispatch issues with ready-to-paste prompts.

1. **#320 — chore(changelog): clean up [Unreleased] for v0.3.x cut.** Release-blocker. Two structural defects: three spawn-mcp bullets leaked into ventd's CHANGELOG (per LESSONS #6/#9/#10, spawn-mcp is separate dev infra and must not ship to ventd operators); and duplicate Keep-a-Changelog section headers (four `### Added`, three `### Changed`, three `### Fixed`, plus sub-scope narrative headers). Prompt dispatches a Sonnet 4.6 CC session; allowlist `CHANGELOG.md` only; bullet-count receipt required in PR body.
2. **#322 — dispatch P10-SBOM-01.** CycloneDX + SPDX via syft in `.goreleaser.yml`; `sbom-validate` CI job with schema validation + govulncheck CRITICAL gate. First task in the serial Phase 10 chain.
3. **#323 — dispatch P10-SIGN-01 [HOLD until #322 merged].** cosign keyless + SLSA L3 provenance via the slsa-github-generator reusable workflow. Identity regex pinned to this repo's workflow path; no wildcards. HOLD label set because `.goreleaser.yml` allowlist collides with #322.
4. **#324 — dispatch P10-REPRO-01 [HOLD until #323 merged].** Byte-identical reproducible builds (`-trimpath`, pinned Go patch, `SOURCE_DATE_EPOCH`, `mod_timestamp` check) plus `.github/workflows/verify-reproducible.yml` running on the phoenix-desktop self-hosted runner with clean `GOMODCACHE`. HOLD label for same allowlist collision reason.
5. **#325 — dispatch UPGRADE-PATH-01 (resolves #183).** Open release-blocker #183 has no owner despite blocking v0.3.0. Per SYSTEM.md pre-release validation rules, coordination on open release-blockers is my lane. Prompt dispatches a harness under `tests/upgrade/` covering all five #183 gates (config / calibration / curves / bcrypt hash / no-re-wizard) across `{ubuntu:24.04, fedora:41, archlinux:latest, alpine:3.20}`, with `.github/workflows/upgrade-path.yml` matrix-running them. Allowlist excludes runtime code — the harness surfaces bugs, it does not fix them.

**v0.3.0 tag-cut assessment: DEFER.** Four concrete blockers, all tracked:
- #320 (CHANGELOG cleanup) — must land before any tag
- #322 (P10-SBOM-01) — must land so v0.3.0 ships with SBOMs attached (rather than retrofitting after first tag)
- #325 / #183 (upgrade-path gates) — five-gate matrix must be green across four distros
- #181 (UI Session E: Phase 4 polish + 4.5i read-only URL sharing) — not Drew's lane; flagged to operator. Needs an owner assignment or explicit deferral from v0.3.0 scope.

My SYSTEM.md `v0.3.0` assessment: content is coherent for the label ("v0.3 stream" explicitly referenced in the current CHANGELOG sub-headers). Scale would also justify v0.4.0, but I see no strong reason to renumber. Pending operator confirmation at tag-cut time.

**Phase 10 dispatch order rationale (committed):** SBOM → SIGN → REPRO, serial. All three share `.goreleaser.yml` (allowlist collision blocks parallelism per LESSONS #11). Dependency chain: SIGN covers SBOMs so SBOMs must exist first; REPRO verifies against signed artifacts so SIGN must exist first.

**Release cadence intent:** SYSTEM.md targets < 14 days between tags during active phases. v0.2.0 cut 2026-04-16; v0.3.0 target cut 2026-04-28 to 2026-05-02 window assuming #320/#322/#325 all resolve in the next 10 days. Past that window, I revisit scope (drop v0.3.0-nonessential items to a v0.3.1 or reclassify as v0.4.0 target) rather than ship an unverified tag.

**Concrete decisions this session that Atlas alone would not have made** (retention evidence per SYSTEM.md exit criteria):
1. Caught spawn-mcp CHANGELOG pollution. Atlas merged the PRs that introduced these entries; Atlas's backlog focus is dispatch+merge throughput, not release-note hygiene. Without Drew, v0.3.0 ships with "`spawn-mcp` now invokes the Claude Code CLI..." in the operator-facing release notes. Filed as #320.
2. Coordinated stale release-blockers. BOOTSTRAP's release-blocker pre-check was stale; #183 and #181 are both open, both tagged `v0.3.0`, both without owners. Atlas-alone would have continued Phase 1/2 dispatch and discovered these at tag-cut time (or missed them entirely). Filed #325 to resolve #183 concretely; #181 flagged to operator for owner assignment.
3. Explicit HOLD sequencing on #323/#324 with dependency-chain rationale in the title. Prevents Atlas from dispatching P10-SIGN-01 before P10-SBOM-01 lands and burning a CC session on an allowlist conflict / merge conflict (LESSONS #14 class failure).

**For other roles:**
- @atlas — five new `role:atlas` issues in your queue. Dispatch order I recommend: #320 first (smallest, fastest), then #322 in parallel with #325 (no allowlist collision between them), then #323 after #322 merges, then #324 after #323 merges. #325 can parallelize with #322 because the harness lives in a different tree.
- @atlas — #181 (Phase 4 UI polish + 4.5i read-only URL sharing) is labelled `release-blocker` but has no owner. It's not my lane to fix. Either assign yourself / dispatch CC for it, or push to the operator for explicit deferral from v0.3.0. If it slips past the cadence window I will recommend dropping it to v0.3.1 scope.
- @cassidy — once #320 lands, please audit the diff for content loss beyond the three targeted spawn-mcp bullets. The PR body will carry a pre/post bullet count but a diff-read confirmation would help close the loop cleanly.
- @cassidy — once #322 lands, Drew will audit the SBOM artifacts themselves (schema validation, govulncheck output); no diff audit needed from you unless workflow-security concerns surface.

**Metrics (first recording):**
- Days since last release tag: **2** (v0.2.0 cut 2026-04-16; target < 14 during active phases; well within window)
- Phase 10 P-tasks complete: **1/4** (P10-PERMPOL-01; three dispatchable now, filed as #322/#323/#324)
- SBOM compliance on latest release: **not applicable** (P10-SBOM-01 not yet landed)
- Reproducible-build delta: **not applicable** (P10-REPRO-01 not yet landed)
- `role:atlas` issues filed by Drew dispatched within 48h: **0/5 filed so far; measurement starts now**

**Followup:** #320, #322, #323, #324, #325 filed. Awaiting Atlas dispatch on #320 and #322.

---

## 2026-04-18 — session continuation: #181 coordination + next-session queue filed

**Context:** operator directed token-economy mode ("optimise on token usage starting NOW"); asked me to file #181 coordination and continue autonomously on the queued items from the previous entry.

**Action taken:** filed three more `role:atlas` issues (total 8 this day).

6. **#326 — coordinate owner for #181.** Not a dispatch prompt — an ownership ask. #181 is UI work; I have no prompt material beyond the reference to `/sessions/*/mnt/files/session_E_*.md` in the issue body. Atlas scopes, assigns, or flags scope-drop to v0.3.1.
7. **#327 — dispatch SUPPLY-GHSA-01.** Enumerate the full 17-CVE set closed by the 1.25.0 → 1.25.9 bump, draft one consolidated GHSA body at `docs/security/advisories/ghsa-v0.3.0-go-stdlib.md`, document the one-GHSA-per-security-release policy in `docs/supply-chain.md`. Maintainer files the advisory on GitHub (CC cannot via MCP).
8. **#328 — dispatch RELEASE-CHECK-01.** `scripts/pre-release-check.sh <sha>` runs the SYSTEM.md pre-release checklist as one command: clean tree, CI green on the SHA, CHANGELOG `[Unreleased]` non-empty + bullet-to-PR-count sanity, zero open `release-blocker` labels, govulncheck CRITICAL=0, build+test clean, plus Phase 10 gates (SBOM/cosign/repro) as they land. Replaces manual walk-through every release.

**Queue state after this session:** everything currently dispatchable is dispatched. Remaining Drew work is gated on landings (SIGN, REPRO, release-notes-prose after #320 lands) or is recurring Drew work that doesn't need an issue (weekly audits).

**Next session opens with:** poll live PR state via `search_issues(updated:>=<last-call>)` + `list_pull_requests(state=open)`. If #320 has landed, file release-notes-prose dispatch. If #322 has landed, flip #323 HOLD to ready. If #325 has landed with a red gate, file the underlying runtime-fix dispatch. Run the first weekly supply-chain audit rollup and post at the top of that session's worklog entry.

**Token economy notes:** two worklog rewrites this session (~9KB each) is LESSONS #10 pattern. Next session: batch all worklog appends into a single end-of-session rewrite, not per-action. Issue bodies are the authoritative record; worklog is summary.

**Followup:** #320, #322, #323, #324, #325, #326, #327, #328 filed; all `role:atlas`. No other action pending from Drew this session.
