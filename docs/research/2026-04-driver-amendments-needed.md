# 2026-04 — Driver Catalog Amendments Needed for Scope-C

**Status:** §SCHEMA-BIOSVER, §SCHEMA-DT, §HP-CONSUMER, §LEGION-1, and §IPMI-1
delivered in spec-03 PR 4 (schema v1.1) and PR 5 (scope-C catalog seed).
§FW-1 (Framework) remains open — requires new ventd backend, not a catalog
amendment. See §FW-1 below.

**Audience:** Phoenix + future Claude Code spec implementation chats.

**Bound spec section:** spec-03 §11 (driver catalog), spec-03 amendment
PWM-controllability, spec-01 IPMI polish.

---

## Summary table

| Amendment | Type | Schema impact | Spec | Status |
|---|---|---|---|---|
| §LEGION-1: legion_hwmon driver entry | NEW driver YAML | YES — bios_version field | spec-03 amend | ✅ DELIVERED PR 5 |
| §FW-1: cros_ec_lpcs driver entry | NEW driver YAML | NO | spec-03 amend | ⏳ open — needs new backend |
| §IPMI-1: ipmi_bmc driver entry | NEW driver YAML | NO | spec-01 / spec-03 | ✅ DELIVERED PR 5 |
| §SCHEMA-DT: device-tree fingerprint | Schema-v1.1 | YES — dt fields | spec-03 amend | ✅ DELIVERED PR 4 |
| §SCHEMA-BIOSVER: bios_version field | Schema-v1.1 | YES — top-level | spec-03 amend | ✅ DELIVERED PR 4 |
| §HP-CONSUMER: 'unsupported' marker | Tier-3 fallback | minor | spec-03 amend | ✅ DELIVERED PR 4 |

P1 = blocks Legion/IPMI scope-C, ship before scope-C tag.
P2 = improves coverage, can ship in v0.6.0.
P3 = polish.

---

## §SCHEMA-BIOSVER — Add `bios_version` to fingerprint

**Why:** Lenovo Legion laptops are dispatched on **DMI BIOS_VERSION
prefix** (4-character family code: GKCN, EUCN, J2CN, LPCN, M3CN, NMCN,
RLCN, Q6CN, etc.) — NOT product_name or board_name. Without this
field, the existing tier-1 fingerprint cannot disambiguate Legion
generations.

**Evidence:**

```c
// LenovoLegionLinux/kernel_module/legion-laptop.c
static const struct dmi_system_id optimistic_allowlist[] = {
    {
        // Generation 6: Legion 5/5pro/7 family
        .ident = "GKCN",
        .matches = {
            DMI_MATCH(DMI_SYS_VENDOR, "LENOVO"),
            DMI_MATCH(DMI_BIOS_VERSION, "GKCN"),
        },
        .driver_data = (void *)&model_v0
    },
    // ...repeats for EUCN, EFCN, J2CN, M3CN, LPCN, NMCN...
};
```

Per kernel msg in issue #84: `legion PNP0C09:00: Read identifying
information: DMI_SYS_VENDOR: LENOVO; DMI_PRODUCT_NAME: 82WM;
DMI_BIOS_VERSION:LPCN42WW` — note `LPCN42WW` is a 4-char family
prefix + minor version.

**Schema change:** Bump fingerprint schema to v1.1. Add
`bios_version` field (optional, glob-supported with `*` suffix). Match
semantics: substring/prefix per existing tier-1 logic.

```yaml
dmi_fingerprint:
  sys_vendor: "LENOVO"
  product_name: "*"
  board_vendor: "*"
  board_name: "*"
  board_version: "*"
  bios_version: "GKCN*"   # NEW — matches GKCN26WW, GKCN31WW, etc
