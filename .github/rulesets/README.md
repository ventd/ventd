# Repo rulesets

`main.json` is the source-of-truth export of the `main` branch
ruleset (`id 15036827`). It pins the required status checks
produced by the `build` matrix and the standalone AppArmor parse
lane in [`.github/workflows/ci.yml`](../workflows/ci.yml):

- `build-and-test-ubuntu`
- `build-and-test-ubuntu-arm64`
- `build-and-test-fedora`
- `build-and-test-arch`
- `build-and-test-alpine`
- `apparmor-parse-debian13`

## Why this file exists

GitHub branch-protection rulesets pin status checks by their exact
string name. If a matrix row in `ci.yml` is renamed, added, or
removed, the live ruleset keeps requiring the old name — every PR
to `main` then blocks with `mergeStateStatus: BLOCKED` against a
check that no job ever reports. This happened once against #114;
issue #121 tracks the fix.

The workflow's `name: ${{ matrix.name }}` convention decouples the
check name from matrix variables (distro, container), so bumping
`ubuntu-24.04 → ubuntu-24.10` does **not** rename the check. The
only way the check name changes is an intentional edit to
`matrix.name` — and that edit must be paired with a matching edit
here.

## Editing workflow

1. Change a row's `matrix.name` in `ci.yml`, or add/remove a row.
2. Update the corresponding `context` strings under
   `rules[].parameters.required_status_checks` in `main.json`.
3. `jq . .github/rulesets/main.json` — must parse.
4. The set of `matrix.name` values in `ci.yml` must equal the set
   of `context` values in `main.json`. No drift.
5. Ship the ci.yml edit and the JSON edit in the same PR.

## Applying after merge (Phoenix only)

After the PR merges, the live ruleset on GitHub still requires the
old check names until it is re-applied. Apply manually — do not
automate this in CI; a bad push would strip protection from `main`.

Preferred: GitHub UI → Settings → Rules → Rulesets → `main` →
edit the required status checks to match `main.json`.

Alternative (gh CLI, admin PAT required):

```sh
gh api --method PUT repos/ventd/ventd/rulesets/15036827 \
  --input .github/rulesets/main.json
```

The API treats server-managed fields (`id`, `source`, `created_at`,
`updated_at`) as read-only and silently ignores them in the PUT
body, so the full export is safe to submit unedited.

Verify with:

```sh
gh api repos/ventd/ventd/rulesets/15036827 \
  | jq -r '.rules[] | select(.type=="required_status_checks") | .parameters.required_status_checks[].context'
```

Output must match the six check names listed above (four amd64
`build-and-test-*` lanes, the `build-and-test-ubuntu-arm64` native
arm64 lane, and the `apparmor-parse-debian13` parser smoke).

## Scope note

This file is a snapshot for re-applying after a rename. It is
**not** a declarative desired-state controller — nothing in CI
diffs `main.json` against the live ruleset. Drift (e.g. a check
added or removed via the GitHub UI) will go undetected until the
next rename prompts a re-export. Keep edits small and ship them
with the matching workflow change.
