# ventd R19 — Battery-Aware Control on Portables

**Spec target:** v0.7.0 (ships alongside R18 acoustic spec)
**Status:** Research complete; locked R1–R18 state shape respected; additive-only changes.
**Bundle position:** Smaller scope than R16/R17/R18; consumes R18's `preset_weight_vector` parametricity.

---

## 1. `/sys/class/power_supply/` Reading Discipline

The kernel `power_supply` class (`Documentation/ABI/testing/sysfs-class-power`, `Documentation/power/power_supply_class.rst`) is the single canonical user-space interface for AC/battery state on Linux. All voltages are in µV, currents in µA, charges in µAh, energies in µWh. Every attribute is exposed both via sysfs and via uevent — the kernel emits `change` uevents on the parent device whenever a driver calls `power_supply_changed()`, which is the mechanism ventd should subscribe to.

**Field reliability ranking across vendors** (from kernel ABI doc and TLP/upower field experience):

| Field | Reliability | Notes |
|---|---|---|
| `type` | Universal | `"Battery"`, `"UPS"`, `"Mains"`, `"USB"`, `"Wireless"` — present on every supply since 2010 |
| `online` (Mains/USB) | High | 0/1; flickers during USB-PD renegotiation on some vendors |
| `status` (Battery) | High | `Charging`/`Discharging`/`Full`/`Not charging`/`Unknown` |
| `capacity` | High | 0–100 % integer; smoothed by driver |
| `current_now` | Medium | Microamps; **negative = discharging**, positive = charging. Some Lenovo/Dell EC firmwares report 0 for several seconds after AC transitions |
| `voltage_now` | High | Microvolts; rarely lies |
| `current_avg`, `power_now` | Vendor-dependent | `power_now` is on most ThinkPads (TLP relies on it), absent on many Chromebooks |
| `cycle_count`, `energy_full_design` | Reliable when present | Useful for doctor surface only |
| `charge_behaviour`, `charge_control_*_threshold` | Sparse | Out of scope for R19 (TLP territory) |

**Multi-battery handling.** Laptops with bay/auxiliary batteries (ThinkPad T-series with Ultrabay, dual-battery Latitudes) expose `BAT0`, `BAT1`, … under `/sys/class/power_supply/`. The system is on AC iff **any** `Mains` or `USB` (PD) supply has `online=1`; the system is on battery iff **all** batteries report `Discharging` and no `Mains/USB` is online. ventd must enumerate all entries matching `type ∈ {Battery, Mains, USB}` at startup and on `add`/`remove` udev events (battery hot-plug into a bay). Aggregate SOC = Σ`energy_now` / Σ`energy_full` across all `Battery` supplies; falling back to capacity-weighted average when energy fields are absent.

**USB-C PD negotiation states.** When a Type-C source negotiates, the kernel may briefly transition `online` 1→0→1 across the PD contract renegotiation (typically 50–500 ms). It may also report a `USB` supply with `online=1` but at a contracted current too low to charge (e.g. a 5 V/1.5 A phone charger plugged into a 65 W laptop). ventd should not treat a `USB`-type online supply as "AC" unless either (a) `current_max × voltage_max ≥ system_idle_power` (heuristic, ~15 W), or (b) any battery transitions to `Charging` within 2 s of the online transition. This avoids wrongly entering AC mode on a USB-PD trickle source that cannot actually sustain Performance preset.

**Dock detection.** Thunderbolt/USB-C docks present an additional `Mains` or `USB` supply node (often via `ucsi_acpi`) that may carry slightly different IDs than the wall brick. From a thermal-control standpoint, ventd treats *any* sufficient AC source identically; however, the doctor surface should record which supply triggered the transition (useful for diagnosing flicker on cheap docks).

**Poll cadence vs. uevent.** The kernel emits a `change` uevent on every state transition; ventd should subscribe to `NETLINK_KOBJECT_UEVENT` (subsystem filter `power_supply`) for transitions and additionally poll at a **slow, fixed cadence of 5 s** to (a) catch missed events on broken EC firmwares (cf. ACPI_WITHOUT_AC_EVENTS in laptop-mode-tools, which exists precisely because some vendors drop AC-adapter events) and (b) sample `current_now` for trend analysis. The 5 s poll is well below the AC↔battery smoothing window (see §2) and contributes negligible CPU/IO.

**Known broken vendor reporting:**
- Some HP and Dell EliteBooks (2018–2021 era) report `online=0` for ~1–3 s during S3/S0ix wake before the AC adapter is re-detected. ventd must suppress mode transitions for the first 5 s after a `pm-resume` signal (R12 already wires `/sys/power/state` observer for hwmon re-enumeration; we reuse it).
- Lenovo X1 Carbon Gen 9 has known intermittent 0-valued `current_now` on the first read after wake.
- ThinkPad bay batteries sometimes report `Unknown` status for several seconds during hot-add.

**UPower D-Bus alternative — rejected.** UPower (`org.freedesktop.UPower`) provides an aggregated `OnBattery` boolean and per-device properties via D-Bus. Rejection rationale, to be documented in `spec-non-goals.md`:

1. **Dependency surface.** Pulling in D-Bus introduces a runtime dependency on `dbus-broker`/`dbus-daemon` and either CGO bindings or a pure-Go D-Bus client (`godbus/dbus` adds ~6 kLOC and brings transitive `golang.org/x/...` deps). ventd's invariant is single-binary, CGO_ENABLED=0, no heavy deps.
2. **Availability.** UPower is not present on minimal server installs, container hosts, embedded MiniPCs, or SteamOS gamescope-only sessions. ventd must run on these.
3. **Information loss.** UPower abstracts away the per-supply current/voltage that R19's optimiser needs for power-budgeting decisions; we'd end up reading `/sys` anyway.
4. **AppArmor surface.** D-Bus access requires additional abstraction includes (`abstractions/dbus-session-strict`) and bus-name-level mediation that complicates the profile.

