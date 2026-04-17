# Escalations

Cowork appends an entry here whenever an escalation trigger fires per
masterplan §12. The developer resolves each entry with one of:

    RESUME <task-id>
    DROP <task-id>
    REWRITE <task-id> <new-allowlist>

or by taking the recommended action directly (manual merge, rerun
failed CI lane, branch edit, or direct Cowork edit per the §14
 small-fix carve-out below).

Cowork does not resume the relevant track until the entry is cleared.

---

## 2026-04-17T23:00:00Z PHASE-0/FIRST-PR — P0-02 roadmap PR awaiting solo-dev merge
Task: P0-02
PR: https://github.com/ventd/ventd/pull/237
Resolved: 2026-04-17T23:10:00Z — developer `RESUME P0-02`; squash-merged as 4aa6a37.

---

## 2026-04-17T23:30:00Z CI-FLAKE-CHECK — P0-03 build-and-test-ubuntu-arm64 failed on docs-only PR
Task: P0-03
PR: https://github.com/ventd/ventd/pull/238
Resolved: 2026-04-17T23:45:00Z — developer re-ran the failed lane; passed on rerun; squash-merged as c08d9b3.

---

## 2026-04-18T00:10:00Z PHASE-T0/FIRST-PR — T0-INFRA-01 awaiting solo-dev merge (with one residual doc miss)
Task: T0-INFRA-01
PR: https://github.com/ventd/ventd/pull/239

Resolved: 2026-04-18T00:25:00Z — developer instructed Cowork to fix the
residual `microcontroller` doc line directly on the branch (option (c)).
Cowork committed `7d9adef` (`test: correct fakemic New constructor doc
(T0-INFRA-01)`) changing one line in internal/testfixture/fakemic/fakemic.go.
All 13 CI lanes green on 7d9adef. PR marked ready-for-review,
squash-merged as e0dcef649d95f25a0c4d1825d995ace273c69c9f. Branch
auto-deleted.

Two policy updates recorded in the same turn:
  * Masterplan §14 ("Cowork must never ... Edit code.") is softened.
    Direct small-fix edits by Cowork are now explicitly permitted when
    a one- or two-line change is measurably cheaper than issuing a
    fresh CC prompt.
  * PHASE-T0/FIRST-PR gate is retained by default for future categories
    of T-task firsts, modulo per-task judgement calls (see below).

---

## 2026-04-18T00:45:00Z CI-FLAKE-CHECK — T0-META-03 build-and-test-fedora failed on markdown-only PR
Task: T0-META-03
PR: https://github.com/ventd/ventd/pull/240
Branch: claude/META-prtpl-9e8d1
Head SHA: 14c9bb5cc35c7dbdb3b071a032ca57ae95be7417
Authorship: Cowork direct (under §14 broadened efficiency carve-out)
Failed lane: build-and-test-fedora

Evidence:
  * Diff is a single file: `.github/pull_request_template.md`, +1 line
    (new regression-test checkbox).
  * The Fedora lane runs `go build` and `go test ./...`. Neither
    touches markdown files. The PR template is not part of any Go
    package, is not compiled, and is not referenced in any test.
  * No code path under `go build` or `go test` distinguishes the
    pre-diff tree from the post-diff tree. The failure cannot have
    been caused by this diff.
  * 10/13 lanes green, including four other OS build lanes:
    ubuntu-arm64, alpine, cross-compile (linux/amd64, linux/arm64),
    plus apparmor-parse-debian13, golangci-lint, govulncheck,
    shellcheck, nix-drift, headless-chromium. Two lanes still
    in-progress (build-and-test-ubuntu, build-and-test-arch).
  * Same pattern as the P0-03 ubuntu-arm64 flake on 2026-04-17T23:30Z;
    that one also cleared on developer rerun.

Recommended action: rerun `build-and-test-fedora` on the PR; expect
green. If it fails again with the same diff, that would indicate a
Fedora-specific issue in the existing tree that happens to manifest
on this run — escalate separately with the log.

PHASE-T-META/FIRST-PR gate: NOT applied to this task. Rationale
recorded in `state.yaml` policy_updates[4]: single-file, zero-code
template edit is below the novelty threshold for a phase-first gate,
and the developer's parallel-work directive explicitly prioritises
efficiency over ceremony on trivia. The gate is retained as default
for future T-META tasks that ship real tooling (T0-META-01,
T0-META-02).

Resolved: _(pending developer lane rerun)_

---
