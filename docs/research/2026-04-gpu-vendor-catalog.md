# 2026-04 GPU Vendor Catalog (spec-03b amendment)

**Status:** spec-03b amendment, schema-compatible with spec-03 amendment v1.0 frozen.
**Scope:** discrete GPU fan control via vendor-specific backends. Extends spec-03 PR 2 schema for GPU class hwmon entries.
**Out of scope:** integrated GPU fans (none exist as separate cooling), eGPU enclosure fans, mining-rig multi-card orchestration, AIB-vendor RGB/LED control.
**Schema additions:** zero. All GPU vendors map to existing v1.0 fields.

---

## 1. Framing

### 1.1 Why spec-03b, not spec-10

GPU fans expose through hwmon sysfs (AMD via `amdgpu`, Intel via `xe-hwmon`, NVIDIA via NVML which itself wraps an internal hwmon-equivalent). The schema integration surface is identical to motherboard chip fans — `capability`, `pwm_unit`, `pwm_enable_modes`, `off_behaviour`, `firmware_curve_offload_capable` all apply.

Backend distinction (NVML purego dlopen vs sysfs vs Intel xe-hwmon RO) is implementation in `internal/hal/gpu/{nvml,amdgpu,xe}.go`, not a spec axis.

### 1.2 What this adds to PR 2 catalog

GPU `name` values added to chip catalog tier-3 fallback (controllability map §1.2 hwmon `name` allowlist):

- `amdgpu` — AMD discrete GPUs, kernel `amdgpu` driver
- `nouveau` — open-source NVIDIA driver, RO sensor only
- `nvidia` — virtual entry, NVML backend (no hwmon `name` exposed via sysfs for proprietary driver)
- `xe` / `i915` — Intel Arc, RO sensor only as of kernel 6.12+
- `radeon` — legacy AMD pre-GCN, RO sensor only

PR 2c diagnostic bundle adds GPU detection (vendor, driver, kernel module, NVML availability, AMDGPU OverDrive bit state).

### 1.3 Predictive thermal (spec-05) implications

Per-GPU thermal mass differs by 3+ orders of magnitude vs CPU (laptop iGPU ~5g effective vs desktop 4090 ~1.5kg with vapor chamber). spec-05 trace harness must capture GPU traces separately — see spec-05-prep §3 GPU stress engines. This catalog locks the read paths the harness uses.

### 1.4 Vendor licensing reality check

| Vendor | Userspace tool | License | ventd interaction |
|---|---|---|---|
| NVIDIA | `nvidia-settings`, NVML | Proprietary lib, MIT bindings | purego dlopen, our own thin wrapper |
| AMD | LACT, CoreCtrl, amdgpu-fan | MIT/GPL | sysfs writes only |
| Intel | none official on Linux | n/a | sysfs reads only |

NVIDIA/go-nvml uses CGO (`bindings.go` is a cgo bridge). **Cannot use directly under CGO_ENABLED=0.** ventd needs ~30 fan-related symbols only — write a thin purego wrapper, not a full reimplementation. Per `internal/hal/gpu/nvml/symbols.go` skeleton in §2.4 below.

---

## 2. NVIDIA backend

### 2.1 Hardware/driver matrix

| Architecture | Generation | NVML write support | Header symbol introduced | Notes |
|---|---|---|---|---|
| Maxwell GM10x/GM20x | GTX 750 / 980 | yes | driver R515+ for `_v2` | first arch with `nvmlDeviceSetFanControlPolicy` |
| Pascal GP10x | GTX 1050–1080 Ti, Titan Xp | yes | R515+ | |
| Volta GV100 | Titan V, V100 | yes (datacenter, often RO via permission) | R515+ | |
| Turing TU10x | RTX 2060–2080 Ti, GTX 16xx | yes | R515+ | |
| Ampere GA10x | RTX 3050–3090 Ti, A100 | yes | R515+ | |
| Ada AD10x | RTX 4060–4090 | yes | R515+ | |
| Blackwell GB20x | RTX 50xx | yes | R515+ baseline, GB200 needs newer | |
| Tegra/Jetson | embedded | no | n/a | passive cooling, no NVML fan API |

