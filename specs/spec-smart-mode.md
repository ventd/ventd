# spec-smart-mode — ventd autonomous fan control architecture

**Status:** DESIGN OF RECORD. Drafted 2026-04-27 in chat following Fork D pivot.
**Supersedes:** Implicit design assumptions in spec-03 (catalog-as-prerequisite),
spec-04 (PI autotune as standalone), spec-05 (predictive thermal as v1.0
work), spec-10 (doctor as install-time issue enumeration), spec-11
(superseded by spec-12).
**Applies to:** v0.5.0.1 through v0.6.0 tag. After v0.6.0 tag, this document
becomes the steady-state architecture reference, amended rather than
rewritten.
**Consumed by:** spec-16 (persistent state foundation), spec-v0_5_1 through
spec-v0_5_10 (smart-mode patch sequence), spec-12 amendment (UI rework for
confidence + three-state wizard).

---

## 1. The constraint that drives everything

> **ventd must work for the very first user no matter who they are.**

There is no installed user base. There is no community feedback loop. There
is no "supported hardware list" to grow over time. Whoever installs ventd
first — TrueNAS Scale admin, NixOS enthusiast, Framework laptop owner,
custom DIY desktop builder, Steam Deck homebrew tinkerer — the install
must succeed or fail honestly, with clear UX, on the first boot.

This constraint rules out:

- **Seed lists.** "We'll grow the catalog with community reports" is not a
  v1.0 strategy when there are zero reporters.
- **Tier-3 fallback as graceful degradation.** Catalog-less mode cannot be a
  fallback — it must be the primary path, fully tested, equal in quality
  to catalog-hit mode.
- **Phased rollout to known cohort.** No cohort exists.
- **"Wait and see what users hit."** No users.

This constraint requires:

- **Behavioural detection over enumeration.** BIOS overrides detected by
  probing response, not by board-name match. Firmware-owns-fans detected
  by observed write/revert behaviour, not by `firmware_owns_fans: true`
  in a catalog YAML.
- **Catalog-less mode as the primary code path.** The hardware catalog
  becomes a *fast-path overlay* — useful when present, never required.
- **Online thermal model with cold-start priors.** The model produces
  useful predictions in minutes from first install, not weeks of
  accumulated history.
- **Confidence-gated control.** Predictive control engages when confidence
  is high. Reactive control runs when confidence is low. UI surfaces which
  mode is active and why.
- **Continuous calibration.** Replaces one-shot at-install calibration.
  Smart-mode is constantly improving for the lifetime of the install.
- **Doctor as runtime conscience.** Not install-time triage. Recovery
  surface for autonomous-mode anomalies.

---

## 2. The three runtime states (Floor D)

ventd is a **monitoring dashboard with optional control**, not a
control daemon with optional monitoring. Probe at install determines which
runtime state the daemon enters.

### 2.1 State definitions

| State | Probe outcome | Daemon behaviour |
|---|---|---|
| **Control mode** | ≥1 readable thermal source AND ≥1 controllable fan channel | Full smart-mode: probe, calibrate, learn, control. UI shows live metrics + control surfaces. |
| **Monitor-only mode** | ≥1 readable sensor (temp/voltage/fan-tach/anything), zero controllable fans | Dashboard runs. UI clearly indicates "monitoring only — no controllable fans detected." Hot-plug awareness re-probes on cadence to detect future driver loads or hardware additions. |
| **Graceful exit** | Zero readable sensors of any kind | Wizard exits with diagnostic. ventd is not installed. Optional contribute-profile tickbox sends anonymised hardware fingerprint upstream. |

### 2.2 Wizard fork on monitor-only outcome

When probe lands in monitor-only state, wizard presents three options:

1. **Keep ventd as a monitoring dashboard.** ventd installs and runs. Will
   auto-detect future fan upgrades or driver changes that introduce
   controllable channels.
2. **Uninstall.** Wizard exits cleanly, no daemon installed.
3. **Optional tickbox: Contribute an anonymised profile of this hardware.**
   Independent of options 1 and 2. Submits hardware fingerprint to
   upstream profiles repo via the spec-14b submission flow.

### 2.3 Re-detection trigger

User can reset ventd to initial setup via the web UI Settings page. This
wipes runtime state and re-runs detection on next start. Used when:

- BIOS update changes fan control behaviour.
- Hardware swap not caught by hot-plug detection.
- User intentionally wants ventd to re-evaluate the system.

Hot-plug detection still runs continuously between resets — driver loads,
new USB AIOs, swapped fans are caught without requiring full reset.

---

## 3. Probe architecture (catalog-less primary, catalog as overlay)

### 3.1 Probe layer responsibilities

The probe layer's job is to discover what hardware ventd can see and
control, without depending on the catalog. The catalog, when present,
provides hints that accelerate or refine probe outcomes — never gates them.

