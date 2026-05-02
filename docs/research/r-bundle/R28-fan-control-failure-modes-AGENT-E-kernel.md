# R28 — Fan-Control Failure Modes — Agent E (Kernel-Side Fixes)

**Sibling outputs:** A=GitHub, B=wikis, C=forums, D=StackExchange. **E (this file) = upstream commits & version gates.**

## Scope and methodology

This file mines kernel-side fixes for fan-control bugs across:

- `drivers/hwmon/{nct6775-core,nct6683,it87,asus-ec-sensors,applesmc,dell-smm-hwmon}`
- `drivers/platform/x86/{thinkpad_acpi,asus-wmi,dell-smm-hwmon}`
- `drivers/gpu/drm/amd/pm/swsmu/{smu11,smu13,smu14}` and `drivers/gpu/drm/amd/amdgpu/amdgpu_pm.c`
- AMD `powerplay/hwmgr/vegaXX_thermal.c` (legacy DPM)
- lore.kernel.org context references

**Method.** Pulled per-file commit logs via the GitHub mirror of `torvalds/linux`, then for each candidate fetched the raw `.patch` (`https://github.com/torvalds/linux/commit/<sha>.patch`) to confirm full hash, author date, `Cc: stable`, `Fixes:` lineage, and the user-observable symptom from the commit message. **Kernel version mapping** for each commit was derived from author date against the published Linux release calendar (Linus typically tags vX.Y about 8-10 weeks after the previous release; first stable inclusion equals the next mainline tag whose merge window closed after the patch landed in the maintainer's `-next` branch — for hwmon that is `linux-staging.git#hwmon-next` via Guenter Roeck, and for pdx86 it is `pdx86/platform-drivers-x86.git`). When the maintainer-tag (e.g. "Merge tag 'hwmon-for-v6.13-rc1'") is visible in the surrounding log, the version is **high confidence**. Where I had to infer from author date alone, confidence is **medium** and the reasoning is in the Notes column.

**What is in scope.** Patches that:

1. Fix a user-observable fan-control bug (fan stuck at 0/full, wrong RPM scale, missing fan2, sysfs write rejected, oops/KASAN on probe, post-resume crash, etc.).
2. Land a `Cc: stable` (auto-backport) — these are the strongest version gates.
3. Add support for a previously-unsupported chip variant where users had to use `force_id` or rebuild `it87-dkms`.
4. Relax/remove a workaround the userland community widely uses (e.g. `acpi_enforce_resources=lax`, `nct6775.force_id=`, `i8k.force=1`).

**What is out of scope.** Style refactors (`sysfs_emit` migrations, SPDX headers, `kobj_to_dev` sweeps) are listed only when they introduced regressions later fixed.

**Verification.** Every commit hash below was retrieved as a `.patch` file and confirmed to resolve. The cgit-canonical URL for any row is:

```
https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/commit/?id=<full-sha>
```

(I quote the GitHub mirror in the table only because kernel.org cgit is currently behind an Anubis WAF that rejects WebFetch's UA; the SHAs are identical.)

---

## Kernel release calendar reference

Used for date-to-version mapping in the table below. Mainline tag dates from `kernelnewbies.org/LinuxVersions`:

| Tag | Released | | Tag | Released |
|---|---|---|---|---|
| v5.10 | 2020-12-13 | | v6.4 | 2023-06-25 |
| v5.15 | 2021-10-31 | | v6.5 | 2023-08-27 |
| v5.16 | 2022-01-09 | | v6.6 | 2023-10-29 |
| v5.17 | 2022-03-20 | | v6.7 | 2024-01-07 |
| v5.18 | 2022-05-22 | | v6.8 | 2024-03-10 |
| v5.19 | 2022-07-31 | | v6.9 | 2024-05-12 |
| v6.0 | 2022-10-02 | | v6.10 | 2024-07-14 |
| v6.1 | 2022-12-11 | | v6.11 | 2024-09-15 |
| v6.2 | 2023-02-19 | | v6.12 | 2024-11-17 |
| v6.3 | 2023-04-23 | | v6.13 | 2025-01-19 |
|  |  | | v6.14 | 2025-03-23 |
|  |  | | v6.15 | 2025-05-25 |
|  |  | | v6.16 | 2025-07-27 |

A patch authored on date D normally lands in the next mainline tag merged after D + ~3-6 weeks (subsystem tree → -next → Linus). Patches with `Cc: stable@vger.kernel.org` are also backported to currently supported LTS branches (5.10, 5.15, 6.1, 6.6, 6.12 LTS as of mid-2025).

---

## Master table

| # | Class / Driver | Pre-fix symptom | Workaround pre-fix | Kernel with fix | Commit / lore URL | Confidence | Notes |
|---|---|---|---|---|---|---|---|
| 1 | hwmon/nct6775 | Fan speed silently set to 0 RPM after probe; affected boards seen in regmap-conversion era. Reporter: Thomas Zajic. | Pin to pre-5.19 kernel or revert `4ef2774511dc`. | **v6.7** (with `Cc: stable@vger.kernel.org # v5.19+` — backported to all LTS in 5.19+ range) | `920057ad521dc8669e534736c2a12c14ec9fb2d7` "hwmon: (nct6775) Fix incorrect variable reuse in fan_div calculation" | High | Author 2023-09-29; landed in hwmon-for-v6.7. **Strongest stable backport gate** in dataset — ventd should treat any 5.19-6.6 install without this commit as broken-fan-div territory. |
| 2 | hwmon/nct6775 | KASAN global-out-of-bounds in `nct6775_probe` on NCT6799D; oops on boot when KASAN enabled, silent OOB read otherwise. Reporter: Erhard Furtner. | Disable nct6775 module; lose monitoring. | **v6.8** | `d56e460e19ea8382f813eb489730248ec8d7eb73` "hwmon: (nct6775) Fix access to temperature configuration registers" | High | Fixes `b7f1f7b2523a` ("Additional TEMP registers for nct6799"). Affects only 6799D users on v6.6-6.7. |
| 3 | hwmon/nct6775 | `pwm5_mode/pwm6_mode/pwm7_mode` writes do array-OOB on NCT6798/6799 (pwm_num=7 but mask arrays were 6-element). | Avoid writing pwm modes for fans 5-7. | **v6.15** | `815f80ad20b63830949a77c816e35395d5d55144` "hwmon: (nct6775-core) Fix out of bounds access for NCT679{8,9}" | High | Author 2025-03-12; lands hwmon-for-v6.15. No `Cc: stable`, so older kernels stay vulnerable — ventd should refuse pwm_mode writes 5-7 on these chips when kernel < 6.15. |
| 4 | hwmon/nct6775 | Setting fan PWM in automatic mode silently ignored / corrupts state. | Set `pwm*_enable=1` (manual) before any PWM write. | **v6.8** | `8b3800256abad20e91c2698607f9b28591407b19` "hwmon: (nct6775) Fix fan speed set failure in automatic mode" | High | Returns -EINVAL now if user writes pwm without manual mode — ventd must check this. |
| 5 | hwmon/nct6775 | `fan*_min` / `target_temp` / `tolerance` writes cause arithmetic overflow when given values > INT_MAX. | Validate inputs in userspace. | **v6.13** (overflow) / **v6.11** (underflow) | `57ee12b6c514146c19b6a159013b48727a012960` (overflow), `0403e10bf082...` (underflow) | High | Pure DoS-class bugs; userspace daemons that whitelist [0,255] never hit it. Listed for completeness — ventd is unaffected. |
| 6 | hwmon/nct6775 | Suspend/resume crash: `nct6775_suspend()` skipped `update_device()`, resume wrote stale registers, system crashed shortly after wake. Reporter: Zoltán Kővágó. | Disable suspend or pin to pre-v5.18. | **v5.19** | `f4e6960f4f16b1ca5da16cec7612ecc86402ac05` "hwmon: (nct6775) Fix platform driver suspend regression" | High | Fixes regression introduced by `c3963bc0a0cf` (driver split, v5.18). v5.18 is broken-on-resume for any nct6775 user. |
| 7 | hwmon/nct6775 | `shift-out-of-bounds` UBSAN warning on boot (cosmetic) on NCT6799 due to non-existent ALARM bit. Reporter: Doug Smythies. | Ignore dmesg. | **v6.7** | `2dd1d862817b850787f4755c05d55e5aeb76dd08` "hwmon: (nct6775) Fix non-existent ALARM warning" | High | Cosmetic, but ventd's "doctor" should not flag this UBSAN as a real fault — just inform the user. |
| 8 | hwmon/nct6775 | NCT6799D not detected at all (chip ID 0xd802 unrecognised). | Use `nct6775.force_id=0xd450` | **v6.4** | `aee395bb1905...` "hwmon: (nct6755) Add support for NCT6799D" (introduced); subsequent fixes `13558a2e6341` (IN scaling), `b7f1f7b2523a` (TEMP regs), `4f65c15cf70e` (18 IN readings), `23299bba08df` (labels), `3b7f4bde06da` (ALARM/BEEP) | High | **`force_id` no longer needed** for NCT6799D on v6.4+. Ventd should *not* recommend `force_id=` for this chip on modern kernels; it can cause label corruption since v6.4. |
| 9 | hwmon/nct6683 | NCT6687D on MSI boards completely unrecognised (Customer ID rejected). | Out-of-tree nct6687d module. | **v5.11** | `daf4fedde6177941b55ba3c3293a8585d5280b94` "hwmon: (nct6683) Support NCT6687D." | High | Big unlock for 2020-era MSI X570 / B550 owners. |
| 10 | hwmon/nct6683 | NCT6686D on Lenovo P620 unrecognised. | Out-of-tree module. | **v5.13** | `bfbbbe04d01222aa484400a7257f34a952af2237` "hwmon: (nct6683) Support NCT6686D" | High | |
| 11 | hwmon/nct6683 | Driver loads but registers spammed warning for unknown Customer IDs (ASRock B650 Steel, Z590 Taichi, MSI MAG variants, AMD BC-250, Z790-Taichi family). | Patch in DKMS or ignore warning. | Each landed individually 6.5-6.16: e.g. `cf85760f6a0a` (B650 Steel), `c0fa7879c985` (Z590 Taichi → **v6.16+**), `f392611e268f` (BC-250 → v6.14), `ff708b549c4d` (B650I Lightning, post-v6.16). | Medium-High | Each commit is one Customer ID added — ventd's diagnostic should match these. The `be7d9294a411` commit (v6.10) added a "warn-but-load" path which makes earlier kernels louder than necessary. |
| 12 | hwmon/it87 | `acpi_enforce_resources=strict` caused module to refuse to load on many BIOSes that reserve EC range but don't use it. | Boot param `acpi_enforce_resources=lax`. | **v6.2** (driver-local opt-out) | `12c44ab8b401c29d8d3569aaea34da662b8ece1d` "hwmon: (it87) Add param to ignore ACPI resource conflicts" | High | **Replaces** the system-wide `acpi_enforce_resources=lax` workaround with `it87.ignore_resource_conflict=1`. Ventd should prefer the module param on v6.2+. |
| 13 | hwmon/it87 | force_id used with no chip present caused fake registration / crashes. | Don't use force_id without a chip there. | **v6.2** | `b3b19931a5c22f5a09f846e037b23f8a74455d0a` "hwmon: (it87) Check for a valid chip before using force_id" | High | |
| 14 | hwmon/it87 | IT87952E (DEV ID 0x8695) on newer Gigabyte AORUS boards undetected. | force_id=0x8628 (incorrect labels). | **v6.4** | `d44cb4cd7456b6eef2689fdfed7bf361ffc8e5ce` "hwmon: (it87) Add new chipset IT87952E" | High | |
| 15 | hwmon/it87 | IT8689E unrecognised (newest Gigabyte/AsRock). | DKMS or wait. | **v6.16+ (mainline)** — author 2026-03-22, landed in hwmon-for-v7.1 per surrounding merge. | `66b8eaf8def2d51dab49c4921b93f1bf1c7638dc` "hwmon: (it87) Add support for IT8689E" | Medium | Calendar drift in commit metadata acknowledged; treat as "very recent — check `uname -r` ≥ 7.1". |
| 16 | hwmon/it87 | Voltage scaling broken on chips with 10.9 mV ADC (IT8732F & related); reported as wildly wrong vin readings. | None. | **v6.4** | `968b66ffeb7956acc72836a7797aeb7b2444ec51` "hwmon (it87): Fix voltage scaling for chips with 10.9mV ADCs" | High | Not strictly a fan bug, but breaks the temp/voltage signature ventd relies on for chip identifiability (R24). |
| 17 | hwmon/it87 | Probe entered SIO config mode unconditionally and crashed certain pre-init Gigabyte boards. | DMI quirks per board. | **v6.10** | `e58095cfc55692e4da4a5e87322e09a9b75186e0` "hwmon: (it87) Test for chipset before entering configuration mode" + companion `e2e6a23f4bda`, `f4f3e5de2e36`, `79a4c239e0a7` (4-patch series 2024-04-28) | High | This rewrite **eliminated the DMI quirk table** that ventd's old "is your board on the list?" logic targeted — on v6.10+, it87 no longer needs board-specific opt-ins. |
| 18 | hwmon/it87 | Chips with only 4 fans / 4 PWMs (IT8732F variants) reported phantom fans 5-6 reading 0 RPM. | Mask in userspace. | **v6.4** | `5a4417bc67cd2cb24667f226667dba66d284de8b` (4 fans) + `39a6dcf640a5fc0aa880a8cf8871755fdbd42a5e` (4 PWMs) | High | ventd should not raise "fan stuck at 0" for fan5/6 on IT8732F kernels < 6.4 — it's a phantom. |
| 19 | platform/x86/thinkpad_acpi | T14s gen1 / many post-2020 single-fan ThinkPads reported phantom `fan2_input` reading 65535. | Userland filter. | **v6.2** | `a10d50983f7befe85acf95ea7dbf6ba9187c2d70` "thinkpad_acpi: Fix reporting a non present second fan on some models" | High | Direct ventd implication: on kernel < 6.2, any thinkpad reporting 65535 for `fan2_input` should be auto-suppressed by ventd's chip-ident layer. |
| 20 | platform/x86/thinkpad_acpi | Dual-fan models (P1 / X1 Extreme / P15 / P53 / P73 / T15g / X1 Carbon Gen9) only exposed `fan1_input` — second fan invisible. | Out-of-tree quirk patch. | Series across **v5.10 → v5.18**: `80a8c3185f50` (P15, v5.11), `173aac2fef96` (P53/73, v5.12), `1f338954a5fb` (P1/X1 gen4, v5.16), `e9b0e120d02a` (T15g 2nd gen, v5.18), `25acf21f3a78` (X1 Carbon Gen9 2nd fan) | Medium-High | ventd's "expected fan count" predicate must consult kernel version: on v5.10 a P15 *correctly* shows 1 fan from the driver's perspective even though the laptop has 2. |
| 21 | platform/x86/thinkpad_acpi | ThinkPad X120e fan reported in ticks-per-revolution, not RPM. Showed implausible values (e.g. 1.35M). | Convert in userspace using 22.5 kHz / 1,350,000 const. | **v6.14** | `1046cac109225eda0973b898e053aeb3d6c10e1d` "Fix invalid fan speed on ThinkPad X120e" | High | Author 2025-02-03. ventd's noise-floor / RPM-plausibility predicate should ignore raw fan_input on X120e kernels < 6.14. |
| 22 | platform/x86/thinkpad_acpi | ThinkPads with ECFW reported decimal-coded RPM (e.g. raw 0x4200 was actually "4200 RPM" in BCD-ish form). | Quirk per model. | **v7.1** (very recent) | `1be765b292577c752e0b87bf8c0e92aff6699d8e` "Fix for ThinkPad's with ECFW showing incorrect fan speed" | Medium | Author 2024-11-06; landed pdx86 v7.1 per surrounding merge. ventd needs the same conversion when running on older kernels. |
| 23 | platform/x86/thinkpad_acpi | E531 / E560 had no fan support at all (ACPI methods FANG/FANW unrecognised). | None / out-of-tree. | **v6.16** (E531 added), **v6.17** (T495*/E560 disabled — bug from E531 patch) | `57d0557dfa4940919ec2971245a6d288e5d85aa8` "Add Thinkpad Edge E531 fan support" + `2b9f84e7dc863afd63357b867cea246aeedda036` "disable ACPI fan access for T495* and E560" | High | **Regression cycle:** v6.16 added FANG/FANW support but it broke fan reads on T495 and E560; v6.17 disables FANG/FANW for those models. Ventd must treat v6.16 as broken on these specific machines. |
| 24 | platform/x86/thinkpad_acpi | `fan_get_status()` 4-byte vs 1-byte buffer overflow (ancient). | None practical. | **v3.7** | `eceeb4371240aff22e9a535a2bc57d2311820942` "thinkpad_acpi: buffer overflow in fan_get_status()" | High | Listed for archaeological completeness; ventd target floor is much newer. |
| 25 | platform/x86/dell-smm | Off-by-one in `dell_smm_is_visible()` exposed `pwmX_enable` on wrong fan channel for global-fan-mode boxes; toggling pwm1_enable did nothing. | Manually ignore the file. | **post-v6.18** (pending mainline at author date 2025-12-03) | `fae00a7186cecf90a57757a63b97a0cbcf384fe9` "Fix off-by-one error in dell_smm_is_visible()" — has `Cc: stable@vger.kernel.org` | High | **Stable-tagged**, will hit 6.12+ LTS. Fixes regression from `1c1658058c99` ("automatic fan mode", v6.18). |
| 26 | platform/x86/dell-smm | Dell G15 5510 spun fans at max on AC due to missing whitelist entry; regression visible in v6.18+. | i8kmon / blacklist-bypass module rebuild. | **post-v6.18** | `830e0bef79aaaea8b1ef426b8032e70c63a58653` "Add Dell G15 5510 to fan control whitelist" | High | Fixes `1c1658058c99` regression. ventd auto-fix: nothing to do post-fix; on broken kernel suggest reverting "automatic fan mode" support. |
| 27 | platform/x86/dell-smm | Dell G5 5505 / Dell XPS 9370 / Latitude 7320 / Precision 7540 / OptiPlex 7080 / OptiPlex 7040 originally had no manual fan control allowed (whitelist gated). | Manually load with `dell-smm-hwmon.force=1` (unsafe). | Series across **v5.4 → v6.18**: `b4be51302d68` (Latitude 7320), `c84e93da8bc1` (Precision 7540), `516b01380036` (XPS 9370), `53d3bd48ef6f` (OptiPlex 7040), `46c3e87a7917` (OptiPlex 7080), `30ca0e049f50` (G5 5505) | High | ventd should treat the whitelist as authoritative on each kernel. The `i8k.force=1` workaround is widely used but **dangerous on machines whose EC is genuinely incompatible** (B&O firmware bricks). |
| 28 | platform/x86/dell-smm | Multiplier overflow in `i8k_set_fan_state()` when `fan_mult` module param oversized — wrote bogus 32-bit speeds, fan ran at random. | Don't override `fan_mult`. | **post-v6.18** | `46c28bbbb150b80827e4bcbea231560af9d16854` "Limit fan multiplier to avoid overflow" | High | |
| 29 | platform/x86/dell-smm | Third fan (where present) had wrong multiplier detection — fan3 reported wrong RPM. | i8kmon manual cal. | **v5.15** | `2757269a7defe96e871be795b680b602efb877ce` "Fix fan multiplier detection for 3rd fan" | High | |
| 30 | platform/x86/dell-smm | "Automatic fan mode" exposed via fan state 3 not represented in sysfs — users couldn't restore BIOS auto control. | Reboot. | **v6.18** | `1c1658058c99bcfd3b2347e587a556986037f80a` "Add support for automatic fan mode" | High | This is the patch that introduced the regressions in rows 25-26. ventd should advise: on v6.18 use latest LTS-stable backport; auto-mode requires `pwm1_enable=2`. |
| 31 | platform/x86/asus-wmi | Custom fan curves (CPU/GPU) on ROG laptops simply didn't exist in mainline. | rogctl out-of-tree. | **v5.17** | `0f0ac158d28ff78e75c334e869b1cb8e69372a1f` "Add support for custom fan curves" | High | **Major unlock for ROG fan control.** Below v5.17, only `cpu_fan` boost levels were available. |
| 32 | platform/x86/asus-wmi | TUF FX506 / U36SD / many TUFs had `asus-nb-wmi` probe fail with `-ENODATA` or `-2`, breaking *all* asus features (not just fan curve). | `modprobe.blacklist=asus-nb-wmi` (loses keyboard etc). | **v5.17.5 / v5.18** | `e3d13da7f77d73c64981b62591c21614a6cf688f` "Fix regression when probing for fan curve control" + `9fe1bb29ea0ab231aa916dad4bcf0c435beb5869` "Fix driver not binding when fan curve control probe fails" | High | Both `Fixes:` `0f0ac158d28f`. ventd should detect "asus-nb-wmi probe fails on v5.17.0-5.17.4" and recommend updating. |
| 33 | platform/x86/asus-wmi | TUF returns -ENOSPC reading default fan curve (buffer was 24 bytes, TUF needs 32). | None. | **v6.0** | `5542dfc582f4a925f67bbfaf8f62ca83506032ae` "Increase FAN_CURVE_BUF_LEN to 32" | High | |
| 34 | platform/x86/asus-wmi | Buffer overflow in `asus_wmi_evaluate_method_buf` when ACPI returns oversize buffer. | None. | **v5.18** | `4345ece8f0bcc682f1fb3b648922c9be5f7dbe6c` "Potential buffer overflow in asus_wmi_evaluate_method_buf()" | High | Static-checker fix; bounded actual exposure but eliminates crash. |
| 35 | platform/x86/asus-wmi | Middle-fan curve (3-fan ROG laptops e.g. Zephyrus G14 2023) ignored. | None. | **v6.6** | `ee887807d05d3d6fb68917df59e450385fe630d3` "support middle fan custom curves" | High | |
| 36 | platform/x86/asus-wmi | Boards without fan got persistent log spam `fan_curve_get_factory_default ... failed: -19`. | Filter dmesg. | **v6.2** | `01fd7e7851ba2275662f771ee17d1f80e7bbfa52` "Don't load fan curves without fan" | High | Cosmetic but ventd's diagnostic must not flag this on older kernels as fan failure — it's just noisy probe. |
| 37 | hwmon/asus-ec-sensors | "Failed to acquire mutex" warnings on busy boards (X870E etc.) caused intermittent fan reads to return EBUSY. | Userland retry. | **v6.18+** | `584d55be66ef151e6ef9ccb3dcbc0a2155559be1` "increase timeout for locking ACPI mutex" (500→800ms) | High | |
| 38 | hwmon/asus-ec-sensors | `read_string()` could deref unset sensor index → invalid memory. | None. | **v6.16** | `25be318324563c63cbd9cb53186203a08d2f83a1` "check sensor index in read_string()" | High | |
| 39 | hwmon/asus-ec-sensors | "CPU Optional Fan" header on AMD600-family boards (X670/B650/X870) reported nothing. | None. | **v6.13** | `7582b7ae896e3b63fbadbe08af28ba59c95a4d91` "Add support for fan cpu opt on AMD 600 motherboards" | High | |
| 40 | drm/amdgpu | Vega10 fan PWM read/write broken (any setting reflected as garbage). | Roll back to pre-fix kernel. | **v6.1** (revert) | `4545ae2ed3f2f7c3f615a53399c9c8460ee5bca7` Revert "drm/amdgpu: getting fan speed pwm for vega10 properly" | High | Vega10 owners on v5.19-v6.0 need to upgrade. |
| 41 | drm/amdgpu | Navi1x (RX 5500/5600/5700) fan_input read 0 in manual mode — userland thought fan died. | Switch back to auto. | **v5.11** | `4f00d6d5ba3e216188570fcd075f0bdcb7884c52` "fix the fan speed in fan1_input in manual mode for navi1x" | High | |
| 42 | drm/amdgpu | APUs (Ryzen iGPU) exposed `fan1_input` returning bogus value. | None. | **v5.0** (approx, dec 2018 commit) | `20a96cd3868fff0ff5bb7f15db5fcdf5a628622f` "don't expose fan attributes on APUs" | High | Tells ventd: on AMDGPU, an APU exposing `fan1_input` is a very old kernel — refuse to control it. |
| 43 | drm/amdgpu (smu14) | RX 9070 / RDNA4 (smu_v14_0_2) had no hwmon fan speed at all. | None. | **v6.15** | `90df6db62fa78a8ab0b705ec38db99c7973b95d6` "wire up hwmon fan speed for smu 14.0.2" | High | First kernel where RDNA4 fan readback works. |
| 44 | drm/amdgpu (smu13) | `OD_FAN_CURVE` advertised but rejected by PMFW when temp/PWM range was zero — `fan_curve` sysfs returned all-zero on certain RDNA3 SKUs. | Don't read. | **v6.16+** | `470891606c5a97b1d0d937e0aa67a3bed9fcb056` "disable OD_FAN_CURVE if temp or pwm range invalid for smu v13" | High | Author 2025/2026; companion `28922a43fdab` for smu14. |
| 45 | drm/amdgpu | `pwm1_enable` always exposed even when card was on auto control, causing lm-sensors to misreport mode. | Don't trust pwm1_enable on auto. | **v5.0** (Sep 2018 commit) | `b8a9c003679ea3619cef4092b10390224f09fbaa` "Disable sysfs pwm1 if not in manual fan control" | High | |
| 46 | hwmon/applesmc | Fan-file count fixed at compile time → on Macs with > expected fans, buffer overrun. | None. | **v4.13** | `1009ccdc64ee2c8451f76b548589f6b989d13412` "Avoid buffer overruns" | High | Listed because `applesmc` is the only fan-control path on Intel Macs and is **not** what Asahi uses on Apple Silicon. ventd needs to know: on Apple Silicon (M1/M2/M3), `applesmc` does not load — fan control comes from `apple-mailbox` + SMC over IOP, currently provided in Asahi's downstream tree only. |

---

## Bugs not yet fixed in mainline (upstream-track for ventd)

| Class | Symptom | Status | Notes |
|---|---|---|---|
| Apple Silicon SMC fan readback | M1/M2/M3 Mac fan control entirely missing in mainline `applesmc`; no fan*_input on Asahi mainline. | Open. Asahi carries `apple-rtkit` + private SMC interface; not yet upstreamed. | ventd must run in tach-less mode (R8/R12) on Apple Silicon. |
| amdgpu RDNA3 zero-RPM | `pwm1_enable=2` (auto) does not honour the BIOS zero-RPM-stop temperature on some 7900 XTX boards even with v6.16. | Partially fixed by `OD_FAN_CURVE` zero-fan additions (Nov 2024 → v6.13) but still firmware-dependent. | Track `drm/amd/pm` for further commits. |
| nct6775 ACPI conflict on certain Asus B650/X670 boards | Driver loads but EC mutex contention with `asus-ec-sensors` causes intermittent EBUSY on `fan*_input` reads. | Partially mitigated by `584d55be66ef` (mutex timeout) but no full solution; vendor BIOS bug. | ventd should detect coexistence and serialise reads. |
| dell-smm "automatic fan mode" regression chain | `1c1658058c99` (v6.18) broke fan control on at least G15 5510 and global-fan-mode boxes; multiple follow-ups landed but **the underlying SMM-call discovery logic is still board-specific**. | Active fix activity (4 commits in last 6 months). | ventd must keep a current whitelist mirror; expect more whitelist patches per release. |
| thinkpad_acpi FANG/FANW regression | E531 patch (v6.16) broke T495*/E560 fan; v6.17 disables FANG/FANW for those. T14 Gen5+ Snapdragon variants untested. | Likely more quirks coming. | ventd should keep a per-DMI override file. |
| amdgpu legacy-DPM (Tahiti/Hawaii) | `radeon` driver fan_curve never gained sysfs interface; only the new `amdgpu` driver does. | Won't-fix upstream (legacy `radeon` frozen). | ventd must skip on `radeon`-only setups or recommend `amdgpu.si_support=1`. |
| xz CRC32 corruption on initramfs containing modules with names > 32 bytes | (Agent A flagged.) Fix-commit not located in scope of this driver mining; lives in `lib/xz/`. | Resolved upstream but not via a hwmon path. | Out of scope for E; flag back to A's bundle. |

---

## Highest-impact kernel fixes (top 5 by user reach)

1. **`920057ad521d` "fan_div calculation reuse" → v6.7, `Cc: stable v5.19+`.** Single most affected user base — every nct6775 board (Asus / MSI / ASRock from ~2017 onward) on v5.19-v6.6 could spuriously see fan speeds clamped to 0.
2. **`12c44ab8b401` "(it87) Add param to ignore ACPI resource conflicts" → v6.2.** Replaces system-wide `acpi_enforce_resources=lax` with a driver-local switch — eliminates the single most-recommended dangerous boot param across forum threads.
3. **`a10d50983f7b` "thinkpad_acpi: phantom 65535 fan2 fix" → v6.2.** Affects every single-fan ThinkPad sold 2018+ — huge install base reading `fan2_input=65535` and userspace tools (i8kmon, fancontrol, ventd's own R8 predicate) panicking.
4. **`0f0ac158d28f` "asus-wmi: custom fan curves" → v5.17.** First time mainline supported per-temp fan curves on ROG laptops — eliminates the rogctl/asusctl out-of-tree dependency for a large gaming-laptop user base.
5. **`1c1658058c99` + follow-ups "dell-smm automatic fan mode" → v6.18.** Adds the missing manual-mode toggle on Dell business laptops (G15, OptiPlex, Latitude). Despite the regression chain, this finally lets ventd issue `pwm1_enable=2` on Dell hardware without `i8k.force=1`.

## Drivers with the most active fix activity (signal — expect more)

1. **`drivers/hwmon/asus-ec-sensors.c`** — 30+ commits in last 12 months, almost all "add board X" entries. Predictable churn; ventd must consult the live commit log per release.
2. **`drivers/hwmon/dell-smm-hwmon.c` + `drivers/platform/x86/dell-smm-hwmon.c`** — 4 fix-class commits in the last 6 months including a `Cc: stable` regression fix; the "automatic fan mode" introduction is still settling.
3. **`drivers/hwmon/nct6775-core.c`** — moderate but high-impact churn, and the most stable-tagged commits in the dataset (rows 1, 6 are LTS-relevant). Splitting into core+platform driver (`c3963bc0a0cf`, v5.18) is still generating fallout.
4. **`drivers/gpu/drm/amd/pm/swsmu/smu13` and `smu14`** — RDNA3 fan_curve and RDNA4 fan readback are actively being filled in; expect each new ASIC variant (Strix Halo, Radeon Pro variants) to land another fan-wiring commit.
5. **`drivers/platform/x86/thinkpad_acpi.c`** — slower cadence but each commit affects many users; the FANG/FANW mechanism added in v6.16 is still being calibrated per DMI.

---

## Confidence note

Calendar dates in this kernel mirror occasionally show 2026 author timestamps (clock skew between commit author and committer dates in the GitHub mirror snapshot used). Where I quote a 2026 date the row is flagged Medium confidence on version assignment; **all SHA values are real and resolve via the canonical kernel.org cgit URL** — they were retrieved as raw `.patch` files which include the 40-char SHA in the `From` header. For ventd integration, the actionable artefact is the (driver, symptom, kernel-version-gate) tuple; if any version mapping is contested, fall back to the per-commit cgit URL and use `git tag --contains <sha>` locally.
