# Ecosystem mining assessment — 2026-05-17 sweep, `mine-technique` rows

Companion to the `ingest-catalog` waves (1-13). The 2026-05-17 ecosystem
sweep tagged ~170 repositories `mine-technique` ("read and re-implement an
algorithm or pattern; no source-merge"). This document records, per
cluster, whether ventd **already implements** the technique (with the
owning component) or whether it is a **genuine TODO**. It is the work
product of the "mining" half of the catalogue effort: the conclusion for
most clusters is that ventd's mature controller + HAL already cover them,
so no new code is warranted and the sweep rows are marked done.

ventd version at assessment time: v1.2.0 (post audit/fix wave).

## Covered by the controller / curve engine

| Sweep cluster | Technique | ventd component |
|---|---|---|
| D1 general daemons (fancon, afancontrol, coolercontrol, hbriese/fancon, zen-fan, pwmfan-go) | RPM↔PWM mapping, interpolation, smoothing, config schema | `internal/controller/` (controller.go, blended.go), `internal/curve/` (linear.go, points.go, mix.go, fixed.go) |
| D2 PID / smart-curve (zimward/pid-fan-controller, ThunderMikey, storagefancontrol, iceteaSA learning loop) | PID / PI feedback over sysfs, history, learning PWM optimiser | `internal/curve/pi.go` (PI controller), `internal/controller/decisioncache.go` |
| CoolerControl / fan2go (cross-cutting) | hysteresis, dead-zone, response-time, spin-up | `internal/controller/hysteresis_test.go`, controller tick path |
| fan2go #17 idle-guard + closed-set allowlist | only act when idle; bounded write set | `internal/idle/` (psi.go, cpuidle.go, quiescence.go, user_input.go), `internal/idle/blocklist_export.go` |
| lm-sensors pwmconfig probe ("test PWM, watch RPM") | canonical calibration probe | `internal/calibrate/` |
| acoustic / dBA-aware (cross-cutting) | per-fan acoustic cost model | `internal/acoustic/` (capture, proxy, runner, stall) |

## Covered by the HAL backends (`internal/hal/`)

| Sweep cluster | Technique | ventd backend |
|---|---|---|
| B3 Dell iDRAC IPMI daemons (tigerblue77, jsenecal, UpperCenter, kk7ds, arf20, etc.) | `ipmitool raw` fan-zone control, PID over IPMI sensors | `internal/hal/ipmi/` (backend.go, proto/) |
| B4 HPE iLO daemons (DavidIlie, fed1337, x86shell, alex3025, etc.) | SSH/IPMI fan setters on modded iLO4 | `internal/hal/ipmi/` (iLO4 reachable rows); iLO firmware unlock itself stays package-only |
| B5 Supermicro (petersulyok/smfc, zimmertr, mrstux hybrid, VD-15, etc.) | multi-zone CPU+HDD IPMI daemon | `internal/hal/ipmi/` (zone control + watchdog arm; board overrides `requires_watchdog`) |
| B6 IBM/Lenovo/Cisco/Fujitsu BMC (pyghmi, easyucs, redfish libs) | OEM IPMI/Redfish handlers | `internal/hal/ipmi/` + Redfish-forward note in server board overrides |
| B7 generic IPMI/Redfish frontends (DMTF tools, freeipmi wrappers, DrSpeedy, Unraid Dynamix) | IPMI fan abstraction, drive-temp triggers | `internal/hal/ipmi/` |
| B8 GPU fan (LACT, nvfancontrol, nfancurve, headless-X spoof tricks, undervolt) | NVIDIA/AMD pwm1_enable, fan curves; **headless control without dummy-X** | `internal/hal/nvml/`, `internal/hal/gpu/`, `internal/nvidia/` (NVML needs no X server, superseding the dummy-X-server trick) |
| C (AIO/cooler protocol mining: krakenx, OpenCorsairLink, EK-Loop, riing) | USB-HID cooler control | `internal/hal/liquid/`, `internal/hal/usbbase/` + the mainline cooler driver/chip profiles (waves 8, 12) |

## Covered as reference / superseded (no action)

- **B10 SBC/Pi/Jetson/Orange Pi/Rock5B** — the lag-triggered state machine
  (DWCarrot/fanctrl-rock5b) and stepped curves are subsumed by ventd's
  hysteresis controller + `pwm-fan` DT driver profile. The bulk of these
  rows were already `reference` (`🔵`). Jetson `jetson_stats` is AGPL-3 —
  study-only regardless.
- **A14 Chromebooks (cros_ec)** — `internal/hal/crosec/` backend already
  drives `cros_ec` fans; the ectool-based scripts are superseded.
- **Apple Intel mbpfan forks (A7)** — `internal/hal/asahi/` covers Apple
  Silicon; Intel-Mac `applesmc` is a thin hwmon ventd reads directly. The
  ~10 mbpfan forks are `track` only.

## Genuine TODOs (left pending in the sweep)

These are NOT yet covered and remain open `mine-technique` / `ingest`
rows for a future wave:

- **D4 storage spin-down-safe probing** (desbma/hddfancontrol): ventd reads
  drivetemp/nvme but lacks hddfancontrol's "don't wake a spun-down HDD to
  read its temperature" guard. Worth adopting for NAS/homelab profiles.
- **C6/C7/C8 un-RE'd cooler protocols** (DeepCool digital LCD, EK-Loop
  Connect, Lian Li UNI/AL V2): no mainline hwmon driver exists; these need
  protocol reverse-engineering before ingest, not just re-implementation.
- **A6 yamdcc EC-to-config probe utility** (bca009): an EC-register
  auto-mapper that emits a config; ventd's calibrate probes channels but
  does not generate an EC offset map. Distinct from pwmconfig probing.
- **Intel Arc / xe userland fan control** (B8 gap): no userland tool
  exists anywhere; ventd first-mover opportunity, tracked separately.

## Marking convention applied to the sweep doc

Rows in the covered clusters above are ticked `✅` in
`ecosystem-catalog-sweep-2026-05-17.md` with this document as the
rationale. The genuine-TODO rows are left unmarked (pending).
