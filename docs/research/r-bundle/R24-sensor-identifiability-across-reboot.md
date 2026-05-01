# R24 — Sensor identifiability under hwmon enumeration drift

**Status:** OPEN. Surfaced 2026-05-01 by the post-v0.5.6 smart-mode
audit (`/root/ventd-walkthrough/smart-mode-smarter.md` §"Open
research questions" #4).

**Question.** Some kernels reorder hwmon indices across boots:
`hwmon0` becomes `hwmon3` because a new module loaded earlier in
the probe order. spec-04 fingerprinting handles **DMI-level**
identity — the system is the same machine — but **per-channel
sensor identity** when the *same physical sensor* changes its
sysfs path is not addressed by any R-item.

Without this:
- R17 coupling groups (Layer B's `b_ij` matrix) are keyed on
  PWM/sensor paths. After enumeration drift, group identity is
  lost; coupling estimator re-warms from scratch.
- R16 anomaly state (Page-Hinkley statistics, RLS drift counters)
  also evaporates on reboot for affected platforms.
- v0.5.5 opportunistic-probe last-probe timestamps (per channel)
  are keyed on `observation.ChannelID(pwmPath)` which is FNV-1a
  of the path — a path change means the bin re-probes immediately.

**Why R1-R20 don't answer this.**
- R6 (polarity midpoint) uses path-based identity at probe time
  but doesn't address cross-reboot stability.
- R7 (signature hashing) operates on comm names, not sensor paths.
- R10 (RLS shards) persists shard state but the keying is via
  channel ID = hash of path, so a path change drops the shard.

**What needs answering.**
1. What's the right per-sensor / per-fan identity primitive for
   cross-reboot stability? Candidates:
   - hwmon `name` attribute + driver sub-id (e.g.,
     `nct6798:pwm1` rather than `hwmon3/pwm1`).
   - I2C bus + address tuple where applicable.
   - SHA-256 over `(driver, chip-name, channel-index-within-chip)`.
2. How do we handle the case where a fan is physically rerouted
   to a different header (so the chip-relative index is the same
   but the physical fan changed)?
3. Does the Linux kernel guarantee `hwmon/<X>/name` is stable
   across reboots? (It SHOULD be, but is there a counter-example?)
4. Migration path for existing v0.5.x installs: can we run a
   one-time path-to-stable-id translation on first start of the
   new code, preserving v0.5.x learned state?

**Pre-requisite for.** Long-term smart-mode UX on platforms with
unstable hwmon enumeration (some Asus boards, some servers, some
older kernels).

**Recommended target.** v0.5.10 or v0.6.0 doctor patch — would
benefit from being available when smart-mode tags complete. If
deferred, it becomes a post-v1.0 migration concern.

**Effort estimate.** 1 R-item + 1 spec patch (~v0.5.10). Research
~2 weeks (kernel-source-diving), spec ~3 days, implementation
~1 week. Migration script for existing installs adds another
~3 days.