Discovery sources (read-only, in order):

1. **`/sys/class/hwmon`** — primary thermal sensor + PWM channel source.
2. **DMI** (`/sys/class/dmi/id/`) — board fingerprint, vendor, BIOS
   version, virtualisation indicators.
3. **`/sys/class/thermal`** — thermal zones, useful when hwmon is sparse.
4. **NVML** (purego, read-only) — GPU sensors, fans on supported drivers.
5. **IPMI** (read-only sensor enumeration) — BMC sensors when present.
6. **EC ACPI methods** — laptop EC where exposed (ThinkPad, Framework,
   Legion).
7. **`/proc/cpuinfo` + RAPL** — CPU power/thermal context.
8. **Container/virt detection** — `/.dockerenv`, `/proc/1/cgroup`,
   `systemd-detect-virt`.

### 3.2 Probe outcome shape

Probe produces a structured result consumed by the wizard:

```yaml
probe_result:
  schema_version: "1.0"
  runtime_environment:
    virtualised: false        # true → graceful exit per Tier 2
    containerised: false      # true → graceful exit per Tier 2
    detected_via: ["dmi", "systemd-detect-virt"]

  thermal_sources:
    - source: "hwmon0"
      sensors: ["temp1_input", "temp2_input"]
      driver: "k10temp"
    - source: "thermal_zone0"
      sensors: ["x86_pkg_temp"]

  controllable_channels:
    - source: "hwmon3"
      pwm: "pwm1"
      tach: "fan1_input"
      polarity: "unknown"      # disambiguated in v0.5.2
      driver: "nct6798"
      capability_hint: "rw_full"   # from catalog overlay if present, null otherwise

  catalog_match:
    matched: true
    fingerprint: "..."
    overlay_applied: ["nct6798_chip_profile"]
    # null when no catalog match — primary path remains valid
```

### 3.3 Catalog as fast-path overlay

When catalog match is present, the overlay provides:

- **Capability hints** that may shorten probe duration (e.g. driver is
  known to support full PWM range, no need to probe step-mode).
- **Polarity priors** that reduce midpoint disambiguation work (still
  verified, never trusted blindly).
- **Quirk flags** (e.g. ASUS `cputin_floats: true`, MSI `msi_alt1`
  modprobe arg).
- **Known firmware-overrides-fans patterns** (treated as priors, verified
  behaviourally).

When catalog match is absent, probe proceeds with default-paranoid
priors: assume polarity unknown, assume PWM range standard 0-255,
assume firmware does not own fans, verify each assumption empirically.

The probe output is the same shape with or without catalog match. Code
paths downstream of probe never branch on "catalog matched vs not."

---

## 4. Hardware non-support classification

### 4.1 Tier 1 — Permanently out before v1.0

| Class | Detection | Behaviour |
|---|---|---|
| **Apple Silicon Linux (Asahi)** | DMI vendor `Apple Inc.` + ARM64 + Asahi devicetree | Graceful exit with diagnostic pointing at Asahi project status. Low priority — Mac community unlikely to install third-party fan control. |
| **BSD (FreeBSD/OpenBSD/NetBSD)** | Build target | Not a build target. Will not produce binaries for BSD. v2.0+ as separate product. |
| **Windows** | Build target | Already documented in masterplan §16. Separate product post-v1.0. Not a build target for this codebase. |

### 4.2 Tier 2 — Out until predicate met

| Class | Detection | Predicate to re-evaluate | Behaviour |
|---|---|---|---|
| **No kernel thermal source** | Probe finds zero sensors | User contributes a profile or driver | Graceful exit + contribute link |
| **No software-controllable fans** | Probe finds sensors but zero PWM channels | Hot-plug detects future driver/hardware | Monitor-only mode, wizard fork |
| **Virtualised guest with no thermal passthrough** | DMI detects KVM/VMware/Hyper-V/QEMU | Host-side install (not guest re-evaluation) | Graceful exit, "install on host" diagnostic |
| **Container** | `/.dockerenv` OR `/proc/1/cgroup` mentions docker/lxc/kubepods OR `systemd-detect-virt --container ≠ none` | `--allow-container` opt-in for read-only monitoring | Graceful exit, "install on host" recommendation. Opt-in flag enables monitor-only inside container. |

### 4.3 Tier 3 — In scope with caveats

| Class | Detection | Caveat | Behaviour |
|---|---|---|---|
| **Steam Deck (SteamOS)** | DMI board match + jupiter EC presence | Firmware aggressively manages fans | `firmware_owns_fans: true` capability detected behaviourally. Default monitor-only. Experimental opt-in workarounds via spec-15 (jupiter-fan-control bypass, direct PD Engine writes, etc.). |
| **Hackintosh** | Heuristic: macOS-targeted DSDT patches detected | Behaviour unknown, research deferred | Treat as untested. May work, no support promise. Diagnostic surfaces "this hardware configuration is not validated." |
| **ChromeOS / ChromeOS Flex** | Crosvm DMI hints, Google-specific kernel modules | Userspace lockdown may prevent ventd from running | Untested. May work. No support promise. Diagnostic surfaces what was detected. |

