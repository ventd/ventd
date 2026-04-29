# spec-v0_5_3 — Envelope C/D probe + idle gate + load monitor

**Status:** DESIGN. Drafted 2026-04-29.
**Ships as:** v0.5.3 (third smart-mode behaviour patch).
**Depends on:** v0.5.1 catalog-less probe (channel enumeration), v0.5.2
polarity disambiguation (`WritePWM` helper, polarity-resolved channel
set), v0.5.1 spec-16 persistent state (KV + LogStore primitives).
**Consumed by:** v0.5.4 passive observation log (consumes envelope
step events from LogStore), v0.5.5 opportunistic active probing
(reuses idle gate primitives), v0.5.10 doctor (recovery surface
consumes envelope abort/pause events).
**References:**
- `spec-smart-mode.md` §5 (probe-write safety envelope), §6.4 (update
  mechanisms), §11 (patch sequence), §13 (cost projection).
- `Defining_Idle_for_ventd_Envelope_C_Calibration__A_Multi-Signal_Predicate_Design.md`
  (R5 — locked design-of-record for the idle predicate).
- `Envelope_C_Abort_Thresholds__Defensible_Numeric_Defaults_for_ventd_Bidirectional_PWM_Probe.md`
  (R4 — locked design-of-record for abort thresholds and class
  detection).
- `specs/spec-v0_5_2-polarity-disambiguation.md` (channel-set contract;
  reference only, not modified).
- `specs/spec-16-persistent-state.md` (storage substrate; reference
  only, not modified).
- `ventd-passive-observation-log-schema.md` (v0.5.4 schema; v0.5.3
  emits step events to a forward-compatible payload format).

---

## 1. Why this patch exists

v0.5.2 produced a fully classified channel set: every channel's
polarity is resolved to `normal`, `inverted`, or `phantom`. The daemon
has a safe `WritePWM` helper but no calibration data — every
controllable channel still has `last_calibration_envelope: ""` and no
response curve.

v0.5.3 is the patch that performs first-contact calibration. Its job
is to produce a usable response curve for every controllable channel
without burning the user's hardware in the process.

The patch ships three foundation pieces and the calibration logic
that consumes them:

1. **System class detection** (`internal/sysclass/`) — identifies
   which of R4's seven thresholds tables applies to this system,
   identifies an ambient-temperature reference sensor.
2. **Idle gate** (`internal/idle/`) — R5's full multi-signal predicate.
   Two evaluation modes: startup gate (5-minute durability) and
   runtime delta-check (baseline-relative, used mid-probe).
3. **Envelope C/D probe** (`internal/envelope/`) — bidirectional
   thermal-guarded PWM step probe with abort-and-rollback discipline,
   step-level resumability, and class-specific threshold lookup.

After v0.5.3 the daemon can: pick safe abort thresholds for any of
seven system classes; refuse to start calibration when the system
isn't idle; abort mid-probe on thermal slope, absolute temperature,
or new background activity; resume mid-probe across daemon restart;
fall back from Envelope C to D on abort; record every probe step to
spec-16 LogStore in the format v0.5.4 will consume.

## 1.1 Ground-up principle

Three foundation packages are shipped *once* and never reworked:

- **`internal/sysclass/`** has multiple downstream consumers (envelope
  thresholds, doctor internals, wizard summary, spec-15 experimental
  feature gating). Built standalone now, never moved later.
- **`internal/idle/`** ships the full R5 predicate (all 11
  components) — no deferrals. Future patches consume `idle.IsIdle()`
  via either entry point and never extend the predicate.
- **LogStore step-event format** is locked at v0.5.3 to match
  `ventd-passive-observation-log-schema.md`. v0.5.4 reads existing
  log records on its first run; no migration.

Per-channel calibration state in spec-16 KV is **resumable at step
granularity**: a daemon restart mid-probe picks up from the next
unstepped PWM value, not from PWM=baseline.

---

## 2. Scope

### 2.1 In scope

**System class detection (`internal/sysclass/`):**

- Seven classes per R4: HEDT-air, HEDT-AIO, mid-desktop, server,
  laptop, mini-PC, NAS-HDD.
- CPU-model regex on `/proc/cpuinfo` as primary signal, storage role
  (`/dev/sd*` rotational + `zpool`/`mdraid`/`btrfs`-pool presence)
  secondary, passive-cooling tertiary (`pwm*_enable` writability).
- BMC presence detection via `/dev/ipmi*` and `dmidecode -t 38`
  (System Management) for Class 4 server gating.
- EC-handshake gate for Class 5 laptop: write `pwm_enable=1`, observe
  whether RPM responds within 5 s; if not, refuse Envelope C with
  documented diagnostic.
- Ambient-sensor identification: prefer label-matched sensor
  (`ambient`, `intake`, `inlet`, `sio`, `systin` substring match);
  fall back to lowest-reading temp sensor at idle; final fallback to
  25 °C with diagnostic logged.
- Class persistence in spec-16 KV under `sysclass` namespace.

**Idle gate (`internal/idle/`):**

- Full R5 predicate per §7 of the research doc.
- Two API entry points:
  - `StartupGate(ctx) (bool, Reason)` — 5-minute durability requirement.
  - `RuntimeCheck(ctx, baseline Snapshot) (bool, Reason)` —
    instantaneous delta from probe-start baseline.
- Hard preconditions: battery refusal, container refusal, boot warmup
  (<600 s), post-resume warmup (<600 s), structural-state allowlist
  (mdstat recovery/resync/check, ZFS scrub via
  `/proc/spl/kstat/zfs/*/state`, BTRFS scrub via sysfs), process-name
  blocklist (~25 names per R5 §7.1).
- Primary signal: PSI (`cpu.some avg60 ≤ 1%`,
  `cpu.some avg300 ≤ 0.8%`, `io.some avg60 ≤ 5%`, `io.some avg300 ≤
  3%`, `memory.full avg60 ≤ 0.5%`).
- Fallback signal (no PSI): `/proc/stat` CPU non-idle ≤ 5%, deep-C-
  state residency ≥ 0.85, `/proc/loadavg` direct read (not
  `getloadavg(3)`) ≤ `0.10 × ncpus`.
- Quiescence: disk aggregate ≤ 1 MB/s and per-device ≤ 4 MB/s over
  60 s; net aggregate ≤ 200 pps over 60 s; AMD `gpu_busy_percent` ≤ 5%
  and NVIDIA NVML `utilization.gpu` ≤ 5% over 60 s.
- Durability: `StartupGate` requires predicate continuously TRUE for
  ≥ 300 s.
- Backoff scheduler: truncated-exponential base 60 s, cap 3600 s, ±20 %
  jitter, daily 12-attempt cap, AC-plug uevent immediate retry.
- Runtime baseline snapshot type: captures probe-start values for
  every signal so `RuntimeCheck` aborts only on *new* activity above
  delta thresholds.
- Operator override: `ventdctl calibrate --force` skips items 2–8
  per R5 §7.7 (hard preconditions never skippable).

**Envelope C/D probe (`internal/envelope/`):**

- Per-channel sequential probe state machine: `idle → probing →
  paused_user_idle | paused_thermal | paused_load | complete_C |
  complete_D | aborted`.
- Class-specific abort threshold lookup from R4 §10 table (full table
  shipped in code; see §6).
- dT/dt slope computation at 10 Hz sample rate; 1-sample hold-time
  for trip per R4 §4.1.
