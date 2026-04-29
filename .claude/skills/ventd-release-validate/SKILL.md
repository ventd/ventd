---
name: ventd-release-validate
description: |
  Use BEFORE pushing a release tag in the ventd repo. Triggers on:
  "cut release", "tag vX.Y.Z", "push tag", "ready to release". Chains
  after ventd-preflight — runs preflight first, then release-specific
  checks: tag does not exist, no orphan release, no in-flight
  release.yml runs, cosign uses --bundle= form, all release.yml
  action SHAs are pinned, CycloneDX version range accepts both 1.5
  and 1.6. Do NOT use for: PR pushes (use ventd-preflight alone),
  diagnosing release failures (use ci-triage), or non-release tags
  (annotations, docs-only).
---

# ventd-release-validate

Comprehensive release pipeline validation before pushing a version tag.

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Latest tags: !`git tag --sort=-v:refname | head -5`

Current branch: !`git branch --show-current`

Recent release runs: !`gh run list --workflow=release.yml --limit 3 --json status,conclusion,headBranch,createdAt 2>/dev/null | head -20`

## Run

```bash
bash .claude/scripts/release-validate.sh <version-tag>
# example: bash .claude/scripts/release-validate.sh v0.5.0
```

Exit 0 = safe to push tag. Exit 1 = FAIL (do not push). Exit 2 = WARN
only (advisory).

## What it checks

1. **Preflight** — runs ventd-preflight with detected spec ID
2. **Tag collision** — tag does not already exist
3. **Orphan release** — no incomplete release on GitHub
4. **Cosign format** — `--bundle=` form (not separate `--output-signature`)
5. **Action pinning** — all `.github/workflows/release.yml` action refs
   pinned (v-tags or 40-char SHAs)
6. **CycloneDX version** — warns if version range doesn't support both
   1.5 and 1.6

## Gotchas (real failure modes — read all before tagging)

- **Tag delete doesn't unfire workflows.** Pushing a tag immediately
  dispatches release.yml; deleting the tag does NOT cancel
  in-flight runs. Use `gh run cancel <run-id>` first, then delete
  the tag.

- **Never tag before merge AND pull.** Sequence is mandatory:
  1. `gh pr merge` succeeds (squash merge confirmed)
  2. `git pull` shows the squash commit on main
  3. THEN `git tag -a <version>`
  Skipping step 2 → tagging the wrong commit. This has happened.

- **Partial publish risk.** release.yml may publish assets to GitHub
  before failing downstream (cosign, SBOM). Check `gh release list`
  and `gh release delete <version>` orphans before retagging.

- **Cosmetic failures don't block binary publish.** provenance / SLSA
  aggregator / changelog-PR steps may show red while binaries
  publish fine. Log in `docs/claude/spec-cost-calibration.md`. Do
  NOT retry the tag — log it and move on.

- **"No checks reported" on tag push.** release.yml never started.
  Push an empty commit to re-trigger:
  `git commit --allow-empty -m "chore: re-trigger CI" && git push`

- **CycloneDX 1.5↔1.6 drift.** The generator auto-bumps. If the
  validator gate is pinned to 1.5, the next release fails. Validator
  must accept both versions; the generator picks one at runtime.

- **Cosign `--bundle=` is mandatory format.** The split
  `--output-signature` + `--output-certificate` form has been
  deprecated upstream. release.yml uses `--bundle=<path>` exclusively.

- **Action pinning matters for supply-chain attestation.** SLSA
  provenance requires SHA-pinned actions. v-tags pass the script's
  check but full 40-char SHAs are stronger. Pin all third-party
  actions to SHA; first-party `actions/*` can be v-tags.

- **`gh release` does not match `git tag`.** A tag exists locally;
  the GitHub release is a separate object. Both must be cleaned for
  retag (`git tag -d`, `git push origin :refs/tags/<v>`,
  `gh release delete <v>`).

## When this is the wrong skill

- **Pushing a feature branch:** ventd-preflight alone is sufficient.
- **Release CI is red after tag push:** ci-triage with `--tag <v>`
  produces the diagnostic.
- **Tag already pushed and you want to redo it:** see "Tag delete
  doesn't unfire workflows" above. There's a sequence; don't skip steps.

## Constraints

- Read-only. Does not push tags, does not edit workflows.
- Does not run release.yml. Validates pre-conditions only.
- Does not delete orphan releases — surfaces them; deletion is a
  user decision.

## Out of scope

- Editing release.yml or any workflow
- Pushing tags
- Deleting orphan releases
- Re-running release pipelines
