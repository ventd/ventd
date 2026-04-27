# spec-15 — Experimental features framework (opt-in vendor-locked workarounds)

**Status:** DRAFT
**Target release:** v0.6.0 framework + first feature (AMD overdrive); v0.7.0 second wave (NVIDIA Coolbits, iLO4 unlock); v0.8.0+ as features mature
**Predecessors:** spec-03 PR 3 (catalog v1.1 with `overrides.unsupported`), spec-10 (doctor reporting surface), spec-12 (UI redesign — setup wizard host for opt-in flow)
**Successors:** none committed; future hardware-specific experimental features attach to this framework rather than introducing new gating mechanisms.
**Phoenix's box matrix coverage:** zero — every Tier 1 feature in this spec targets hardware Phoenix does not own. Validation is community-driven, gated by telemetry consent and the verification workflow (spec-13).

---

## 0. tl;dr

Four classes of vendor-locked hardware (Dell iDRAC9 ≥ 3.34, HPE iLO5/6, NVIDIA vBIOS-locked SKUs, AMD overdrive bit) have community-established workaround paths that are real, opinionated, and risky. ventd should not pretend these don't exist (dishonest to users who own this hardware) and should not enable them silently (violates user-trust model). spec-15 ships an **experimental features framework** that:

1. Adds an `experimental:` field to schema v1.2 with bound rules for declaration semantics.
2. Adds `--enable-<feature>` CLI flags following the spec-02 `--enable-corsair-write` pattern.
3. Adds a setup wizard page (extending spec-12 PR 4) that lists experimental features with one-line risk descriptions and links to detail docs.
4. Adds `ventd doctor` reporting of active experimental flags.
5. Defines a feature-promotion lifecycle (experimental → stable → deprecated) with explicit graduation criteria.

Three Tier 1 features ship under this framework in the order they're cheapest to implement and lowest-risk to validate:

- **F1 `amd_overdrive`** (v0.6.0) — kernel ppfeaturemask bit detection + sysfs PWM/SMU curve writes. Covers RDNA2/3/4. Lowest cost, widest user impact.
- **F2 `nvidia_coolbits`** (v0.7.0) — X11 Coolbits fallback for cards where NVML silently no-ops. Niche but documented.
- **F3 `ilo4_unlocked`** (v0.7.0) — detects upstream-patched iLO4 firmware (kendallgoto/ilo4_unlock) and uses the SSH `fan p N min/max` interface. Gen8/Gen9 only.

One Tier 2 feature is reserved for v0.8.0+ pending a Phoenix go/no-go decision:

- **F4 `idrac9_legacy_raw`** (deferred) — detects iDRAC9 < 3.34.34.34 and re-enables `ipmitool raw 0x30 0x30 0x02 0xff <pct>`. Carries CVE-2019-3705 disclosure burden. Should not ship without a clear user demand signal.

Three explicitly-rejected non-features are documented to prevent re-litigation:

- **N1** Supermicro lower-threshold zeroing (must be default, not opt-in — already in spec-03 scope-C).
- **N2** iLO5/iLO6 patched firmware (does not exist).
- **N3** GPU vBIOS flashing or iDRAC firmware downgrade automation (ventd never modifies firmware).

---

## 1. Background

### 1.1 Why experimental features at all

ventd's vision is "zero-config, any-hardware." For ~70% of homelab/NAS targets this is achievable through stable APIs (hwmon, NVML, IPMI on Supermicro, NBFC for laptops). For the remaining ~30%, vendors have either deliberately removed control (Dell 14G iDRAC9 ≥ 3.34) or never exposed it (HPE iLO5/6, Intel Arc on Linux, certain NVIDIA SKUs, AMD without the overdrive bit). The community has built workarounds — patched firmware, kernel cmdline flags, X11 Coolbits, IPMI raw-command exploits on old firmware. These are real and used by thousands of homelab operators.

ventd's three options:

1. **Ignore them.** Users see "Read-only mode" on hardware they know can be controlled and conclude ventd is incompetent.
2. **Adopt them silently.** A user upgrades ventd, kernel taints "out-of-spec" without warning, AMD support tells them they're on their own, ventd loses trust.
3. **Adopt them behind explicit opt-in with named risks.** User makes informed decision, ventd's reputation as the honest controller compounds.

spec-15 is option 3.

### 1.2 What is and isn't experimental