- T_abs absolute trip per R4 class table.
- Ambient-headroom precondition per Q6 logic before probe arms.
- Bidirectional PWM step sequence: from `baseline_pwm` step downward
  by configured step_size per class, holding each step for
  per-class duration to capture dT/dPWM.
- On Envelope C abort: ramp affected channel to PWM=255 (or current
  if higher) via `WritePWM`, log abort reason, fall back to Envelope D
  for the channel.
- Envelope D probe: same procedure but writes never go below
  baseline_pwm; only steps upward.
- Step-level resumability via spec-16 KV channel state + LogStore
  step events.
- Restoration discipline: `defer`-based baseline restore on every
  exit path (success, thermal abort, load abort, context cancel,
  panic).
- Class 4 server BMC gate: `[envelope.classes].server = false` by
  default if `/dev/ipmi*` present; user must set
  `allow_server_probe = true` to enable.
- Class 5 laptop EC handshake gate: probe handshake before any
  Envelope C step; failure marks channel `phantom_ec_unresponsive`
  and skips to Envelope D.

**Storage integration:**

- spec-16 KV `calibration.envelope.<channel_id>` per-channel state
  record (state, envelope, started_at, completed_step_count,
  abort_reason, last_update).
- spec-16 LogStore append per probe step: probe-start event, per-step
  observed values, abort/pause/complete events. Format compliant with
  `ventd-passive-observation-log-schema.md` §2.1 schema_version=1
  payload.
- Operator override flag persisted in `calibration.envelope.override`.

**Wizard surface:**

- Probe screen during Envelope C/D: per-channel state pills (pending,
  testing, paused, complete-C, complete-D, aborted), live step
  counter, estimated time remaining.
- "Background activity detected" pause modal: surfaces refusal reason,
  Resume / Cancel buttons.
- Monitor-only fallback: if every controllable channel aborts to
  Envelope D and D probe completes with insufficient curve coverage,
  wizard re-enters v0.5.1's monitor-only fork with
  `[diagnostics] envelope_universal_refusal`.

**Synthetic CI fixtures:**

- Per-class threshold lookup tests across all 7 classes.
- Mocked `/proc/pressure/*`, `/proc/stat`, `/proc/loadavg`,
  `/proc/uptime`, `/proc/mdstat`, `/proc/spl/kstat/zfs/*/state`,
  `/proc/[pid]/comm`, `/sys/class/power_supply/AC/online`,
  `/sys/class/drm/card*/device/gpu_busy_percent` for idle gate.
- Per-class fault-injection tests for envelope abort triggers
  (thermal slope, T_abs, load monitor).
- Resumability tests: kill mid-probe, restart, verify probe resumes
  from next unstepped PWM.
- LogStore step-event schema-compliance test against v0.5.4
  contract.

**Configuration surface:**

```toml
# /etc/ventd/ventd.toml additions
[envelope]
enabled = true

[envelope.classes]
hedt_air      = true
hedt_aio      = true
mid_desktop   = true
server        = false   # default-false per Q1 + R4 §10 BMC gate
laptop        = true
mini_pc       = true
nas_hdd       = true

[envelope.server]
allow_server_probe = false  # opt-in flag for Class 4

[idle]
psi_cpu_some_avg60_max = 1.00
psi_cpu_some_avg300_max = 0.80
psi_io_some_avg60_max = 5.00
psi_io_some_avg300_max = 3.00
psi_mem_full_avg60_max = 0.50
durability_seconds = 300
daily_attempt_cap = 12
process_blocklist_extra = []  # operator extension per R5 §9
```

### 2.2 Out of scope

- **Layer A response-curve learning beyond initial probe.** v0.5.5
  (opportunistic active probing) extends curve coverage; v0.5.4
  (passive observation) records ongoing observations. v0.5.3 produces
  the first curve only.
- **Layer B thermal-coupling map.** v0.5.7 territory.
- **Layer C marginal-benefit RLS.** v0.5.8 territory.
- **Confidence formula and gating.** v0.5.9 territory. v0.5.3 records
  per-channel `last_calibration_envelope` (C / D-after-C-abort /
  D-cold-start) but does not compute or expose confidence.
- **Doctor recovery surface.** v0.5.10 territory. v0.5.3 emits
  structured events to LogStore; doctor consumes them later.
- **NBFC integration.** spec-09 territory; if NBFC has claimed the EC,
  v0.5.3 falls back to the standard EC handshake test which will fail,
  classifying laptop channels as `phantom_ec_unresponsive`.
- **Drift-triggered re-probe.** v0.5.4+ detects polarity/curve drift;
  v0.5.3 probes once per channel per `started_at` epoch.
- **Polarity re-resolution.** v0.5.2 owns polarity. v0.5.3 inherits
  the resolved channel set.
- **Forward-migration from earlier calibration cache.** Existing
  `internal/calibration/cache` schema continues to work in parallel;
  migration deferred per spec-16 §10.3.

---

## 3. System class detection (`internal/sysclass/`)

### 3.1 Detection algorithm

Detection runs once at probe time (post-v0.5.1 channel enumeration,
post-v0.5.2 polarity disambiguation, pre-Envelope-C). Result persists
in spec-16 KV. Re-detection runs on wizard "Reset to initial setup"
path (per spec-v0_5_1 §5.3).

```go
package sysclass

type SystemClass int
const (
    ClassUnknown SystemClass = iota
    ClassHEDTAir
    ClassHEDTAIO
    ClassMidDesktop
    ClassServer
    ClassLaptop
    ClassMiniPC
    ClassNASHDD
)

type Detection struct {
    Class           SystemClass
    Evidence        []string         // ordered list of facts that drove classification
    Tjmax           float64          // °C, from Intel ARK / AMD Tctl regex
    AmbientSensor   AmbientSensor    // see §3.3
    BMCPresent      bool             // /dev/ipmi* exists OR dmidecode -t 38 reports SMS
    ECHandshakeOK   bool             // laptop only; nil for non-laptop
}

func Detect(ctx context.Context, probe *probe.Result) (*Detection, error)
```

### 3.2 Class detection rules

In order of precedence:

1. **NAS first** — if `≥1` block device has `rotational=1` AND
   (`zpool list` returns ≥1 pool OR `/proc/mdstat` has active arrays
   OR `/sys/fs/btrfs/` has non-empty UUID dirs) → `ClassNASHDD`.
2. **Mini-PC second** — if no controllable PWM channel found in v0.5.1
   probe (passive cooling), AND CPU model regex matches N100/N150/
   N305/J-series → `ClassMiniPC`. (The no-PWM mini-PC is the degenerate
   case; with PWM, fall through to mid-desktop unless laptop matches.)
3. **Laptop third** — battery present
   (`/sys/class/power_supply/BAT*` exists), OR DMI chassis-type matches
   `Notebook/Laptop/Sub-Notebook/Convertible` → `ClassLaptop`.
4. **Server fourth** — BMC present (per `Detection.BMCPresent`), OR
   CPU model regex matches Xeon Platinum/Gold, EPYC, Threadripper-Pro
   → `ClassServer`.
5. **HEDT-AIO fifth** — CPU model regex matches HEDT (13900K/14900K/
   7950X/9950X/9950X3D), AND any controllable channel has a
   liquid-cooler hint (NZXT/Corsair/EK USB-HID present, OR
   sensor label matches `coolant`/`pump`/`liquid`) → `ClassHEDTAIO`.