Reading sysfs directly is what TLP, laptop-mode-tools, and acpid-based scripts do; the path set is bounded and AppArmor-enumerable: `/sys/class/power_supply/*/{type,status,online,capacity,current_now,voltage_now,power_now,energy_now,energy_full,current_max,voltage_max}` (read-only).

---

## 2. AC↔Battery Transition Smoothing

The acoustic problem: a sudden 30 % PWM step on plug/unplug is audibly jarring even when the absolute level is moderate, because the human auditory system is highly sensitive to onset/offset envelope rate. From the psychoacoustic literature (Zwicker & Fastl, *Psychoacoustics — Facts and Models*, 2007, Chapter 8 on loudness and Chapter 10 on temporal effects; Schneider & Feldmann 2015 on fan noise psychoacoustic evaluation), the salient quantities are:

- **Just-noticeable difference (JND) for sound intensity**: ≈0.5–1.0 dB across most of the dynamic range (Scharine, USAARL HMDS Section 19/Ch. 11).
- **Fluctuation strength** peaks at ~4 Hz modulation; ramps slower than ~0.25 Hz are perceived as "level changes" rather than fluctuation.
- **Critical onset rate**: for broadband noise, ramp rates above ~3–5 dB/s are reliably detected as "changes" by naive listeners; below ~1 dB/s the change is essentially below the threshold of conscious noticing under normal cognitive load.

**ventd ramp budget design.** Translate dB/s into PWM-step-per-second using R18's calibrated PWM→dB curve (per-fan, per-platform; defaults to 0.4 dB per PWM% as a fleet-wide prior derived from the R18 acoustic measurements). Targeting **≤1 dB/s** during AC↔battery transitions yields ~2.5 PWM%/s, i.e. a 25 PWM-percentage transition takes ~10 s. We adopt:

- **Ramp budget**: 8 s nominal, 12 s maximum (configurable via `smart_mode.battery.ramp_seconds`, clamped 4–20).
- **Slew limit**: ≤2 PWM%/s during transitions, on top of R18's existing acoustic-objective slew limit. The transition smoother takes whichever is more conservative.
- **Direction asymmetry**: AC→battery (fan slows) ramps **faster** than battery→AC (fan speeds up), because (a) thermal headroom on battery is the constraint and we want to capture power savings quickly, and (b) downward ramps in fan noise are less perceptually salient than upward ramps (the auditory system attends to onsets). Default: 6 s down, 10 s up.

**Hysteresis design (USB-PD flicker resistance).**

State machine has three observed states: `AC`, `BAT`, `TRANSITIONING`. Transitions require:

1. **Debounce window**: an `online` change must persist for ≥3 s before the optimiser is informed. Sub-3-s flickers are absorbed without acoustic effect.
2. **Confirmation evidence**: AC→BAT requires either (a) all batteries `Discharging` for ≥2 consecutive samples, or (b) `online=0` on all `Mains`/`USB` supplies for ≥3 s. BAT→AC requires `online=1` AND (any battery `Charging` OR `Full`) for ≥2 s.
3. **Anti-thrash bound**: at most 1 mode flip per 30 s (configurable). If a flip has occurred within the bound and another would-be flip is detected, ventd stays in TRANSITIONING and re-evaluates after the bound elapses.
4. **Fallback for marginal PD**: if AC is online but no battery is charging within 5 s and SOC is decreasing, treat as BAT (this is the "phone charger plugged into laptop" case).

The 30-s anti-thrash bound is conservative compared to laptop-mode-tools' implicit ~10 s and TLP's event-driven (no debounce) model; ventd's longer bound is justified because a fan-curve switch is more user-perceptible than a CPU-governor switch.

---

## 3. Preset Overlay vs. New "Battery" Preset

**Recommendation: Battery as overlay (architecture (a)) with optional user-pinned override.**

R18 already shipped `preset_weight_vector` parametricity — a preset is no longer a hardcoded curve but a 4-element vector `(w_thermal, w_acoustic, w_power, w_responsiveness)` summed to 1.0 (the `w_power` weight is exactly the slot R18 reserved for R19). The overlay model multiplies the active preset's weight vector by a power-source modulation matrix:

```
on_battery: w_acoustic *= 1.15, w_power *= 4.0, w_thermal *= 0.95, then renormalize
on_ac:      identity (no-op)
```

Then renormalisation preserves the simplex constraint. The `w_power` weight, when non-zero, drives the optimiser toward setpoints that minimise integrated fan-power within the thermal envelope (see §6).

**Why overlay beats new-preset:**

1. **State-shape impact**: overlay adds a single 4-float `battery_modulation_vector` (16 bytes) plus a 1-byte `power_source` enum. New-preset would require a 4th `preset_weight_vector` slot in the locked preset table — additive, but it duplicates the user's mental model ("which of the 4 do I pick?") and forces R7 workload signatures to map into a 4-way space.
2. **UX coherence**: users select Silent/Balanced/Performance based on what they're *doing* (writing code, browsing, gaming, encoding). Whether they're plugged in is orthogonal to that intent. Forcing a 4th preset conflates the two axes.
3. **Composition**: overlay composes cleanly with R7 workload-derived preset selection — the workload picks the base preset, the overlay applies the power-source modulation on top. New-preset would force ambiguous precedence rules ("does the workload-detected Performance beat the auto-selected Battery?").
4. **Handheld override case**: handheld gaming users on Steam Deck-class hardware *do* sometimes want to pin "Performance" while on battery (accept battery cost for frame-rate). This is supported by the overlay model via a `--ignore-power-source` user flag, which sets `battery_modulation_vector := identity` for the session. No new preset needed; the existing Performance pin honours user intent.