```

**Migration:** Existing entries don't set `bios_version` → defaults to
`*` → no behavior change.

**Tests:**
- RULE-FINGERPRINT-04 (new): DMI bios_version glob match works.
- RULE-FINGERPRINT-05 (new): fingerprint without bios_version still matches as before.

---

## §LEGION-1 — `legion_hwmon.yaml` driver entry

**Driver source:** `johnfanv2/LenovoLegionLinux` (DKMS, OOT — NOT mainline
as of kernel 6.13). Module name `legion_laptop`. Some upstreaming
work in progress on lkml.

**Path category:** Out-of-tree DKMS. Same pattern as `nct6687d`
(Fred78290). Driver YAML must carry `kernel_module_source: out_of_tree`
+ `repository:` + `dkms_required: true`.

**Capabilities exposed (10-point firmware curve — KEY for spec-05
P4-HWCURVE):**

```
/sys/kernel/debug/legion/fancurve     (RW debugfs — 10 temp+pwm points)
/sys/module/legion_laptop/drivers/platform:legion/PNP0C09:00/powermode
                                       (RW — 0/1/2/? for balanced/perf/quiet/custom)
/sys/.../platformprofile               (RW — same as powermode but via std platform_profile API)
/sys/.../mini_fancurve_enabled         (RW — bool — secondary low-temp curve override)
/sys/.../lockfancontroller             (RW — bool — freezes fan to current state)
```

**Auth method enum:** ACCESS_METHOD_EC, ACCESS_METHOD_ACPI,
ACCESS_METHOD_WMI, ACCESS_METHOD_WMI2, ACCESS_METHOD_WMI3 — varies
per generation. Not user-visible; driver picks. Some generations have
ec_readonly mode where fan curve can be READ but not written.

**Not all features available on all models:** features bitfield
includes `fancurve / powermode / platformprofile / minifancurve`. Some
boards omit minifancurve. Driver exposes feature flags via
`/sys/kernel/debug/legion/`.

**EC chip ID variance:** 5507, 5508, 8227 observed across generations.
This is internal to the driver but matters for fan-curve schema
compat — newer chips support extended fan curve formats.

**Proposed driver YAML skeleton:**

```yaml
driver:
  name: "legion_hwmon"
  module: "legion_laptop"
  kernel_module_source: "out_of_tree"
  repository: "https://github.com/johnfanv2/LenovoLegionLinux"
  license: "GPL-2.0-only"
  dkms_required: true
  mainline_status: "not-mainline-2026-04"
  capabilities:
    - "fancurve_10point"      # P4-HWCURVE prior art
    - "powermode_4state"
    - "platformprofile"
    - "minifancurve"          # optional per model
    - "lockfancontroller"
  sysfs_paths:
    fancurve: "/sys/kernel/debug/legion/fancurve"
    powermode: "/sys/module/legion_laptop/drivers/platform:legion/PNP0C09:00/powermode"
    platformprofile: "/sys/firmware/acpi/platform_profile"
  defaults:
    curves: []
  notes: "OOT DKMS driver. Userspace must install via package manager
    or COPR (Fedora). Driver enforces a per-generation BIOS_VERSION
    allowlist; entries outside allowlist refuse to load (error -12)
    unless force=1 modparam set."
```

**Not in scope here:** the actual 10-point fancurve schema for spec-05
P4-HWCURVE design. That's a future chat. This entry only registers
the driver.

---

## §FW-1 — `cros_ec_lpcs.yaml` driver entry (Framework)

**Correction from initial scoping:** `cros_ec_lpcs` is the LPC bus
glue, NOT the fan-control driver itself. Framework laptops expose fan
control through a stack:

```
cros_ec_lpcs       (LPC bus shim — module: cros_ec_lpcs)
  └── cros_ec_dev  (chardev /dev/cros_ec — module: cros_ec_dev)
        └── cros_ec_sensorhub (sensors — module: cros_ec_sensorhub)
        └── EC commands (fan control via ec_command() ioctls)
