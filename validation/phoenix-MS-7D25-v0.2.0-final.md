# phoenix-MS-7D25 — v0.2.0 final rig verification

This document captures the rig-side outcome of the v0.2.0 F2/F3 merge train
against `main` immediately prior to the v0.2.0 tag. Every PR below has been
merged to main; this runbook exercises them end-to-end on the rig.

PRs covered in this run:

- #31 — `fix(config): resolve hwmon symlinks in buildChipMap`
- #42 — `fix(config): disambiguate multi-match chip_name via hwmon_device`
  (replaces closed PR #37; same code, rebased onto post-#31 main)
- #38 — `fix(install): ensure /etc/ventd config ownership is ventd:ventd`
- #32 — `fix(setup): pre-validate generated config before the review screen`
- #33 — `fix(setup): gate case_curve on len(gpuFans) > 0`
- #34 — `docs: NVIDIA GPU fan control setup guide for v0.2.0`

The runbook driving the automated checks below is
`validation/run-rig-checks.sh`. Run it on the rig with:

```bash
sudo bash validation/run-rig-checks.sh
```

The script writes a transcript to `validation/results/rig-check-<TS>.log`
and prints a PASS/FAIL/SKIP/MANUAL summary to stdout. Copy that summary
into the **Results** tables below; attach the log file to the PR.

The v0.2.0-specific checks in sections **V1**, **V2**, and **V3** below
are not driven by the shared harness — they are one-shot manual probes
tied to the three PR clusters that this release is about.

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
| NVIDIA driver    | _____________ |

---

## Results — shared harness

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

---

## Results — v0.2.0 specific

### V1 — config ownership under systemd (PR #38)

Goal: prove that every config mutation originated from the systemd unit
(or from an explicit `sudo ventd ...` invocation) lands on disk as
`ventd:ventd 0600`. The daemon's `writeFileSync` chown-matches the tmp
file to the parent dir's owner before the atomic rename whenever the
writer's euid is 0; non-root writers produce ventd-owned files natively.

| ID    | Check                                                                                                   | Result | Notes |
|-------|---------------------------------------------------------------------------------------------------------|--------|-------|
| V1.i  | Clean state: `stat -c '%U:%G %a' /etc/ventd /etc/ventd/config.yaml` → `ventd:ventd 0750` / `ventd:ventd 0600` |        |       |
| V1.ii | Login via web UI, change any setting, Save → `stat` ownership/mode unchanged                           |        | Driver: service runs as User=ventd; no chown-match path exercised. |
| V1.iii| `sudo /usr/local/bin/ventd --setup --config /tmp/vownership/config.yaml` against a ventd-owned scratch dir → written config is `ventd:ventd 0600` |        | Exercises the euid-0 chown-match path. |
| V1.iv | Stop daemon, manually replace `/etc/ventd/config.yaml` with `sudo tee` (simulating an operator footgun), restart daemon, save from UI → file ends up `ventd:ventd 0600` again |        | Regression guard for the F2 reproducer. |
| V1.v  | `journalctl -u ventd --since "10 min ago"` contains no `permission denied` on config load              |        |       |

### V2 — hwmon resolution on dual-nct6687 (PRs #31 + #42)

Goal: prove that the daemon starts cleanly against a config that names
`chip_name: nct6687` with `hwmon_device: /sys/devices/platform/nct6687.2592`
on a rig where two chips both report `name=nct6687`. Pre-#31 the resolver
saw zero chips; post-#31 it saw both and errored on ambiguity; post-#42 it
picks the configured device path.

Environment preflight — all of these should be true before running V2:

```bash
for h in /sys/class/hwmon/hwmon*; do
  name=$(cat "$h/name" 2>/dev/null)
  dev=$(readlink -f "$h/device")
  echo "$h  name=$name  device=$dev"
done
```

Expected output shows `hwmon5 name=nct6687 device=.../nct6683.2592` and
`hwmon6 name=nct6687 device=.../nct6687.2592` (exact indices may shift
across reboots).

| ID     | Check                                                                                            | Result | Notes |
|--------|--------------------------------------------------------------------------------------------------|--------|-------|
| V2.i   | `sudo systemctl start ventd` → `systemctl is-active ventd` returns `active`                      |        |       |
| V2.ii  | `journalctl -u ventd --since "2 min ago"` contains `config loaded` and no `matches multiple hwmon devices` |        |       |
| V2.iii | `curl -sk https://localhost:9999/api/fans` → each hwmon fan has a non-empty `rpm` field          |        | Proves the daemon is actually reading the resolved paths. |
| V2.iv  | Edit `/etc/ventd/config.yaml` under sudo, blank out `hwmon_device` on one fan, restart → daemon fails with a clear `disambiguate with hwmon_device` error naming both candidate hwmonN entries |        | Regression guard: empty HwmonDevice on an ambiguous chip must still error loudly. |
| V2.v   | Restore the HwmonDevice, restart → daemon active again, no stale errors in journal              |        |       |

### V3 — NVIDIA docs walkthrough (PR #34)

Goal: prove that `docs/nvidia-fan-control.md` is accurate for this rig and
walk-throughable by a non-sysadmin. GPU fan *control* stays out of scope
for this v0.2.0 tag (documented as a known issue, not a code fix).

| ID    | Check                                                                                                    | Result | Notes |
|-------|----------------------------------------------------------------------------------------------------------|--------|-------|
| V3.i  | Render `docs/nvidia-fan-control.md` on GitHub; every link resolves (README link, CHANGELOG link)         |        |       |
| V3.ii | Option A wording does NOT claim the udev rule "grants write access" — it describes the group-membership gate |        | Post-F2 doc correction landed in 0fb7e97 (PR #34, commit 2). |
| V3.iii| Copy-paste Option A commands (`groupadd -f ventd`, `usermod -aG ventd ventd`, `udevadm …`, `systemctl restart ventd`) into a root shell — all parse cleanly, `/dev/nvidiactl` and `/dev/nvidia0` end up owned `root:ventd 0660` |        | Do NOT continue to `nvidia-settings -a GPUFanControlState=1` unless the rig already has an X session. |
| V3.iv | `nvidia-smi -i 0 --query-gpu=temperature.gpu --format=csv,noheader` still works after step V3.iii        |        | Confirms temperature read path unaffected by the group/mode change. |
| V3.v  | Daemon cold-start post-rule-install: `journalctl -u ventd --since "5 min ago" \| grep -i nvidia` shows temperature polling but no `Insufficient Permissions` noise for *read* paths |        | Fan *write* may still return Insufficient Permissions — documented v0.2.0 known issue. |

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

- **PASS** — every check returned PASS or SKIP-with-reason; no FAIL or
  MANUAL outstanding. v0.2.0 tag is unblocked.
- **PASS-WITH-ISSUES** — non-P0 FAILs filed as issues; release proceeds
  with a known-issues entry in CHANGELOG.
- **FAIL** — any of `0a.i`, `0a.iv`, `0b.i`, `V1.i`, `V1.iii`, `V2.i`,
  or `V2.iv` failed. These are P0 (the v0.2.0 release notes claim each
  of these fails without them). Block the tag until fixed.

Conclusion: _____________

---

## Re-run procedure

The harness is idempotent and safe to re-run after each fix. Suggested
workflow when iterating:

1. Run the full script: `sudo bash validation/run-rig-checks.sh`.
2. Walk sections V1 / V2 / V3 by hand against the current rig state.
3. Open issues for any FAIL items.
4. Land the fix on a feature branch.
5. Re-run both the script and the V-series checks; copy the new summaries
   into the tables above.
6. When all FAIL → PASS, update the conclusion above and commit this file.
