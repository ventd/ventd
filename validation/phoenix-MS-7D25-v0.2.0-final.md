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
| Board            | MSI PRO Z690-A DDR4 |
| CPU              | 12th-gen Intel (coretemp visible) |
| GPU              | NVIDIA (NVML present; `nvidia-smi` returns GPU 0 temp = 44 °C at test time) |
| Super I/O driver | nct6687 (kernel module: `nct6683` — two chip instances, both `chip_name=nct6687`, disambiguated by `hwmon_device`) |
| Tested by        | phoenixdnb |
| Date             | 2026-04-15T16:43:24Z (UTC) |
| ventd commit     | `1a38fd2ab891bf3909e2617d4cf366de0b9f7f58` (then pulled to include PR #44 + PR #46) |
| Kernel           | 6.17.0-20-generic |
| Distro           | Ubuntu 24.04 |
| NVIDIA driver    | nvml-initialised (per `journalctl -u ventd` `"NVML initialised"`); driver version read succeeded via `nvidia-smi` |

---

## Results — shared harness

Raw transcript: [phoenix-MS-7D25-rig-check.log](phoenix-MS-7D25-rig-check.log).
Summary: 3 PASS, 2 FAIL, 8 SKIP, 0 MANUAL.

### PR #21 — install path + chip-agnostic udev

| ID    | Check                                     | Result | Notes |
|-------|-------------------------------------------|--------|-------|
| 0a.i  | Udev rule present and chip-agnostic       | FAIL*  | *Script false-positive. See issue 5. The rule at `/etc/udev/rules.d/90-ventd-hwmon.rules` IS chip-agnostic; the script's `grep -E 'ATTR\{name\}\|ATTRS\{name\}'` matches a COMMENTED-OUT example line (`#SUBSYSTEM=="hwmon", ATTR{name}=="amdgpu", RUN+=…`). Product code is correct; F1 script needs a `grep -v '^\s*#'` guard. |
| 0a.ii | `--probe-modules` persists                | PASS   | `/etc/modules-load.d/ventd.conf` contains `nct6683`, loaded. |
| 0a.iii| `EnrichChipName` populates config         | FAIL*  | *Script false-positive. See issue 5. Script counts 5 `pwm_path` vs 4 `chip_name`; the 5th fan is `gpu0` (type: nvidia) which correctly has no `chip_name`. All hwmon fans have `chip_name`. Fix: exclude nvidia fans from the denominator. |
| 0a.iv | hwmonN renumber survival                  | SKIP   | Manual (F1 script auto-skips non-interactive). Partially covered by the post-reboot verifier (0a.v); live modprobe cycle not exercised in this session. |
| 0a.v  | Reboot survival                           | SKIP → DEFERRED | Cannot bridge `sudo reboot` from this verification session. Standalone verifier committed at `validation/postreboot-verify.sh` + `validation/ventd-postreboot-verify.service`; enable after issue 1 is fixed, then reboot, then read `/var/log/ventd/postreboot-*.log`. |

### PR #25 — watchdog, recovery, calibration safety

| ID     | Check                                    | Result | Notes |
|--------|------------------------------------------|--------|-------|
| 0b.i   | kill -KILL triggers ventd-recover ≤ 2s   | PASS*  | *Script PASSES its own criterion (no `pwm_enable=0` observed 3 s after SIGKILL) BUT the underlying safety promise is broken: `systemctl status ventd-recover` shows `Active: inactive (dead)`. Recover never fired. See issue 1 — `OnFailure=ventd-recover.service` is in the wrong section of the shipped unit file and systemd silently ignores it. |
| 0b.ii  | sd_notify watchdog active                | PASS   | After installing the repo-current `deploy/ventd.service` (the installed unit was stale — issue 2): `Type=notify`, `NotifyAccess=main`, `WatchdogUSec=2s`. |
| 0b.iii | Hung main loop triggers restart          | SKIP   | Deferred — covered by `internal/sdnotify` unit tests per script design. |
| 0b.iv  | Calibration zero-PWM ceiling ≤ 2.2s      | SKIP   | Manual; not exercised (task forbids PWM writes during verification). |
| 0b.v   | New fan detected within 10s              | SKIP   | Manual; requires plugging in a new fan header. |
| 0b.vi  | rmmod;modprobe detected within 10s       | SKIP   | Script hard-codes `nct6687d`; this rig uses in-tree-adjacent `nct6683` module. |
| 0b.vii | AppArmor profile clean                   | SKIP   | No `ventd` AppArmor profile loaded on this host. |
| 0b.viii| SELinux module clean                     | SKIP   | No SELinux on Ubuntu 24.04 default. |

---

## Results — v0.2.0 specific

### V1 — config ownership under systemd (PR #38)

| ID    | Check                                                                                                   | Result | Notes |
|-------|---------------------------------------------------------------------------------------------------------|--------|-------|
| V1.i  | Clean state: `/etc/ventd` → `ventd:ventd 0750`, `/etc/ventd/config.yaml` → `ventd:ventd 0600`            | PASS   | `sudo stat -c '%U:%G %a' /etc/ventd /etc/ventd/config.yaml` → `ventd:ventd 750` / `ventd:ventd 600`. |
| V1.ii | Save from web UI leaves ownership/mode unchanged                                                        | PARTIAL | Not driven from the UI this run, but covered implicitly: V2.iv round-trip and the TLS-migration round-trip both used `sudo install -o ventd -g ventd -m 600 … /etc/ventd/config.yaml`; post-restart `stat` remained `ventd:ventd 600` with no drift. Full web-UI-driven V1.ii is SKIPPED. |
| V1.iii| `sudo /usr/local/bin/ventd --setup --config /tmp/vownership/config.yaml` → written config is `ventd:ventd 600` | SKIP   | `--setup` is interactive (wizard over TTY); this verifier runs under a non-interactive shell. Not exercised. |
| V1.iv | Operator footgun: `sudo tee` over config, restart, Save → ends up `ventd:ventd 0600`                    | SKIP   | Not exercised (no web-UI save performed). Close sibling exercised: the TLS-migration fix in the pre-verification step applied `sudo install -o ventd -g ventd -m 600 /tmp/config.yaml.new /etc/ventd/config.yaml` and ownership survived the subsequent restart — `ventd:ventd 600` confirmed. |
| V1.v  | No `permission denied` on config load in journal                                                        | PASS   | `journalctl -u ventd --since "10 min ago"` shows `config loaded path=/etc/ventd/config.yaml controls=5` with no permission-denied noise. |

### V2 — hwmon resolution on dual-nct6687 (PRs #31 + #42)

Environment preflight (run once before V2):

```
/sys/class/hwmon/hwmon0  name=acpitz           device=…/thermal_zone0
/sys/class/hwmon/hwmon1  name=nvme             device=…
/sys/class/hwmon/hwmon2  name=nvme             device=…
/sys/class/hwmon/hwmon3  name=hidpp_battery_0  device=…
/sys/class/hwmon/hwmon4  name=coretemp         device=…
/sys/class/hwmon/hwmon5  name=nct6687          device=/sys/devices/platform/nct6683.2592
/sys/class/hwmon/hwmon6  name=nct6687          device=/sys/devices/platform/nct6687.2592
```

Two chips both self-reporting `nct6687` — the exact ambiguity PR #42 was
written to resolve.

| ID     | Check                                                                                            | Result | Notes |
|--------|--------------------------------------------------------------------------------------------------|--------|-------|
| V2.i   | `sudo systemctl start ventd` → `is-active` = `active`                                            | PASS   | After fixing the TLS-migration gap (issue 3) and installing the repo-current unit (issue 2). `journalctl` reports `hwmon: PWM channels visible writable=16 total=16 chips="nct6687=hwmon5 (8/8 g+w), nct6687=hwmon6 (8/8 g+w)" example=/sys/class/hwmon/hwmon5/pwm1`. |
| V2.ii  | Journal contains `config loaded` and no `matches multiple hwmon devices`                         | PASS   | Both lines empirically confirmed in the same journal. |
| V2.iii | `curl -sk https://localhost:9999/api/fans` → each hwmon fan has a non-empty `rpm` field          | SKIP / PARTIAL | `/api/fans` requires auth (`{"error":"unauthorized"}` without a session cookie). Substituted: `curl -sk https://127.0.0.1:9999/api/ping` returns 200, and `journalctl` shows `controller: manual PWM control acquired` for Cpu Fan / Pump Fan / System Fan #1 / System Fan #2 all on `/sys/class/hwmon/hwmon6/pwm{1..4}`. Direct sysfs fan_input for Cpu Fan reads 328 RPM at PWM=12 (spinning). Daemon is reading/writing the correct resolved paths. |
| V2.iv  | Blank `hwmon_device` on one fan, restart → daemon errors with a clear disambiguate pointer naming both candidates | PASS | Exercised live: replaced `hwmon_device: /sys/devices/platform/nct6687.2592` on Cpu Fan with a comment, restarted. Journal: `fatal err="load config: resolve hwmon paths in /etc/ventd/config.yaml: fan \"Cpu Fan\": chip_name \"nct6687\" matches multiple hwmon devices (hwmon5, hwmon6); set hwmon_device to the stable /sys/devices/... path to disambiguate"`. Both candidates named. Exactly the user-facing error PR #42 promised. |
| V2.v   | Restore `hwmon_device`, restart → daemon active, no stale errors                                 | PASS   | Restored from backup; `is-active` = `active`; journal clean from restart forward. |

### V3 — NVIDIA docs walkthrough (PR #34)

| ID    | Check                                                                                                    | Result | Notes |
|-------|----------------------------------------------------------------------------------------------------------|--------|-------|
| V3.i  | Render `docs/nvidia-fan-control.md` on GitHub; every link resolves                                       | SKIP   | Not rendered this session. `docs/nvidia-fan-control.md` exists in the repo at HEAD. Recommend visual check from browser before tagging. |
| V3.ii | Option A wording does NOT claim the udev rule "grants write access"                                      | SKIP   | Not text-diffed this session. |
| V3.iii| Copy-paste Option A commands — all parse; `/dev/nvidiactl`+`/dev/nvidia0` end up `root:ventd 0660`        | SKIP   | Not exercised (requires mutating system-level groups and a logged-in user). |
| V3.iv | `nvidia-smi -i 0 --query-gpu=temperature.gpu --format=csv,noheader` still works                          | PASS   | Returns `44` during the run. |
| V3.v  | Daemon cold-start: `journalctl … \| grep -i nvidia` shows temperature polling, no Insufficient Permissions on *read* paths | PASS | `journalctl -u ventd --since "3 min ago" \| grep -i nvidia` → `"NVML initialised"`, then only `controller: PWM write failed … nvml: set fan 0 speed device 0: Insufficient Permissions`. No *read* path errors. Write-path errors are the documented v0.2.0 known issue — see issue 4. |

Allowed values: **PASS**, **FAIL**, **SKIP** (environmental), **MANUAL**.

---

## Issues found

### Issue 1 (P0) — `OnFailure=ventd-recover.service` is in the wrong systemd section; silently ignored; recover never fires

`deploy/ventd.service:35` places `OnFailure=ventd-recover.service` under `[Service]`.
systemd accepts `OnFailure=` only in `[Unit]`. Confirmed with `systemd-analyze verify`:

```
/etc/systemd/system/ventd.service:35: Unknown key name 'OnFailure' in section 'Service', ignoring.
```

`systemctl show ventd -p OnFailure,Triggers` → empty. After SIGKILL of ventd,
`ventd-recover.service` stayed `Active: inactive (dead)`. The "any exit path
within two seconds" promise from PR #25 is decorative.

**Fix**: move `OnFailure=ventd-recover.service` (and its comment block) from
`[Service]` into `[Unit]` in `deploy/ventd.service`. Then re-verify 0b.i by
confirming `systemctl status ventd-recover` shows `Active: active (exited)`
with a timestamp inside the test window after a SIGKILL of `ventd`.

Introduced in PR #25 (commit `f0fd5da`).

### Issue 2 (P1) — binary-only install path leaves pre-PR-#25 unit file on disk

`sudo install -m 0755 ./ventd /usr/local/bin/ventd` (the operator-facing
upgrade step in the runbook) does not refresh `/etc/systemd/system/ventd.service`
or install `ventd-recover.service`. On this rig the unit on disk was last
written 2026-04-14 21:20:51 and was still `Type=simple`, no `WatchdogSec`,
no `OnFailure=`. Every operator upgrading a binary from before PR #25 inherits
that stale unit and has 0b.i / 0b.ii silently regressed.

**Fix**: update the install script (and the documented upgrade runbook) to
re-install `deploy/ventd.service` and `deploy/ventd-recover.service`, then
`systemctl daemon-reload` + `systemctl restart ventd`. Or have a one-shot
`ventd --install-systemd-units` subcommand that an operator runs as part
of any upgrade.

This was fixed in-place for this verification (both units installed, daemon-reload, restart).

### Issue 3 (P1) — pre-PR-#5 configs do not carry `tls_cert`/`tls_key`; the daemon crashloops on upgrade

PR #5 added `cfg.Web.RequireTransportSecurity()` which refuses `0.0.0.0` binds
without TLS. PR #5 also wires first-boot auto-generation of `tls.{crt,key}`.
On this rig `/etc/ventd/tls.crt` and `/etc/ventd/tls.key` exist (Apr 14) but
`/etc/ventd/config.yaml` did not carry the `web.tls_cert` / `web.tls_key`
fields. Either `Save()` dropped them at some point, or first-boot never
persisted the fields it populated in memory. Net effect: the post-F2 binary
refuses to start on a pre-F2 config.

**Fix** (one-time-migration): at `Load()` time, if `cfg.Web.TLSCert`/`TLSKey`
are empty AND `<config-dir>/tls.crt` and `<config-dir>/tls.key` both exist
AND parse as a valid keypair, re-populate the fields (and `Save()` to persist).
Or: ensure first-boot `Save()` serialises the fields it wrote.

Worked around for this verification by inserting the two lines under `web:`
and reinstalling the config with `sudo install -o ventd -g ventd -m 600`.

### Issue 4 (v0.2.0 known issue, not a blocker) — NVIDIA fan *write* path fails under the hardened sandbox

Every poll tick: `controller: PWM write failed fan=gpu0 curve=gpu_curve err="nvml: set fan 0 speed device 0: Insufficient Permissions"`.
NVML init succeeded and temperature reads work (V3.iv PASS). The fan-set path
needs a privilege or device ACL the PR #38 sandbox does not grant.

V3.v of this runbook already records this as the documented v0.2.0 known
issue. The journal noise (1 error / 2 s) is a UX regression that a log-once-then-disable
would fix; the underlying lack of GPU fan *control* is accepted.

**Recommend (post-v0.2.0)**: (1) log the NVML `Insufficient Permissions` once per
process lifetime, then disable further write attempts for that fan; (2) open
a follow-up issue for widening the sandbox (least-privilege) or providing a
setup step that grants `ventd` the required access.

### Issue 5 (P2, script only) — F1 script false positives

- `check_0a_i_udev_rule_chip_agnostic`: greps `ATTR{name}` across the whole
  file; matches a commented example block. Fix: `grep -v '^\s*#'` first.
- `check_0a_iii_enrich_chip_name_in_config`: counts every `pwm_path` as a
  hwmon fan. NVIDIA fans have `pwm_path: "0"` (the index of the GPU, not a
  sysfs path) and no `chip_name`. Fix: count only fans whose block contains
  `type: hwmon`.

Neither is a product defect. Both should land with one-line patches each
before the next rig run so `summary: N FAIL` reflects reality.

---

## Conclusion

Conclusion: **FAIL**.

Rationale:

- `0b.i` PASSes the script's narrow criterion but fails in substance
  (issue 1 — the `OnFailure` directive is ignored by systemd). The
  v0.2.0 tag's safety story hinges on `ventd-recover` firing; it
  does not.
- `0a.i` FAILs per the script but the product itself is fine — script
  false positive (issue 5). Does not block the tag on its own.
- All P0 product gates otherwise clean: V1.i PASS, V2.i PASS, V2.iv PASS,
  V2.v PASS. Hwmon dual-chip disambiguation from PR #31 + PR #42 works
  as advertised on the live rig. PR #38's config ownership gates hold.

**Do not tag v0.2.0 until issue 1 is fixed** and 0b.i is re-verified
with `systemctl status ventd-recover` showing `Active: active (exited)`
inside the 2-second window after SIGKILL. Issues 2 and 3 affect the
upgrade path from pre-v0.2.0 deploys and should land alongside. Issue 4
is accepted as a v0.2.0 known issue. Issue 5 is a harness-only follow-up.

The Proxmox fresh-VM smoke is tracked separately per the release runbook.

---

## Re-run procedure

1. Apply fixes for issue 1 (OnFailure section) and ideally 2, 3, 5.
2. Rebuild + install binary AND unit files.
3. Run full script: `sudo bash validation/run-rig-checks.sh`.
4. Walk sections V1 / V2 / V3 by hand against the current rig state.
5. Enable and reboot to exercise the post-reboot verifier:
   ```bash
   sudo install -m 0755 validation/postreboot-verify.sh \
       /usr/local/sbin/ventd-postreboot-verify.sh
   sudo install -m 0644 validation/ventd-postreboot-verify.service \
       /etc/systemd/system/ventd-postreboot-verify.service
   sudo systemctl daemon-reload
   sudo systemctl enable ventd-postreboot-verify.service
   sudo reboot
   # after reboot:
   sudo cat /var/log/ventd/postreboot-*.log
   ```
6. When everything is PASS, update the conclusion and commit.
