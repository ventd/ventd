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

Resolved: _(awaiting developer)_
