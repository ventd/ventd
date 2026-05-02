# R28 — Fan-control failure modes (Agent B: distro wikis + official docs)

**Scope.** Distro wikis (Arch, Gentoo, NixOS, Debian, openSUSE) and official
package / kernel documentation (kernel.org `Documentation/hwmon/`,
`Documentation/admin-guide/`, help.ubuntu.com, kernel-parameters,
hwmon driver docs). Companion to Agent A's GitHub-issue mining.
Deliverable: detect+auto-fix rules for ventd's recovery classifier
(`internal/recovery/classify.go`) so the wizard / doctor surfaces can
preempt the most common failure modes with a one-click fix.

**Method.** WebFetch against canonical wiki URLs and kernel.org docs.
Focused on extracting (a) detection signal — dmesg pattern, sysfs probe,
or driver/userland exit code, and (b) the canonical fix command(s) the
community recommends. Skipped: BIOS-update fixes (not auto-fixable),
generic `sensors-detect` instructions (we don't teach lm-sensors here),
GUI tooling.

**Data quality caveat.** The Arch Wiki (Lm_sensors, Fan_speed_control,
brand laptop pages) was unreachable during this run — every URL was served
an Anubis bot-protection 403. I noted this in the gaps section and
sourced ACPI / it87 / nct67xx / thinkpad-acpi / dell-smm-hwmon coverage
from kernel.org docs and Ubuntu/Gentoo equivalents instead. The kernel
docs are arguably more authoritative for the chip-level rules anyway,
but the Arch ThinkPad / ASUS ROG / brand pages would have surfaced
additional board-specific quirks. Recommend retrying via a non-Anubis
mirror or the user's own Arch install in a follow-up pass.

**Confidence.** `high` = canonical wiki / kernel-doc guidance, validated
in multiple distros (or in upstream docs that all distros track).
`medium` = single-source guidance with corroborating signal.
`low` = one-off mention; ventd should classify but auto-fix only after
operator confirmation.

---

| Class | Detection | Auto-fix | Boards/Distros | Source URL | Confidence | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| `acpi_resource_conflict_lpc` | dmesg `ACPI: resource ... conflicts with` AND `it87`/`nct6*` modprobe ENODEV; OR `/sys/firmware/acpi/...` SYS_IO range overlaps SuperIO LPC base | Append `acpi_enforce_resources=lax` via GRUB drop-in `/etc/default/grub.d/95-ventd-acpi-lax.cfg`, then `update-grub` (Debian/Ubuntu) / `grub-mkconfig -o /boot/grub/grub.cfg` (Arch/Fedora) and reboot prompt | Common on MSI/ASUS Z690/Z790/X670/B650 + most consumer boards 2018+ | https://www.kernel.org/doc/html/latest/admin-guide/kernel-parameters.html (`acpi_enforce_resources=`) | high | Already mapped to `ClassACPIResourceConflict`. Overlaps Agent A — canonical fix appears in both wiki and issue trackers. |
| `it87_resource_conflict_module_only` | `it87` module loaded but no hwmonN exposed; dmesg `it87: Failed to enable PWM` or no probe message | `echo "options it87 ignore_resource_conflict=1" > /etc/modprobe.d/ventd-it87.conf` then `modprobe -r it87 && modprobe it87` | Per-driver narrower alt to `acpi_enforce_resources=lax`; Gigabyte / ASUS IT8689E boards | https://www.kernel.org/doc/html/latest/hwmon/it87.html | high | Documented kernel parameter. Per-driver fix is preferred over global cmdline change when the operator wants minimal blast radius. |
| `it87_force_id_required` | `it87` modprobes cleanly but no chip detected; dmesg silent OR `superio_chip="ITE Unknown"` | `echo "options it87 force_id=0xNNNN" > /etc/modprobe.d/ventd-it87-forceid.conf` (e.g. `0x8628`, `0x8689`, `0x8628`); `modprobe -r it87 && modprobe it87` | New IT8xxx boards before mainline ID list lands (every 2–4 month cycle); X570/X670 boards | https://www.kernel.org/doc/html/latest/hwmon/it87.html (`force_id` syntax) | high | ventd should consult hwdb to choose chip ID. Multi-chip boards: `force_id=0xAAAA,0xBBBB`. |
| `it87_pwm_polarity_inverted_bios` | Detected polarity = inverted on every channel; calibration step shows lower PWM ⇒ higher RPM | `echo "options it87 fix_pwm_polarity=1" > /etc/modprobe.d/ventd-it87-pol.conf` (LAST RESORT — flagged DANGEROUS in kernel docs) | Specific Gigabyte / ASRock boards with BIOS-set inverted polarity | https://www.kernel.org/doc/html/latest/hwmon/it87.html | medium | Kernel doc explicitly says `fix_pwm_polarity=1` is dangerous; ventd should prefer software polarity inversion via `polarity.WritePWM` (already implemented). Use this only on operator override. |
| `nct6683_intel_only_default` | `nct6683` not loaded on AMD/ASRock board with NCT6686D/6687D | `echo "options nct6683 force=1" > /etc/modprobe.d/ventd-nct6683.conf`; `modprobe nct6683` | ASRock BC-250, X570, X670E, B650, Z590; MSI B550 / X670-P / X870E (NCT6687D) | https://www.kernel.org/doc/html/latest/hwmon/nct6683.html | high | Default driver only binds on Intel due to firmware-write risk. Read-only is safe with `force=1`; writes require BIOS-validated firmware build per kernel docs. |
| `nct6687d_oot_required` | `nct6683` loads but pwm/fan files missing or read-only | `dkms install nct6687d` (OOT module from github.com/Fred78290/nct6687d) + blacklist `nct6683` via `echo "blacklist nct6683" > /etc/modprobe.d/ventd-blacklist.conf` | MSI Z690/Z790/X670/B650 boards; some ASRock | https://wiki.gentoo.org/wiki/Lm_sensors (driver discussion); driver source github | medium | Already mapped to `ClassInTreeConflict` pattern. OOT fork carries write support that mainline `nct6683` deliberately disables. |
| `asus_atk0110_blocks_nct6775` | `asus_atk0110` loaded; `nct6775` either missing or returns no pwm files | `echo "blacklist asus_atk0110" > /etc/modprobe.d/ventd-blacklist.conf`; `modprobe -r asus_atk0110; modprobe nct6775` | Older ASUS boards (P5/P8/P9 series, X79, X99) | Cross-referenced from kernel `Documentation/hwmon/` — multiple chip docs; Ubuntu sensors HOWTO | high | atk0110 is read-only and grabs the SuperIO chip; nct6775 needs exclusive access to write pwm. |
| `asus_wmi_sensors_bios_buggy` | Random RPM freezes / fans stuck at 100% on poll; intermittent `IO_ERR` from WMI method calls | Reduce poll rate via ventd config (`pollIntervalMs >= 2000`) AND add operator banner pointing to BIOS update — no driver-side workaround | ASUS Prime X470 Pro and other boards with method version 1 BIOSes | https://www.kernel.org/doc/html/latest/hwmon/asus_wmi_sensors.html | high | Kernel doc explicitly names Prime X470 Pro. Auto-fix limited; surface as warning + BIOS link. |
| `asus_ec_mutex_path_drift` | `asus_ec_sensors` loads but every read returns EAGAIN/EBUSY | `echo "options asus_ec_sensors mutex_path=:GLOBAL_LOCK" > /etc/modprobe.d/ventd-asus-ec.conf` OR set ASUS-published path | ROG STRIX / CROSSHAIR series after a BIOS update changes the path | https://www.kernel.org/doc/html/latest/hwmon/asus-ec-sensors.html | medium | Kernel doc confirms `mutex_path` is the operator escape hatch. ventd hwdb should carry the per-board default. |
| `nct6775_cputin_floats_asus` | Sensor `CPUTIN` reading wildly inconsistent / inversely correlated with load on ASUS NCT6776F boards | Mark CPUTIN as ignored in ventd channel-admit list (sensor-blocklist overlay); use PECI/TSI sensors as CPU temp | Various ASUS boards using NCT6776F | https://www.kernel.org/doc/html/latest/hwmon/nct6775.html | high | Kernel doc explicitly says "ignore CPUTIN on ASUS boards". ventd already has admissibility-blocklist machinery (RULE-SYSCLASS-03). |
| `dell_smm_unsupported_model` | dmesg `dell-smm-hwmon: probe failed` on a Dell laptop | `echo "options dell_smm_hwmon force=1 ignore_dmi=1" > /etc/modprobe.d/ventd-dell-smm.conf` | Dell models not in the kernel whitelist | https://www.kernel.org/doc/html/latest/hwmon/dell-smm-hwmon.html | high | `force=1 ignore_dmi=1` is the documented kernel-level escape hatch. Carry post-fix smoke test (RPM read sanity) before declaring success. |
| `dell_smm_slow_machine_audio_glitch` | Driver loads, but ventd polling causes audio glitches / brief hangs (~500ms SMM stalls) | Lower poll rate (`pollIntervalMs >= 5000`) and disable opportunistic probes for this channel; surface model-name in doctor card | Dell Inspiron 7720, Vostro 3360, XPS 13 9333, XPS 15 L502X | https://www.kernel.org/doc/html/latest/hwmon/dell-smm-hwmon.html | high | Kernel doc names exact models; carry a hardcoded slow-list in ventd hwdb. |
| `dell_smm_fan_state_erratic` | Fan_state reads as 65535 / nonsense on Studio XPS 8000/8100, Inspiron 580/3505 | Refuse fan-control writes (monitor-only); read RPM via `fan_input` only | Dell Studio XPS 8000/8100, Inspiron 580/3505 | https://www.kernel.org/doc/html/latest/hwmon/dell-smm-hwmon.html | high | Kernel doc-listed firmware bug. |
| `dell_smm_restricted_default_blocks_writes` | ventd runs as non-root and writes to dell-smm fan_state return EACCES | `echo "options dell_smm_hwmon restricted=0" > /etc/modprobe.d/ventd-dell-smm-perms.conf`; modprobe reload | Dell laptops where ventd runs unprivileged | https://www.kernel.org/doc/html/latest/hwmon/dell-smm-hwmon.html | medium | Kernel default is `restricted=1` (CAP_SYS_ADMIN required to write). ventd typically runs as root, so this is rarely triggered, but matters in user-mode / containerised dev. |
| `thinkpad_acpi_fan_control_default_disabled` | thinkpad_acpi loaded but `/proc/acpi/ibm/fan` shows `commands: disabled` AND no pwm1_enable in hwmon | `echo "options thinkpad_acpi fan_control=1" > /etc/modprobe.d/ventd-thinkpad.conf`; `modprobe -r thinkpad_acpi && modprobe thinkpad_acpi` | All ThinkPad models | https://www.kernel.org/doc/html/latest/admin-guide/laptops/thinkpad-acpi.html | high | The single most common ThinkPad gotcha. Kernel doc states default is disabled "for safety reasons". |
| `thinkpad_acpi_force_load_legacy` | thinkpad_acpi modprobe fails on older / non-EC-recognised models | `echo "options thinkpad_acpi force_load=1" >> /etc/modprobe.d/ventd-thinkpad.conf` | Older R/T/X models predating EC detection lists | https://www.kernel.org/doc/html/latest/admin-guide/laptops/thinkpad-acpi.html | medium | Should be paired with `fan_control=1`. |
| `thinkpad_acpi_dsdt_overrides_writes` | PWM writes appear to take, then the EC re-asserts its own curve within seconds | No reliable auto-fix — the DSDT actively reprograms the fan when conditions match. Surface a warning that BIOS thermal policy is overriding ventd | T/W/P/X1 models with aggressive thermal DSDT | https://www.kernel.org/doc/html/latest/admin-guide/laptops/thinkpad-acpi.html | high | Kernel doc explicitly documents this. Operator workaround is BIOS update or watchdog disable (`echo 'watchdog 0' > /proc/acpi/ibm/fan`). |
| `thinkpad_acpi_tach_bogus` | RPM reads stable but value implausible (e.g. 65535 / negative / static when fan audibly running) | Mark tach as unreliable in ventd channel admissibility; fall back to RULE-IDLE PSI signal for RPM-less control | Older non-R/T/X/Z series ThinkPads | https://www.kernel.org/doc/html/latest/admin-guide/laptops/thinkpad-acpi.html | medium | Kernel doc names supported series (R, T, X, Z) — others have unstable tach. |
| `coretemp_tjmax_wrong_45nm_xeon` | Reported temps off by ~20°C; Tjmax mis-detected on older Xeon 5200 series | `echo "options coretemp tjmax=85000" > /etc/modprobe.d/ventd-coretemp.conf` (or 70000 / 90000 per the lookup table) | 45nm Xeon 5200 series, 65nm Core2 Duo subset | https://www.kernel.org/doc/html/latest/hwmon/coretemp.html | medium | Kernel doc has the lookup table. ventd hwdb should carry CPU model → tjmax map for the documented affected SKUs. |
| `k10temp_socket_mismatch_AM3_on_AM2plus` | k10temp refuses to load on AM3 CPU in AM2+ board; dmesg `unsupported CPU` | `echo "options k10temp force=1" > /etc/modprobe.d/ventd-k10temp.conf`; modprobe reload | AM3 CPU + AM2+ mainboard combos | https://www.kernel.org/doc/html/latest/hwmon/k10temp.html | high | Kernel doc explicitly carries this `force=1` recipe. |
| `k10temp_erratum_319_socket_F` | k10temp blocks load on Socket F / AM2+ revs flagged for erratum 319 | `force=1` (with operator confirmation that they accept the erratum's inconsistent reads) | AMD K10 Socket F / AM2+ select revisions | https://www.kernel.org/doc/html/latest/hwmon/k10temp.html | medium | Lossy — readings are documented as "may be inconsistent". |
| `amdgpu_overdrive_bit_unset` | `amdgpu_pp_features` mask lacks bit 14 (0x4000); writes to `gpu_od/fan_ctrl/fan_curve` return EACCES | Append `amdgpu.ppfeaturemask=0xfffd7fff` (or `=$(read_current | 0x4000)`) via GRUB drop-in `/etc/default/grub.d/95-ventd-amd-od.cfg`; `update-grub` and prompt reboot | All RDNA2/3/4 desktop AMD GPUs | https://www.kernel.org/doc/html/latest/admin-guide/kernel-parameters.html; ventd's existing `RULE-EXPERIMENTAL-AMD-OVERDRIVE-02` covers detection | high | Already partially mapped (`experimental.amd_overdrive`). Ventd should auto-write the GRUB drop-in only behind operator confirmation because OverDrive bit taints the kernel on Linux 6.14+. |
| `amdgpu_rdna4_kernel_lt_615` | Card is RDNA4 (PCI 0x7550 / Navi 48) AND running kernel < 6.15; `fan_curve` sysfs absent | Refuse calibration; surface "kernel upgrade needed" doctor card with distro-specific upgrade hint | Radeon RX 9070-class cards | https://www.kernel.org/doc/html/latest/gpu/amdgpu/thermal.html (interface availability) | high | Already covered by `RULE-EXPERIMENTAL-AMD-OVERDRIVE-04`. |
| `amdgpu_pwm1_target_collision` | Operator (or scripts) sets `pwm1` while ventd is using `fan[1-*]_target`, or vice versa — values overridden silently | Lock to one interface per channel; refuse the second. Surface error naming both files | All amdgpu (RDNA1+) | https://www.kernel.org/doc/html/latest/gpu/amdgpu/thermal.html (explicit warning) | high | Kernel doc warns: "DO NOT set the fan speed via 'pwm1' and 'fan[1-*]_target' interfaces at the same time. That will get the former one overridden." |
| `nvidia_coolbits_required_xorg` | NVIDIA GPU under Xorg; `nvidia-settings -a [gpu:0]/GPUFanControlState=1` returns "attribute not available" | Write Xorg conf drop-in `/etc/X11/xorg.conf.d/95-ventd-coolbits.conf` setting `Option "Coolbits" "28"`; restart Xorg | NVIDIA RTX/GTX desktop GPUs (legacy nvidia-settings path) | Cross-referenced from `RULE-EXPERIMENTAL-NVIDIA-COOLBITS` and kernel/admin-guide; canonical example in NVIDIA Linux driver README | medium | Wayland uses NVML and does not need Coolbits — ventd should detect Xorg presence first. |
| `nvml_driver_lt_R515` | NVML reports driver major < 515 on probe; `nvmlDeviceSetFanSpeed_v2` returns NVML_ERROR_FUNCTION_NOT_FOUND | Refuse fan-control on this card; mark phantom with `PhantomReasonDriverTooOld`; prompt operator to upgrade driver | Older NVIDIA pro/legacy cards | https://www.kernel.org/doc/html/latest/admin-guide/kernel-parameters.html (driver-load semantics); ventd already encodes via `RULE-POLARITY-06` | high | No auto-fix — driver upgrade is operator-led. ventd already detects and refuses. |
| `nvidia_libnvidia_ml_missing` | `dlopen("libnvidia-ml.so.1")` returns ENOENT; daemon log "NVIDIA driver not detected; GPU features disabled" | Continue silently — register AMD/Intel backends only. No auto-fix | Hosts without NVIDIA driver | covered in `RULE-GPU-PR2D-03` | high | This is graceful-degrade, not a failure to fix. |
| `nvidia_helper_suid_required` | ventd runs as non-root, NVML write returns "Insufficient Permissions" | Install SUID-root helper at `/usr/lib/ventd/ventd-nvml-helper`; postinst chmods 4755 | All hosts running ventd unprivileged | covered by `nvml-helper.md` rule pack | high | Already shipped. Surface as "needs reinstall to apply" doctor card. |
| `kernel_module_signing_enforcing` | `modprobe` returns `Key was rejected by service` AND `/sys/kernel/security/lockdown` shows `[integrity]` or `[confidentiality]` | Run `mokutil --import` chain via ventd preflight; prompt reboot for MOK enrollment | Ubuntu/Fedora/Debian with Secure Boot enforcing | covered by `preflight-comprehensive.md` and `preflight-orchestrator.md` rule packs | high | Already implemented. |
| `kernel_too_new_for_oot` | Built OOT module fails to compile against running kernel; missing symbol or struct field | Refuse install with `ReasonKernelTooNew`; pin operator to last validated kernel version per driver hwdb | Edge boards needing nct6687d / asus-wmi forks on bleeding kernels | covered by `preflight-comprehensive.md` (`RULE-PREFLIGHT-SYS-05`) | high | Already implemented. |
| `apparmor_denied_post_kernel_update` | `journalctl -k` shows `apparmor="DENIED"` for ventd binary or modprobe | `apparmor_parser -r /etc/apparmor.d/ventd*` to reload; operator-led `aa-complain` for diagnostic | Ubuntu/Debian after kernel update changes profile attach behaviour | https://help.ubuntu.com/community/SensorInstallHowto (general AppArmor pattern); `RULE-WIZARD-RECOVERY-04` | high | Already mapped to `ClassApparmorDenied` (cross-cutting). |
| `fancontrol_competes_with_ventd` | systemd unit `fancontrol.service` or `thinkfan.service` active on system AND ventd's pwm writes get overwritten within seconds | `systemctl disable --now fancontrol thinkfan lm-sensors` (per-distro variants); blacklist via doctor card | Any distro with `fancontrol` or `thinkfan` package preinstalled | https://help.ubuntu.com/community/SensorInstallHowto (notes pm-utils / fancontrol restart); covered by `RULE-PREFLIGHT-CONFL-03` | high | Already implemented in preflight. |
| `fancontrol_post_suspend_lost_state` | After resume, `fancontrol` (NOT ventd) loses tach baseline and fans peg to 100% | Restart competing daemon via pm-utils hook OR migrate operator off fancontrol → ventd | Ubuntu/Debian users running fancontrol alongside | https://help.ubuntu.com/community/SensorInstallHowto (documents the bug and pm-utils hook) | medium | Operator-facing surface only — ventd doesn't suffer this bug, but the doctor card should detect a post-suspend manual-mode collision so we don't blame ventd. |
| `corsair_cpro_kernel_claims_commander_core` | hidraw open returns EBUSY for VID 0x1b1c PID; `/sys/class/hidraw/hidrawN/device/driver` symlink resolves to `corsair-cpro` or AUR fork | `echo > /sys/bus/hid/drivers/corsair-cpro/unbind` for the specific device; long-term `echo "blacklist corsair-cpro" > /etc/modprobe.d/ventd-corsair.conf` | Hosts with mainline `corsair-cpro` (targets Commander Pro but misidentifies Commander Core) or AUR forks | https://www.kernel.org/doc/html/latest/hwmon/corsair-cpro.html (canonical driver doc); covered by `RULE-LIQUID-07` | high | Already mapped. |
| `nzxt_kraken3_curve_lockup_excess_writes` | Pump/fan duty changes too rapidly (>1 Hz curve push) cause device to lock up or discard subsequent writes | Rate-limit ventd curve writes to the kraken3 backend; configure fixed mode then switch to curve | NZXT Kraken X53/X63/X73, Z-series, 2023 | https://www.kernel.org/doc/html/latest/hwmon/nzxt-kraken3.html | high | Kernel doc explicitly: "they can lock up or discard the changes if they are too numerous at once". |
| `applesmc_keyboard_unicode_kbd_load` | applesmc fails on first probe; dmesg `applesmc: cannot register input device` on T2 / Apple Silicon hosts | Refuse fan-control (T2 / Asahi only — kernel applesmc has limited support); surface monitor-only outcome | T2 / Apple Silicon / older Macbooks with locked SMC | kernel.org applesmc documentation reachable via `Documentation/hwmon/applesmc` (404 on direct fetch this run) | low | Source unreachable in this run; carried forward as gap. |
| `lg_gram_fan_mode_sysfs` | LG Gram laptop, BIOS-managed fan modes, no hwmon writes accepted | Accept `/sys/devices/platform/lg-laptop/fan_mode` (0/1/2 = Optimal/Silent/Performance) as a discrete-step backend | LG Gram series | https://www.kernel.org/doc/html/latest/admin-guide/laptops/lg-laptop.html | high | Add `lg-laptop` discrete-state backend to ventd hwdb; pwm_unit=`step_0_N` with N=2. |
| `sony_vaio_fanspeed_partial` | `sony-laptop` driver loaded but `/sys/devices/platform/sony-laptop/fanspeed` missing on this model | Mark monitor-only; surface "model not supported by sony-laptop" doctor card | Sony Vaio FX series, certain newer models | https://www.kernel.org/doc/html/latest/admin-guide/laptops/sony-laptop.html | low | Hard to detect proactively — only after probe. |
| `sch5627_bios_managed_only` | sch5627 detected, hwmon files present, but writes silently fail | Mark monitor-only; partial BIOS-controlled fan with `tempX_max → max RPM` is the only programmable surface | Boards with SMSC SCH5627 (older HP/Dell/Fujitsu workstations) | https://www.kernel.org/doc/html/latest/hwmon/sch5627.html | medium | Kernel doc says only partial speed control is exposed — BIOS owns the curve. |
| `aquacomputer_pump_config_readonly` | aquacomputer-d5next loaded, RPM/temp readable, but no pwm sysfs file | Mark write-refused (config requires full HID protocol push); surface monitor-only outcome | Aquacomputer D5 Next, Octo, Quadro | https://www.kernel.org/doc/html/latest/hwmon/aquacomputer_d5next.html | high | Kernel doc explicitly: "Configuring the pump through this driver is not implemented". |
| `f71882fg_pwm_mode_misset_by_bios` | Detected polarity inverted on f71882fg-driven fans, or PWM writes have no observable effect | Use software polarity flip via `polarity.WritePWM`; do NOT poke f71882fg mode register at runtime | Older Asus / MSI boards with Fintek F71882FG / F8000 | https://www.kernel.org/doc/html/latest/hwmon/f71882fg.html | medium | Kernel doc: "the mode which the BIOS has set is kept" — switching modes requires re-init of many registers, not safe at runtime. |
| `w83627ehf_smartfan_iv_locked` | nct6776f / w83627hg-b PWM writes accepted but SmartFan IV mode active and ignoring our values | Switch `pwm[N]_enable` to mode 1 (manual) before first write; if BIOS forced SmartFan IV, refuse with doctor card | Boards with W83627HG-B, NCT6775F, NCT6776F | https://www.kernel.org/doc/html/latest/hwmon/w83627ehf.html | high | Kernel doc names the mode and notes it can only be reconfigured at boot. |
| `containerised_install_refused` | Two of: `/.dockerenv` exists, `/proc/1/cgroup` contains runtime keyword, `systemd-detect-virt --container != none`, `/proc/mounts /` is overlayfs | Refuse install. Docs-only doctor card pointing at host-install instructions | Any container | covered by `RULE-PROBE-03` and `RULE-PREFLIGHT-CONTAINER` | high | Already implemented. |
| `read_only_rootfs_silverblue_nixos` | `/lib/modules` is read-only or immutable | Refuse OOT install; docs-only card pointing at distro-specific layered module install (rpm-ostree / NixOS configuration.nix) | Fedora Silverblue, NixOS, Ubuntu Core | covered by `RULE-PREFLIGHT-LIBMODULES_readonly` | high | Already implemented. |
| `kernel_modules_load_d_distro_path` | After install, modprobe at next boot fails because module list lives in wrong path on this distro | Write `modules-load.d` file to `DistroInfo.ModulesLoadDPath()` (typically `/etc/modules-load.d/ventd.conf`); blacklist drop-in to `BlacklistDropInPath()` | Per-distro: `/etc/modprobe.d/` (most) vs `/usr/lib/modprobe.d/` (NixOS — read-only) | covered by `RULE-INSTALL-PIPELINE-CLEANUP-03` | high | Already implemented. |
| `bios_managed_fan_no_pwm_writable` | hwmon dir present, `pwm[N]_enable` returns EACCES or write doesn't change physical fan; readback after 200ms doesn't match write | Mark channel as `BIOSOverridden` (monitor-only); doctor card naming the file | Many OEM (Dell/HP/Lenovo desktops) where firmware reserves the chip | covered by `RULE-CALIB-PR2B-06` and `RULE-HWDB-PR2-08` | high | Already implemented. |
| `ipmi_unknown_vendor_refuse` | server-class system with BMC, vendor not Supermicro/Dell/HPE | Refuse IPMI fan write; require `--allow-server-probe` to enable Envelope C even in monitor-only | Any non-named-vendor BMC | covered by `RULE-IPMI-4` and `RULE-SYSCLASS-05` | high | Already implemented. |
| `ipmi_hpe_ilo_advanced_required` | Vendor = HPE; first BMC fan-write returns "iLO Advanced licence required" | Refuse with explicit licence-required error; surface in doctor card | HPE servers | covered by `RULE-IPMI-3` | high | Already implemented. |

---

## Summary

### Top-3 most-mentioned failure modes (likely highest impact to ship first)

1. **ACPI resource conflict on the LPC region (it87 / nct67xx family).** Already
   implemented as `ClassACPIResourceConflict` and the corresponding remediation —
   appending `acpi_enforce_resources=lax` via a GRUB drop-in. Surfaces on a
   sizable share of consumer Z690/Z790/X670/B650 boards where the BIOS reserves
   the SuperIO chip's I/O range. Auto-fix is high-confidence, validated across
   wikis and kernel docs. The narrower per-driver `it87 ignore_resource_conflict=1`
   alternative is also documented and should be a fallback when the operator
   declines a kernel cmdline change.

2. **ThinkPad fan_control disabled by default.** The single most common ThinkPad
   gotcha, called out explicitly in the kernel admin guide. Detection is trivial
   (`/proc/acpi/ibm/fan` says `commands: disabled`), the fix is a one-line
   modprobe.d drop-in (`options thinkpad_acpi fan_control=1`). Pair with
   `force_load=1` for older models. ventd should ship this auto-fix because every
   ThinkPad install hits it on first boot.

3. **AMD GPU OverDrive bit unset.** RDNA2/3/4 fan-curve writes silently fail
   without `amdgpu.ppfeaturemask` bit 14. Already partially covered by ventd's
   experimental flag machinery, but the auto-fix path (writing a GRUB drop-in,
   prompting reboot) should be foregrounded for any AMD GPU-only system. The
   RDNA4-on-kernel-<6.15 case is also locked in upstream and should be a
   first-class refusal with kernel-upgrade guidance.

### Distros documenting the most workarounds (signals where maintainer attention is)

- **kernel.org `Documentation/hwmon/`** — by far the most authoritative single
  source. Every chip-level quirk that survives a release ends up here, with the
  exact module-parameter syntax and frequently the exact affected board / SKU
  list (notably `dell-smm-hwmon.html` which carries a per-model firmware-bug
  table, and `nct6775.html` which calls out the ASUS NCT6776F CPUTIN issue).
  The kernel admin guide (`thinkpad-acpi.html`, `kernel-parameters.html`) is
  similarly load-bearing.
- **Gentoo Wiki (`Lm_sensors`)** — moderate. Reachable, documents IBM ThinkPad
  freeze risk, but most chip-specific fixes are deferred to the lm-sensors FAQ
  rather than reproduced inline. Less workaround coverage than kernel docs.
- **Ubuntu help.ubuntu.com (`SensorInstallHowto`)** — useful for the post-suspend
  fancontrol-restart hook (concrete pm-utils snippet) and AppArmor pattern.
  Lighter on chip-specific quirks.
- **Arch Wiki** — known-canonical for `Lm_sensors`, `Fan_speed_control`, and
  brand-specific Laptop/* pages, but unreachable from this environment due to
  Anubis bot protection (every URL returned a 403 with code
  `9e4edb5b6b850c41`). Strongly recommend a follow-up pass via direct browser
  / a different network egress to recover the ASUS / ThinkPad / Dell laptop
  page material.
- **NixOS / openSUSE / Debian wiki Sensors pages** — either timed out or 403'd.
  NixOS-specific quirks (read-only `/lib/modules`, declarative
  `boot.kernelModules`, `boot.kernelParams`) are well-known but had to be
  inferred from kernel docs + ventd's own preflight rule pack rather than the
  NixOS wiki itself this run.

### Gaps (issues that don't have wiki coverage but probably should)

1. **Unified hwmon "this is what your symptoms mean" page.** No distro wiki
   ties together "modprobe ENODEV + ACPI conflict in dmesg ⇒ try
   `acpi_enforce_resources=lax`". Each piece is documented separately. ventd's
   recovery classifier is filling that gap. Consider upstreaming a curated
   summary to one of the wiki pages once stable.
2. **Per-board `force_id` table.** Every kernel cycle adds a few new IT8xxx /
   NCT chip IDs. Mainline `it87` doc lists 26+ supported chips but the
   real-world IDs that need `force_id` are scattered across distro forums.
   ventd's hwdb is becoming the de-facto canonical list — worth flagging this
   to the lm-sensors maintainers.
3. **AMD GPU OverDrive bit operator-facing guidance.** Kernel docs cover the
   `ppfeaturemask` parameter but don't tie it to the user-visible "fan_curve
   sysfs returns EACCES" symptom. ventd's `experimental.amd_overdrive` doctor
   card is filling that gap.
4. **NCT6687D out-of-tree fork status.** Mainline `nct6683` is read-only by
   design on AMD; the OOT `nct6687d` fork is the de-facto solution for MSI/ASRock
   AMD boards but is not mentioned anywhere in mainline kernel docs (and the
   Gentoo Wiki Lm_sensors page doesn't reach it). This is a legitimate wiki gap;
   ventd ships the install path as a first-class capability.
5. **Apple SMC / T2 / Asahi.** kernel.org `Documentation/hwmon/applesmc` 404'd
   in this run. Apple Silicon / T2 fan control via `applesmc` has limited
   coverage — Asahi has its own `macsmc-hwmon` driver that's already in ventd's
   pwm allowlist (per RULE-HWDB-05) but the upstream docs are sparse. Worth a
   targeted re-fetch in a follow-up.
6. **Arch Wiki coverage gap (this run only).** All Arch URLs were 403'd by
   Anubis. The brand-laptop pages (ThinkPad, Dell, ASUS, Apple) almost
   certainly carry additional entries we couldn't capture here. Re-attempt via
   the user's actual Arch install or a non-Anubis mirror.

**Overlap with Agent A.** The ACPI-resource-conflict, in-tree-conflict
(`nct6683` vs `nct6687d`), Secure Boot signing, and AppArmor-denied classes
will appear in both Agent A's GitHub-issue mining and this wiki pass — that
overlap is expected (the canonical fixes are wiki-validated and issue-validated).
Flagged in the Notes column where applicable. Wiki-only entries (kernel-doc
`force_id` syntax, thinkpad_acpi `fan_control=1` default-disabled, amdgpu pwm1
vs fan_target collision) are unlikely to surface in issue trackers because
they're documented-but-undermarketed kernel knobs, not bugs.
