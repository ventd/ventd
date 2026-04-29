# R14 — Calibration Time Budget for ventd's First-Run Wizard

## PART 1 — Long-form research document

### Executive summary

R14 fixes the time contract that ventd's first-run wizard makes with the user. The contract has three layers:

1. **A defensible wall-clock budget** decomposed into stages, tied to physical measurement constraints already locked in R5, R6, and R11 rather than to wishful thinking. Best case is ~6–8 minutes for a one-channel desktop with a clean idle gate; the typical 4-channel desktop completes in 9–15 minutes; an 8-channel NAS with HDD fans and idle-gate contention can take **45–90 minutes**, and pathological worst cases (idle gate never satisfied, scrubs running, AC unplug events) can push past three hours before the wizard gives up.
2. **A staged progress UX** that respects Nielsen's 0.1 s / 1 s / 10 s limits and the operating practice of long-running Linux maintenance commands (smartctl, btrfs scrub, zpool resilver). Below 10 seconds: a labelled spinner; from 10 seconds to about two minutes: a per-stage determinate bar with stage label and per-channel sub-status; beyond two minutes: a "walk-away" affordance with notification on completion and a live channel-by-channel status table that the user *can* watch but is not expected to.
3. **A contract about what "calibrated" means**: not "fully optimised forever" but "ventd has enough data to safely start controlling fans." Continuous learning is explicitly framed as ventd's *normal* mode; the first-run wizard only seeds it. This framing is essential because the smart-mode pivot already acknowledges that full convergence happens over hours-to-weeks, not minutes.

The recommended default headline number to put in the wizard's opening screen is **"This usually takes about 10 minutes; on systems with HDDs it can take 30–60 minutes."** The per-stage budget tables, progress thresholds, failure flows, and resumability rules below justify and operationalise that number.

### Methodology

This research synthesises five strands:

1. **Hard physics**: tachometer settling time (Intel 4-wire PWM spec, two pulses per revolution, 25 kHz carrier, ≤2 s start-up pulse), thermal time constants of HDDs (SCT logging interval defined as 1 minute, drive thermal mass producing minutes-scale step responses, e.g., 13 °C drops over minutes once airflow is restored), and the abort thresholds and noise-floor ticks already locked in R4, R5, R6, R11.
2. **HCI literature**: Nielsen's three response-time thresholds (0.1 s / 1 s / 10 s) from *Usability Engineering* and the NN/g progress-indicator guidelines, augmented by his own "time scales of UX" treatment of multi-minute and multi-hour operations.
3. **Comparative tooling**: how Linux ecosystem long-running maintenance utilities present progress and time estimates — `smartctl --test=long` ("Please wait N minutes for test to complete; Test will complete after [absolute timestamp]; … 90% of test remaining"), `btrfs scrub status` (Duration / Time left / ETA / % bytes scrubbed / rate), `zpool status` ("X scanned out of Y at Z/s, T to go", explicitly approximate, can pass 100%), `pwmconfig` from lm-sensors (interactive, blocking, no progress bar at all), `fan2go` PWM-map autodetect (silent, runs at daemon startup), Steam shader pre-caching (background, percentage in Downloads pane, skippable with documented stutter penalty).
4. **Distributed-systems practice for resumable long jobs**: btrfs scrub's 5-second-interval status file and resume-from-position protocol; zpool scrub's pause-and-resume sync to disk; HTCondor's checkpoint-exit + `+is_resumable` model. These inform the spec-16 KV checkpoint shape.
5. **The locked design constraints from R1–R11 and spec-12 / spec-16** as given in the task brief.

For each Q1–Q6 sub-question the analysis derives a defensible number or rule, then cross-checks against (a) the locked constraint, (b) at least one comparable tool's behaviour, and (c) the HCI threshold that applies to the resulting wall-clock duration.

---

### Q1. Wall-clock budget decomposition

#### Q1.1 Per-stage cost model

The total calibration wall-clock time T is the sum of the following stages, executed largely sequentially (with two safe parallelism opportunities flagged below). Symbol N denotes the number of controllable PWM channels, K_C the number of distinct PWM points exercised per channel during Envelope C (taken as 3 by default: low-shoulder, mid, high-shoulder), and the loop-class is determined per channel by the dominant temperature sensor's update cadence (R11: fast-loop 2 s for CPU/GPU; slow-loop 60 s for HDD/drivetemp).

##### Stage S1 — Tier-2 detection (R1)

Tier-2 detection is a few /proc and /sys reads. Even on a server with many PCI devices these complete in well under a second. Budget: **t_S1 = 1 s** (rounded up so the wizard can show a visible "Detecting hardware" tick rather than flashing past).

##### Stage S2 — hwmon enumeration + ghost filtering (R2)

Walking `/sys/class/hwmon/*` and reading the per-channel `name`, `pwm*`, `pwm*_enable`, `fan*_input`, and `temp*_input` files for each device is a constant-time directory scan plus O(channels) reads. Ghost-entry filtering (per R2: probe runs to drop hwmon entries whose `pwm*_enable` cannot be flipped or whose tach never moves under any drive) requires a brief touch-and-restore on each candidate. A reasonable budget is **t_S2 = 1 + 0.5·N seconds**, capped to about 5 s; ghost filtering itself does not write significant PWM excursions, so it does not need tach settling time.

##### Stage S3 — Per-channel polarity probe (R6)

R6 fixes the per-channel midpoint probe: drive PWM to a midpoint (≈128/255), wait for tach to settle. The brief states a 64-PWM step takes ~3–5 s for tach to settle, with ΔRPM_probe ≥ 150 RPM (200 for sleeve-bearing) at 5× SNR margin. A defensible per-channel polarity budget therefore comprises:

- 2 s baseline tach observation at current PWM,
- 5 s settle at midpoint,
- 1 s reading + decision,
- 5 s settle on restore.

Thus **t_S3,channel ≈ 13 s**, rounded to **15 s** to absorb tach-pulse-stretching artefacts (cf. Analog Devices Analog Dialogue 38-02: low-frequency PWM chops the tach signal so a "complete tach cycle" must be guaranteed; the Intel 4-wire spec acknowledges fan startup pulses up to 2 s).

##### Stage S4 — Per-channel Envelope C (R4) or Envelope D fallback

Envelope C exercises K_C=3 PWM points per channel. At each point ventd must collect enough samples to evaluate saturation and derive the PWM→ΔT/dt coupling. The minimum sample count comes from R11:

- **Fast-loop class** (CPU/GPU; 2-second tick, N=20 samples): one PWM point requires ≥40 s of dwell after a 5 s settle ⇒ **45 s/point**, ⇒ **t_S4,fast ≈ 3·45 = 135 s ≈ 2.25 min/channel**.
- **Slow-loop class** (HDD/NAS drivetemp; 60-second tick, N=3 samples): one PWM point requires ≥180 s of dwell after a 30 s settle (drives have substantial thermal mass; the SCT Temperature logging interval is 1 minute, and observed transients are minutes-scale: see TrueNAS community measurements showing 13 °C drops over comparable airflow events). Net **t_S4,slow ≈ 3·210 = 630 s ≈ 10.5 min/channel**.

