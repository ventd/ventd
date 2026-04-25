# CI Action Pinning Invariants

These rules enforce supply-chain hygiene in `.github/workflows/*.yml`. An
unpinned or corrupt action ref silently succeeds at parse time while allowing
an untrusted commit to run arbitrary code in the CI environment.

Each rule below is bound to test functions and subtests in
`tools/actionpincheck/main_test.go`. If a rule text is edited, update the
corresponding test in the same PR; if a new rule lands, it must ship with a
matching test or `tools/rulelint` blocks the merge.

## RULE-CI-01: Every action ref must be a 40-char SHA or a version tag starting with `v`

Every `uses: <owner>/<repo>@<ref>` line in `.github/workflows/*.yml` must
pin `<ref>` to either a 40-character lowercase hex SHA or a version tag
whose first character is `v`. Branch names (`main`, `master`) are rejected.
Short SHAs (fewer than 40 chars) are rejected. A branch pin silently tracks
the branch HEAD, admitting any future commit without review; a short SHA is
ambiguous under some pack states and may resolve differently on different
runners.

Bound: tools/actionpincheck/main_test.go:TestActionPinCheck_RefFormat
Bound: tools/actionpincheck/main_test.go:matrix_template_skipped
Bound: tools/actionpincheck/main_test.go:local_action_skipped
Bound: tools/actionpincheck/main_test.go:uses_inside_run_block_skipped
Bound: tools/actionpincheck/main_test.go:multiple_violations_counted

## RULE-CI-02: A 40-char SHA pin must carry a trailing version comment

Every `uses: <owner>/<repo>@<sha>` line where `<sha>` is a 40-char hex SHA
must be followed on the same line by a trailing comment that matches:

  Trailing comment MUST match: # v\d+\.\d+\.\d+(-\S+)?(\s.*)?

Example: `uses: actions/checkout@a1b2c3...40 # v6.0.2`

A patch-level semver in the comment makes pin-bump diffs reviewable —
reviewers see the intended version change, not just SHA shuffling. A missing
comment or a major-only comment (`# v6`) does not satisfy this rule.

Bound: tools/actionpincheck/main_test.go:TestActionPinCheck_SHAHasVersionComment
Bound: tools/actionpincheck/main_test.go:reusable_workflow_with_path_and_sha

## RULE-CI-03: Non-allowlisted owners must pin to a 40-char SHA

Owners in the allowlist (`actions`, `github`, `docker`) may use a `v<N>` or
`v<N>.<M>` tag pin without a SHA. All other owners must pin to a 40-char
SHA. First-party GitHub, Docker, and `actions/*` owners have organisational
provenance and a public incident-response process; tag mutation events would
have immediate public visibility. Third-party repos (`taiki-e`, `goreleaser`,
`peter-evans`, `sigstore`, etc.) must SHA-pin to prevent a tag force-push
from silently changing what runs in CI.

Bound: tools/actionpincheck/main_test.go:TestActionPinCheck_AllowlistBoundary
