# CI Action Pinning Policy

ventd enforces three invariants on every `uses:` reference in
`.github/workflows/*.yml`. The checker runs in the `actionpincheck` CI job
on every PR and push to main.

## Rules at a glance

| Rule | Requirement |
|------|-------------|
| RULE-CI-01 | `@ref` must be a 40-char SHA or a `v`-prefixed tag. No branch names, no short SHAs. |
| RULE-CI-02 | A 40-char SHA must be followed by `# v<major>.<minor>.<patch>` on the same line. |
| RULE-CI-03 | Only `actions/*`, `github/*`, and `docker/*` may use v-tag pins. All others must SHA-pin. |

## Correct examples

```yaml
# SHA pin with full semver comment — satisfies all three rules
- uses: goreleaser/goreleaser-action@e24998b8b67b290c2fa8b7c14fcfa7de2c5c9b8c # v7.1.0

# First-party v-tag — allowed by RULE-CI-03 allowlist
- uses: actions/checkout@v4

# Reusable workflow with SHA
- uses: org/repo/.github/workflows/wf.yml@a1b2c3d4e5f6789012345678901234567890abcd # v1.2.3
```

## Incorrect examples

```yaml
# Branch name — RULE-CI-01
- uses: some/action@main

# Short SHA — RULE-CI-01
- uses: some/action@abc1234

# SHA without version comment — RULE-CI-02
- uses: some/action@a1b2c3d4e5f6789012345678901234567890abcd

# SHA with major-only comment — RULE-CI-02 (needs patch level: # v4.x.y)
- uses: some/action@a1b2c3d4e5f6789012345678901234567890abcd # v4

# Third-party v-tag without SHA — RULE-CI-03
- uses: taiki-e/install-action@v2
```

## How to pin a new action

1. Find the SHA for the target tag:
   ```bash
   gh api repos/owner/repo/git/ref/tags/v1.2.3 | jq -r '.object.sha'
   # For annotated tags, dereference the tag object:
   gh api repos/owner/repo/git/tags/<object-sha> | jq -r '.object.sha'
   ```
2. Write: `uses: owner/repo@<sha> # v1.2.3`

## How to update a pinned action

1. Find the new release SHA:
   ```bash
   gh api repos/owner/repo/git/ref/tags/v1.3.0 | jq -r '.object.sha'
   ```
2. Replace the old SHA and version comment.

## Checker invocation

```bash
go run ./tools/actionpincheck .github/workflows/*.yml
```

Exit 0 = clean. Exit 1 = violations on stderr as `file:line: RULE-CI-NN: ...`.

## Allowlist changes

The allowlist (`actions`, `github`, `docker`) is baked into
`tools/actionpincheck/main.go`. To expand it, edit `allowlistedOwners` and
add a test case in `TestActionPinCheck_AllowlistBoundary`. Expansions require
explicit agreement — SHA pinning is the default.