6. **HEDT-Air sixth** — CPU model regex matches HEDT, AND no AIO
   indicators → `ClassHEDTAir`.
7. **Mid-desktop fallback** — any other configuration with controllable
   PWM channels → `ClassMidDesktop`.
8. **Unknown** — fallback if no class matched (rare; e.g. unknown CPU
   model, no PWM, no battery, no BMC, no storage). Treated identically
   to mini-PC for safety: the most-conservative thresholds apply.

### 3.3 Ambient sensor identification

```go
type AmbientSensor struct {
    Source          AmbientSource     // Labeled | LowestAtIdle | Fallback25C
    SensorPath      string            // empty for Fallback25C
    SensorLabel     string            // empty for Fallback25C
    Reading         float64           // °C at probe-start
}

type AmbientSource int
const (
    AmbientLabeled       AmbientSource = iota  // explicit ambient/intake/inlet/sio/systin label
    AmbientLowestAtIdle                        // heuristic: lowest temp sensor after R5 5-min idle
    AmbientFallback25C                         // no sensors enumerable; assume 25 °C
)
```

Resolution order at probe-start:

1. Walk all enumerated `temp*_input` sensors. If any sensor's
   `temp*_label` matches case-insensitive substrings `ambient`,
   `intake`, `inlet`, `sio`, `systin` → use that sensor.
2. Otherwise: apply the **admissibility filter** (below) to all
   enumerated temp sensors, then use `min(reading)` across the
   surviving candidates after the R5 startup gate's 5-minute
   durability has elapsed.
3. Otherwise: assume 25 °C, log diagnostic
   `AMBIENT-FALLBACK-25C-NO-SENSORS`.

**Admissibility filter for the lowest-at-idle heuristic:**

Exclude any sensor whose `temp*_label` contains (case-insensitive
substring match) any of:

- `package`, `junction`, `vrm`, `drivetemp`
- `cpu`, `gpu`, `core`, `tdie`, `tctl`, `tccd`
- `coolant`, `pump`, `liquid` (AIO sensors are not ambient)
- `nvme`, `ssd`, `hdd` (storage temps run above ambient)

Sensors without a `temp*_label` are admissible (motherboard SIO
sensors typically lack labels and represent chassis temperature
loosely). If filtering produces zero candidates, fall through to
step 3 (Fallback25C) — never use a labeled CPU/GPU sensor as
ambient.

This filter prevents the "quiet idle 5800X package at 40 °C wins as
ambient" failure mode where the heuristic would otherwise pick the
warmest sensor that happens to also be the lowest at the moment.

Refusal cases (Envelope C refuses to begin):

- `Reading < 10 °C` → `AMBIENT-IMPLAUSIBLE-TOO-COLD`. Sensor likely
  broken or system in unusual environment.
- `Reading > 50 °C` → `AMBIENT-IMPLAUSIBLE-TOO-HOT`. System cannot
  safely calibrate regardless of class.

Class-specific headroom check then applies per R4 §6:

| Class | Min `(Tjmax − ambient)` headroom |
|---|---|
| HEDT-air, HEDT-AIO | ≥ 60 °C |
| Mid-desktop, laptop, mini-PC | ≥ 55 °C |
| Server | ≥ 50 °C |
| NAS-HDD | n/a (per-drive `mfg_max − 10 °C` derate, formula in §6) |

### 3.4 Persistence

```yaml
sysclass:
  schema_version: 1
  detection:
    class: "hedt_aio"
    evidence:
      - "cpu_model_regex:13900K"
      - "aio_hint:label_match:coolant"
    tjmax: 100.0
    ambient:
      source: "labeled"
      sensor_path: "/sys/class/hwmon/hwmon2/temp3_input"
      sensor_label: "SYSTIN"
      reading: 28.2
    bmc_present: false
    ec_handshake_ok: null   # non-laptop
    detected_at: "2026-04-29T..."
```

---

## 4. Idle gate (`internal/idle/`)

### 4.1 API shape

```go
package idle

type Reason string  // structured: "on_battery", "psi_pressure", "blocked_process:rsync", etc.

type Snapshot struct {
    Timestamp        time.Time
    PSI              PSIReadings    // empty if unavailable
    CPUStat          CPUStat
    LoadAvg          [3]float64
    DiskBytesPerSec  uint64
    NetPPS           uint64
    GPUBusyPercent   map[string]float64   // per-device; AMD + NVIDIA
    Processes        map[string]int       // blocked-process name → count at snapshot time
    StructuralFlags  StructuralFlags      // mdraid/zfs/btrfs activity bitfield
}

func StartupGate(ctx context.Context) (bool, Reason, *Snapshot)
func RuntimeCheck(ctx context.Context, baseline *Snapshot) (bool, Reason)

type Predicate interface {
    StartupGate(ctx context.Context) (bool, Reason, *Snapshot)
    RuntimeCheck(ctx context.Context, baseline *Snapshot) (bool, Reason)
}
```

`StartupGate` returns `(true, "ok", snapshot)` only after the
predicate has been continuously TRUE for ≥ 300 s. The returned
`*Snapshot` is the probe-start baseline that callers pass to
`RuntimeCheck` for the duration of the probe.

`RuntimeCheck` evaluates the predicate instantaneously and compares
against `baseline`. Aborts only on:

- A new hard precondition becoming true (battery unplugged → battery
  refusal; new structural-state activity; new blocked process appeared
  in the process table that was NOT in `baseline.Processes`).
- PSI pressure increase: `current.psi.cpu.some_avg10 > baseline + 5%`
  (delta gate; baseline jitter tolerated).
- GPU busy increase: `current.gpu_busy_percent > baseline + 10%`.
- Disk/net rate increase by >2× over baseline.

PSI `avg10` is used at runtime (not `avg60`) because mid-probe abort
must react fast — 60 s averaging is too slow for protecting an
in-flight PWM step.

### 4.2 Hard preconditions (R5 §7.1)

`StartupGate` and `RuntimeCheck` both refuse if any of the following
is true:

1. Battery present AND on battery power.
2. Container detection: `systemd-detect-virt --container` returns
   non-`none` AND `/proc/pressure/cpu` reflects container view (not
   host view).
3. Storage maintenance: `/proc/mdstat` contains `recovery|resync|check
   =`, OR ZFS scrub active per `/proc/spl/kstat/zfs/*/state`, OR
   BTRFS scrub active per `/sys/fs/btrfs/<uuid>/devinfo/*/scrub_in_progress`.
4. Process-name blocklist hit (full list per R5 §7.1: rsync, restic,
   borg, duplicity, pbs-backup, plex-transcoder, "Plex Media Scanner",
   jellyfin-ffmpeg, ffmpeg, handbrakecli, x265, x264, makeflags, make,
   apt, dpkg, dnf, rpm, pacman, yay, paru, zypper, updatedb,
   plocate-updatedb, mlocate, smartctl, fio, stress-ng, sysbench,
   plus operator-extended list from
   `[idle].process_blocklist_extra`).
5. `/proc/uptime` first field < 600 s (boot warmup).
6. Time since last suspend/resume < 600 s (post-resume warmup; read
   from `systemctl show systemd-suspend.service -p
   ActiveEnterTimestamp`).

Operator override (`ventdctl calibrate --force`) skips items 2–6.
Items 1 (battery) is never skippable; items 2 (container) is never
skippable. Override reason persists in
`calibration.envelope.override`.

### 4.3 Primary signal (PSI)