### 4.4 Detection mechanism

Detection runs at install time during wizard. Result persists in
calibration store. Re-detection triggers via Settings → Reset to initial
setup.

Hot-plug awareness runs continuously between full resets — driver loads,
new hardware, removed hardware are caught without re-running full
detection.

---

## 5. Probe-write safety envelope

### 5.1 First-contact envelope hierarchy

ventd never writes PWM values to a freshly-discovered channel without
user consent and a deliberate safety envelope. The envelope hierarchy is:

**Envelope C — Bidirectional probe with thermal guard (default attempt).**

- Writes any PWM value 0-255 (after polarity disambiguation per §5.2).
- Tight-loop temperature monitoring during probe.
- Abort triggers:
  - dT/dt exceeds threshold X°C/s on any thermal source ventd is monitoring
  - T_abs exceeds threshold Y°C on any monitored source
  - Background activity detected (load monitor, §5.4)
- On abort: ramp affected channel to PWM=255, mark "C-aborted," surface
  to user, fall back to Envelope D for that channel.

**Envelope D — Ramp-up-only fallback.**

- Writes never go below the channel's current PWM value.
- ventd can only increase cooling during exploration, never reduce it.
- Curve learning incomplete on the low end (min-spin, stall-PWM not
  discoverable from probe alone).
- Continuous calibration fills in low-end coverage opportunistically
  during normal operation (per §6).

**Per-channel envelope state** is recorded in calibration store:

```
last_calibration_envelope: "C" | "D-after-C-abort" | "D-cold-start"
abort_reason: "thermal_slope" | "thermal_absolute" | "background_activity" | null
```

### 5.2 Polarity midpoint disambiguation

Before any real calibration begins on a channel:

1. Record baseline RPM and temps.
2. Write PWM = 128 (midpoint), hold 3 seconds.
3. Observe RPM response:
   - **RPM increased** → polarity normal, proceed with calibration.
   - **RPM decreased** → polarity inverted, record and re-interpret all
     subsequent writes through the inverted mapping.
   - **No measurable change** → channel is suspect (no tach, fixed-speed,
     phantom, or driver gives back-pressure). Mark `phantom_channel: true`,
     register as monitor-only, do not proceed to calibration.

The midpoint write is the safest single value under either polarity
assumption — neither full-stop nor full-speed.

### 5.3 User idle gate

Before any envelope C/D probe begins, wizard presents:

> "Calibration will take approximately N minutes. For accurate readings:
>
> - Close all open applications.
> - Do not use the machine during calibration.
> - The fans will run at varying speeds.
>
> Click Begin when ready."

User clicks Begin → probe starts. Cancel returns to wizard previous step.

### 5.4 Runtime load monitor

During probe, ventd watches:

- `/proc/loadavg` 1-minute average
- Per-CPU utilisation delta from baseline
- (Optionally) NVML GPU utilisation if GPU sensors are being calibrated

If any signal exceeds threshold for >Y seconds during probe step:

1. Abort current probe step.
2. Ramp affected channel to safe value (PWM=255 default; or current value
   if higher).
3. Surface UI: "Background activity detected — please ensure system is
   idle. Click Resume to continue calibration from this step."
4. User clicks Resume → probe continues from the same step.

Wizard is **resumable mid-calibration**, not just restartable. Calibration
progress is persistent per-channel with a "paused for user" state.

### 5.5 Continuous calibration is always running

Envelope C/D is the bootstrap. Continuous calibration (§6) runs for the
lifetime of the install. There is no separate "calibration is complete"
state — the system is always learning. The first-contact probe is a
special-case acceleration; subsequent learning proceeds through normal
operation.

---

## 6. The three layers of continuous learning

Smart-mode learns three distinct properties of each system. Each has its
own update cadence, persistence model, and confidence concept.

### 6.1 Layer A — Per-fan response curve

**What:** PWM-to-RPM mapping per channel. Polarity. Min-spin PWM
threshold. Stall-PWM threshold. PWM unit (duty 0-255 vs step-mode vs
percentage).

**Stability:** Static-ish per fan. Drifts slowly with dust accumulation,
bearing wear, oil migration. Recalibration triggered by drift detection
(§6.5) or BIOS update (§3 catalog overlay invalidation).

**Storage shape:** Per-channel record in calibration store. Versioned by
hardware fingerprint + firmware version (per existing spec-03 amendment
schema v1.2).

**Learning sources:**
- Initial: Envelope C/D probe at install (§5).
- Continuous: passive observation (§6.4) of every controller write +
  observed RPM response.