A feature is experimental if **all** of:
- It uses a vendor-unsupported, community-reverse-engineered, or kernel-tainting code path.
- The risk is non-trivial (warranty implications, security CVEs, hardware misbehavior, voided support contracts).
- A reasonable homelab user would want it but not have it enabled by default.
- Phoenix does not own the hardware to validate it directly (so promotion to stable depends on community telemetry).

A feature is **not** experimental if:
- It's the documented vendor API (HPE ThermalConfiguration, Dell sanctioned racadm). These ship as default.
- It's a community-derived but vendor-tolerated path (Supermicro raw IPMI commands, NBFC EC writes). These ship as default per their respective specs.
- The risk is informational only (e.g., "this command requires root" is not a risk, it's a precondition).

The distinction matters because experimental adds friction. Don't add friction where it isn't earned.

### 1.3 The graduation lifecycle

Every experimental feature carries a graduation criterion. Once met, the feature drops the `experimental:` flag and becomes default-on (or default-off-but-stable, depending on risk). Examples:

- `amd_overdrive` graduates to stable when ≥ 50 unique successful telemetry submissions across ≥ 3 RDNA generations land in the verification workflow.
- `ilo4_unlocked` graduates when ≥ 5 distinct DL-series models report 30+ days of stable operation.
- `nvidia_coolbits` graduates when the NVML-vs-Coolbits fallback path is exercised on ≥ 10 distinct PCI IDs without regression.

If a feature fails to graduate within 18 months of introduction, it's a candidate for deprecation. ventd does not carry experimental code indefinitely.

---

## 2. Goals and non-goals

### 2.1 Goals

- **G1.** Provide a single, consistent opt-in surface for all hardware-unlock features, so users don't have to learn N flag-styles for N vendors.
- **G2.** Make the risk of each feature legible at the moment of opt-in (CLI flag, wizard checkbox, doctor report) — not buried in a docs site.
- **G3.** Surface active experimental flags in `ventd doctor` and in diagnostic bundles (per spec-03 PR 2c redactor) so support discussions can reference them without back-and-forth.
- **G4.** Ship F1 (`amd_overdrive`) in v0.6.0 alongside spec-04 PI autotune. AMD users on RDNA3+ are a meaningful homelab population; current "we can't control your GPU fans" is a real gap.
- **G5.** Define graduation criteria up front, so experimental features don't accumulate as permanent technical debt.

### 2.2 Non-goals

- **NG1.** ventd does not write firmware. Not iLO firmware (user runs ilo4_unlock themselves), not iDRAC firmware (user accepts old version themselves), not GPU vBIOS (out of scope forever).
- **NG2.** ventd does not modify kernel cmdline, GRUB config, or `/etc/default/grub`. AMD overdrive requires `amdgpu.ppfeaturemask` set by the user; ventd detects the bit's presence and acts accordingly.
- **NG3.** ventd does not unlock SecureBoot, sign DKMS modules, or touch MOK. Out-of-tree kernel modules are the user's problem (NBFC's `acpi_ec` per spec-09 is the precedent).
- **NG4.** No Tier 2 features ship without a Phoenix go/no-go gate. F4 `idrac9_legacy_raw` is documented in spec-15 but does not have implementation tasks until Phoenix explicitly approves.
- **NG5.** No "expert mode" or "advanced settings" hand-wavy bucket. Every experimental feature is explicitly named and individually toggled.

---

## 3. Architecture

### 3.1 Schema v1.2 addition

`internal/hwdb/schema.go` (canonical schema source — schema v1.1 amendment lives in spec-03-amendment-schema-v1_1.md):

```yaml
# Existing v1.1 fields unchanged. New top-level field:

experimental:
  amd_overdrive: bool             # F1 — defaults false
  nvidia_coolbits: bool           # F2 — defaults false
  ilo4_unlocked: bool             # F3 — defaults false
  idrac9_legacy_raw: bool         # F4 — defaults false; reserved for v0.8.0+

# Per-board catalog entries can declare which experimental features they require:

  experimental_required:          # NEW — board lists experimental features this profile depends on
    - amd_overdrive               # e.g. RX 7900 XTX entry; without overdrive, board is read-only
```

The validator (per RULE-SCHEMA-08, schema v1.1) treats unknown experimental keys as errors, not warnings. Adding a new experimental feature requires a schema bump (v1.3 etc.) — this prevents typo-fueled silent acceptance.

### 3.2 Runtime gating

