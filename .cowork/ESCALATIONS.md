# Escalations

Cowork appends an entry here whenever an escalation trigger fires per
masterplan §12. The developer resolves each entry with one of:

    RESUME <task-id>
    DROP <task-id>
    REWRITE <task-id> <new-allowlist>

or by taking the recommended action directly. Cowork may also resolve
an escalation autonomously when a corrective path is within its
authority (e.g. rebasing a PR to retry a flaky CI lane).

---

## 2026-04-17T23:00:00Z PHASE-0/FIRST-PR — P0-02
Resolved 2026-04-17T23:10:00Z via `RESUME P0-02`; merged as 4aa6a37.

## 2026-04-17T23:30:00Z CI-FLAKE-CHECK — P0-03 ubuntu-arm64
Resolved 2026-04-17T23:45:00Z via developer lane rerun; merged as c08d9b3.

## 2026-04-18T00:10:00Z PHASE-T0/FIRST-PR — T0-INFRA-01
Resolved 2026-04-18T00:25:00Z via developer directive: Cowork fixed
residual doc line directly on branch; merged as e0dcef6. §14
small-fix carve-out recorded and later broadened.

---

## 2026-04-18T00:45:00Z CI-FLAKE-CHECK — T0-META-03 build-and-test-fedora
PR: https://github.com/ventd/ventd/pull/240
Diff: `.github/pull_request_template.md`, +1 line (markdown only).
Failed lane: `build-and-test-fedora`.
Impossibility: `go build` / `go test` do not read markdown; the PR
template is not in any Go package. No code path distinguishes pre-
from post-diff trees under this lane.

**Status 2026-04-18T00:55:00Z: retrying via rebase (Cowork autonomous).**
Rebased #240 onto current main (037e1c0) via update_pull_request_branch;
CI re-triggering on the new head. Corroborating evidence strengthens
the flake hypothesis: PR #241 (T0-INFRA-02) ran the identical Fedora
pipeline minutes later and passed green. If the rebased run also fails
Fedora, escalate properly with log retrieval — but that would imply a
real Fedora-specific regression in the tree that coincidentally
manifests on runs with arbitrary diffs, which seems unlikely.

Resolved: merged as 8646e05 (T0-META-03 entry in state.yaml).

---

## 2026-04-18T (session-resume) PHASE-1/FIRST-PR — P1-HAL-01
PR: https://github.com/ventd/ventd/pull/247
Head: c6726f93e3bd4a3de5bd29ee3f0fa5ed9950a33e
CI: 13/13 green.
Review: .cowork/reviews/P1-HAL-01-R1.md

Reason: Phase 1 begins with this PR; per masterplan §6 + Cowork
SYSTEM prompt, the first PR of any new phase does not auto-merge.
Separately, the PR body flags a behavioural deviation: lazy
manual-mode acquisition inside `hal/hwmon.Backend.Write` replaces
the pre-refactor fail-fast `pwm_enable=1` write at `controller.Run`
startup. Acquire failures no longer return a fatal error; they
surface as a logged tick-level Write error. The diff preserves every
observable log line, all byte-level write semantics, and every
watchdog restore path. The only behaviour change is the fail-fast
→ fail-loud-and-log transition for a broken `pwm_enable` file.
Cowork read: acceptable — a systemd restart loop would hit the same
failure. Developer sign-off requested before merge.

Recommended action: RESUME P1-HAL-01. Cowork will mark ready-for-review
and squash-merge once resumed.

Resolved: _(awaiting developer)_

---

## 2026-04-18T (session-resume) PHASE-1/FIRST-PR + PLAN-INTERPRETATION — P1-FP-01
PR: https://github.com/ventd/ventd/pull/246
Head: 91e18b5dc5fa586d853024b0e352c03af71f49c9
CI: 13/13 green.
Review: .cowork/reviews/P1-FP-01-R1.md

Reason: Phase 1 first-PR gate (same rule as P1-HAL-01). Compounded
by a plan-interpretation ambiguity: masterplan §8 P1-FP-01 DoD says
"old map deleted", but `knownDriverNeeds` is consumed by
`internal/hwmon/dmi.go` and `internal/hwmon/install.go`, both outside
the task's allowlist. CC left the map whole and ported only its
DMI-trigger data into the new hwdb, which is defensible under the
allowlist constraint. Masterplan §14 forbids Cowork from
reinterpreting the plan, so this escalates.

Recommended action: RESUME P1-FP-01 as partial. The allowlist
constraint is the stronger signal; retroactively widening allowlists
to chase DoD items sets a bad precedent. Track `knownDriverNeeds`
retirement as a separate new task whose allowlist explicitly includes
`internal/hwmon/dmi.go` and `internal/hwmon/install.go`.

Alternative: REWRITE P1-FP-01 with widened allowlist
`internal/hwdb/**, internal/hwmon/autoload.go, internal/hwmon/dmi.go,
internal/hwmon/install.go, CHANGELOG.md` and re-dispatch. Slower; not
recommended.

Resolved: _(awaiting developer)_

---

## 2026-04-18T (session-resume) LINT-REGRESSION — T0-INFRA-03
PR: https://github.com/ventd/ventd/pull/245
Head: 7324b2d692a6b15987965e8001affd89cf461e04
CI: 12/13 green, golangci-lint FAIL.

Reason: The previous session's checkpoint claimed commit 7324b2d
cleared lint ("removed unused `fired atomic.Int32` from
TestConcurrentAdvanceAndNewTimer"). Remote CI contradicts that claim:
golangci-lint still fails on 7324b2d.

Cowork diagnostic: `Clock.t *testing.T` field in
`internal/testfixture/faketime/faketime.go` is dead storage (assigned
in New, never read — the cleanup closure captures `t` directly).
Likely `unused`/`structcheck` trip. Removing the field should clear
the lint. Cowork cannot retrieve CI job logs via current MCP tooling
to confirm which linter fired.

Action taken: revision prompt written to
`.cowork/prompts/T0-INFRA-03-revise.md`. Cowork does not edit code.
Revision task dispatched to a new CC session (Sonnet 4.6, same model
as the original task).

Resolved: _(pending CC revision + CI re-run)_