NVML fan-control symbols added in driver R515 (May 2022) and R520 (Oct 2022). Pre-R515 cards on legacy drivers (R470 LTS) are read-only. Fan policy API (`nvmlDeviceSetFanControlPolicy`) added in R520.

### 2.2 Symbol surface ventd actually needs

Read-side (always available R450+):

```
nvmlInit_v2
nvmlShutdown
nvmlSystemGetDriverVersion
nvmlSystemGetNVMLVersion
nvmlDeviceGetCount_v2
nvmlDeviceGetHandleByIndex_v2
nvmlDeviceGetHandleByUUID
nvmlDeviceGetUUID
nvmlDeviceGetName
nvmlDeviceGetPciInfo_v3
nvmlDeviceGetTemperature
nvmlDeviceGetTemperatureThreshold
nvmlDeviceGetNumFans
nvmlDeviceGetFanSpeed_v2
nvmlDeviceGetMinMaxFanSpeed
nvmlDeviceGetFanControlPolicy_v2  (R520+)
nvmlDeviceGetThermalSettings       (R515+)
nvmlErrorString
```

Write-side (gated behind capability probe):

```
nvmlDeviceSetFanSpeed_v2          (R515+)
nvmlDeviceSetDefaultFanSpeed_v2   (R515+, broken per forum, see §2.5)
nvmlDeviceSetFanControlPolicy     (R520+)
```

Total ~17–20 symbols. Tractable to maintain a hand-written purego wrapper without c-for-go generation pipeline.

### 2.3 Schema mapping (v1.0 fields)

```yaml
- name: nvidia
  driver: nvidia (proprietary, NVML backend)
  capability: rw_full           # R520+, downgrade to ro_sensor_only on R515-/R470
  pwm_unit: percentage_0_100    # NVML uses 0-100 not 0-255
  pwm_unit_max: 100
  pwm_enable_modes:
    - manual                    # nvmlFanControlPolicy_TEMPERATURE_DISCRETE → manual
    - auto                      # nvmlFanControlPolicy_TEMPERATURE_CONTINOUS_SW → vBIOS curve
  off_behaviour: bios_dependent # cannot write 0; some cards have zero-RPM mode
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - nvidia-settings (X11/Coolbits)
    - GreenWithEnvy
    - any other NVML writer
  fan_control_capable: true
  fan_control_via: nvml
  polling_latency_ms_hint: 50   # NVML calls are fast, <50ms typical
  required_modprobe_args: null
  pwm_polarity_reservation: null
  exit_behaviour: bios_dependent  # nvmlDeviceSetDefaultFanSpeed_v2 broken per forum, restore via policy=auto
  runtime_conflict_detection_supported: true  # detect via NVML_ERROR_NO_PERMISSION on probe
  firmware_curve_offload_capable: false  # NVML cannot upload custom curves; vBIOS curve only as auto mode
```

### 2.4 purego wrapper skeleton

```go
// internal/hal/gpu/nvml/loader.go
package nvml

import (
    "fmt"
    "github.com/ebitengine/purego"
)

const libname = "libnvidia-ml.so.1"

type Lib struct {
    handle uintptr

    // function pointers - declare per symbol used
    Init                      func() Return
    Shutdown                  func() Return
    DeviceGetCount            func(*uint32) Return
    DeviceGetHandleByIndex    func(uint32, *Device) Return
    DeviceGetFanSpeed_v2      func(Device, uint32, *uint32) Return
    DeviceSetFanSpeed_v2      func(Device, uint32, uint32) Return
    DeviceSetFanControlPolicy func(Device, uint32, FanControlPolicy) Return
    DeviceGetNumFans          func(Device, *uint32) Return
    // ... ~17 more
}

func Open() (*Lib, error) {
    h, err := purego.Dlopen(libname, purego.RTLD_NOW|purego.RTLD_GLOBAL)
    if err != nil {
        return nil, fmt.Errorf("nvml dlopen: %w", err)
    }
    l := &Lib{handle: h}
    purego.RegisterLibFunc(&l.Init, h, "nvmlInit_v2")
    purego.RegisterLibFunc(&l.Shutdown, h, "nvmlShutdown")
    purego.RegisterLibFunc(&l.DeviceGetCount, h, "nvmlDeviceGetCount_v2")
    purego.RegisterLibFunc(&l.DeviceGetHandleByIndex, h, "nvmlDeviceGetHandleByIndex_v2")
    purego.RegisterLibFunc(&l.DeviceGetFanSpeed_v2, h, "nvmlDeviceGetFanSpeed_v2")
    purego.RegisterLibFunc(&l.DeviceSetFanSpeed_v2, h, "nvmlDeviceSetFanSpeed_v2")
    purego.RegisterLibFunc(&l.DeviceSetFanControlPolicy, h, "nvmlDeviceSetFanControlPolicy")
    purego.RegisterLibFunc(&l.DeviceGetNumFans, h, "nvmlDeviceGetNumFans")
    // ... rest
    return l, nil
}
```