If Envelope C aborts on the per-class dT/dt ceiling (Class 5 laptops ≤ 3.0 °C/s; Class 7 NAS HDDs ≤ 2.0 °C/min), the channel falls back to Envelope D — three states (off, low, high). Envelope D needs only one settle per state and one short observation. Budget **t_S4,EnvD ≈ 60 s** for fast-loop classes, **t_S4,EnvD ≈ 360 s = 6 min** for slow-loop classes. Envelope D is therefore *cheaper* than Envelope C, which means an early abort actually **shortens** the per-channel time at the cost of a coarser model — an important property for the worst-case bound.

##### Stage S5 — Idle-gate wait (R5)

R5 requires the idle predicate to hold continuously for ≥ 300 s before Envelope C may begin. There are three behaviour regimes:

- **Lucky path**: the system was already idle when the user opened the wizard. The 300 s window starts immediately. **t_S5 = 300 s = 5 min**.
- **Bumpy path**: the predicate fires false-positive (e.g., a backup completes mid-window) one or two times. Per R5 the retry uses base 60 s / cap 3600 s exponential backoff. Two violations followed by success: **t_S5 ≈ 300 + 60 + 300 + 120 + 300 ≈ 18 min**.
- **Stuck path**: the system is consistently busy (active scrub, container churn, sustained high CPU). After N attempts (recommended N=5 with backoff), wizard surfaces a "we cannot find a calm moment" message and offers a degraded sensors-only outcome (per spec-12 three-state outcome). Time-to-give-up therefore caps at roughly **300+60+300+120+300+240+300+480+300 ≈ 40 min** before the wizard offers the user the choice to abandon Envelope C.

Notably, idle-gate time is **shared across all channels** of the same class — once the predicate has held for 300 s, all eligible channels can begin Envelope C in that window without re-arming the gate, *provided* the system stays idle. If the system becomes non-idle during Envelope C the channel aborts (see Q5) and the gate must be re-satisfied before retry.

##### Stage S6 — Per-channel thermal-coupling characterisation (Layer B seed)

Layer B coupling is computed from the Envelope C samples: it is essentially a regression over data already collected. Budget **t_S6,channel ≈ 1–2 s** of CPU work plus persistence I/O. This stage is essentially free; it is broken out only because the wizard should report it as a distinct "Computing fan→thermal coupling" step so the user understands what they are paying for.

##### Stage S7 — Persistence I/O (spec-16 KV writes)

spec-16 atomic-write semantics imply rename(2)-based commit: open temp, write payload (≤ a few KB even with channel coupling vectors), fsync, rename. On any modern filesystem this is a few ms per checkpoint, dominated by fsync latency (typically 5–50 ms). Even if ventd writes a checkpoint after every PWM point (3 per channel × N channels) the cumulative I/O cost is well under a second. **t_S7 ≈ 0.5 s total**, accountable but invisible.

#### Q1.2 Defensible per-stage time budget table