**Auto-preset-switching interaction.** The overlay model is invariant under R7's auto-selection: workload signature → base preset → modulation → final weights. Plugging in does not change which base preset the workload triggered; it only removes the modulation. This matches user expectation (no surprise re-pinning when AC is connected).

**State machine.**
```
              [user pin]            [user pin cleared]
       ┌─────────────────────┐     ┌────────────────────┐
       │                     ▼     ▼                    │
   AUTO_OVERLAY ──── plug ───→ AC_OVERLAY ──── unplug ──→ AUTO_OVERLAY
       │                                                 │
       └── critical SOC ──→ CRITICAL_OVERRIDE ────────→ ─┘
                                  ▲
                                  └── SOC > threshold + hysteresis
```

Three states only; no preset duplication. The `CRITICAL_OVERRIDE` state forces a hardcoded silent-floor weight vector (see §5).

---

## 4. R7 Workload Signature × Battery State

R7 signatures hash `proc/comm` (and command-line tail per R7) to a 32-bit signature, then map signature → learned `preset_weight_vector` adjustment. The question: does the signature space need a battery-state dimension?

**Empirical premise.** "Compile under battery" and "compile on AC" *should* yield different fan curves: on AC we want short-duration boost to finish quickly; on battery we want sustained moderate cooling because thermal-throttle-induced stalls ruin perf-per-Wh. So the optimal point in weight-vector space genuinely differs.

**Naive solution: double the keyspace** to `(signature, power_source)`. Rejected because:
- It violates R7's locked state shape (the signature table has a fixed footprint per R7's RAM budget).
- Most signatures see one power source overwhelmingly; doubling wastes 50 %+ of the table.
- "Quadruple with critical-battery" makes this worse.

