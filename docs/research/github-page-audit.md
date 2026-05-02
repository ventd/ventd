# GitHub repo audit vs. R28 research findings (8 agents)

**Audited:** README.md, repo description, topics, deploy/ventd.service, docs/hardware.md, .github/ISSUE_TEMPLATE/

**Cross-referenced against:** the eight R28 agent reports (~370 quirk rows + kernel-fix mapping) on `docs/research/r-bundle/`.

Findings sorted by severity. Tier 1 are factually wrong (immediate fix). Tier 2 are overclaims that bite hostile-board users. Tier 3 are missing acknowledgments / underclaims. Tier 4 is templates and process.

---

## Tier 1 — factually wrong, fix immediately

### 1.1 README claims "runs unprivileged" but daemon is `User=root` since v0.5.8.1

**Where:** README lines 21, 69, 161 + the comparison table.

**Reality:** `deploy/ventd.service` ships `User=root` and `Group=root` (verified). The v0.5.8.1 root-flip (PR #787, task #50) reverted ventd to root. The README's centerpiece differentiator vs. fan2go and CoolerControl is *no longer true*.

**Fix:** Remove the "runs unprivileged" claims from the README, the Safety section, and the comparison table. Or: re-flip the daemon to non-root in v0.6.0 and put the claim back when it's true again. Pick one — but the current state (claim is in README, daemon is root) has to end.

### 1.2 README documents the setup-token flow, eliminated in v0.5.8.1

**Where:** README lines 137–141.

> The setup wizard prompts for a one-time token on first run. The daemon does not log the token to journald; it writes it to `/run/ventd/setup-token` (0600, root-only)…
> ```
> sudo cat /run/ventd/setup-token
> ```

**Reality:** v0.5.8.1 PR #765 eliminated the setup token (task #48 — completed). First-boot now uses self-signed TLS + first-login-creates-account. The README still tells users to `sudo cat` a file that no longer exists.

**Fix:** Replace the "setup token" paragraph with the current first-boot flow (visit URL → set password on first login).

### 1.3 README claims NixOS support; research confirms we don't have it

**Where:** README line 145 — distro list includes NixOS.

**Reality:** Agent F documented (with NixOS GitHub issue links) that imperatively-written `/etc/modprobe.d/*` and `/etc/sensors.d/*` are **silently ignored** on NixOS — these are the exact paths every auto-fix endpoint in `internal/web/server.go` writes to. The MOK enrollment and DKMS flow also differ. We have no `nix-env` / `configuration.nix` integration.

**Fix:** Drop NixOS from the supported-distros list until we ship Nix module fragments. Or note "NixOS: limited — modprobe.d-based auto-fixes do not persist; manual `configuration.nix` integration required".

### 1.4 Hardware coverage number stale

**Where:** README line 57 — "Hardware database (52 boards, 6 vendors)".

**Reality:** The catalog has grown since v0.5.0 and `tools/rule-index` reports more entries today. R28 research suggests committing to a moving floor ("130+ boards" as of v0.5.9, growing) rather than freezing at 52.

**Fix:** Either generate the count from `internal/hwdb/profiles-v1.yaml` at release time and substitute, or replace with "growing catalog of 100+ boards" and link to `docs/hardware.md`.

---

## Tier 2 — overclaims that bite hostile-board users

### 2.1 "Zero-config" / "Zero terminal after install" / "any distro" — overclaim

**Where:** Repo description ("Zero-config fan control daemon"), README line 10 ("Install, open the browser, click Apply — ventd handles the rest"), line 62 ("Zero terminal after install").

**Reality:** R28 confirms the long tail is real:
- **Agent A:** Gigabyte AORUS / MSI Z690 boards need `acpi_enforce_resources=lax` OR `it87 ignore_resource_conflict=1` (kernel ≥6.2)
- **Agent G:** Dell has 5 distinct control-plane paths within a single OEM; HP EliteBook G10+ / ZBook G9-G10 are SMM-locked (literally impossible from Linux); post-2020 Dell XPS 9320/9500/9710 are EC-locked
- **Agent H:** Apple Silicon (M1-M4), Surface Pro 9, Intel NUC ("no software controllable fans" per Intel) — all firmware-managed
- **Phoenix's HIL session:** ~30 issues on a single MS-7D25 board

The current "zero-config" framing sets expectations the product can't meet on hostile hardware, then the wizard surfaces a recovery card that often loops.

**Fix (Path C alignment):** Reframe as "Zero-config on most consumer hardware, monitor-only fallback when the kernel doesn't expose writable fan control. Driver install for advanced setups is opt-in." Keep the "click Apply, ventd handles the rest" promise for the 70% it's true for; honestly position the rest.

### 2.2 README implies all fan controls are writable

**Where:** README lines 19, 54.

> It enumerates everything writable through hwmon, NVML, and a native USB HID stack…
> Enumerates every writable fan control the kernel exposes…

**Reality:** "Every writable fan control the kernel exposes" is technically accurate but misleading — the kernel doesn't expose writable controls for plenty of hardware (HP EliteBook EC-locked, Surface SAM EC, Apple Silicon by Asahi policy, Intel NUC, post-2020 Dell XPS, NVIDIA H100/A100 datacenter GPUs, AMD Instinct MI200/MI300X — full list in Agent G/H).

**Fix:** Add a paragraph immediately under Features acknowledging this:
> **What ventd cannot control:** SMM-locked HP EliteBook / ZBook fans, post-2020 Dell XPS EC-locked chassis, Surface SAM EC, Apple Silicon (Asahi reads fans, doesn't write), Intel NUC, datacenter GPUs (H100/A100/MI300X), HPE iLO Standard tier without Advanced license, iDRAC ≥3.34 (manual control vendor-revoked). On these, ventd surfaces monitor-only mode with a clear explanation.

### 2.3 Vendor-daemon coexistence not addressed

**Where:** Not in README at all — gap.

**Reality:** Agent G's #1 architectural finding: ventd should detect and **defer** to vendor-shipped daemons (`asusd`, `system76-power`, `tccd`/Tuxedo Control Centre, `slimbookbattery`) rather than fight them. On those laptops, the vendor daemon is doing the same job — installing ventd on top creates conflict, not value.

**Fix:** Add a "Coexistence with vendor tools" subsection in Features:
> **Linux-first laptop OEMs (System76, Tuxedo Computers, Star Labs, Slimbook).** ventd detects `system76-power`, `tccd`, and `slimbookbattery` and steps aside — your vendor tool already controls fans correctly. ventd registers as monitor-only on these systems.

### 2.4 ASUS ROG / asusctl coexistence not mentioned

**Where:** Not in README.

**Reality:** Agent G + C: ASUS ROG laptops with `asusctl` running already have working fan control. ventd installed on top will fight `asusctl`. Should detect + defer.

**Fix:** Same as 2.3 — add asusctl to the deferral list.

---

## Tier 3 — missing acknowledgments / underclaims

### 3.1 No mention of the recovery framework, which is unique

**Where:** Features section doesn't list it.

**Reality:** The classifier + auto-fix-card framework (issue #800, this PR) is genuinely unique — fan2go, CoolerControl, fancontrol all just emit error strings. ventd's per-class structured remediation with one-click auto-fix is the real differentiator. R28 research confirms 100+ catalog rows of operator-actionable workarounds the framework can encode.

**Fix:** Add a Features bullet:
> **Self-healing recovery.** When something goes wrong (Secure Boot blocking module load, DKMS state collision, in-tree driver conflict, ACPI region reservation), ventd classifies the failure and offers one-click auto-fixes — install kernel headers, queue MOK enrollment, write modprobe quirks, re-run install with cleared state. No other Linux fan tool ships this.

### 3.2 No "What's not yet supported" honesty section

**Where:** Missing.

**Reality:** Phoenix's "30 issues per machine" frustration is the predictable result of users showing up expecting one thing and getting another. R28 research has a clean list of "documented impossible" hardware (Agent G's upstream-track list, Agent H's vendor-blocked list). Surfacing it builds trust.

**Fix:** New section after Supported Platforms:
> ## Hardware that cannot be controlled from Linux
>
> The following hardware **cannot** be controlled by ventd or any Linux fan tool — the firmware/hypervisor blocks all software access. ventd will detect them and surface monitor-only mode with a clear explanation:
>
> - HP EliteBook G10+, ZBook G9/G10 — SMM-locked
> - Post-2020 Dell XPS 9320, 9500, 9710 — EC-locked
> - Surface Pro 9, Surface Laptop Studio — Surface Aggregator EC
> - Microsoft Surface kbd-cover keyboards — by design
> - Apple Silicon Macs (M1, M2, M3, M4) — Asahi project policy is read-only
> - Intel NUC — per Intel: "no software controllable fans"
> - Acer Predator/Nitro post-2021 BIOS — EC-locked
> - HPE iLO Standard (Gen8, Gen9, Gen10 without Advanced license)
> - iDRAC firmware ≥3.34.34.34 — manual control vendor-revoked
> - NVIDIA datacenter GPUs (H100, H200, A100) — firmware-locked
> - AMD Instinct MI200, MI300X — firmware-locked
> - OEM mini-PCs without in-tree EC drivers (Beelink, GMKtec, AceMagic — model-specific)
>
> Full per-board breakdown: `docs/hardware.md`.

### 3.3 No mention of preflight orchestrator (this PR's headline feature)

**Where:** Missing — PR #816 is unmerged at audit time, but README will need updating.

**Fix:** Add Features bullet post-merge:
> **Terminal-first preflight (`ventd preflight`).** Before the systemd unit is even installed, the install script runs an interactive preflight that detects Secure Boot prerequisites, missing kernel headers, in-tree driver conflicts, and 20+ other install-time blockers. It walks you through Y/N-gated auto-fixes for each — no opening a wiki, no guessing modprobe options. The web UI never shows install-time errors because they're caught and fixed in the terminal first.

### 3.4 Comparison table missing rows

**Where:** README lines 155–168.

**Reality:** Table compares to fan2go, CoolerControl, thinkfan, fancontrol but doesn't include:
- **Recovery / self-healing** — only ventd has this (uniquely good for users)
- **OOT module install + MOK enrollment automation** — only ventd
- **Hardware/distro-aware auto-fix dispatch** — only ventd

**Fix:** Add rows for the above three differentiators.

---

## Tier 4 — issue templates + process

### 4.1 Hardware-report template doesn't ask for the right info

**Where:** `.github/ISSUE_TEMPLATE/hardware-report.yml`

**Reality:** R28 research shows the consistent pattern across thousands of issues: ventd needs DMI vendor + product + version, kernel version, OOT module name + version, dmesg `it87`/`nct6` lines, `/sys/class/hwmon/*/name`, and the hwdiag bundle (already a feature). Without all of these, classifying a hardware-report issue is guesswork.

**Fix:** Update the YAML to require those fields, with copy-paste-able commands the user runs (`dmidecode -s system-product-name`, `dmesg | grep -iE 'it87|nct6|acpi.*resource'`, `lsmod | grep -iE 'it87|nct6|asus|dell'`, `uname -r`).

### 4.2 Bug template missing diag-bundle prompt

**Where:** `.github/ISSUE_TEMPLATE/bug.yml`

**Fix:** Add a required field "Did you run `ventd diag bundle` and attach it?" — the bundle has 90% of the data we need to triage.

### 4.3 Hardware compatibility doc (`docs/hardware.md`) needs auto-population

**Where:** `docs/hardware.md` is hand-maintained; falls behind as the catalog grows.

**Fix:** Generate the table from `internal/hwdb/profiles-v1.yaml` at release time. The catalog already has every confirmed-working board; the doc should be the catalog as Markdown, not a duplicate.

---

## Repo description + topics

**Current description:**
> "Zero-config fan control daemon for Linux. Auto-detects hardware via hwmon and NVML, self-calibrates from a web UI, auto-recovers from crashes. Single static Go binary. Any distro, any init system, amd64 and arm64. AMD, NVIDIA, and motherboard fans."

**Issues:** "Zero-config" + "any distro" both contradict R28 reality.

**Suggested replacement:**
> "Auto-config fan control daemon for Linux. Hwmon + NVML detection, browser-based calibration, structured failure recovery with one-click auto-fixes for 100+ known hardware quirks. Single static Go binary, monitor-only fallback when the kernel doesn't expose writable fan control. systemd / OpenRC / runit; amd64 and arm64; Debian/Ubuntu/Fedora/Arch/openSUSE/Alpine."

**Topics to add:** `recovery`, `auto-fix`, `secure-boot`, `dkms`, `mok`, `it87`, `nct6687d` (board-specific tags improve searchability).

---

## Summary action list

**Do now (Tier 1):**
- [ ] Remove "runs unprivileged" from README + comparison table (or re-flip the daemon)
- [ ] Replace setup-token paragraph with current first-boot flow
- [ ] Drop NixOS from supported-distros list (or note partial)
- [ ] Update board count or generate dynamically

**Do this week (Tier 2):**
- [ ] Reframe "zero-config" → "auto-config + monitor-only fallback"
- [ ] Add "What ventd cannot control" section
- [ ] Add vendor-daemon coexistence subsection

**Do post-merge of PR #816 (Tier 3):**
- [ ] Add recovery framework Features bullet
- [ ] Add `ventd preflight` Features bullet
- [ ] Expand comparison table with recovery / OOT-install / auto-fix rows

**Do as ongoing process (Tier 4):**
- [ ] Update issue templates with required diagnostic fields
- [ ] Auto-generate `docs/hardware.md` from catalog YAML