Required when `/proc/pressure/cpu` exists AND begins with `some `.
Thresholds per `[idle]` config block (defaults from R5 §7.2):

```
cpu.some  avg60  ≤ 1.00 %    AND  avg300 ≤ 0.80 %
io.some   avg60  ≤ 5.00 %    AND  avg300 ≤ 3.00 %
memory.full avg60 ≤ 0.50 %
```

### 4.4 Fallback signal (no PSI)

Triggered only when `/proc/pressure/cpu` is absent or malformed.

```
F1. /proc/stat CPU non-idle ≤ 5% averaged over 60 s @ 1 Hz sample
F2. /sys/devices/system/cpu/cpu*/cpuidle/state*/time deep-state
    residency Σ ≥ 0.85 over 60 s
F3. /proc/loadavg 1-min and 5-min ≤ 0.10 × ncpus  (read direct, not
    via getloadavg(3) — see R5 §6 lxcfs trap)
```

### 4.5 Quiescence (R5 §7.4–7.5)

Disk: `/proc/diskstats` aggregate sectors_read+written ≤ 1 MB/s over
60 s, AND no individual device > 4 MB/s.

Network: `/proc/net/dev` aggregate rx+tx packets ≤ 200 pps over 60 s.

GPU (if present):
- AMD: every `/sys/class/drm/card*/device/gpu_busy_percent` ≤ 5%
  averaged over 60 s.
- NVIDIA: NVML `utilization.gpu` ≤ 5% averaged over 60 s. Read via
  the v0.5.2-shipped NVML symbol loader; CGO_ENABLED=0 maintained.

### 4.6 Durability + backoff

`StartupGate` maintains a monotonic `idle_since` timestamp. Any FALSE
predicate evaluation resets `idle_since = now`. Gate returns true only
when `now - idle_since ≥ 300 s`.

On refusal: schedule next attempt with `next = min(60 × 2^n, 3600) ±
20 % jitter` where n is consecutive refusals. Daily cap 12 attempts.

Override paths:
- `on_battery` → poll AC online via uevent, retry on AC plug-in
  (skips backoff scheduler).
- `storage_maintenance` → poll mdstat / ZFS state every 300 s.
- `boot_warmup` → fixed retry at boot+600 s.
- `post_resume` → fixed retry at resume+600 s.

---

## 5. Envelope C/D probe (`internal/envelope/`)

### 5.1 Per-channel state machine

```
       ┌─────┐
       │idle │
       └──┬──┘
          │ start
          ▼
    ┌──────────┐    user paused        ┌───────────────────┐
    │ probing  │ ◄────────────────────►│ paused_user_idle  │
    └─┬──┬──┬──┘    runtime-load       └───────────────────┘
      │  │  │       triggered          ┌───────────────────┐
      │  │  └─────────────────────────►│ paused_load       │
      │  │       thermal trip          └───────────────────┘
      │  └─────────────────────────────►┌───────────────────┐
      │                                 │ paused_thermal    │
      │                                 └─────────┬─────────┘
      │                                           │ class-specific
      │                                           ▼ rollback
      │                                 ┌───────────────────┐
      │                                 │ aborted_C         │
      │                                 └─────────┬─────────┘
      │                                           │ fall back
      │                                           ▼
      │                                 ┌───────────────────┐
      │                                 │ probing_D         │
      │ probe complete                  └───────────────────┘
      ▼
   ┌────────────┐                                 ┌───────────────────┐
   │ complete_C │  or                             │ complete_D        │
   └────────────┘                                 └───────────────────┘
```

Channel states are per-channel; multiple channels probe sequentially
(never in parallel — per spec-smart-mode §5.4 and R4 §4 risk model).

### 5.2 Envelope C procedure

```
Pre-conditions:
  - sysclass.Detect succeeded
  - polarity_resolved (per v0.5.2 contract)
  - StartupGate(ctx) → (true, _, baseline)
  - ambient_headroom_ok per §3.3 + R4 §6
  - For Class 5: ec_handshake_ok=true
  - For Class 4: [envelope.server].allow_server_probe=true OR
    BMCPresent=false

Step sequence (per channel):
  1. Snapshot baseline_pwm = ReadPWM(channel)
  2. baseline_temps = read all monitored sensors
  3. Persist KV: state=probing, started_at=now, envelope=C
  4. LogStore: emit ProbeStart event
  5. For step in class.PWMSteps[]:
     a. RuntimeCheck(baseline) → if abort, goto Pause/Abort
     b. WritePWM(channel, step.pwm)  -- via v0.5.2 helper
     c. LogStore: emit StepBegin event
     d. Hold for class.HoldSeconds, sampling temps at 10 Hz
     e. Trip-evaluate dT/dt and T_abs each sample
        - If dT/dt > class.dTdtThresh: thermal_abort
        - If T_abs > class.TAbsThresh: thermal_abort
     f. PWM-readback verification:
        pwm_actual = ReadPWM(channel)
        if abs(pwm_actual - step.pwm) > 4:
            classify channel "phantom_bmc_overrides"
            log BMC-OVERRIDE-DETECTED
            abort this channel to Envelope D
        (BMC/EC firmware override detection — Class 4 server,
         Class 5 laptop primarily; quantization tolerance of ±4
         covers hwmon driver fan_max scaling per R11.8.4)
     g. Compute step.dT_per_dPWM
     h. LogStore: emit StepEnd event with observed values
        (include pwm_actual alongside pwm_target)
     i. Persist KV: completed_step_count++
  6. WritePWM(channel, baseline_pwm)  -- restore
  7. LogStore: emit ProbeComplete event with envelope=C
  8. Persist KV: state=complete_C, last_calibration_envelope=C

On thermal_abort:
  1. WritePWM(channel, 255)  -- max cooling
  2. LogStore: emit ProbeAbort event with reason=thermal_slope|thermal_absolute
  3. Persist KV: state=aborted_C, abort_reason=...
  4. Transition to Envelope D for this channel

On runtime-load abort:
  1. WritePWM(channel, max(baseline_pwm, current_pwm))
  2. LogStore: emit ProbePause event with reason=load
  3. Persist KV: state=paused_load
  4. Wait for user Resume in wizard, or auto-resume after StartupGate
     re-passes
  5. On resume: re-evaluate StartupGate → fresh baseline → continue
     from next unstepped PWM

On user pause:
  1. Identical to runtime-load abort except reason=user_idle
```

### 5.3 Envelope D procedure

Identical to Envelope C step sequence with one constraint:

```
WritePWM(channel, step.pwm) is permitted ONLY when
  step.pwm >= baseline_pwm
```

Class step tables include both ascending (Envelope D usable) and
descending (Envelope C only) ranges. Envelope D iterates ascending
steps only.

On Envelope D completion: state=complete_D,
last_calibration_envelope = D-after-C-abort | D-cold-start.

If ALL channels reach complete_D with insufficient curve coverage
(e.g. starting baseline already at PWM=255 → no upward steps
possible): wizard re-enters monitor-only fork per spec-v0_5_1 §5.1
with diagnostic `ENVELOPE-UNIVERSAL-D-INSUFFICIENT`.

### 5.4 Restoration discipline

Every probe procedure uses `defer` for baseline restore on every
exit path:

