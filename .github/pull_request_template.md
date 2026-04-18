<!--
PR template for ventd/ventd.

Atlas + Cassidy both read this. Blanks that get filled reduce review time;
"N/A" / "none" are valid answers where applicable.
-->

## Summary

<!-- One paragraph. What changed, why. Link the issue number(s) fixed. -->

Fixes #

## Risk class

<!-- Choose one:
- `routine` : non-safety path, standard change
- `hardening` : defensive change, no safety-posture shift (e.g. additional fsync, extra validation)
- `safety-critical` : touches internal/controller/, internal/watchdog/, internal/calibrate/, internal/hal/*/, OR knowingly changes behaviour that could affect fan control / thermal safety (regardless of path)

Safety-critical PRs trigger Atlas's (B) gate: opened as DRAFT, Cassidy audits within 24h, Atlas promotes + merges at T+24h if no blockers filed.
-->

Risk class: 

## Verification

<!-- How you confirmed the change works. Required at minimum:
- `go build ./...` output (or state it was clean)
- `go test -race -count=1 ./<affected packages>` tail
- `gofmt -l` + `go vet` clean
Optional but welcome:
- Benchmarks, flamegraphs, real-hardware validation logs
-->

## Concerns

<!-- Anything that worried you during the change. Examples:
- "This widens the Controller struct by one field; Cassidy may want to audit lifecycle."
- "The regression test required a new test seam in ReadFanMaxRPM."
- "I picked option (a) over (b); alternative was cleaner but needed wider refactor."

If you have zero concerns, write "None" — that's a valid answer.
-->

## Deviations

<!-- Where the implementation diverged from the prompt / issue / masterplan.
Examples:
- "Prompt said 50ms retry; code was already at 100ms from #263, kept 100ms."
- "Added a second test not in the prompt because the first didn't exercise panic-button reset."

If none, write "None".
-->

## Branch cleanliness

<!-- Paste these two outputs:
- `git log --oneline origin/main..HEAD` (commits in this PR)
- `git diff --stat origin/main..HEAD | tail -1` (file count + line delta)
-->

<!--
Hygiene:
- Single commit preferred unless the change is genuinely layered (refactor + feature).
- CHANGELOG entry under `## [Unreleased]` for any user-facing / operator-facing change.
- Issue label `role:atlas` gets removed automatically when Atlas merges.
- Draft → ready promotion: Atlas does this for safety-critical PRs after (B) window closes; for routine PRs, CC may promote directly.
-->
