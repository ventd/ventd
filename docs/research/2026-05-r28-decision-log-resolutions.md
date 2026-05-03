# R28 §5 Decision-log resolutions

**Date:** 2026-05-03
**Scope:** Authoritative upstream truth for the 8 contradictions logged in
`docs/research/r-bundle/R28-master.md` §5 (lines 467–591).
**Method:** Direct kernel git inspection via cloned `torvalds/linux`
sparse-checkout at `v7.1-rc1` (HEAD on 2026-05-03), corroborated by
`Documentation/hwmon/*.rst` in-tree.
**Stance:** Phoenix authorised modifying rules and catalog rows where
they disagree with upstream. Each item below names the concrete change.

---

## 5.1 NCT6797D mapping (chip ID 0xd450)

### Authoritative source(s)

- `drivers/hwmon/nct6775-platform.c:87`: `#define SIO_NCT6797_ID 0xd450`
- `drivers/hwmon/nct6775-platform.c:36`: `[nct6797] = "NCT6797D"`
- `drivers/hwmon/nct6775-core.c:32` (chip table): `nct6797d 14 7 7 2+6 0xd450`
- NCT6796D = `0xd420`, NCT6798D = `0xd428`, NCT6799D = `0xd800`.

### Verdict

**Chip ID `0xd450` is unambiguously NCT6797D in mainline `nct6775`.**
Agent E2's "force_id target for NCT6799D" describes a pre-v6.4
operator workaround in which NCT6799D (true ID `0xd800`) was
force-loaded under the NCT6797D detection slot to get *some* PWM
control. That workaround is now harmful — label corruption since v6.4
(R28 row E2#9). Agent C's "nct6687d misidentifies 0xd450 as NCT6687"
is **wrong about the mechanism** — Fred78290's nct6687d does not key
off Super-I/O ID; it keys off hwmon `name` substring. The bug Agent C
describes (silent no-op writes on MSI Z690/Z790 with NCT6797D) does
happen, but the cause is driver mis-selection by ventd /
sensors-detect, not chip-ID confusion in the driver.

### Required change in ventd

1. **Add chip-name disambiguation** in `internal/hwmon/autoload.go`:
   when sensors-detect or scan-driver enumeration loads `nct6687`,
   verify the resulting hwmon `name` is in `{nct6686, nct6687}`. If
   `name == nct6797`, refuse to keep nct6687 loaded and prefer
   `nct6775`.
2. **Tighten the DMI triggers** at `internal/hwmon/autoload.go:153–160`
   so they only fire after no mainline `nct6775` binding succeeded
   AND the loaded hwmon name is `nct6683` (the read-only mainline stub
   for NCT6687D — nct6797 never reports as nct6683).
3. **Fix `internal/hwdb/catalog/chips/super_io.yaml:97`** — description
   currently reads `"AMD X570/B550 boards"`; NCT6797D ships on Intel
   Z690/Z790 too. Replace with: `"Nuvoton NCT6797D — chip ID 0xd450,
   supported by mainline nct6775"`.

### Risk if we don't change it

On an MSI MPG Z790 with NCT6797D running an old kernel where
sensors-detect mistakenly loads OOT nct6687 first, ventd persists
nct6687 as the boot module and fan PWM writes silently no-op.

### Implementation cost: M

Three file edits + one disambiguation unit test.

---

## 5.2 dell-smm-hwmon `restricted=` security model

### Authoritative source(s)

- `drivers/hwmon/dell-smm-hwmon.c:130–132` (v7.1-rc1):
  ```c
  static bool restricted = true;
  module_param(restricted, bool, 0);
  MODULE_PARM_DESC(restricted, "Restrict fan control and serial number to CAP_SYS_ADMIN (default: 1)");
  ```
- `Documentation/hwmon/dell-smm-hwmon.rst` verbatim:
  > "restricted:bool — Allow fan control only to processes with the
  > `CAP_SYS_ADMIN` capability set or processes run as root … If your
  > notebook is shared with other users and you don't trust them you
  > may want to use this option. (default: 1, only available with
  > `CONFIG_I8K`)"

### Verdict

**The kernel doc does NOT contain a "never set restricted=0" warning.**
Defaults to `1`, recommends `1` for shared notebooks, leaves `0` as
a choice. The "DANGEROUS" framing in Agent B / E2 overstates upstream
posture — but the security risk on a multi-user laptop is real.
**R28 §5.2's prescription remains correct in spirit:** ventd should
never auto-set `restricted=0`.

### Required change in ventd

1. **Confirmed:** ventd never writes `restricted=0`. Searched
   `internal/` and `boards/dell.yaml`: no occurrences. **No code
   change needed for behaviour.**
2. **Fix the misleading comment at `internal/hwdb/catalog/boards/dell.yaml:54`**
   ("Kernel module restricted=0 may be needed") — change to: "Kernel
   default `restricted=1` is correct for ventd; ventd runs as root
   with CAP_SYS_ADMIN. Operators on shared laptops MUST keep
   restricted=1."
3. **Add a negative-allowlist test** asserting `restricted=0` never
   appears as a value in any catalog YAML — extend the existing
   driver-args validator.

### Risk if we don't change it

Low. Behaviour is already correct; the comment fix is hygiene + a
guardrail against future drift.

### Implementation cost: S

Comment fix + one negative-allowlist test.

---

## 5.3 `acpi_enforce_resources=lax` blast radius

### Authoritative source(s)

- **Commit `12c44ab8b40`** "hwmon: (it87) Add param to ignore ACPI
  resource conflicts", **author Ahmad Khalifa**, **2022-10-04**,
  signed-off by Guenter Roeck.
- **Earliest containing tag: `v6.2-rc1`** → landed in **kernel v6.2**.
- `Documentation/hwmon/it87.rst` verbatim:
  > "ignore_resource_conflict bool — Similar to acpi_enforce_resources=lax,
  > but only affects this driver. … Provided since there are reports
  > that system-wide acpi_enfore_resources=lax can result in boot
  > failures on some systems. Note: This is inherently risky since it
  > means that both ACPI and this driver may access the chip at the
  > same time. This can result in race conditions and, worst case,
  > result in unexpected system reboots."

### Verdict

R28's claim is correct: `ignore_resource_conflict=1` landed in v6.2,
narrows the bypass to one driver, motivated by per-driver locality.
Per-driver risk is still present (kernel calls it "inherently risky"),
just smaller blast radius than the cmdline.

**ventd's existing implementation does NOT branch on kernel version.**
`web/server.go::handleGrubCmdlineAdd` unconditionally writes
`acpi_enforce_resources=lax`. The
`internal/hwmon/modprobe_options.go` allowlist contains only
`thinkpad_acpi fan_control=1`; `it87 ignore_resource_conflict=1` is
listed as a *future* Stage 1B/1C entry (line 20–22 comment). The R28
master claim "Stage 1 in flight, branches kernel version correctly"
is **currently false** — branching is documented, not coded.

### Required change in ventd

1. **Add `it87 ignore_resource_conflict=1` to `allowedModprobeOptions`**
   in `internal/hwmon/modprobe_options.go:23`.
2. **Wire kernel-version detection into the recovery card chain**
   (`internal/recovery/remediation.go:257, 281`): on kernel ≥ 6.2 +
   it87, prefer the per-driver drop-in via
   `/api/hwdiag/modprobe-options-write` over the cmdline. Cmdline is
   the fallback only for kernel < 6.2.
3. **Add `RULE-MODPROBE-OPTIONS-04`** in
   `.claude/rules/modprobe-options-write.md`: "When kernel ≥ 6.2 AND
   module == it87, the recovery card MUST prefer
   `ignore_resource_conflict=1` over `acpi_enforce_resources=lax`."

### Risk if we don't change it

On a kernel ≥ 6.2 ASUS board with both `asus_atk0110` and `it87`,
ventd's blanket `acpi_enforce_resources=lax` recommendation breaks
`asus_atk0110` (Launchpad #953932). Per-driver path avoids this and
needs no reboot.

### Implementation cost: M

Three file edits + new rule + bound test.

---

## 5.4 NVML helper recursion guard

### Authoritative source(s)

- `internal/nvidia/helper.go:43–55` (`needsHelper()`), bound by
  `RULE-NVML-HELPER-RECURSION-01`.
- `internal/nvidia/helper_test.go::TestNeedsHelper_RootBypasses`.

### Verdict

**The existing rule is structurally correct.** The recursion guard
(`if os.Geteuid() == 0 { return false }`) is the first check in
`needsHelper()`, runs before any `os.Stat(helperPath())`. When the
SUID helper executes, its euid == 0, the guard fires, and the helper
goes direct — no re-invocation possible.

**The S2-5 datacenter-GPU detect-and-refuse path is safe by
construction**: classification happens at probe time (read-only
`nvml.LaptopDGPU` / forthcoming `nvml.DatacenterGPU`), the result is
written to the wizard outcome KV namespace, and any write attempt on
a `ClassDatacenterGPU` channel returns `OutcomeMonitorOnly` *before*
dispatch reaches `WriteFanSpeed` (the only function that consults
`needsHelper()`). The helper is never called.

### Required change in ventd

**No production change required.** The S2-5 PR must add a new test
asserting datacenter-GPU classification refuses writes without
invoking either direct NVML or the helper:
`TestNVML_DatacenterGPU_RefusesWithoutHelper`.

Minor: `errorsAs` in `helper.go:144` only walks `Unwrap() error`
(not `Unwrap() []error`). Fine for the current single-wrap chain;
worth documenting in the rule file if `runHelper` ever multi-wraps.

### Risk if we don't change it

None. The S2-5 implementation has a test obligation, not a rule
change.

### Implementation cost: S

One new test in S2-5's PR.

---

## 5.5 ThinkPad fan2_input=65535 sentinel filter

### Authoritative source(s)

- **Commit `a10d50983f7befe85acf95ea7dbf6ba9187c2d70`** (full hash;
  R28 master truncated to `a10d50983f7b`), author **Jelle van der
  Waa <jvanderwaa@redhat.com>**, **2022-10-19**, reviewed +
  signed-off by Hans de Goede. Subject:
  "platform/x86: thinkpad_acpi: Fix reporting a non present second
  fan on some models". Body:
  > "thinkpad_acpi was reporting 2 fans on a ThinkPad T14s gen 1, even
  > though the laptop has only 1 fan. The second, not present fan
  > always reads 65535 (-1 in 16 bit signed), ignore fans which report
  > 65535 to avoid reporting the non present fan."
- **Landing tag: `v6.1`** (NOT v6.2 as R28 master claims). Verified
  via `git tag --contains a10d50983f7 | grep -E '^v[0-9]+\.[0-9]+(-rc[0-9]+)?$' | sort -V | head`
  → earliest is `v6.1-rc3`, included in the v6.1 release.

### Verdict

**Two corrections to R28 master:** (1) tag is **v6.1**, not v6.2;
(2) the existing `RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE` (10000 RPM
cap) is correct for consumer scope. Industrial server fans
(Delta-style hyperscale rotors) do legitimately exceed 10k RPM
(11–14k peak), but they're already gated behind
`--allow-server-probe` (`RULE-SYSCLASS-05`); operator opts in but
loses tach data — acceptable for v0.5.x.

### Required change in ventd

1. **Keep `RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE` unconditional, 10000
   cap unchanged.**
2. **Add a doc note** in `.claude/rules/hwmon-sentinel.md`
   acknowledging server-class >10k fans are filtered. When real HIL
   telemetry surfaces, raise to 15000 under a new
   `RULE-SENTINEL-FAN-SERVER-CAP` gated on `ClassServer`.
3. **Fix tag references** anywhere in `docs/` that say "v6.2" for
   commit `a10d50983f7` — correct to **v6.1**.
4. **Update the catalog comment for thinkpad_acpi**: per-driver
   workaround for pre-v6.1 second-fan sentinel is no longer needed —
   universal `RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE` covers it for
   pre-v6.1 kernels.

### Risk if we don't change it

For consumer hardware: zero. For hyperscale/HEDT >10k fans: tach data
silently filtered — acceptable in v0.5.x.

### Implementation cost: S

Doc-comment corrections only.

---

## 5.6 Steam Deck DMI dispatch (Jupiter vs Galileo)

### Authoritative source(s)

- **Mainline kernel at v7.1-rc1:** `find drivers -iname '*steam*'
  -o -iname '*jupiter*' -o -iname '*galileo*'` returns **zero results**.
  No `steamdeck-hwmon.c` in `drivers/hwmon/`. No Steam Deck driver
  in `drivers/platform/x86/`. Mainline-adjacent Steam Deck commits
  as of 2026-04 only touch ACP audio, panel-orientation quirks,
  k10temp APU ID, and AMDGPU GFX hangs — none expose hwmon PWM.
- The driver lives in: Valve's downstream kernel (SteamOS),
  `philipl/steamdeck-kernel-driver` (DKMS), and `KyleGospo/steamdeck-kmod`.
- ArchWiki Steam Deck:
  > "If you are using a mainline kernel, you need patches from
  > Valve's kernel … e.g. by installing the user-adapted steamdeck-dkms
  > ACPI platform driver in DKMS form."

### Verdict

**ventd's `internal/hwdb/catalog/drivers/steamdeck-hwmon.yaml` is
wrong about mainline status.** Driver requires SteamOS kernel or a
DKMS module. **Both DMI variants ARE covered by the same driver**
when present (Agent H#32 right). DMI fingerprint disambiguation must
still happen on the ventd side — per R3 (Steam Deck on the
catalog-less refusal list) and project policy on per-revision
sysclass shards, priors must NOT be shared across Jupiter/Galileo
even when the driver is the same.

### Required change in ventd

1. **Audit `internal/hwdb/catalog/drivers/steamdeck-hwmon.yaml`** —
   the citation `https://www.kernel.org/doc/html/latest/hwmon/steamdeck.html`
   on line 29 is a **dead link** (no such kernel doc). Replace with
   `https://github.com/philipl/steamdeck-kernel-driver` and SteamOS
   pages. Add a `recommended_alternative_driver` block pointing to the
   DKMS route for mainline kernels.
2. **Fix the description (line 6)**: `"Valve Steam Deck fan controller
   (LCD 'Jupiter' + OLED 'Galileo'). NOT in mainline kernel as of
   v7.1; ships in SteamOS kernel and via philipl/steamdeck-kernel-driver
   DKMS."`
3. **When the catalog board row for Steam Deck lands**,
   `dmi_fingerprint.product_name` MUST distinguish `"Jupiter"` (LCD)
   vs `"Galileo"` (OLED) so per-revision signature priors stay
   separate.
4. **Add a probe-time check** in `autoload.go`: if DMI product_name
   is Jupiter/Galileo and `steamdeck_hwmon` not in
   `/sys/class/hwmon/*/name`, emit a Wizard Recovery card with
   `ClassMissingModule` + `philipl/steamdeck-kernel-driver` install
   hint.

### Risk if we don't change it

Operator on Bazzite / HoloISO / Manjaro-on-Deck installs ventd, the
catalog references a non-existent mainline driver, the wizard
silently fails enumeration with no actionable diagnostic.

### Implementation cost: M

Yaml edits + autoload-time DMI probe + recovery card wiring +
sysclass shard separation enforcement.

---

## 5.7 RDNA3/4 OD_FAN_CURVE all-zero rejection

### Authoritative source(s)

- **Commit `3e6dd28a110`** "drm/amd/pm: disable OD_FAN_CURVE if temp
  or pwm range invalid for smu v13", author **Yang Wang
  <kevinyang.wang@amd.com>** (AMD), **2026-03-19**, Acked-by Alex
  Deucher. Cherry-picked from `470891606c5a`. CC: stable. Closes
  ROCm/amdgpu issue 208. Body reproduces:
  > ```
  > $ sudo cat /sys/bus/pci/devices/<BDF>/gpu_od/fan_ctrl/fan_curve
  > 0: 0C 0%   …   4: 0C 0%
  > FAN_CURVE(hotspot temp): 0C 0C
  > FAN_CURVE(fan speed):    0% 0%
  > $ echo "0 50 40" | sudo tee fan_curve
  > [...] amdgpu: Fan curve temp setting(50) must be within [0, 0]!
  > ```
- **Landing tag: `v7.0`** (visible at `v7.0-rc6`).
- **Companion `cb47c882c31`** "drm/amd/pm: add missing od setting
  PP_OD_FEATURE_ZERO_FAN_BIT for smu v13.0.0/13.0.7", same author,
  **2026-03-03**, also **v7.0** (v7.0-rc4).
- **Affected smu13 chip variants:** `smu_v13_0_0` (Navi 31 — RX 7900
  XTX/XT/GRE) and `smu_v13_0_7` (Navi 31 derivatives).
- AMD GitLab issue 5018 + ROCm/amdgpu issue 208.

### Verdict

**R28 master is wrong on landing tag.** R28 §5.7 says "post-v6.18"
and proposes a rule gated on "kernel <7.1". Actual landing is **v7.0**.
The fix applies to **all RDNA3 cards using smu_v13_0_0 or
smu_v13_0_7** — RX 7900 XTX/XT/GRE.

Pre-fix: PMFW exposes `gpu_od/fan_ctrl/fan_curve` with all-zero
ranges; any write fails with `Fan curve temp setting(N) must be
within [0, 0]!`. Custom fan curves impossible on these SKUs on
kernel < 7.0. The existing `RULE-EXPERIMENTAL-AMD-OVERDRIVE-04`
covers RDNA4 (Navi 48) on kernel 6.15. R28 §5.7 underestimates
urgency — RX 7900 XTX is a **shipping mainstream card** with a
**known-broken write path** under-fix.

### Required change in ventd

1. **Add `RULE-EXPERIMENTAL-AMD-OVERDRIVE-05`** binding a check in
   `internal/hal/gpu/amdgpu/sysfs.go::WriteFanCurveGated`. Pseudocode:
   ```go
   if isRDNA3SMU13(c) && allZeroFanCurveRange(c.CardPath) && !kernelAtLeast(7, 0, osReleasePath) {
       return ErrRDNA3FanCurveNeedsKernel70
   }
   ```
   Bound test: `internal/hal/gpu/amdgpu/rdna3_test.go::TestRDNA3_FanCurveAllZeroRangeRefusedOnPreV70`.
2. **Add affected SKU PCI IDs** to catalog (`amdgpu_rdna3.yaml`):
   `0x744c` (RX 7900 XTX), `0x745e` (RX 7900 XT), `0x7448` (RX 7900
   GRE), and any smu_v13_0_7 derivative — cross-check against
   `drivers/gpu/drm/amd/amdgpu/amdgpu_drv.c::pciidlist[]`.
3. **Update R28 master Stage-2 candidate #62** to record landing tag
   as **v7.0**, not "v7.1". Gate is `kernel < 7.0`.

### Risk if we don't change it

On Linux < v7.0 with RX 7900 XTX/XT/GRE, ventd's OD_FAN_CURVE writes
produce repeated `Fan curve temp setting must be within [0, 0]!`
dmesg errors and no fan-curve effect.

### Implementation cost: M

New rule + bound test + sysfs gate + catalog SKU list.

---

## 5.8 IT8689E mainline kernel landing tag

### Authoritative source(s)

- **Commit `66b8eaf8def`** "hwmon: (it87) Add support for IT8689E",
  author **Markus Hoffmann**, **2026-03-22 10:33:01 +0000**,
  signed-off by Guenter Roeck.
- **Landing tag: `v7.1-rc1`** (verified by `git tag --contains
  66b8eaf8def | grep -E '^v[0-9]+\.[0-9]+' | sort -V | head` →
  only `v7.1-rc1`). Will land in **v7.1 release**. Linux 7.0 was
  released **2026-04-12**; the merge window for v7.0 closed
  ~2026-03-30, so this commit was **too late for v7.0** and slid
  into v7.1.

### Verdict

**R28 master is correct: v7.1.** The "could ship in v7.0 or v7.1"
hedge is now resolved: definitively **v7.1**. Note: this commit only
fixes the chip-ID / register-layout side; the
`PWM-stuck-but-accepted` issue tracked in frankcrawford/it87#96
(Gigabyte X670E Aorus Master) is a separate BIOS-side problem and
remains unresolved upstream.

### Required change in ventd

1. **Update R28 master Stage-2 candidate #62** with confirmed source
   `66b8eaf8def Markus Hoffmann 2026-03-22 → v7.1`.
2. **Update `internal/hwdb/catalog/chips/ite_family.yaml:61`**: add
   note that on **kernel ≥ 7.1, IT8689E is supported by mainline
   `it87` without `force_id=`** — only `ignore_resource_conflict=1`
   is still needed when ACPI has reserved the I/O region.
3. **Make `recommended_alternative_driver`** in
   `internal/hwdb/catalog/drivers/it87.yaml` conditional on kernel
   < 7.1 for IT8689E (currently fires unconditionally → operator gets
   an unnecessary OOT DKMS recommendation on v7.1+ Gigabyte boards).
   Same kernel-version-gate logic as item 5.3.

### Risk if we don't change it

On a kernel ≥ 7.1 Gigabyte board with IT8689E, ventd unnecessarily
recommends installing OOT `frankcrawford/it87` DKMS when mainline
already supports it. Friction on a happy path.

### Implementation cost: S

Yaml edits + a kernel-version-gate condition in
`recommended_alternative_driver` evaluation.

---

## Required-changes summary table

Ranked by urgency = real-world failure probability × operator
visibility × ventd's user-base distribution.

| # | Item | Concrete change | Cost | Urgency |
|---|------|-----------------|------|---------|
| 1 | **5.7** RDNA3 OD_FAN_CURVE | New `RULE-EXPERIMENTAL-AMD-OVERDRIVE-05` + sysfs gate refusing writes when all-zero range AND kernel < 7.0; SKU PCI list `0x744c`/`0x745e`/`0x7448`. | M | **High** — RX 7900 XTX shipping mainstream; bug reproducible on every < v7.0 kernel today. |
| 2 | **5.6** Steam Deck mainline | Fix `steamdeck-hwmon.yaml` to declare DKMS-only (NOT mainline); add DMI dispatch (Jupiter/Galileo) for sysclass shard separation; replace dead kernel.org citation. | M | **High** — every Steam Deck mainline-kernel install hits this; current catalog promises a non-existent mainline driver. |
| 3 | **5.3** it87 acpi resource | Add `it87 ignore_resource_conflict=1` to `allowedModprobeOptions`; branch recovery card on kernel ≥ 6.2 to prefer per-driver path; new `RULE-MODPROBE-OPTIONS-04`. | M | **High** — fixes Launchpad #953932 (`asus_atk0110` breakage under blanket `lax`). |
| 4 | **5.1** NCT6797D / 0xd450 | Chip-name disambiguator in `autoload.go`; tighten DMI triggers for nct6687d so they don't capture NCT6797D paths; fix `super_io.yaml` description. | M | **Medium** — affects MSI Z690/Z790 NCT6797D users hitting OOT nct6687 first; rare on modern kernels. |
| 5 | **5.8** IT8689E v7.1 | Update catalog: mainline `it87` covers IT8689E on kernel ≥ 7.1 (no force_id needed); make `recommended_alternative_driver` conditional. | S | **Medium** — slides in cleanly; today's regression is "unnecessary OOT recommendation", not "broken". |
| 6 | **5.5** ThinkPad fan2 sentinel | Doc-comment fix only: kernel landing is **v6.1**, not v6.2 (R28 master is wrong); keep universal sentinel rule unchanged. | S | **Low** — existing rule correct; only the documented tag is wrong. |
| 7 | **5.2** dell-smm restricted= | Fix the misleading "restricted=0 may be needed" comment in `boards/dell.yaml`; add a negative-allowlist test asserting `restricted=0` never reaches catalog. | S | **Low** — current behaviour correct; this is hygiene + a future-proof guardrail. |
| 8 | **5.4** NVML helper recursion | No production change. S2-5 datacenter-GPU PR must add `TestNVML_DatacenterGPU_RefusesWithoutHelper` asserting refused class never invokes the helper. | S | **Low** — existing rule + guard correct; this is a test obligation, not a fix. |

---

## Cross-cutting findings

1. **Two R28 master tag-claims are wrong:**
   - 5.5 thinkpad fan2 commit landed in **v6.1**, not v6.2.
   - 5.7 smu13 OD_FAN_CURVE fix landed in **v7.0**, not "post-v6.18 /
     v7.1" — gate should be `kernel < 7.0`, not `< 7.1`.
2. **Two ventd catalog rows are upstream-incorrect today:**
   - `super_io.yaml::nct6797` description claims AMD-only.
   - `steamdeck-hwmon.yaml` cites a non-existent kernel doc and
     implies mainline support.
3. **Two new rules to add in the same PR series:**
   `RULE-EXPERIMENTAL-AMD-OVERDRIVE-05` (item 5.7) and
   `RULE-MODPROBE-OPTIONS-04` (item 5.3). Both have load-bearing
   bound-test obligations.
4. **No upstream source supports the dell-smm-hwmon "DANGEROUS"
   framing.** The kernel doc tone is measured. ventd's internal
   docs should match upstream tone, not forum-folklore hyperbole.
5. **The R28 master claim that #2 Stage-1 is "in flight, branches
   kernel version correctly" is currently false.** ventd's
   `acpi_enforce_resources=lax` recommendation is unconditional;
   no `it87 ignore_resource_conflict=1` path exists yet (modprobe
   allowlist is `thinkpad_acpi`-only).

---

*Prepared 2026-05-03 against torvalds/linux at v7.1-rc1 HEAD
(sparse-checkout: drivers/hwmon, drivers/platform/x86,
drivers/gpu/drm/amd/pm, Documentation/hwmon). Primary sources cited
inline by commit hash + author + date. No commit hashes invented.*
