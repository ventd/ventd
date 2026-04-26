# spec-12 Amendment — OOT Driver Install Handling in Setup Flow

**Status:** Draft. Targets spec-12 PR 4 (setup flow) when it lands.
**Bound spec sections:** spec-12 §6 (setup flow integration design), §3 PR 4 file list, §5 (setup-flow rules RULE-UI-SETUP-01..10).
**Predecessor:** spec-11 (superseded by spec-12). spec-03 schema v1.1 (`dkms_required`, `kernel_module_source`, `repository` fields on driver YAMLs already shipped via scope-C).
**Why now:** scope-C catalog adds boards (Lenovo Legion via `legion_hwmon`) where the matched driver is OOT DKMS — not in mainline kernel. spec-12 setup flow currently has no story for "matched board needs a kernel module the user doesn't have installed." Without this amendment, a Legion user runs through setup, hits step 4 (calibration), and watches calibration fail silently because `legion_laptop` isn't loaded.

---

## §1 — Why this amendment exists

Phoenix's vision: **zero-config, zero-terminal, any-hardware**. scope-C catalog research surfaced that ~5-10% of supported hardware needs out-of-tree kernel modules:
- Lenovo Legion gaming laptops → `legion_laptop` (johnfanv2/LenovoLegionLinux DKMS)
- MSI nct6687d boards → `nct6687d` (Fred78290/nct6687d DKMS)
- Future: Framework laptops via NBFC (deferred to spec-09)

The current spec-12 setup flow runs:
```
token → doctor → hardware → calibrate → preview → apply
```

If `hardware` matches a board with `dkms_required: true` and the module is not loaded, `calibrate` fails. The user sees "0 controllable channels" and has no path forward inside the wizard.

This amendment defines a **conditional pre-calibration step** that detects the OOT-driver gap, presents the user with their distro-specific install command, and either guides them through or accepts that some scenarios are not "zero-terminal" within v1.0.

---

## §2 — Honest framing

Before the design: this amendment **does not** make ventd auto-install kernel modules. Three reasons documented in §6 of the broader vision review:

1. **DKMS requires kernel headers + build toolchain.** Pulling these into a TrueNAS/Unraid appliance violates the appliance contract.
2. **Distro-specific package management.** Per-distro installer logic, signing key management, graceful failure across 6+ package managers is a meaningful subsystem to maintain.
3. **Secure Boot + MOK enrollment requires interactive firmware-level password.** ventd cannot do this without user input — by definition.

What ventd CAN do:
- **Detect** the gap explicitly during setup, not silently at calibration.
- **Generate** the exact install command for the user's detected distro.
- **Verify** module load post-install before allowing setup to proceed.
- **Optionally execute** the install via pkexec (one password prompt) when the user opts in.

This is "one-prompt setup," not "zero-terminal setup," for OOT-driver hardware. Acceptable v1.0 boundary.

---

## §3 — Setup flow change

Insert a new conditional sub-step between step 3 (hardware) and step 4 (calibrate). Numbering stays at 5 user-visible steps; the new sub-step appears only when needed.

```
START
  │
  ▼
Step 1 — Token authentication                     [unchanged]
  │
  ▼
Step 2 — Doctor preflight                         [unchanged]
  │
  ▼
Step 3 — Hardware confirmation                    [unchanged]
  │   matched board profile + EffectiveControllerProfile
  │
  ▼
[Step 3.5 — Driver install — CONDITIONAL]         [NEW]
  │   triggers only if matched board's primary_controller.chip
  │   resolves to a driver YAML where:
  │     dkms_required: true  AND  module not currently loaded
  │
  │   If trigger condition false: skip silently, proceed to step 4.
  │
  ├─ User picks "I'll install it myself"
  │   → Show distro-detected install command (copy-button)
  │   → Show "Verify" button — runs lsmod check, advances when module loads
  │
  ├─ User picks "Install via pkexec"
  │   → ventd elevates via pkexec, runs distro-detected install
  │   → Streams progress to UI
  │   → On success: auto-advance to step 4
  │   → On failure: fall back to manual flow with error context
  │
  └─ User picks "Skip — use telemetry-only mode"
      → Sets profile-runtime override unsupported_at_runtime: true
      → Advances to step 4 with calibration in read-only mode
      │   (calibration completes, autocurve skipped per spec-03 amendment §HP-CONSUMER pattern)
  │
  ▼
Step 4 — Calibration                              [unchanged behaviour, now reachable for OOT boards]
  │
  ▼
Step 5 — Curve preview + apply                    [unchanged]
  │
  ▼
COMPLETE
```

