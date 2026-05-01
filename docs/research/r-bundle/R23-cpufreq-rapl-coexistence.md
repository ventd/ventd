# R23 — Three-controller stable coexistence: ventd × cpufreq × powercap

**Status:** OPEN. Surfaced 2026-05-01 by the post-v0.5.6 smart-mode
audit (`/root/ventd-walkthrough/smart-mode-smarter.md` §"Open
research questions" #3).

**Question.** Three Linux control loops act on the same thermal
state simultaneously:

1. **ventd** — adjusts PWM to manage temperature.
2. **cpufreq governor** (`schedutil`, `performance`, `powersave`)
   — scales CPU frequency to fit thermal/power headroom.
3. **powercap RAPL** — caps CPU package power, throttling when
   the cap is hit.

Under sustained load these three can produce limit cycles:
- More PWM → cooler die.
- Cooler die → governor boosts frequency.
- Higher frequency → more heat.
- More heat → more PWM.

R19 (battery-aware portables) addresses *part* of this for the
AC-vs-battery boundary. **No R-item covers the AC-side three-
controller-stable-coexistence question.**

**Why R1-R20 don't answer this.**
- R12 (bounded-covariance RLS) treats cpufreq/powercap output as
  noise / drift; doesn't model their feedback into the system.
- R19 sees the cpufreq governor as a configuration switch
  (battery → `powersave`, AC → `performance`), not a co-controller.
- R5 (idle gate) consumes cpufreq via `/proc/loadavg` but doesn't
  model the closed-loop interaction.

**What needs answering.**
1. What's the maximum-stable-gain bound on ventd's predictive
   contribution given cpufreq + powercap dynamics? Standard
   linear-feedback theory should give a bound; needs derivation
   for ventd's specific architecture.
2. Should ventd expose a "cooperative mode" where it consumes the
   cpufreq governor's transition events (`/sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq`)
   as an input regressor?
3. How does ventd detect a limit-cycle in the wild? An auto-
   detector ("predictive-mode contribution caused observable
   oscillation; back off") would be safer than analytical bounds.
4. Are there published academic results on multi-loop thermal
   control stability for CPU subsystems specifically? (Beyond
   the standard MIMO control-theory literature.)

**Pre-requisite for.** Predictive-mode robustness on
high-power-density desktops where cpufreq + powercap are
aggressive (13900K-class, EPYC, modern Threadripper).

**Recommended target.** Post-v0.7.x; could land alongside R21
(thermal-throttle attribution) since both touch the cpufreq /
powercap surface.

**Effort estimate.** 1 R-item (research-heavy: ~3 weeks for the
stability analysis), 1 spec patch (~v0.8.x), implementation
~1 week.
