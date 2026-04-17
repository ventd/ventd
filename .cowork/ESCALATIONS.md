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
Resolved: merged as 8646e05 (T0-META-03 entry in state.yaml).

---

## 2026-04-18T (session-resume) PHASE-1/FIRST-PR — P1-HAL-01
Resolved: merged as part of #247 in prior session.

---

## 2026-04-18T (session-resume) PHASE-1/FIRST-PR + PLAN-INTERPRETATION — P1-FP-01
Resolved: merged as #246 in prior session.

---

## 2026-04-18T (session-resume) LINT-REGRESSION — T0-INFRA-03
Resolved: merged as #245 in prior session.

---

## 2026-04-18T (clean-slate-resume) INFRA-DOWN — spawn-mcp unusable

Subject: spawn-mcp /tmp/cc-runner permission denied; both dispatches blocked.

Task: wd-safety (T-WD-01) + permpol (P10-PERMPOL-01) dispatches
PR: — (never opened; spawn failed)

Reason: First two `spawn_cc()` calls of the session both returned
`[Errno 13] Permission denied: '/tmp/cc-runner/cc-<alias>-<hex>.md'`.
Filesystem or unit-config regression against the spawn-mcp service.
Pattern matches LESSON #6 class: a systemd directive conflict or a
tmp-cleanup race that wasn't caught because we have no ephemeral smoke
target for the service. Queue is blocked; no CC sessions can spawn.

Root-cause candidates (ordered by probability):

1. `PrivateTmp=yes` crept back into `spawn-mcp.service`. With
   `PrivateTmp=yes` the service sees a private tmpfs mount that does
   not contain `/tmp/cc-runner`. LESSON #6 explicitly called this out
   as architecturally incoherent with a cross-user IPC path under
   /tmp, but the unit may have been redeployed from an earlier state.

2. `/tmp/cc-runner` directory was cleared by tmp cleanup (reboot or
   systemd-tmpfiles) and never recreated. Fix requires
   `RuntimeDirectory=cc-runner` (gives `/run/cc-runner` with correct
   perms, auto-created on start) or `ExecStartPre=/usr/bin/install -d
   -o <svc-user> -g <svc-user> -m 0700 /tmp/cc-runner`.

3. Ownership drift: dir exists but is owned by root after a manual
   touch; service user cannot write.

Recommended action: developer-choice. Suggested diagnostic on
phoenix-desktop:

    ls -lad /tmp/cc-runner
    systemctl cat spawn-mcp.service | grep -E 'PrivateTmp|ReadWritePaths|RuntimeDirectory|User|ExecStartPre'
    journalctl -u spawn-mcp.service -n 50 --no-pager

If #1: set `PrivateTmp=no`; `daemon-reload && systemctl restart spawn-mcp`.
If #2 or #3: add `RuntimeDirectory=cc-runner` to the unit (cleanest),
or an `ExecStartPre=install -d -o <user> -g <user> -m 0700
/tmp/cc-runner` line. Reload + restart.

Queue held until user signals spawn-mcp is back. `wd-safety` and
`permpol` aliases remain valid and ready to re-dispatch on resume.

Resolved: merged #251 (spawn-mcp user collapse) and #252 (print-mode +
session log); spawn-mcp operational. Queue unblocked.

---

## 2026-04-18T07:45:00+10:00 RULE-FILE/REVIEW — T-HAL-01 / PR #258

Subject: New `.claude/rules/hal-contract.md` file introduces a safety
contract that binds every future HAL backend implementation. Per
Cowork auto-merge restrictions, new rule files require explicit
review, not silent auto-merge.

Task: T-HAL-01
PR: https://github.com/ventd/ventd/pull/258

Cowork review (serving as reviewer-of-record per solo-dev mode):

The file declares 8 invariants (RULE-HAL-001 through RULE-HAL-008),
each with a clear statement, a rationale, a documented NVML skip where
applicable, and a `Bound:` line pointing at the subtest that enforces
it.

Per-rule assessment:

- **HAL-001 Enumerate idempotent** — correct; standard collection-read
  contract.
- **HAL-002 Read no-mutation** — correct; phase separation between
  Read and Write is load-bearing for the controller's tick model. The
  NVML skip is an honest environmental constraint.
- **HAL-003 Write faithful (no silent clamping)** — correct; the
  rationale that "controller owns clamping" is the right defense
  against double-clamping by backends.
- **HAL-004 Restore safe on un-opened channels** — correct; both hwmon
  (fallback to PWM=255 when OrigEnable=-1) and NVML (return
  ErrNotAvailable as clean error) satisfy the invariant letter. The
  tracked followup for a `CapStatefulRestore` bit (to distinguish
  restore-to-captured-value from reset-to-auto) is appropriate scope
  for a new P-task, not a blocker here.
- **HAL-005 Caps stable** — correct; UI fan inventory depends on this.
- **HAL-006 Role deterministic** — correct; mirrors 005 for role.
- **HAL-007 Close idempotent** — correct; standard resource cleanup.
- **HAL-008 Write idempotent on acquired channel** — the specific
  claimed mechanism (re-issuing pwm_enable=1 resets the auto-curve
  timer on some firmware) is not directly corroborated by upstream
  hwmon docs; available evidence shows pwm_enable semantics vary
  significantly per-chip. However the invariant itself is sound
  regardless: don't re-issue mode transitions unnecessarily. The
  implementation (sync.Map gate) is free. Defensive practice is
  correct even if the specific mechanism claim is conservative.

CI status: 16/16 green on first pass (build-and-test-{ubuntu, ubuntu-arm64,
fedora, arch, alpine}, apparmor-parse-debian13, golangci-lint,
shellcheck, nix-drift, cross-compile-matrix {amd64, arm64}, rulelint,
regresslint, govulncheck, headless-chromium, status).

Rulelint specifically reports: `ok: 15 rule(s), 15 bound(s) verified`
— every `Bound:` line points at a real subtest and every subtest has a
rule covering it.

Reason: Escalation exists to document the review, not to block it.
Rule-file accept lands here as a record.

Recommended action: RESUME (merge).

Resolved: _(merging now; SHA will be recorded post-merge)_