---

## §4 — Trigger condition

Step 3.5 activates if and only if all three hold:

1. **Board profile matches AND has a primary_controller.chip reference.**
2. **The driver YAML for that chip carries `dkms_required: true`.**
3. **The named module is NOT in `/proc/modules`** (or `lsmod` equivalent for the running kernel).

Implementation in `internal/web/setup_driver_check.go`:

```go
func needsDriverInstall(profile *EffectiveControllerProfile) (*DriverGap, bool) {
    driver := profile.Driver
    if driver == nil || !driver.DKMSRequired {
        return nil, false
    }

    if isModuleLoaded(driver.Module) {
        return nil, false
    }

    return &DriverGap{
        ModuleName:    driver.Module,
        Repository:    driver.Repository,
        License:       driver.License,
        InstallHints:  resolveDistroInstallHints(driver),
        FallbackCmd:   driver.FallbackInstall, // optional: catalog-specified fallback
    }, true
}

func isModuleLoaded(name string) bool {
    data, err := os.ReadFile("/proc/modules")
    if err != nil {
        return false
    }
    return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s`).Match(data)
}
```

The `DriverGap` struct flows into the setup state machine and renders step 3.5 only when populated.

---

## §5 — Distro detection + install command generation

Detect distro via `/etc/os-release` (`ID=` and `ID_LIKE=` fields). Generate install commands for the top-tier distros:

| Distro | Detection | legion_hwmon command |
|---|---|---|
| Arch / Manjaro | `ID=arch` or `ID_LIKE=arch` | `yay -S lenovolegionlinux-git` (or `paru`, document pacman path) |
| Fedora / RHEL | `ID=fedora\|rhel\|centos` | `sudo dnf copr enable johnfan/LenovoLegionLinux && sudo dnf install LenovoLegionLinux-dkms` |
| Debian / Ubuntu | `ID=debian\|ubuntu` | `git clone https://github.com/johnfanv2/LenovoLegionLinux && cd LenovoLegionLinux && sudo make install` (no upstream PPA as of 2026-04) |
| openSUSE | `ID=opensuse-tumbleweed\|opensuse-leap` | OBS package or source build |
| Alpine | `ID=alpine` | NOT SUPPORTED — Alpine doesn't ship kernel headers; recommend telemetry-only |
| NixOS | `ID=nixos` | Refer to nixpkgs package or flake — install path is nix-rebuild, not imperative |
| TrueNAS SCALE | `ID=truenas-scale` | NOT SUPPORTED — appliance contract; recommend telemetry-only |
| Unraid | detected via `/etc/unraid-version` | NOT SUPPORTED — appliance contract; recommend telemetry-only |
| Proxmox VE | `ID=debian` + pveversion presence | Same as Debian, BUT requires `pve-headers` not `linux-headers-$(uname -r)` |

The install commands are stored as YAML in `internal/web/setup_distro_commands.yaml` (NOT in the catalog driver YAMLs — keeps catalog data clean and decouples distro packaging churn from catalog updates).

**Catalog driver YAML extension (small):**

```yaml
# In internal/hwdb/catalog/drivers/legion_hwmon.yaml — add optional field
fallback_install:
  source_url: "https://github.com/johnfanv2/LenovoLegionLinux"
  source_command: |
    git clone https://github.com/johnfanv2/LenovoLegionLinux
    cd LenovoLegionLinux/kernel_module
    sudo make reloadmodule
  notes: "Distro-specific packaging may exist; check setup_distro_commands.yaml first."
```

