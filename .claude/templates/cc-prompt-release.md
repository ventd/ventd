# CC Prompt — Release PR

<!-- Template for CC prompts that cut a release: CHANGELOG, version bump, tag.
     Distinct from impl/docs because it touches the release pipeline directly. -->

## Release
- Tag: `v<MAJOR>.<MINOR>.<PATCH>`
- Spec milestone: <e.g. v0.5.0 = spec-03 profile library>
- Branch: `release/v<X.Y.Z>`
- Base: `main` (must be green and have all milestone PRs merged)

## Model & cost
- Model: Haiku (mechanical CHANGELOG transcription + version bump)
- Estimated cost: $0.50–$2
- If CHANGELOG requires synthesis across many PRs → Sonnet, $2–$5

## Target files
**Modify:**
- `CHANGELOG.md` — new section at top under `[Unreleased]`
- `<version-bearing file>` — bump version constant (only if ventd embeds one; verify path before assuming)

**Create:**
- None typically — release artifacts come from CI, not the PR

**Do not touch:**
- Any `*.go` source file outside the version bump
- README (release notes go in CHANGELOG, not README)

## Invariants
- `git tag --sort=-v:refname | head -5` does NOT show the new tag yet (collision check)
- `gh pr list --state open --base main` is empty for milestone label
- All milestone PRs have `state=MERGED` and `mergedAt` populated
- `gh release list` shows no orphan release for the new tag
- CHANGELOG follows existing format (compare against last 3 entries)

## Success condition
1. CHANGELOG.md updated with dated, sectioned entry (Added/Changed/Fixed/Security)
2. CHANGELOG entry references PR numbers, not internal commits
3. Version constant bumped if applicable
4. Branch pushed, PR opened, CI green
5. **Phoenix tags manually after PR merges** — CC does not push tags

## Verification (Phoenix runs)
```bash
.claude/scripts/release-validate.sh v<X.Y.Z>
```
Must pass before tag push. Runs the pipeline-validator skill.

## Tag procedure (Phoenix runs after PR merge)
```bash
git checkout main
git pull --ff-only
git log -1 --oneline                              # confirm squash on main
git tag --sort=-v:refname | head -5               # confirm no collision
git tag -a v<X.Y.Z> -m "Release v<X.Y.Z>"
git push origin v<X.Y.Z>
gh run watch                                      # observe release.yml
gh release view v<X.Y.Z>                          # confirm publish
```

## Out of scope
- Cutting the tag (Phoenix does this, not CC)
- Editing release.yml or any workflow file (separate PR)
- Publishing to package registries (CI handles)

## Failure modes to flag
- CHANGELOG `[Unreleased]` section is empty or sparse → not enough merged work for a release
- Tag collision with existing tag (`git tag` shows it already)
- Open PRs labeled with this milestone still exist
- CI red on `main` at HEAD

## Commit & PR style
- Commit: `chore(release): v<X.Y.Z>`
- PR title: `chore(release): v<X.Y.Z>`
- PR body: copy of the CHANGELOG entry being added
