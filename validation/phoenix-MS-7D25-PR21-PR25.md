# phoenix-MS-7D25 — PR #21 + PR #25 rig verification

This document captures the rig-side outcome of the daemon-hardening checklists
attached to PRs #21 (chip-agnostic install path, ResolveHwmonPaths) and
#25 (sd_notify watchdog, ventd-recover.service, ZeroPWMSentinel, 10s rescan,
SELinux + AppArmor policies).

The runbook driving every check below is `validation/run-rig-checks.sh`.
Run it on the rig with:

```bash
sudo bash validation/run-rig-checks.sh
```

The script writes a transcript to `validation/results/rig-check-<TS>.log` and
prints a PASS/FAIL/SKIP/MANUAL summary to stdout. Copy that summary into the
**Results** tables below; attach the log file to the PR.

---

## Test rig

| Field            | Value |
|------------------|-------|
| Board            | MSI MAG Z790 |
| CPU              | Intel i9-13900K |
| GPU              | NVIDIA RTX 4090 |
| Super I/O driver | nct6687d (out-of-tree) |
| Tested by        | _____________ |
| Date             | _____________ |
| ventd commit     | _____________ |
| Kernel           | _____________ |
| Distro           | _____________ |

---

## Results

### PR #21 — install path + chip-agnostic udev

| ID    | Check                                     | Result | Notes |
|-------|-------------------------------------------|--------|-------|
| 0a.i  | Udev rule present and chip-agnostic       |        |       |
| 0a.ii | `--probe-modules` persists                |        |       |
| 0a.iii| `EnrichChipName` populates config         |        |       |
| 0a.iv | hwmonN renumber survival                  |        |       |
| 0a.v  | Reboot survival                           |        |       |

### PR #25 — watchdog, recovery, calibration safety

| ID     | Check                                    | Result | Notes |
|--------|------------------------------------------|--------|-------|
| 0b.i   | kill -KILL triggers ventd-recover ≤ 2s   |        |       |
| 0b.ii  | sd_notify watchdog active                |        |       |
| 0b.iii | Hung main loop triggers restart          |        | (deferred — covered by `internal/sdnotify` unit tests) |
| 0b.iv  | Calibration zero-PWM ceiling ≤ 2.2s      |        |       |
| 0b.v   | New fan detected within 10s              |        |       |
| 0b.vi  | rmmod;modprobe detected within 10s       |        |       |
| 0b.vii | AppArmor profile clean                   |        | (only on AppArmor distros) |
| 0b.viii| SELinux module clean                     |        | (only on SELinux distros) |

Allowed values: **PASS**, **FAIL**, **SKIP** (environmental, e.g. no
AppArmor on Fedora), **MANUAL** (operator must follow up — usually because
the script paused for an interactive step).

---

## Issues found

For each FAIL, paste the relevant log excerpt and a one-line reproducer.
Open a GitHub issue per failure and link it here.

```
(empty until a check fails)
```

---

## Conclusion

One of:

- **PASS** — every check returned PASS or SKIP-with-reason; no FAIL or MANUAL
  outstanding. v0.2.0 release is unblocked.
- **PASS-WITH-ISSUES** — non-P0 FAILs filed as issues; release proceeds with
  a known-issues entry in CHANGELOG.
- **FAIL** — any of `0a.i`, `0a.iv`, or `0b.i` failed. These are P0 (the
  README claims fail without them). Block the release until fixed.

Conclusion: _____________

---

## Re-run procedure

The harness is idempotent and safe to re-run after each fix. Suggested
workflow when iterating:

1. Run the full script: `sudo bash validation/run-rig-checks.sh`.
2. Open issues for any FAIL items.
3. Land the fix on a feature branch.
4. Re-run the script; copy the new summary into this table.
5. When all FAIL → PASS, update the conclusion above and commit this file.