The table below gives the budget contribution from each stage in three reference scenarios. "Best case" is a tightly-coupled single-fan desktop (e.g., the 5800X+3060 Proxmox host's CPU fan only); "typical" is a 4-channel air-cooled desktop where every channel is fast-loop (CPU, two case fans, GPU); "worst case" is an 8-channel storage server with at least 3 HDD-fan channels behind drivetemp sensors and one stubborn idle-gate.

| Stage | Best (1-ch desktop) | Typical (4-ch desktop, fast-loop) | Worst (8-ch NAS, mixed; ≥3 HDD chans; bumpy idle) |
|---|---|---|---|
| S1 Tier-2 detection | 1 s | 1 s | 1 s |
| S2 hwmon enum + ghost filter | 2 s | 3 s | 5 s |
| S3 Polarity probe (15 s × N) | 15 s | 60 s | 120 s |
| S5 Idle gate wait | 300 s | 300 s | 18 min (≈1080 s) |
| S4 Envelope C (fast-loop, 135 s × N_fast) | 135 s | 540 s | 5 fast × 135 s = 675 s |
| S4 Envelope C (slow-loop, 630 s × N_slow) | — | — | 3 slow × 630 s = 1890 s |
| S6 Layer B seed | 2 s | 8 s | 16 s |
| S7 Persistence I/O | 0.5 s | 0.5 s | 0.5 s |
| **Total** | **≈ 7.6 min** | **≈ 15.2 min** | **≈ 62 min** |

Key parallelism considerations that the table does *not* exploit:

- **Polarity probes can be batched within an isolated PWM domain** (different controllers exercised in parallel) but **not within the same controller** (cross-PWM coupling on shared boards confounds the tach-delta measurement). On a typical NAS with one Super-IO chip controlling all chassis fans, polarity probes must be sequential. The table assumes serial.
- **Envelope C cannot be safely parallelised across channels that share a thermal zone** (running two fans up at once destroys the attribution of ΔT to either). Within a *single* channel, however, K_C points are sequential by definition. The table assumes serial across channels of the same class.
- **Cross-class parallelism is safe**: while CPU-fan Envelope C is running (2 s ticks, 135 s total), an HDD-fan slow-loop dwell at one PWM point can run concurrently if their thermal sensors do not couple. In practice for 4-bay NAS this is rarely worth the implementation complexity; recommend serial v1 implementation with parallelism flagged for future work.

#### Q1.3 Sanity check against comparable tools

| Tool | Headline duration | Reporting style |
|---|---|---|
| `smartctl --test=long` (typical 4 TB HDD) | "Please wait 410 minutes" (announced upfront) | Absolute completion time + "% of test remaining" |
| `smartctl --test=short` | "Please wait 2 minutes" (announced upfront) | Same |
| `btrfs scrub` (17 TiB) | ~16 hours observed | Live: Duration, Time left, ETA, %, rate |
| `zpool status` resilver (24 TiB) | ~2 hours observed | "T to go", explicitly approximate |
| `pwmconfig` (lm-sensors) | Interactive; ~2–5 min for typical board | None — prompts the user step-by-step |
| `fan2go` PWM-map autodetect | Seconds | None — silent at daemon startup |
| Steam shader pre-cache (per game) | Seconds to ~10 min | Percent-done bar; skippable with stutter penalty |

ventd's typical case at ~15 min sits cleanly between `smartctl --test=short` and `--test=long`, and ventd's worst case at ~60 min sits well below `btrfs scrub` on consumer-NAS scale. The honest precedent is therefore *announce the budget upfront and report progress live*, exactly as `smartctl` does, rather than the silent style of `fan2go`.

---

### Q2. Progress presentation

#### Q2.1 The applicable HCI thresholds

Nielsen's three response-time limits (NN/g, derived from *Usability Engineering* 1993 and Miller 1968) apply with full force at the wizard scale:

- **0.1 s** — operations that should feel instantaneous (e.g., clicking "Start calibration" should produce immediate UI acknowledgement before any backend work begins).
- **1 s** — limit for keeping the user's flow uninterrupted; relevant to the wizard's transitions between the welcome screen and the running screen.
- **10 s** — limit for keeping the user's attention focused on the dialogue. Past 10 s the user *will* mentally task-switch and the UI must explicitly support that.

NN/g's progress-indicator guideline (Pernice 2014, with updates) is: spinner for 2–10 s; percent-done indicator for ≥10 s; for highly variable durations, lower the percent-done cutoff because a spinner that runs forever causes abandonment. There is also a soft threshold near 60 s past which users typically leave the page if not given an explicit "you can leave this running" affordance, and a 10-minute boundary past which a single-task focus becomes implausible (Nielsen, Time Scales of UX, 2024).

ventd's calibration is *firmly* in the "above 10 seconds and highly variable" regime for everything except S1 and S2. The wizard must therefore:

- show **immediate** acknowledgement (<0.1 s) on the "Begin" click,
- show **stage-labelled** feedback within 1 s of starting,
- show a **percent-done indicator with ETA** as soon as the active stage's expected duration crosses 10 s, and
- offer a **"leave this running, we'll notify you"** affordance whenever the *remaining* total is expected to exceed about 2 minutes.

#### Q2.2 What other long-running install/calibration tools do

- **smartctl long self-test**: announces an upfront duration ("Please wait 13 minutes for test to complete"), prints an absolute completion time ("Test will complete after Mon May 13 03:48:25 2024"), and reports "% of test remaining" on subsequent `-c` queries. This is the **best-in-class precedent** for ventd because, like ventd, smartctl knows its budget upfront from device-reported polling times.
- **btrfs scrub status**: reports Duration, Time left, ETA, total-to-scrub, bytes-scrubbed (%), rate, and error summary. Status file written every 5 seconds; resume from saved position on cancel/reboot.
- **zpool scrub / resilver**: reports % done and ETA; the manual explicitly notes both are "only approximate, because the amount of data in the pool and the other workloads on the system can change", and that progress can pass 100% on a live filesystem. ZFS pauses scrubs and *sync the pause state to disk* so a paused scrub survives reboot — an important precedent for spec-16's resumability requirement.
- **pwmconfig (lm-sensors)**: blocking interactive; no progress bar; loud safety warning ("Pwmconfig will shutdown your fans to calibrate settings against fan speed. Make sure your system won't overheat while you configure your system. Preferably the computer is completely idle, and all power-saving measures are enabled."). ventd's wizard should *replace* this terrible UX, not emulate it.
- **fan2go autodetect**: silent at daemon start; no UX. Acceptable for a power-user tool but unacceptable for ventd's "any user, any hardware" remit.
- **Steam shader pre-caching**: percent bar with "Skip processing" affordance that documents the cost (game stutter until done in background). The principle here is **"long operations may run in the background; surface a clear contract about what is sacrificed if the user skips"**.
- **Machine-learning model warm-up / first-run quantisation**: typically uses indeterminate spinners with task-name labels ("Compiling model… this may take several minutes on first run"). The relevant precedent is the *honest "first run only" framing* — the user accepts a long wait now in exchange for fast subsequent runs.

#### Q2.3 Recommended UX hybrid for ventd

A single uniform progress UI cannot cover ventd's range from 1 s detection to 90 min worst case. The recommendation is a **three-tier hybrid**:

**Tier W1 — Pre-flight (S1+S2, < 10 s)**: a labelled spinner with current stage text ("Detecting fan controllers…", "Cataloguing hwmon entries…"). No percentage. No ETA.

**Tier W2 — Polarity + idle-gate + Envelope C (the bulk of the wait)**: a determinate progress bar with three components:

1. **Headline ETA**: "About X minutes remaining" updated every 5 s. The ETA is computed from completed stages plus the *remaining* per-channel budget table (Q1.2), not by extrapolating instantaneous rate (which would be extremely noisy because of the 300 s idle gate plateau).
2. **Stage label**: "Probing channel 3 of 4 (CPU fan): Envelope C, dwelling at PWM 192…".
3. **Per-channel status table** (collapsed by default, expandable): each channel as a row, current sub-stage, last tach reading, last temp reading, current PWM. This is the *power-user equivalent of `zpool status`* — opt-in detail without forcing it on the basic user.

**Tier W3 — "Walk away" affordance**: visible from the moment the headline ETA exceeds 2 minutes, prominent above the progress bar, with text such as "You can close this tab. ventd will keep calibrating in the background and notify you when it's done." Notification mechanisms in v1.0:

- Browser tab title flashes on completion (no extension required).
- An optional desktop notification via the browser Notifications API, gated on user grant.
- A persistent state in spec-16 KV that the wizard reads on reload to show "Calibration complete — review results" or "Still running, X% done".

Email/SMS/push are explicitly **out of scope** for v1.0 (solo-dev budget; integration cost not justified for a one-time first-run flow). They appear instead as a "share the wizard URL to your phone" QR code so the user can monitor on a second device — a zero-cost feature that exploits ventd's already-existing local-network web UI.

#### Q2.4 What **not** to do

- **Indeterminate spinner past 30 s**: NN/g specifically warns users may wait forever and abandon the task. Do not use for any stage above 10 s.
- **Fake percent-done that crawls linearly**: progress bars whose mapping is fictional damage trust permanently. Use real per-stage completion or omit the percentage.
- **Disabling the close button**: hostile and unnecessary; spec-16 resumability means the user is free to leave.
- **Long-form blocking modal "Calibration in progress, do not close" warnings**: these signal that the daemon is fragile, which contradicts the "zero-config, zero-terminal" vision.

---

### Q3. Interruption handling

#### Q3.1 Wizard tab close

The wizard is a thin client over a daemon-owned state machine. Closing the tab MUST NOT pause, abort, or otherwise affect calibration. The daemon owns the calibration, period; the wizard merely *renders* its state. On reload, the wizard reconnects, reads the current calibration state from spec-16 KV (or via a `GET /api/v1/calibration/status` endpoint that internally reads the same KV), and rejoins the in-progress flow exactly where it left off. This is the same model `btrfs scrub status` and `zpool status` use — the operation lives in the kernel/daemon, the CLI/UI is read-only over it.

The single exception is the "Cancel" button (Q3.4). Closing the tab is *not* cancellation.

#### Q3.2 System shutdown mid-calibration

spec-16 atomic writes ensure the on-disk state is always consistent. The recommended checkpoint *granularity* is at **stage-within-channel** boundaries, not finer:

| Checkpoint event | What is persisted |
|---|---|
| End of S1 (detection) | Tier-2 detection result vector |
| End of S2 (enumeration) | hwmon catalog with ghost flags |
| End of polarity probe per channel | Polarity decision + raw probe data |
| Idle-gate window opening | Timestamp of window start (so recovery can decide whether the 300 s has already elapsed) |
| End of each PWM dwell within Envelope C | (PWM, mean ΔT/dt, RPM) tuple appended to channel record |
| End of Envelope C per channel | Per-channel coupling vector + Envelope C completion flag |
| End of Layer B seed | Final coupling matrix |

On restart, the daemon reads spec-16 state and resumes from the highest-numbered completed stage, *re-running only the most recent in-flight PWM dwell* (which loses at most ~210 s for a slow-loop class HDD channel, ~45 s for a fast-loop CPU channel). This is the same trade-off as btrfs scrub's 5-second status file update interval — fine enough to make resume cheap, coarse enough that the I/O is invisible.

The only state that does *not* resume is the idle-gate's 300 s durability counter: by definition, the predicate must hold continuously for 300 s, and a system shutdown trivially breaks that continuity. The gate is re-armed from zero on resume.

#### Q3.3 Idle-gate refusal mid-Envelope-C

R5's idle-gate is a precondition for *starting* Envelope C; the per-class dT/dt ceilings in R4 are the abort triggers *during* Envelope C. The two interact in the following way on mid-channel non-idle events:

- **Hard refusal triggers** (AC unplug, container start, mdadm/zfs/btrfs scrub, process blocklist hit): immediately abort the in-flight PWM dwell, restore PWM to its pre-calibration value, mark the channel "Envelope C interrupted", drop into Envelope D fallback for that channel after the gate re-arms. Do **not** retry Envelope C in the same wizard session — Envelope D is good enough to seed Layer B with a coarser model, and the user has been waiting long enough.
- **Soft thermal abort** (per-class dT/dt ceiling crossed): same flow — abort dwell, restore PWM, fall back to Envelope D for the channel. This is the R4 contract.

The principle: **a channel never burns more than its budgeted Envelope C time + a small Envelope D tail (≤ 6 min for HDD, ≤ 1 min for fast-loop)**. Worst case per channel is therefore bounded by `t_S4,EnvC + t_S4,EnvD = 10.5 + 6 = 16.5 min` for an HDD channel, `2.25 + 1 = 3.25 min` for a fast-loop channel. The user never gets stuck in an unbounded retry loop on a single channel.

#### Q3.4 Cancel calibration

A "Cancel" button in the wizard MUST leave the system in a state that is:

1. **Thermally safe**: every channel's PWM is restored to the value it held *before* calibration began. This is captured at S2 (enumeration) and stored. If ventd was already controlling fans, that means restoring ventd's pre-calibration PWM; if ventd was not yet controlling, it means returning the channel to firmware/automatic mode (write `2` to `pwm*_enable` for nct67xx-style chips, or detach control entirely on chips where automatic mode is the safe default). This mirrors `pwmconfig`'s well-known warning: "we will attempt to restore each fan to full speed after testing. However, it is **very important** that you physically verify that the fans have been restored to full speed".
2. **Operationally honest**: ventd does **not** start running fan curves with partial calibration. If S4 did not complete for a channel, that channel is marked "uncalibrated" and ventd refuses to control it — falling instead to the firmware default. The smart-mode pivot's three-state outcome (control / sensors-only / graceful exit) accommodates this naturally: a Cancel during S5 or earlier produces a "sensors-only" outcome system-wide; a Cancel mid-S4 produces "control on calibrated channels, sensors-only on uncancelled channels" if any channel completed, or "sensors-only" otherwise.
3. **Resumable**: the partial state stays in spec-16 KV. The wizard's next run offers "Resume calibration" as the default action and "Start over" as a secondary action.

Default-to-100% safety as a permanent post-cancel state is **rejected** — it is acoustically unacceptable on consumer hardware and contradicts the zero-terminal vision. Falling back to firmware automatic mode is the right default.

---

### Q4. User-expectation anchors

#### Q4.1 The headline number

The wizard's first screen must set an honest expectation. The recommended copy (per spec-12 wizard mockup conventions) is:

> **"This usually takes about 10 minutes."**
>
> **"On systems with hard drives or many fans, it can take 30 to 60 minutes."**
>
> **"You can close this tab and come back later — calibration runs in the background."**

The choice of "about 10 minutes" as the headline is anchored to the typical 4-channel desktop case (15.2 min in the table, rounded *down* slightly to 10 min as a Schelling point users will round in their heads anyway, then offset by the more conservative 30–60 min secondary anchor for NAS users). Calling out HDDs explicitly is essential because the slow-loop dwell time genuinely dominates and users with HDDs need to know in advance.

The phrase "you can close this tab" preempts the anxiety created by every other Linux fan tool's blocking, root-shell-only, "do not interrupt" UX. It is the single most important user-trust line on the screen.

#### Q4.2 The first-run vs continuous-learning distinction

ventd's smart-mode is explicitly progressive (Layer A passive, Layer B coupling, Layer C marginal benefit). The wizard must communicate that first-run calibration is a **seeding** step, not a one-shot model fit. Recommended copy on the completion screen:

> **"Calibration complete. ventd is now controlling your fans."**
>
> **"ventd will keep learning your system over the next 24 hours and refining its decisions. You don't need to do anything."**
>
> **"If your hardware changes (new fans, drives, GPU), run calibration again from Settings."**

The mental-model pivot here is from "calibrated = optimised" (false; the user reasonably expects this from the word) to "calibrated = ready to safely run" (true; aligned with how the system actually works). This framing reuses well-understood prior art: "device drivers initialising"; "ZFS scrub will inspect data over the next several hours"; "Steam pre-caching shaders will continue in the background"; ML model fine-tuning. None of these claim instant optimality at the moment they finish their headline operation.

The cost framing is asymmetric and should be presented that way:

- **First-run cost**: one-time, ~10 minutes typical, up to an hour on NAS, requires the system to be relatively idle.
- **Continuous-learning cost**: zero — the daemon is already running; learning happens during normal operation and is invisible.

#### Q4.3 What "calibrated" actually means

To the *system*: the spec-16 KV state contains, for every controllable channel, (a) an unambiguous polarity, (b) at least one model — Envelope C if it succeeded, Envelope D otherwise — characterising PWM→ΔT/dt coupling sufficient to choose safe fan curves, and (c) a flag indicating which Layer in the smart-mode hierarchy can begin contributing.

To the *user*: ventd has confirmed "this fan responds to this PWM and influences this temperature" for every fan it intends to control, has established a *safe* operating range, and is ready to begin running. It is not a guarantee that decisions are optimal — only that decisions are safe and informed by real measurement rather than guesses.

This is the same contract `lm_sensors --detect` makes ("we found the chips") versus the contract a complete tuned `fancontrol` config makes ("we understand them"); ventd's first-run sits in between and the wizard copy must say so.

---

### Q5. Failure-mode messaging

For each likely failure, the wizard needs a concrete user-facing flow. The flows below are written as if they appear directly to the user.

#### Q5.1 Idle gate cannot be satisfied

After 5 retries with R5's exponential backoff the wizard surfaces:

> **"We can't find a quiet moment to test fans on this system."**
>
> **"ventd needs about 5 minutes of low-activity time to safely measure how each fan affects your temperatures. We tried 5 times over the last half hour, but something on your system kept doing background work."**
>
> **"Common causes: scheduled backups, ZFS scrub, container builds, or laptops on battery power."**
>
> **Two buttons:** **"Try again later"** (closes the wizard, leaves spec-16 state with `gate_unmet_count++`) and **"Continue without full calibration (sensors-only mode)"** (graceful exit per spec-12; ventd reads sensors and exposes them in the UI but does not control any fans).

This is the smart-mode pivot's `sensors-only` outcome. Note we *never* offer "calibrate anyway, ignoring safety" — the idle gate exists for thermal-safety reasons.

#### Q5.2 Envelope C aborts on a channel

User-facing copy when an R4 abort threshold is crossed mid-dwell:

> **"Channel 3 (HDD fan): had to stop the detailed test."**
>
> **"This channel changed temperature too quickly under load (likely because two drives share a thermal zone). ventd switched to a simpler 3-point measurement that's safer for your drives."**

The retry strategy is **no retry of Envelope C in this session** — fall through to Envelope D and continue. The reasoning: an Envelope C abort means the ceiling was real, and re-attempting in the same idle window is equally likely to abort again. Envelope D is the principled fallback. A user who wants to retry Envelope C can re-run calibration from settings (e.g., after improving case airflow).

#### Q5.3 Polarity ambiguous on a phantom channel

R6 expects ΔRPM_probe ≥ 150 RPM (200 for sleeve-bearing). Below this the channel is ambiguous. R2 ghost-filtering will already have caught most pure phantoms, but residual ambiguity is possible (e.g., a controllable PWM whose tach is tied to a different fan; a header with no fan attached but a working PWM register).

Wizard copy:

> **"Channel 5: this looks like a phantom or unconnected fan header."**
>
> **"ventd can change the PWM, but the fan speed reading didn't move noticeably (RPM change was less than 150). This usually means there's no fan plugged in here, or the speed sensor wire is shared with another fan."**
>
> **"ventd will skip this channel — it's safer than guessing."**

No retry; skip and continue. The channel is marked "ambiguous-skipped" in spec-16 state. A power user can unhide it via `ventd channels --include-ambiguous` (CLI) but the wizard does not surface this option.

#### Q5.4 No channel produces measurable thermal coupling

Pathological case: every Envelope C / Envelope D probe completes without aborting, but no PWM excursion changed any temperature reading by more than the noise floor. This is a **truly disconnected fan** scenario — fans spin, but they are not actually moving meaningful air past any sensor (e.g., NAS where the front fans are clogged with dust, or a home-built case where the only PWM-controlled fan happens to be the PSU exhaust which the system temperature sensors don't see).

Wizard copy:

> **"Calibration finished, but ventd couldn't tell which fans cool which sensors."**
>
> **"All your fans respond to commands, but none of them produced a measurable temperature change during the test. This usually means the fans don't move air across the temperature sensors that ventd can read."**
>
> **"ventd will keep watching your system over the next few hours; sometimes the relationships become clear during normal use. In the meantime, ventd is leaving fan control to your motherboard's automatic mode."**

This maps cleanly to the smart-mode pivot's "Layer B has insufficient signal — defer to Layer A passive observation" path. ventd does **not** start controlling fans in this state.

#### Q5.5 Hardware refusal mid-calibration (Steam Deck, Framework with fragile EC)

Per the brief, Steam Deck is excluded from HIL validation, but defensive handling is still required for users who install ventd on these platforms.

Detection of fragile-EC behaviour during calibration:

- PWM writes silently fail (write succeeds, read-back does not match).
- PWM writes succeed but produce no tach change *and* no temperature change (distinct from Q5.4 because the write itself is suspect).
- Repeated EC errors in `dmesg` (ventd does not parse dmesg in v1.0, so this is detected indirectly via write-readback diff).

Wizard copy:

> **"This system has a custom embedded controller that doesn't behave like a standard PC fan."**
>
> **"ventd detected this is a [Steam Deck / Framework laptop / unsupported EC type] and will not attempt to control its fans. Your firmware's built-in fan curve will continue to run."**
>
> **"You can still use ventd to monitor temperatures."**

This is the third branch of spec-12's three-state outcome: `graceful-exit` for control, `sensors-only` available. The key UX principle is that ventd never claims to control hardware it cannot reliably drive — silence is better than thermal incidents.

---

### Q6. Resumability budget

#### Q6.1 Granularity

The recommended granularity is **stage-within-channel**, with one finer-grained exception inside Envelope C (per-PWM-point checkpoint).

Rationale:

- Polarity probes are short (~15 s) and cheap to redo. Per-channel is the right granularity.
- Envelope C dwells are long (45 s fast-loop, 210 s slow-loop). Per-PWM-point checkpointing reduces worst-case redundant work to one dwell after a crash.
- Envelope D states (off/low/high) are short enough that per-channel granularity is sufficient.

The spec-16 KV checkpoint shape (sketch):

```
calibration:
  schema_version: 1
  started_at: <timestamp>
  current_stage: enum {detect, enumerate, polarity, idle_gate, envelope, layer_b, complete}
  channels:
    - id: <hwmon_path>
      class: enum {fast_loop, slow_loop}
      polarity: {decided: bool, value: enum {normal, inverted}, raw: [...]}
      envelope_c:
        attempted: bool
        aborted: bool
        points: [{pwm: int, dt_dt: float, rpm: int, sample_count: int}, ...]
      envelope_d:
        attempted: bool
        states: {off: ..., low: ..., high: ...}
      coupling: {seeded: bool, vector: [...]}
      pre_calibration_pwm: int   # for safe restore on cancel/crash
  idle_gate:
    last_window_start: <timestamp or null>
    consecutive_failures: int
```

This is small (a few KB even for an 8-channel system), JSON-serialisable, and trivially diffable for debug.

#### Q6.2 Checkpoint frequency

The btrfs scrub precedent is "status file updated every 5 seconds". For ventd the corresponding rule is **checkpoint at every PWM-point completion within Envelope C, and at every stage boundary elsewhere**. Concretely:

- After S1 completion: 1 write.
- After S2 completion: 1 write.
- After each polarity decision: 1 write per channel ⇒ N writes.
- On idle-gate window start: 1 write.
- After each Envelope C PWM dwell: K_C writes per channel ⇒ K_C × N writes.
- After Envelope C / D channel completion: 1 write per channel.
- After Layer B seed: 1 write.

For a typical 4-channel desktop: 1 + 1 + 4 + 1 + 12 + 4 + 1 = **24 writes total**. At ~10 ms per atomic-rename write, this is **240 ms of cumulative I/O over a 15-minute calibration** — entirely invisible to users and well within the spec-16 atomic-write contract.

#### Q6.3 Crash recovery semantics

On daemon restart (planned or crash) the recovery procedure is:

1. Read `calibration` KV. If `current_stage == complete`, do nothing — calibration finished cleanly before the crash.
2. Otherwise, identify the highest channel index whose Envelope C/D was completed.
3. For each prior channel, trust the persisted polarity, envelope, and coupling.
4. For the first incomplete channel, retain its polarity decision (cheap to keep) and its already-completed Envelope C PWM points; resume Envelope C from the next unmeasured PWM point. If polarity itself was incomplete, redo polarity (cheap).
5. Re-arm the idle gate from zero (R5 continuity requirement).
6. Continue.

Worst-case redundant work after recovery:

- One incomplete Envelope C dwell: ≤ 210 s for HDD, ≤ 45 s for fast-loop ⇒ **~3.5 min lost** in the worst single-event case.
- Idle-gate re-arm: 300 s ⇒ **5 min lost** unconditionally.
- Net: **≤ 8.5 minutes** of redundant work per crash event.

This is cheap relative to the worst-case full calibration time and is dominated by the unavoidable idle-gate re-arm rather than by I/O or measurement overhead — meaning even *more* aggressive checkpointing would not reduce the recovery cost meaningfully. The K_C-point granularity is therefore the right design point.

---

### Recommended UX flow with example wizard text and progress states

A complete example flow for a typical 4-channel desktop, illustrating the three-tier hybrid in action:

**Step 0 — Welcome screen (instant)**

```
Welcome to ventd.

We need to learn about your fans before ventd can control them safely.

This usually takes about 10 minutes.
On systems with hard drives, it can take 30 to 60 minutes.

You can close this tab — calibration runs in the background.

[ Begin calibration ]   [ Use sensors-only mode ]
```

**Step 1 — Pre-flight (W1 spinner, ~3 s)**

```
⏳ Detecting fan controllers…
⏳ Cataloguing 14 hwmon entries…
✓  Found 4 controllable fan channels.
```

**Step 2 — Polarity (W2 progress bar, ~60 s)**

```
Step 2 of 4: Confirming fan polarity
[████░░░░░░░░░░░░░░░░] 25%   ETA: about 14 minutes

Currently testing: Channel 1 of 4 (CPU fan)
   Setting PWM to mid-range, watching tach…   832 RPM → 1410 RPM ✓

› Show details for all channels (4 rows, expandable)
```

**Step 3 — Idle-gate wait (W2 with explicit gate UI, ~5 min)**

```
Step 3 of 4: Waiting for a quiet moment
[██████░░░░░░░░░░░░░░] 30%   ETA: about 13 minutes

ventd needs 5 minutes of low system activity before measuring fans.
   Quiet for 02:14 of 05:00 …

You can leave this open. We'll continue when the system is calm.
```

**Step 4 — Envelope C (W2 + W3 walk-away affordance, ~9 min)**

```
Step 4 of 4: Measuring how each fan affects temperatures
[████████████░░░░░░░░] 60%   ETA: about 6 minutes

📥  You can close this tab — we'll keep going and notify you when done.

Currently testing: Channel 3 of 4 (Case fan, rear)
   PWM 192 → CPU temp +0.4 °C/min

Channel 1 (CPU fan)        ✓ Done — strong cooling effect
Channel 2 (GPU fan)        ✓ Done — strong cooling effect
Channel 3 (Case fan, rear) ⏳ Measuring (point 2 of 3)
Channel 4 (Case fan, top)  · Queued
```

**Step 5 — Completion (instant)**

```
✓ Calibration complete.

ventd is now controlling 4 fans on your system.

ventd will keep learning your system over the next 24 hours.
You don't need to do anything.

[ Open dashboard ]   [ Run calibration again ]
```

---

### Citations

(Primary sources cited inline above.)

1. Jakob Nielsen, "Response Times: The 3 Important Limits", *Usability Engineering* (1993) and NN/g, https://www.nngroup.com/articles/response-times-3-important-limits/
2. Kathryn Whitenton / Jakob Nielsen, "Progress Indicators Make a Slow System Less Insufferable", NN/g, https://www.nngroup.com/articles/progress-indicators/
3. Jakob Nielsen, "Time Scales of UX: From 0.1 Seconds to 100 Years", https://jakobnielsenphd.substack.com/p/time-scale-ux
4. Robert B. Miller, "Response time in man-computer conversational transactions", *Proc. AFIPS Fall Joint Computer Conference* Vol. 33, 267–277 (1968).
5. Brian A. Myers, "The importance of percent-done progress indicators for computer-human interfaces", *CHI '85*.
6. Intel, "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification", Rev 1.3, September 2005, https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf — 25 kHz PWM, two pulses/revolution, ≤2 s startup pulse, behaviour below minimum duty cycle "undetermined".
7. Analog Devices, "Why and How to Control Fan Speed for Cooling Electronic Equipment" (Analog Dialogue 38-02), https://www.analog.com/en/analog-dialogue/articles/how-to-control-fan-speed.html — tach signal chopping under low-frequency PWM, pulse-stretching considerations.
8. smartmontools project, `smartctl(8)` man page (Debian testing), https://manpages.debian.org/testing/smartmontools/smartctl.8.en.html — long self-test announce-then-poll model; "% of test remaining" reporting.
9. btrfs-progs project, `btrfs-scrub(8)` man page, https://btrfs.readthedocs.io/en/latest/btrfs-scrub.html — Duration / Time left / ETA / Bytes scrubbed reporting; status file written every 5 seconds; resume from saved position.
10. OpenZFS, `zpool-status(8)` and `zpool-scrub(8)`, https://openzfs.github.io/openzfs-docs/man/master/8/zpool-status.8.html — explicitly approximate ETA, may exceed 100% on live filesystems; pause-state synced to disk.
11. lm-sensors, `pwmconfig` and `fancontrol(8)`, https://man.archlinux.org/man/extra/lm_sensors/fancontrol.8.en — interactive UX pattern with explicit overheat warning.
12. fan2go project documentation, https://github.com/markusressel/fan2go — silent autodetect PWM-map model (counterexample for the wizard).
13. Linux kernel hwmon documentation, https://www.kernel.org/doc/html/latest/hwmon/sysfs-interface.html — `pwm*_enable` semantics and standard sysfs interface.
14. CHTC, "Checkpointing Jobs", https://chtc.cs.wisc.edu/uw-research-computing/checkpointing — exit-driven resumable-job pattern (`+is_resumable`) used as the conceptual model for ventd's mid-calibration crash recovery.

---

## PART 2 — Spec-ready findings appendix block

### R14: Calibration time budget for first-run wizard

**Defensible defaults (concrete numbers and choices)**

- **Headline budget (wizard opening copy)**: "About 10 minutes typical; 30–60 minutes on systems with HDDs."
- **Per-stage budgets** (all configurable in `internal/calibration/budget.go` with these as `const` defaults):
  - `S1_DETECT_BUDGET = 1 s`
  - `S2_ENUMERATE_BUDGET_BASE = 1 s`, `S2_ENUMERATE_BUDGET_PER_CHAN = 0.5 s`
  - `S3_POLARITY_PER_CHAN = 15 s`
  - `S5_IDLE_GATE_MIN = 300 s` (locked by R5)
  - `S5_IDLE_GATE_MAX_RETRIES = 5` (then surface "we couldn't find a quiet moment" and offer sensors-only)
  - `S4_ENVC_FAST_PER_POINT = 45 s`, `S4_ENVC_SLOW_PER_POINT = 210 s`, `S4_ENVC_POINTS = 3`
  - `S4_ENVD_FAST_TOTAL = 60 s`, `S4_ENVD_SLOW_TOTAL = 360 s`
  - `S6_LAYER_B_PER_CHAN = 2 s`
  - `S7_PERSIST_PER_WRITE = 50 ms` (assumed worst-case fsync)
- **Progress UI thresholds**:
  - 0–10 s: labelled spinner, no percentage.
  - 10 s–2 min: determinate stage-bar with ETA, no walk-away affordance.
  - 2 min+: determinate stage-bar with ETA *plus* prominent "you can close this tab" affordance and per-channel status table (collapsed by default).
- **Checkpoint granularity**: stage-within-channel; finer per-PWM-point inside Envelope C.
- **Recovery cost bound**: ≤ 8.5 min redundant work per crash event (one dwell + idle-gate re-arm).
- **Cancel semantics**: restore pre-calibration PWM on every channel; mark uncompleted channels as uncalibrated; ventd refuses to control uncalibrated channels (firmware automatic mode takes over). Calibration state preserved; "Resume" offered as default on next wizard run.
- **Three-state outcome mapping** (per spec-12): `control` if Envelope C or Envelope D completed for ≥1 channel with measurable coupling; `sensors-only` if idle gate exhausted retries or no channel produced coupling; `graceful-exit` if Tier-2 detection / hwmon enumeration produced no controllable channels or hardware-refusal pattern detected.

**Citations (URLs to primary sources)**

- https://www.nngroup.com/articles/response-times-3-important-limits/
- https://www.nngroup.com/articles/progress-indicators/
- https://jakobnielsenphd.substack.com/p/time-scale-ux
- https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf
- https://www.analog.com/en/analog-dialogue/articles/how-to-control-fan-speed.html
- https://manpages.debian.org/testing/smartmontools/smartctl.8.en.html
- https://btrfs.readthedocs.io/en/latest/btrfs-scrub.html
- https://openzfs.github.io/openzfs-docs/man/master/8/zpool-status.8.html
- https://man.archlinux.org/man/extra/lm_sensors/fancontrol.8.en
- https://github.com/markusressel/fan2go

**Reasoning summary (one paragraph)**

The total calibration wall-clock is bounded by the hardware physics already locked in R4/R5/R6/R11: tachs need 3–5 s to settle, the idle gate requires 300 s of clean idle, fast-loop sensors need ~40 s of dwell per PWM point and slow-loop (HDD) sensors need ~180 s, and Envelope C exercises three points per channel. Composing those constraints gives a typical 4-channel desktop budget of ~15 min and a worst-case 8-channel NAS budget of ~60 min, with the slow-loop dwell time and idle-gate re-arms dominating the long tail. Nielsen's 0.1 s / 1 s / 10 s response-time thresholds and NN/g's progress-indicator guideline directly imply a three-tier UX (spinner → determinate bar → walk-away affordance with notification) keyed on the *expected remaining* duration; the smartctl announce-then-poll precedent and the btrfs scrub Duration/Time-left/ETA/% reporting model are the strongest in-tree precedents and ventd should imitate them. spec-16 atomic writes at stage-within-channel granularity (with per-PWM-point granularity inside Envelope C) bound recovery work to ≤ ~8.5 min per crash, which is cheap relative to the calibration budget and dominated by the unavoidable idle-gate re-arm. The honest user-facing framing is "calibrated = ready to safely run", not "calibrated = optimised forever" — continuous learning is ventd's normal state.

**HIL-validation flag (which fleet members validate which aspects)**

| Aspect | Validator(s) |
|---|---|
| Best-case 1-channel desktop budget | Laptop A (lid-closed CPU-only fan), MiniPC Celeron (single chassis fan) |
| Typical 4-channel desktop fast-loop budget | 13900K+RTX 4090 dual-boot, 5800X+3060 Proxmox host |
| Worst-case 8-channel NAS slow-loop budget (partial) | TerraMaster F2-210 NAS — *limited validation: only 2-bay, will not exercise 8-channel path; must extrapolate slow-loop dwell timings* |
| Idle-gate retry / scrub interaction | 5800X+3060 Proxmox host (ZFS scrub deliberately scheduled to collide with calibration) |
| Polarity probe edge cases (sleeve-bearing 200 RPM threshold) | All three laptops (typical sleeve-bearing CPU fans) |
| Envelope C abort on dT/dt ceiling | 13900K+RTX 4090 (Class 5 laptop ceiling tested via stress run) and TerraMaster (Class 7 HDD ceiling) |
| Resume after daemon SIGKILL mid-Envelope-C | 5800X+3060 Proxmox host (most controllable) |
| Wizard tab-close / reload mid-calibration | Any host with a browser; primary on Proxmox host |
| Hardware-refusal / fragile-EC graceful-exit path | **NOT validated on hardware** — Steam Deck excluded per fleet policy; Framework not in fleet. Must be validated by static-analysis review and synthetic fault injection only. |

**Confidence**

**Medium-High.** Rationale: the per-stage time budgets are derived directly from already-locked design constants (R4/R5/R6/R11) and a single layer of arithmetic; physics-bound stages (idle-gate 300 s, slow-loop 60 s tick × 3 samples) cannot be made faster without breaking those locks. The HCI thresholds are 30+ years stable. The main residual uncertainty is the worst-case 8-channel NAS bound: the test fleet's TerraMaster F2-210 is a 2-bay system, so the 8-channel × 3-HDD-channels worst case must be partially extrapolated. The recommendation downgrades from "High" to "Medium-High" specifically on this extrapolation. Confidence in the typical-case budget and the UX flow itself is High.

**Spec ingestion target**

- Primary: `spec-12-wizard.md` § "Calibration view: states, copy, and progress" (replaces existing wizard mockup section per the smart-mode pivot).
- Secondary: `spec-16-persistent-state.md` § "Calibration checkpoint shape" (canonical KV layout for `calibration:` namespace).
- Tertiary: `spec-08-calibration.md` § "Time budget and abort/resume semantics" (cross-reference to R4/R5/R6/R11; defines the Go-side `const` budget table that drives the wizard ETA).

**Review flags (contradictions and open questions)**

- *No contradictions* with already-locked R1, R2, R4, R5, R6, R11 design. The budget assumes those locks hold; if R5 ever weakens the 300 s requirement, the typical-case budget should be revisited downward.
- **Open question — parallelism:** the recommended v1 implementation is fully serial across channels for Envelope C. The R14 budget tables explicitly do not exploit cross-class parallelism. Whether to enable it as an optional fast-path for users who pass `--accept-coupling-risk` is **deferred to a future R-task**.
- **Open question — notification mechanism:** the recommendation is browser tab-title flash + optional Notifications API + QR-code-to-phone. Email/SMS/push are out of scope. If user research disagrees post-v1.0, this becomes its own R-task.
- **Open question — exact Envelope C point count K_C:** the brief states "multiple PWM points exercised per channel" without fixing the count. R14 assumes K_C = 3 for budget arithmetic. R4 is the canonical owner of this number; if R4 fixes K_C ≠ 3, the budget tables must be regenerated.
- **HIL gap — Steam Deck / Framework EC**: the hardware-refusal flow (Q5.5) is unverified on real fragile-EC hardware. Recommend gating the `graceful-exit` outcome behind a dry-run plus a strong fallback ("if in doubt, do not control") and explicitly documenting this gap in the v1.0 release notes.
- **Wizard mockup rework dependency**: spec-12 mockups are flagged as "currently being reworked per smart-mode pivot". R14's Step 0–5 example copy is offered as a concrete proposal but is not load-bearing on the budget itself.

---

## PART 3 — Implementation file targets (extending C.5 list)

### `internal/calibration/budget.go`

**Purpose**: Per-stage time-budget bookkeeping. Owns the constant table, computes expected wall-clock for the current channel-set, exposes a `BudgetEstimate` struct that the wizard consumes.

**Surface (sketch)**:

```go
package calibration

import "time"

// Stage budgets — defaults from R14. All overridable for HIL tuning via flags.
const (
    S1DetectBudget         = 1 * time.Second
    S2EnumerateBaseBudget  = 1 * time.Second
    S2EnumeratePerChan     = 500 * time.Millisecond
    S3PolarityPerChan      = 15 * time.Second
    S5IdleGateMinHold      = 300 * time.Second // R5 lock
    S5IdleGateMaxRetries   = 5
    S4EnvCFastPerPoint     = 45 * time.Second
    S4EnvCSlowPerPoint     = 210 * time.Second
    S4EnvCPoints           = 3
    S4EnvDFastTotal        = 60 * time.Second
    S4EnvDSlowTotal        = 360 * time.Second
    S6LayerBPerChan        = 2 * time.Second
    S7PersistPerWrite      = 50 * time.Millisecond
)

type ChannelClass int
const (
    ClassFastLoop ChannelClass = iota // CPU/GPU; R11 2 s tick
    ClassSlowLoop                     // HDD; R11 60 s tick
)

type Channel struct {
    ID    string
    Class ChannelClass
}

type BudgetEstimate struct {
    Total          time.Duration
    PerStage       map[Stage]time.Duration
    ConfidenceBand time.Duration // ± window for ETA display
}

// Estimate returns a budget for the given channel set, assuming a clean idle gate
// (1 retry). Worst-case estimation is provided by EstimateWithRetries.
func Estimate(chs []Channel) BudgetEstimate { /* ... */ }

func EstimateWithRetries(chs []Channel, gateRetries int) BudgetEstimate { /* ... */ }
```

Tests: golden-vector tests for the three reference scenarios in Q1.2 (1-channel desktop, 4-channel fast-loop desktop, 8-channel mixed NAS). Unit-tests must lock the headline numbers (7.6 / 15.2 / 62 minutes) so future budget edits are visible in code review.

### `internal/calibration/progress.go`

**Purpose**: Live progress reporting. Owns the public type the wizard renders, including stage label, percent-done (per-stage, not synthetic), per-channel sub-status, and ETA.

**Surface (sketch)**:

```go
package calibration

type ProgressTier int
const (
    TierW1Spinner ProgressTier = iota // <10 s active stage
    TierW2Bar                          // 10 s – 2 min remaining
    TierW3WalkAway                     // >2 min remaining
)

type Progress struct {
    Tier            ProgressTier
    StageLabel      string         // "Probing channel 3 of 4 (CPU fan): Envelope C…"
    PercentDone     float64        // 0..100, real not synthetic
    ETA             time.Duration  // remaining budget
    Channels        []ChannelStatus
    WalkAwayMessage string         // populated when Tier == TierW3WalkAway
}

type ChannelStatus struct {
    ID         string
    Class      ChannelClass
    Stage      string  // "polarity", "envelope_c_pwm_192", "done", "queued", ...
    LastRPM    int
    LastTempC  float64
    Outcome    string  // "ok", "envelope_c_aborted", "ambiguous", "uncalibrated", ""
}

// Subscribe returns a channel of Progress updates emitted at most every 1 s
// (Nielsen 1 s flow threshold). Slow-loop dwells emit at most every 5 s.
func Subscribe(ctx context.Context) <-chan Progress { /* ... */ }
```

Tier transitions are computed from `EstimatedRemaining()` and update at most once per second. The implementation reads the live budget burn-down — *not* a wall-clock-based estimate that ignores the idle gate's plateau.

### `internal/calibration/checkpoint.go`

**Purpose**: spec-16 KV resume integration. Defines the on-disk shape of `calibration` state, atomic-write semantics, and the resume planner.

**Surface (sketch)**:

```go
package calibration

type CheckpointStage int
const (
    StageDetect CheckpointStage = iota
    StageEnumerate
    StagePolarity
    StageIdleGate
    StageEnvelope
    StageLayerB
    StageComplete
)

type Checkpoint struct {
    SchemaVersion   int
    StartedAt       time.Time
    CurrentStage    CheckpointStage
    Channels        []ChannelCheckpoint
    IdleGate        IdleGateCheckpoint
}

type ChannelCheckpoint struct {
    ID                 string
    Class              ChannelClass
    Polarity           PolarityResult
    EnvelopeC          *EnvelopeCResult
    EnvelopeD          *EnvelopeDResult
    Coupling           *CouplingVector
    PreCalibrationPWM  uint8 // for safe restore on cancel/crash
}

// Persist writes atomically (open temp, write, fsync, rename).
// Returns the elapsed I/O cost so budget.go can account for it.
func Persist(c *Checkpoint, kv KVStore) (time.Duration, error) { /* ... */ }

// Plan returns the next stage to run given a loaded checkpoint, plus
// the bound on how much redundant work the resume implies.
func Plan(c *Checkpoint) (next CheckpointStage, redundantWorkBound time.Duration, err error) { /* ... */ }
```

Tests: round-trip (Persist → Load → equal); resume-from-each-stage (golden scenarios); `redundantWorkBound` ≤ 9 min in all paths (R14 invariant).

### `internal/wizard/calibration_view.go`

**Purpose**: HTTP handlers and HTML templates for the wizard's calibration screens. Renders the three-tier UX.

**Surface (sketch)**:

```go
package wizard

// GET /wizard/calibrate         — pre-flight (welcome, "Begin")
// POST /wizard/calibrate        — start (kicks off internal/calibration)
// GET /wizard/calibrate/status  — JSON Progress for the live UI
// POST /wizard/calibrate/cancel — cancel; restores PWMs; preserves checkpoint
// GET /wizard/calibrate/result  — completion screen

func (h *Handler) GetCalibrateWelcome(w http.ResponseWriter, r *http.Request) { /* W0 */ }
func (h *Handler) StartCalibration  (w http.ResponseWriter, r *http.Request) { /* */ }
func (h *Handler) GetCalibrateStatus(w http.ResponseWriter, r *http.Request) { /* W1/W2/W3 */ }
func (h *Handler) CancelCalibration (w http.ResponseWriter, r *http.Request) { /* */ }
func (h *Handler) GetCalibrateResult(w http.ResponseWriter, r *http.Request) { /* */ }
```

Templates: one per tier (`tier_w1_spinner.html`, `tier_w2_bar.html`, `tier_w3_walkaway.html`), one per failure mode (Q5.1–Q5.5), one for completion (Q4.2 framing), one for resume-on-reopen ("Calibration is still running on your system — X% done; reconnecting…"). The templates use server-sent events (SSE) over `/wizard/calibrate/status` so the wizard updates without polling — pure-Go `net/http` is sufficient (no JS framework dependency, consistent with the solo-developer / `CGO_ENABLED=0` constraint).

A small amount of progressive-enhancement JS is acceptable for the SSE listener and the collapsed/expanded per-channel table; both must degrade gracefully to a meta-refresh page for users with JS disabled (per the zero-terminal *and* zero-trust-of-third-party-script ethos that fits ventd's homelab audience).