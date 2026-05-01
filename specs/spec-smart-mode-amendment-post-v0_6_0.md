# spec-smart-mode amendment — post-v0.6.0 roadmap

**Status:** AMENDMENT to `specs/spec-smart-mode.md`. Drafted
2026-05-01 following the post-v0.5.6 smart-mode audit
(`smart-mode-smarter.md`).

**Scope.** Extends spec-smart-mode §11 (patch sequence) with the
post-v0.6.0 plan and registers five new R-items (R21-R25)
identified by the audit as genuine research gaps.

---

## §11 amendment — post-v0.6.0 patch sequence

After v0.6.0 tag (smart-mode complete), the audit identifies five
high-impact improvements (A1-A5) that ship as v0.7 and v0.8. Each
extends an already-locked R-item (R16-R19) and slots cleanly:

| Tag | Scope | Source |
|---|---|---|
| **v0.7.0** | Acoustic dithering for coupled fans (A1) — push BPF beat frequency outside the 2-8 Hz fluctuation-strength peak via micro-RPM offsets within R17 coupling groups. Closed-form, no microphone. | R18 §7 |
| | Layer-A coupling residual reattribution (A3) — read-only doctor surface showing auto-discovered coupling groups via Pearson + bivariate Granger on Layer-A residuals. Suppresses spurious R12 drift events. | R17 §11.2 |
| | Power-source-aware preset overlay (A4) — multiplicative AC/battery modulation of the preset weight vector. R18 already reserved `preset_weight_vector` for this. | R19 |
| **v0.8.0** | Cross-shard ambient detection (A2) — host-scope Page-Hinkley detector on the cross-channel mean of Layer-B residuals. Catches AC failure / seasonal change as one signal. | R16 Tier-1 |
| | Per-channel cross-signature aggregation (A5) — channel-scope detector aggregating Layer-B residuals across signature shards. Catches uniform faults (heatsink dust, blocked intake). | R16 Tier-2 |
| **v0.8.x** | Remaining R16 detectors (anomaly stack), liquidctl backend ports (pure-Go reimplementation of NZXT / Lian Li / Corsair AIO protocols). | — |
| **v0.9.0** | UI / presentation layer (live RPM/PWM curve plotting in web UI, time-of-day schedules, preset preview mode, per-source mixing UI). Phoenix's future-ideas Tier-1 #1, #2. | — |
| **v1.0.0** | SMART-trend HDD failure prediction (Argus-style); R20 fleet federation (opt-in). | R20 |

A-items come from the agent's competitive-gap analysis. The
audit's full reasoning lives at
`/root/ventd-walkthrough/smart-mode-smarter.md`.

## §14 amendment — clarify what stays out of scope

The original §14 listed Apple Silicon / BSD / Windows; this
amendment adds explicit out-of-scope items the audit surfaced:

- **Microphone-based acoustic profiling.** R18 covers no-mic; mic-
  based is post-v1.0.
- **Closed-loop interaction with cpufreq governor.** Open per R23;
  current behaviour is "treat it as noise."
- **Smartwatch / wearable feedback loops.** Out of scope. Ever.
- **Cloud sync of profiles to ventd's servers.** Phoenix's
  future-ideas Tier-3 #11; will not land.

## New R-items registered

The audit identified five genuine research gaps not covered by
R1-R20. Stubs at `docs/research/r-bundle/`:

| R-item | Question | Target tag |
|---|---|---|
| **R21** | Multi-zone thermal-throttle attribution | post-v0.6.0 / v0.7.x |
| **R22** | Workload-signature persistence across kernel/userspace upgrades | v0.8.x |
| **R23** | Three-controller stable coexistence (ventd × cpufreq × powercap) | v0.7.x or v0.8.x |
| **R24** | Sensor identifiability under hwmon enumeration drift | v0.5.10 or v0.6.0 |
| **R25** | Calibration ↔ anomaly-detection mutual exclusion | v0.5.10 |

Each stub describes the question, why R1-R20 don't answer it, and
an effort estimate. None blocks v0.5.7 / v0.5.8 / v0.5.9 / v0.5.10.

## Cost / pacing impact

Spec-smart-mode §13 cost projection ($180-300 across v0.5.0.1 →
v0.5.10) is unchanged by this amendment. v0.7+ costs are
out-of-budget for the original projection but fit comfortably in
the $300/mo running budget given quarterly cadence post-v0.6.0.

---

**End of amendment.**
