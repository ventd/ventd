# 2026-04 Board Catalog Citations — scope-C append

This section appends to the existing scope-A and scope-B citations
docs. Per-field source-of-truth for each scope-C YAML entry.

---

## Schema v1.1 amendment (`spec-03-amendment-schema-v1.1.md`)

### §SCHEMA-BIOSVER

| Field | Source |
|---|---|
| BIOS_VERSION 4-char prefix dispatch pattern | github.com/johnfanv2/LenovoLegionLinux/blob/main/kernel_module/legion-laptop.c — optimistic_allowlist DMI table |
| GKCN/EUCN/H1CN/M3CN/LPCN/N0CN family enumeration | legionfancontrol.com — public BIOS prefix → model family table |
| DMI_BIOS_VERSION:M3CN31WW evidence | github.com/johnfanv2/LenovoLegionLinux/issues/76 — Slim 5 16APH8 dmesg |
| DMI_BIOS_VERSION:LPCN45WW evidence | github.com/johnfanv2/LenovoLegionLinux/issues/234 — Pro 7 16ARX8H dmesg |
| Glob match semantics (existing v1 pattern) | spec-03 PR 1 schema implementation |

### §SCHEMA-DT

| Field | Source |
|---|---|
| /proc/device-tree/compatible null-separated list semantics | docs.kernel.org/devicetree/usage-model.html — kernel devicetree binding doc |
| /proc/device-tree/model string format | github.com/raspberrypi/firmware boot/bcm2712-rpi-5-b.dtb |
| Pi 5 compatible string "raspberrypi,5-model-b" | scope-B citations doc (already filed) |
| Pi 4B compatible string "raspberrypi,4-model-b" | github.com/raspberrypi/linux arch/arm/boot/dts/broadcom/bcm2711-rpi-4-b.dts |
| CM4 compatible string "raspberrypi,4-compute-module" | github.com/raspberrypi/linux arch/arm/boot/dts/broadcom/bcm2711-rpi-cm4.dtsi |
| DMI-first / DT-fallback dispatch precedence | scope-B Pi 5 entry hack documented this implicitly; v1.1 codifies |

### §HP-CONSUMER

| Field | Source |
|---|---|
| hp-wmi-sensors business-class scope | docs.kernel.org/hwmon/hp-wmi-sensors.html — explicit "(and some HP Compaq) BUSINESS-CLASS computers" wording |
| hp-wmi consumer driver scope (no fan API) | github.com/torvalds/linux drivers/platform/x86/hp-wmi.c — hotkey/rfkill driver, no fan ABI |
| Acer Aspire kernel ABI gap | confirmed absence in linux/drivers/platform/x86/acer-wmi.c — acer-wmi exists but exposes no fan_get/set ioctl |
| Surface fan driver scope | docs.kernel.org/hwmon/sysfs-interface.html — surface_fan covers Surface Pro 7+ specifically |

---

## `legion_hwmon.yaml` driver entry

| Field | Source |
|---|---|
| Out-of-tree DKMS kernel module | github.com/johnfanv2/LenovoLegionLinux README — DKMS install workflow |
| GPL-2.0-only license | github.com/johnfanv2/LenovoLegionLinux/blob/main/LICENSE |
| 10-point fancurve via /sys/kernel/debug/legion/fancurve | github.com/johnfanv2/LenovoLegionLinux README — debugfs interface section |
| powermode + platform_profile API | github.com/johnfanv2/LenovoLegionLinux/issues/257 — IdeaPad Gaming 3 dmesg shows both interfaces |
| EC chip ID variance (0x8227, 0x5507, 0x5508) | github.com/johnfanv2/LenovoLegionLinux/issues/71 — Pro 5 R9000P 2023 EC 0x5507 mismatch dmesg + issues/234 + issues/76 |
| force=1 modparam fallback to GKCN config | github.com/johnfanv2/LenovoLegionLinux/issues/76 — explicit "Using configuration for system: GKCN" force=1 dmesg |
| Mainline upstreaming in progress | mailing list — patches not landed as of kernel 6.13 (verified by absence in mainline drivers/platform/x86/) |
| Conflicts: PlasmaVantage, CinnamonVantage | github.com/johnfanv2/LenovoLegionLinux README — alternative GUI list |