**Recommended solution: per-signature `battery_modifier` scalar.** Add a single `int8` field (range −64…+63, scaling factor ±0.5) to the existing R7 signature record, representing how much the global battery overlay should be amplified or attenuated for this workload. State-shape impact: R7's locked record gains 1 byte per signature; given R7's existing budget (256 signatures by default per R7's locked sizing), this is +256 bytes total — within R7's pre-allocated padding (R7 reserved 8 bytes/record padding for "future scalars"). **This is additive and respects the locked R1–R18 envelope.**

Algorithm:
```
final_vector = renormalize( base_preset
                          ⊙ (1 + sig.battery_modifier/128 · is_battery)
                          ⊙ battery_modulation_vector(power_source) )
```

Critical-battery state is *not* a fourth dimension — it short-circuits the whole stack and forces the override vector (see §5), so the signature modifier doesn't apply.

**Learning rule.** R7's existing online learning observes (workload, outcome) pairs. R19 extends the outcome to record `(power_source, integrated_fan_energy, throttle_events)`. The `battery_modifier` is updated only on battery samples, with a slow learning rate (R7-locked) so AC behaviour is not perturbed.

---

## 5. Critical-Battery Preset Override

Below a configurable SOC threshold, ventd forces a "minimum-cooling-while-not-throttling" override. Reference: laptop-mode-tools' `MINIMUM_BATTERY_CHARGE_PERCENT=3` (default; see `laptop-mode.conf(8)`) and `DISABLE_LAPTOP_MODE_ON_CRITICAL_BATTERY_LEVEL`, which disables aggressive features at battery-critical to give the user maximum time to find a charger.

**ventd policy:**

| SOC (default) | Behaviour |
|---|---|
| > 20 % | Normal battery overlay |
| 10–20 % | "Reserve" overlay: `w_power *= 1.5` further (more aggressive power saving) |
| 5–10 % | "Critical" override: lock to `w_power=0.7, w_thermal=0.3, w_acoustic=0, w_resp=0` — fan runs only enough to stay below passive trip point |
| < 5 % | Same as Critical, plus emit doctor warning |

Hysteresis: 2 % band on each threshold to prevent thrash if SOC oscillates around boundary during high-discharge bursts.

**UX.** ventd surfaces critical state via:
- `ventd doctor` displays a warning row when Critical/Reserve is active, with the SOC value and the override vector.
- A single `journald` log entry at `WARNING` level on entry/exit (rate-limited: at most one entry per state transition).
- No notifications, no D-Bus, no popups — that's desktop-environment territory and out of ventd's scope.

**User bypass.** Two paths:
- `ventd doctor override --ignore-critical-battery` (session flag; expires at next reboot or after configured TTL, default 1 h). The doctor surface explicitly logs that the user has bypassed.
- A persistent config knob `smart_mode.battery.critical_threshold_pct = 0` disables the feature entirely. Set in `/etc/ventd/config.toml`; documented as discouraged.

The CLI-flag-with-TTL pattern is preferred to a permanent override because the most common reason to bypass is "I'm running a 30-min benchmark and accept the cost" — a one-shot, time-bounded action.

---

## 6. `/sys/class/thermal` Trip-Point Integration

Per `Documentation/driver-api/thermal/sysfs-api.rst`, each thermal zone exposes:
```
/sys/class/thermal/thermal_zone[N]/
  type, temp, mode, policy, available_policies
  trip_point_[K]_temp     (millidegrees C, RO mostly)
  trip_point_[K]_type     ("critical" | "hot" | "passive" | "active[0-N]")
  trip_point_[K]_hyst     (RW, optional)
  cdev[M], cdev[M]_trip_point, cdev[M]_weight
```

For ventd's purposes:
- **Critical** trip = system-shutdown threshold; never approach. ventd reads it as the hard ceiling.
- **Hot** = vendor-defined emergency; treat as critical-minus.
- **Passive** = throttling kicks in (CPU frequency reduction). On battery, we want to operate **just below** this trip — staying cool wastes fan power; throttling wastes time-to-completion (and therefore Wh). The optimal setpoint on battery is `T_passive − margin` where margin accounts for sensor lag and PI overshoot (default 3 °C).
- **Active[K]** = fan-control trip points (firmware's intended fan-on threshold). On AC, ventd's setpoint sits below `active0`. On battery, ventd allows it to drift up toward the next-higher active trip but stays below `passive − margin`.

**Setpoint shift.** R19's PI controller setpoint is shifted by `+ΔT_battery` when on battery. Default `ΔT_battery = +5 °C`, clamped so that `setpoint ≤ T_passive − 3 °C`. This is the "stay just below the throttle line" strategy and is the single biggest fan-power saver on battery.

**Dynamic trip points (Intel TCC offset, charge-state-dependent thresholds).** Some Intel laptops modulate `passive` trip points via Thermal Control Circuit offset — `passive_temp` can change at runtime. ventd must:
1. Re-read all `trip_point_*_temp` values on every uevent from the thermal subsystem (the kernel emits `change` events on the thermal_zone kobject when policies update).
2. Additionally re-read on AC↔battery transitions (some firmwares lower `passive` on battery to discourage sustained boost).
3. Cap the setpoint shift after each re-read: `setpoint := min(target, T_passive_now − margin)`.

**Polling.** The thermal class supports netlink notifications (`/sys/class/thermal/thermal_zone[N]/`'s parent emits `change` uevents on trip crossings; the framework also exposes a netlink generic family). ventd reuses the existing R5 thermal polling loop and adds trip-point cache invalidation on receipt of any thermal uevent.

**AppArmor delta.** Add read access to `/sys/class/thermal/thermal_zone*/trip_point_*_{temp,type,hyst}` (these are already implicitly enumerable as part of the existing `thermal_zone*/temp` access; the path glob is unchanged in shape).

---

## 7. Steam Deck Specifics

**TDP gating.** The Steam Deck's APU TDP is user-controllable via GameScope's slider (default range 3–15 W, sometimes extended via Decky plugins). TDP is set via `/sys/class/hwmon/hwmon[X]/power[1-2]_cap` for the Mendocino/Van Gogh APU (amdgpu/k10temp or `amd_pmf` driver paths). ventd does **not** modify TDP — that's the user's compositor's job. ventd only reads it as a feed-forward signal: lower TDP → less heat to dissipate → relax the setpoint.

**jupiter-fan-control coexistence.** Valve's stock daemon (`jupiter-fan-control.service`, source mirrored at `Jovian-Experiments/jupiter-fan-control` on GitHub; original Valve repo private) is a Python daemon driven by `/usr/share/jupiter-fan-control/jupiter-fan-control-config.yaml`. It owns the fan via the `steamdeck` platform driver's hwmon attributes (`pwm1`, `pwm1_enable`, `fan1_target`, `fan1_input` — see the `drivers/platform/x86/steamdeck.c` patch series posted by Andrey Smirnov at lore.kernel.org/lkml, Feb 2022, registering ACPI device `VLV0100`).

ventd's policy on Steam Deck:
1. **Detection**: at startup, check (a) `/sys/devices/virtual/dmi/id/product_name` containing `Jupiter` or `Galileo` (LCD vs OLED Deck), AND (b) presence of the `steamdeck` hwmon node (`name == "steamdeck"`).
2. **Default behaviour**: `defer` mode. ventd does *not* take fan ownership; it runs as an observer and reports to `ventd doctor` what jupiter-fan-control is doing. This is the safe default and matches SteamOS's read-only-rootfs assumption.
3. **Coordination mode** (opt-in): if the user explicitly stops `jupiter-fan-control.service` and starts ventd with `--platform=steamdeck-coordinate`, ventd takes ownership. R19 does *not* ship this mode by default; documenting the path is sufficient.
4. **Never fight**: ventd must detect concurrent writes to `pwm1` (via reading back the value after a settle delay) and, if mismatched, re-enter `defer` mode and emit a doctor warning. This prevents the two daemons from oscillating.

**Handheld vs docked detection.** The Deck dock presents:
- A Mains supply via USB-PD on the dock connector.
- HDMI hot-plug visible via `/sys/class/drm/card[X]-HDMI*/status`.
- The `steamdeck` extcon driver emits role notifications (the `extcon-provider` interface in `steamdeck.c` reports USB Type-C connector events).

For thermal purposes, ventd treats "docked" as an AC mode hint: docked → likely longer session, slightly higher acoustic tolerance. But the primary signal remains the AC online state, not the dock state per se.

**SteamOS read-only rootfs.** SteamOS 3.x mounts `/usr` read-only (Btrfs subvolume). ventd installation must:
- Place the binary in `/var/lib/ventd/bin/` or `~/.local/bin/ventd` (writable) rather than `/usr/local/bin`.
- Use `systemctl --user` or `/etc/systemd/system/` (the latter is on a writable subvolume on SteamOS).
- Document the install via `steamos-readonly disable`/`enable` flow as an alternative for advanced users.

This is an installation concern, not a runtime concern; flag it in `INSTALL.md` rather than as runtime code.

---

## 8. Framework Specifics

Framework Laptop 13/16 EC firmware is a downstream of the Chromium EC project (`FrameworkComputer/EmbeddedController` on GitHub, forked from `chromium.googlesource.com/chromiumos/platform/ec`). The EC handles fan control natively; userspace access is via the `cros_ec_lpcs` kernel driver, exposing:
- `/sys/class/hwmon/hwmon[X]/` with `name="cros_ec"` carrying temps and fan RPM.
- The `ectool` userspace tool (out-of-tree but published) sends `EC_CMD_THERMAL_*` and `EC_CMD_PWM_*` commands.

The EC's built-in thermal table (`board/hx20/thermal.c` and similar per-board files in the Framework EC repo) defines per-sensor trip-and-target temperatures and a closed-loop fan control. Userspace can override via `ectool fanduty <pct>` (which puts the EC into manual mode) or restore via `ectool autofanctrl`.

**ventd's policy on Framework:**

1. **Detection**: `dmi/id/sys_vendor == "Framework"` AND presence of `cros_ec` hwmon. The board name (Framework 13 AMD, Framework 13 Intel, Framework 16) determines which sensor map applies.
2. **AC mode**: ventd takes manual fan control via the cros_ec hwmon `pwm[N]_enable=1` + `pwm[N]=<value>` interface (equivalent to `ectool fanduty`), running its smart-mode optimiser as on any other platform.
3. **Battery mode (recommended)**: Defer to firmware. Set `pwm[N]_enable=0` (auto) on AC→BAT transition, re-take on BAT→AC. Rationale: the Framework EC's thermal table is well-tuned for battery (Framework's developers explicitly tune for fan-power efficiency on battery), and ventd's PI controller adds minimal value over the firmware curve while consuming wakeup CPU. The exception is when a user has measured better behaviour with ventd in control on battery — config flag `smart_mode.battery.framework_defer_to_firmware = false`.
4. **Critical battery**: always defer to firmware, regardless of the above flag. The EC is the safest controller when SOC is critical.

**Framework 16 dGPU power-gating.** When the Radeon 7700S module is power-gated (no GPU load), its thermal sensor is unavailable or pinned to a default. ventd must tolerate `ENODATA` / `ENODEV` reads from the GPU thermal zone and not interpret missing data as cool. R5's existing `last-known-good with TTL` strategy handles this; on battery, the TTL should be longer (60 s vs 10 s) because the GPU is more often power-gated.

---

## 9. ThinkPad Specifics

**`thinkpad_acpi` 60-second watchdog.** Per `Documentation/admin-guide/laptops/thinkpad-acpi.rst`: when userspace takes manual fan control via `pwm1_enable=1`, the kernel arms a safety watchdog. If userspace does not write to `pwm1` (or `/proc/acpi/ibm/fan` with `enable`/`disable`/`level`/`watchdog`) within the configured interval (default `fan_watchdog` module parameter, max 120 s), the driver reverts the fan to firmware control. The watchdog is **per-write**, not periodic — every successful write rearms it.

**Implication for ventd on battery.** If ventd's design on battery is "write a moderate PWM and sleep until the next sample interval", a sample interval longer than `fan_watchdog` (typically ≤60 s) causes the firmware to take back control silently. On battery this might be desirable (firmware curve is power-efficient), but the *unpredictability* — the user sees ventd "stop working" after 60 s — is bad UX.

**ventd's policy:**

- On AC: ventd writes `pwm1` at the active sample rate (1–5 s typical from R5); watchdog never trips.
- On battery, two sub-modes:
  - `manage` (default): ventd continues writing at its sample rate, possibly slowed to 10–15 s. As long as samples are < `fan_watchdog`, control is retained.
  - `defer-on-battery` (opt-in): on AC→BAT, ventd writes `pwm1_enable=0` (auto/firmware) and sleeps; on BAT→AC, retakes manual mode. Mirrors the Framework recommendation.
- **Watchdog programming**: ventd explicitly programs `fan_watchdog` to 30 s on takeover (well above the expected sample interval), giving the firmware a clear safety net if ventd hangs. This is the conservative TLP-style approach.

**TLP / tlp-rdw coexistence.** TLP (`linrunner.de/tlp`) is the dominant Linux laptop power tool. It manages CPU governor, PCIe ASPM, USB autosuspend, Wi-Fi power, and `platform_profile` (the unified ACPI platform-profile interface), but **TLP does not manage fan curves directly**. Per the TLP documentation, TLP's `PLATFORM_PROFILE_ON_AC=balanced` / `PLATFORM_PROFILE_ON_BAT=low-power` writes to `/sys/firmware/acpi/platform_profile`, and the firmware (or `thinkpad_acpi`) reacts.

ventd must:
1. **Detect TLP**: `systemctl is-active tlp.service` returning `active` AND existence of `/etc/tlp.conf` indicate TLP is in charge.
2. **Detect platform_profile in use**: read `/sys/firmware/acpi/platform_profile` and `platform_profile_choices`. If TLP is changing the profile on AC↔battery transitions, ventd's fan-control policy should compose with that, not fight it. Specifically: on a ThinkPad where TLP sets `platform_profile=low-power` on battery, the firmware likely already biases the fan curve toward quiet — ventd's overlay should reduce its own additional bias to avoid over-quieting (and thereby thermal-throttling).
3. **Doctor surface**: report TLP detection and platform_profile state. Never modify `/sys/firmware/acpi/platform_profile` itself — that's TLP's domain.

**`/proc/acpi/ibm/fan` interface lifetime.** This procfs interface is documented as the legacy interface in the kernel docs; the recommended modern interface is the hwmon `pwm1`/`pwm1_enable` under `/sys/devices/platform/thinkpad_hwmon/`. ventd should prefer hwmon and only fall back to `/proc/acpi/ibm/fan` if `pwm1_enable` is not present (very old ThinkPads). The interface is not deprecated for removal but is "feature-frozen" — new ThinkPad features go to hwmon.

**`fan_control=1` module parameter.** Manual fan control on `thinkpad_acpi` requires `fan_control=1` at module load, which is *off* by default for safety. ventd cannot enable this at runtime; it must detect the condition (`echo 7 > /proc/acpi/ibm/fan` returns `EACCES`) and emit a doctor instruction telling the user to add `options thinkpad_acpi fan_control=1` to `/etc/modprobe.d/`. This is a pre-existing R3 (platform-driver-detection) concern; R19 only adds the battery-mode-specific phrasing in the doctor recommendation.

---

# Spec-Ready Findings Appendix

## Algorithm Choice + Rationale

**Power-source-aware preset_weight_vector modulation with AC↔battery transition smoother.**

Specifically:
1. **Power-source state machine** (3 states: AC, BAT, TRANSITIONING; debounce 3 s; anti-thrash 30 s) driven by uevent + 5 s sysfs poll.
2. **Battery modulation overlay**: `effective_weights = renormalize(base_preset ⊙ battery_modulation_vector(power_source) ⊙ (1 + sig.battery_modifier·is_battery))`.
3. **PI setpoint shift**: `setpoint_battery = min(setpoint_ac + ΔT_battery, T_passive − margin)`, default `ΔT_battery = +5 °C`, `margin = 3 °C`.
4. **Transition slew**: ≤2 PWM%/s during transitions; 6 s ramp down (AC→BAT), 10 s ramp up (BAT→AC); composes with R18's existing slew limiter (more conservative wins).
5. **Critical-battery override**: at SOC < 5 %, force a hardcoded silent-floor weight vector; bypassable with TTL'd CLI flag.

Rationale: composes cleanly with R7 workload signatures and R18 acoustic objective (overlay model preserves existing optimiser structure); no new preset enumerations; no new D-Bus dependencies; minimal state-shape impact.

## State Shape / RAM Impact

Locked R1–R18 envelope is preserved; all R19 additions are additive.

| Field | Bytes | Location |
|---|---|---|
| `power_source` enum (AC/BAT/TRANSITIONING/UNKNOWN) | 1 | smart_mode core state |
| `power_source_changed_at` (monotonic ns) | 8 | smart_mode core state |
| `battery_modulation_vector` (4× float32) | 16 | config |
| `delta_t_battery_centi_celsius` (int16) | 2 | config |
| `critical_soc_threshold_pct` (uint8) | 1 | config |
| `critical_override_engaged` (bool) | 1 | smart_mode core state |
| `transition_target_pwm` (uint8 per fan) | N×1 | smart_mode per-fan state |
| `transition_start_pwm` (uint8 per fan) | N×1 | smart_mode per-fan state |
| `transition_started_at` (monotonic ns) | 8 | smart_mode core state |
| `last_uevent_seq` (uint64) | 8 | uevent reader |
| `aggregate_soc_percent` (uint8) | 1 | smart_mode core state |
| `dock_present` (bool) | 1 | smart_mode core state |
| **R7 per-signature `battery_modifier` (int8)** | 1× signature_count | R7 table (consumes pre-allocated padding; net impact: 0) |

**Total fixed RAM**: ~47 bytes + 2N (fans). For N=4 fans this is 55 bytes. Per-supply tracking adds ~32 bytes per `power_supply` directory (typically 2–4 supplies → 64–128 bytes). **Grand total: ~180 bytes**, well under the 1 KiB R19 budget.

R7 modifier byte fits in pre-allocated padding (R7 reserved 8 bytes/record for "future scalars"). No table-resize. Locked envelope respected.

## RULE-* Bindings (rulelint-enforced, 1:1 with subtests)

- **RULE-PWR-01**: ventd MUST enumerate `/sys/class/power_supply/` at startup and on every `add`/`remove` udev event for that subsystem; missing supplies must not cause panic.
- **RULE-PWR-02**: AC↔BAT transition MUST be debounced for ≥3 s; sub-3-s flickers MUST NOT trigger a mode change.
- **RULE-PWR-03**: At most 1 power-source mode flip per 30 s wall-clock window (anti-thrash bound).
- **RULE-PWR-04**: PWM slew rate during AC↔BAT transition MUST NOT exceed 2 PWM%/s (or the R18 acoustic slew limit, whichever is more conservative).
- **RULE-PWR-05**: A USB-PD source with `online=1` but `voltage_max × current_max < 15 W` AND no battery `Charging` within 5 s MUST be treated as BAT mode, not AC.
- **RULE-PWR-06**: On battery, PI setpoint MUST satisfy `setpoint ≤ T_passive − 3 °C` for every thermal zone with a passive trip point; if no passive trip exists, fall back to AC setpoint.
- **RULE-PWR-07**: At SOC < `critical_soc_threshold_pct` (default 5), the critical-override weight vector MUST be active until SOC ≥ threshold + 2 % hysteresis OR the user issues a TTL'd bypass.
- **RULE-PWR-08**: When `jupiter-fan-control.service` is active OR DMI vendor is Valve+jupiter, ventd MUST default to `defer` mode (read-only observer) unless `--platform=steamdeck-coordinate` is explicitly passed.
- **RULE-PWR-09**: When `tlp.service` is active, ventd MUST NOT write to `/sys/firmware/acpi/platform_profile`.
- **RULE-PWR-10**: On `thinkpad_acpi` platforms in manual fan-control mode, ventd MUST program `fan_watchdog` to ≥30 s and ensure inter-write interval < `fan_watchdog`.
- **RULE-PWR-11**: For 5 s after a `pm-resume` signal, AC↔BAT transitions MUST be suppressed (broken-vendor-on-resume mitigation).
- **RULE-PWR-12**: ventd MUST NOT depend on D-Bus or UPower at runtime; absence of a session bus must not impair power-source detection.
- **RULE-PWR-13**: Trip-point cache MUST be invalidated and re-read on every uevent from `/sys/class/thermal/` and on every AC↔BAT transition.

## Doctor Surface Contract

`ventd doctor` adds a `power` section:

**Internals**:
```
power_source: BAT (since 14:23:01, 12m ago)
  supplies: AC0=offline, BAT0=Discharging 67%, BAT1=Discharging 89%
  aggregate_soc: 78%
  power_now: -8.4W (discharging)
  dock_present: no
  ramp_state: idle (last transition AC→BAT at 14:23:01, took 6.2s)
  thrash_window: 0 flips in last 30s
  battery_modulation: w_thermal=0.32, w_acoustic=0.28, w_power=0.36, w_resp=0.04
  setpoint_shift: +5°C (capped at T_passive−3°C = 79°C)
  critical_override: inactive (threshold 5%, current 78%)
  tlp_detected: yes (platform_profile=low-power)
  jupiter_fan_control: not detected
  thinkpad_fan_watchdog: n/a (not a ThinkPad)
```

**RECOVER** suggestions (matching R13's recover-doctor format):
- "If fan is louder on battery than expected: check `battery_modulation_vector` config."
- "If throttling on battery: lower `delta_t_battery_centi_celsius` toward 0; verify passive trip point with `cat /sys/class/thermal/thermal_zone*/trip_point_*_temp`."
- "If transitions are audible: increase `ramp_seconds` from default 8 toward 12."
- "If TLP and ventd disagree: TLP owns platform_profile, ventd owns PWM; both must be running concurrently is supported and intended."

**Live metrics** (per R13's live-metrics surface):
- `ventd_power_source` (gauge: 0=AC, 1=BAT, 2=TRANSITIONING)
- `ventd_aggregate_soc_percent`
- `ventd_battery_power_watts` (negative = discharge)
- `ventd_transition_count_total` (counter)
- `ventd_critical_override_active` (gauge 0/1)
- `ventd_setpoint_shift_celsius`

## HIL Validation Matrix

Phoenix fleet: Proxmox host (5800X+3060), MiniPC (Celeron), Steam Deck, 3 laptops, **NO Framework**, **NO ThinkPad**.

| Test ID | Description | Hardware | Status |
|---|---|---|---|
| HIL-PWR-01 | AC↔BAT transition debounce (3 s flicker test) | Any laptop | **Runnable now** |
| HIL-PWR-02 | Anti-thrash 30 s bound (rapid plug/unplug) | Any laptop | **Runnable now** |
| HIL-PWR-03 | Slew rate ≤2 PWM%/s during transition (microphone capture) | Any laptop | **Runnable now** |
| HIL-PWR-04 | Power-source enumeration with multi-battery | — | **BLOCKED**: no dual-battery laptop in fleet (need ThinkPad T-series with Ultrabay or similar) |
| HIL-PWR-05 | USB-PD low-wattage source rejection (5 V/1.5 A → BAT) | Any USB-C laptop | **Runnable now** (use phone charger) |
| HIL-PWR-06 | Critical-battery override engages at 5 % SOC | Any laptop | **Runnable now** (drain to threshold) |
| HIL-PWR-07 | Setpoint shift respects T_passive−3 °C cap | Any laptop with passive trip | **Runnable now** |
| HIL-PWR-08 | Trip-point dynamic update (Intel TCC offset) | Intel laptop | **Runnable now** if any of the 3 laptops is Intel |
| HIL-PWR-09 | Steam Deck `jupiter-fan-control` defer mode | Steam Deck | **Runnable now** |
| HIL-PWR-10 | Steam Deck dock detection (HDMI hot-plug) | Steam Deck + dock | **Runnable now if dock available**, else BLOCKED |
| HIL-PWR-11 | Steam Deck SteamOS read-only-rootfs install | Steam Deck | **Runnable now** |
| HIL-PWR-12 | Framework cros_ec defer-on-battery | Framework 13/16 | **BLOCKED**: no Framework in fleet |
| HIL-PWR-13 | Framework 16 dGPU power-gate thermal-zone-missing handling | Framework 16 | **BLOCKED**: no Framework in fleet |
| HIL-PWR-14 | ThinkPad fan_watchdog 60 s expiry handling | ThinkPad | **BLOCKED**: no ThinkPad in fleet |
| HIL-PWR-15 | ThinkPad TLP coexistence (platform_profile observability) | ThinkPad + TLP | **BLOCKED**: no ThinkPad in fleet |
| HIL-PWR-16 | Suspend/resume AC-flicker suppression (5 s window) | Any laptop | **Runnable now** |
| HIL-PWR-17 | UPower absent → still works (chroot/minimal env) | MiniPC | **Runnable now** |
| HIL-PWR-18 | Proxmox VM with no power_supply class → no-op gracefully | Proxmox host | **Runnable now** |
| HIL-PWR-19 | Workload signature × battery_modifier learning convergence | Any laptop, repeated workload | **Runnable now** |
| HIL-PWR-20 | Doctor surface `power` section renders correctly under each state | All hardware | **Partially runnable**; Framework/ThinkPad rows skipped |

**Hardware acquisition recommended for v0.7.0 sign-off**: at least one ThinkPad (T-series with Ultrabay if budget allows; otherwise a modern X1 Carbon or T14 satisfies HIL-PWR-14, -15) and at least one Framework 13 (HIL-PWR-12, -13). Without these, R19 ships with the platform-specific paths gated behind `--experimental` flags and an explicit "untested on Framework/ThinkPad" doctor warning.

## Estimated CC Cost (Sonnet, single PR)

| Component | Estimate |
|---|---|
| `internal/power/` new package (sysfs reader, uevent subscriber, debouncer, state machine) | ~450 LOC Go |
| `internal/smartmode/` modifications (overlay multiplication, setpoint shift, critical override) | ~180 LOC Go |
| R7 hook (battery_modifier read in scoring path; learning-rule update) | ~60 LOC Go |
| Transition smoother (slew limit, ramp budget) | ~120 LOC Go |
| Trip-point cache + invalidation | ~80 LOC Go |
| Platform-specific defer logic (Steam Deck detection, jupiter-fan-control coexistence, Framework cros_ec defer, ThinkPad fan_watchdog) | ~200 LOC Go |
| Doctor surface additions (power section, RECOVER strings, live metrics) | ~150 LOC Go |
| Config additions (TOML schema; defaults; validation) | ~80 LOC Go |
| AppArmor profile additions | ~10 lines |
| Unit tests (table-driven for state machine, debounce, slew, overlay math, critical override, signature modifier) | ~600 LOC Go test |
| HIL test scaffolding (subtests bound 1:1 with RULE-PWR-*) | ~250 LOC Go test |
| Documentation (`spec-smart-mode.md` battery section; `spec-power.md` new file; updates to `spec-doctor.md`, `spec-rulelint.md`) | ~600 lines markdown |
| **Total** | **~1.3 kLOC Go production + ~850 LOC tests + ~600 lines docs**; estimated 1 PR, 1–2 review cycles. |

## Spec Target Version

**v0.7.0** — ships alongside R18 acoustic spec; consumes R18's `preset_weight_vector` parametricity slot for `w_power`.

## Actionable Conclusions

1. **Adopt overlay architecture, not new preset.** Update `spec-smart-mode.md` to define `battery_modulation_vector` as a 4-element multiplier on the active preset's weight vector, applied after R7 signature lookup, before R18 acoustic objective evaluation.
2. **Reject UPower D-Bus.** Document rationale in `spec-non-goals.md` as a permanent decision (single-binary, no D-Bus, AppArmor-enumerable paths).
3. **Direct sysfs + uevent.** Subscribe to `NETLINK_KOBJECT_UEVENT` filtered on `power_supply` and `thermal` subsystems; supplement with 5 s polling for broken-vendor mitigation.
4. **3 s debounce + 30 s anti-thrash + 5 s post-resume suppression.** Hardcode the 5 s post-resume window; expose debounce and anti-thrash as config knobs with the cited defaults.
5. **8 s ramp default, ≤2 PWM%/s slew.** Compose with R18's slew limiter (more conservative wins). 6 s down / 10 s up asymmetry justified by psychoacoustic onset salience.
6. **R7 gets `battery_modifier int8` per signature.** Single byte fits R7's reserved padding; no keyspace doubling. Update `spec-r7.md` to document this slot.
7. **Critical battery at 5 %** with 2 % hysteresis; bypass via TTL'd CLI flag, not persistent config (`ventd doctor override --ignore-critical-battery`).
8. **Setpoint shift +5 °C on battery, capped at T_passive − 3 °C.** Re-evaluate cap on every thermal uevent and AC↔BAT transition.
9. **Steam Deck: defer to jupiter-fan-control by default.** Coordinate mode behind `--platform=steamdeck-coordinate`; never fight; detect concurrent writes and back off.
10. **Framework: defer to firmware on battery by default.** Take manual control on AC; release on AC→BAT transition. Override available via `smart_mode.battery.framework_defer_to_firmware = false`.
11. **ThinkPad: program `fan_watchdog=30s` on takeover.** Battery sub-mode `defer-on-battery` opt-in; default `manage` matches ventd's general-platform behaviour.
12. **TLP coexistence: never write `platform_profile`.** Detect TLP via systemd; surface in doctor.
13. **Add `spec-power.md`** documenting the power-source state machine, sysfs path set, AppArmor delta, and RULE-PWR-01..13 bindings.
14. **Update `spec-rulelint.md`** to register the 13 new RULE-PWR-* invariants and their 1:1 subtest mapping.
15. **Hardware acquisition request**: prioritise one Framework 13 and one ThinkPad (X1 Carbon or T14) before v0.7.0 final to validate the platform-specific paths; ship v0.7.0 with `--experimental` gating for those platforms if hardware is unavailable.
16. **AppArmor profile delta**: add read access to `/sys/class/power_supply/*/{type,status,online,capacity,current_now,voltage_now,power_now,energy_now,energy_full,current_max,voltage_max}` and confirm existing `/sys/class/thermal/thermal_zone*/trip_point_*_*` glob covers trip-point reads. No new write paths; battery-mode never writes to power_supply.
17. **No new heavy dependencies.** All R19 logic uses stdlib (`os`, `bufio`, `golang.org/x/sys/unix` for netlink), consistent with R1–R18 invariants.