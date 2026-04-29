# R5 — User Idle Gate: Defining "Idle" for ventd Envelope C Calibration

**Research target:** ventd v0.5.3+ (Linux fan controller daemon, Go 1.25+, CGO_ENABLED=0, GPL-3.0) — the predicate used to gate the Envelope C dT/dPWM probe so that calibration runs only when the system is idle enough to yield a clean thermal response curve.
**Audience:** homelab/NAS users on TrueNAS, Unraid, Proxmox (LXC + KVM), plus desktops and laptops.
**Date:** 2026-04-28.

---

## 1. Why this matters: what corrupts an Envelope C probe

Envelope C measures the differential thermal response of a cooling loop by stepping PWM and watching how a temperature sensor responds. The calibration is only valid when the *only* thermal forcing function on the sensor is the fan step itself. Any of the following will corrupt the measurement:

- **CPU bursts**: a 200 ms compile-job kworker spike injects ~10–30 W of package power that swamps the fan-step signal.
- **GPU bursts**: a Plex hardware-decode session can shift GPU package power by 20+ W.
- **Disk-spinup heat**: a ZFS scrub I/O burst wakes drives that re-emit IR into chassis thermistors.
- **Background daemons that look idle to `top`**: smbd accept loops, ZFS arc shrinker, cron-driven `mlocate.updatedb`, BTRFS balance, mdadm resync, automatic `apt`/`dnf-makecache`, Plex library scans.
- **lxcfs lying**: in Proxmox 8 / Debian 12 LXC containers, `/proc/loadavg` returns the *host's* getloadavg() through `glibc`, while `cat /proc/loadavg` returns the container's. Trusting one and not the other is a real footgun. (See §6.)

A defensible idle predicate must therefore (a) be multi-signal, (b) tolerate `/proc` lies in containers, (c) be cheap enough to run continuously, (d) work headlessly, and (e) hard-refuse on laptop battery.

---

## 2. Survey of how prior-art tools define "idle"

The single most striking finding from this survey is that **none of the major Linux fan-control daemons currently have an explicit user-idle gate gating their calibration**. They all assume the user runs calibration interactively, and the responsibility for being idle falls on the human operator. That is itself a citation: ventd is breaking new ground here, and the design needs to be defensible from first principles, not by analogy.

### 2.1 Tool-by-tool review

| Tool | Calibration step? | Idle gate? | Signal / mechanism | Source reference |
|---|---|---|---|---|
| **fan2go** (markusressel/fan2go) | Yes — sweeps PWM at startup to find `minPwm`/`startPwm`/`maxPwm` per fan; results visible via `fan2go fan --id … curve` | **No idle gate.** Documentation acknowledges the sweep "can take quite a while though and can lead to overheating of components if they are under load. To avoid this use the minPwm and maxPwm fan config options to set the boundaries yourself." | Manual operator vigilance only | github.com/markusressel/fan2go README |
| **fancontrol / pwmconfig** (lm-sensors) | Yes — `pwmconfig` interactively probes each PWM, briefly shutting fans to find min-stop and start-up PWM | **No idle gate.** The tool prints a warning: *"Pwmconfig will shutdown your fans to calibrate settings against fan speed. Make sure your system won't overheat while you configure your system. Preferably the computer is completely idle, and all powersaving measures are enabled."* Responsibility is on the operator | github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/pwmconfig (warning string ~line 320) |
| **CoolerControl** | No structured calibration; user-driven curves | Has a *power-state* feature: drive temp reported as 0 °C if HDD is in stand-by, so curves drop fan speed for spun-down disks. This is **not** a user-idle gate — it's a per-sensor "device idle" gate | docs.coolercontrol.org "Drive Power State" |
| **NBFC / nbfc-linux** | No calibration; user supplies fan-curve JSON | None | github.com/nbfc-linux/nbfc-linux |
| **liquidctl** | None — it's a stateless transport | None | github.com/liquidctl/liquidctl |
| **asusctl / supergfxctl** | None | None | github.com/flukejones/asusctl |
| **TuxClocker** | User-driven curves only | None | github.com/Lurkki14/tuxclocker |
| **thinkfan** | None — config-driven | None | github.com/vmatare/thinkfan |
| **Argus Monitor (Windows ref)** | "Fan Calibration" measures stop/start RPM; user-initiated, no gate | None implicit | argusmonitor.com docs |
| **Fan Control (Rem0o, Windows)** | Has "Fan calibration" auto-mode that ramps 100→0 % to map the RPM/% curve. Discussion #2333 confirms each step takes ~8–20 s | None — operator is asked to leave the system alone. The "Auto" curve has internal *load* zones (idle vs load temperature), but those are output-curve concepts, **not** a calibration idle gate | github.com/Rem0o/FanControl.Releases Discussion #2333 |
| **OpenHardwareMonitor / LibreHardwareMonitor** | Read-only sensors | n/a | n/a |
| **systemd-logind** | n/a — provides `IdleHint` D-Bus property at `/org/freedesktop/login1/session/<id>` | Reads input device activity (X11) or `xss-lock` proxy on tty | freedesktop.org/software/systemd/man/logind.conf.html |
| **swayidle / hypridle** | n/a — ext-idle-notify consumers | Wayland `ext-idle-notify-v1` | github.com/swaywm/swayidle |
| **xss-lock + X Screensaver ext.** | n/a | Bridges X11 idle → systemd `IdleHint` | manpage xss-lock(1) |
| **powertop --autotune** | n/a — applies tunables | None for "idle"; runs whenever invoked. `--calibrate` toggles backlight/Wi-Fi for 1.5 h, expressly NOT idle-aware | wiki.archlinux.org/title/Powertop |
| **TLP** | n/a | Has *AC vs battery* policy, NOT a system-idle policy. Uses `/sys/class/power_supply/AC/online` | linrunner.de/tlp |
| **cpuidle (kernel)** | n/a | Defines per-CPU C-state residency in `/sys/devices/system/cpu/cpu*/cpuidle/state*/{time,usage}`; this is the ground truth for "this CPU thread spent N µs in idle" | docs.kernel.org/admin-guide/pm/cpuidle.html |