- Gap-fill: opportunistic active probing (§6.4) for coverage on low end
  of curve.

### 6.2 Layer B — Per-channel thermal-coupling map

**What:** Which thermal sensors does this channel actually cool? Channel
1 affects CPU temp strongly. Channel 3 affects nothing measurable.
Channel 4 affects GPU temp slightly via case airflow.

**Stability:** Property of the system geometry — case, mount, airflow
paths. Stable per-machine. Changes when case fans are rearranged or
hardware is moved.

**Storage shape:** Per-channel matrix mapping channel → set of (sensor,
coupling_strength) pairs. Coupling strength is a coefficient, not a
boolean.

**Learning sources:**
- Passive observation only. Layer B cannot be probed actively because
  every channel-write affects the system; we cannot run a controlled
  experiment on coupling without faking a workload.
- Confidence builds as the controller naturally visits diverse RPM points
  on each channel and the model fits coupling coefficients to observed
  ΔT response.

### 6.3 Layer C — Per-workload marginal-benefit function

**What:** For each (channel, current_load_signature, current_RPM_range),
how much does ΔRPM produce ΔT? This is the function that drives "stop
ramping when not helping." Saturation point is where this function
crosses zero.

**Stability:** Workload-dependent. Idle saturation differs from
sustained-CPU saturation differs from sustained-GPU saturation. Varies
with ambient temperature, throttling state, dust, season.

**Storage shape:** RLS estimator state per (channel, workload signature).
Workload signatures are learned via §6.6.

**Learning sources:**
- Passive observation: every controller write provides a data point on
  the marginal-benefit function for the current workload signature.
- Drift detection (§6.5): when predicted marginal benefit diverges from
  observed, scheduled recalibration of affected channel.

### 6.4 Update mechanisms (layered)

**Shape 1 — Passive observation, always on, from minute one.**

Every controller write logged with full context: (PWM_set, RPM_observed,
temp_observed_per_sensor, load_signature, workload_signature, timestamp,
ambient_proxy). Persistence via spec-16 append-only log.

This is essentially free — instrumenting the existing control path. The
log is the raw data feeding all three layers. Analysis runs on a
separate cadence (background goroutine), not in the control hot loop.

**Shape 2 — Opportunistic active probing, kicks in after 24h.**

Identifies coverage gaps in Layer A specifically. The controller's
natural operating range may never visit PWM=40, so passive observation
never sees it. Opportunistic probing fills the gap.

Triggers:
- System has been idle for >N minutes (load below threshold).
- Thermal sources have been stable for >M minutes.
- No recent user input (where detectable).
- Channel has Layer A coverage gap that hasn't been visited in passive
  observation in last 7 days.

Action: write the gap PWM value for ~30 seconds, observe response, log,
return to controller-managed value.

User can disable globally in Settings → "Never actively probe after
install." Disabled in manual mode (per §7.4).

**Shape 3 — Drift detection, starts when predictive model reaches
confidence threshold.**

Watches model-vs-reality fit. When predicted RPM or predicted ΔT
diverges from observed by more than threshold X for more than Y minutes,
schedule focused recalibration of affected channel for next idle window.

Until predictive model is confident, drift detection has nothing to
compare against and is dormant. After threshold, drift detection becomes
the primary mechanism for catching environmental change (BIOS update,
dust, fan replacement, ambient shift).

### 6.5 Drift detection threshold

Initial implementation: fixed thresholds (10% RPM error, 2°C/s prediction
error sustained over 5 minutes). Adaptive thresholds based on per-
channel confidence intervals are correct in theory but harder to ship —
deferred to post-v0.6.0 refinement.

### 6.6 Workload signature learning

**Default ON in auto mode. OFF in manual mode.** Settings toggle for
power users.

Mechanism:

1. Read process names from `/proc/<pid>/comm` for processes with
   non-trivial CPU/GPU utilisation.
2. Hash names locally (no plaintext storage, no network egress).
3. Each unique hash is an opaque "workload identity."
4. Layer C maintains per-(channel, workload-identity) RLS state.
5. When a previously-seen workload-identity returns, controller
   pre-adjusts based on learned thermal pattern.

Coarse classification (loadavg + per-CPU util) always available as
fallback when no workload-identity is matched.

Fine signals (RAPL CPU power, NVML GPU power, NVML GPU util) used when
sensors are available. Improves classification quality without
additional invasiveness.

**Hashes never leave the machine.** Diag bundle includes only opaque
hash IDs by default; raw or hashed names are redacted via existing P9
redactor primitives if the user explicitly opts in for support.

---

## 7. Optimisation target

ventd does not optimise toward thermal setpoints. ventd optimises **acoustic
cost vs thermal benefit**, subject to a user-selected noise-vs-perf
preference.

### 7.1 The objective function

For each channel at each control tick:

```
benefit(ΔRPM) = Layer_C.predicted_ΔT(workload_signature, channel, current_RPM, ΔRPM)
cost(ΔRPM)    = acoustic_cost(channel, current_RPM, ΔRPM, preset)
proceed_with_ΔRPM iff benefit(ΔRPM) > cost(ΔRPM) * preset_factor
```

When `Layer_C.predicted_ΔT` is approximately zero — i.e. the system is at
saturation for the current workload — the controller does not ramp
further regardless of remaining headroom. Ramping further would be pure
acoustic cost, no thermal benefit. The controller refuses.

### 7.2 Three presets

Default UX surfaces three presets, no manual thermal targets in default
auto mode:

| Preset | Behaviour |
|---|---|
| **Silent** | Maximum weight on acoustic cost. Controller accepts higher temperatures within thermal safety margins to keep fans quiet. Saturation refusal more aggressive — even small predicted ΔT may not justify ramp. |
| **Balanced** | Default. Roughly equal weighting. Saturation refusal at predicted ΔT below threshold. |
| **Performance** | Maximum weight on thermal headroom. Controller ramps fans aggressively to stay well below throttle thresholds. Saturation refusal only when predicted ΔT is genuinely zero. |

A continuous slider is deferred until the optimisation surface is well
understood. The three presets map to three points on the acoustic-cost
weighting; the slider would interpolate but requires the user to reason
about a continuous axis they don't have intuition for.

### 7.3 Per-channel-class optimisers

Three channel classes have different optimisation parameters:

| Class | State variables | Special considerations |
|---|---|---|
| **CPU/case fans** | PWM↔RPM, thermal coupling to CPU/system temps, acoustic cost | Standard target. Most patches focus here. |
| **GPU fans** | PWM↔RPM, thermal coupling to GPU temp/junction/memory, acoustic cost. **Throttling state** as a benefit signal. | When GPU is thermally throttling, "benefit" of more cooling includes restored clock speed, not just ΔT. Saturation point looks different. |
| **AIO pumps** | Pump RPM↔coolant flow rate (where measurable), thermal coupling, **pump noise qualitatively different** from fan noise | Many AIO pumps are read-only or fixed-RPM (e.g. Arctic Liquid Freezer locked at 100%). When read-only, ventd reads telemetry but does not control. When controllable, optimisation axis differs from fans. |

### 7.4 Manual mode

Manual mode is non-default. Power user selects per-channel:

- Manual fan curve (explicit PWM-vs-temp points).
- Explicit thermal targets (channel ramps to maintain temp ≤ X).
- Fixed PWM (channel always at N%).

Manual mode disables for that channel:
- Workload signature learning (per §6.6)
- Opportunistic active probing (per §6.4)
- Saturation refusal (manual curve is honoured even when wasteful)

Drift detection still runs in manual mode but only triggers user-visible
recalibration prompts, never autonomous action.

The mode (auto vs manual) is the master switch for "ventd is allowed to
learn." Power users have one legible kill switch.

### 7.5 Preset switch UX

When user changes preset:

1. **Immediate feedback.** Controller applies new preset's objective
   function on next control tick. Fans audibly respond within seconds.
2. **Continued background optimisation.** Layer C parameters re-fit to
   the new preset over minutes to hours.
3. **UI surfaces calibration state.** Confidence indicator per channel
   updates. "Still learning your system under Silent preset" messaging
   when confidence is low under the new objective.

---

## 8. Confidence-gated control

### 8.1 The blended controller

Two controllers run simultaneously:

- **Reactive PI controller** — classical proportional-integral on
  (current_temp - target_temp). Always available, requires no learned
  state. Cold-start safe.
- **Predictive controller** — uses Layer A/B/C state to predict thermal
  future and pre-adjust. Requires learned state to function.

The output sent to PWM hardware is a **blended weighted average** of the
two controllers' outputs:

```
output = w_pred * predictive_output + (1 - w_pred) * reactive_output
w_pred = clamp(min(conf_A, conf_B, conf_C), 0, 1)
```

When all three layers are confident, predictive dominates. When any
layer's confidence is low, reactive provides safety. The blend is smooth
— there is no hard switchover that could cause control discontinuity.

### 8.2 Confidence per layer

Each layer reports a confidence in [0, 1]:

- **Layer A confidence** rises as response curve coverage broadens and
  observed PWM-RPM scatter narrows.
- **Layer B confidence** rises as coupling-coefficient fits stabilise
  across diverse workload visits.
- **Layer C confidence** rises per (channel, workload-signature) pair as
  RLS estimator residuals shrink.

Aggregate "smart-mode mode" indicator surfaces in UI:

- **Cold start** — any layer at confidence < 0.3
- **Warming** — all layers in [0.3, 0.7]
- **Predictive mode active** — all layers > 0.7
- **Drifting** — was Predictive, now any layer dropped below 0.5
- **Aborted** — Envelope C aborted, channel in D-fallback or monitor-only