---

## `ipmi_bmc.yaml` driver entry

| Field | Source |
|---|---|
| Mainline since kernel 2.6 | docs.kernel.org/driver-api/ipmi.html |
| ipmi_si + ipmi_devintf module split | docs.kernel.org/driver-api/ipmi.html — module organization section |
| /dev/ipmi0 device path | docs.kernel.org/driver-api/ipmi.html — userspace interface section |
| Supermicro raw 0x30 0x45 0x01 set-mode | b3n.org/supermicro-fan-speed-script/ — direct command reference |
| Supermicro raw 0x30 0x70 0x66 zone PWM | mikesbytes.org/server/2019/03/01/ipmi-fan-control.html — zone control + 0x30 0x91 0x5A HDD bay variant |
| Dell raw 0x30 0x30 0x01/0x02 enable-manual + set-pwm | virtualbytes.io/dell-poweredge-ipmi-fan-control/ — full Dell raw command reference |
| Dell iDRAC9 3.34+ blocked workflow | dell.com/community thread "Dell ENG is taking away fan speed control" — Dell employee response confirms deliberate removal |
| Dell PE 14G/15G in supported list | dell.com/support kb 000257346 — Dell explicitly lists R740/R640/R750 etc with thermal-offset workflow |
| HPE iLO 5/6/7 fan-control NOT supported | github.com/alex3025/ilo-fans-controller README — explicit unsupported list |
| HPE iLO 5 IPMI 2.0 standard-only emulation | manualslib.com/manual/1436981/Hp-Hpe-Ilo-5.html?page=302 — iLO 5 user manual section on IPMI |
| iLO 5 thermal config web UI options | support.hpe.com docDisplay a00105236en_us — HPE iLO 5 user guide cooling config page |

---

## Legion gaming laptop entries (`lenovo-legion.yaml`)

### lenovo-legion-5-15arh05h

| Field | Source |
|---|---|
| Product Name 82B1 | github.com/johnfanv2/LenovoLegionLinux/issues/110 — direct dmidecode |
| BIOS Version FSCN family | linux-hardware.org probe 7244a526b1 — FSCN24WW; legionfancontrol.com — FSCN family classification |
| EC chip 0x8227 | inferred from same-generation Gen 5/6 evidence in johnfanv2 issues |
| Driver allowlist inclusion | johnfanv2 README — "Lenovo Legion 5 15ARH05H" listed as supported |

### lenovo-legion-5-15ach6h

| Field | Source |
|---|---|
| Product Name 82JU | github.com/johnfanv2/LenovoLegionLinux/issues/219 — direct dmidecode (15ACH6H 82JU GKCN50WW) |
| BIOS Version GKCN family | github.com/johnfanv2/LenovoLegionLinux/issues/386 — GKCN65WW dmidecode; johnfanv2 README |
| EC chip 0x8227 | github.com/johnfanv2/LenovoLegionLinux/issues/219 — implicit in successful load without ID warning |
| maximumfanspeed register warning | github.com/johnfanv2/LenovoLegionLinux/issues/386 — direct dmesg evidence |

### lenovo-legion-5-pro-16ach6h

| Field | Source |
|---|---|
| Product Name 82JQ | johnfanv2 README — explicit "Legion 5 Pro 16ACH6H (82JQ)" |
| BIOS Version GKCN | johnfanv2 README — "BIOS GKCN58WW" |
| Same EC + fancurve as 15ACH6H | inferred — same Gen 6 AMD GKCN family |

### lenovo-legion-7-16ithg6

