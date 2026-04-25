# ventd Release Validation

One-page reference for the ventd release-validate script. Use this immediately before pushing a release tag.

## What it does

The release-validate script performs comprehensive release pipeline checks before tag push. It validates:
- Preflight checks pass (remote, branch, tree, spec, rulelint)
- Target tag doesn't already exist
- No orphan release on GitHub
- Cosign uses correct `--bundle=` format
- All GitHub Actions are SHA-pinned
- CycloneDX version range supports both 1.5 and 1.6

## When to run

Run immediately before any of these:
- Pushing a release tag: `git push origin <version-tag>`
- After merging a release PR, before tag push
- Before any `git tag -a` command for a release

## Sample output (passing run)

```
ventd release validate — target=v0.5.0
============================================================
Running preflight with spec_id=spec-03...
ventd preflight — spec=spec-03
...
Recent release.yml runs:
  (error or none)

Summary:
  preflight: ✓
  tag_collision: ✓
  orphan_release: ✓
  cosign_bundle: ✓
  sha_pinning: ✓
  cyclonedx: ✓
============================================================
```

## Exit codes

| Code | Meaning | Action |
|------|---------|--------|
| 0 | All checks passed | Safe to push tag |
| 1 | FAIL checks exist | Fix issues before pushing tag |
| 2 | WARN checks only | Advisory; safe to push |

## Exit code 1 failures (do not push tag)

| Check | Problem | Fix |
|-------|---------|-----|
| preflight | Fails (see preflight.md) | Fix state before release |
| tag_collision | Tag already exists | Choose a new version, delete old tag if needed |
| orphan_release | Incomplete release on GitHub | `gh release delete <version>` before re-tagging |
| cosign_bundle | Not using `--bundle=` form | Fix `.github/workflows/release.yml` cosign invocation |
| sha_pinning | Unpinned actions in release.yml | Pin all action refs to v-tags or 40-char SHAs |

## Exit code 2 warnings (advisory)

| Check | Problem | Action |
|-------|---------|--------|
| cyclonedx | Version range may not support both 1.5+1.6 | Verify validator in release.yml accepts both versions |

## Usage

```bash
.claude/scripts/release-validate.sh <version-tag>
```

Example:
```bash
.claude/scripts/release-validate.sh v0.5.0
```

## Known gotchas (from v0.3.1, v0.4.0, v0.4.1 releases)

**Tag delete does not unfire workflows**: pushing a tag immediately dispatches `release.yml`. Deleting the tag afterwards does NOT cancel in-flight runs. If you need to cancel:
```bash
gh run cancel <run-id>
git tag -d v<X.Y.Z>
git push origin :refs/tags/v<X.Y.Z>
```

**"No checks reported"**: if `release.yml` never starts after tag push, the CI system may not have triggered it. Push an empty commit to re-trigger:
```bash
git commit --allow-empty -m "chore: re-trigger CI"
git push
```

**Partial publish**: `release.yml` may publish assets to GitHub (binaries, checksums) before failing downstream (cosign, SBOM). Always check:
```bash
gh release view v<X.Y.Z>
```
If partially published, `gh release delete` the orphan and retry the tag.

**Cosmetic failures acceptable**: In the release run, these may show red without blocking binary publish:
- Provenance attestation
- SLSA aggregator integration
- Changelog PR auto-filing

Log these cosmetic failures in `docs/claude/spec-cost-calibration.md` and do NOT retry the tag. The release is complete when binaries are published.

## Release workflow integration

This script is the final validation gate before Phoenix executes the tag push. Always run on the same machine where you'll run `git push origin <tag>`, as environment differences (gh auth, git config) can cause unexpected failures.
