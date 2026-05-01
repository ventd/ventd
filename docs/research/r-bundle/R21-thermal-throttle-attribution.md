# R21 — Multi-zone thermal-throttle attribution

**Status:** OPEN. Surfaced 2026-05-01 by the post-v0.5.6 smart-mode
audit (`/root/ventd-walkthrough/smart-mode-smarter.md` §"Open
research questions" #1).

**Question.** When the OS thermal-throttle path activates (CPU
clocks drop, GPU power-limits engage, etc.) every Layer A/B/C
residual shifts discontinuously. R7 handles workload-signature
changes by re-keying RLS shards; R12 detects parameter drift via
Page-Hinkley. **Neither cleanly handles "the OS just put the
brakes on the workload."**

The shift looks like:
- A genuine workload step (R7's signature-change path tries to
  re-key — but the comm names didn't change).
- A drift event (R12 fires Page-Hinkley — but the parameters
  didn't drift; the input shifted under us).

Result: spurious recalibration, confidence collapse, blended
controller's `w_pred` drops to zero unnecessarily.

**Why R1-R20 don't answer this.**
- R7 keys on `/proc/PID/comm`; throttle state is orthogonal.
- R12 looks for residual mean shifts; throttle creates exactly
  this signature without parameter change.
- R15 (spec-05 audit) addresses RLS forgetting, not input
  classification.

**What needs answering.**
1. How does ventd consume `/sys/devices/system/cpu/cpu*/thermal_throttle/{core,package}_throttle_count`
   and `intel_powerclamp` / `amd_pstate` rate-limit signals?
2. Should throttle events be a controller-state input (akin to
   preset-change, freezing parameter updates during the event)
   or a residual-fitting input (a regressor column)?
3. How long after the throttle event subsides should normal
   parameter updates resume?
4. Are there equivalent signals on AMD (`amd_energy`,
   `amd_pmf`) that need different handling?

**Pre-requisite for.** Confidence-gated controller robustness on
13900K-class systems where transient throttling is common under
sustained AVX-512 workloads.

**Recommended target.** Post-v0.6.0; the v0.5.x→v0.6.0 sequence
ships without this and falls back gracefully (R12 reports drift,
controller widens reactive-mode contribution; recovery is
correct, just slow).

**Effort estimate.** 1 R-item + 1 spec patch (~v0.7.x). Research
~1 week, spec ~2 days, implementation ~3-5 days.
