# Spec 08 — CI action pinning via rulelint pattern

**Masterplan IDs this covers:** CI supply-chain hygiene. Extends the `.claude/rules/` invariant-binding pattern to `.github/workflows/*.yml` third-party action references.
**Target release:** v0.4.1 (co-ships with spec-06).
**Estimated session cost:** Sonnet, 1 session, $5–8. No Opus required. No HIL — pure Go tool + YAML walker.
**Dependencies already green:** `tools/rulelint` pattern established; `meta-lint.yml` CI job exists.

---

## Why this ships v0.4.1

v0.4.0 release cycle had two action-pinning failures:

1. **`cosign --output-signature` flags ignored under `--new-bundle-format` default** — not pin-related itself, but surfaced because the action version that silently changed default flag behaviour was pinned to a SHA that had since been force-pushed or the wrapper script was pasted from a stale source.
2. **Pinned SHAs rot from paste corruption** — `v7.0.11 create-pull-request=22a9089` etc. had tail-truncated SHAs that never resolved. Workflows silently didn't dispatch. Lost ~90 minutes on release day chasing "why is this workflow not firing."

Meta-lesson: SHA-pinning is only valuable if the SHA is *correct*. A corrupt SHA is strictly worse than an unpinned action — the former silently fails, the latter runs the latest.

Project convention: machine-enforced invariants live in `.claude/rules/*.md` + `tools/rulelint`. Action-pinning policy belongs in the same shape.

## Scope — what this session produces

One PR. Three artefacts.

**Files (new):**
- `.claude/rules/ci-action-pinning.md` — RULE-CI-01..03 with Bound: lines pointing to rulelint-style checks
- `tools/actionpincheck/main.go` — walker over `.github/workflows/*.yml`, enforces the rules
- `tools/actionpincheck/main_test.go` — golden-input fixtures for each rule
- `docs/ci-action-pinning.md` — policy reference for contributors