| Field | Source |
|---|---|
| Product Name 82K6 | driverscollection.com listing — "Lenovo Legion 7-16ITHg6 Laptop (Type 82K6)" + Lenovo official support page url path |
| BIOS Version H1CN family | driverscollection.com — H1CN58WW BIOS file naming for 82K6; legionfancontrol.com — H1CN family is "Legion 5/5pro/7 Series Intel 2021" with explicit "Legion 7 16ITHg6" inclusion |
| EC chip 0x8227 | inferred from same Gen 6 generation pattern |

### lenovo-legion-slim-5-16aph8

| Field | Source |
|---|---|
| Product Name 82Y9 | github.com/johnfanv2/LenovoLegionLinux/issues/76 — direct dmidecode |
| BIOS Version M3CN family | issue #76 — DMI_BIOS_VERSION:M3CN31WW |
| force=1 modparam requirement | issue #76 — explicit "legion_laptop is forced to load" dmesg |
| GKCN fallback config | issue #76 — "Using configuration for system: GKCN" |
| EC chip 0x8227 | issue #76 — implicit (same as Gen 6 GKCN family pattern) |

### lenovo-legion-pro-7-16arx8h

| Field | Source |
|---|---|
| Product Name 82WS | github.com/johnfanv2/LenovoLegionLinux/issues/133 — direct dmidecode |
| BIOS Version LPCN family | issue #133 (LPCN45WW) + issue #234 (LPCN45WW with dmesg) |
| EC chip 0x5507 | issue #234 — direct "Read embedded controller ID 0x5507" dmesg |
| EC chip mismatch graceful handling | issue #234 — driver continues loading despite "Expected EC chip id 0x8227 but read 0x5507" |
| LPCN dedicated config (no force=1 needed) | issue #234 — "Using configuration for system: LPCN" |
| 82WS official Lenovo confirmation | psref.lenovo.com/Product/Legion/Legion_Pro_7_16ARX8H |

### lenovo-legion-pro-5-16irx9

