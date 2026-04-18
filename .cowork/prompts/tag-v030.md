You are Claude Code, working on the ventd repository.

## Task
ID: TAG-V030
Track: RELEASE
Goal: Create the v0.3.0 annotated git tag on main, push it, and create the matching GitHub release with the pre-drafted release notes body.

## Care level
MEDIUM. Tag creation is not reversible from the release-notes perspective (people subscribed to releases will be notified). Triple-check: main tip SHA, tag body content, release notes file. The pre-drafted release notes at `release-notes/v0.3.0.md` are the source of truth — copy them into the GitHub release body verbatim.

## Context

- Main tip (verified): `99689a447cae8a7f3c0cd4c28cc66db290ca0733` (post-release-notes commit).
- Previous tag: `v0.2.0` (2026-04-16).
- Release notes file: `release-notes/v0.3.0.md` at main.
- Phase 1 closed. Ultrareview-1 cleared (blocker fixed in #270).

## What to do

1. Verify the local clone is up to date:
   ```
   git fetch origin main --tags
   git checkout main
   git pull --ff-only origin main
   ```
   Main tip must be `99689a44...`. If it isn't, halt and report.

2. Verify the release notes file exists and is the final version:
   ```
   test -f release-notes/v0.3.0.md
   grep -q "editorial notes" release-notes/v0.3.0.md && echo "DRAFT NOT FINAL" && exit 1
   head -5 release-notes/v0.3.0.md
   ```
   Expected: file exists; no "editorial notes" string (the draft editorial block was stripped); first heading is `# ventd 0.3.0`.

3. Create the annotated tag:
   ```
   git tag -a v0.3.0 -m "ventd 0.3.0 — Phase 1 close

Architectural foundation: HAL, fingerprint-keyed hwdb, hot-loop
optimisation, HAL-driven calibration, Go 1.25.9 security bump.
Full notes: release-notes/v0.3.0.md"
   ```

4. Push the tag:
   ```
   git push origin v0.3.0
   ```

5. Create the GitHub release using `gh` CLI:
   ```
   gh release create v0.3.0 \
     --title "ventd 0.3.0 — Phase 1 close" \
     --notes-file release-notes/v0.3.0.md \
     --latest
   ```
   Do NOT use `--draft`. This is a real release. Do NOT use `--prerelease`.

6. Verify the release was created:
   ```
   gh release view v0.3.0
   ```
   Expected: `tag: v0.3.0`, release body contains the first paragraph of the release notes, `isDraft: false`, `isPrerelease: false`.

7. (Optional, non-blocking) Confirm the artifact release assets (binaries, checksums) were auto-attached by goreleaser if the release workflow triggers on tag push. If goreleaser's workflow isn't configured to fire on tag push, note that as a followup — the release page is valid without assets but less useful.

## Definition of done

- Annotated tag `v0.3.0` exists on main at SHA `99689a44...`.
- Tag pushed to origin.
- GitHub release exists at https://github.com/ventd/ventd/releases/tag/v0.3.0.
- Release body is the contents of `release-notes/v0.3.0.md`.
- Release is marked as "latest" and is NOT a draft or prerelease.
- `gh release view v0.3.0` shows no warnings.

## Out of scope

- Updating the README to advertise v0.3.0 — the Phase 1 work is already in the README's Features section and the "What's coming" section signposts Phase 2 correctly. No README diff is needed for this release.
- Social cross-posts — per RELEASE-PLAN.md this is a quiet release.
- Triggering goreleaser manually if the tag-push workflow doesn't fire — that's a followup for a separate CC session if needed.
- Any code changes.

## Branch and PR

No PR for this task. Tag creation happens directly on main. This is explicitly allowed by RELEASE-PLAN.md for tag operations.

## Constraints

- Files touched: none (tag operation only).
- Do NOT force-push.
- Do NOT create any other tags or branches.
- If `gh release create` fails, delete the tag locally and on origin (`git tag -d v0.3.0 && git push origin :refs/tags/v0.3.0`) before retrying, so there's no dangling tag.

## Reporting

- STATUS: done / failed
- TAG_SHA: output of `git rev-parse v0.3.0`
- RELEASE_URL: output of `gh release view v0.3.0 --json url -q .url`
- RELEASE_ASSETS: list of attached assets (may be empty if goreleaser didn't fire; flag that)
- CONCERNS: anything unexpected
- FOLLOWUPS: especially if goreleaser didn't auto-attach binaries