```go
func (p *Prober) probeChannel(ctx context.Context, c *Channel) (err error) {
    baseline, readErr := c.ReadPWM(ctx)
    if readErr != nil {
        return readErr
    }

    defer func() {
        if restoreErr := c.WritePWM(ctx, baseline); restoreErr != nil {
            // Log; do not overwrite primary err
            slog.Error("envelope.restore_failed", ...)
        }
    }()

    // ... probe logic ...
}
```

Subtest verifies that fault injection at each probe step leaves the
channel at its baseline value.

### 5.5 LogStore step-event format

Forward-compatible with `ventd-passive-observation-log-schema.md`
§2.1 Record shape. v0.5.3 emits these event types:

```
event_type: probe_start | step_begin | step_end | probe_pause |
            probe_resume | probe_abort | probe_complete

payload (msgpack-encoded):
  schema_version: 1
  channel_id: "hwmon3:pwm1"
  envelope: "C" | "D"
  event_type: <as above>
  timestamp_ns: int64
  pwm_target: uint16            // 0-255 (uint16 for header alignment)
  pwm_actual: uint16
  temps: map<sensor_id, float64>
  rpm: uint32
  controller_state: 5           // probing per schema doc §2.2
  event_flags: uint32           // per schema doc §2.3 bits
  abort_reason: string          // empty unless event_type=probe_abort
```

Event-flag bits set by v0.5.3 (already reserved by schema doc §2.3):

- bit 4: `ENVELOPE_C_ABORT` (set on probe_abort during C probe)
- bit 5: `ENVELOPE_D_FALLBACK` (set on probe_complete with envelope=D
  after a C-abort)
- bit 6: `IDLE_GATE_REFUSED` (set on probe_pause with reason=load)

### 5.6 Persistence shape (KV)

```yaml
calibration:
  envelope:
    schema_version: 1
    override:
      force_skip_softgates: false
      forced_at: ""             # set by --force CLI
    channels:
      "hwmon3:pwm1":
        state: "complete_C"     # per state machine §5.1
        envelope: "C"
        started_at: "2026-04-29T..."
        completed_step_count: 7
        baseline_pwm: 180
        last_step_pwm: 60
        abort_reason: ""
        last_update: "2026-04-29T..."
        last_calibration_envelope: "C"
```

Step-level data lives in LogStore (not KV). Restart-resume reads KV
for state + started_at, then reads LogStore filtered by
`(channel_id, started_at)` to reconstruct completed-step list, then
resumes from next unstepped PWM.

### 5.7 WritePWM callback plumbing

The v0.5.2 `polarity.WritePWM` helper takes a backend-write callback:

```go
func WritePWM(ch *probe.ControllableChannel, value uint8,
              fn func(uint8) error) error
```

The `fn` parameter performs the actual sysfs/NVML/IPMI write; the
helper applies polarity inversion before invoking `fn`. Envelope
probe must therefore capture a per-channel write callback at probe
construction and reuse it for every step.

**Pattern:**

```go
package envelope

type channelWriter struct {
    ch        *probe.ControllableChannel
    writeFunc func(uint8) error    // captured from HAL backend at probe-start
}

func (cw *channelWriter) Write(value uint8) error {
    return polarity.WritePWM(cw.ch, value, cw.writeFunc)
}

func (cw *channelWriter) Read() (uint8, error) {
    // ReadPWM does NOT need polarity inversion — the helper returns
    // the logical value as ventd's controller sees it.
    return polarity.ReadPWM(cw.ch)
}
```

Probe-start construction:

```go
func (p *Prober) buildChannelWriter(ch *probe.ControllableChannel) *channelWriter {
    return &channelWriter{
        ch:        ch,
        writeFunc: p.hal.WriteFunc(ch),  // backend-specific
    }
}
```

Backend `WriteFunc` returns the appropriate per-channel writer:
- hwmon: writes to `ch.PWMPath` via `os.WriteFile`.
- NVML: calls `nvmlDeviceSetFanSpeed_v2(device, fan_index, value*100/255)`.
- IPMI: invokes per-vendor backend's `SetPWM` via the existing
  vendor command interface.
- EC: writes to the channel's `/sys/class/hwmon/.../pwm*` (EC
  presents as hwmon).

This pattern means envelope code never directly touches sysfs/NVML/
IPMI — every write path goes through `polarity.WritePWM` + the
captured backend callback. RULE-ENVELOPE-01 enforces.

---

## 6. Class threshold table (R4 §10 ingestion)

Implemented as a const Go table in
`internal/envelope/thresholds.go`. Static data; no runtime mutation.

```go
type Thresholds struct {
    DTDtAbortCPerSec    float64    // °C/s; 0 means n/a (NAS uses minute-scale)
    DTDtAbortCPerMin    float64    // °C/min; 0 means n/a (CPU classes use s-scale)
    DTDtWindow          time.Duration  // averaging window for dT/dt
    TAbsOffsetBelowTjmax float64   // °C; class 7 uses absolute; see TAbsAbsolute
    TAbsAbsolute        float64    // °C; class 7 only; 0 for other classes
    AmbientHeadroomMin  float64    // °C; (Tjmax − T_ambient) ≥ this
    PWMSteps            []uint8    // descending steps for C, ascending for D
    HoldSeconds         time.Duration
    SampleHz            int        // 10 for CPU classes
    BMCGated            bool       // class 4 only
    ECHandshakeRequired bool       // class 5 only
}

var ClassThresholds = map[SystemClass]Thresholds{
    ClassHEDTAir: {
        DTDtAbortCPerSec: 2.0,
        TAbsOffsetBelowTjmax: 15.0,
        AmbientHeadroomMin: 60.0,
        PWMSteps: []uint8{180, 140, 110, 90, 70, 55, 40},
        HoldSeconds: 30 * time.Second,
        SampleHz: 10,
    },
    ClassHEDTAIO: {
        DTDtAbortCPerSec: 1.5,
        TAbsOffsetBelowTjmax: 15.0,
        AmbientHeadroomMin: 60.0,
        PWMSteps: []uint8{180, 140, 110, 90, 70, 55, 40},
        HoldSeconds: 45 * time.Second,  // longer for AIO coolant transient
        SampleHz: 10,
    },
    ClassMidDesktop: {
        DTDtAbortCPerSec: 1.5,
        TAbsOffsetBelowTjmax: 12.0,
        AmbientHeadroomMin: 55.0,
        PWMSteps: []uint8{180, 140, 110, 90, 70, 55, 40},
        HoldSeconds: 30 * time.Second,
        SampleHz: 10,
    },
    ClassServer: {
        DTDtAbortCPerSec: 1.0,
        TAbsOffsetBelowTjmax: 20.0,
        AmbientHeadroomMin: 50.0,
        PWMSteps: []uint8{200, 170, 140, 120, 100},  // narrower range, BMC may override
        HoldSeconds: 30 * time.Second,
        SampleHz: 10,
        BMCGated: true,
    },
    ClassLaptop: {
        DTDtAbortCPerSec: 2.0,
        TAbsOffsetBelowTjmax: 15.0,
        AmbientHeadroomMin: 55.0,
        PWMSteps: []uint8{180, 140, 110, 90, 70, 55, 40},
        HoldSeconds: 30 * time.Second,
        SampleHz: 10,
        ECHandshakeRequired: true,
    },
    ClassMiniPC: {
        DTDtAbortCPerSec: 1.0,
        TAbsOffsetBelowTjmax: 20.0,
        AmbientHeadroomMin: 55.0,
        PWMSteps: []uint8{180, 140, 110, 90, 70},  // small PWM range typical
        HoldSeconds: 30 * time.Second,
        SampleHz: 10,
    },
    ClassNASHDD: {
        DTDtAbortCPerSec: 0,            // n/a; minute-scale
        DTDtAbortCPerMin: 1.0,
        DTDtWindow: 5 * time.Minute,
        TAbsAbsolute: 50.0,             // derate per drive: min(50, mfg_max-10, ambient+15)
        AmbientHeadroomMin: 0,          // n/a; per-drive derate handles it
        PWMSteps: []uint8{200, 170, 140, 120, 100},
        HoldSeconds: 5 * time.Minute,   // HDD time constants are minutes
        SampleHz: 1,                    // sectors_read deltas at 1 Hz
    },
}
```