| Field | Source |
|---|---|
| Product Name 83DF | github.com/johnfanv2/LenovoLegionLinux/issues/163 — direct dmidecode |
| BIOS Version N0CN family (NOT NMCN) | issue #163 dmidecode + bsd-hardware.info probe 3fa8964010 N0CN24WW + Lenovo support kb file naming N0CN24WW/N0CN29WW |
| force=1 modparam requirement | inferred from N0CN absence in legion_laptop allowlist (issue #163 tracks adding it) |

---

## Dell PowerEdge entries (`dell-poweredge.yaml`)

### dell-poweredge-r740

| Field | Source |
|---|---|
| sys_vendor "Dell Inc." | scope-A Dell citations doc — consistent across all Dell post-2010 |
| product_name "PowerEdge R740" | skywardtel.com — "Linux: Run sudo dmidecode -s system-product-name" → "Model Number: R740" |
| iDRAC9 BMC | dell.com KB 000177885 — R740 listed under iDRAC9 generation table |
| 14G generation classification | skywardtel.com — "R740 → Second digit = 4 → 14th Generation" |
| Raw 0x30 0x30 0x01/0x02 fan workflow | virtualbytes.io/dell-poweredge-ipmi-fan-control/ — full reference |
| 3.34 firmware fan-control block | dell.com community thread 647f8593f4ccf8a8de47aa9b — Dell employee confirms deliberate removal |
| iDRAC firmware downgrade KB | dell.com KB 000225924 — Dell-permitted downgrade path for 14G/15G |

### dell-poweredge-r640

| Field | Source |
|---|---|
| Same 14G platform as R740 | dell.com KB 000257346 — R640 explicitly listed alongside R740/R740xd in supported-models table |
| Same iDRAC9 + thermal pattern | dell.com KB 000177885 |
| 1U chassis louder by static-pressure design | engineering common knowledge; not citation-required |

### dell-poweredge-r740xd

| Field | Source |
|---|---|
| 14G xd variant | dell.com KB lists R740XD alongside R740 |
| Aggressive third-party PCIe thermal response | techmikeny.com/blogs/techtalk/how-to-lower-fan-speed-after-installing-third-party-card — comprehensive R740xd third-party-card workflow + 14G blockage warning |
| Raw 0x30 0xce 0x00 0x16 PCIe override (12G/13G only) | techmikeny.com — explicit deprecation note for 14G+ |

---

## HPE ProLiant entries (`hpe-proliant.yaml`)

### hpe-proliant-dl380-gen10

| Field | Source |
|---|---|
| sys_vendor "HPE" | inferred from HPE post-rebrand standard (vs older "HP") |
| product_name "ProLiant DL380 Gen10" | newserverlife.com user guide PDF naming + HPE official store buy.hpe.com 1010026818 |
| iLO 5 BMC | cloudninjas.com/collections/hpe-proliant-dl380-gen10-ilo-licensing — HPE Gen10 → iLO 5 |
| iLO 5 fan-control unsupported | github.com/alex3025/ilo-fans-controller README — explicit unsupported list |
| Thermal config web UI dropdown | support.hpe.com docDisplay a00105236en_us — iLO 5 cooling features doc |

### hpe-proliant-dl360-gen10

| Field | Source |
|---|---|
| 1U sibling of DL380 Gen10 | HPE Gen10 product line — DL360 1U / DL380 2U convention since Gen5 |
| Same iLO 5 BMC, same blockage | github.com/alex3025/ilo-fans-controller — Gen10 family enumeration |
| Coffee Lake + Cascade Lake support window | HPE QuickSpecs (DL360 Gen10) — Xeon Scalable Gen 1/2 confirmed |

---

## Supermicro additional entries (`supermicro-additional.yaml`)

### supermicro-x11sch-ln4f

| Field | Source |
|---|---|
| sys_vendor "Supermicro" | scope-B citations doc (already filed) |
| board_name "X11SCH-LN4F" | supermicro.com/en/products/motherboard/X11SCH-LN4F — official product page |
| AST2500 BMC | beachaudio.com listing — explicit "ASPEED AST2500 BMC" + Supermicro spec sheet |
| 4x I210 LAN (variant differentiator vs X11SCH-F) | beachaudio.com + supermicro.com product pages |
| NCT6776 Super-I/O | inferred — same Coffee Lake C246 platform as X11SCH-F (scope-B citation: spinics.net X9SRL-F NCT6776F detection) |
| DMI baseboard convention | thomas-krenn.com/en/wiki/Read_out_mainboard_name — Supermicro DMI baseboard reading example |

### supermicro-x12sth-f

| Field | Source |
|---|---|
| board_name "X12STH-F" | supermicro.com/en/products/motherboard/X12STH-F — official product page |
| LGA1200 Rocket Lake Xeon E-2300 platform | supermicro.com spec sheet |
| AST2600 BMC | supermicro.com — explicit BMC chip listing on product spec |
| NCT6796D Super-I/O | supermicro.com X12STH-F spec — "Super I/O Nuvoton NCT6796D" listed |

### supermicro-h13ssl-n

| Field | Source |
|---|---|
| board_name "H13SSL-N" | scope-B citations doc (referenced) |
| AST2600 BMC | supermicro.com H13SSL-N product spec |
| NCT7802 I2C-bus thermal | scope-B citations doc — sbexr.rabexc.org kernel hwmon Kconfig (already cited for H12SSL-i; H13 inherits) |
| BMC panic mode + RTX issue | experts-exchange.com 29295680 — direct H13SSL-N + RTX panic-mode evidence |
| Tach via IPMI sensors only on some revs | inferred from same experts-exchange thread + H12SSL-i scope-B note pattern |
| `ipmitool mc reset cold` recovery | experts-exchange.com same thread — direct workflow |

---

## Raspberry Pi additional entries (`raspberry-pi-additional.yaml`)

### raspberry-pi-4-model-b

| Field | Source |
|---|---|
| compatible "raspberrypi,4-model-b" | github.com/raspberrypi/linux arch/arm/boot/dts/broadcom/bcm2711-rpi-4-b.dts |
| Official Pi 4 fan = gpio-fan binary on/off | forums.raspberrypi.com/viewtopic.php?t=363655 — Raspberry Pi engineer confirms "no tacho output, no speed monitoring" for official fan |
| Aftermarket PWM via pwm-fan overlay | forums.raspberrypi.com/viewtopic.php?t=362312 — community pwm-fan overlay reference + viewtopic.php?t=354125 (RPi engineer-authored) |
| 4-level cooling map default 50/60/65/70°C | forums.raspberrypi.com/viewtopic.php?t=363655 — example dtparam fan_temp0/1/2/3 + duty 75/128/192/255 |
| GPIO 12/14/18 hardware PWM | waveshare.com/wiki/PI4-FAN-PWM — supported PWM pin enumeration + raspberrypi.com docs |
| Cooling-device must-detach pattern | scope-B Pi 5 citation (same dynamic) |

### raspberry-pi-cm4-on-cm4io-board

| Field | Source |
|---|---|
| compatible "raspberrypi,4-compute-module" | github.com/raspberrypi/linux arch/arm/boot/dts/broadcom/bcm2711-rpi-cm4.dtsi |
| Carrier-board-dependent fan path | raspberrypi.com/documentation/computers/compute-module.html — explicit "Fan connector. Fan connector supporting standard 12 V fans with PWM drive" on CM4IO product spec |
| EMC2301 chip + I2C bus 10 + addr 0x2f | jeffgeerling.com/blog/2021/controlling-pwm-fans-raspberry-pi-cm4-io-boards-emc2301/ — direct evidence + i2cdetect output |
| dtoverlay=cm4io-fan + dtparam=i2c_vc=on setup | github.com/neggles/cm4io-fan README + jeffgeerling.com workflow |
| Mainline emc2301 driver upstreamed ~2022 | jeffgeerling.com — "Recently, a driver for the EMC2301 fan controller was merged into Raspberry Pi's Linux fork" |
| 8 fan speed steps EMC2301 chip limit | github.com/neggles/cm4io-fan README — "EMC2301 gives you 8 fan speed steps" |
| Closed-loop RPM control on-chip | github.com/neggles/cm4io-fan + raspberrypi.com forum t=308787 |

---

## Framework backend memo (`2026-04-framework-backend-memo.md`)

| Field | Source |
|---|---|
| cros_ec_lpcs LPC bus shim role | github.com/torvalds/linux drivers/platform/chrome/cros_ec_lpcs.c |
| /dev/cros_ec chardev | github.com/torvalds/linux drivers/platform/chrome/cros_ec_chardev.c |
| EC_CMD_PWM_SET_FAN_DUTY = 0x0024 | chromium.googlesource.com platform/ec include/ec_commands.h |
| EC_CMD_PWM_GET_FAN_RPM_DUTY = 0x0025 | same source |
| Framework EC firmware fork | github.com/FrameworkComputer/EmbeddedController |
| ChromiumOS ectool reference impl | chromium.googlesource.com platform/ec util/ectool.cc |
| No mainline hwmon Framework fan path | absence in linux/drivers/platform/chrome/ as of kernel 6.13 — verified via repo grep |
| NBFC Linux reimplementation | github.com/nbfc-linux/nbfc-linux (referenced; spec-09 chat to confirm endorsement) |

---

## Cross-references to existing docs

- scope-B board-catalog citations: see `2026-04-board-catalog-citations-scope-b-append.md`.
- scope-A board-catalog citations: see `2026-04-board-catalog-citations.md`.
- driver amendments roadmap: see `2026-04-driver-amendments-needed.md`
  (this scope-C delivery completes §SCHEMA-BIOSVER, §SCHEMA-DT, §LEGION-1,
  §IPMI-1, and §HP-CONSUMER from that roadmap; §FW-1 deferred per
  Framework memo).
