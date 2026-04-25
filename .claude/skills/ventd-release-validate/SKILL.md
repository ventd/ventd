---
name: ventd-release-validate
description: Use BEFORE pushing a release tag in the ventd repo. Chains after ventd-preflight. Validates: tag does not exist, no orphan release, no in-flight release.yml runs, cosign uses --bundle= form, all release.yml action SHAs are pinned, cyclonedx version range accepts 1.5+1.6. Triggers on intents like "cut release", "tag vX.Y.Z", "push tag".
---

# ventd-release-validate

Comprehensive release pipeline validation before pushing a version tag.

## Usage

Run this skill before:
- Pushing a release tag
- Cutting a new version
- Running the release.yml workflow

Example invocation:
```bash
bash .claude/scripts/release-validate.sh v0.5.0
```

## Checks performed

1. **Preflight** — runs ventd-preflight with detected spec ID
2. **Tag collision** — confirms tag does not already exist
3. **Orphan release** — confirms no incomplete release on GitHub
4. **Cosign format** — confirms `--bundle=` form (not separate `--output-signature`)
5. **Action pinning** — confirms all `.github/workflows/release.yml` action refs are pinned (v-tags or 40-char SHAs)
6. **CycloneDX version** — warns if version range doesn't support both 1.5 and 1.6

## Exit codes

- **0** — all checks passed, safe to push tag
- **1** — one or more FAIL checks (do not push tag)
- **2** — WARN checks only, FAILs empty (advisory, safe to push)

## Failure remediation

| Check | Failure | Fix |
|-------|---------|-----|
| tag collision | tag exists | Delete tag locally/remote and choose a new version |
| orphan release | release exists | `gh release delete <version>` before re-tagging |
| cosign format | not using `--bundle=` | Verify `.github/workflows/release.yml` cosign invocation |
| action pinning | unpinned actions | Pin to v-tag or 40-char SHA in workflow file |
| cyclonedx range | version mismatch | Verify validator accepts both 1.5 and 1.6 |

## Known gotchas

- **Tag delete doesn't unfire workflows**: pushing a tag immediately dispatches release.yml; deleting the tag does NOT cancel in-flight runs. Must use `gh run cancel` first.
- **"No checks reported"**: if release.yml never starts, push an empty commit to re-trigger: `git commit --allow-empty -m "chore: re-trigger CI" && git push`
- **Partial publish**: release.yml may publish assets to GitHub before failing downstream (cosign, SBOM). Check `gh release list` and `gh release delete` orphans.
- **Cosmetic failures acceptable**: provenance / SLSA aggregator / changelog-PR steps may show red without blocking binary publish. Log in spec-cost-calibration.md, do not retry tag.

## When to run

Always run immediately before:
- `git push origin <version-tag>`
- After Phoenix runs `git tag -a <version>` but before push

Run after merge of release PR, before tag push.