```

There is **no hwmon path** for Framework fan control as of kernel
6.13 mainline. Fan reads work via `ectool --interface=lpc fanduty get`
(userspace ChromiumOS ectool ported to Linux), but no kernel sysfs
hwmon entry exposes fan PWM as writable channel.

**Path forward:** ventd would need a custom Framework backend that
opens `/dev/cros_ec` and issues `EC_CMD_PWM_SET_FAN_DUTY` commands
directly. This is materially different from hwmon — it's the IPMI/AIO
shape (custom backend), not a driver entry.

**Implication:** Framework support is **not a driver-catalog
amendment** — it's a **new ventd backend (ec_command)**. Defer to
v0.7.0+. Out of scope for spec-03 driver catalog.

**Alternative path:** spec-09 NBFC integration. NBFC has Framework
laptop profiles that could flow through that integration once it
ships v0.8.0.

---

## §IPMI-1 — `ipmi_bmc.yaml` driver entry

**Why this is a "driver" in the catalog sense:** The board catalog
already references `chip: "ipmi_bmc"` (Supermicro X11SCH-F, H12SSL-i in
this scope-B drop). Without a driver YAML defining capabilities, the
matcher can't dispatch.

**Spec linkage:** spec-01 IPMI polish. Once spec-01 ships v0.3.x, the
IPMI backend exists. The driver YAML formalizes its presence in the
catalog so board entries can reference it.

**Proposed minimal entry:**

```yaml
driver:
  name: "ipmi_bmc"
  module: "ipmi_si"           # plus ipmi_devintf for /dev/ipmi0
  kernel_module_source: "mainline"
  mainline_status: "mainline-since-2.6"
  capabilities:
    - "fan_pwm_via_ipmi_raw"
    - "fan_tach_via_ipmi_sensors"
    - "temperature_via_ipmi_sensors"
  device_path: "/dev/ipmi0"
  conflicts_with_userspace:
    - "ipmitool"
    - "freeipmi"
    - "OpenManage Server Administrator"
  notes: "Backend for any board with on-die or socketed BMC (Supermicro
    AST2x00, Dell iDRAC, HP iLO, IBM/Lenovo IMM). ventd should prefer
    this backend over hwmon Super-I/O when both are available, because
    BMCs typically override hwmon writes within seconds.
    Vendor-specific raw IPMI commands required for fan PWM control —
    NOT covered by generic IPMI sensor read interface. See spec-01 for
    Supermicro-specific raw commands; Dell/HP profile work deferred."
```

**Server-board catalog dependency:** With this driver YAML in place,
we can add Dell PowerEdge R740, HPE ProLiant DL380, full Supermicro
range, IBM x3550 etc as scope-C boards.

---

## §SCHEMA-DT — Device-tree systems (no DMI)

**Why:** Raspberry Pi 5 scope-B entry synthesizes a fingerprint from
`/proc/device-tree/model` because no DMI exists. This is a hack
unless the schema formally supports it.

**Affected boards (current and v0.6+):**
- Raspberry Pi 5 Model B (this drop)
- Raspberry Pi 4B (potential v0.6+)
- Raspberry Pi CM4 + carrier boards (v0.7+)
- Pine64 / Rock64 / Odroid family (post-v1.0)
- NVIDIA Jetson Orin / Xavier (post-v1.0)

**Proposed schema-v1.1 addition:**

```yaml
dt_fingerprint:               # NEW — alternative to dmi_fingerprint
  compatible: "raspberrypi,5-model-b"
  model: "Raspberry Pi 5 Model B Rev 1.0"