`internal/experimental/` (new package):

```
internal/experimental/
├── flags.go          # struct ExperimentalFlags { AMDOverdrive bool, NVIDIACoolbits bool, ... }
├── flags_test.go
├── precondition.go   # detects whether each feature's preconditions are met
├── precondition_test.go
└── doctor.go         # doctor-report integration
```

`flags.go`:

```go
type ExperimentalFlags struct {
    AMDOverdrive     bool
    NVIDIACoolbits   bool
    ILO4Unlocked     bool
    IDRAC9LegacyRaw  bool
}

func ParseFromCLI(args []string) ExperimentalFlags { ... }
func ParseFromConfig(cfg *Config) ExperimentalFlags { ... }
func (f ExperimentalFlags) Active() []string { ... }  // for doctor reporting
```

CLI flags (matching spec-02 `--enable-corsair-write` precedent):
- `--enable-amd-overdrive`
- `--enable-nvidia-coolbits`
- `--enable-ilo4-unlocked`
- `--enable-idrac9-legacy-raw` (Tier 2, gated)

Config-file equivalent in `/etc/ventd/ventd.yaml`:

```yaml
experimental:
  amd_overdrive: true
  nvidia_coolbits: false
```

Per RULE-EXPERIMENTAL-FLAG-PRECEDENCE, CLI flags override config-file values; config-file overrides daemon defaults; daemon defaults are all `false`.

### 3.3 Backend integration

Each experimental feature lives in its respective HAL backend, gated by a flag check:

```go
// internal/hal/amdgpu/backend.go
func (b *AMDGPUBackend) Calibrate(...) {
    if !b.flags.AMDOverdrive {
        return CalibrationResult{
            Capability: ReadOnly,
            Reason:     "AMD GPU fan control requires --enable-amd-overdrive (sets amdgpu.ppfeaturemask kernel taint)",
        }, nil
    }
    // ... actual calibration
}
```

The Reason string is consumed by the UI per spec-12 to display a one-line "this is read-only because X" banner.

### 3.4 Precondition detection

`precondition.go` checks whether each feature's preconditions are actually met at runtime:

- `amd_overdrive`: parse `/proc/cmdline` for `amdgpu.ppfeaturemask=`, check bit 14 (`0x4000`). If flag is enabled but bit isn't set, ventd refuses to write and logs an actionable error.
- `nvidia_coolbits`: check `$DISPLAY` is set, check `nvidia-settings` is in PATH, check Coolbits XConfig setting via `nvidia-settings -q` (read-only query). If flag is enabled but X is unavailable, ventd logs and falls back to NVML.
- `ilo4_unlocked`: SSH to iLO host, send `fan p 0 min` query, check for non-error response. The `fan` command is only present in patched firmware.
- `idrac9_legacy_raw`: query iDRAC firmware version via Redfish, check `< 3.34.34.34`. If flag is enabled but firmware is post-block, ventd refuses with explanation.

Each precondition check has a bound rule (RULE-EXPERIMENTAL-PRECONDITION-* per feature).

### 3.5 Doctor and diag-bundle integration

`ventd doctor` reports experimental flags:

```
Experimental features:
  ✓ amd_overdrive       (active, ppfeaturemask bit 0x4000 detected)
  ✗ nvidia_coolbits     (not enabled)
  ✗ ilo4_unlocked       (not enabled)
  ✗ idrac9_legacy_raw   (not enabled)
```

Diag bundle (per spec-03 PR 2c redactor pipeline) includes a redacted snapshot:

```json
{
  "experimental_flags_active": ["amd_overdrive"],
  "experimental_preconditions_met": {
    "amd_overdrive": true,
    "ppfeaturemask_value": "0xfffd7fff"
  }
}
```

No CVE disclosures or warning text are reproduced in the diag bundle — the bundle is for support diagnosis, not for policy litigation.

### 3.6 Setup wizard integration (spec-12 PR 4 amendment)

spec-12 PR 4's setup flow gains an "Experimental features" page after device calibration and before completion:

```
Step 4 of 5: Experimental features (optional)

These features unlock fan control on hardware where vendors have
locked or restricted access. They may carry warranty, security, or
support implications. Each is off by default.

[ ] AMD GPU overdrive
    Enables fan control on RX 6000/7000/9000 series GPUs.
    Requires: amdgpu.ppfeaturemask=0xffffffff in kernel cmdline.
    Trade-off: kernel marked "out-of-spec" on Linux 6.14+. AMD will
    not provide support for issues on tainted kernels. [Learn more]

[ ] NVIDIA Coolbits (X11 only)
    Fallback for NVIDIA cards where the modern API silently fails.
    Requires: X11 session with active display. Does not work on Wayland.
    [Learn more]

[ ] HPE iLO4 patched firmware
    Enables direct fan control on Gen8/Gen9 ProLiant servers running
    the kendallgoto/ilo4_unlock patched iLO4 firmware.
    Requires: user has flashed patched iLO4 v2.77 manually; iLO security
    override switch enabled.
    Trade-off: voids HPE support contract. ventd does not flash firmware. [Learn more]

[Continue without enabling]   [Enable selected]
```

[Learn more] links to ventd docs (`/docs/experimental/<feature>.md`) which contain the full risk text, citations to vendor statements (CVE numbers for iDRAC, kernel commits for AMD), and step-by-step preparation instructions.

Per RULE-UI-SETUP-EXPERIMENTAL-01, the wizard cannot enable any experimental feature without the user clicking through the [Learn more] link at least once per feature, tracked client-side. This is a friction-on-purpose mechanism.

---

## 4. Tier 1 features

### 4.1 F1 — AMD overdrive (`amd_overdrive`)

**Target:** v0.6.0 (alongside spec-04 PI autotune so AMD GPUs join the autotune target list).

**Hardware coverage:**
- RDNA2 (RX 6400 → RX 6950 XT): single-PWM `pwm1`/`pwm1_enable` interface
- RDNA3 (RX 7600 → RX 7900 XTX): SMU fan-curve interface (`gpu_od/fan_*`)
- RDNA4 (RX 9070, RX 9070 XT): SMU curve, requires kernel ≥ 6.15
- Workstation Pro W6000/W7000: same as consumer counterparts

**Out of scope:**
- MI200/MI250/MI300/MI325 datacenter (passively cooled OAM modules; chassis fans are BMC territory per spec-09 framework)
- APUs (no discrete fan; ventd reads temp only)

**Precondition:**
- Kernel cmdline contains `amdgpu.ppfeaturemask=` with bit 14 (`0x4000`) set
- Recommended user value: `amdgpu.ppfeaturemask=0xffffffff` (all bits) — covers OD, manual fan, manual clocks

**Detection logic** (`precondition.go`):
```go
func DetectAMDOverdrive() (enabled bool, mask uint32, err error) {
    cmdline, err := os.ReadFile("/proc/cmdline")
    if err != nil { return false, 0, err }
    re := regexp.MustCompile(`amdgpu\.ppfeaturemask=(0x[0-9a-fA-F]+|\d+)`)
    matches := re.FindStringSubmatch(string(cmdline))
    if matches == nil { return false, 0, nil }
    val, err := strconv.ParseUint(matches[1], 0, 32)
    if err != nil { return false, 0, err }
    return (val & 0x4000) != 0, uint32(val), nil
}
```

**Risk disclosure (must appear in wizard, doctor, and docs):**

> Linux kernel 6.14+ marks the kernel "out-of-spec" tainted when amdgpu.ppfeaturemask enables overdrive (commit b472b8d829c1). AMD will not provide support for issues encountered on a tainted kernel. ventd's overdrive feature does not modify your kernel cmdline; you must add the parameter yourself. ventd reports the taint state in `ventd doctor`.

**Bound rules:**
- `RULE-EXPERIMENTAL-AMD-OVERDRIVE-01`: backend refuses to write `pwm1`/SMU curve when flag is false.
- `RULE-EXPERIMENTAL-AMD-OVERDRIVE-02`: precondition check fails actionable when flag is true but `ppfeaturemask` bit unset.
- `RULE-EXPERIMENTAL-AMD-OVERDRIVE-03`: doctor reports active state and ppfeaturemask value.
- `RULE-EXPERIMENTAL-AMD-OVERDRIVE-04`: RDNA4 path refuses to operate on kernel < 6.15 with actionable error.

**Graduation criterion:** ≥ 50 unique successful telemetry submissions across ≥ 3 RDNA generations within 12 months. On graduation, `amd_overdrive` becomes default-on if AMD GPU detected and ppfeaturemask is set; users still need the kernel cmdline themselves.

### 4.2 F2 — NVIDIA Coolbits (`nvidia_coolbits`)