**Files (modified):**
- `.github/workflows/meta-lint.yml` — add `actionpincheck` as a new step after rulelint
- `tools/rulelint/rules_test.go` (or wherever rulelint's rule-discovery runs) — add `ci-action-pinning.md` to the rulelint-aware rule file list (only needed if rulelint has a hardcoded list; most likely it globs `.claude/rules/*.md`)
- `CHANGELOG.md` — v0.4.1 entry

**Out of scope for this PR but enabled by it:**
- Fixing pinning of existing workflows — separate follow-up PR once the checker is green against an audit list.
- Auto-updating pins via Dependabot — not chosen (manual bumps decided 2026-04-25 per user preference).

## Invariant bindings (`.claude/rules/ci-action-pinning.md`)

1. **RULE-CI-01** — Every `uses: <owner>/<repo>@<ref>` line in `.github/workflows/*.yml` MUST pin `<ref>` to either a 40-char SHA or a version tag starting with `v`. Branch names (`main`, `master`) reject. Short SHAs reject. **Rationale:** pin-to-SHA is the strong policy; pin-to-v-tag is acceptable-with-tradeoff for first-party and widely-used third-party actions (actions/checkout, actions/setup-go). **Binds to:** `TestActionPinCheck_RefFormat`.

2. **RULE-CI-02** — Every `uses: <owner>/<repo>@<sha>` that pins to a 40-char SHA MUST have a trailing comment of the form `# v<semver>` naming the human-readable version the SHA resolves to. Example: `uses: actions/checkout@a1b2c3d4... # v4.1.7`. **Rationale:** makes pin-bump PRs reviewable; diffs show version intent, not just SHA changes. **Binds to:** `TestActionPinCheck_SHAHasVersionComment`.

3. **RULE-CI-03** — An allowlist of first-party actions MAY use `v<major>` or `v<major>.<minor>` tag pins without SHA: `actions/*`, `github/*`, `docker/*`. All other actions MUST pin to SHA. **Rationale:** first-party actions have organisational provenance and tag-republishing has clearer incident response than for random third-party repos. **Binds to:** `TestActionPinCheck_AllowlistBoundary`.

## Design of `tools/actionpincheck/main.go`

**Pattern:** mirror `tools/rulelint` — YAML walker, exits with stderr diagnostics + exit code, runs in CI as a single shell command.

**Core walk:**

```go
// Pseudocode only — CC writes the actual Go.
for each file in filepath.Glob(".github/workflows/*.yml"):
    parse YAML, find all "uses:" string values
    for each uses value:
        split on "@" → (repo, ref)
        validate ref per RULE-CI-01 (40-char SHA or v-tag)
        if 40-char SHA: check preceding-line/trailing comment has "# v<semver>" (RULE-CI-02)
        if v-tag without SHA: check repo is in allowlist (RULE-CI-03)
        on violation: print file:line, violated rule ID, exit 1
```

**Dependencies:** stdlib only. `gopkg.in/yaml.v3` if already in go.mod (likely yes); else text-scanner approach. CC should grep go.mod first.

**Size target:** <200 LOC for main.go, <150 LOC for test. Anything larger is over-engineered.

**Test fixtures:**
- Valid pin to SHA with version comment — passes all rules.
- Valid pin to v-tag for first-party action — passes.
- Invalid: pin to `@main` — RULE-CI-01 trips.
- Invalid: pin to `@v4` on a third-party action (not in allowlist) — RULE-CI-03 trips.
- Invalid: pin to 40-char SHA without trailing `# v...` comment — RULE-CI-02 trips.
- Invalid: short SHA — RULE-CI-01 trips.
- Edge: `uses:` inside a `|` block-scalar comment — parser must skip.
- Edge: matrix-generated `uses:` with `${{ matrix.action }}` — must not false-positive (parser skips templated values).

## Definition of done

- [ ] `go test -race ./tools/actionpincheck/...` passes.
- [ ] `go run ./tools/actionpincheck .github/workflows/*.yml` runs clean against a known-good fixture; runs dirty with clear diagnostic against known-bad fixtures.
- [ ] `.github/workflows/meta-lint.yml` has `actionpincheck` step after `rulelint` step; CI job green.
- [ ] `.claude/rules/ci-action-pinning.md` has RULE-CI-01..03 each with `Bound:` line.
- [ ] `tools/rulelint` run locally shows no orphans, no missing bindings.
- [ ] `docs/ci-action-pinning.md` present; contributor policy documented.
- [ ] **Explicitly NOT in DoD:** fixing existing pin violations in `.github/workflows/*.yml`. This PR adds the checker. Fixing existing violations is a separate follow-up PR opened same day as this merges (CC handles it as a rapid second session, likely $2–4).

## Explicit non-goals

- No Dependabot integration. Manual bumps are the policy (2026-04-25 decision).
- No runtime action-signature verification (cosign on actions). Out of project scope; v1.0+ supply-chain territory.
- No extension to action-content scanning (e.g., Rekor log lookup for SHA provenance). Too much for a point release.
- No auto-fix mode (`--fix`). Checker is read-only; human updates pins.
- No separate `.actionpinignore` file. Allowlist lives in RULE-CI-03, changes via rule edit.

## Red flags — stop and page me

- CC wants to auto-resolve v-tag to SHA and fail if user pinned to a non-current SHA → out of scope; that's an updater, not a checker.
- CC proposes pulling the allowlist from an external source (e.g., GitHub Marketplace verified list) → no network, bake allowlist into rule file.
- Tool grows past 400 LOC → over-engineered, constrain.
- CC wants to extend to `.gitlab-ci.yml`, Azure Pipelines, etc. → GitHub Actions only for v0.4.1.
- False positives on `${{ matrix.* }}` templated values → parser must skip these explicitly, not just work-by-coincidence.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-08-ci-action-pinning.md end to end. Then read:
- tools/rulelint/main.go (or wherever rulelint lives — the pattern to mirror)
- .claude/rules/hwmon-safety.md (invariant file format)
- .github/workflows/meta-lint.yml (the CI job to extend)
- .github/workflows/release.yml (an action-heavy workflow to test against)

Single PR. Create:
1. .claude/rules/ci-action-pinning.md with RULE-CI-01..03 each bound to a
   subtest name.
2. tools/actionpincheck/main.go — YAML walker, <200 LOC target, stdlib +
   yaml.v3 only (confirm yaml.v3 already in go.mod; if not, ask before adding).
3. tools/actionpincheck/main_test.go — fixtures for pass and fail cases of
   each rule, plus edge cases (matrix templating, block scalars).
4. docs/ci-action-pinning.md — policy reference.
5. Extend .github/workflows/meta-lint.yml to run actionpincheck after rulelint.

Commit at boundaries:
- feat(tools/actionpincheck): walker + fixtures for RULE-CI-01..03
- test(tools/actionpincheck): edge cases for templating and block scalars
- docs(rules): add ci-action-pinning.md with bound invariants
- ci(meta-lint): run actionpincheck in the meta-lint job
- docs(ci): contributor policy for action pinning

After every edit: go test -race ./tools/actionpincheck/...

Success condition:
  cd ~/ventd
  go test -race ./tools/actionpincheck/...
  go run ./tools/actionpincheck .github/workflows/*.yml
  # The command may exit non-zero if existing workflows have pin violations.
  # That is expected — fixing them is a follow-up PR, not this PR's scope.
  # Confirm it exits non-zero with clear diagnostics naming file:line + RULE-CI-<N>.
  go run ./tools/rulelint
  # Zero orphans, zero missing bindings.

Stop and surface if:
- Tool grows past 250 LOC — reconsider design before continuing.
- Parsing .github/workflows/*.yml needs a YAML lib not already in go.mod.
- Fixing existing pin violations "while I'm here" tempts — that's a follow-up PR.
- Rulelint discovery of the new rule file doesn't work out of the box —
  check if rulelint has a hardcoded rule list (if yes, extend it).
```

## Why this is cheap

- Pure Go, no hardware, no network.
- YAML walking is a solved problem (`yaml.v3`).
- Rule format proven by `hwmon-safety.md`, `ipmi-safety.md`, etc.
- Rulelint pattern proven by `tools/rulelint`.
- Test fixtures are small YAML files; <50 LOC each.
- No dependency added if `yaml.v3` already present (almost certainly yes given ventd's config YAML parsing).

## Follow-up PR (same-day, not this spec)

After this spec lands green, open a second CC session (Sonnet, $2–4) to:
- Run `go run ./tools/actionpincheck .github/workflows/*.yml` and produce a diff.
- Fix each violation by looking up the correct SHA for each action's tagged release.
- Add trailing `# v<semver>` comments.
- Single commit: `ci: pin third-party actions to SHA with version comments`.

Keep that PR separate so the checker merges on its own merit without entangling with pin-bumping work.