This is the catalog's per-driver "if all else fails, here's how to build from source" hint. Setup flow uses it as last-resort.

---

## §6 — pkexec install mode (opt-in)

When the user clicks "Install via pkexec" in step 3.5, ventd:

1. **Generates a single shell command** based on detected distro.
2. **Calls pkexec** with the generated command.
3. **Streams stdout/stderr** to the setup UI via SSE (reuses spec-12 PR 4 calibration progress streaming infrastructure).
4. **On exit code 0**: runs `modprobe <module>`, verifies via `/proc/modules`, auto-advances.
5. **On exit code ≠ 0**: shows the error output verbatim, falls back to manual install panel with the same command pre-filled in a copy-button widget.

**Critical safety constraints:**

- **No arbitrary command execution.** pkexec receives ONE shell command, generated from a static template + verified distro fields. The template is in `setup_distro_commands.yaml` and reviewed at PR time.
- **No network call from ventd itself.** The install command may invoke `git clone` or `dnf install` which hit network — that's the package manager's behavior, not ventd's. ventd doesn't fetch tarballs, scripts, or signatures.
- **No Secure Boot bypass.** If the user has Secure Boot enabled and the build doesn't auto-sign, the module won't load post-install. Setup detects this (module not in `/proc/modules` after install completes successfully) and shows clear guidance: "Install completed but module did not load. Likely Secure Boot. Run `sudo mokutil --import /var/lib/dkms/<module>/mok.pub`, reboot, return to setup."
- **No silent fallback to source build** when distro package fails. User explicitly opts into source build via separate button.