Class 7 NAS-HDD `TAbsAbsolute` is the per-drive computed limit:

```go
// At probe-start, for each HDD in pool:
T_abort_drive = min(50.0, drive.MfgMax - 10.0, ambient_reading + 15.0)

// Pool limit = lowest-rated drive's limit
T_abort_pool = min(T_abort_drive across all drives)
```

`drive.MfgMax` from datasheet defaults table per R4 §5; conservative
fallback 60 °C if drive model not in table.

---

## 7. Invariant bindings

Rules ship in three groups, bound 1:1 to subtests in their respective
test files. `tools/rulelint` enforces.

### 7.1 RULE-SYSCLASS-* (Class detection)

| Rule ID | Statement |
|---|---|
| `RULE-SYSCLASS-01` | `sysclass.Detect` MUST evaluate detection rules in the §3.2 precedence order. Subtest verifies via fixtures where multiple rules could match. |
| `RULE-SYSCLASS-02` | Class detection MUST persist to spec-16 KV under the `sysclass` namespace before any Envelope C step writes a PWM value. Subtest verifies KV write ordering. |
| `RULE-SYSCLASS-03` | Ambient sensor identification MUST follow the §3.3 fallback chain: labeled → lowest-at-idle → 25 °C. Subtest covers all three branches. |
| `RULE-SYSCLASS-04` | Ambient reading MUST refuse Envelope C if `<10 °C` or `>50 °C`. Subtest covers boundary cases. |
| `RULE-SYSCLASS-05` | Class 4 server with BMC present MUST refuse Envelope C unless `[envelope.server].allow_server_probe = true`. Subtest covers gated and gated-disabled paths. |
| `RULE-SYSCLASS-06` | Class 5 laptop MUST run EC handshake before Envelope C; failed handshake MUST classify channel as `phantom_ec_unresponsive` and skip to Envelope D. Subtest covers handshake success and failure. |
| `RULE-SYSCLASS-07` | Class detection result MUST include the full `Evidence` slice for diag bundle inclusion. Subtest verifies evidence completeness for each class. |

### 7.2 RULE-IDLE-* (Idle gate)

| Rule ID | Statement |
|---|---|
| `RULE-IDLE-01` | `StartupGate` MUST require predicate continuously TRUE for ≥ 300 s before returning `(true, ...)`. Subtest verifies durability state machine via injected clock. |
| `RULE-IDLE-02` | `StartupGate` AND `RuntimeCheck` MUST refuse on battery (`AC/online == 0` OR `BAT*/status == Discharging`). Override flag MUST NOT skip this. Subtest covers AC online, battery discharging, and override-attempt-rejected. |
| `RULE-IDLE-03` | `StartupGate` AND `RuntimeCheck` MUST refuse in unprivileged container. Override flag MUST NOT skip this. Subtest covers fixture `/proc/1/cgroup` with lxc/docker/kubepods entries. |
| `RULE-IDLE-04` | When `/proc/pressure/cpu` exists AND begins with `some `, PSI MUST be primary; cpuidle fallback MUST NOT be evaluated. Subtest covers PSI-present and PSI-absent fixtures. |
| `RULE-IDLE-05` | `/proc/loadavg` MUST be read directly via file read; `getloadavg(3)` MUST NOT be used. Subtest covers via build-tag exclusion check on glibc symbol. |
| `RULE-IDLE-06` | Process blocklist MUST include the R5 §7.1 base list AND any names from `[idle].process_blocklist_extra`. Subtest covers config-extension behaviour. |
| `RULE-IDLE-07` | `RuntimeCheck` MUST evaluate against the `baseline` Snapshot; refusal MUST occur only on new activity exceeding delta thresholds, not on baseline-resident activity. Subtest covers a fixture where Cinebench is running at probe-start and continues; predicate returns true. |
| `RULE-IDLE-08` | Refusal MUST schedule next attempt per backoff formula: `min(60 × 2^n, 3600) ± 20% jitter`, daily cap 12. Subtest verifies via injected clock. |
| `RULE-IDLE-09` | Operator override (`ventdctl calibrate --force`) MUST skip items 2–6 of §4.2 hard preconditions; items 1 (battery) and 2 (container) MUST never be skipped. Subtest covers override-permitted and override-rejected. |
| `RULE-IDLE-10` | `StartupGate` MUST return a `*Snapshot` capturing all signals at success time. Subtest verifies returned snapshot is non-nil and populated. |

### 7.3 RULE-ENVELOPE-* (Envelope C/D probe)

| Rule ID | Statement |
|---|---|
| `RULE-ENVELOPE-01` | Every PWM write during Envelope C/D MUST go through the v0.5.2 `WritePWM` helper. No direct sysfs writes. Subtest enforces via package-internal contract test. |
| `RULE-ENVELOPE-02` | Envelope C/D MUST restore baseline PWM on every exit path: success, thermal abort, load abort, context cancel, panic. Subtest injects faults at each probe step and asserts baseline restored. |
| `RULE-ENVELOPE-03` | Class threshold lookup MUST return values from §6 const table; runtime mutation MUST NOT occur. Subtest verifies table-driven behaviour for all 7 classes. |
| `RULE-ENVELOPE-04` | dT/dt trip MUST evaluate per sample at class-specified `SampleHz`; trip MUST fire on first sample exceeding threshold (1-sample hold). Subtest covers boundary cases for Classes 1, 3, 4, 6. |
| `RULE-ENVELOPE-05` | T_abs trip MUST fire when any monitored sensor reads `≥ Tjmax − TAbsOffsetBelowTjmax` (CPU classes) or `≥ TAbsAbsolute` derate (Class 7). Subtest covers per-class boundary. |
| `RULE-ENVELOPE-06` | Ambient-headroom precondition MUST evaluate before any PWM step; refusal MUST log `AMBIENT-HEADROOM-INSUFFICIENT` and skip envelope-C for that channel. Subtest covers per-class headroom failure. |
| `RULE-ENVELOPE-07` | Envelope C abort MUST transition state to `aborted_C` then `probing_D`; channel-D probe MUST NOT begin until channel-C state is persisted. Subtest verifies transition ordering via KV inspection. |
| `RULE-ENVELOPE-08` | Envelope D MUST refuse PWM writes below `baseline_pwm`. Subtest covers attempted descending-write rejection. |
| `RULE-ENVELOPE-09` | Per-channel KV state MUST persist after each completed step (state + completed_step_count + last_step_pwm). Daemon restart MUST resume from `next unstepped PWM`. Subtest covers kill-and-restart with simulated KV+LogStore. |
| `RULE-ENVELOPE-10` | LogStore step events MUST conform to v0.5.4 schema_version=1 payload (`ventd-passive-observation-log-schema.md` §2.1). Subtest verifies decoded events match schema field names + types. |
| `RULE-ENVELOPE-11` | Channels probed in parallel MUST NOT occur. Subtest verifies sequential per-channel iteration via concurrent-call rejection. |
| `RULE-ENVELOPE-12` | `paused_user_idle` AND `paused_load` states MUST resume by re-running `StartupGate` (fresh baseline) before continuing. Subtest covers pause-then-resume across simulated time. |
| `RULE-ENVELOPE-13` | Wizard MUST surface monitor-only fallback (`ENVELOPE-UNIVERSAL-D-INSUFFICIENT`) when all channels complete in `complete_D` with curve coverage below the per-channel minimum. Subtest covers all-D-with-narrow-coverage fixture. |
| `RULE-ENVELOPE-14` | After each probe step's hold period, PWM readback MUST be compared to PWM target. If `abs(pwm_actual - pwm_target) > 4`, channel MUST transition to `phantom_bmc_overrides` and abort to Envelope D for that channel. Subtest covers via fixture where mock backend returns a value outside the ±4 quantization tolerance. |