### 8.3 UI surface

Confidence is **load-bearing UI**, not optional. Spec-12's mockups do not
currently show confidence anywhere — that gap drives the spec-12
amendment work.

Required surfaces:

- **Dashboard:** per-channel confidence bar or pill, current mode.
- **Devices page:** per-channel detailed confidence breakdown by layer.
- **Doctor internals fold (§9):** raw confidence numbers per layer per
  channel, recent drift events, recent envelope aborts.

---

## 9. Doctor as runtime conscience

### 9.1 Primary purpose

Doctor is the **recovery surface** — appears when something needs user
attention. Healthy systems' doctor page is mostly empty.

Recovery items fall in classes:

- **Calibration aborts.** "Channel 2 calibration aborted twice. Possible
  causes: cooler may not be removing heat fast enough during probe; system
  may not have been idle. Try [contribute logs / re-run calibration /
  switch to monitor-only]."
- **Drift detected.** "Channel 1 thermal response has drifted from baseline.
  Likely causes: dust accumulation, ambient temperature shift, BIOS update.
  Recalibration scheduled for next idle window."
- **Sensor anomalies.** "Sensor `temp4_input` has reported 0°C for 6
  hours — likely disconnected or driver bug. Excluded from fan
  decisions."
- **Stuck cold-start.** "Channel 3 has been in cold-start for 7 days.
  System may not visit enough varied workloads for Layer C to converge.
  Consider running stress-ng to seed thermal model. [Run guided
  warm-up]."
- **Mode/preset mismatches.** "Workload pattern detected suggesting
  throttling under sustained load. Your preset is Silent. Consider
  Balanced for thermal headroom."
- **Hardware change detected.** "BIOS update detected since last boot.
  Recalibrating channels 1, 3, 5 over next idle windows."

### 9.2 Internals fold

Below the recovery surface, an expandable section exposes smart-mode's
internal state for users who want to inspect:

- Per-channel learning state (cold-start / warming / converged /
  drifting / aborted)
- Per-layer confidence numbers
- Recent recalibration events (timestamp, trigger, outcome)
- Recent envelope aborts (timestamp, reason, channel)
- Workload signature library state (count, top signatures by frequency,
  per-signature confidence)