`nvmlDevice_t` is opaque per NVML API. ventd treats it as a `uintptr` handle and never dereferences it.

Capability probe for write support:

```go
// At init, attempt nvmlDeviceSetFanControlPolicy on each device with current policy.
// NVML_ERROR_NOT_SUPPORTED → arch < Maxwell (cap downgrade to ro_sensor_only).
// NVML_ERROR_NO_PERMISSION → wrong uid/gid (record, ventd will run as root anyway).
// NVML_SUCCESS → rw_full.
// NVML_ERROR_FUNCTION_NOT_FOUND from purego.RegisterLibFunc → driver < R520 (downgrade to rw_quirk: speed-only, no policy).
```

### 2.5 NVIDIA quirks ventd handles

**Q1: Headless / Wayland write path.** Pre-R515, the only fan-write API was `nvidia-settings -a [fan:N]/GPUTargetFanSpeed=...` which required X11. ventd targets R515+ NVML always. Document in install notes: ventd requires NVIDIA driver ≥ 515 for fan control.

**Q2: `nvmlDeviceSetDefaultFanSpeed_v2` is broken.** Per [NVIDIA forum thread](https://forums.developer.nvidia.com/t/nvmldevicesetdefaultfanspeed-v2-does-not-resume-fan-speed-algorithm-please-fix/214430), this call does not resume the auto fan curve as documented. Workaround: call `nvmlDeviceSetFanControlPolicy(dev, fan, NVML_FAN_POLICY_TEMPERATURE_CONTINOUS_SW)` on shutdown to restore vBIOS auto curve. Document as `exit_behaviour: bios_dependent`.

**Q3: Per-card fan count varies.** Some cards expose 1 fan (RTX 3060), some 2 (RTX 3080), some 3 (4090 Founders). ventd iterates `0..nvmlDeviceGetNumFans(dev)-1`. Per-fan independent control is supported — RTX 3090/4090 with 3 fans can be addressed individually.

**Q4: Laptop dGPUs often refuse fan control.** Per [forum report](https://forums.developer.nvidia.com/t/no-fan-control-in-nvidia-geforce-rtx-3050-ti-laptop-gpu/282020), Lenovo X1 Extreme + RTX 3050 Ti returns `GPUFanControlState=0` with no `GPUTargetFanSpeed` option, even with Coolbits=28 in xorg.conf. EC controls the fan. Catalog response: **laptop dGPU detection** (DMI chassis_type ∈ {laptop, notebook, sub_notebook, hand_held, convertible} → mark `requires_userspace_ec`, recommend NBFC backend per spec-09).

**Q5: Driver/library version mismatch.** Driver upgrades without reboot leave libnvidia-ml.so out of sync with kernel module → `nvmlInit_v2` returns NVML_ERROR_DRIVER_NOT_LOADED. ventd's behaviour: log, mark device monitor-only, retry on next service start. Do not fail the whole daemon.

**Q6: NVML driver model on Linux.** `nvmlDeviceGetDriverModel_v2` is Windows-only (returns NVML_ERROR_NOT_SUPPORTED on Linux). ventd skips this entirely.

**Q7: Persistence mode interaction.** With `nvidia-persistenced` running, NVML stays initialized after first client exits. Without it, NVML re-initializes each time a tool calls `nvmlInit_v2`. ventd as a long-running daemon holds NVML open, so persistence mode is not strictly required, but recommended.

**Q8: Fan speed exceeds 100%.** Per NVML docs: "This value may exceed 100% in certain cases." This refers to read-side `nvmlDeviceGetFanSpeed_v2` — the reported value can be >100% on some Quadro/Tesla cards in distress modes. ventd clamps display to 100% but logs raw value.

**Q9: Driver mode under Wayland.** Wayland doesn't run as root by default; without root, NVML write calls return NVML_ERROR_NO_PERMISSION even though the read side works. ventd runs as root regardless of session type, so this is not an issue for ventd. It is an issue for any user-side tools the user may also be running (CoolerControl, GreenWithEnvy) — use the `conflicts_with_userspace` runtime detection.

### 2.6 Multi-GPU enumeration

`nvmlDeviceGetCount_v2` returns total devices including ones the current uid lacks permission for. ventd as root sees all. Iterate by index 0..count-1; for each, store UUID (`nvmlDeviceGetUUID`) as the stable identifier across reboots. PCI bus-id reordering is real after kernel updates.

### 2.7 SLI / NVLink

For SLI/NVLink-bridged cards, each GPU still has independent fan control via NVML. No special handling needed — ventd treats each as a separate hwmon device.

---

## 3. AMD backend

### 3.1 Architecture matrix

AMD fan control split by SMU firmware (PMFW) generation, not just GPU architecture.

| Family | Cards | sysfs interface | RDNA-style fan_curve | Quirks |
|---|---|---|---|---|
| GCN1-4 | HD 7xxx, R9 2xx/3xx, RX 4xx/5xx | `pwm1`, `pwm1_enable` | no | classic hwmon, manual=1/auto=2 |
| Vega | Vega 56/64, Radeon VII, MI25/50/60 | `pwm1`, `pwm1_enable` | no | APU Vega does NOT expose pp_od_clk_voltage |
| RDNA1 | RX 5500/5600/5700 | `pwm1`, `pwm1_enable` | partial via OD curves | |
| RDNA2 | RX 6400 / 6500 / 6600 / 6700 / 6800 / 6900 | `pwm1`, `pwm1_enable` | no | manual write works, auto restore buggy |
| RDNA3 | RX 7600 / 7700 / 7800 / 7900 | **fan_curve only** | yes | manual `pwm1` write blocked, must use `gpu_od/fan_ctrl/fan_curve` |
| RDNA4 | RX 9070 (Navi 48) | fan_curve only, broken in 6.14 | yes (curve mode broken per LACT #524) | very early, validation gap |
| CDNA / MI200 / MI300 | datacenter | varies, often passive | n/a | out of scope, no consumer fan |

### 3.2 RDNA1/2 manual mode (classic path)

```
echo 1 > /sys/class/drm/card$N/device/hwmon/hwmon$M/pwm1_enable
echo 128 > /sys/class/drm/card$N/device/hwmon/hwmon$M/pwm1
```

`pwm1_enable` values per AMD kernel docs:
- `0` = fan disabled (writing duty does nothing)
- `1` = manual fan control (duty 0–255 in `pwm1`)
- `2` = automatic fan speed control (vBIOS or PMFW curve)

Note: this matches generic hwmon docs but with AMD-specific semantics.

### 3.3 RDNA3 fan_curve interface

RDNA3 firmware **removes** manual fan control. Only path is the 5-anchor-point curve at `/sys/class/drm/card$N/device/gpu_od/fan_ctrl/fan_curve`. Format:

```
echo "<index> <temp_c> <pwm_pct>" > fan_curve   # set point
echo "c"                          > fan_curve   # commit
echo "r"                          > fan_curve   # reset to default
```

5 anchor points (index 0..4). PWM units: percentage_0_100 in this interface (different from `pwm1` which is 0–255). Default curve out of factory is broken on many 7xxx cards (all-zero defaults — fans never spin).

ventd treats this as `pwm_unit: percentage_0_100`, `pwm_unit_max: 100`, `pwm_enable_modes: [firmware_curve, auto]`. The `firmware_curve_offload_capable: true` field becomes essential here — spec-05 P4-HWCURVE will use this path for predictive offload.

### 3.4 Other fan_ctrl entries (kernel 6.13+)

```
gpu_od/fan_ctrl/fan_curve                    # RDNA3+
gpu_od/fan_ctrl/fan_target_temperature       # RDNA3+
gpu_od/fan_ctrl/acoustic_limit_rpm_threshold # RDNA3+
gpu_od/fan_ctrl/acoustic_target_rpm_threshold# RDNA3+
gpu_od/fan_ctrl/fan_minimum_pwm              # RDNA3+
gpu_od/fan_ctrl/fan_zero_rpm_enable          # RDNA3+, kernel 6.13+
gpu_od/fan_ctrl/fan_zero_rpm_stop_temperature # RDNA3+, kernel 6.13+
```

ventd surfaces `fan_zero_rpm_enable` as a calibration-time observation (record current state, restore on shutdown). spec-04 PI controller will need to know whether zero-RPM is on, since auto-mode dead-band changes fan response shape.

### 3.5 Schema mapping

```yaml
# RDNA1/2 entry
- name: amdgpu
  variant: rdna1_rdna2
  capability: rw_full
  pwm_unit: duty_0_255
  pwm_unit_max: 255
  pwm_enable_modes:
    - disabled       # pwm1_enable=0
    - manual         # pwm1_enable=1
    - auto           # pwm1_enable=2
  off_behaviour: bios_dependent  # pwm1_enable=0 disables fan, pwm1_enable=2 returns to vBIOS curve
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - LACT
    - CoreCtrl
    - amdgpu-fan / amdfand
    - CoolerControl
    - radeon-profile-daemon
  fan_control_capable: true
  fan_control_via: hwmon
  polling_latency_ms_hint: 30
  required_modprobe_args: null
  pwm_polarity_reservation: null
  exit_behaviour: restore_auto  # write pwm1_enable=2 on shutdown
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false  # no fan_curve interface on RDNA1/2

# RDNA3+ entry
- name: amdgpu
  variant: rdna3_rdna4
  capability: rw_quirk           # fan_curve only, not rw_full
  pwm_unit: percentage_0_100     # fan_curve takes pct, not 0-255
  pwm_unit_max: 100
  pwm_enable_modes:
    - firmware_curve  # write 5-anchor curve, ASIC interpolates
    - auto            # default vBIOS curve via "r" reset
  off_behaviour: bios_dependent
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - LACT
    - CoreCtrl
    - CoolerControl (firmware-controlled mode)
  fan_control_capable: true
  fan_control_via: amdgpu_fan_curve
  polling_latency_ms_hint: 100   # fan_curve write+commit slower than direct pwm1
  required_modprobe_args:
    - amdgpu.ppfeaturemask=0x4000  # OverDrive bit, may be needed for full control
  pwm_polarity_reservation: null
  exit_behaviour: restore_auto    # write "r" to reset to default curve
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true  # spec-05 P4-HWCURVE candidate
```

### 3.6 AMD quirks ventd handles

**Q1: Stuck-auto-mode dance (RDNA1/2).** Documented driver bug, kernel 5.11+: with `pwm1_enable=2` on boot, `pwm1` value stuck at 0, fan never spins. Workaround: write `pwm1_enable=1`, then `pwm1=128`, then `pwm1_enable=2` to "wake up" the firmware curve. ventd performs this dance on first calibration if `pwm1=0` AND `temp1_input > 50000` (50°C).

**Q2: ppfeaturemask gating.** For RDNA3 fan_curve and RDNA1/2 manual override, OverDrive bit (`0x4000`) often required in kernel cmdline:
```
amdgpu.ppfeaturemask=$(printf '0x%x' $(($(cat /sys/module/amdgpu/parameters/ppfeaturemask) | 0x4000)))
```
ventd cannot set kernel cmdline. Detection: check `/sys/module/amdgpu/parameters/ppfeaturemask`, AND-with `0x4000`, log if zero and recommend in install message.

**Q3: Tainted kernel since 6.14.** Per Gentoo wiki: "On recent kernels like v6.14 (commit b472b8d829c1), the CPU will be tainted as being out-of-spec if overdrive is enabled." Document: enabling OD taints kernel — ventd does not require OD for basic `pwm1` writes, only for RDNA3+ `fan_curve` access.

**Q4: Reset-to-auto write fails.** Arch wiki and multiple bug reports: `pwm1_enable=2` after manual mode often does NOT restore vBIOS curve; sometimes requires driver reload. Mitigation: ventd documents this in exit_behaviour, attempts the write on shutdown, but does not guarantee success. Suggested user action: reboot to fully reset.

**Q5: Multi-monitor blocks pp_dpm changes.** Per LinuxReviews: with two monitors attached, writes to `pp_dpm_sclk` etc. return EINVAL. Doesn't affect fan control directly, but ventd's diagnostic bundle must capture this for users who report related issues.

**Q6: hwmon$M number unstable across boots.** Standard hwmon issue: `/sys/class/drm/card0/device/hwmon/hwmonN` where N varies. Mitigation: ventd resolves via `/sys/class/drm/card*/device/hwmon/hwmon*/name` matching, never hard-codes hwmonN.

**Q7: APU Vega has no OD interface.** Ryzen APUs (2200G/3400G/5700G/etc.) integrated Vega graphics share thermal with CPU, no separate fan, no `pp_od_clk_voltage`. ventd ignores APU GPU entries (covered by k10temp/zen power monitoring).

**Q8: Zero-RPM mode interaction with PI control.** spec-04 cross-cut: when `fan_zero_rpm_enable=1` and PMFW is in auto, fan stops below threshold temp. PI controller in manual mode bypasses this. ventd's PI tuning must avoid commanding 0% if zero-RPM is disabled (causes audible pulsing as fan ramps off/on).

**Q9: CoolerControl firmware-controlled profile coexistence.** Per CC docs: "Do not run LACT and CoolerControl in fan-control mode at the same time for the same newer AMDGPU card." Same applies to ventd. Runtime detection: ventd writes a sentinel value, reads back after 200ms; if value drifted, another writer is fighting. Log and switch to monitor-only.

### 3.7 Hardware validation gaps

Phoenix's box matrix has zero AMD discrete GPUs. spec-05-prep memory note flagged "$80–150 AUD AMD GPU acquisition" for AMDGPU validation. Recommended targets:

| Card | Why | Cost (AUD, 2026 used market) |
|---|---|---|
| RX 6600 / 6600 XT | covers RDNA2 mainstream, working manual + auto | $120-200 |
| RX 7700 XT / 7800 XT | RDNA3 fan_curve required for spec-05 P4-HWCURVE | $400-600 (new market) |
| RX 580 / 5500 XT | covers GCN/RDNA1 legacy paths | $50-100 |

Minimum: any RDNA2 card to close the basic manual-write path. RDNA3 ideally before spec-05 v0.7.0.

---

## 4. Intel backend

### 4.1 Status as of kernel 6.12+

| Generation | Status | Driver | hwmon name | Capability |
|---|---|---|---|---|
| iGPU (any) | n/a | i915, xe | n/a | no fan |
| DG1 | abandoned | i915 | none | no fan API |
| DG2 (Arc Alchemist A380/750/770) | RO sensor (6.12+) | i915 | i915 | ro_sensor_only |
| BMG (Battlemage B580/B570) | RO sensor (6.12+) | xe | xe | ro_sensor_only |
| Xe3 (Panther Lake) | upcoming | xe | xe | unknown — likely fan-firmware-managed |

Per Phoronix and kernel ABI docs at `Documentation/ABI/testing/sysfs-driver-intel-xe-hwmon`: `fan1_input`, `fan2_input`, `fan3_input` are RO. **No `pwm1` or `pwm1_enable` exposed.**

Intel ships fan control firmware (`fan_control_8086_e20b_8086_1100.bin`) that runs on the GPU itself. There is no Linux-side write path. Windows tools that "control" Arc fans are talking to the same firmware via WMI; the firmware has the final say.

### 4.2 Schema mapping

```yaml
- name: i915
  capability: ro_sensor_only
  pwm_unit: null
  pwm_unit_max: null
  pwm_enable_modes: []
  off_behaviour: state_off       # cannot write, semantically off-as-far-as-userspace-concerned
  recommended_alternative_driver: null  # no OOT alternative exists
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: null
  polling_latency_ms_hint: 50
  required_modprobe_args: null
  pwm_polarity_reservation: null
  exit_behaviour: state_off
  runtime_conflict_detection_supported: false
  firmware_curve_offload_capable: true   # firmware controls curve, but ventd can't upload to it

- name: xe
  capability: ro_sensor_only
  # ... otherwise identical to i915 entry above
```

### 4.3 What ventd does with Intel discrete

Read fan RPM (`fan1_input` etc.) for diagnostic/monitoring display. **Never** attempt to write — there's nothing to write to. Surface to user: "Intel Arc fan control is firmware-managed. ventd shows fan speed but cannot adjust it."

### 4.4 Hardware validation gap

No Intel discrete GPU in Phoenix's box matrix. Low priority — RO path is trivial (just file reads) and doesn't unlock spec-05. If Intel exposes a write path in some future kernel, ventd updates the catalog entry and ships in next patch.

---

## 5. Cross-vendor concerns

### 5.1 Multi-GPU systems

ventd enumerates each GPU as a distinct hwmon device. Identification stable-keys:
- NVIDIA: GPU UUID via NVML
- AMD/Intel: PCI bus-id (`/sys/class/drm/card*/device/uevent` → `PCI_SLOT_NAME`)

`/sys/class/drm/card*` numbering is unstable across kernel versions — never persist `cardN`.

### 5.2 Hybrid laptops (NVIDIA Optimus / AMD PRIME)

dGPU is power-gated when idle. NVML write attempts wake the dGPU. Frequent fan adjustments → defeat of dGPU power saving → battery drain. ventd's behaviour for hybrid laptops: detect via DMI chassis_type ∈ {laptop, notebook}, AND second GPU present, AND iGPU exists. If true → mark dGPU as `monitor-only on battery, full-control on AC` (cross-cuts spec-04 PI tuning for AC-vs-battery profile selection).

### 5.3 eGPU enclosures (Thunderbolt, OCuLink)

Out of scope for v1.0. eGPU hot-plug means GPU enumeration changes mid-runtime; ventd would need to handle hwmon device add/remove via udev. Punt to post-v1.0.

### 5.4 Mining-rig multi-card (>4 GPUs)

Out of scope. Mining-rig users have specialized tools (HiveOS, etc.); ventd targets workstation/desktop/homelab.

### 5.5 GPU thermal mass implications for spec-05

Three regimes:
- Discrete desktop GPU (RTX 4090, RX 7900): ~1–2 kg copper + vapor chamber, thermal time constant 30–120s
- Discrete laptop GPU (mobile RTX/Radeon): ~50–100g aluminum, time constant 5–20s
- iGPU sharing CPU IHS: <10g effective mass, time constant 1–3s, dominated by CPU dynamics

spec-05 trace harness must capture each. spec-05-prep's box matrix already covers all three (4090 desktop, 3060 in Proxmox, IdeaPad iGPU, SteamDeck APU).

### 5.6 Predictive thermal hardware-curve offload (spec-05 P4-HWCURVE)

Only RDNA3+ exposes a writable on-chip 5-point curve. legion_hwmon exposes a 10-point curve. NVML has no curve upload (just speed setpoint). Intel firmware-only.

Spec-05 P4-HWCURVE candidates ranked by leverage:
1. **legion_hwmon** (10 points) — already in spec-03 amendment, primary validation target
2. **AMD RDNA3 fan_curve** (5 points) — secondary, validation needs an RDNA3 card
3. **NVML manual mode + ventd-side curve** — fallback, no offload, just normal PI control

### 5.7 Diagnostic bundle GPU detection items (PR 2c contribution)

Bundle should capture:
- PCI vendor/device for each `/sys/class/drm/card*` (lspci -nn equivalent)
- For NVIDIA: NVML driver version, NVML library version, count of devices, per-device UUID + name + arch
- For AMDGPU: kernel module version (from `modinfo amdgpu`), `ppfeaturemask` value, OverDrive bit state, per-card `gpu_metrics` snapshot
- For Intel: xe vs i915 driver per card, kernel version (xe-hwmon needs 6.12+ for fan reporting)
- For all: tainted kernel state (`/proc/sys/kernel/tainted`)
- conflicts: list of running processes holding `/dev/nvidia*`, `/dev/dri/card*`

---

## 6. RULE-HWDB-PR2-* invariants for GPU entries (additions to PR 2 schema)

No new RULE-HWDB-PR2 invariants. All 14 from spec-03 amendment apply unchanged. Specifically:

- **RULE-HWDB-PR2-08** (refuse `bios_overridden`): Intel xe entries have `capability: ro_sensor_only` so apply path never runs.
- **RULE-HWDB-PR2-09** (BIOS version mismatch → recal): GPU vBIOS version captured via NVML `nvmlDeviceGetVbiosVersion` / amdgpu `amdgpu_pm_info`. Stored in `firmware_version` field of calibration result.
- **RULE-HWDB-PR2-13** (firmware_curve_offload_capable matches pwm_enable_modes presence of `firmware_curve`): RDNA3+ entry sets both consistently.

---

## 7. Out of scope for spec-03b

1. **eGPU enclosure fans** (separate fan controller in enclosure, not card): post-v1.0.
2. **AIB-vendor proprietary RGB/LED control**: out of scope, ventd is fan-only.
3. **Compute-only datacenter GPU fans** (MI300, H100 SXM): often passive or chassis-managed, post-v1.0.
4. **Mining-rig orchestration**: out of scope.
5. **GPU power-limit control**: separate concern, may live in spec-11+.
6. **Per-GPU-zone thermal sensors** (memory junction, hot spot, edge): ventd reads these for monitoring but uses the highest of the three for control (per spec-04 framing). Cross-zone weighting deferred to spec-05.

---

## 8. Cross-spec dependencies

| Spec | Dependency direction |
|---|---|
| spec-03 PR 2 | spec-03b extends — must land first |
| spec-03 PR 2c diag bundle | spec-03b adds GPU detection items (§5.7) |
| spec-04 PI autotune | GPU fans have wider thermal time constants; tuning ranges differ (cross-cut, no schema change) |
| spec-05 predictive thermal | RDNA3 fan_curve and legion_hwmon are P4-HWCURVE prior art |
| spec-05-prep trace harness | This catalog locks the read paths the harness uses for GPU traces |
| spec-09 NBFC | Laptop dGPU `requires_userspace_ec` cases route to NBFC backend |

---

## 9. Implementation plan (post-PR 2)

spec-03b ships as a **separate PR after PR 2c**, since it depends on PR 2 schema being on main. Sequencing:

1. PR 2a: matcher + chip catalog (no GPU entries yet, just hwmon chips)
2. PR 2b: calibration probe
3. PR 2c: diagnostic bundle (includes GPU detection items per §5.7)
4. **PR 2d (this spec): GPU vendor catalog**
   - `internal/hal/gpu/nvml/{loader,types,wrappers}.go` — purego NVML thin wrapper
   - `internal/hal/gpu/amdgpu/{sysfs,fan_curve,quirks}.go` — sysfs-direct
   - `internal/hal/gpu/xe/{sysfs}.go` — RO only
   - `internal/hwdb/catalog/drivers/amdgpu.yaml` — RDNA1/2 + RDNA3/4 variants
   - `internal/hwdb/catalog/drivers/nvidia.yaml` — virtual entry (no hwmon name)
   - `internal/hwdb/catalog/drivers/i915.yaml` + `xe.yaml`
   - subtests for capability probe, RDNA3 fan_curve write, NVML write, hybrid-laptop detection
5. spec-05-prep trace harness consumes this in PR 1

PR 2d est cost: $20-35 (smaller than 2a-2c, isolated to gpu/ subtree, schema unchanged).

---

## 10. Open questions for chat 3 / Phoenix

- **Q1:** spec-03b ships as PR 2d (after 2c), or held until v0.6.0+ when actually needed by PI/predictive? **Recommendation: PR 2d, because PR 2c diag bundle benefits from having NVML detection in place.**
- **Q2:** purego NVML wrapper — vendored under `internal/hal/gpu/nvml/` or separate go module `github.com/ventd/purego-nvml`? **Recommendation: internal first; extract to module post-v1.0 if other projects want it.**
- **Q3:** AMD GPU acquisition — wait until v0.5.0 spec-03 PR 3 needs validation, or buy now to unblock spec-03b PR 2d? **Recommendation: buy a used RDNA2 ($120-200) before v0.5.0, RDNA3 only if budget allows before v0.7.0 spec-05.**

---

**End of spec-03b GPU vendor catalog.**