---

## 8. Failure modes enumerated

1. **Class detection misclassifies (e.g. NAS-with-SSDs detected as
   mid-desktop because no rotational drives).** Mid-desktop thresholds
   are conservative-enough for SSD-only systems; calibration completes
   safely. NAS-specific Class 7 treatment is missed but no damage
   occurs. Doctor surface (v0.5.10) flags class re-evaluation as a
   diagnostic action.

2. **Ambient sensor reads as 0.0 °C (broken sensor).** Caught by
   `<10 °C` refusal per RULE-SYSCLASS-04. User sees clear error;
   wizard offers reset path.

3. **Tjmax regex fails to identify CPU model.** Class falls back to
   `ClassUnknown`; thresholds applied are mini-PC's most-conservative.
   Calibration works but suboptimal. Diag bundle includes `cpu_model`
   string for forward catalog improvement.

4. **PSI present but `/proc/pressure/cpu` returns malformed data.**
   Idle gate falls back to cpuidle path. Subtest covers via fixture.

5. **NVML driver crashes mid-runtime-check.** GPU quiescence read
   fails; runtime check returns refusal with reason `gpu_check_failed`.
   Probe pauses, user resumes via wizard.

6. **Envelope C abort during the first PWM step.** Channel never
   completes a successful step before falling back. Envelope D begins
   from baseline_pwm with no descending data. D may produce limited
   curve; this is acceptable per spec-smart-mode §5.1 — partial data
   is better than none.

7. **All channels Envelope-C-abort.** Per RULE-ENVELOPE-13, system
   transitions to monitor-only with diagnostic. User sees clear UX:
   "ventd cannot safely calibrate this system. Showing temperatures
   only."

8. **Daemon killed mid-probe; KV write succeeded but LogStore append
   was buffered and lost.** spec-16 LogStore uses `O_APPEND | O_DSYNC`
   (RULE-STATE-03); kernel buffer is flushed before write returns.
   Last record may be torn (caught by CRC on read per RULE-STATE-04).
   Recovery: filtered LogStore replay reconstructs completed-step
   list; missing tail step re-runs.

9. **spec-16 KV write fails mid-probe (disk full).** State write
   fails; probe procedure logs the error and continues — in-memory
   state is correct, just not durable. On restart the channel
   re-probes from baseline. Doctor surfaces disk-full.

10. **Wizard window closed mid-probe.** Daemon continues probing in
    background (probe is daemon-side, not wizard-side). State persists
    via KV. User reopens wizard later, sees current probe state.

11. **User clicks Cancel during pause modal.** Daemon writes
    `state=aborted` to KV with reason `user_cancelled`, restores
    baseline PWM, exits probe. Channel marked `not_calibrated` until
    user re-enters wizard for that channel.

12. **System suspends mid-probe.** R5 post-resume warmup gates
    re-entry; daemon waits 600 s after resume before re-running
    `StartupGate`. State persists as `paused_thermal` (or whichever
    triggered last) across suspend.

13. **Class 5 laptop EC handshake passes initially, fails mid-probe
    (EC firmware silently took over).** Runtime check catches via PWM
    write succeeding but RPM not changing per the v0.5.2 phantom
    detection. Channel transitions to `aborted_C`, falls back to
    Envelope D, which also fails handshake → `phantom_ec_runtime`.
    Channel demoted to monitor-only at runtime.

14. **Class 4 server BMC kicks in mid-probe and ramps fans to 100%
    despite ventd's PWM writes.** Detected by the per-step PWM
    readback verification in §5.2 step 5f: if `pwm_actual !=
    pwm_target ± 4` after the hold period, channel transitions to
    `phantom_bmc_overrides`, aborts to Envelope D, logs
    `BMC-OVERRIDE-DETECTED`. RuntimeCheck idle predicate is
    independent of this detection — runtime check operates on system
    activity signals, not on PWM readback.

15. **Concurrent `StartupGate` calls (race condition).** Idle package
    uses sync.Once-per-snapshot semantics; subtest covers via parallel
    callers.

16. **`/proc/pressure/cpu` exists but is empty (cgroup with PSI but
    no recent activity).** Treated as PSI-present-but-zero; predicate
    passes. Subtest fixture covers.

17. **Operator passes `--force` flag with active battery.** Override
    rejects per RULE-IDLE-09 with diagnostic
    `OVERRIDE-NEVER-SKIPS-BATTERY`. Subtest covers.

18. **systemd-detect-virt returns non-zero exit (binary missing).**
    Idle package treats as `command_not_found`, falls through to file-
    based container detection. Subtest covers.

19. **`/proc/spl/kstat/zfs/*/state` is read-permission-denied.**
    Idle package treats as "ZFS not present" rather than refusing —
    avoids false-refusal on systems where ZFS is loaded but kstat
    permissions are tight. Subtest covers.

20. **Step-event LogStore write succeeds but msgpack encoding
    produced different bytes than v0.5.4 expects.** RULE-ENVELOPE-10
    schema-conformance test catches this; subtest decodes emitted
    payload with the v0.5.4 reader contract.

---

## 9. Validation criteria

### 9.1 Synthetic CI tests

All required, all must pass on every PR:

**sysclass:**
- 7 classes detection across CPU model fixtures.
- Ambient sensor: labeled / heuristic / 25C-fallback paths.
- Ambient refusal: `<10 °C`, `>50 °C` boundaries.
- BMC gating (Class 4 with and without `allow_server_probe`).
- EC handshake (Class 5 success and failure).

**idle:**
- PSI primary path with thresholds at boundary.
- cpuidle fallback path when `/proc/pressure/cpu` absent.
- All 6 hard preconditions (battery, container, mdraid, ZFS,
  blocklist, boot warmup, post-resume).
- Process-blocklist extension via config.
- Durability state machine across simulated 5-minute window.
- Backoff scheduler boundary (12-attempt cap, jitter applied).
- Override flag permits and rejects as specified.
- Snapshot baseline + RuntimeCheck delta evaluation: existing process
  doesn't trigger; new process triggers.
- lxcfs trap: `getloadavg(3)` exclusion verified.

**envelope:**
- Per-class threshold lookup across 7 classes.
- dT/dt trip at boundary value, single-sample firing.
- T_abs trip at boundary, including Class 7 derate formula.
- Ambient headroom refusal.
- Step state machine: idle → probing → complete_C, idle → probing →
  aborted_C → probing_D → complete_D.
