# hwmon: Comprehensive Reference for ventd

Research compiled 2026-04-24. Anchor kernel: 5.15 LTS floor, noting additions through 6.14+.

Purpose: single reference for ventd's fan-control daemon design. Covers kernel sysfs ABI, every driver family ventd may encounter, out-of-tree drivers, userspace tool prior art, and known gotchas. Skim the TOC; `Ctrl-F` for specifics.

---

## Table of Contents

1. [Kernel subsystem architecture](#1-kernel-subsystem-architecture)
2. [sysfs ABI: complete attribute reference](#2-sysfs-abi-complete-attribute-reference)
3. [PWM semantics and `pwmN_enable` modes](#3-pwm-semantics-and-pwmn_enable-modes)
4. [Driver catalogue by chip family](#4-driver-catalogue-by-chip-family)
5. [CPU thermal drivers](#5-cpu-thermal-drivers)
6. [Storage thermal (drivetemp, nvme)](#6-storage-thermal-drivetemp-nvme)
7. [GPU fan control (amdgpu, nouveau, nvidia)](#7-gpu-fan-control)
8. [Vendor-platform drivers (ASUS/MSI/Gigabyte/ASRock)](#8-vendor-platform-drivers)
9. [AIO and USB fan controllers](#9-aio-and-usb-fan-controllers)
10. [Laptop EC paths (thinkpad, dell-smm, cros_ec, framework)](#10-laptop-ec-paths)
11. [Server paths (IPMI, PECI)](#11-server-paths-ipmi-peci)
12. [ARM SoC / pwm-fan / thermal zones](#12-arm-soc--pwm-fan--thermal-zones)
13. [Out-of-tree drivers ventd must know about](#13-out-of-tree-drivers-ventd-must-know-about)
14. [Userspace prior art](#14-userspace-prior-art)
15. [Device naming and persistence](#15-device-naming-and-persistence)
16. [Fan mechanics: tach, pulses, spin-up, stop-start](#16-fan-mechanics-tach-pulses-spin-up-stop-start)
17. [Gotcha catalogue](#17-gotcha-catalogue)
18. [Go ecosystem for hwmon](#18-go-ecosystem-for-hwmon)
19. [Recommendations for ventd](#19-recommendations-for-ventd)

---

## 1. Kernel subsystem architecture

**Where hwmon lives.** Driver code is in `drivers/hwmon/` in the mainline tree. The core (`drivers/hwmon/hwmon.c`) provides registration helpers `hwmon_device_register_with_info` and `devm_hwmon_device_register_with_info` that let chip drivers focus on register access while the core creates the standard sysfs attributes. Chip drivers describe themselves with a `struct hwmon_chip_info` (channel types, attributes, visibility/read/write callbacks). The core also integrates with the thermal subsystem — `hwmon_thermal_add_sensor` registers each `tempN_input` as a thermal zone when one is declared for it in device tree / ACPI.

**Class vs. physical paths.** Every registered device appears as `/sys/class/hwmon/hwmonN` (a symlink to the physical device). Pre-lm-sensors-3.0.1, attributes had to live in the physical device directory; since 3.0.1, the hwmon class directory is also scanned. Modern drivers put everything in the class directory (best practice).

**Access bus types.** Drivers attach via many bus types:
- **LPC / Super-I/O** (ISA-like I/O ports): nct6775, it87, w83627ehf, sch5627/36, f71882fg etc.
- **PCI** (config space): k10temp, fam15h_power.
- **CPU MSR / platform**: coretemp.
- **I2C / SMBus**: lm75, lm85, adt7475, max31790, emc2103, nct7802 etc.
- **USB-HID**: corsair-cpro, corsair-psu, nzxt-kraken2/3, nzxt-smart2, aquacomputer-d5next, gigabyte-waterforce, asus-rog-ryujin.
- **WMI / ACPI**: asus_wmi_sensors, asus_wmi_ec_sensors, asus_ec_sensors, hp-wmi-sensors, dell-smm-hwmon (SMM), surface_fan.
- **BMC / PECI**: peci-cputemp, peci-dimmtemp, occ-hwmon, ibmaem, xgene-hwmon.
- **SCSI/ATA**: drivetemp.
- **NVMe**: built into NVMe subsystem (CONFIG_NVME_HWMON).
- **DRM/amdgpu**: exposed under `/sys/class/drm/cardN/device/hwmon/hwmonN`.
- **Generic PWM**: pwm-fan (uses kernel PWM framework + optional tach IRQ).

The key consequence: **ventd must enumerate all `/sys/class/hwmon/*` entries and classify by `name`**, not assume any particular bus layout.

---

## 2. sysfs ABI: complete attribute reference

All values are **fixed-point decimal**. Writes that aren't valid numbers are interpreted as `0` — libsensors-style clients must always format input carefully. Standard unit conventions:

| Type | Unit | Scaling |
|---|---|---|
| `inN_*` voltage | millivolts | raw mV |
| `tempN_*` temperature | millidegrees Celsius | × 1000 |
| `fanN_input` | RPM | raw RPM |
| `pwmN` | 0–255 | raw duty byte |
| `pwmN_freq` | Hz | raw Hz |
| `currN_*` | milliamps | raw mA |
| `powerN_*` | microwatts | × 1_000_000 |
| `energyN_*` | microjoules | × 1_000_000 |
| `humidityN_*` | millipercent | × 1000 |

**Numbering convention.** Voltage inputs start at 0 (historical datasheet convention). Everything else starts at 1.

**Global attributes per hwmon device:**
- `name` — chip name (required).
- `label` — optional descriptive label.
- `update_interval` — chip's internal update rate in ms (rarely writable).

**Voltages (`inN_*`):** `input`, `min`, `max`, `lcrit`, `crit`, `average`, `lowest`, `highest`, `reset_history`, `label`, `enable`, `rated_min`, `rated_max`. `cpuN_vid` for VID pins. `vrm` for VRM version.

**Fans (`fanN_*`):** `input` (RPM), `min`, `max`, `target` (closed-loop target RPM), `div` (legacy divisor), `pulses` (tach pulses per revolution — **critical for accurate RPM**), `label`, `enable`, `fault`, `alarm`, `min_alarm`, `max_alarm`, `beep`.

**PWM (`pwmN`, `pwmN_*`):** `pwmN` is the duty cycle byte (0–255). Supporting attributes: `enable`, `mode` (0 = DC/voltage, 1 = PWM), `freq` (base PWM Hz), `auto_channels_temp` (bitmask — which temp channels drive auto mode for this PWM). Curve points: `auto_point[1-*]_pwm`, `auto_point[1-*]_temp`, `auto_point[1-*]_temp_hyst`. On some chips the curves hang off the temp channel instead: `tempN_auto_point[1-*]_*`.

**Temperatures (`tempN_*`):** `input`, `min`, `max`, `crit`, `crit_hyst`, `emergency`, `emergency_hyst`, `lcrit`, `lcrit_hyst`, `max_hyst`, `min_hyst`, `offset`, `type` (sensor kind: thermistor, diode, PECI, etc.), `label`, `lowest`, `highest`, `reset_history`, `enable`, `rated_min`, `rated_max`, `fault`, `alarm`. A chip that expresses temp via voltage ADC (external thermistor) reports the value in **millivolts, not millidegrees** — the driver declares it that way and userspace must convert.

**Alarms.** Two styles: one `fanN_alarm`/`tempN_alarm` channel-wide flag, or per-limit (`tempN_max_alarm`, `tempN_crit_alarm`, ...). A given chip uses one style. Alarms on most chips latch and clear on read.

**Fault.** `fanN_fault` / `tempN_fault` = 1 means the reading should be distrusted. Common for an unused/unplugged fan header (tach = 0 but not a real stall).

**Power/Current/Energy.** `powerN_*` microwatts, `currN_*` milliamps, `energyN_*` microjoules. Power chips may offer `_cap`, `_accuracy`, `_average_interval`.

**Intrusion.** `intrusionN_alarm` — sticky, clears on write 0.

**Full ABI reference:** `Documentation/ABI/testing/sysfs-class-hwmon` and `Documentation/hwmon/sysfs-interface.rst` in the kernel tree.

---

## 3. PWM semantics and `pwmN_enable` modes

**The canonical ABI (this is non-negotiable across all drivers):**

| `pwmN_enable` | Meaning |
|---|---|
| 0 | No fan speed control — fan at full speed (manual at max) |
| 1 | Manual mode — write `pwmN` to set duty 0–255 |
| 2+ | Automatic fan speed control (chip-specific, see per-driver docs) |

Chip-specific `2+` modes on nct6775 (for example): 2 = Thermal Cruise, 3 = Fan Speed Cruise, 4 = Smart Fan III (NCT6775F only), 5 = Smart Fan IV (multi-point curve). Each higher mode layers on more chip-resident auto-control attributes.

**The "off" fiction.** `pwmN_enable = 0` semantically means "no control = full speed", because the ABI specifies fans should NEVER be silently stopped by the kernel. Drivers that would naturally allow a true "off" have to fake it: e.g. nct6775 implements `enable=0` by switching to manual mode and writing `pwm=255`. That's why there was an LKML exchange in Nov 2023 where Guenter Roeck refused a patch trying to change `nct6775` to report `off` when `pwm==255` — the ABI says 0 = full speed, period, and setting `pwm` to 254 while `pwm_enable==0` implicitly flips manual mode on.

**`pwmN_mode`.** 0 = DC (voltage control — for 3-pin fans), 1 = PWM (for 4-pin fans). Some chips can't switch at runtime; some lock this per-header at board-time.

**`pwmN_freq`.** PWM carrier frequency. Lower carriers (~25 kHz is the Intel 4-wire PWM spec; many boards default lower like 1–30 kHz) help some fans, hurt others. Per nct6775 docs: if small `pwmN` values don't produce control variance, try lowering `pwm1_freq`. On nct/it87, all PWMs share one freq so only `pwm1_freq` is writable.

**Curve attributes (`pwmN_auto_point[1-7]_*`).** Chip-resident curves. The convention: each point pairs a temperature with a PWM value; temperatures must be monotonically increasing; below the first point the fan uses the `pwmN_floor` (if supported) or stops; above the last point it runs at the last point's PWM. nct6775 additionally exposes `pwmN_start`, `pwmN_stop_time`, `pwmN_step_up_time`, `pwmN_step_down_time`, `pwmN_temp_tolerance`, `pwmN_crit_temp_tolerance`, `pwmN_temp_sel` (which temp channel feeds which PWM), and weighted secondary-temp attributes.

**Key ventd decision.** Chip-resident curves are power-loss-safe and avoid polling but are tied to whatever temp sources the chip sees (internal SYSTIN/CPUTIN, PECI, etc.). Userspace curves (ventd's model) give flexibility (any temp source including NVMe, drive, GPU, network card) but must poll and survive suspend/resume.

---

## 4. Driver catalogue by chip family

This is the complete mainline list as of kernel 7.0-rc. Organized by practical relevance to homelab/NAS.

### 4.1 Super-I/O / LPC (the big one for desktops/NAS)

**Nuvoton NCT67xx family** — driver: `nct6775`. Supports NCT6102D, 6104D, 6106D, 5572D, 6771F, 6772F, 6775F (W83677HG-I), 5573D, 5577D, 6776D/F, 5532D, 6779D, 6791D, 6792D, 6793D, 6795D, 6796D, 6796D-S, 6799D-R. NCT6796D-S and 6799D-R share the same datasheet; driver treats them as one. Added IDs in recent kernels: 0xd802 was first thought to be 6796D-S but is actually 6799D in newer kernels. This family covers most mainstream consumer motherboards (ASUS, MSI, Gigabyte, ASRock) from ~2013 onward. Up to 7 PWM channels. Fan control modes: manual, Thermal Cruise (2), Fan Speed Cruise (3), Smart Fan IV (5 — the multi-point curve mode). Reports up to 10 of 25 possible temp sources.

**Nuvoton NCT6683D / NCT6686D / NCT6687D** — driver: `nct6683` (in-tree). This driver is intentionally restricted: it whitelists only specific vendor strings (Mitac, Intel firmware, ASRock since 5.12). Most AMD MSI and ASRock boards using these chips are NOT supported in-tree for fan control; the driver is effectively read-only on those.

**ITE IT87 family** — driver: `it87`. Supports IT8603E, 8620E, 8623E, 8628E, 8705F, 8712F, 8716F, 8718F, 8720F, 8721F (IT8758E), 8726F, 8728F, 8732F, 8771E, 8772E, 8781F, 8782F, 8783E/F, 8786E, 8790E, 8792E (8795E), 87952E, SiS950 (clone of 8705F). Many newer ITE chips (IT8665E, IT8613E, IT8792E-2, IT8607E etc.) only work via **frankcrawford/it87 out-of-tree fork** which carries additional IDs and fixes — see §13. Key module params: `update_vbat`, `fix_pwm_polarity` (dangerous — flips PWM polarity for boards where BIOS misconfigured it), `force_id` (force a specific chip ID for debugging), `ignore_resource_conflict` (needed when ACPI claims the same I/O region).

**Winbond / early Nuvoton** — driver: `w83627ehf` (legacy; superseded by nct6775 for NCT6775F/NCT6776F). Still supports W83627EHF/EHG/DHG/DHG-P/UHG, W83667HG/HG-B. `w83627hf`, `w83781d`, `w83791d`, `w83792d`, `w83793`, `w83795`, `w83l785ts`, `w83l786ng` — older Winbond chips; mostly pre-2010 hardware.

**Fintek** — drivers: `f71805f`, `f71882fg`. F71805 was common on older Dell boards; F71882/83/89 on various industrial/embedded boards.

**SMSC** — drivers: `smsc47b397`, `smsc47m192`, `smsc47m1`, `sch5627`, `sch5636` (Fujitsu SCH5627/36 Theseus series — used on Fujitsu servers).

**National Semiconductor** — `pc87360`, `pc87427`.

**VIA** — `via686a`, `vt1211`.

**Fujitsu** — `ftsteutates` (FTS Teutates chip on Fujitsu D3xxx boards).

### 4.2 I²C / SMBus sensor chips (embedded, industrial, some desktops)

**National/TI LM** chips: `lm63`, `lm70`, `lm73`, `lm75`, `lm77`, `lm78`, `lm80`, `lm83`, `lm85`, `lm87`, `lm90` (many clones), `lm92`, `lm93`, `lm95234`, `lm95245`. lm85 is notable — multi-point auto-fan-curve chip, used on some server boards.

**Analog Devices (ADI/ADT/ADM)**: `adm1025`, `adm1026`, `adm1031`, `adm1177`, `adm1266`, `adm1275`, `adm9240`, `adp1050`, `ads7828`, `adt7410`, `adt7411`, `adt7462`, `adt7470`, `adt7475` (the classic "smart fan" multi-zone controller with hardware TACH-to-RPM table math), `amc6821`. ADT7475 and ADM1031 support full hardware auto-fan curves with spin-up logic.

**Maxim**: `max127`, `max1619`, `max1668`, `max197`, `max31722`, `max31730`, `max31760`, `max31785`, `max31790` (8-channel fan controller — common on servers), `max31827`, `max34440`, `max6620`, `max6639`, `max6645`, `max6650`, `max6697`, `max77705`.

**Microchip**: `emc1403`, `emc2103`, `emc2305`, `emc6w201`, `mcp3021`, `tc654` (2-channel fan tach/control), `tc74`.

**NXP/ON**: `g760a`, `g762` (closed-loop fan controller with target-RPM mode).

**Sensirion** (environmental): `aht10`, `sht15`, `sht21`, `sht3x`, `sht4x`, `shtc1`, `hih6130`, `hs3001`, `htu31`, `chipcap2`.

**Texas Instruments**: `ina209`, `ina2xx`, `ina233`, `ina238`, `ina3221`, `tmp102`, `tmp103`, `tmp108`, `tmp401`, `tmp421`, `tmp464`, `tmp513`, `tps23861`, `tps25990`, `tps40422`, `tps53679`, `tps546d24`.

### 4.3 PMBus / power management (servers, PSUs)

Driver `pmbus` is a core + many subordinate drivers: `acbel-fsg032`, `adm1275`, `bel-pfe`, `bpa-rs600`, `crps`, `dps920ab`, `fsp-3y`, `ibm-cffps`, `inspur-ipsps1`, `ir35221`, `ir36021`, `ir38064`, `isl68137`, `lm25066`, `lt3074`, `lt7182s`, `ltc2978`, `ltc3815`, `ltc4282`, `ltc4286`, `max15301`, `max16064`, `max16065`, `max16601`, `max17616`, `max20730`, `max20751`, `max8688`, `mp2856`, `mp2869`, `mp2888`, `mp2891`, `mp2925`, `mp29502`, `mp2975`, `mp2993`, `mp5023`, `mp5920`, `mp5926`, `mp5990`, `mp9941`, `mp9945`, `mpq8785`, `pim4328`, `pli1209bc`, `pm6764tr`, `pxe1610`, `q54sj108a2`, `stpddc60`, `ucd9000`, `ucd9200`, `xdp710`, `xdpe12284`, `xdpe152c4`, `zl6100`. Useful for reading PSU temps and currents on server platforms.

### 4.4 SoC-integrated

ARM SoC: `aspeed-pwm-tacho` (AST2400/2500 BMCs), `aspeed-g6-pwm-tach` (AST2600), `npcm750-pwm-fan` (Nuvoton BMC), `sparx5-temp` (Microchip SparX-5), `sfctemp`, `sg2042-mcu`, `gxp-fan-ctrl` (HPE iLO 6 BMC), `gsc-hwmon` (Gateworks System Controller), `sl28cpld`, `bt1-pvt` (Baikal-T1 PVT), `lan966x`.

Apple Silicon: `macsmc-hwmon` (Asahi SMC hwmon).

Fan-only on-board controllers: `pwm-fan` (generic PWM + optional tach IRQ), `gpio-fan` (discrete steps via GPIOs), `mlxreg-fan` (Mellanox switch platform), `qnap-mcu-hwmon` (QNAP NAS MCU — directly relevant to ventd's NAS audience), `mc33xs2410_hwmon`.

### 4.5 Misc server/BMC

`ibmaem`, `ibm-cffps`, `ibmpowernv`, `occ-hwmon` (IBM POWER On-Chip Controller), `intel-m10-bmc-hwmon`, `xgene-hwmon`, `smpro-hwmon` (Ampere Altra), `menf21bmc`.

### 4.6 GPU integration

`peci-cputemp`, `peci-dimmtemp` — out-of-band Intel CPU/DIMM thermals over PECI.
AMD GPU: via the `amdgpu` DRM driver (not under `drivers/hwmon/` but registers hwmon class device). See §7.
NVIDIA nouveau: via DRM (§7).

### 4.7 Odd niche

`ntc_thermistor` (generic NTC thermistor via ADC), `raspberrypi-hwmon` (RPi undervoltage/throttle status from firmware), `surface_fan` (Surface Pro/Laptop), `cgbc-hwmon` (Congatec Board Controller), `dell-smm-hwmon`, `gpd-fan` (GPD handhelds), `thinkpad-acpi` (registered as fan hwmon from the thinkpad platform driver), `asus_rog_ryujin` (ASUS Ryujin AIO LCD), `gigabyte_waterforce`, `surface_fan`.

---

## 5. CPU thermal drivers

### 5.1 Intel — `coretemp`

Reads the Digital Thermal Sensor (DTS) inside every modern Intel CPU. Per-core (`tempN_input` with N = core_id + 2; labels `Core 0`, `Core 1`, ...) and per-package (`Physical id 0`) temperature. Resolution 1°C. Reported temperature is computed as `TjMax - register_value` — it's a delta. TjMax on Nehalem and later is read from MSR `IA32_TEMPERATURE_TARGET`; older CPUs use lookup tables / heuristics / `tjmax` module parameter.

Attributes: `tempN_input`, `tempN_max` (cooling trip — Core 2 only), `tempN_crit` (TjMax), `tempN_crit_alarm` (Out-of-Spec, sticky), `tempN_label`.

### 5.2 AMD — `k10temp`

PCI-config-space access. Covers Family 10h through 19h (Zen 3). For pre-Zen CPUs: one reading, `temp1_input`, labeled Tctl — a **relative** non-physical value that means "distance to needed-cooling point". AMD explicitly documents it is not a physical temperature. `temp1_max` reports the max value for the Tctl control scheme; `temp1_crit` and `temp1_crit_hyst` are the throttling thresholds if BIOS has enabled hardware temperature control.

On Zen (17h+) and later: Tctl (`temp1_input`) is the fan-control temperature (offset-compensated); Tdie (`temp2_input`) is the actual die temperature for CPUs that expose it. Zen/Zen2/Zen3 also report per-CCD temperatures as `temp{3..10}_input`, labeled `Tccd1..Tccd8` — critical for accurate fan response on high-core-count CPUs where individual CCDs spike independently.

Erratum 319 on older Socket F / AM2+ revisions: sensor returns inconsistent values. Driver refuses to load without `force=1`.

### 5.3 Out-of-band: `peci-cputemp`

PECI (Platform Environment Control Interface) is Intel's single-wire side-channel for BMC → CPU DTS access. Reported in the kernel from 5.18 onward. The driver reports DTS as a delta to TCC (Thermal Control Circuit) activation — for a CPU with 85°C TCC and 35°C current, PECI returns -50°C. The hwmon driver converts to `tempN_input` in millidegrees by calculating `tjmax + dts_margin`. Covers package and per-core DTS. Intel-recommended over direct DTS for fan control because PECI values are the averaged DTS over a ~256ms window — less spikey.

**Complementary:** `peci-dimmtemp` reports DIMM temps over PECI. Attributes become available only after the CPU's BIOS completes memory training.

---

## 6. Storage thermal (drivetemp, nvme)

### 6.1 `drivetemp` — SATA/SCSI disks

Mainline since 5.6. Uses the ATA SCT Command Transport feature to read current temperature and, if available, limits and historic min/max. Falls back to SMART attribute reads if SCT not supported. Depends on SCSI && ATA.

Attributes: `temp1_input` (always), `temp1_lcrit`, `temp1_min`, `temp1_max`, `temp1_crit`, `temp1_lowest`, `temp1_highest`. All in millidegrees.

**Critical gotcha:** reading the temperature on some drives (WD120EFAX is the documented case) resets the spin-down timer — the drive never sleeps. Workaround: poll at intervals larger than 2× the spin-down time. The same effect appears with `hdtemp` and `smartd`. For ventd, this means polling interval must be **configurable per-drive** or the daemon has to use the kernel's cached-within-interval reads carefully.

### 6.2 NVMe — `nvme` subsystem built-in

Mainline since 5.5 (`CONFIG_NVME_HWMON`). Exposes NVMe Composite temperature (labeled "Composite") and up to 8 additional per-sensor readings as `tempN_input` (`Sensor 1` … `Sensor 8`). WCTEMP (warning composite temperature) is reported as `temp1_max`; CCTEMP (critical) as `temp1_crit`. Newer kernels also export min/max thresholds for individual sensors.

**Critical gotcha — NVMe threshold writes can brick monitoring on some drives.** The `NVME_QUIRK_NO_TEMP_THRESH_CHANGE` was added because an Intel SSDPEKKW512G7 would latch a temperature warning on any threshold write (even writing -273°C would trigger it, and only a controller reset cleared it). ventd must **never write NVMe temperature thresholds** — read-only.

The "Composite" label is the primary NAND temperature; additional sensors are vendor-specific (controller, flash, etc.) with no standardization.

---

## 7. GPU fan control

### 7.1 AMD — `amdgpu`

Fan control sysfs lives at `/sys/class/drm/cardN/device/hwmon/hwmonM/`. Standard attributes: `pwm1`, `pwm1_enable`, `pwm1_min`, `pwm1_max`, `fan1_input`, `fan1_min`, `fan1_max`, `fan1_target`, `fan1_enable`. Temperature: `temp[1-3]_input` (temp1 edge, temp2 junction/hotspot, temp3 memory), with `_crit`, `_crit_hyst`, `_emergency` on SOC15+ dGPUs.

**Warning: never set `pwm1` and `fan1_target` simultaneously — the later one overrides the earlier.**

**RDNA3 (SMU13, Radeon RX 7000) is different.** No traditional `pwm1` manual control; firmware exposes a **`fan_curve`** interface at `/sys/class/drm/cardN/device/gpu_od/fan_ctrl/fan_curve`. Format: 5 anchor points, each "index temp_C pwm_percent". Write each point, then write `c` (commit) to activate or `r` (reset) to revert. Valid ranges on RX 7800 XT example: temp 25–100°C, pwm 15–100%. Linux 6.13 added `fan_zero_rpm` and `fan_zero_rpm_stop_temperature` for controlling the RDNA3 "zero RPM below threshold" feature. Linux 6.x+ also has `acoustic_limit_rpm_threshold`, `acoustic_target_rpm_threshold`, `fan_target_temperature`, `fan_minimum_pwm` for the automatic PMFW control mode.

**Known AMD quirks:**
- Kernel 5.11+ can boot with `pwm1==0` in auto mode with the fan inactive. Workaround: write 1 to `pwm1_enable`, write a nominal PWM (~81), then optionally write 2 back to `pwm1_enable` to return to auto — this nudges PMFW into spinning.
- Resetting `pwm1_enable` back to auto can fail silently; driver reload is sometimes required.
- RDNA3 default fan curve ships with all points at 0°C/0% on some cards — fans literally won't spin. You MUST write a curve.

### 7.2 NVIDIA — nouveau

When using the nouveau driver: fan control via the DRM card's hwmon. Attributes: `pwm1_enable` (NONE/MANUAL/AUTO), `pwm1`, `pwm1_min`, `pwm1_max`, `fan1_input`. Thermal trip attributes: `temp1_auto_point1_temp` (fan boost), `temp1_auto_point1_temp_hyst`, `temp1_max` (downclock), `temp1_max_hyst`, `temp1_crit`, `temp1_crit_hyst`, `temp1_emergency` (shutdown), `temp1_emergency_hyst`. Not all cards have a drivable fan — the pwm1 attributes only appear if the card's vbios declares one.

Proprietary nvidia-driver: does NOT expose hwmon. Fan control via NVML / nvidia-settings (requires X server for non-compute cards). `nvidia-smi -q -d TEMPERATURE` reports temperature. For headless control use libnvidia-ml (NVML) via a library wrapper; there is no sysfs-path option.

### 7.3 Surface, Intel GPUs

`surface_fan` handles Microsoft Surface fan. Intel iGPU thermal is usually covered by coretemp + thermal zones, not a separate hwmon fan.

---

## 8. Vendor-platform drivers

### 8.1 ASUS

**Historical `asus_wmi_sensors`** — early WMI method for older AMD (Crosshair VI/VII, Prime X399/X470, Strix X470/B450 etc.) boards. Identifies via DMI board-name matching with a DMI_EXACT_MATCH table. Uses WMI GUID `466747A0-70EC-11DE-8A39-0800200C9A66` and methods GET_VALUE (`RWEC`), GET_INFO (`PWEC`), etc.

**`asus_wmi_ec_sensors` (Linux 5.17+, deprecated 5.18+)** — extends coverage to X570/B550/TRX40 boards that don't expose sensor readings through WMI directly but via ACPI EC. WMI BREC method reads arbitrary EC registers. Deprecated since `asus_ec_sensors` does everything better.

**`asus_ec_sensors` (the preferred current driver, Linux 5.18+)** — reads EC registers directly, acquiring an ACPI mutex (typically `\_SB.PCI0.SBRG.EC0.MUT0` on most boards) to avoid races with board firmware. Board-specific register maps. Supported boards (as of 7.0): MAXIMUS VI HERO, PRIME X470-PRO, X570-PRO, X670E-PRO WIFI, Z270-A; Pro WS TRX50-SAGE WIFI (and A), Pro WS X570-ACE, Pro WS WRX90E-SAGE SE; ProArt X570/X670E/X870E-CREATOR WIFI, B550-CREATOR; ROG Crosshair VIII DARK HERO, HERO/HERO (WI-FI), FORMULA, IMPACT; Crosshair X670E HERO/GENE; Maximus X HERO, XI HERO/HERO (WI-FI), Z690 FORMULA; ROG Strix B550-E/I GAMING, B650E-I GAMING WIFI, B850-I GAMING WIFI, X470-I GAMING, X570-E/F/I GAMING (+ E WIFI II), X670E-E/I GAMING WIFI, X870-F/I GAMING WIFI, X870E-E/H GAMING WIFI7; Strix Z390-F, Z490-F, Z690-A GAMING WIFI D4, Z690-E GAMING WIFI, Z790-E GAMING WIFI II, Z790-I GAMING WIFI; ROG Zenith II Extreme/Alpha; TUF GAMING X670E PLUS / PLUS WIFI. Module param `mutex_path` overrides the ACPI mutex if ASUS changes it in a BIOS update; `:GLOBAL_LOCK` means use ACPI global lock.

Exposed readings: Chipset (PCH) temp, CPU package temp, motherboard temp, T_Sensor header reading, VRM temp, CPU_Opt fan RPM, VRM heatsink fan RPM, chipset fan RPM, water flow meter (RPM), water-in/out temps, CPU current, CPU core voltage. Read-only — no fan control via this driver; for PWM control on the same board you still need `nct6775`.

### 8.2 MSI / Gigabyte

No vendor-blessed mainline driver. `nct6775` covers most older boards. Newer MSI B550/X570 boards with NCT6687D-R → see §13 (nct6687d out-of-tree). Gigabyte AIOs (Aorus WATERFORCE) have their own USB-HID driver `gigabyte_waterforce`.

### 8.3 ASRock

Mix of `nct6775` (older), `nct6683` (X570 Phantom Gaming series and several Z270/Z370/B250 models — supported since Linux 5.12), and out-of-tree forks (see §13) for newer A620/B650 NCT6686D boards.

### 8.4 HP / Dell / Lenovo

- HP: `hp-wmi-sensors` for consumer desktops; server iLO over IPMI.
- Dell consumer: `dell-smm-hwmon` for SMM-accessible temps and fans on older laptops; iDRAC for servers (see §11).
- Lenovo: `thinkpad-acpi` for laptops (§10); server iDRAC-equivalent over IPMI.

### 8.5 Framework

Uses `cros_ec_hwmon`. Linux 7.0 adds fan target speed writing and per-sensor temperature threshold handling (patches by Thomas Weißschuh). Before then, Framework fan control required `ectool`/`fw-fanctrl` from userspace, which pokes the ChromeOS EC directly.

### 8.6 Other laptop ECs

`lenovo_legion_laptop` (out-of-tree), `asus-nb-wmi` (single-fan limit), `dell-smm-hwmon` (many Dells), `dell-wmi`, `ideapad-laptop`, `msi-ec` (out-of-tree).

---

## 9. AIO and USB fan controllers

### 9.1 Corsair

**`corsair-cpro` (mainline since 5.9).** USB-HID. Corsair Commander Pro and Obsidian 1000D. 6 fan connectors, 4 temp connectors, 2 RGB channels, SATA power voltage monitoring. Since the device is USB, hotswap works. `fanN_label` shows connector name and status, `fanN_input` RPM, `fanN_target` target RPM, `pwmN` 0–255, `tempN_input` C, `inN_label` voltage rail (12V/5V/3.3V).

**Limitation:** no hardware curves via this driver (that's done via Corsair's own firmware which the driver doesn't touch), and no RGB. liquidctl 1.9.0+ detects when `corsair-cpro` is bound and uses hwmon instead of direct HID access.

**`corsair-psu` (mainline).** PSU monitoring for HX750i/850i/1000i/1200i, RM650i/750i/850i/1000i, AXi series. Temps, voltages, currents, fan speed, efficiency.

**Commander Core / Core XT / ST** — NOT in mainline. liquidctl supports experimentally. ventd's spec-02 targets this via liquidctl-style userspace reverse-engineering.

**Hydro (all-in-one pump+fan AIOs) — H80i v2, H100i v2, H115i, newer H100i Platinum/Elite/RGB, Hydro Pro/Pro RGB** — liquidctl territory, not in-kernel.

### 9.2 NZXT

**`nzxt-kraken2` (liquidtux or mainline).** Kraken X42/X52/X62/X72.

**`nzxt-kraken3` (mainline).** Kraken X53/X63/X73, Z53/Z63/Z73, Kraken 2023/2023 Elite/Standard. Reports liquid temp, pump speed, fan speed. Cooler must be initialized (via liquidctl once per power-on) to expose data.

**`nzxt-smart2` (mainline).** NZXT Smart Device V2, RGB & Fan Controller, Grid+ V3.

**`nzxt-grid3` (liquidtux out-of-tree).** Grid+ V3, Smart Device (V1).

### 9.3 Aquacomputer

**`aquacomputer-d5next` (mainline).** Covers D5 Next pump, Farbwerk 360, Octo, Quadro, High Flow Next, Aquaero (some versions), Leakshield. Exposes many sensors — temps, fan speeds, pressures, flows.

### 9.4 Other AIOs

**`asus_rog_ryujin` (mainline).** ASUS ROG Ryujin LCD AIOs — temps and fan/pump speeds.

**`gigabyte_waterforce` (mainline).** Gigabyte Aorus WATERFORCE series.

### 9.5 liquidctl and liquidtux

**`liquidctl`** — userspace CLI/Python driver library for AIOs and related USB devices (LEDs, RAM, PSUs). Superset of liquidtux coverage. Python-based, supports massive device range. ventd's spec-02 essentially parallels liquidctl for the specific Corsair Commander Core family Phoenix targets, but in pure Go with hidraw directly.

**`liquidtux`** — out-of-tree kernel DKMS modules (nzxt-grid3, nzxt-kraken2, nzxt-kraken3, nzxt-smart2) that pre-date mainline; exposes everything as standard hwmon sysfs.

---

## 10. Laptop EC paths

### 10.1 ThinkPad — `thinkpad-acpi`

Exposes a hwmon device named `thinkpad`. `pwm1_enable`: 0 = PWM offline / full-speed mode, 1 = manual PWM (use `pwm1` 0–255), 2 = hardware EC auto mode, 3 = reserved. Modes 0 and 2 are not supported on all ThinkPads; driver returns EINVAL when unsupported. `pwm1` is scaled from the firmware's 0–7 level range to 0–255 — level 7 is ~255, level 0 is 0.

**Fan safety watchdog.** Unique to ThinkPads. The EC will revert to automatic fan control if userspace doesn't write `pwm1_enable` (or `pwm1` while in manual mode) within a configurable timeout (up to 120s). Implemented via the `/proc/acpi/ibm/fan` watchdog command. ventd in manual mode on a ThinkPad must periodically re-arm by writing.

**Disengaged / full-speed.** The chip's "disengaged" mode (= `full-speed`) disables the EC's RPM closed-loop — fan ramps to maximum unregulated speed. Wears bearings fast. Use only for emergency cooling. Procfs accepts `enable`/`disable`/`level 0..7|auto|full-speed|disengaged`/`watchdog N`. Driver must be loaded with `options thinkpad_acpi fan_control=1` (default-off because it's risky).

Dual-fan ThinkPads: only one fan is supported by the in-tree driver.

### 10.2 Dell — `dell-smm-hwmon`

Uses Dell's SMM BIOS interface. Legacy SMM: trigger via writing to ioports 0xb2 and 0x84. Newer Dell: SMM calls over ACPI WMI — auto-detected, preferred on non-legacy systems. Temps and fan RPMs via `/sys/class/hwmon/hwmonN/name = dell_smm`. Each `pwmN` controls fan N.

Dell BIOS automatic fan control can override manual settings every few seconds on many machines. There exist optional SMM commands to enable/disable automatic control (0x31a3/0x35a3/0x30a3/0x34a3) but they're disabled by default because they cause severe side-effects on many systems. Some laptops (Inspiron 3505, Precision 490, Vostro 1720, etc.) have a "magic" 4th fan state that signals the BIOS to do auto for that fan specifically.

### 10.3 ChromeOS / Framework — `cros_ec_hwmon`

Temps via the ChromeOS EC. Covers Chromebooks, Framework Laptop 13/16 (hx20-based EC). Expanded in kernel 7.0 for fan target speed + per-temp threshold. Manual mode via `ectool --interface=fwk fanduty N` / `autofanctrl`. Framework EC is open source (FrameworkComputer/EmbeddedController on GitHub).

### 10.4 Notebook FanControl Linux (NBFC)

Userspace daemon (`nbfc-linux/nbfc-linux`) ported from the cross-platform NBFC. Uses `ec_sys` module or `/dev/port` to read/write EC registers directly. Configured via JSON per-laptop-model files (many community-contributed). Unlike NBFC-on-Windows which polls at `EcPollIntervall`, NBFC-Linux sets fan speed once per threshold crossing. Works alongside thinkpad-acpi/dell-smm where they're inadequate. This is the model ventd should follow if Phoenix ever adds laptop support post-v1.0: per-model config files, never assume a generic EC layout.

---

## 11. Server paths (IPMI, PECI)

### 11.1 IPMI — Dell iDRAC / HP iLO / Supermicro

IPMI is vendor-specific at the fan-control layer. Reading temps/fans is standard via `ipmitool sdr list`, but fan **control** requires raw commands unique to each vendor.

**Dell iDRAC raw commands** (iDRAC 7/8, and iDRAC 9 up to firmware 3.30.30.30):
- Enable manual fan control: `ipmitool raw 0x30 0x30 0x01 0x00`
- Restore auto: `ipmitool raw 0x30 0x30 0x01 0x01`
- Set fan speed (all fans) to X%: `ipmitool raw 0x30 0x30 0x02 0xff 0xNN` where `0xNN` is hex percent (e.g. `0x2D` = 45%).

Newer iDRAC 9 (>3.30.30.30) disables these raw commands — Dell blocked them. Workarounds involve third-party PCIe cards that force auto mode up (disable via another raw command `0xCE 0xEC`). Phoenix's ventd spec-01 sidecar handles this for the v0.3.x IPMI polish.

**HP iLO** — different raw commands, less commonly fan-controllable from Linux.

**Supermicro** — `ipmitool raw 0x30 0x45 0x01 0x00..03` sets fan mode: Standard, Full, Optimal, PUE2. Individual fan duty via `ipmitool raw 0x30 0x70 0x66 0x01 0xNN 0xYY` where NN is zone, YY is percent. Persists across reboots.

**In-band vs out-of-band.** Local Linux → `/dev/ipmi0` (requires `ipmi_devintf` + `ipmi_si` modules and ventd needs CAP_SYS_RAWIO + DeviceAllow=/dev/ipmi0 rw + dedicated user — exactly what Phoenix's sidecar model does). Remote → `ipmitool -I lanplus -H <ip> -U <user> -P <pass>`.

**Warning.** IPMI fan control commands are not atomic; daemon crashes after `raw 0x30 0x30 0x01 0x00` leave fans at the last manual setting indefinitely. A watchdog/supervision pattern is required.

### 11.2 PECI

Already covered in §5.3. Note: on BMC-equipped server platforms, PECI is the BMC's CPU thermal channel; on homelab desktop platforms it's usually accessed via the Super-I/O PCH bridge. The `peci-cputemp` and `peci-dimmtemp` drivers specifically target BMC-side OOB access.

---

## 12. ARM SoC / pwm-fan / thermal zones

### 12.1 `pwm-fan` (generic)

Driver: `pwm-fan`. Sits on top of the kernel PWM framework, so any PWM provider (SoC PWM block, PCA9685, etc.) can drive a fan. Exposes standard pwm/fan hwmon attributes. Optional tach: since 4.x, an interrupt input + counting timer produces `fan1_input` RPM readings with `pulses-per-revolution` in DT (default 2). Since late 2024 the driver supports `fan-stop-to-start-percent` and `fan-stop-to-start-us` DT properties to handle fans that need a kick-start pulse above running-duty to spin up from rest (Delta AFC0612DB-F00 is the documented case — needs 30% PWM to start from stop, can run slower after).

### 12.2 Thermal framework & cooling-maps

`/sys/class/thermal/thermal_zoneN/` holds the thermal-zone device. Attributes: `type`, `temp`, `mode` (enabled/disabled), `policy` (currently selected governor: `step_wise`, `bang_bang`, `user_space`, `power_allocator`), `available_policies`, `trip_point_N_temp`, `trip_point_N_hyst`, `trip_point_N_type` (active/passive/critical/hot), `cdevN_trip_point` (which trip this cooling device binds to), `cdevN_weight`, `emul_temp` (WO — fake temperature for testing).

Cooling devices at `/sys/class/thermal/cooling_deviceN/`: `type`, `max_state`, `cur_state`.

**Governor quirk.** `step_wise` (default) ignores hysteresis when evaluating trip points but uses it for cooling-level decisions. `bang_bang` is specifically for on/off cooling devices (uses hysteresis to prevent oscillation). RPi PoE HAT fan and the RPi 5 fan originally shipped with `step_wise` making the fan appear to ignore hysteresis — switching to `bang_bang` fixes single-level fans.

### 12.3 Raspberry Pi 5 example

Relevant because many NAS users run RPi-based builds. Device tree declares `pwm-fan` with `cooling-levels = <0 75 125 175 250>` (5 levels, 0–255). Trips: `cpu-tepid` 50°C, `cpu-warm` 60°C, `cpu-hot` 67.5°C, `cpu-vhot` 75°C, each with 5°C hysteresis, all type `active`. `cooling-maps` bind trips to (fan, min_state, max_state) tuples: e.g. `cpu_tepid` → `<&fan 1 1>` means at 50°C use cooling level 1 (PWM 75/255).

A common userspace observation: hwmon exposes pwm-fan's `pwm1` and `fan1_input` but writing to `pwm1` while the thermal framework still owns the fan doesn't work reliably. Userspace fan control on thermal-zone-managed boards needs to disable the thermal zone's governor or override via `cur_state` on the cooling device.

### 12.4 gpio-fan

Discrete speeds via GPIOs. Useful for older NAS hardware with voltage-switched fans. Configured entirely via DT `gpio-fan-speeds` levels.

---

## 13. Out-of-tree drivers ventd must know about

### 13.1 `it87` — frankcrawford fork

GitHub: `frankcrawford/it87` (the canonical active fork). a1wong fork also exists. Covers many ITE chips the mainline driver doesn't know about yet — key additions: IT8613E, IT8665E (force_id 0x8665), IT8686E, IT8688E, IT8689E, IT8790E, IT8792E revisions, IT8792E-2, IT87952E support, and newer entries as they appear. Exposes extra features like 16-bit tach counters on more chips, more PWM channels.

**Install pattern:** DKMS from AUR (`it87-dkms-git`) or equivalent; `echo 'options it87 ignore_resource_conflict=1 force_id=0x8665' > /etc/modprobe.d/it87.conf`. Requires `acpi_enforce_resources=lax` kernel param on some systems OR the per-module `ignore_resource_conflict=1` (safer, narrower scope).

**ACPI conflict.** ITE chips sit at LPC I/O addresses that ACPI often also claims. Default kernel policy refuses to load the driver to prevent races. `ignore_resource_conflict=1` skips this check — but if ACPI genuinely touches the chip concurrently, you can get random reboots. Common on Intel NUCs.

### 13.2 `nct6687d` — Fred78290

GitHub: `Fred78290/nct6687d`. Reverse-engineered from LibreHardwareMonitor's Windows code. Covers NCT6687-R on MSI MAG B550/B650, some ASUS, Intel H410M/H510M/Z590, MSI X870 (with alternative register map via `msi_alt1`). Not in mainline because Nuvoton refused to release datasheets — the code is reverse-engineered and limited. Module params: `manual=1` (allow custom sensors.conf voltage mappings), `msi_fan_brute_force=1` (BETA — writes PWM to all 7 fan curve control points; helps MSI boards where standard PWM writes don't stick). Blacklist `nct6683` first to prevent it from claiming the device.

### 13.3 `nct6686d` — s25g5d4

GitHub: `s25g5d4/nct6686d`. Fork of Fred78290's work targeting newer ASRock A620/B650 boards (A620I Lighting WiFi specifically). Same reverse-engineered basis.

### 13.4 `asrock-nct6683` — branchmispredictor

GitHub: `branchmispredictor/asrock-nct6683`. Patches the stock `nct6683` driver to enable PWM writing on ASRock boards where the mainline version is intentionally read-only. Module param `pwm_set_delay` adds microsecond delay after PWM writes — workaround for tools like fan2go that read back immediately and see stale values. ASRock B550 Taichi Razer Edition is the documented target.

### 13.5 `asus-wmi-sensors` — electrified

GitHub: `electrified/asus-wmi-sensors`. Predecessor to the mainline `asus_wmi_sensors`. Still useful for some boards not yet covered by mainline `asus_ec_sensors`. Explicitly NOT for X570/B550/TRX40 — those need the mainline EC driver.

### 13.6 `asus-wmi-ec-sensors` — zeule

GitHub: `zeule/asus-wmi-ec-sensors`. Bridge driver that was eventually upstreamed. Still maintained for board-specific additions ahead of mainline merges.

### 13.7 `liquidtux`

GitHub: `liquidctl/liquidtux`. DKMS hwmon kernel modules for NZXT gear (see §9.2). Most are now in mainline but the out-of-tree version has newer device support.

### 13.8 `nbfc-linux`

GitHub: `nbfc-linux/nbfc-linux`. Laptop EC-based fan control daemon. C port of the C# original; doesn't require Mono.

### 13.9 `zenpower3`

GitHub: `Ta180m/zenpower3`. Alternative to `k10temp` for AMD Zen that exposes voltage, current, and power per core (not just temp). Sometimes conflicts with `k10temp`; must unload one first. Used by power/clocking tools.

### 13.10 Others worth knowing

- `lenovo_legion_laptop` — Lenovo Legion gaming laptops EC fan curve control.
- `msi-ec` — MSI laptop EC readings.
- `dell-bios-fan-control-git` — toggle off BIOS-side override on some Dells.
- `asusctl`/`asusd` — ASUS ROG laptops.
- `amdfand` / `amdgpu-fan` — AMD GPU fan curve daemons (Python).
- `CoolerControl` — GPLv3 GUI/daemon; aggregates hwmon + liquidctl + amdgpu + NVIDIA.
- `GreenWithEnvy` (GWE) — NVIDIA-focused fan/clock GUI.
- `LACT` — AMD GPU overclocking/fan-curve daemon (Rust).
- `fw-fanctrl` — Framework Laptop fan control (Python + ectool).
- `hhfc` — handheld-focused hwmon fan controller.

---

## 14. Userspace prior art

Ordered by relevance to ventd's design choices.

### 14.1 `fan2go` — Go, most relevant comparison

GitHub: `markusressel/fan2go`. Go, hwmon-based, standalone daemon, YAML config. Probably ventd's closest analog. `fan2go detect` enumerates hwmon devices and prints fans/sensors per platform (e.g. `nct6798-isa-0290`, `amdgpu-pci-0031`). Config references fans by `platform: <regex>` + `rpmChannel: N` + `pwmChannel: N` (defaults to same number as RPM channel). Curves can be linear or set-of-points; sensors can be hwmon, block-device (SATA/NVMe), or file-based. Supports `neverStop: true` (guarantees non-zero). Key quote from its docs: "The hwmon fan index is based on device enumeration and is not stable for a given fan if hardware configuration changes. The Linux kernel hwmon channel is a better identifier for configuration as it is largely based on the fan headers in use."

**Design lesson for ventd:** bind configurations to (platform_name, channel_index), not (hwmon_index, channel). Channel indices are mostly fixed for a given board+chip combination across reboots; hwmon index is not.

### 14.2 `CoolerControl` — C++/Rust, most featured

GitLab-primary (GitHub mirror at `codifryed/coolercontrol`). System daemon + web UI + optional desktop app + REST API. Auto-detects hwmon/sysfs, liquidctl, NVIDIA, AMD GPU. Packages for Arch, Debian/Ubuntu, Fedora, openSUSE, Nix, Gentoo, Unraid, Docker, AppImage. GPL v3+. Biggest competitor feature-wise; also biggest dependency tree. ventd explicitly avoids this complexity.

### 14.3 `fancontrol` + `pwmconfig` (lm-sensors)

The OG. Bash script (`pwmconfig`) that tests each PWM by stopping fans sequentially and watching which `fanN_input` drops; generates `/etc/fancontrol` config. `fancontrol` daemon then reads the config and polls. Config format:
```
INTERVAL=10
DEVPATH=hwmon2=devices/platform/nct6775.2576
DEVNAME=hwmon2=nct6775
FCTEMPS=hwmon2/pwm1=hwmon0/temp1_input
FCFANS=hwmon2/pwm1=hwmon2/fan1_input
MINTEMP=hwmon2/pwm1=30
MAXTEMP=hwmon2/pwm1=65
MINSTART=hwmon2/pwm1=150
MINSTOP=hwmon2/pwm1=0
```

**pwmconfig behavior and gotchas:**
- Stops each fan for ~5s to correlate PWM channels to RPM readings.
- Generates `MINSTART` (minimum PWM to spin up from stop) and `MINSTOP` (minimum PWM while running).
- Hangs indefinitely on certain code paths when `fanN_input` returns empty string (Debian bug #1029160).
- Requires manual `fanN_div` tuning for older chips with legacy fan divisors.
- Uses `DEVPATH=` + `DEVNAME=` pair to survive hwmon renumbering — but only if the physical device path is stable, which it often isn't (see §15).
- `INTERVAL=10` is seconds between polling cycles — too slow for fast-transient workloads, too fast for power-saving.

`fancontrol` is the baseline ventd aims to replace for the average user. Its limitations are ventd's feature list.

### 14.4 `thinkfan` — lightweight, laptop-focused

GitHub: `vmatare/thinkfan`. C++. Yaml or legacy config. Supports hwmon, LM sensors libsensors, libatasmart (direct SMART reads), NVML (NVIDIA), and thinkpad_acpi `tp_fan` driver. Example config:
```yaml
sensors:
  - hwmon: /sys/class/hwmon
    name: coretemp
    indices: [1, 2, 3, 4, 5]
fans:
  - tp_fan: /proc/acpi/ibm/fan
levels:
  - [0, 0, 55]
  - [1, 48, 60]
  - [2, 50, 61]
  - [7, 56, 70]
  - ["level auto", 60, 75]
  - ["level full-speed", 68, 32767]
```
Notable: respects `disengaged`/`full-speed` mode for ThinkPads; supports `tp_fan` sysfs level-0-to-7 in addition to standard pwm. Actively warns that userspace fan control on laptops can fry the machine.

### 14.5 `liquidctl` (reference, not Linux fan-control proper)

GitHub: `liquidctl/liquidctl`. Python CLI for AIOs, RGB controllers, PSUs, Vengeance RGB DIMMs. Supports dozens of devices across Corsair, NZXT, EVGA, MSI, ASUS, Aquacomputer. Example usage:
```
liquidctl list
liquidctl initialize all
liquidctl status
liquidctl --match kraken set fan speed 20 30 30 50 34 80 40 90 50 100
liquidctl --match kraken set pump speed 70
```
When a kernel driver is bound (v1.9.0+), liquidctl uses hwmon for reads. ventd's spec-02 work parallels liquidctl's approach for Corsair Commander Core but in Go.

### 14.6 `fancon` — C++, curves

GitHub: `hbriese/fancon`. Config is protobuf-text. Notable for explicit temp-averaging/smoothing intervals, rpm-stickiness intervals (prevent rapid decrease), temp-averaging intervals. Supports temp→RPM curves with explicit RPM→PWM calibration.

### 14.7 `afancontrol`

GitHub: `KostyaEsmukov/afancontrol`. Python. INI config. Supports arbitrary command-based sensors (e.g. `nvme smart-log ... | grep temperature`). Per-fan PID options. Temperature filters (EMA smoothing).

### 14.8 `fand`

GitHub: `doitsujin/fand`. LISP-like config with named sensors/fans and composable expressions: `(maximum (panic "45" "50" (sensor-input "cputemp")) (smooth "10" (steps ...)))`. Very small codebase.

### 14.9 `hhfc`

GitHub: `Samsagax/hhfc`. Python, YAML. Handheld-focused (Steam Deck-likes) but works as a generic hwmon-only userspace fan daemon. Scale/offset per-sensor: `divisor: 1000`, `offset: 20` (AMD Tctl offset).

### 14.10 `nv_fan_control`

GitHub: `sammcj/nv_fan_control`. Go. Reads nvidia-smi temperature, writes PWM directly to a motherboard fan via hwmon sysfs. Interesting because it's Go — same language as ventd. Minimal codebase (~200 lines) is a decent reference for polling+write patterns.

### 14.11 `amdgpu-fancontrol`, `amdfand`

GitHub: `grmat/amdgpu-fancontrol` (bash), `amdfand` (Rust). AMD-specific fan curve daemons. `grmat` script pattern: `FILE_PWM=$(echo /sys/class/drm/card0/device/hwmon/hwmon?/pwm1)` with globbing; 3-point minimum fan curve with hysteresis. Useful reference for handling the amdgpu auto-to-manual transition quirks.

### 14.12 `nbfc-linux`

Already covered in §10.4.

---

## 15. Device naming and persistence

This is where userspace fan-control tools go to die. Summary of the problem:

### 15.1 `hwmonN` is non-persistent

`/sys/class/hwmon/hwmonN` indexing is **not stable across reboots, module reloads, or hardware changes**. Verbatim from Jean Delvare (kernel hwmon maintainer, on lm-sensors mailing list):

> "The kernel makes no guarantee about device name stability. Some subsystems (most notably the sound subsystem) have tried it but it simply doesn't work. For most subsystems this is now handled by udev, unfortunately hwmon devices have no node in /dev so udev is of no use in our case. The persistent device naming is thus achieved by libsensors. Applications which care about stable device names (or getting everything right for that matter) should use libsensors."

### 15.2 What *is* stable

| Identifier | Stability |
|---|---|
| `/sys/class/hwmon/hwmonN/name` | Stable for a given chip/driver combo |
| Full physical path under `/sys/devices/` | Mostly stable (depends on bus ordering) |
| Channel index within a chip (fan1, fan2, pwm1) | Stable per-chip; mapped to specific pins/headers on the board |
| DMI board name (`/sys/class/dmi/id/board_name`) | Stable |
| DMI board vendor | Stable |
| USB device serial number | Stable per-device (hotswap changes nothing logically) |
| PCI BDF | Usually stable unless hardware is reseated into different slot |

### 15.3 Enumeration pattern ventd should use

```
for each dir in /sys/class/hwmon/hwmon*:
    read dir/name                    # stable chip/driver name
    determine bus type via readlink  # ../../devices/{pci, platform, usb, ...}
    for each attribute matching pwm[0-9]+$, fan[0-9]+_input, temp[0-9]+_input:
        channel = integer suffix
        key = (dir/name, bus_type + optional_addr, channel)
```

This gives ventd a stable (name, channel) tuple that survives reboots. Example stable keys:
- `nct6798-isa-0290` / `fan2` / `pwm2`
- `k10temp-pci-00c3` / `temp1`
- `nvme-pci-0100` / `temp1`
- `amdgpu-pci-0300` / `pwm1` / `fan1`

This matches the fan2go and coolercontrol identification scheme, and the sensors-3.0.0 libsensors chip naming convention.

### 15.4 Why pwmconfig's `DEVPATH=` approach fails

Because the physical device path contains the LPC/ISA probe address (`.2576` in `nct6775.2576`) which doesn't change, but the `hwmonN` symlink target number does, AND the order in which sensor modules load affects the `hwmonN` assignment. Forcing module load order via `/etc/modules-load.d/sensors.conf` fixes this *if* the board has a stable set of sensors. Adding a USB AIO or losing a drive can shift the indices.

### 15.5 udev rules don't help directly

You can write udev rules matching on `DRIVERS==` or `ATTR{name}==` — but you can't use `NAME=` on hwmon devices (brain0 on IRC, circa 2011: "you cannot use NAME= to change names"). What you *can* do: create a symlink under `/dev/` via a udev rule that shells out to find the matching hwmonN path:
```
KERNEL=="hwmon*", ATTR{name}=="nct6798", SYMLINK+="hwmon_cpu"
```
But wait — hwmon devices have no `/dev/` node by default. You'd have to create one with `DEVPATH` magic. Practically: just enumerate in userspace every time and match by `name`.

### 15.6 DMI-based board identification

For vendor-platform drivers (asus_ec_sensors style) and per-board quirks, ventd can read:
- `/sys/class/dmi/id/sys_vendor` ("ASUSTeK COMPUTER INC.", "Dell Inc.", ...)
- `/sys/class/dmi/id/product_name` (laptop model or OEM PC model)
- `/sys/class/dmi/id/board_vendor` ("ASUSTeK COMPUTER INC.")
- `/sys/class/dmi/id/board_name` ("ROG STRIX X570-E GAMING WIFI II")

The kernel helper `dmi_name_in_vendors(str)` is what kernel drivers use. Phoenix's spec-01 DMI gate is exactly this pattern. Whitespace and bracketed content can vary across BIOS revisions — case-insensitive substring match is safer than exact match (see `abituguru3` history).

---

## 16. Fan mechanics: tach, pulses, spin-up, stop-start

### 16.1 Pulses per revolution

Standard 4-pin PC fans: **2 pulses per revolution**. Most server / datacenter fans also 2. Some industrial/automotive blowers use 1, 3, or 4.

**Calculation:** `RPM = pulses_per_second * 60 / pulses_per_rev`. Most hwmon drivers do this internally. Some chips (ADT7475, ADT7476A, MAX6615, LM64) measure PERIOD rather than count — 90 kHz clock counting between pulses, stored as 16-bit. Formula: `RPM = (freq * 60) / TACH_count` where `freq=90000` for 2ppr, `f/2` for 1ppr, `f*2/3` for 3ppr.

The sysfs attribute `fanN_pulses` exposes this when the chip is configurable. Writing it tells the chip how to interpret tach counts. For manually-tuned configurations (lab fans, servers with odd fan types), this is critical.

### 16.2 Spin-up behavior

Fans can't start reliably from stop with low PWM. Typical spin-up requirements:
- Consumer 4-pin PC fans: need ~30–40% PWM to reliably start, can then be reduced to ~15–20% while running.
- Server delta fans: usually 100% kick for a fraction of a second, then can drop to specified RPM.
- Pumps in AIOs: similar; often have a manufacturer-specified minimum.

**Chip-level spin-up:** ADT7475, AMC6821, MAX6645 have dedicated spin-up logic — take 33% → 100% → back to setpoint in a ramp. `pwmN_start` on nct6775 is the PWM used to start the fan when temperature crosses into range.

**Kernel-level (pwm-fan):** since the Nov 2024 patch by Marek Vasut, DT properties `fan-stop-to-start-percent` and `fan-stop-to-start-us` let the driver kick from 30% (or whatever) for 100ms (or whatever) before dropping to target, if target is below spin-up.

**Userspace-level:** pwmconfig determines MINSTART by starting at `pwm=0`, incrementing, until RPM > threshold. ventd's calibrator should do the same but also capture MINSTOP (spin to stop without restarting) and a hysteresis band between.

### 16.3 Zero-RPM / fan stop modes

Modern fans supporting zero-RPM: AMD RDNA3 (firmware-controlled), NZXT Kraken X3/Z3, Corsair iCUE AIOs, most Noctua/BeQuiet/Arctic PWM fans below a cutoff PWM.

Behavior: at PWM < some threshold (typically 10–20%), fan stops rotating but stays electrically connected. The tach reads 0. Next time PWM goes above spin-up-threshold, fan re-starts — but you lose tracking during the stop interval, and if you're wrong about the spin-up PWM, the fan fails to restart and temperatures climb.

**ventd must track "was this fan stopped by us?" vs "has this fan failed?".** Distinguish:
- `pwmN=0` + `fanN_input=0` + `fanN_fault=0` → intentional stop
- `pwmN>start_threshold` + `fanN_input=0` + polls with no increase → probable failure
- `fanN_fault=1` → driver says failed (some chips detect stall)

### 16.4 DC (voltage) vs PWM fans

Classic 3-pin (molex/KK) fans: no PWM wire, speed controlled by supply voltage. Motherboard fan control for these uses `pwmN_mode=0` (DC mode) — the chip pulses its own output at high freq, which a capacitor on the header smooths back to a DC voltage level. Not all chips support DC mode (nct6775 does, it87 does, many newer superio don't).

Minimum start voltage: 3-pin fans typically need ~5-6V to start, 4V to sustain. Below that the motor stalls. Some AIO pumps (Corsair LL/QL style) are 4-wire PWM and can't use DC mode — forcing DC on those blows through the cap.

### 16.5 Suspend/resume PWM loss

The PWM framework in kernel doesn't guarantee that PWM state is preserved across suspend. `pwm-fan` has a patch from 2014 (Kamil Debski) that manually re-applies pwm_config on resume, and a 2018 patch (Thierry Reding) that sets fan=0 on suspend to force the state to be re-applied on resume.

Consequence: ventd must handle suspend/resume signals (systemd `PrepareForSleep` on DBus) and re-write all PWM/pwm_enable values after resume, or the fans may come back in a stale state (usually auto mode, which may be wrong for user's curve).

---

## 17. Gotcha catalogue

Concrete failure modes ventd must handle or surface clearly.

### 17.1 hwmon index renumbering

Covered in §15.1. Fix: enumerate by name, never hardcode hwmonN.

### 17.2 `pwm_enable` reverts to automatic on its own

Some drivers/BIOSes reset `pwmN_enable` to automatic under various conditions:
- BIOS fan control via ACPI re-writes registers on interval (Dell laptops, some server boards).
- Suspend/resume (all chips that don't save state).
- Kernel thermal governor kicks in due to a trip point crossing.
- Driver-detected fault (nct6775 does this on some failures).

**Fix for ventd:** verify `pwmN_enable` on every write cycle; re-assert if it changed. Log an event the first time per boot. Phoenix's existing issue #594 (pwm_enable reassertion, v0.4.0) is exactly this.

### 17.3 BIOS reclaims fan control

Independent of pwm_enable — some BIOSes (MSI on certain B550 boards, ASRock Rack) poll the fan control register directly via SMI and overwrite whatever userspace sets. Symptom: you write `pwm=100`, immediately read back `100`, 2 seconds later read back `180` (BIOS auto mode value). No driver fix possible; workaround is to disable fan control in BIOS *and* in any motherboard-vendor ACPI quirks. MSI `nct6687d` driver has `msi_fan_brute_force` mode that writes PWM to all 7 fan-curve points to trick the BIOS.

### 17.4 ASUS "CPUTIN floats" bug

On various ASUS boards (X570, X470, B450, etc.) using nct6775: the CPUTIN sensor on the nct chip is not actually connected, so it reports garbage or inversely-correlated values (PECI-style — lower real temp = higher reading). Use tempN where the label is `CPU` or `PECI` instead. `sensors-detect` and sensors.conf label conventions help but ventd should not expose floating sensors to users.

### 17.5 NVMe threshold write bricks monitoring (Intel SSDPEKKW512G7)

Covered in §6.2. ventd must not write NVMe temp thresholds.

### 17.6 WD120EFAX spin-down timer

Covered in §6.1. Per-drive poll interval.

### 17.7 ACPI / driver conflict on it87-supported chips

Covered in §13.1. Use `ignore_resource_conflict=1` per-module; avoid global `acpi_enforce_resources=lax`.

### 17.8 Fan tach reads 0 on DC-mode or no-tach fan

A 3-wire fan in DC mode can produce tach pulses; a 2-wire fan never will. If `fanN_input` is persistently 0 while `pwmN>0`, ventd must distinguish "no tach line connected" from "fan stopped". Use `fanN_fault` if available; otherwise rely on user config saying "this fan has no tach".

### 17.9 BIOS-controlled fan appears as "read-only PWM"

`pwmconfig` output shows `Manual control mode not supported, skipping` when BIOS has locked the chip into auto mode. Workaround: in BIOS, set each fan to "Manual / Disabled / N/A" (wording varies). On many boards you must disable every fan silent-mode feature in BIOS.

### 17.10 nct6683 intentionally read-only on most boards

Mainline `nct6683` only allows writes on whitelisted vendor strings. ASRock got added in 5.12 for a subset of boards. If your board's nct6683 name shows up and nothing responds to pwm writes, that's why — move to out-of-tree `asrock-nct6683` or similar.

### 17.11 AMD RDNA3 fan curve must be initialized

Default fan curve on many RDNA3 cards is all zeros — fan never spins. See §7.1. Write a curve at boot.

### 17.12 ThinkPad watchdog expires

Covered in §10.1. Periodic re-assert required in manual mode.

### 17.13 Dell BIOS re-writes fan speed

Dell laptops (Inspiron, Latitude, Vostro) have BIOS polling that overrides userspace every few seconds. Workarounds are fragile (SMM commands that may disable auto globally, with side-effects). ventd on Dell laptops should probably warn and operate in monitoring-only mode unless user explicitly enables experimental mode.

### 17.14 Libsensors ignores USB hwmon devices (historical)

Newer libsensors (4.x) handles USB-bound hwmon. Older versions silently skip them. ventd writing its own enumeration sidesteps this entirely.

### 17.15 Writes interpreted as 0 on invalid input

If you write "auto" to `pwmN_enable` (a string, not a number), the kernel parses it as 0 (which is full-speed!). Always stringify integers. Same for writing "manual" or any non-numeric value.

### 17.16 Alarm flags are latching on some chips, self-clearing on others

nct6775 alarms latch until register read; it87 alarms clear after any register read. ventd should read alarms only on transition events, not poll. Track state changes.

### 17.17 `pwm[1-*]_auto_point` indexing varies

Some chips 0-based, some 1-based. Some support 4 points, some 5, some 7. ventd reading existing chip curves needs to probe rather than assume.

### 17.18 Driver says PWM works but fan actually on BIOS fan-header

On boards with split fan-header topology (CPU_FAN goes to one chip, SYS_FAN to another), `pwm1` on one hwmon may control SYS_FAN and nothing controls CPU_FAN from userspace. This is usually documented in board manuals. pwmconfig correlation testing catches this — ventd's calibration phase should too.

### 17.19 `fan2go detect` and `lm_sensors` fan labels

Chip-reported labels are hints at best. `CPUFAN`, `SYSFAN`, `AUXFAN`, `CHA_FAN` — not standardized across vendors. Board manufacturer often redirects them. ventd should prompt for user labeling during calibration.

### 17.20 Module load order affects hwmon ordering

Debian and Arch both have `/etc/modules-load.d/*.conf` for enforcing module load order. But systemd parallel module loading can defeat this. The only true fix is enumeration by name at runtime.

### 17.21 Thermal zone governor ignores hysteresis with `step_wise`

Covered in §12.2. RPi 5 fan-always-on bug was this. If ventd registers cooling-maps via DT, `bang_bang` governor may be needed instead.

### 17.22 `pwm_enable=1` + `pwm=0` does different things per chip

On nct6775: `pwm=0` means actually 0 duty (fan stops if supported). On some other chips: `pwm=0` means fallback to minimum. ABI says 0 = lowest speed, 255 = full, but "lowest" isn't defined as either off or some configurable minimum. Always calibrate — don't assume.

### 17.23 amdgpu boot-time auto stuck

Covered in §7.1. pwm=0 at boot with auto mode. Kick via manual→auto dance.

### 17.24 NVMe Composite vs Sensor 1 vs Sensor 2 disagreement

`Composite` is the reported NAND temperature; `Sensor 1/2/3/...` are vendor-specific (controller, additional NAND, etc.). Sensor indices aren't standardized. Sample Corsair MP500 report: Sensor 1 (NAND) tracks `Composite`, Sensor 2 (controller) differs by 40°C and can get "stuck". ventd should default to reading `Composite` (`temp1_input`) for NVMe unless user specifically configures.

### 17.25 lock contention between kernel driver and liquidctl

If you `modprobe corsair-cpro` *and* run liquidctl against the same Commander Pro, HID reports interleave and both agents misread/misset. liquidctl 1.9.0+ auto-detects the kernel driver and uses sysfs instead of HID. Older liquidctl = remove from autoload with `blacklist corsair-cpro` if you want liquidctl to own the device.

---

## 18. Go ecosystem for hwmon

### 18.1 Generic sysfs readers

- **`prometheus/procfs`** (github.com/prometheus/procfs/sysfs) — extensive sysfs parser. Used by node_exporter. Has `ReadUintFromFile`-style helpers, parses `/sys/class/{net,power_supply,...}` but NOT specifically hwmon. Good reference for patterns.
- **`periph.io/x/host/v3/sysfs`** — hardware-access library. Has generic `ThermalSensors` enumerator that covers both thermal and hwmon temp sensors. Doesn't cover fan/pwm control. Worth stealing the temperature enumeration pattern.
- **`gopackage/sysfs`** — simple helper for sysfs read/write with test doubles (`sysfstest`). Good as a testing-pattern reference.
- **`ungerik/go-sysfs`** — Debian/Ubuntu package `golang-github-ungerik-go-sysfs-dev`. Sysfs helpers.

### 18.2 No existing Go hwmon library

There is no established, maintained Go library that models hwmon specifically (equivalent to Rust's `libmedium`). Closest is fan2go's internal enumeration code. ventd's approach — writing its own — is the pragmatic choice.

### 18.3 Relevant existing patterns

- `fan2go`'s `internal/hwmon` package — enumeration and detection.
- `nv_fan_control` — minimal reader/writer pattern (~200 lines).
- `periph.io`'s `sysfs.ThermalSensors` — temperature enumeration.

### 18.4 HID for AIOs (spec-02 territory)

- **Pure-Go hidraw:** Phoenix already built this in `internal/hal/usbbase/hidraw/`. GPL-3.0 constraint met. CGO_ENABLED=0 maintained.
- **`go-hid`** uses cgo → disqualified for ventd.
- **`karalabe/hid`** also cgo.
- **`sstallion/go-hid`** cgo.

### 18.5 NVML (NVIDIA) from Go

- **`NVIDIA/go-nvml`** — official NVIDIA binding. Requires CGO because NVML is a C library. For ventd's NVIDIA temp reading: use `purego dlopen` of `libnvidia-ml.so.1` directly (same pattern Phoenix used for NVML in the existing codebase).

### 18.6 DMI/SMBIOS

- `/sys/class/dmi/id/*` — just read the files. No library needed.
- `go-smbios/smbios` — parses raw SMBIOS if you need detailed DMI types. Probably overkill for ventd.

---

## 19. Recommendations for ventd

Not a plan, not a spec. Distilled from the research above. Phoenix already has many of these encoded in existing rules.

### 19.1 Enumeration (already covered in spec-01)

- Walk `/sys/class/hwmon/hwmon*`, read `name` from each.
- Classify by bus: `readlink device` → contains `/pci/`, `/platform/`, `/usb/`, `/i2c-*`, `/drm/`.
- For each chip: scan attribute names, regex for `pwm[0-9]+$`, `fan[0-9]+_input`, `temp[0-9]+_input`.
- Key all config by `(name, channel_index)`, not `(hwmonN, channel_index)`.

### 19.2 Detection layer — what ventd identifies at install

1. **Super-I/O chip** — parse dmesg / modprobe output, or probe `/sys/class/hwmon/*/name` after `modprobe nct6775` and `modprobe it87`.
2. **DMI board** — `cat /sys/class/dmi/id/{sys_vendor,board_vendor,board_name}`.
3. **CPU** — coretemp or k10temp presence.
4. **Storage** — scan `/sys/class/nvme/*/device/hwmon/hwmon*/` for NVMe and `/sys/block/sd*/device/hwmon/hwmon*/` for drivetemp.
5. **GPU** — `/sys/class/drm/card*/device/hwmon/hwmon*/`.
6. **AIOs** — probe USB for known vendor+product IDs (spec-02).
7. **IPMI** — `/dev/ipmi0` existence + `ipmitool mc info` (spec-01).

### 19.3 Per-chip caveats ventd should hard-code

- **nct6775**: Smart Fan IV mode 5 for curve mode; `pwmN_start` to ensure spin-up; `pwm1_freq` if PWM at low duty doesn't respond.
- **it87**: always `ignore_resource_conflict=1` in modprobe config; only manual mode (`pwm_enable=1`) works reliably.
- **nct6683**: read-only on most boards; suggest out-of-tree `asrock-nct6683` or `nct6687d` if MSI/newer ASRock detected.
- **k10temp**: for Zen/Zen2/Zen3 CPUs, use Tdie (temp2) not Tctl (temp1) for display; use highest CCD temp (`Tccd{1..8}`) for fan control.
- **coretemp**: use the package temp (Physical id 0) for fan control, not per-core max.
- **amdgpu**: for pre-RDNA3, use `pwm1_enable=1`+`pwm1=X`; for RDNA3+, write `fan_curve` + `c`.
- **nvme**: read `Composite` only; never write thresholds.
- **drivetemp**: per-drive poll interval; watch for WD120EFAX-style drives.
- **thinkpad-acpi**: re-assert pwm1_enable every 60s when in manual mode (watchdog timeout is 120s).
- **dell-smm-hwmon**: monitoring-only by default on laptops.
- **ipmi**: privilege-separated sidecar (already spec-01).

### 19.4 Calibration phase

- For each (pwm, fan) pair: step pwm from 255→0 in chunks, record RPM, identify:
  - `pwm_max_stable_rpm` — RPM at pwm=255 after fan stabilizes 10s.
  - `pwm_min_running` — lowest pwm where RPM > 0 and stable.
  - `pwm_min_start` — lowest pwm where fan reliably restarts from stop.
  - `fan_stops_at_zero` — boolean; does the fan reach 0 RPM at pwm=0?
- For each temp sensor: record baseline idle temp; note which are CPU/GPU/drive-correlated.
- Offer user label mapping ("Fan 2 on nct6798 is my front case intake").
- Store results under `/var/lib/ventd/calibration/<dmi_hash>.toml`.

### 19.5 Curve engine

- Temperature source: one or more (sensor, weight) pairs, with per-source min/max clamping.
- Interpolation: linear between points, with spin-up logic when transitioning from stop.
- Hysteresis: separate ramp-up and ramp-down curves, or one curve with a dead-band.
- Smoothing: EMA with configurable tau (1-30s typical). Temperature smoothing prevents "fan flapping" on short spikes.
- Stickiness: minimum time at a PWM level before decreasing (fancon-style).

### 19.6 Safety

- **Kernel thermal framework crit trips** ALWAYS take precedence; ventd should never hold a PWM below what the kernel's thermal governor demands.
- **Per-fan min PWM** hard floor below which ventd will not go (protects zero-RPM-incapable fans from stalling).
- **Max-on-panic**: if any sensor is `NaN`, unreadable, or exceeds user-specified critical, force all fans to `pwm_enable=0` (full speed per ABI).
- **Graceful shutdown**: on SIGTERM/SIGINT, write `pwm_enable=2` (or whatever the pre-ventd value was) and exit. On SIGKILL, the kernel's thermal zone takes over.
- **Watchdog on self**: if ventd hangs, systemd `WatchdogSec=` restarts it. For extra paranoia, on restart, detect whether PWM values look sane vs. stale.

### 19.7 Per-driver persistence

After reboot, every `pwmN` and `pwmN_enable` goes back to whatever the BIOS/driver defaults. ventd must re-write every cycle (already implied by the polling daemon design). Don't rely on writing once at boot.

### 19.8 What NOT to do (avoiding Cowork-style rabbit holes)

- Don't reinvent libsensors. Read sysfs directly; it's simpler and more robust than libsensors' parsing.
- Don't parse `/etc/sensors.conf`. Let advanced users write ventd-native config; give `fan2go detect`-style output to help them.
- Don't try to auto-correlate PWM→fan via stop-and-watch at install time. Ask user during calibration, then verify. fan2go and pwmconfig do this and it's fragile on boards with multi-fan daisy-chain headers.
- Don't try to become CoolerControl. ventd is zero-terminal, zero-config, invisible. No web UI, no REST API in v1.0.
- Don't integrate liquidctl as a Python subprocess. Do HID yourself in Go (already on the roadmap).

---

## Appendix A: Documentation & source pointers

**Kernel tree locations (upstream master):**
- `Documentation/hwmon/` — per-driver docs.
- `Documentation/hwmon/sysfs-interface.rst` — sysfs ABI (the canonical reference).
- `Documentation/hwmon/hwmon-kernel-api.rst` — kernel-internal driver API.
- `Documentation/ABI/testing/sysfs-class-hwmon` — ABI testing documentation.
- `Documentation/driver-api/thermal/sysfs-api.rst` — thermal framework.
- `drivers/hwmon/` — driver source.
- `drivers/thermal/` — thermal subsystem source.
- `drivers/nvme/host/hwmon.c` — NVMe hwmon integration.
- `drivers/gpu/drm/amd/amdgpu/amdgpu_pm.c` — amdgpu fan/thermal sysfs.
- `drivers/platform/x86/thinkpad_acpi.c` — ThinkPad fan sysfs.

**Kernel documentation URLs (kernel.org):**
- `https://docs.kernel.org/hwmon/index.html` — driver index.
- `https://docs.kernel.org/hwmon/sysfs-interface.html` — ABI.
- `https://docs.kernel.org/driver-api/thermal/sysfs-api.html` — thermal framework.
- `https://dri.freedesktop.org/docs/drm/gpu/amdgpu/thermal.html` — amdgpu thermal.

**Mailing lists:**
- `linux-hwmon@vger.kernel.org` — kernel hwmon development.
- `lm-sensors@lm-sensors.org` — libsensors userspace.
- `amd-gfx@lists.freedesktop.org` — amdgpu.

**Key maintainers:**
- **Guenter Roeck** — hwmon subsystem maintainer. Most authoritative answers about ABI.
- **Jean Delvare** — historical maintainer, sensors-detect.
- **Eugene Shalygin** — ASUS EC sensors driver author.
- **Marius Zachmann** — Corsair cpro driver author.
- **Jonas Malaco** — NZXT drivers (liquidtux, nzxt-kraken*/smart*).
- **Akinobu Mita** — NVMe hwmon.

## Appendix B: Key GitHub repos cited

| Repo | Purpose |
|---|---|
| `torvalds/linux` | Mainline reference |
| `frankcrawford/it87` | Active it87 out-of-tree fork |
| `a1wong/it87` | Alternative it87 fork |
| `Fred78290/nct6687d` | NCT6687-R (MSI B550 etc.) |
| `s25g5d4/nct6686d` | NCT6686D (newer ASRock AMD) |
| `hzyitc/nct6687d-debian` | Debian-packaged nct6687d |
| `branchmispredictor/asrock-nct6683` | ASRock nct6683 PWM-write patch |
| `electrified/asus-wmi-sensors` | Legacy ASUS WMI |
| `zeule/asus-wmi-ec-sensors` | Pre-upstream ASUS EC |
| `liquidctl/liquidctl` | Python AIO driver library |
| `liquidctl/liquidtux` | Out-of-tree NZXT hwmon drivers |
| `markusressel/fan2go` | Go hwmon fan daemon (closest analog) |
| `codifryed/coolercontrol` | Featured competitor |
| `vmatare/thinkfan` | ThinkPad-focused daemon |
| `KostyaEsmukov/afancontrol` | Python daemon, command sensors |
| `hbriese/fancon` | C++ daemon, smoothing |
| `doitsujin/fand` | C daemon, LISP config |
| `Samsagax/hhfc` | Python handheld daemon |
| `sammcj/nv_fan_control` | Go NVIDIA→mobo fan controller |
| `grmat/amdgpu-fancontrol` | AMD GPU bash daemon |
| `nbfc-linux/nbfc-linux` | Laptop EC daemon (C port) |
| `TamtamHero/fw-fanctrl` | Framework Laptop daemon |
| `FrameworkComputer/EmbeddedController` | Framework EC firmware source |
| `tigerblue77/Dell_iDRAC_fan_controller_Docker` | Dell IPMI control reference |
| `chenxiaolong/ipmi-fan-control` | Supermicro IPMI (archived) |
| `MisterZ42/corsair-cpro` | Commander Pro (predates mainline) |
| `JackDoan/corsair-cpro` | Commander Pro fork |
| `maclarsson/cfancontrol` | Commander Pro Python GUI |
| `Ta180m/zenpower3` | AMD Zen power/voltage/current |
| `ilya-zlobintsev/LACT` | AMD GPU overclock daemon |
| `herbingk/pwm-gpio-fan` | RPi GPIO PWM DT overlay |
| `prometheus/procfs` | Go sysfs library (no hwmon specifics) |
| `periph.io/x/host/v3/sysfs` | Go periph (thermal enumeration) |
| `gopackage/sysfs` | Go sysfs helper with test doubles |
| `NVIDIA/go-nvml` | NVIDIA Management Library for Go |

End of reference.