- Saturation observations per channel ("ramping above 75% has produced
  ΔT < 0.1°C on the last 12 sustained-CPU workloads")

### 9.3 Consolidation with Health page

spec-12's separate Health page consolidates into doctor. One page
answers "is ventd OK." Live metrics on top, active recovery items in
middle, expandable internals fold beneath.

### 9.4 CLI parity for doctor specifically

`ventd doctor` exposes the same content as the web UI doctor page,
formatted for terminal. Headless NAS admins SSH in and get the full
picture without opening a browser.

This is **doctor-specific**, not a global ventd CLI/web parity invariant.
Other web UI features (curve editor, dashboards with graphs, wizard
flows) do not require CLI equivalents.

---

## 10. What gets built, what gets removed

The build sequence proceeds ground-up. Each patch lays foundation or
builds on what previous patches laid. When a new code path supersedes
an old one, the old path is removed in the same patch — no parallel
compatibility shims, no feature flags for "old mode."

### 10.1 Removed during smart-mode build-out

- **`bios_known_bad.go` enumeration** — replaced by behavioural
  detection in v0.5.1.
- **Catalog-as-prerequisite assumption** — inverted in v0.5.1; catalog
  becomes fast-path overlay.
- **One-shot install-time calibration semantics** — replaced by Envelope
  C/D + continuous calibration in v0.5.3.
- **spec-04 PI autotune (standalone)** — subsumed into v0.5.9 confidence-
  gated controller. spec-04 file deleted.
- **spec-12 wizard catalog-hit-only setup flow** — reworked in v0.5.1
  to handle three-state outcome (control / sensors-only / graceful exit).
- **spec-12 mockups without confidence indicators** — reworked across
  v0.5.7-v0.5.9.
- **spec-10 issue-enumeration model** — fully replaced by v0.5.10
  recovery-surface design. Original spec-10 rewrite shelved permanently.

### 10.2 Kept (no rework)

- HAL backend registry — universal, no rewrite.
- Calibration store schema — works; what gets stored changes (continuous
  samples not one-shot).
- Hardware catalog — demoted from prerequisite to fast-path overlay.
  Existing v1.2 schema continues to apply.
- Install contract, AppArmor, polkit — orthogonal to smart-mode.
- spec-15 experimental framework — orthogonal, ships either way.
- Web UI token system / sidebar / shell — design system survives, page
  contents change.
- Diag bundle infrastructure — orthogonal.
- spec-05 predictive thermal research — graduates from "v1.0 spec shell"
  to "v0.6.0 implementation" via patch sequence.
- spec-14a/14b profile contribution flow — used by Q1 wizard's
  contribute-profile tickbox.
- spec-16 persistent state — ships first as v0.5.0.1, foundation for
  passive observation logging in v0.5.4.

---

## 11. Patch sequence to v0.6.0

| Tag | Scope | Foundation laid |
|---|---|---|
| v0.5.0.1 | spec-16 persistent state | Multi-shape KV + binary + append-only log primitive. |
| v0.5.1 | Catalog-less probe + Tier-2 detection + 3-state wizard fork | Probe layer, runtime environment detection, monitor-only path. |
| v0.5.2 | Polarity midpoint disambiguation | Per-channel polarity + phantom-channel detection. |
| v0.5.3 | Envelope C/D probe + user idle gate + load monitor | Safe first-contact write path. Resumable calibration. |
| v0.5.4 | Passive observation logging (Layer A foundation) | Append-only log of every controller write + observed response. |
| v0.5.5 | Opportunistic active probing for Layer A gaps | Idle-window gap-fill for low end of curve. |
| v0.5.6 | Workload signature learning + classification | Auto-mode signature library, coarse + fine classification. |
| v0.5.7 | Per-channel thermal-coupling map (Layer B) | Coupling coefficients per (channel, sensor) pair. |
| v0.5.8 | Marginal-benefit + saturation detection (Layer C) | RLS estimator per (channel, workload), saturation refusal. |
| v0.5.9 | Confidence-gated controller + confidence UX | Blended reactive + predictive, confidence indicators in UI. |
| v0.5.10 | Doctor recovery-surface + internals fold + CLI parity | Smart-mode runtime conscience. |
| **v0.6.0** | **TAG: smart-mode complete** | **All foundations consolidated, no parallel paths.** |

---

## 12. Per-patch validation shape

Every smart-mode patch declares three validation criteria (when
applicable; explicit not-applicable required when skipped):

1. **Synthetic tests in CI** — one or more, multiple when fixture
   coverage adds signal. Required.
2. **Behavioural HIL** — names a specific fleet member, Phoenix verifies
   before merge. Required.
3. **Time-bound metric** — when patch touches calibration speed, probe
   duration, or convergence. Optional but explicit when skipped.

### 12.1 HIL fleet

| Member | Hardware | Use |
|---|---|---|
| Proxmox host 192.168.7.10 | 5800X + RTX 3060 | Primary VM infra. Most patches HIL-verify here via VMs with hwmon/passthrough. |
| MiniPC 192.168.7.222 | Celeron | Low-end Linux HIL. Sensors-only fork testing. CLI-only doctor verification. |
| Steam Deck | jupiter EC | Firmware-owns-fans HIL. Spec-15 experimental opt-in testing. |
| 3 laptops | Various EC | Laptop EC class. NBFC integration. EC polarity edge cases. |
| Win11 desktop | 13900K + RTX 4090 | Reserved for v0.5.7-v0.5.9 (top-end Intel thermal coupling, NVML R515+ writes). Dual-boot Linux for HIL session. Daily dev remains in WSL. |
| 9900K + GTX 1660 | second-gen Intel + older NVIDIA | PLANNED — needs motherboard swap from dead 10900K, building this weekend. Once online: older nct6798 variants, older NVML behaviour, BIOS quirks. |
| 10900K | possibly dead | Skip. Redundant with 9900K. |

### 12.2 HIL strategy: Option C with B-fallback

Most patches HIL-verify on Proxmox + MiniPC + Steam Deck + 9900K
(when online) + laptops. The 13900K + RTX 4090 desktop is reserved for
2-3 late-phase patches that genuinely need top-end Intel thermal
coupling or NVML R515+ write paths (v0.5.7, v0.5.8, v0.5.9).

When those land, dual-boot Win11 desktop into Linux for the HIL
session, then return to WSL for daily dev. Each HIL session is an OS
reboot cycle (~5-10min). Acceptable cost for late-phase verification.

---

## 13. Cost projection

| Patch | Estimated CC cost (Sonnet, post-spec-drafting) |
|---|---|
| v0.5.0.1 spec-16 | $15-25 |
| v0.5.1 catalog-less probe | $15-25 |
| v0.5.2 polarity disambig | $5-10 |
| v0.5.3 envelope C/D + idle gate | $20-35 |
| v0.5.4 passive logging | $10-20 |
| v0.5.5 opportunistic probing | $15-25 |
| v0.5.6 workload signatures | $15-25 |
| v0.5.7 thermal coupling | $15-25 |
| v0.5.8 marginal-benefit + saturation | $25-40 |
| v0.5.9 confidence-gated controller | $25-40 |
| v0.5.10 doctor rewrite | $20-30 |
| **Total** | **$180-300** |

Plus chat-side architecture and spec-drafting (Max plan flat-rate).
Spec-12 amendment for UI rework adds estimated $20-40 across spec-12 PR
2-4 retrofits.

Spread across roughly 3 months at 3-4 patches/month, well within
$300/month budget.

---

## 14. Out of scope for this design

- **Specific spec content per patch.** This document defines the shape;
  per-patch specs (`spec-v0_5_1-catalog-less-probe.md` etc.) define the
  invariants, subtests, and DoD per patch.
- **MPC controller.** Predictive controller in v0.5.9 is RLS+IMC-PI per
  spec-05 research. MPC remains rejected per spec-05.
- **LSTM/GPR thermal model.** Rejected per spec-05.
- **Acoustic dithering / beat-frequency breaking.** P7-DITHER masterplan
  task. Post-v0.6.0.
- **Microphone-based bearing degradation detection.** Post-v0.6.0.
- **Profile contribution mechanics.** spec-14a/14b territory. This
  design references but does not redefine.
- **Windows / BSD / Apple Silicon.** Per §4, out of scope.
- **MPC/RLS mathematical derivation.** spec-05 territory.

---

## 15. Failure modes enumerated

1. **Probe finds zero sensors on truly bare hardware.** Graceful exit
   per §2. Wizard surfaces what was looked for and not found, contribute
   link offered, no daemon installed. Recovery: user contributes a
   profile.

2. **Polarity disambiguation inconclusive on a phantom channel.** Per
   §5.2: marked `phantom_channel: true`, registered as monitor-only,
   excluded from control decisions. UI surfaces in doctor. Recovery:
   user manually verifies via BIOS or `ventd calibrate --force-channel
   N` after diagnosis.

3. **Envelope C aborts repeatedly on the same channel.** After 2 aborts,
   channel falls back to Envelope D permanently. Surfaced to user with
   recommended diagnosis path. Recovery: user investigates cooler /
   ambient / case airflow, optionally reruns calibration after
   resolving.

4. **Layer C never converges on a workload signature.** If RLS residuals
   stay above threshold after N samples, signature is flagged "unstable"
   in doctor internals. Controller falls back to coarse-classification
   priors for that workload. Recovery: long-term observation or user
   intervention to retire the signature.

5. **Drift detected but recalibration repeatedly aborts.** Envelope C
   abort → fallback to D as in (3). User notified via doctor.

6. **User changes preset, smart-mode hasn't learned new objective yet.**
   Per §7.5: immediate reactive response, gradual Layer C re-fit,
   confidence indicator drops to "warming" until convergence under new
   preset.

7. **Hardware change between boots not caught by hot-plug.** Recovery:
   user resets to initial setup via Settings → re-detection runs.
   Calibration store wiped for affected channels.

8. **BIOS update changes fan control behaviour.** Per existing schema
   v1.2 mechanism: `firmware_version` field in calibration record
   detects mismatch on boot, triggers automatic recalibration of
   affected channels.

9. **User disables auto mode mid-learning.** Switch to manual disables
   signature learning, opportunistic probing, saturation refusal.
   Existing learned state persists for if/when user returns to auto.

10. **Probe correctly detects firmware-owns-fans, but user wants
    control anyway.** Default monitor-only with experimental opt-in
    workaround offered (via spec-15 framework). User explicitly accepts
    risk and unlocks workaround. Steam Deck is the canonical example.

---

## 16. Success criteria

This architecture is correct if:

1. ☐ A first-time user with hardware ventd has never seen can install
   ventd and have it either (a) calibrate and control fans within ~10
   minutes, or (b) install as a sensors-only dashboard, or (c) refuse
   gracefully with diagnostic — no fourth path.
2. ☐ ventd never reduces cooling on a channel during first contact
   without thermal guard active.
3. ☐ ventd never ramps fans further when the controller can prove
   marginal thermal benefit is approximately zero.
4. ☐ User in default auto mode never sees a thermal target setting,
   never sees a manual curve editor (unless they go looking).
5. ☐ User in default auto mode picks one of three presets (Silent /
   Balanced / Performance) and walks away.
6. ☐ Smart-mode confidence is surfaced in the UI as a load-bearing
   element, not a debug feature.
7. ☐ Doctor surfaces recovery items when they exist and is mostly empty
   when they don't.
8. ☐ Headless NAS admins get the same doctor output via SSH `ventd
   doctor` as web UI users see.
9. ☐ No code path branches on "catalog matched vs not" downstream of
   the probe layer.
10. ☐ All eleven patches v0.5.0.1 through v0.5.10 ship with the per-
    patch validation criteria specified in §12.
11. ☐ v0.6.0 is tagged only when all eleven patches are merged and HIL-
    verified.
12. ☐ At v0.6.0 tag, no parallel "old mode" code paths exist for any
    smart-mode subsystem.

---

**End of design of record.**

Amendments to this document follow the same PR discipline as any other
ventd change. When reality diverges from this document, fix one of them
deliberately — don't let drift accumulate.