```

Match semantics: read `/proc/device-tree/compatible` (null-separated
list) and `/proc/device-tree/model`. Glob-supported.

**Mutual exclusion:** A board profile may have either `dmi_fingerprint`
OR `dt_fingerprint`, not both. Schema validator enforces this. The
matcher tries DMI first; if `/sys/class/dmi/id/sys_vendor` returns
empty/missing, falls through to DT.

**Synthesis fallback:** For boards that have BOTH DMI and DT (some
Snapdragon devices, Apple silicon via Asahi), allow `dmi_fingerprint`
to win — it's more specific.

---

## §HP-CONSUMER — 'unsupported' tier-3 fallback marker

**Why:** scope-B HP entry for Pavilion x360 sets
`overrides.unsupported: true` to record that consumer-class HP
laptops have NO Linux fan-control path. Currently the tier-3 generic
fallback would route them to `generic-coretemp-only` or similar,
which silently provides telemetry without flagging the user.

**Proposed change:** Schema validator recognizes
`overrides.unsupported: true` and ventd's matcher emits a one-shot
INFO log: "This hardware has no Linux fan-control driver. ventd
will report sensors only." This prevents the user from wondering
why their fan curve isn't being applied.

**Scope:** spec-03 amendment §11.6 (new) — tier-3 fallback semantics
extension.

**Affected consumer-class laptop families to flag in scope-C:**
- HP Pavilion (all)
- HP Envy (most)
- HP Spectre (most)
- Acer Aspire (most — no fan ABI)
- Some Asus Vivobook variants
- Some Microsoft Surface (the surface_fan driver covers ONLY some Pro models)

---

## Recommended ordering

1. **Land §SCHEMA-BIOSVER first** (small, contained schema change, no driver work).
2. **Land §LEGION-1 + §SCHEMA-DT in parallel** — they share no code.
3. **Block §IPMI-1 on spec-01 IPMI backend ship** (already in flight per memory).
4. **Defer §FW-1** — write spec-09 NBFC chat first to decide whether
   Framework support flows through NBFC or through new ec_command
   backend. The latter is a >$30 spec; the former is included in
   already-budgeted spec-09 work.
5. **§HP-CONSUMER bundles into the spec-03 amendment §11 update** —
   trivial, ride along with whichever amendment lands first.

---

## CC cost estimates

| Amendment | Estimated CC tokens (Sonnet) |
|---|---|
| §SCHEMA-BIOSVER schema bump + migration test | $3-5 |
| §LEGION-1 driver YAML + 5-8 Legion board YAMLs | $8-12 |
| §IPMI-1 driver YAML | $2-3 |
| §SCHEMA-DT schema bump + Pi5 + 2 ARM SBC entries | $5-8 |
| §HP-CONSUMER override.unsupported semantics | $2-4 |
| §FW-1 → defer (no estimate) | — |

Total scope-C wave (excluding §FW-1): ~$20-32 across 3-4 PRs.
Within Phoenix's per-spec $10-30 envelope per PR.

---

## Out of scope for this doc

- spec-05 P4-HWCURVE schema design (Legion's 10-point curve as prior art).
- spec-09 NBFC integration design (Framework / consumer fallback).
- spec-10 doctor changes for any new driver.
- Asahi Linux Apple-silicon fan path (no driver yet exists in mainline; deferred to post-v1.0).

---

## References

Primary:
- LenovoLegionLinux source: https://github.com/johnfanv2/LenovoLegionLinux/blob/main/kernel_module/legion-laptop.c
- LegionFanControl model table (cross-validation of BIOS_VERSION → model mapping): https://www.legionfancontrol.com/
- Issue #327 (16IRX9 NMCN30WW): https://github.com/johnfanv2/LenovoLegionLinux/issues/327
- Issue #76 (Slim 5 16APH8 M3CN31WW): https://github.com/johnfanv2/LenovoLegionLinux/issues/76
- Issue #234 (7 Pro 16ARX8H LPCN45WW + EC chip 5507): https://github.com/johnfanv2/LenovoLegionLinux/issues/234
- Framework EmbeddedController (cros_ec_lpcs context): https://github.com/FrameworkComputer/EmbeddedController
- ChromiumOS ectool (userspace EC commands): https://chromium.googlesource.com/chromiumos/platform/ec/+/refs/heads/main/util/ectool.cc
- IPMI subsystem doc: https://docs.kernel.org/driver-api/ipmi.html
- Pi 5 device-tree cooling: https://github.com/raspberrypi/firmware/blob/master/boot/bcm2712-rpi-5-b.dtb