**Target:** v0.7.0 (after F1 ships and the framework is validated).

**Hardware coverage:**
- Cards where NVML returns success but vBIOS silently no-ops (RTX 2070 Super FE on driver 550–580 confirmed; other Pascal/Turing SKUs suspected)
- Workstation cards on X11 systems with no Wayland migration plan

**Out of scope:**
- Wayland-only systems (Coolbits requires X11, full stop)
- Headless servers without `Xvfb`/dummy plug (technically possible, brittle, document but don't recommend)
- Cards where NVML works (use NVML, not Coolbits — flag is for fallback only)

**Precondition:**
- `$DISPLAY` is set
- `nvidia-settings` is in PATH
- ventd has X authority (root-priv X since driver 465; `xhost +SI:localuser:root` or run ventd as user with X access)

**Detection logic:**
```go
func DetectNVIDIACoolbits() (available bool, err error) {
    if os.Getenv("DISPLAY") == "" { return false, nil }
    _, err := exec.LookPath("nvidia-settings")
    if err != nil { return false, nil }
    cmd := exec.Command("nvidia-settings", "-q", "[gpu:0]/CoolBits")
    out, err := cmd.Output()
    if err != nil { return false, err }
    // Parse output for non-zero CoolBits value
    return strings.Contains(string(out), ": "), nil
}
```

**Risk disclosure:**

> NVIDIA Coolbits is the legacy X11-based fan control mechanism. It works on cards where the modern NVML API silently fails (vBIOS-locked SKUs). Wayland systems and headless servers without a virtual framebuffer cannot use this path. ventd will fall back to NVML automatically if Coolbits is unavailable.

**Bound rules:**
- `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-01`: backend uses Coolbits only when flag is true AND NVML write returned success-but-no-effect (detected via fan-RPM read-back).
- `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-02`: precondition check fails actionable when flag is true but X is unavailable.
- `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-03`: doctor reports last NVML write outcome per device (success-effective / success-noeffect / failure).

**Graduation criterion:** Coolbits fallback path exercised on ≥ 10 distinct PCI IDs without regression. On graduation, the fallback becomes automatic — no opt-in needed — because by that point ventd has confidence the heuristic is right.

### 4.3 F3 — HPE iLO4 unlocked (`ilo4_unlocked`)

**Target:** v0.7.0 (with F2; both are X11/legacy-flavored and share UI affordance).

**Hardware coverage** (per kendallgoto/ilo4_unlock README, validated):
- DL360p Gen8, DL380p Gen8/Gen9, DL80 Gen9, SL4540 Gen8, ML350 Gen9
- Other Gen8/Gen9 ProLiant with iLO4 v2.77 patched firmware likely work; community-reported

**Out of scope:**
- Gen10+ (no patched firmware exists; spec-15 explicitly does not promise this)
- Gen7 or older (iLO3 / pre-Redfish; not a meaningful target)

**Precondition:**
- iLO accessible via SSH from ventd host
- iLO firmware is patched v2.77 (detection: SSH `fan p 0 min` returns numeric, not "command not found")
- iLO security override switch is enabled (user action; ventd cannot detect this remotely)

**Detection logic:**
```go
func DetectILO4Unlocked(host, user, key string) (unlocked bool, err error) {
    out, err := sshExec(host, user, key, "fan p 0 min")
    if err != nil { return false, err }
    // Patched firmware returns "fan p 0 min: <value>"; stock returns "Unknown command: fan"
    return strings.Contains(out, "fan p 0 min:"), nil
}
```

**Risk disclosure:**

> The ilo4_unlock project (kendallgoto/ilo4_unlock, 520★) provides patched iLO4 v2.77 firmware that exposes per-fan SSH commands. Flashing requires the iLO security override switch and voids HPE support. ventd does not flash firmware — you must flash the patched firmware yourself following the upstream project's instructions. Chained `fan` commands separated with `;` are known to crash iLO until power-cycle; ventd issues commands sequentially.

**Bound rules:**
- `RULE-EXPERIMENTAL-ILO4-UNLOCK-01`: backend uses SSH `fan p N min/max` only when flag is true and SSH probe confirms patched firmware.
- `RULE-EXPERIMENTAL-ILO4-UNLOCK-02`: backend never sends chained commands (no `;` in any SSH payload).
- `RULE-EXPERIMENTAL-ILO4-UNLOCK-03`: precondition check fails actionable when flag is true but SSH probe fails (wrong firmware, wrong credentials, network).
- `RULE-EXPERIMENTAL-ILO4-UNLOCK-04`: doctor reports detected iLO firmware version and patched/stock state.

**Graduation criterion:** ≥ 5 distinct DL-series models report 30+ days of stable operation. Graduation does not flip default-on (the firmware-flash precondition prevents that) — graduation just removes the experimental warning text and moves the feature to a normal "advanced backends" section.

### 4.4 F4 — Reserved: iDRAC9 legacy raw (`idrac9_legacy_raw`)

**Target:** v0.8.0+, **gated by Phoenix go/no-go decision before any implementation work.**

**Reason for deferral:** the user population is small (homelabbers running pre-3.34.34.34 iDRAC9 firmware in 2026 must have either dodged Dell's RoT-blocked downgrades since 2021 or never updated). Most are aware of CVE-2019-3705 risk. ventd shipping support for this hardware is correct in principle but creates a CVE disclosure surface that is meaningful effort to maintain.

**If approved:** implementation follows F1/F2/F3 framework (CLI flag, precondition check, bound rules, doctor reporting). The CVE disclosure is hard-coded in the wizard text and re-displayed every time the flag is toggled.

---

## 5. Out of scope (explicit non-goals)

### 5.1 N1 — Supermicro lower-threshold zeroing is not experimental

Supermicro IPMI fan threshold defaults are wrong for any aftermarket fan. Setting `ipmitool sensor thresh FANN lower 0 100 200` is the **correct** behavior, not opt-in. spec-03 scope-C catalog already declares this per-board. spec-15 does **not** add this as an experimental feature; the catalog entry is canonical.

### 5.2 N2 — iLO5/iLO6 patched firmware does not exist

There is no community-patched iLO5 or iLO6 firmware that adds fan control. The boot verification on iLO5/6 SoCs is stronger than iLO4's, and no GitHub project as of 2026 has produced a working patch. spec-15 will not list `ilo5_unlocked` or `ilo6_unlocked` as future features. If this changes, a future spec adds it.

### 5.3 N3 — Firmware modification is permanently out of scope

ventd does not flash iLO firmware, iDRAC firmware, GPU vBIOS, BMC firmware on any platform, or motherboard BIOS/UEFI. The user runs upstream tools (HPE SUM, Dell racadm, AMDVbFlash, NVIDIA nvflash) themselves. ventd may **detect** that the user has done so and use the resulting capability, but it never modifies firmware. This is a permanent invariant.

### 5.4 N4 — No "expert mode" bucket

Every experimental feature is named, individually toggled, and individually disclosed. ventd does not have a single "advanced settings" or "expert mode" toggle that bundles features together. The friction of per-feature opt-in is intentional.

---

## 6. Failure modes and recovery

### 6.1 Flag enabled, precondition fails

User enables `--enable-amd-overdrive` but kernel cmdline doesn't have ppfeaturemask. ventd:
1. Logs an error at startup with the exact missing kernel cmdline parameter
2. Refuses to enter the experimental code path (treats AMD GPU as read-only)
3. `ventd doctor` reports "amd_overdrive: enabled but precondition unmet (ppfeaturemask=0x4000 not set in /proc/cmdline)"
4. UI shows "AMD overdrive enabled but kernel parameter missing — see [link]"

No silent fallback to the default code path with the experimental flag still claimed-active. Either the precondition is met or the feature is inactive.

### 6.2 Patched firmware reverts during operation

User has iLO4 patched firmware enabled, runs ventd successfully, then HPE pushes an automated firmware update that overwrites the patch. ventd's next iLO probe returns "Unknown command: fan". ventd:
1. Logs the precondition regression
2. Falls back to read-only telemetry mode
3. `ventd doctor` flags the regression
4. UI shows banner: "iLO4 unlocked feature lost — firmware appears to have been overwritten"

### 6.3 Kernel update breaks AMD overdrive

User upgrades from kernel 6.13 to 6.14, kernel taint warning appears in dmesg. ventd does not change behavior (the bit is still set), but `ventd doctor` adds a notice: "Kernel ≥ 6.14 marks overdrive 'out-of-spec' tainted; AMD support unavailable on tainted kernels."

### 6.4 User disables flag mid-operation

User starts ventd with `--enable-amd-overdrive`, GPU runs at custom curve. User restarts without the flag. ventd:
1. Restores AMD GPU to auto mode (`pwm1_enable=2` for RDNA2, equivalent SMU reset for RDNA3+) per the standard `exit_behaviour: restore_auto` invariant
2. Reads-only thereafter

This is the same restore semantics as spec-09 EC fans on shutdown.

---

## 7. Bound rules summary

| Rule | Description | Subtest |
|---|---|---|
| `RULE-EXPERIMENTAL-FLAG-PRECEDENCE` | CLI > config-file > daemon-default | `TestFlags_PrecedenceCLIOverConfig` |
| `RULE-EXPERIMENTAL-SCHEMA-VALIDATION` | unknown experimental keys fail validation | `TestSchema_UnknownExperimentalKeyRejected` |
| `RULE-EXPERIMENTAL-DOCTOR-REPORTING` | doctor lists all flags with active/precondition state | `TestDoctor_ReportsExperimentalFlags` |
| `RULE-EXPERIMENTAL-DIAG-INCLUSION` | diag bundle includes flag state per redactor pipeline | `TestDiag_IncludesExperimentalFlags` |
| `RULE-EXPERIMENTAL-AMD-OVERDRIVE-01` | refuse PWM/SMU write when flag false | `TestAMD_OverdriveRefusedWhenFlagFalse` |
| `RULE-EXPERIMENTAL-AMD-OVERDRIVE-02` | actionable error when flag true but bit unset | `TestAMD_OverdriveActionableWhenBitMissing` |
| `RULE-EXPERIMENTAL-AMD-OVERDRIVE-03` | doctor includes ppfeaturemask value | `TestDoctor_IncludesPPFeatureMask` |
| `RULE-EXPERIMENTAL-AMD-OVERDRIVE-04` | RDNA4 refuses on kernel < 6.15 | `TestAMD_RDNA4RefusesOldKernel` |
| `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-01` | use Coolbits only as NVML fallback | `TestNVIDIA_CoolbitsUsedOnlyAsFallback` |
| `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-02` | actionable error when flag true but X unavailable | `TestNVIDIA_CoolbitsActionableNoX` |
| `RULE-EXPERIMENTAL-NVIDIA-COOLBITS-03` | doctor reports NVML write effectiveness per device | `TestDoctor_NVMLEffectivenessPerDevice` |
| `RULE-EXPERIMENTAL-ILO4-UNLOCK-01` | SSH fan command only when flag true and probe confirms | `TestILO4_FanCommandsGated` |
| `RULE-EXPERIMENTAL-ILO4-UNLOCK-02` | never chain SSH commands with semicolon | `TestILO4_NoChainedCommands` |
| `RULE-EXPERIMENTAL-ILO4-UNLOCK-03` | actionable error when flag true but probe fails | `TestILO4_ActionableProbeFailure` |
| `RULE-EXPERIMENTAL-ILO4-UNLOCK-04` | doctor reports detected iLO firmware patched/stock | `TestDoctor_ILO4FirmwareState` |
| `RULE-UI-SETUP-EXPERIMENTAL-01` | wizard requires Learn-more click before enable | `TestSetupUI_LearnMoreRequired` |

17 rules total, F4 reserved (no rules drafted until go/no-go).

---

## 8. PR sequencing

Conservative PR layout, costs annotated per spec-cost-calibration patterns:

**v0.6.0 (spec-04 + spec-15 framework + F1):**
- **PR 1 — framework scaffolding:** `internal/experimental/` package, schema v1.2 amendment, CLI flag plumbing, doctor reporting hooks. No backend integration yet. ~$5-8 Sonnet.
- **PR 2 — F1 AMD overdrive:** RDNA2/3/4 backend integration, precondition detection, bound rules, subtests. ~$10-15 Sonnet.
- **PR 3 — F1 docs:** `/docs/experimental/amd-overdrive.md` with risk disclosure and kernel cmdline guide. ~$2-3 Haiku.

v0.6.0 total: **$17-26**, comfortably under $30 spec target.

**v0.7.0 (spec-15 wave 2):**
- **PR 4 — F2 NVIDIA Coolbits:** backend integration, precondition, bound rules. ~$8-12 Sonnet.
- **PR 5 — F3 iLO4 unlocked:** SSH client wrapper (reuses existing IPMI session pattern), precondition probe, bound rules. ~$10-15 Sonnet.
- **PR 6 — wizard integration:** spec-12 PR 4 amendment, experimental-features step. ~$5-8 Sonnet.
- **PR 7 — F2/F3 docs:** two doc pages. ~$3-5 Haiku.

v0.7.0 total: **$26-40**.

**v0.8.0 (Tier 2, gated):**
- **PR 8 — F4 iDRAC9 legacy raw (if approved):** backend integration plus the CVE disclosure surface. ~$8-12 Sonnet plus ~$3-5 docs Haiku.

If F4 is rejected, spec-15 has no v0.8.0 work item.

**Budget total across spec-15 lifetime: $43-78** (without F4) or $54-95 (with F4). Spread across 2-3 releases this is well within $300/month CC target.

---

## 9. Verification workflow integration (spec-13)

Each Tier 1 feature graduation depends on telemetry. spec-13's verification workflow already accepts diagnostic bundles. spec-15 adds:

- A `experimental_telemetry` field to bundles documenting which flags were active and whether the feature operated as expected (binary: did fan RPM follow PWM input within calibration tolerance?).
- A graduation dashboard in the verification repo showing per-feature counts toward graduation criteria.
- Per-PCI-ID success/failure tables for AMD GPUs, NVIDIA cards, and iLO4-patched HPE models.

This is the same shape as spec-03's catalog telemetry and reuses spec-13's submission infrastructure.

---

## 10. Migration and backwards compatibility

Schema v1.2 adds the `experimental:` field to per-board entries. v1.0 and v1.1 catalog entries without the field continue to work — the field defaults to all-false. No catalog entries need to be updated when spec-15 ships.

CLI flag deprecation policy: the existing `--enable-corsair-write` flag (spec-02 v0.4.0) is **not** retroactively folded into the experimental framework. Corsair writes are not experimental — they are a deliberate user opt-in for HID safety reasons, not for vendor-lock workarounds. The flag stays where it is. Future write-gating flags should follow the experimental pattern only if they fit the §1.2 criteria.

---

## 11. Open questions

- **Q1.** Should `--enable-amd-overdrive` automatically include a kernel-taint warning in the systemd journal at startup? Currently spec says ventd reports the taint in `doctor` but doesn't log it on every boot. Argument for logging: visibility. Argument against: noise on every restart for users who already accepted the trade-off. **Recommend:** log once at startup at INFO, suppress on subsequent restarts within 24h.

- **Q2.** Should F2 NVIDIA Coolbits be off by default if NVML works? The current spec says yes (Coolbits is a fallback for cards where NVML silently fails). But this requires fan-RPM read-back to detect "success-no-effect," which adds 5-10 seconds to calibration. **Recommend:** yes, off by default, fall-back path triggered only on detected NVML no-op.

- **Q3.** Does F3 iLO4 unlocked need its own credential storage, or does it share spec-01's IPMI credential pool? **Recommend:** share — iLO uses IPMI on TCP/623 and SSH on TCP/22 with the same admin credentials. Reuse spec-01's credential abstraction.

- **Q4.** F4 idrac9_legacy_raw — should ventd refuse to enable the flag if iDRAC is reachable from the public internet? Detecting "public" is non-trivial but a heuristic ("not RFC1918 source for the iDRAC management interface") is achievable. **Recommend defer:** the user enabling pre-3.34 firmware is implicitly accepting CVE risk; ventd refusing to start because of network topology is overreach. Document the recommendation in the wizard text instead.

Phoenix to resolve Q1-Q3 before PR 1 lands. Q4 defers with F4.

---

## 12. References

- Cross-vendor research report (project file `2026-04-firmware-locked-fan-control-research.md` from this chat) — primary source for risk disclosures and feature scope.
- spec-02 §`--enable-corsair-write` — opt-in flag pattern precedent.
- spec-03 amendment schema v1.1 — overrides framework that experimental extends.
- spec-09 NBFC integration — partner-daemon detection pattern reused in F3 ilo4_unlock.
- spec-10 doctor — reporting surface.
- spec-12 PR 4 setup wizard — UI host for opt-in flow.
- spec-13 verification workflow — telemetry path for graduation criteria.
- kendallgoto/ilo4_unlock — upstream patched-firmware project (F3).
- Linux kernel commit b472b8d829c1 — AMD ppfeaturemask taint introduction (F1).
- CVE-2019-3705 — iDRAC9 unauthenticated RCE (F4 risk disclosure).

---