**pkexec policy file** (ships with ventd packaging, lives in `/usr/share/polkit-1/actions/io.ventd.install-driver.policy`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<policyconfig>
  <action id="io.ventd.install-driver">
    <description>Install kernel module for ventd-detected hardware</description>
    <message>Authentication is required to install a kernel module for fan control.</message>
    <defaults>
      <allow_inactive>no</allow_inactive>
      <allow_active>auth_admin</allow_active>
    </defaults>
  </action>
</policyconfig>
```

Standard polkit pattern — one auth prompt per setup session, doesn't grant broader root.

---

## §7 — Telemetry-only fallback

If the user picks "Skip — use telemetry-only mode," setup writes a per-board override to `setup-output.yaml` (the file produced by step 5 commit):

```yaml
# /etc/ventd/config.yaml.d/setup-output.yaml — written by step 5 atomic commit
board_runtime_overrides:
  - board_id: "lenovo-legion-5-15ach6h"
    unsupported_at_runtime: true
    reason: "user_skipped_dkms_install_at_setup"
    skipped_at: "2026-04-26T..."
```

ventd's matcher reads this file at startup and applies the override. The board runs in telemetry-only mode (curves: [], sensors-only) using the same code path as scope-C boards with `overrides.unsupported: true` baked into the catalog.

**Important: this is a per-installation runtime decision, not a catalog change.** The board catalog still says the board is fully supported with the OOT driver. The user just decided not to install it. Reversing the decision is straightforward: install the module, run `ventd setup reset`, re-run setup.

---

## §8 — Invariants (5 new)

Added to `.claude/rules/ui.md` under spec-12 PR 4 setup-flow rules. Numbering continues from RULE-UI-SETUP-01..10:

| Rule | Subtest | Statement |
|---|---|---|
| RULE-UI-SETUP-11 | TestUISetup_DriverGapDetection | Step 3.5 activates only when all three conditions hold: (a) matched board has primary_controller.chip referencing a driver YAML, (b) that driver YAML has `dkms_required: true`, (c) the named module is not in `/proc/modules`. |
| RULE-UI-SETUP-12 | TestUISetup_DistroDetection | Distro detection reads `/etc/os-release` exclusively. ID and ID_LIKE fields are normalised before lookup against `setup_distro_commands.yaml`. Unrecognised distro falls through to source-build fallback with explicit "your distro is not in our list" UI message. |
| RULE-UI-SETUP-13 | TestUISetup_PkexecCommandTemplated | pkexec invocation receives ONE command string assembled from a verified template + distro fields. No user-supplied input is interpolated into the command. Test fixture: feed adversarial board id / module name through the path, assert no shell injection. |
| RULE-UI-SETUP-14 | TestUISetup_TelemetryOnlyOverride | "Skip — use telemetry-only" path writes `board_runtime_overrides[].unsupported_at_runtime: true` to `setup-output.yaml`. ventd's matcher reads this file and applies the override at runtime. Test: spin up matcher with the override file present, assert calibration is skipped for the named board id. |
| RULE-UI-SETUP-15 | TestUISetup_PostInstallVerification | After pkexec install completes (exit 0), setup verifies the module is actually loaded via `/proc/modules` check before advancing. If module did not load (Secure Boot blocked, dkms build silently failed, etc.), setup shows the Secure-Boot-or-MOK-needed guidance and stays on step 3.5. |

---

## §9 — Files added or modified

**Files (new):**
- `internal/web/setup_driver_check.go` — `needsDriverInstall()`, `isModuleLoaded()`, `DriverGap` struct.
- `internal/web/setup_distro.go` — `/etc/os-release` parser, distro normalisation.
- `internal/web/setup_distro_commands.yaml` — distro → install command mapping.
- `internal/web/setup_pkexec.go` — pkexec invocation + SSE streaming.
- `internal/web/setup_driver_check_test.go` — RULE-UI-SETUP-11 and -15 bindings.
- `internal/web/setup_distro_test.go` — RULE-UI-SETUP-12 + RULE-UI-SETUP-13 bindings.
- `internal/web/setup_pkexec_test.go` — RULE-UI-SETUP-13 binding (command-injection fixture).
- `internal/hwdb/runtime_overrides.go` — reads `setup-output.yaml`'s `board_runtime_overrides` list, applies per-board overrides at matcher dispatch.
- `internal/hwdb/runtime_overrides_test.go` — RULE-UI-SETUP-14 binding.
- `web/setup-driver-step.html` — step 3.5 UI fragment (loaded into existing setup.html accordion).
- `web/setup.js` — extended to handle step-3.5 conditional flow (modify, not new).
- `packaging/polkit/io.ventd.install-driver.policy` — polkit action definition.
- `.claude/rules/ui-setup-driver.md` — RULE-UI-SETUP-11..15 rule files.

**Files (modified):**
- `internal/hwdb/profile_v1.go` (or v1_1.go if schema v1.1 ships first) — extend Driver struct with optional `FallbackInstall` field (matches the YAML extension in §5).
- `internal/web/setup.go` — wire step 3.5 conditional branch into the state machine.
- `internal/web/setup_state.go` — add `awaiting_driver_install` and `installing_driver` sub-states between `hardware_confirmed` and `calibrating`.
- `internal/hwdb/catalog/drivers/legion_hwmon.yaml` — add `fallback_install` block (already drafted in scope-C; this confirms shape).
- `internal/hwdb/catalog/drivers/nct6687d.yaml` — same.
- `docs/setup.md` — document step 3.5 user-facing flow.
- `docs/install.md` — distro install matrix moved here from setup_distro_commands.yaml's notes.
- `CHANGELOG.md` — `[Unreleased]` entry.

**Files NOT touched:**
- `cmd/ventd/main.go` — no daemon changes, no new flags.
- `internal/calibration/*` — calibration unchanged. Telemetry-only mode is a matcher-side override, not a calibration-side change.
- Existing scope-A/B/C board YAMLs — adding `fallback_install` only on driver YAMLs that need it (legion_hwmon, nct6687d).

---

## §10 — Out of scope

- **Auto-install via apt without user opt-in.** Only triggered by explicit "Install via pkexec" click.
- **Module signing for Secure Boot.** ventd does not generate or manage MOK keys. Documents the manual flow when Secure Boot blocks load post-install.
- **NixOS imperative install path.** NixOS is declarative — setup detects it, shows "your distro requires editing your configuration.nix; here's the snippet," does not attempt pkexec.
- **TrueNAS SCALE / Unraid auto-install.** These are appliance OSes; setup detects them and routes straight to telemetry-only mode with a clear "this is an appliance OS" message.
- **Mainline driver install fallback.** If the matched board needs a mainline driver that isn't loaded (rare — usually means user has stripped their kernel), setup falls back to the same flow but the install command is `sudo modprobe <module>` rather than DKMS install.
- **Cross-distro testing in CI.** ventd's CI tests synthesised `/etc/os-release` fixtures, not real distro images. Cross-distro HIL is Phoenix's manual responsibility (3 of his 7 boxes are different distros — Ubuntu/Fedora/Arch covers most cases).
- **Re-running step 3.5 after kernel upgrade.** If user upgrades their kernel and the DKMS module rebuilds correctly, no action needed. If DKMS rebuild fails, ventd detects "module not loaded" at next startup and shows a banner pointing to docs — not a setup re-run.

---

## §11 — Estimated CC implementation cost

This amendment lands as part of spec-12 PR 4. PR 4 base estimate per spec-12 §3: $15-25.

Adding step 3.5:
- ~150 LOC `setup_driver_check.go`
- ~80 LOC `setup_distro.go`
- ~60 LOC distro commands YAML (data, hand-curated)
- ~120 LOC `setup_pkexec.go` (SSE streaming reuses existing PR 4 calibration streaming)
- ~50 LOC `runtime_overrides.go`
- ~250 LOC test files (5 RULE-* subtests + table-driven cases)
- ~120 LOC `setup-driver-step.html` UI fragment
- ~80 LOC `setup.js` extension
- 5 rule files + polkit policy file + docs updates

**Increment to PR 4: $8-15.**

Revised PR 4 total: **$23-40**, vs original $15-25.

This is the price of the OOT-driver story. Without this amendment, ~5-10% of users hit a silent calibration failure they can't diagnose. With it, they get a clear path forward.

---

## §12 — Acceptance criteria for this amendment

This amendment is "done" when:
- [ ] Phoenix has read it and signed off on §3 step flow + §6 pkexec opt-in design.
- [ ] §5 distro matrix is reviewed for completeness (any common distro missing?).
- [ ] §8 invariants RULE-UI-SETUP-11..15 are agreed as the binding set.
- [ ] §10 "out of scope" list doesn't surface a missed scenario Phoenix considers in-scope.
- [ ] Cost increment to PR 4 ($8-15) is accepted.

**No code lands for this amendment until spec-12 PR 4 implementation begins.** It folds in as additional PR 4 scope; no separate PR.

---

## §13 — Cross-references

- `specs/spec-12-ui-redesign.md` — base spec this amendment extends.
- `specs/spec-11-firstrun-wizard.md` — original wizard spec, superseded.
- `specs/spec-03-amendment-schema-v1.1.md` — `unsupported` override semantics that inform telemetry-only fallback.
- `internal/hwdb/catalog/drivers/legion_hwmon.yaml` — primary OOT-driver case.
- `internal/diag/redactor/` — anonymisation primitives (not touched here).

---

## §14 — Summary

When setup matches a board needing an out-of-tree kernel module that isn't loaded, a new conditional step 3.5 appears between hardware confirmation and calibration. The user can: (a) install manually with copy-button command, (b) install via pkexec with one auth prompt, or (c) skip and run telemetry-only. Setup verifies module load post-install before advancing. Telemetry-only mode reuses scope-C's `unsupported: true` semantics via a runtime override file.

**Boundary held honestly:** "zero-terminal" remains the goal for ~90% of hardware (mainline drivers). For the OOT-driver minority, "one-prompt" is the v1.0 acceptable degradation. Not "zero-config" promises broken — the UX is honest about what the user is being asked to do, why, and how to skip if they prefer.

The catalog already labels this constraint via `dkms_required: true` and `kernel_module_source: out_of_tree`. Setup just makes the labeling user-visible at install time instead of failing silently at calibration time.
