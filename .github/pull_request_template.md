## Summary

<!-- One paragraph. What and why. -->

## Changes

<!-- Bulleted list of concrete changes. -->

## Issue

<!-- Closes #NN  /  Refs #NN  -->

## Checklist

- [ ] Tests added or updated
- [ ] For each issue referenced above (`Closes` / `Fixes` / `Resolves`), a regression test `TestRegression_Issue<N>_*` is present in the diff or already in the tree — or the issue carries the `no-regression-test` label with an explanation on the issue
- [ ] `go vet ./...` and `go test -race ./...` pass
- [ ] `golangci-lint run` passes (once `.golangci.yml` lands)
- [ ] Coverage not regressed
- [ ] Rules followed: `.claude/rules/{attribution,collaboration,hwmon-safety,web-ui,usability,go-conventions}.md`
- [ ] `CHANGELOG.md` updated (unless `docs:` or `ci:` only)
- [ ] `release-notes/v0.3.0-plan.md` updated if scope changed
- [ ] Validation log attached if rig-touching (`validation/phoenix-MS-7D25-*.md`)