- Restoration: fault injection at each step verifies baseline restored.
- Resumability: kill mid-probe, restart, verify resume from next step.
- Universal D-insufficient → monitor-only fallback.
- LogStore event format conforms to v0.5.4 schema.

### 9.2 Behavioural HIL

Fleet members required, in priority order:

1. **13900K + Arctic LF II 420 + 4090 (Class 2 HEDT-AIO)** —
   *Highest priority.* Runs full Envelope C probe; verifies abort
   triggers don't fire under normal calibration; verifies abort
   triggers DO fire under simulated runaway (artificial dT/dt via
   stress-ng + brief PWM=0 manual injection in synthetic test).
2. **Proxmox host 5800X + Noctua + RTX 3060 (Class 3 mid-desktop)** —
   Runs full probe. Verifies idle gate refuses during ZFS scrub and
   `apt update`. Verifies NVIDIA NVML quiescence path during low-Plex-
   traffic window.
3. **MiniPC Celeron (Class 6 mini-PC)** — Runs probe; expected outcome
   is most-conservative thresholds applied. Verifies probe completes
   on minimal hardware. Tests `Fallback25C` ambient path if no
   labeled sensor.
4. **TerraMaster F2-210 (Class 7 NAS)** — *Limited.* Validates per-drive
   `T_abort = min(50, mfg_max-10, ambient+15)` math via observable
   path; PWM probe path may be unavailable depending on hwmon
   inventory results. Documented as known limitation.
5. **Laptop (any one of three; Class 5)** — EC handshake test. If
   handshake passes, run full probe with Class 5 thresholds. If
   handshake fails, verify channel demoted correctly.

**Not required (no fleet member, synthetic-CI only):**
- Class 1 HEDT-air: no native fleet member. Synthetic table-driven
  verification only. Documented gap in TESTING.md.
- Class 4 Server: no native fleet member. Synthetic only. BMC gate
  prevents accidental damage on field-validation users.

### 9.3 Time-bound metric

**Per-channel probe time:**
- Classes 1, 2, 3, 5, 6: 7 steps × 30 s hold = 3.5 min plus
  baseline restore + step transitions ≈ 4 min total.
- Class 2 HEDT-AIO: 7 steps × 45 s hold = 5.25 min ≈ 6 min total.
- Class 4 Server: 5 steps × 30 s hold ≈ 3 min total.
- Class 7 NAS: 5 steps × 5 min hold ≈ 25 min total.

**Total system probe time** for typical desktop (4–6 controllable
channels): 16–24 min for HEDT/mid-desktop classes.

Wizard displays estimated remaining time as `(remaining_steps ×
hold_seconds) + restore_overhead × remaining_channels`.

---

## 10. PR sequencing

Two PRs, single v0.5.3 tag after PR-B merges (per Q7 lock-in).

### 10.1 PR-A — sysclass + idle foundation

Branch: `spec-v0_5_3-foundation`

Commits (ordered):
1. `feat(sysclass): system class detection with ambient heuristic`
2. `feat(sysclass): RULE-SYSCLASS-01..07 bindings`
3. `feat(idle): R5 multi-signal predicate (PSI primary, cpuidle fallback)`
4. `feat(idle): hard preconditions (battery, container, structural, blocklist)`
5. `feat(idle): durability state machine + backoff scheduler`
6. `feat(idle): RuntimeCheck baseline-delta evaluation`
7. `feat(idle): RULE-IDLE-01..10 bindings`
8. `test(sysclass): synthetic CI fixtures across 7 classes`
9. `test(idle): synthetic /proc/* fixtures + boundary tests`

Estimated cost: **$12–20** (Sonnet, mechanical R5/R4 transcription
against locked research; no design exploration).

Post-merge state: code present in main, no in-repo consumers, daemon
behaviour unchanged (PR-A's packages are imported only by tests).

No tag fired.

### 10.2 PR-B — envelope + wizard + tag

Branch: `spec-v0_5_3-envelope`

Commits (ordered):
1. `feat(envelope): per-channel state machine + class threshold table`
2. `feat(envelope): Envelope C bidirectional probe with abort triggers`
3. `feat(envelope): Envelope D ramp-up-only fallback`
4. `feat(envelope): step-level resumability via spec-16 KV + LogStore`
5. `feat(envelope): RULE-ENVELOPE-01..13 bindings`
6. `feat(wizard): probe screen + pause/resume modals`
7. `feat(wizard): monitor-only fallback when all channels reach D-insufficient`
8. `chore(config): [envelope] and [idle] config blocks`
9. `chore(apparmor): /proc/pressure/* + /sys/class/power_supply/* read access`
10. `test(envelope): synthetic per-class probe + resumability tests`

Estimated cost: **$12–20** (Sonnet; touches existing wizard
scaffolding so 50% pad applied per calibration rule).

Post-merge state: full Envelope C/D pipeline operational. Tag
`v0.5.3` after squash commit lands on main.

### 10.3 Cumulative cost

Total v0.5.3 estimated CC spend: **$24–40**. Within target range
($20–35 nominal, $40 ceiling per smart-mode-handoff cost discipline).

---

## 11. Estimated cost

- Spec drafting (chat): $0 (this document).
- CC implementation PR-A: $12–20.
- CC implementation PR-B: $12–20.
- HIL verification: post-merge, manual on Phoenix's fleet per §9.2.
- Total: **$24–40**.

---

## 12. Per-patch validation shape (per spec-smart-mode §12)

### 12.1 Synthetic CI

Per §9.1.

### 12.2 Behavioural HIL

Per §9.2. Highest-priority HIL: 13900K+LF II (Class 2) and Proxmox
host (Class 3).

### 12.3 Time-bound metric

Per §9.3. Wizard surfaces estimated remaining time.

---

## 13. Open questions resolved

The eight design questions surfaced at chat-start (2026-04-29):

1. **Which abort-threshold classes ship?** All 7 default-on; Class 4
   server default-refuses if BMC detected (`allow_server_probe = true`
   to enable). Per Q1 lock-in.
2. **Where does class detection live?** `internal/sysclass/`
   standalone package per Q2 lock-in.
3. **Idle gate scope?** Full R5 predicate, no deferrals, per Q3
   lock-in.
4. **Idle gate vs runtime monitor?** One package, two functions
   (`StartupGate`, `RuntimeCheck(baseline)`), per Q4 lock-in.
5. **Resumability state shape?** KV holds channel state + started_at;
   LogStore holds per-step events in v0.5.4-compatible format. Per
   Q5 lock-in.
6. **Ambient gate fallback?** Labeled → lowest-at-idle → 25 °C;
   refuse `<10 °C` or `>50 °C`. Per Q6 lock-in.
7. **Event-flag bits to v0.5.4 log?** Resolved by Q5: emit to LogStore
   directly with v0.5.4-compatible payload format from day one.
8. **PR sequencing?** Two PRs, single v0.5.3 tag after PR-B. Per Q7
   lock-in.

---

## 14. References

- `spec-smart-mode.md` §5 (probe-write safety envelope), §6.4 (update
  mechanisms), §11 (patch sequence).
- R4 research doc: abort thresholds.
- R5 research doc: idle predicate.
- `specs/spec-v0_5_2-polarity-disambiguation.md` (channel-set contract).
- `specs/spec-16-persistent-state.md` (storage substrate).
- `ventd-passive-observation-log-schema.md` (v0.5.4 schema; v0.5.3
  emits compatible payload).
