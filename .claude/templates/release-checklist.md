# Release Checklist

Sequenced steps for cutting a ventd release. Walk top-to-bottom, do not skip.
Encodes lessons from v0.3.1, v0.4.0, v0.4.1.

## Pre-release (run before release PR)

- [ ] All milestone PRs merged: `gh pr list --state merged --search "milestone:vX.Y.Z"`
- [ ] No open PRs blocking: `gh pr list --state open --search "milestone:vX.Y.Z"`
- [ ] `main` is green: `gh run list --branch main --limit 5`
- [ ] Working tree clean on local main: `git status` and `git log -1 --oneline` matches `origin/main`
- [ ] Tag does not exist: `git tag --sort=-v:refname | head -5` does NOT include target version
- [ ] No orphan release: `gh release list` does NOT include target version

## Release PR (CC handles via cc-prompt-template-release.md)

- [ ] CC opens `release/vX.Y.Z` branch
- [ ] CHANGELOG entry follows last 3 entries' format
- [ ] PR body = CHANGELOG entry
- [ ] CI green on PR
- [ ] Squash merge: `gh pr merge --squash --delete-branch`

## Post-merge (Phoenix manually, CC does NOT push tags)

```bash
git checkout main
git pull --ff-only
git log -1 --oneline                          # confirm squash commit on main
git tag --sort=-v:refname | head -5           # one final collision check
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

## Watch the pipeline

```bash
gh run watch                                   # release.yml triggered by tag push
```

If release.yml fails:
1. **Do NOT delete the tag without first running** `gh run cancel` for any in-flight workflows triggered by the tag push
2. Check `gh release list` for an orphan release (sometimes publishes partially)
3. If orphan: `gh release delete vX.Y.Z`
4. Delete tag locally + remote: `git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`
5. Push empty commit to re-trigger if "No checks reported": `git commit --allow-empty -m "chore: re-trigger CI" && git push`

## Verify publish

- [ ] `gh release view vX.Y.Z` shows assets (binary + checksums + SBOM bundle)
- [ ] SBOM bundle pair count: each binary has `.bundle` and `.cyclonedx.json` companion
- [ ] Cosmetic-failure check: provenance / SLSA aggregator / changelog-PR steps may show red without blocking publish — log them in calibration doc, do not retry tag

## Pipeline gotchas (each cost a release this year)

- **cosign default bundle format change**: must use `--bundle=${artifact}.bundle`, NOT `--output-signature` + `--output-certificate` (silently produced unsigned artifacts in v0.3.1)
- **Pinned action SHA rot**: paste corruption produces SHAs that never resolve; pin to v-tag or verify via `gh api /repos/{owner}/{repo}/commits/{sha}`
- **CycloneDX version drift**: cyclonedx-gomod auto-bumps spec versions (1.5→1.6); release.yml validator must accept both
- **Tag delete does not unfire workflow**: pushing a tag dispatches workflows immediately; deleting the tag does not cancel in-flight runs
- **"No checks reported"**: workflows never dispatched (often after tag delete+repush); empty commit re-triggers

## Post-release hygiene

- [ ] Update `docs/claude/spec-cost-calibration.md` with actual CC spend
- [ ] Close milestone: `gh api repos/:owner/:repo/milestones/N -X PATCH -f state=closed`
- [ ] Open next milestone if needed
- [ ] Delete merged release branch confirmed deleted (squash merge auto-deletes if `--delete-branch` used)
