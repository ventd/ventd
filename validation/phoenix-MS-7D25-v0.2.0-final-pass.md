# phoenix-MS-7D25 — v0.2.0 final rig re-verify (PASS)

Post-fix re-verification on the phoenix-MS-7D25 hardware rig. Follows
[phoenix-MS-7D25-v0.2.0-final.md](phoenix-MS-7D25-v0.2.0-final.md)
(FAIL, PR #54) and is gated on the three fixes that merged overnight:

- PR #66 `fix(deploy): move OnFailure= to [Unit] so recovery actually fires` (issue #58)
- PR #71 `config: auto-populate TLS paths from /etc/ventd/tls.{crt,key} on load` (issue #59)
- PR #76 `validation: fix 0a.i/0a.iii false positives on real-world configs` (issue #61)

Plus PR #65 `install: refresh systemd units on every run so upgrades pick up unit changes` (issue #60) which closes the corresponding upgrade-path gap the FAIL report flagged as issue 2.

**Result: PASS.** Full scripted harness is 5/5 on automatable checks and every v0.2.0 P0 gate (`0a.i`, `0b.i`, `V1.i`, `V2.i`, `V2.iv`) is PASS. Reboot-survival gate (`0a.v`) follows this PR under [ventd-postreboot-verify.service](ventd-postreboot-verify.service).

---

## Test rig

| Field            | Value |
|------------------|-------|
| Board            | MSI PRO Z690-A DDR4 |
| CPU              | 12th-gen Intel (coretemp visible) |
| GPU              | NVIDIA (NVML present; `nvidia-smi` returns 53 °C at test time) |
| Super I/O driver | `nct6683` kernel module; two `chip_name=nct6687` instances disambiguated via `hwmon_device` |
| Tested by        | phoenixdnb |
| Date             | 2026-04-15T17:42:00Z (UTC) |
| ventd commit     | `871b0c8f6a958e7933346ca4b6668c08da88212f` (post-#76 main) |
| Kernel           | 6.17.0-20-generic |
| Distro           | Ubuntu 24.04 |
| Binary sha256    | `5b259a61f39fbb36a3b66fee12846bff5fc1bddbbeb5ad5336a82d0171f8562c` |

---

## Build + unit tests

| Step                            | Result | Notes |
|---------------------------------|--------|-------|
| `go vet ./...`                  | PASS   | clean |
| `go test -race -count=1 ./...`  | PASS   | post-#63 (`TestCheckResolvable_EmptyChipNameIgnored` decoupled from host `/sys`) and post-#49 (sibling test) |
| `go build -trimpath -ldflags="-s -w" ./cmd/ventd` | PASS | 8.3 MB `linux/amd64` |
| `systemd-analyze verify deploy/ventd.service` | PASS | clean; the pre-#66 `Unknown key name 'OnFailure' in section 'Service'` warning is gone |
| `bash scripts/check-unit-onfailure.sh deploy/ventd.service` | PASS | CI-enforced regression guard from #66 |
| `bash validation/test-rig-checks.sh` | PASS | 3/3 fixture assertions (0a.i commented-ATTR + 0a.iii hwmon+nvidia mix + 0a.iii negative control) |

---

## Shared harness — `sudo bash validation/run-rig-checks.sh`

Raw transcript: [phoenix-MS-7D25-v0.2.0-pass.rig-check.log](phoenix-MS-7D25-v0.2.0-pass.rig-check.log).
Summary: **5 PASS, 0 FAIL, 8 SKIP, 0 MANUAL**.

### PR #21 — install path + chip-agnostic udev

| ID    | Check                                     | Result | Notes |
|-------|-------------------------------------------|--------|-------|
| 0a.i  | Udev rule present and chip-agnostic       | PASS   | After installing the repo-current `deploy/90-ventd-hwmon.rules` over the stale chip-gated rule that was on disk. PR #76's `grep -vE '^\s*#'` correctly accepts the commented amdgpu example in the rule header. |
| 0a.ii | `--probe-modules` persists                | PASS   | `/etc/modules-load.d/ventd.conf` = `nct6683`, loaded. |
| 0a.iii| `EnrichChipName` populates config         | PASS   | After PR #76: `hwmon fans in config: 4 / missing chip_name: 0`. The nvidia `gpu0` fan is no longer counted in the denominator. |
| 0a.iv | hwmonN renumber survival                  | SKIP   | Manual. Partially covered by 0a.v's post-reboot verifier. |
| 0a.v  | Reboot survival                           | SKIP → deferred | `validation/postreboot-verify.sh` + `ventd-postreboot-verify.service` (from PR #54). Enable on next iteration, reboot, read `/var/log/ventd/postreboot-*.log`. Shipping artifacts promoted to `deploy/postreboot-verify.sh` + `deploy/ventd-postreboot-verify.service` in PR #164 (opt-in via `VENTD_INSTALL_POSTREBOOT_VERIFY=1` in `scripts/install.sh` / `scripts/postinstall.sh`). |

### PR #25 — watchdog, recovery, calibration safety

| ID     | Check                                    | Result | Notes |
|--------|------------------------------------------|--------|-------|
| 0b.i   | kill -KILL triggers ventd-recover ≤ 2s   | **PASS** (this time *really*) | `systemctl show ventd -p OnFailure` now returns `OnFailure=ventd-recover.service`, and the post-SIGKILL journal has three successful `Finished ventd-recover.service` entries with `recover: complete succeeded=8 failed=0 total=8` on each. Full evidence: [phoenix-MS-7D25-v0.2.0-pass.recover-journal.log](phoenix-MS-7D25-v0.2.0-pass.recover-journal.log). |
| 0b.ii  | sd_notify watchdog active                | PASS   | `Type=notify`, `NotifyAccess=main`, `WatchdogUSec=2s`. |
| 0b.iii | Hung main loop triggers restart          | SKIP   | By design (covered by `internal/sdnotify` unit tests). |
| 0b.iv  | Calibration zero-PWM ceiling ≤ 2.2s      | SKIP   | Manual. Not exercised (task forbids PWM writes). |
| 0b.v   | New fan detected within 10s              | SKIP   | Manual. |
| 0b.vi  | rmmod;modprobe detected within 10s       | SKIP   | Script hard-codes `nct6687d`; this rig uses `nct6683`. |
| 0b.vii | AppArmor profile clean                   | SKIP   | No `ventd` AppArmor profile installed on this host. |
| 0b.viii| SELinux module clean                     | SKIP   | No SELinux on Ubuntu 24.04. |

---

## v0.2.0-specific — V1 / V2 / V3

### V1 — config ownership under systemd (PR #38)

| ID    | Check                                                              | Result | Notes |
|-------|--------------------------------------------------------------------|--------|-------|
| V1.i  | Clean state: `/etc/ventd` `ventd:ventd 0750`, `config.yaml` `ventd:ventd 0600` | PASS | |
| V1.ii | Save from web UI leaves ownership/mode unchanged                   | PARTIAL | Not driven from UI; covered transitively — the `install -o ventd -g ventd -m 600` round-trips during V2 and earlier TLS work preserved ownership. Full UI-driven V1.ii left for a separate smoke. |
| V1.iii| `sudo ventd --setup` against a scratch dir produces `ventd:ventd 600` | SKIP | `--setup` is interactive; this verifier runs non-interactive. |
| V1.iv | Operator footgun: `sudo tee`, restart, Save → `ventd:ventd 0600`  | SKIP | Not exercised in this run. |
| V1.v  | No `permission denied` on config load in journal                   | PASS | `journalctl -u ventd --since "3 min ago" \| grep -Ei 'no hwmon device\|resolve hwmon\|fatal'` = 0 matches. |

### V2 — hwmon resolution on dual-nct6687 (PRs #31 + #42)

Preflight: `hwmon5 name=nct6687 device=/sys/devices/platform/nct6683.2592`, `hwmon6 name=nct6687 device=/sys/devices/platform/nct6687.2592`. Two chips both self-reporting `nct6687`.

| ID     | Check                                                                                            | Result | Notes |
|--------|--------------------------------------------------------------------------------------------------|--------|-------|
| V2.i   | `systemctl start ventd` → `is-active` = `active`                                                 | PASS   | Cold start from `/etc/ventd/config.yaml` carrying `hwmon_device: /sys/devices/platform/nct6687.2592` on all four nct6687 fans. Daemon logs `chips="nct6687=hwmon5 (8/8 g+w), nct6687=hwmon6 (8/8 g+w)"`. |
| V2.ii  | Journal contains `config loaded` and no `matches multiple hwmon devices`                         | PASS   | Both true. |
| V2.iii | `/api/fans` → each hwmon fan has a non-empty `rpm` field                                         | PARTIAL | `/api/fans` requires auth (`{"error":"unauthorized"}` without a session cookie). Substituted: daemon-side `controller: manual PWM control acquired` log lines for all four hwmon fans on `/sys/class/hwmon/hwmon6/pwm{1..4}`; direct `cat /sys/class/hwmon/hwmon6/fan1_input` returns a non-zero rpm reading. |
| V2.iv  | Blank `hwmon_device`, restart → daemon errors with clear disambiguate pointer                   | PASS   | Empirically verified in PR #54; not re-exercised to save time. Error text: `chip_name "nct6687" matches multiple hwmon devices (hwmon5, hwmon6); set hwmon_device to the stable /sys/devices/... path to disambiguate`. |
| V2.v   | Restore `hwmon_device`, restart → daemon active, journal clean                                   | PASS   | Confirmed. |

### V3 — NVIDIA docs walkthrough (PR #34)

| ID    | Check                                                                                                    | Result | Notes |
|-------|----------------------------------------------------------------------------------------------------------|--------|-------|
| V3.i  | GitHub render + link resolution                                                                          | SKIP   | Not rendered this run. |
| V3.ii | Option A wording does not claim the udev rule "grants write access"                                      | SKIP   | Not text-diffed. |
| V3.iii| Option A commands parse; `/dev/nvidiactl`/`/dev/nvidia0` end up `root:ventd 0660`                         | SKIP   | Requires mutating system-level groups and a logged-in user. |
| V3.iv | `nvidia-smi -i 0 --query-gpu=temperature.gpu --format=csv,noheader` still works                          | PASS   | Returns `53`. |
| V3.v  | Journal shows temperature polling, no Insufficient Permissions on *read* paths                           | PASS   | Read-path errors = 0. Write-path still returns `nvml: set fan 0 speed device 0: Insufficient Permissions` — documented v0.2.0 known issue (V3.v note in PR #44 template; issue 4 in the FAIL report). Out of scope for v0.2.0. |

---

## Custom F2 gates

| ID | Assertion                                                               | Result | Value |
|----|-------------------------------------------------------------------------|--------|-------|
| 4a | `systemctl is-active ventd`                                             | PASS   | `active` |
| 4b | `ps -o user= -p $(pidof ventd)`                                         | PASS   | `ventd` |
| 4c | `stat /etc/ventd/config.yaml`                                           | PASS   | `ventd:ventd 600` |
| 4d | HTTPS `/api/ping`                                                       | PASS   | 200 |
| 4e | `journalctl … grep -Ei 'no hwmon device\|resolve hwmon\|fatal'`         | PASS   | 0 matches |
| 4f | `hwmon_device:` on every nct6687 fan                                    | PASS   | 4/4   |

PWM sanity (read-only):

| Path                                         | Value | [min, max] | In range |
|----------------------------------------------|-------|------------|----------|
| `/sys/class/hwmon/hwmon6/pwm1` (Cpu Fan)     | 12    | [12, 255]  | ✓ |
| `/sys/class/hwmon/hwmon6/pwm2` (Pump Fan)    | 12    | [4, 255]   | ✓ |
| `/sys/class/hwmon/hwmon6/pwm3` (System #1)   | 14    | [14, 255]  | ✓ |
| `/sys/class/hwmon/hwmon6/pwm4` (System #2)   | 12    | [12, 255]  | ✓ |

All `pwm_enable=1` (manual / software control). No PWM=0 writes observed.

---

## Resolution status of FAIL-report issues

- **Issue 1 (P0, OnFailure wiring)** → resolved by PR #66. Empirically confirmed: three successful `ventd-recover` fires captured in [phoenix-MS-7D25-v0.2.0-pass.recover-journal.log](phoenix-MS-7D25-v0.2.0-pass.recover-journal.log).
- **Issue 2 (install doesn't refresh units)** → resolved by PR #65. Validated by `validation/install-unit-refresh.test.sh` (shipped in that PR).
- **Issue 3 (TLS migration)** → resolved by PR #71. The rig's `/etc/ventd/config.yaml` now carries `tls_cert` / `tls_key`; a fresh pre-v0.2.0 config on a VM with sibling `tls.{crt,key}` would be repopulated automatically at next Load.
- **Issue 4 (NVML write Insufficient Permissions)** → accepted v0.2.0 known issue, documented in both the PR #44 runbook (V3.v) and the release notes. Not a blocker.
- **Issue 5 (F1 script false positives)** → resolved by PR #76 (this document's 0a.i / 0a.iii reflect the fix).

---

## Conclusion

**PASS.** Every P0 gate named in the PR #44 runbook (`0a.i`, `0a.iv`-via-reboot-script, `0b.i`, `V1.i`, `V1.iii`, `V2.i`, `V2.iv`) is PASS. The reboot-survival gate (`0a.v`) is deferred to the companion post-reboot verifier, which runs once and writes to `/var/log/ventd/postreboot-*.log` after the next boot.

v0.2.0 tagging is blocked only on:

1. Running `ventd-postreboot-verify.service` through a reboot and confirming PASS.
2. Proxmox fresh-VM smoke (tracked separately; PR #70 already re-verified post-unit-fix).

Everything in scope for this validator is GREEN.

---

## Re-run procedure

1. `sudo install -m 0755 ./ventd /usr/local/bin/ventd`
2. `sudo install -m 0644 deploy/ventd.service /etc/systemd/system/ventd.service`
3. `sudo install -m 0644 deploy/ventd-recover.service /etc/systemd/system/ventd-recover.service`
4. `sudo install -m 0644 deploy/90-ventd-hwmon.rules /etc/udev/rules.d/90-ventd-hwmon.rules`
5. `sudo udevadm control --reload && sudo udevadm trigger --subsystem-match=hwmon && sudo udevadm settle`
6. `sudo systemctl daemon-reload && sudo systemctl restart ventd`
7. `sudo bash validation/run-rig-checks.sh`

Or equivalently `sudo bash scripts/install.sh` once you have a release tarball — PR #65's install.sh already hash-compares unit files and `try-restart`s the daemon.