### 2.2 The lesson from prior art

The prior-art consensus is *"calibration is an interactive privileged operation; the user is responsible for being idle."* ventd's autonomous Envelope C breaks that assumption — it must run unattended, possibly weeks after install, possibly on a headless NAS. So ventd inherits **no working precedent** for the idle predicate and must construct one. Fortunately, the necessary primitives all exist in /proc and /sys.

---

## 3. Catalog of available idle signals

This is the complete signal-comparison table. "FP rate" = looks idle but isn't; "FN rate" = looks busy but is idle.

| Signal | What it measures | Resolution / staleness | FP rate | FN rate | Headless? | Container-safe? | Cost to read |
|---|---|---|---|---|---|---|---|
| `/proc/loadavg` 1-min | EWMA of runnable + `TASK_UNINTERRUPTIBLE` task count, sampled every 5 s + 1 tick | ~5 s sample, 60 s decay constant; min latency to read 0.0 from a busy state ≈ 60 s | **High**. EWMA trails reality. A 60 s CPU burner only pulls 1-min loadavg to ~0.62, not 1.0 (Brendan Gregg, 2017). Conversely a brief I/O storm can keep it elevated for minutes | Medium. Includes D-state, so heavy `dd` to NFS pumps loadavg even though no CPU is consumed | Yes | **Broken on Proxmox 8 / Debian 12 LXC**: `getloadavg(3)` (glibc) returns host loadavg even when `cat /proc/loadavg` returns container loadavg, because lxcfs `--enable-loadavg` only intercepts the file read, not the syscall path used by libc. Confirmed 2024+ in lxc/lxc#4372 | 1 read, ~10 µs |
| `/proc/stat` (`cpu` line delta) | CPU jiffies in user/sys/idle/iowait over an interval | Whatever sample window you pick (typ. 1 s) | Low–medium. A cron @minute spike of 200 ms is invisible if you sample at 1 Hz with 5 s averaging | Low | Yes | lxcfs *does* virtualize `/proc/stat` for cpuset-restricted containers, but iowait is meaningless inside a container. Use only `idle` and `nonidle = user+sys+nice+irq+softirq+steal` | 1 read + delta math; ~50 µs |
| `/proc/pressure/cpu` (PSI) | Cumulative µs that "some" / "all" tasks were stalled waiting for CPU; EWMA over 10 / 60 / 300 s | 10 s minimum window, sampled with 100 ms granularity inside the kernel | Low. PSI rises only when there is actual contention, so an idle 16-core box with one busy core stays at `some avg10 ≈ 0` | Slightly higher than utilization for "is anyone running"; will show 0 even when one core is at 100 % because nothing is *waiting* | Yes | Kernel ≥4.20. **PSI is enabled by default in Debian/Ubuntu/Arch but DISABLED by default in RHEL 8/9** (`CONFIG_PSI_DEFAULT_DISABLED=y`); requires `psi=1` cmdline. Inside cgroup v2 containers PSI is per-cgroup at `/sys/fs/cgroup/<group>/{cpu,memory,io}.pressure`, which is the **correct** signal for LXC/Docker | 1 read, ~10 µs |
| `/proc/pressure/io` (PSI) | Stall waiting for I/O completion | 10 / 60 / 300 s | Low | Low | Yes | Same as cpu.pressure. ZFS scrub typically pushes `io.some avg10` > 5 % | ~10 µs |
| `/proc/pressure/memory` (PSI) | Stall waiting for page reclaim, swap-in, refaults | 10 / 60 / 300 s | Low | Low | Yes | Same | ~10 µs |
| `/sys/devices/system/cpu/cpuN/cpuidle/stateK/{time,usage}` | Cumulative µs each CPU spent in C-state K, plus enter count | µs precision; updated every wakeup | Very low. C6/C8/C10 residency only accumulates if the core actually halted | Low. POLL/C1 dominates if the workload does many short sleeps; you must aggregate across deep states only | Yes | **Often unavailable inside LXC** — sysfs cpuidle is host-only. Useful on bare metal and KVM guests, not LXC. On AMD systems may show only POLL+C1+C2 | N reads × cpus × states; 1–5 ms on a 32-thread box |
| `/sys/class/powercap/intel-rapl/intel-rapl:0/energy_uj` | Cumulative package energy in µJ; differentiate to get watts | Counter wraps at `max_energy_range_uj` (≈262 J on most CPUs ⇒ ~6 s at 40 W); read frequency must be ≥1/4 the wrap interval | Low — power is the truest "is the CPU doing anything" measure | Low | Yes | **Not exposed in LXC** unless explicitly bind-mounted. Intel only on Linux ≤5.x; AMD got `amd_energy` and later `amd_pmf`/`amd_hsmp` exposure on newer kernels. Read access tightened in 2020 (CVE-2020-8694) — now requires `CAP_SYS_ADMIN` | 1 read, ~5 µs |
| `/sys/class/drm/card*/device/gpu_busy_percent` | AMD GPU busy % | 1 s smoothed | Low | Low | Yes | Container: only if `/dev/dri` is passed through | ~5 µs |
| `nvidia-smi --query-gpu=utilization.gpu` (or NVML directly) | NVIDIA GPU busy % | ~1 s | Low | Low | Yes | Container: needs `nvidia-container-toolkit` | NVML call ~5 ms; **avoid via CGO** in ventd; use sysfs `/proc/driver/nvidia/gpus/*` files where possible |
| `/proc/diskstats` (sectors read/written deltas) | Block-layer activity per block device | 1 s sample | Low | Medium — a busy ZFS ARC hit serves reads from RAM and shows zero diskstats activity, but the box is still doing real work | Yes | Container: returns host or filtered list depending on lxcfs version | 1 read, ~50 µs |
| `/sys/class/net/*/statistics/{rx,tx}_packets` | NIC packet counters | 1 s sample | Low | Low (a Wake-on-LAN heartbeat is ~1 packet/min — easy to filter) | Yes | Container-safe (per-netns) | ~100 µs aggregate |
| `/proc/<pid>/io` aggregated | Per-process bytes read/written including page-cache | Cumulative; needs delta | Low | Low | Yes | Container: per-pidns | Expensive: O(N pids); use sparingly |
| **systemd-logind D-Bus** `IdleHint` on Manager + per-Session | Set true by display manager / xss-lock when no input for `IdleActionSec` | Coarse — depends on session | High when no graphical session exists (gdm "manager-early" never sets idle, see systemd#34844, 2024) | High on tty-only servers — `IdleHint` may be permanently `false` regardless of activity | **No** on headless. Useful only as a *desktop signal*. systemd #9622 (2018) confirms tty/ssh sessions are not tracked | Container: useless inside LXC (no logind) | D-Bus call, ~1 ms |
| **Wayland `ext-idle-notify-v1`** | Compositor-reported user activity timeout | Programmable timeout (seconds) | Low when present | n/a | **No** on headless | Container: only if Wayland socket is forwarded | Connection setup + event |
| **X11 `XScreenSaverQueryInfo` / `xprintidle`** | Time since last keyboard/mouse | 1 ms | Low | n/a | **No** on headless | Same | One Xss call, ~1 ms |
| `/sys/class/power_supply/AC/online` (or `…/ADP1/online`) | AC adapter state | Edge-triggered via uevent; readable any time | n/a | n/a | Yes | LXC: present if udev/sysfs is exposed | ~1 µs |
| `/sys/class/power_supply/BAT*/status` | "Charging" / "Discharging" / "Full" / "Not charging" | Polled, ~1–10 s | n/a | n/a | Yes | Same | ~1 µs |
| `thermald`/`intel_powerclamp` hints | Forced CPU idle injection | n/a | n/a | n/a | Yes | n/a | n/a |
| Process-name allowlist (rsync, smbd, smbd-accept, plex-transcoder, zfs-scrub, mdadm, btrfs balance) | Heuristic process census of `/proc/[pid]/comm` | Snapshot | n/a | n/a | Yes | Per-pidns | Reading 200 procfs entries is ~5 ms |

### 3.1 Why PSI is the keystone signal (and why loadavg is not)

Three independent kernel-layer truths converge on PSI as the right primary signal:

1. **The kernel's own documentation** explicitly contrasts PSI with loadavg: *"The shortest averaging window is 1m, which is extremely coarse, and it's sampled in 5s intervals. A *lot* can happen on a CPU in 5 seconds. This *may* be able to identify persistent long-term trends and very clear and obvious overloads, but it's unusable for latency spikes and more subtle overutilization. PSI's shortest window is 10s."* (Facebook PSI v2 patch cover letter, LWN/759658).
2. **Brendan Gregg (2017)** showed loadavg conflates D-state I/O with CPU demand, which means `mdadm resync` looks like a "load" of 1.0 even though the CPU is 99 % idle. ventd cares about whether the *CPU and storage are quiet*, not whether D-state tasks are pending — so the conflation is actively harmful.
3. **lxcfs is broken for loadavg in Proxmox 8/Debian 12** (lxc/lxc#4372, 2023; reproduced again in pi-hole/pi-hole#5565, 2024): `getloadavg(3)` returns the host load while `/proc/loadavg` returns the container's. ventd would have to bypass libc to read the file directly. Even if it does, lxcfs's loadavg is computed by walking `/proc/<pid>/status` for `R`/`D` state every 5 s — extremely lossy. PSI inside the container's cgroup at `/sys/fs/cgroup/cpu.pressure` does not have this problem because the kernel directly accounts the cgroup.

### 3.2 Why C-state residency is the second-best signal (when PSI is unavailable)

Sometimes PSI is disabled (RHEL 8 default, some embedded kernels < 4.20 still in the wild on TrueNAS Core which is FreeBSD-derived). C-state residency is then the fallback. The kernel's `cpuidle` subsystem (Documentation/admin-guide/pm/cpuidle.rst) maintains per-CPU per-state cumulative time and entry count. The well-known invariant is:

> If, over interval Δt, **deep-state residency (sum of state2..stateN time across all CPUs) divided by (Δt × ncpus)** is > X, the system was largely idle.

Practical X = 0.85 for a desktop/server, 0.95 for a NAS that should mostly halt cores. This is robust against the loadavg "ghost load" because it directly observes the hardware halt.

---

## 4. False-positive (looks idle but isn't) catalog for ventd

A "false-positive" is the dangerous case: ventd thinks idle, fires Envelope C, and the probe is corrupted by hidden background work. Each scenario below is a *concrete* workload ventd's homelab/NAS audience will hit.

| Scenario | What it does to thermal | Why naive idle gates miss it | What catches it |
|---|---|---|---|
| **ZFS scrub** (TrueNAS, default Sunday 00:00) | Disks spin sustained reads; CPU ARC compression hashing burns 1–8 % CPU; chassis temp drifts up 2–5 °C over hours | loadavg can stay < 0.5 because most work is in I/O wait; CPU% is low | `io.pressure some avg60 > 5 %`; `/proc/diskstats` sectors_read delta; ZFS-aware: read `/proc/spl/kstat/zfs/<pool>/state` |
| **mdadm resync** | Same as scrub but for Linux software RAID | Same | Read `/proc/mdstat`; if it contains `recovery =` or `resync =`, refuse |
| **BTRFS scrub or balance** | Sustained CPU + I/O | Same | `btrfs scrub status` or check `/sys/fs/btrfs/<uuid>/devinfo/*/scrub_speed_max` |
| **Plex / Jellyfin background scan or transcode** | GPU and/or CPU ramp | `/proc/loadavg` may not budge if transcode is on QSV/NVENC; CPU% partial | GPU sysfs busy_percent; PSI io for media library scan; process allowlist `plex-transcoder`, `Plex Media Scanner`, `jellyfin-ffmpeg` |
| **rsync / borg / restic backup** | Heavy disk + network | loadavg moves but slowly; PSI io spikes | `io.pressure` + process allowlist |
| **Automatic update jobs** (`apt unattended-upgrades`, `dnf-automatic`, `pacman -Syu` from cron) | Heavy CPU + I/O | Burst of 30–600 s | PSI cpu + process allowlist `apt`, `dpkg`, `dnf`, `rpm`, `pacman` |
| **`mlocate.updatedb` / `plocate-updatedb` cron** | Light but sustained directory walking; chassis temp can shift on a hot HDD case | Easy to miss in a 60 s window | `io.pressure`; process allowlist |
| **systemd-tmpfiles --clean (cron weekly)** | Bursty IO | Same | Same |
| **Steam shader pre-cache / anti-cheat services** | GPU compile bursts | GPU busy % ramps | GPU sysfs |
| **VM live migration / Proxmox PBS backup** | Heavy network + disk | Sustained | NIC pps + io.pressure |
| **A long-lived SSH `tmux attach` with `htop` running** | ~2–4 % CPU, but otherwise idle | Pure CPU%; loadavg stable | Tolerable — well below thermal noise |

### 4.1 The critical insight for false-positives

There is no purely *generic* signal that catches ZFS scrub or mdadm resync without a domain-aware probe. ventd's calibration must therefore couple a generic-signal idle predicate (PSI + C-state) with a small **structural-state allowlist** that explicitly checks `/proc/mdstat`, `/proc/spl/kstat/zfs/*/state`, and `btrfs scrub status -d` style probes. This is the homelab-specific idle gate.

---

## 5. False-negative (looks busy but is idle) catalog

False-negatives just delay calibration; they're not dangerous, but they mean ventd never gets around to calibrating, which is also bad.

| Scenario | What looks busy | Why it's actually idle | Mitigation |
|---|---|---|---|
| `kworker/u32:1+events_unbound` post-resume burst | loadavg jumps to 4 for ~30 s | Just async finalization | Use 5-min window, not 1-min |
| `systemd-tmpfiles --clean` weekly | Brief I/O spike | 30–120 s, then clean | Use 5-min PSI averages |
| Hourly `cron @hourly` jobs | ~1–10 s CPU burst | Transient | 5-min window |
| ZFS ARC trim / metaslab rotation | Brief ZFS kthread activity | Internal | 5-min window |
| Network discovery broadcasts (mDNS, NetBIOS) | A few packets/s | Idle | Filter packet-count threshold to > 100 pps |
| GDM / SDDM "manager-early" session | Logind reports `IdleHint=false` permanently (systemd#34844) | No real user | Don't use logind alone |
| Clock-sync (chrony/ntpd) waking up | Brief CPU 0.1 % | Idle | Below threshold |

---

## 6. The Proxmox LXC trap, in detail

ventd will be deployed inside Proxmox LXC containers (homelab community standard). Three independent failure modes affect the idle gate inside LXC:

1. **`/proc/loadavg` discrepancy.** Confirmed in `lxc/lxc#4372` (2023), reproduced in pi-hole/pi-hole#5565, Zabbix forum #473153, and Proxmox forum thread 137529: with lxcfs `--enable-loadavg`, `cat /proc/loadavg` returns the container's load (computed by lxcfs itself from `/proc/<pid>/status` walking — see lxc/lxcfs `src/proc_loadavg.c::refresh_load`), but `getloadavg(3)` from glibc bypasses the file and returns the host's value. **ventd must read the file directly, never use `getloadavg`.** And even then the value is approximate.
2. **`/proc/stat` partial virtualization.** lxcfs filters by cgroup cpuset, but iowait inside a container is meaningless.
3. **`/sys/devices/system/cpu/cpu*/cpuidle`** is **not** virtualized by lxcfs. Reading it inside an LXC sees host-wide C-state residency, which is wrong for "this container is idle" but actually right for ventd's purpose ("is the *machine* idle so the thermal probe is valid?"). **This is a feature, not a bug, for fan control.** ventd controls hardware fans, which serve the host, so host-level idle is what matters.
4. **PSI inside cgroup v2** (`/sys/fs/cgroup/cpu.pressure` from the container's view) gives the *container's* pressure, but again ventd cares about *host* pressure to decide whether a thermal probe is safe. **ventd should read host PSI from `/proc/pressure/cpu` if it is running on the host or has host /proc bind-mounted; if it is in an unprivileged LXC and `/proc/pressure/*` shows the container's view, ventd should refuse to calibrate** because it cannot see whether the host is doing other work.

**Design implication:** ventd should detect "am I in a container?" via standard checks (presence of `/proc/1/cgroup` containing `lxc`/`docker`, `systemd-detect-virt --container`, or `/.dockerenv`) and:
- If running on bare metal or in a KVM/VM guest with full /proc access ⇒ run calibration normally.
- If running in an unprivileged LXC ⇒ **refuse calibration and log "ventd cannot reliably gate calibration in an unprivileged LXC; install ventd on the Proxmox host instead."** This matches ventd's actual deployment model (fan PWM is a host-only resource via hwmon).

---

## 7. The recommended ventd idle predicate

The predicate is a *compound, multi-signal, hysteresis-protected, durability-gated* rule. It is named `idle_enough_for_envelope_c()` and returns `(bool, reason string)`.

### 7.1 Hard preconditions (ANY ⇒ refuse)

```
1. Battery present AND on battery power:
     /sys/class/power_supply/AC/online == "0"
     OR /sys/class/power_supply/BAT*/status == "Discharging"
   ⇒ REFUSE. Reason: "on_battery"

2. Container detection:
     systemd-detect-virt --container ∈ {lxc, lxc-libvirt, docker, podman, …}
     AND /proc/pressure/cpu represents container's cgroup, not host
   ⇒ REFUSE. Reason: "unprivileged_container"

3. Storage-subsystem busy:
     /proc/mdstat contains "recovery =" or "resync =" or "check ="
     OR ZFS scrub active: any pool's "scan: scrub in progress" via
        parsing /proc/spl/kstat/zfs/*/state if module loaded
     OR BTRFS scrub: any /sys/fs/btrfs/*/devinfo/*/scrub_in_progress == 1
   ⇒ REFUSE. Reason: "storage_maintenance"

4. Process-name blocklist active:
     Walk /proc/[pid]/comm for any of:
       rsync, restic, borg, duplicity, pbs-backup,
       plex-transcoder, "Plex Media Scanner", jellyfin-ffmpeg, ffmpeg,
       handbrakecli, x265, x264, makeflags, make,
       apt, dpkg, dnf, rpm, pacman, yay, paru, zypper,
       updatedb, plocate-updatedb, mlocate,
       smartctl (with active --test running),
       fio, stress-ng, sysbench
   ⇒ REFUSE. Reason: "blocked_process:<name>"

5. Recent boot:
     /proc/uptime first field < 600 seconds (10 min)
   ⇒ REFUSE. Reason: "boot_warmup"

6. Recent suspend/resume:
     systemctl show systemd-suspend.service -p ActiveEnterTimestamp
     less than 600 s ago
   ⇒ REFUSE. Reason: "post_resume"
```

### 7.2 Primary signal block (ALL must pass)

```
P1. CPU pressure (PSI):
    /proc/pressure/cpu  some avg60  ≤ 1.00 %
    AND                 some avg300 ≤ 0.80 %
P2. I/O pressure (PSI):
    /proc/pressure/io   some avg60  ≤ 5.00 %
    AND                 some avg300 ≤ 3.00 %
P3. Memory pressure:
    /proc/pressure/memory full avg60 ≤ 0.50 %
```

Justification for thresholds: `joedefen/psistat` recommends ≥10 % "some" stalls as the lowest *actionable* threshold for performance degradation; we are a full order of magnitude below that. A truly idle desktop/NAS sits at < 0.1 % for `cpu.some avg60` and < 1 % for `io.some avg60`; the 1 % / 5 % bounds give margin for the harmless background noise listed in §5 without admitting the real workloads in §4.

### 7.3 Fallback signal block (used when PSI unavailable, e.g. RHEL 8 default)

```
F1. CPU utilization (1-second sample, averaged over 60 s):
    Across all CPUs: 1.0 - idle_jiffies/total_jiffies ≤ 5 %
F2. Deep C-state residency (Δt = 60 s):
    Σ over CPUs, Σ over states ≥ state2 of (time_us delta)
      / (60_000_000 × ncpus)  ≥ 0.85
F3. /proc/loadavg (read directly, do NOT use getloadavg):
    1-min ≤ ncpus × 0.10
    AND 5-min ≤ ncpus × 0.10
```

PSI is preferred whenever it is present (`stat /proc/pressure/cpu` succeeds and contains `some` line). Detection check: `/proc/pressure/cpu` exists AND first line begins with `some `.

### 7.4 Storage / network quiescence

```
Q1. Disk: aggregate sectors_read+sectors_written over 60 s
    ≤ 1 MB/s AND no individual device > 4 MB/s
Q2. Network: aggregate rx_packets+tx_packets over 60 s
    ≤ 200 pps (filters out mDNS/NetBIOS noise but still
    rejects active SMB serving, NFS, web traffic)
```

### 7.5 GPU quiescence (if GPUs present)

```
G1. AMD: every /sys/class/drm/card*/device/gpu_busy_percent ≤ 5 %
    averaged over 60 s
G2. NVIDIA: NVML utilization.gpu ≤ 5 % averaged over 60 s
    (read via /proc/driver/nvidia/gpus/*/information +
     /sys/bus/pci/drivers/nvidia/.../power/runtime_status, or via
     ventd's optional NVML helper subprocess to keep CGO_ENABLED=0)
```

### 7.6 Idle durability (the warm-up gate)

```
D1. The above predicate must have been continuously TRUE
    for at least 5 minutes before Envelope C may begin.
D2. ventd evaluates the predicate at 1 Hz and maintains a
    monotonic "idle_since" timestamp.
D3. Any FALSE evaluation resets idle_since to now.
D4. Calibration may begin when (now - idle_since) ≥ 300 s.
```

Rationale for 300 s: matches PSI's `avg300` window, ensuring a complete kernel-level averaging cycle has agreed with our 1 Hz polling. It also exceeds typical cron-burst durations (most @hourly jobs complete in < 60 s; weekly jobs < 300 s).

### 7.7 Cooperative deferral (when refused)

```
On refusal:
  - Log structured reason and timestamp.
  - Schedule next attempt with truncated exponential backoff:
      base = 60 s
      next = min(base × 2^n, 3600 s)  with n = consecutive refusals
      jitter ±20 %
  - On certain reasons, override the schedule:
      "on_battery"        → poll AC online via uevent, retry on AC plug-in
      "storage_maintenance" → poll mdstat / ZFS state every 300 s
      "boot_warmup"       → fixed retry at boot+600 s
      "post_resume"       → fixed retry at resume+600 s
  - Daily cap: at most 12 calibration attempts per 24 h to avoid
    pathological loops.
  - Operator override: a SIGUSR1 (or `ventd calibrate --force`)
    skips items in §7.2/7.3/7.4/7.5 but never §7.1 (battery & container
    refusals are absolute).
```

### 7.8 Pseudo-code reference

```go
// idle_enough_for_envelope_c returns (ok, reason).
// Reads are zero-allocation hot-path: pre-allocate scratch buffers.
func IdleEnough() (bool, string) {
    // Hard preconditions
    if onBattery()              { return false, "on_battery" }
    if inUnprivilegedContainer(){ return false, "unprivileged_container" }
    if storageMaintenance()     { return false, "storage_maintenance" }
    if p := blockedProcess(); p != "" { return false, "blocked_process:"+p }
    if uptime() < 600*time.Second { return false, "boot_warmup" }
    if sinceLastResume() < 600*time.Second { return false, "post_resume" }

    // Primary or fallback
    if havePSI() {
        if !psiOK() { return false, "psi_pressure" }
    } else {
        if !cpuStatOK() || !cpuidleOK() || !loadavgOK() {
            return false, "cpu_busy_fallback"
        }
    }

    if !diskQuiet() { return false, "disk_busy" }
    if !netQuiet()  { return false, "net_busy"  }
    if !gpuQuiet()  { return false, "gpu_busy"  }

    return true, "ok"
}
```

---

## 8. Cross-reference: kernel docs, mailing-list patches, primary sources

The recommended predicate is grounded in:

- **PSI**: docs.kernel.org/accounting/psi.html — defines `some`/`full` semantics and the 10/60/300 s windows; LWN.net/Articles/759658 (Johannes Weiner v2 patch cover letter, 2018) explicitly contrasts PSI with loadavg and motivates the 10 s shortest window.
- **cpuidle**: docs.kernel.org/admin-guide/pm/cpuidle.html — defines `time` and `usage` per state; `target-residency-us` characterizes when a state is "worth entering."
- **loadavg semantics**: kernel source `kernel/sched/loadavg.c`; Brendan Gregg, *Linux Load Averages: Solving the Mystery* (2017); confirms TASK_UNINTERRUPTIBLE inclusion, 5 s sample, EWMA constants.
- **lxcfs loadavg**: github.com/lxc/lxcfs `src/proc_loadavg.c::refresh_load` (walks `/proc/<pid>/status` for R/D states); github.com/lxc/lxc/issues/4372 (2023) documents Debian 12 regression where `getloadavg(3)` and `/proc/loadavg` disagree.
- **logind IdleHint**: freedesktop.org/software/systemd/man/logind.conf.html `IdleAction=`/`IdleActionSec=`; systemd source `src/login/logind-session.c` reads input device idle time; systemd issues #9622 (2018, tty/ssh sessions not tracked) and #34844 (2024, manager-early greeter session never sets IdleHint) confirm logind is unreliable headlessly.
- **Wayland**: gitlab.freedesktop.org/wayland/wayland-protocols `staging/ext-idle-notify-v1.xml` (introduced in Wayland-Protocols 1.27, 2022).
- **X11**: X Screen Saver extension (Xss) `XScreenSaverQueryInfo`.
- **ACPI battery**: Documentation/ABI/testing/sysfs-class-power; `/sys/class/power_supply/AC/online` is the canonical AC-presence flag.
- **fancontrol/pwmconfig**: lm-sensors/lm-sensors `prog/pwm/pwmconfig` (warning text, ~line 320).
- **fan2go**: github.com/markusressel/fan2go README, "minPwm/maxPwm" section.
- **Brendan Gregg, Linux performance signals**: brendangregg.com/blog/2017-08-08/linux-load-averages.html; brendangregg.com/Slides/LISA2019_Linux_Systems_Performance.pdf; brendangregg.com/Articles/Netflix_Linux_Perf_Analysis_60s.pdf.
- **Phoronix on PSI overhead**: phoronix.com/news/Wayland-Protocols-1.27 (idle-notify), and the Netdata write-up at netdata.cloud/blog/linux-load-average-myths-and-realities/.

---

## 9. Hardware-in-the-loop validation plan (for ventd's HIL fleet)

The predicate must be empirically validated, especially for false-positive ZFS scrub detection and Proxmox LXC behavior. The HIL fleet matches well:

| Fleet member | Test |
|---|---|
| **Proxmox host (5800X + RTX 3060)** | (a) Bare-metal install: verify all signals work end-to-end. (b) Spin up an unprivileged Debian 12 LXC; confirm `idle_enough_for_envelope_c()` returns `unprivileged_container`. (c) Run a scripted ZFS scrub on a pool; confirm refusal with `storage_maintenance`. (d) Trigger Plex hardware decode; confirm refusal with `gpu_busy` |
| **MiniPC (Celeron)** | Headless server profile. Validate predicate works with no graphical session: PSI + C-state path. Run for 24 h with a synthetic mixed workload (rsync hourly + cron updatedb + idle gaps); count true positives vs false positives across log |
| **13900K + RTX 4090 desktop (dual-boot)** | Validate GPU-quiescence path on NVIDIA without CGO. Validate logind IdleHint integration when desktop session is active. Compare predicate decisions while gaming vs while desk-locked vs while screen-saver. |
| **3 laptops** | (a) Hard-refuse on battery: unplug, confirm refusal within 1 s. (b) On AC, confirm calibration may proceed. (c) `systemd-inhibit --what=idle` from Firefox/Steam — confirm it does NOT block ventd (ventd's predicate is independent of logind's idle inhibitors, by design) |

The Proxmox host is the highest-value HIL target because it exercises the most distinctive failure modes (LXC, ZFS scrub, mixed GPU workload).

---

## 10. Summary recommendation

ventd should adopt a **PSI-primary, cpuidle-fallback, multi-signal compound predicate with structural-state allowlist and 5-minute durability gate**, hard-refusing on battery and inside unprivileged containers, with truncated-exponential-backoff retry. Loadavg is **not** the primary signal — PSI is. logind's IdleHint and Wayland's `ext-idle-notify-v1` are NOT used for the idle gate (they are unreliable headlessly and orthogonal to thermal idleness anyway), but they MAY be optionally consumed in a future v1.x release as an *additional* refusal source on desktops where the operator prefers calibration to defer to "user is at the keyboard."

This design is conservative — it errs strongly toward false-negatives (deferring calibration unnecessarily) over false-positives (running calibration on a busy system). Given that Envelope C only needs to succeed once per (fan × sensor × ambient-class) tuple over the lifetime of an install, conservatism is the right tradeoff.

---

## Artifact 2 — Spec-ready findings appendix block

### R5 — User idle gate (what "idle" means for calibration)
- **Defensible default(s):**
  - **Hard refusal preconditions (ANY ⇒ refuse):** `/sys/class/power_supply/AC/online == "0"` OR `/sys/class/power_supply/BAT*/status == "Discharging"`; container detection (`systemd-detect-virt --container` ∈ {lxc, docker, podman, …} with non-host PSI view); `/proc/mdstat` contains `recovery|resync|check =`; ZFS scrub active (`/proc/spl/kstat/zfs/*/state`); BTRFS scrub active; process-name blocklist hit (rsync, restic, borg, plex-transcoder, jellyfin-ffmpeg, ffmpeg, handbrakecli, apt, dpkg, dnf, rpm, pacman, updatedb, fio, stress-ng); uptime < 600 s; time-since-resume < 600 s.
  - **Primary signal (PSI, when `/proc/pressure/cpu` exists):** `cpu.some avg60 ≤ 1.00 %` AND `cpu.some avg300 ≤ 0.80 %` AND `io.some avg60 ≤ 5.00 %` AND `io.some avg300 ≤ 3.00 %` AND `memory.full avg60 ≤ 0.50 %`.
  - **Fallback (no PSI):** CPU non-idle ≤ 5 % over 60 s sampled at 1 Hz; deep-C-state residency fraction ≥ 0.85 over 60 s; `/proc/loadavg` read directly (NOT via `getloadavg(3)`) with 1-min and 5-min ≤ `0.10 × ncpus`.
  - **Quiescence:** disk Σ(sectors_read+sectors_written) ≤ 1 MB/s aggregate over 60 s, no device > 4 MB/s; NIC Σ(rx+tx)_packets ≤ 200 pps over 60 s; AMD `gpu_busy_percent` ≤ 5 % avg 60 s; NVIDIA NVML utilization.gpu ≤ 5 % avg 60 s.
  - **Durability:** predicate must be continuously TRUE for **≥ 300 s** before Envelope C may begin.
  - **Retry:** truncated exponential backoff base 60 s, cap 3600 s, ±20 % jitter, daily cap 12 attempts, immediate re-try on AC-plug uevent.
- **Citation(s):**
  - Linux PSI documentation: https://docs.kernel.org/accounting/psi.html (defines `some`/`full`, 10/60/300 s windows, kernel ≥4.20).
  - cpuidle subsystem: https://docs.kernel.org/admin-guide/pm/cpuidle.html (defines per-CPU per-state `time`/`usage` ground truth for idleness).
  - Brendan Gregg, *Linux Load Averages: Solving the Mystery* (2017): https://www.brendangregg.com/blog/2017-08-08/linux-load-averages.html (loadavg includes TASK_UNINTERRUPTIBLE; 5 s sampling; explains why loadavg alone is wrong).
  - lxcfs loadavg regression in Proxmox 8 / Debian 12: https://github.com/lxc/lxc/issues/4372 (`getloadavg(3)` returns host load even when `/proc/loadavg` returns container load).
  - lm-sensors `pwmconfig` warning establishing prior-art "operator is responsible for idle": https://github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/pwmconfig.
- **Reasoning summary:** PSI is the only kernel-native signal that combines low overhead, sub-15-second resolution, cgroup-correctness, and resistance to D-state ghost-load that corrupts loadavg, making it the right primary signal; cpuidle C-state residency is the fallback for older or RHEL-style kernels with PSI disabled. logind `IdleHint` and Wayland `ext-idle-notify-v1` are explicitly excluded from the idle gate because they are unreliable on headless NAS/server installs (systemd #9622 confirms tty/ssh sessions are untracked; #34844 confirms greeter sessions never set IdleHint) and orthogonal to thermal idleness anyway. A structural-state allowlist is required because no generic signal reliably catches ZFS scrub, mdadm resync, or BTRFS scrub at the level needed for a clean dT/dPWM measurement, and these are the canonical homelab/NAS workloads. ventd hard-refuses inside unprivileged Proxmox LXC because it cannot see host-level pressure from there, which is consistent with ventd's deployment model (fan PWM is a host-level resource via hwmon).
- **HIL-validation flag:** **Yes** —
  - **Proxmox host (5800X + RTX 3060)** runs the canonical scrub-vs-idle test: trigger ZFS scrub on a test pool, confirm `idle_enough_for_envelope_c` returns `storage_maintenance`; spawn unprivileged LXC, confirm `unprivileged_container` refusal; trigger Plex HW decode, confirm `gpu_busy`.
  - **MiniPC (Celeron)** runs the headless 24-hour soak: synthetic rsync + cron + idle gap mixture, log every predicate decision, post-process to count FP/FN.
  - **3 laptops** validate battery hard-refusal: unplug-AC events, expect refusal within 1 s of the AC uevent.
  - **13900K + RTX 4090 desktop** validates NVIDIA quiescence path without CGO and confirms predicate independence from systemd-inhibit (Firefox/Steam idle inhibitors must NOT block ventd).
- **Confidence:** **High** for PSI-primary path on Linux ≥4.20 with cgroup v2 (Debian 11+, Ubuntu 20.04+, Arch, Fedora 32+, Proxmox 7+, TrueNAS Scale, modern Unraid). **Medium** for the cpuidle-fallback path (less validated in literature). **Medium** for the structural-state allowlist — the regex set is comprehensive but inevitably non-exhaustive (e.g., custom NAS vendor backup daemons), so the spec must allow operator extension via config.
- **Spec ingestion target:** `spec/v0.6/r5-idle-gate.md`; predicate implementation lands in `internal/idle/predicate.go`; refusal-reason enum in `internal/idle/reason.go`; config knobs (`idle.psi_cpu_some_avg60_max`, `idle.durability_seconds`, `idle.process_blocklist`, `idle.daily_attempt_cap`) added to `spec/v0.6/config-schema.md`. Cross-reference patch numbers: this consumes R3 (Envelope C protocol) for the calibration handshake and feeds R7 (operator override CLI/`ventd calibrate --force`).