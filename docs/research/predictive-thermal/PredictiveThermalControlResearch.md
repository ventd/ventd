# ventd Predictive Thermal Control: Design-Space Research Report

**Target project:** ventd — a pure-Go (Go 1.25+, CGO_ENABLED=0, GPL-3.0), zero-config autolearning fan controller daemon for Linux homelab/NAS systems, aspiring to be the first shipping Linux fan controller that is **predictive rather than reactive**.

This report surveys the full design space (prior art, model classes, online learning, signal acquisition, storage, safety) and ends with a concrete recommended architecture and a spec outline.

---

## 1. Short-horizon thermal prediction (5–30 s)

### 1.1 The physics — what we are actually modeling

CPU-plus-heatsink thermal behaviour is well-approximated by a **lumped-parameter RC network**: power dissipated by the die charges a thermal "capacitance" (die + spreader + heatsink mass × specific heat) through a "resistance" (junction-to-case, case-to-heatsink, heatsink-to-air). The dominant first-order time constant τ = R·C determines how temperature lags power. This is the canonical HotSpot formulation and is the mainstream model in microarchitecture thermal research ([Skadron et al., *HotSpot: A Compact Thermal Modeling Methodology for Early‑Stage VLSI Design*, IEEE TVLSI 2006](https://www.cs.virginia.edu/~skadron/Papers/hotspot_tvlsi06.pdf)).

Time-constant scales matter a great deal for ventd:

* The **silicon die + spreader** has τ on the order of **tens of milliseconds** (Mesa-Martínez et al. report τ ≈ 54 ms on a modern processor) ([*Characterizing Processor Thermal Behavior*, ASPLOS 2010](https://users.soe.ucsc.edu/~renau/docs/asplos10.pdf)).
* The **package + heatsink + chassis air volume**, which is what a *fan controller* actually manipulates, has τ on the order of **several seconds to tens of seconds** — e.g. Microchip measures ~6 s for SAMA7G54 junction to reach 60 % of final value ([Microchip, *Linux Thermal Control Experiment*](https://onlinedocs.microchip.com/oxy/GUID-3C446FCE-1416-4EA6-A798-FFA31AAE007B-en-US-2/GUID-3C446FCE-1416-4EA6-A798-FFA31AAE007B-en-US-2/GUID-6353C83C-8BEC-48D0-8CC6-1089732B5FD2.html)).

This gap is the crux of the ventd opportunity: for the purpose of **fan speed**, the plant is a slow first-/second-order system with τ of **5–60 s**, which is precisely the short-horizon window where a lightweight model can anticipate by 5–30 s with high accuracy. The millisecond-scale silicon dynamics are handled by the CPU itself (RAPL, P-states, kernel thermal framework); ventd targets the system-level loop.

### 1.2 Model-class comparison

**Lumped-parameter RC models (first/second order).** The classical single-node model is

    C · dT/dt = P − (T − T_amb) / R(fan_rpm)

where R depends nonlinearly on fan RPM (convective heat transfer coefficient h scales roughly as RPM^0.5–0.8 over the fan's operating range). First-order models with power input are adequate for fan-speed control down to ~1 °C steady-state error on single-socket systems ([Skadron *et al.*, HotSpot paper set](https://www.cs.virginia.edu/~skadron/lava/HotSpot/documentation.htm)); second-order (die vs. case node) is needed only when both fast transients and steady state must be tracked. Storage cost: 3–6 parameters. Inference cost: trivial (a handful of multiplies). Fully CGO-free in Go via `gonum/mat`.

**State-space + Kalman filter for parameter estimation.** Widely used in building HVAC and battery-SoC literature for *joint* state + parameter estimation: the state is temperature(s), and unknown R/C are appended to the state vector so an Extended Kalman Filter tracks them online. Ye *et al.* successfully use dual/constrained EKF for building thermal-zone RC identification, reporting ~25 % faster convergence vs. joint EKF with improved numerical stability ([Ye, Roth & Johnson, *Sequential state prediction and parameter estimation with constrained dual EKF for building zone thermal responses*, Energy & Buildings 2018](https://www.sciencedirect.com/science/article/abs/pii/S0378778817316821)). MDPI provides a recent tutorial review of the approach ([Simultaneous State and Parameter Estimation, MDPI Sensors 2025](https://www.mdpi.com/1424-8220/25/22/7043)). Inference cost is O(n²) per step for an n-dim state; for n≤8 this is trivial. Pure-Go Kalman libraries exist: `rosshemsley/kalman` offers non-uniform-time-step filters ([rosshemsley/kalman on pkg.go.dev](https://pkg.go.dev/github.com/rosshemsley/kalman)) and `konimarti/kalman` offers an adaptive Kalman filter with explicit A/B/C/D/Q/R matrices ([konimarti/kalman on GitHub](https://github.com/konimarti/kalman)) — both pure-Go, gonum-based, production-used. They are a sound baseline to fork rather than reimplement.

**Recursive Least Squares (RLS) with forgetting factor.** The workhorse of online system identification. Given an ARX regressor φ_k and output y_k:

    P_k = (1/λ)[P_{k-1} − P_{k-1}φ_k(λ + φ_k^T P_{k-1}φ_k)^{-1}φ_k^T P_{k-1}]
    θ_k = θ_{k-1} + P_k φ_k (y_k − φ_k^T θ_{k-1})

The forgetting factor λ∈(0.95, 0.999] balances tracking speed vs. noise; **variable/adaptive forgetting factor** variants (VFF-RLS) give better behaviour when plant dynamics change abruptly ([Paleologu, Benesty, Ciochină, *A Robust Variable Forgetting Factor RLS Algorithm*, IEEE SPL 2008](https://ieeexplore.ieee.org/document/4639569/); MDPI Energies paper on AFFRLS for lithium-ion RC identification: ([Yang et al. 2019](https://www.mdpi.com/1996-1073/12/12/2242)) shows it outperforms fixed-λ RLS in accuracy and convergence). Numerical concerns: the covariance P can wind up ("covariance windup") during poor excitation — standard remedies are covariance resetting, UD-factored RLS, and directional/partial forgetting. RLS is roughly O(n²) per step; for n = 5–15 regressors, literally microseconds in pure Go with gonum. This is the *easiest* method to implement correctly from scratch; no Go library is strictly needed.

**ARX / ARMAX.** ARX is what RLS actually identifies: y(k) = −a₁y(k−1) − … − aₙy(k−n) + b₁u(k−1) + … + bₘu(k−m) + e(k). With 2–3 lags of temperature and 1–2 lags of (power, fan-RPM, load) exogenous inputs, ARX produces very good 5–30 s prediction on thermal plants and is used routinely in HVAC identification — e.g. Ashar *et al.* report 98.01 % best-fit for an ARX(3,3,1) real-time temperature-plant model ([*ARX model identification for real-time temperature*, ICIC Express Letters 2020](http://www.icicel.org/ell/contents/2020/2/el-14-02-01.pdf)). ARMAX adds a moving-average noise model, which is rarely needed when inputs are rich (we have RAPL power, utilisation, fan RPM). **ARX + RLS** is the best "small, simple, easy to explain, easy to debug" choice.

**Derivative-based feedforward preaction.** Instead of predicting temperature explicitly, compute a control contribution from dP/dt and d(load)/dt so the fan spins up *at the same instant as* the power spike rather than waiting for temperature to rise. This is a classical industrial technique: feedforward adds a model-based best-guess to a small feedback PID, *before* the error appears ([Feed forward (control), Wikipedia](https://en.wikipedia.org/wiki/Feed_forward_(control)); mbedded.ninja tutorial treats it as a standard PID extension ([*PID Control*, mbedded.ninja](https://blog.mbedded.ninja/programming/general/pid-control/))). Cost: essentially free. Two gains and a smoother. This is the primary mechanism by which ventd "beats" a pure feedback curve controller and should always be present regardless of which model class runs underneath.

**Tiny MLPs in pure Go.** A fully-connected 4-layer 16×16 MLP is a few kB of weights and some thousands of multiply-adds per inference — well within budget. In pure Go, viable options are `gonum/mat` hand-rolled (this is what Data Dan uses to stay CGO-free: ([Building a neural network from scratch in Go, datadan.io](https://datadan.io/blog/neural-net-with-go))). Gorgonia's CPU path also works without CGO but defaults to gonum/BLAS and is heavyweight for inference ([gorgonia README](https://pkg.go.dev/gorgonia.org/gorgonia)). Newer pure-Go ML frameworks exist (`born-ml/born`, `zerfoo/zerfoo`: [born-ml/born](https://github.com/born-ml/born), [zerfoo/zerfoo](https://github.com/zerfoo/zerfoo)) but are focused on LLM inference and are massive overkill. **For 5–30 s horizons on smooth thermal signals, neural networks are *not* worth the complexity vs. ARX/RLS**; the literature on predictive thermal control for MPSoCs consistently uses linear RC + MPC, with ANN only appearing at the datacenter scale where spatial CFD effects dominate ([Coskun *et al.*, *Proactive Temperature Management in MPSoCs* 2008](https://dl.acm.org/doi/10.1145/1393921.1393966)).

**Gaussian Process regression.** GPR gives calibrated uncertainty which is attractive for safety, but is O(N³) in training points and O(N²) per prediction, and standard implementations require Eigen/BLAS. Online sparse variants (GoGP: [Le *et al.*, ICDM 2017](https://ieeexplore.ieee.org/document/8215498/)) mitigate the cost but increase complexity substantially. A pure-Go implementation exists but is research-grade ([`bitbucket.org/dtolpin/gogp`](https://pkg.go.dev/bitbucket.org/dtolpin/gogp)). **Not recommended** for v1; the uncertainty information a GP provides can be approximated by an RLS/Kalman covariance trace at a fraction of the cost.

**LSTM / GRU.** Demonstrably good on thermal time-series with complex delays ([Wang *et al.*, Energy & Buildings 2023](https://www.sciencedirect.com/science/article/abs/pii/S2352710222013225)), but inference/training cost is not zero on tiny homelab CPUs when polled at 1 Hz across many cores/fans, and the literature shows prediction delay artefacts that require Bi-LSTM/GRU to fix. **Not recommended**: violates minimal-memory spirit and adds substantial dependency weight. The thermal plant is almost linear; deep nonlinear models are the wrong tool.

**Model Predictive Control (MPC).** MPC *is* the right framework for **the controller** (not the model). Academic work on multicore thermal management uses MPC extensively: ([Bartolini *et al.*, *Thermal Control of Manycore and Multicore Processors*, IEEE CSS](https://ieeecss.org/sites/ieeecss/files/2019-06/IoCT2-RC-Bartolini-1.pdf); *Multicore Thermal Management with MPC*, IEEE 2009: [ieeexplore.ieee.org/document/5275073/](https://ieeexplore.ieee.org/document/5275073/); two-layer distributed MPC in [Bambini *et al.*, Control Engineering Practice 2022](https://www.sciencedirect.com/science/article/abs/pii/S0967066122000235)). These papers all work on-chip (DVFS, task migration) with millisecond horizons; the same math at 1 Hz with fan RPM as the manipulated input is trivial by comparison. **Explicit MPC** (Bemporad's approach, pre-solving the QP offline to produce a piecewise-affine function of the state) keeps runtime cost constant and avoids linking a QP solver, which is a lifesaver for CGO-free builds. For ventd v1 this is overkill; for v2+ it's the long-term goal.

### 1.3 Short-horizon ranking for ventd

| Class | Predictive at 5–30 s | Params | CPU/RAM cost | Pure-Go | Recommendation |
|---|---|---|---|---|---|
| **ARX + RLS** | Excellent (98 % fit in literature) | 5–15 | microseconds, <1 kB | Trivial (gonum) | **Primary model** |
| **1st/2nd-order RC + EKF** | Excellent, physically interpretable | 4–8 | microseconds, <1 kB | Yes (rosshemsley/konimarti) | **Secondary / physical sanity check** |
| **Derivative feedforward** | Essential at all horizons | 2–4 | nanoseconds | Trivial | **Always on, independent of model** |
| Tiny MLP | Good but unnecessary | 100s–1000s | ms, kB | Yes (gonum) | v2 if evidence demands |
| GPR (sparse/online) | Good with uncertainty | grows with N | too much | Research-only (gogp) | No |
| LSTM/GRU | Good but laggy, heavy | 10k+ | too much | Possible but heavy | No |
| MPC (explicit) | Optimal controller | small after offline solve | microseconds | Possible | v2 controller layer |

---

## 2. Long-horizon workload anticipation (30 s – 5 min)

### 2.1 Process-launch detection

Four realistic sources on Linux:

1. **/proc polling.** Simple, zero-privilege, but misses short-lived processes and has O(procs × freq) cost. Useful as a *fallback* only.
2. **Netlink proc-connector (`NETLINK_CONNECTOR` / `CN_IDX_PROC`).** Kernel emits `PROC_EVENT_FORK`, `PROC_EVENT_EXEC`, `PROC_EVENT_EXIT`, etc. over a netlink socket. Requires `CAP_NET_ADMIN` (essentially root), available since 2.6.15, pure-Go accessible via `vishvananda/netlink`'s `ProcEventMonitor` ([netlink/proc_event_linux.go](https://github.com/vishvananda/netlink/blob/main/proc_event_linux.go)). This is the **best-fit** choice for ventd: kernel-pushed events, no polling, low overhead, pure Go.
3. **fanotify with `FAN_OPEN_EXEC` / `FAN_OPEN_EXEC_PERM`** (since Linux 5.0): triggers precisely on execve/execveat/uselib of a file ([fanotify(7) man page](https://man7.org/linux/man-pages/man7/fanotify.7.html); [fanotify_mark(2)](https://www.man7.org/linux/man-pages/man2/fanotify_mark.2.html)). Excellent for exec detection, but requires mount-wide marks and root. Use *as an alternative* to netlink; the information content is similar.
4. **eBPF tracepoints** (`sched_process_fork`, `sched_process_exec`, `sched_process_exit`). Highest-fidelity, lowest-overhead option, arbitrary kernel data extraction, the canonical source for tools like `execsnoop` ([Red Hat *Analyzing system performance with eBPF*](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/managing_monitoring_and_updating_the_kernel/analyzing-system-performance-with-ebpf)). Cilium's `cilium/ebpf` is a **pure-Go, CGO-free** library with no dependencies beyond the standard library — exactly what ventd needs ([cilium/ebpf](https://github.com/cilium/ebpf); [ebpf-go docs](https://ebpf-go.dev/)). Caveats: requires kernel 5.x+ with BTF/CO-RE for portability, requires `CAP_BPF` or root, and embeds compiled eBPF bytecode (built with `bpf2go` from a tiny C source — this is build-time C, not runtime CGO).
5. The **audit subsystem** via netlink is theoretically usable but rule management is heavyweight and the event stream is noisy.

**Recommendation for ventd:** **netlink proc-connector as the primary, eBPF as an optional fast-path** when kernel supports it. Both are pure-Go; both require root; ventd already runs as root to poke hwmon PWM. Fall back to 1 Hz `/proc` polling only when neither is permitted.

### 2.2 Workload fingerprinting

Given a stream of `(exec_path, comm, cgroup, uid, argv_hash, timestamp)` events, learn which ones coincide with sustained thermal/power excursions.

**Primitive: label each exec event ex post.** After N seconds of observation following an exec, compute Δtemperature, Δpower, Δutilization, duration. If the excursion exceeds thresholds, tag the signature as a "heavy workload". Store per-signature:

    signature = (basename(exec_path), argv_template_hash_if_available)
    stats     = {count, mean_dpower, mean_dtemp, mean_duration, var, last_seen}

This is a *Bayesian update* — a mean and variance estimator per key — not clustering. It handles the 80 % case (game launches, `make`, `ffmpeg`, `handbrake`, `restic backup`, VM boot) with trivial state (~100 bytes per known workload).

**When clustering does help:** argv patterns (`docker compose up -d foo`, `python train.py --model=...`) produce too many distinct keys. Online k-means / incremental clustering over an embedding of (exec, argv tokens, cgroup depth, user) groups related launches. Pure-Go clustering options: `muesli/kmeans` ([github.com/muesli/kmeans](https://github.com/muesli/kmeans)), `mpraski/clusters` (k-means++, DBSCAN, OPTICS, with Online API; [pkg.go.dev](https://pkg.go.dev/github.com/mpraski/clusters)), and `wearelumenai/distclus` (multi-threaded, online, distance-based; [pkg.go.dev](https://pkg.go.dev/github.com/wearelumenai/distclus)). For ventd v1, **hash-keyed running stats** suffice; clustering is a v2 feature.

**Time-series motif discovery** (matrix profile) finds recurring patterns without labels — weekly TrueNAS scrub at 3 am, daily Proxmox backup, nightly Plex transcodes. The canonical algorithm family is STAMP/STOMP/SCRIMP++ by Keogh's group ([UCR Matrix Profile page](https://www.cs.ucr.edu/~eamonn/MatrixProfile.html); [Zhu *et al.*, *SCRIMP++*, ICDM 2018](https://ieeexplore.ieee.org/document/8594908/)). A pure-Go implementation exists and is healthy: `matrix-profile-foundation/go-matrixprofile` ([GitHub](https://github.com/matrix-profile-foundation/go-matrixprofile)). This is the *right* tool for "learn that every Sunday at 03:00 there's a 40 min 80 °C event". Cost is the main concern — STOMP is O(n²) in window count. Running it **offline** (cron, or background goroutine at low priority) over the past week of compressed 1 Hz telemetry is fine and the output is a static set of "scheduled events" the online loop can anticipate.

### 2.3 Prior art for workload-triggered profiles

Windows gaming software (Corsair iCUE, ASUS Armoury Crate, Razer Synapse) implements this via **static user-defined mapping of executable path → profile**. iCUE asks you to "link profile to one or more executable files" ([Corsair: How to Create an iCUE Profile for Your Favorite Game](https://www.corsair.com/us/en/explorer/diy-builder/how-tos/how-to-create-an-icue-profile-for-your-favorite-game/)); Razer Synapse has "Linked Games" ([Razer Insider community thread](https://insider.razer.com/mice-and-surfaces-9/does-razer-synapse-have-application-specific-settings-24724); [aurasync.net Synapse guide](https://aurasync.net/how-to-use-razer-synapse/)); Armoury Crate has "Scenario Profiles" listed across every device category ([rog.asus.com/armoury-crate](https://rog.asus.com/pt/armoury-crate)). None of these *learn*; all require the user to configure. **ventd learning workload signatures automatically would be a genuine first in the consumer space.**

### 2.4 Datacenter/academic prior art

* **Google DeepMind (2016)**: 40 % reduction in data-center cooling energy by training a feedforward neural net on 2 years of data relating IT load, weather, cooling plant state to temperatures, then using it to set cooling setpoints ([summary in *Energy and Thermal-aware Resource Management of Cloud*, arXiv 2021](https://arxiv.org/pdf/2107.02342); patent filed 2018).
* **Task-thermal prediction with ANN**: [Zapater *et al.*, *Task scheduling with ANN-based temperature prediction in a data center*, Engineering with Computers 2011](https://link.springer.com/article/10.1007/s00366-011-0211-4) show ANN can predict workload thermal effect in real time for DC scheduling.
* **ProphetStor Federator.ai** ([prophetstor.com white paper](https://prophetstor.com/white-papers/ai-driven-data-center-cooling-google-vs-prophetstor/)) markets commercial predictive-analytics workload+cooling optimisation (flagged: this is a vendor marketing document, not a peer-reviewed claim).
* **Ramos & Bianchini, C-Oracle: Predictive Thermal Management for Data Centers, HPCA 2008** is the most-cited foundational proactive-cooling paper.
* **Coskun, Rosing & Gross, *Proactive Temperature Management in MPSoCs*, 2008** ([ACM DAC](https://dl.acm.org/doi/10.1145/1393921.1393966)) uses regression to estimate future temperature and pre-allocate workload, explicitly contrasting with reactive methods — this is the nearest conceptual relative to what ventd is trying to do, but targets task migration not fans.

### 2.5 Linux-side adjacent work

* **`schedutil`** cpufreq governor uses PELT utilisation estimates and util_est to select frequencies; it is *load-predictive* in a limited sense (load tracking is EWMA, not true prediction), and EAS adds an energy model on top ([kernel.org: Schedutil](https://docs.kernel.org/scheduler/schedutil.html), [Energy Aware Scheduling](https://www.kernel.org/doc/html/latest/scheduler/sched-energy.html)).
* **`uclamp`** (util clamping, 5.3+) allows userland to hint per-task performance floors/ceilings ([kernel.org: Utilization Clamping](https://docs.kernel.org/scheduler/sched-util-clamp.html)). ventd could, in principle, *read* these signals as additional inputs.
* **`auto-cpufreq`** monitors workload and battery to pick a governor; it **does not predict**, it reacts to current load ([itsfoss](https://itsfoss.gitlab.io/post/automatically-optimize-cpu-speed-and-power-with-auto-cpufreq-in-linux/), [ostechnix](https://ostechnix.com/optimize-performance-and-battery-life-with-auto-cpufreq/)).

---

## 3. Online learning and system identification

### 3.1 RLS

Viable in Go, well-understood, fits our ARX model class. Add:
- **Variable forgetting factor** tied to prediction-error energy (Paleologu et al., cited above) so the model "wakes up" when dynamics shift.
- **Covariance trace monitoring + resetting** to prevent windup during long idle periods.
- **Excitation test** (is u rich enough?) — skip updates when `||φ||` is below threshold, which prevents pathological drift when the machine is idle.

### 3.2 Kalman / EKF for joint state-parameter

The Matthies pattern: θ_{k+1} = θ_k + w_k (random walk), augment state, run EKF. Literature uses this for battery, HVAC, motor thermal. Numerical concerns in float64 pure Go are manageable with square-root / UD forms if we observe instability; gonum's `mat.Cholesky` and `mat.SVD` are exported and CGO-free. Confusingly, several classical RLS variants are exactly equivalent to particular Kalman filters ([Sayed & Kailath, as summarized in Paleologu's related work](https://www.semanticscholar.org/paper/A-Robust-Variable-Forgetting-Factor-Recursive-for-Paleologu-Benesty/438730e9aa0e30de4f2f8f0120bc03d608e3ba3f)); we can start with RLS and upgrade to EKF when we need to add state (e.g., estimated ambient) without rewriting.

### 3.3 Controller tuning

Three viable approaches for tuning the PI that rides the learned plant:

1. **IMC (Internal Model Control) analytic tuning.** Once we have a FOPDT (first-order plus dead-time) fit with gain K, time constant τ, and dead time θ, the IMC-PI rules give K_c and τ_I as a closed-form function of a single user parameter λ (desired closed-loop time constant) ([Rivera, Morari & Skogestad, *Internal Model Control 4: PID Controller Design*; summary at apmonitor.com](https://apmonitor.com/pdc/index.php/Main/ProportionalIntegralControl)). This is **robust, predictable, needs no disturbance** — ideal for ventd: after the plant model is learned, controller gains are a one-line formula. This is our primary tuning path.
2. **Åström-Hägglund relay autotuning.** Replaces the controller with a bang-bang relay, observes the resulting limit cycle, extracts ultimate gain and period, derives PI gains via Ziegler-Nichols-style rules — but **without** driving the plant to the instability boundary ([Control Engineering: *Relay Method Automates PID Loop Tuning*](https://www.controleng.com/articles/relay-method-automates-pid-loop-tuning/); [Warwick review](https://warwick.ac.uk/fac/cross_fac/iatl/research/reinvention/archive/volume5issue2/hornsey/)). Excellent for *initial fan calibration* (bang fan low/high, watch temperature swing, extract dynamics without a user-visible instability) but not for continuous live operation.
3. **Classical Ziegler-Nichols.** Not safe on a live machine (requires marginal stability test). Reject.

### 3.4 Drift detection

Thermal-plant drift in homelab deployments happens: dust accumulates, thermal paste dries, a fan dies, ambient swings 15 °C between summer and winter, a user swaps a heatsink.

Standard streaming-drift detectors:
- **Page-Hinkley test** (CUSUM on residuals): very cheap, one running mean + accumulator, robust for gradual drift ([de Gruyter-Brill 2025 review](https://www.degruyterbrill.com/document/doi/10.1515/eng-2025-0158/html?lang=en); [OneUptime](https://oneuptime.com/blog/post/2026-01-30-concept-drift-detection/view)).
- **ADWIN** (adaptive windowing): dynamically adjusts a reference window; excellent at abrupt drift; moderate memory ([Mahgoub et al., MEDI 2022](https://link.springer.com/chapter/10.1007/978-3-031-21595-7_4)).
- **KSWIN** (Kolmogorov-Smirnov sliding windows): non-parametric distribution shift.

Page-Hinkley on the prediction residual is the cheapest useful signal and is what ventd should start with; ADWIN is a natural upgrade. Both have trivial pure-Go implementations; a Go port is small (the reference `blablahaha/concept-drift` is Python but is ~200 lines total — straightforward to translate: [blablahaha/concept-drift](https://github.com/blablahaha/concept-drift)).

### 3.5 Hardware-change detection

Good cache key (most-to-least stable):

1. **DMI/SMBIOS fingerprint** (`/sys/class/dmi/id/`: `sys_vendor`, `product_name`, `product_serial` where readable, `board_vendor`, `board_name`, `bios_version`). Pure-Go access via [mdlayher/go-smbios](https://mdlayher.com/blog/accessing-smbios-information-with-go/) (no CGO) or simply reading the sysfs files. Board serial is usually DMI-unique when firmware fills it. SMBIOS type-17 memory devices and type-4 processor info add additional discriminators ([go-dmidecode](https://pkg.go.dev/github.com/fenglyu/go-dmidecode)).
2. **Enumerated hwmon chips** (chip name, channel count, PWM/tacho presence) — detect fan replacements and cooler swaps.
3. **CPU model** (`/proc/cpuinfo`: vendor_id, model name, cache sizes), **number of cores**, **GPU PCI IDs + VBIOS**.
4. **Kernel version** (only for invalidating cached sysfs layout assumptions).

Strategy: compose the top-level fingerprint as SHA-256 of the stable tuple; store model under `state/<fingerprint>/model.*`; on mismatch, optionally **graft** from the closest historical profile (same product_name, different bios_version = likely safe to warm-start; different board entirely = cold start). Homelab users running identical nodes can **export and import** a profile file keyed by fingerprint.

### 3.6 Safety for online learning

The canonical failure mode is "learned fans-off works" during a 30-minute cool-idle window, then the machine cooks when a workload hits.

Defences:

* **Hard physical envelope** that always overrides the model: `temp > T_crit ⇒ PWM = 100 %`, `temp > T_crit − 5 ⇒ PWM ≥ aggressive_floor(T, per-model)`. This is a separate code path that runs even if the learned controller produces NaN.
* **Parameter box constraints.** R ∈ [R_min, R_max] determined from physics (any real CPU cooler has bounded R); clamp after every RLS update.
* **Shadow control.** Run new controller in shadow, current controller authoritative. Compare residuals over a confidence window (e.g. 24 h of observations covering idle + load). Promote only when shadow residuals are statistically ≤ live residuals on a held-out metric. This is the exact pattern used in cyber-physical RL safety research ([*Stepping Out of the Shadows: RL in Shadow Mode*, arXiv 2410.23419](https://arxiv.org/abs/2410.23419); [Guissouma *et al.*, *Continuous Safety Assessment of Updated Supervised Learning Models in Shadow Mode*, IEEE 2023](https://ieeexplore.ieee.org/document/10092729/)) and in autonomous-vehicle shadow-testing. It is also what HPC-AK-MPC uses for thermal/process control with learned Koopman models and "historical process constraints" ([Wu *et al.*, arXiv 2506.08983](https://arxiv.org/abs/2506.08983)).
* **Bounded exploration** during calibration: sweep PWM only while temperatures are comfortably below a safe margin and abort immediately on exceedance.
* **Fallback curves.** Store a conservative static temperature→PWM table per-platform family (read-only, shipped with ventd). If learning diverges for >N cycles, fall back to this table while emitting a loud log/dbus signal.
* **Monitoring.** Expose prediction error, forgetting factor, parameter magnitudes, time-since-last-model-update via Prometheus and a Unix socket — users (and ventd itself, via Page-Hinkley) need to know.

This composite pattern is what mature automotive/HVAC/process-control code does; it is under-appreciated in consumer software and would differentiate ventd strongly.

---

## 4. Storage, persistence, and portability

### 4.1 Format

Requirements: forward-compatible, versioned, human-inspectable for debug, small.

Trade-offs:

| Format | Human-readable | Fwd-compat | Size | Schema | Verdict |
|---|---|---|---|---|---|
| **JSON** | Yes | Manual (ignore unknown fields) | Bulky | Optional | Best for v1 — instantly debuggable |
| **JSON + schema-version field** | Yes | Easy to migrate | Bulky | Soft | **Recommended** |
| Protobuf | No (needs `protoc --decode`) | Strong (field numbers; additive changes safe) | Compact | Required | Good for v2 if model grows |
| FlatBuffers | No | Strong, zero-copy | Slightly larger than proto | Required | Overkill (fan daemon isn't latency-bound on state I/O) |
| CBOR / MsgPack | Partially | Medium | Compact | Soft | Middle ground, no big win |

Protobuf's additive-only evolution guarantees (adding fields, renaming fields, removing fields safely via reserved numbers) are real and well-documented ([Dawson, *Learning Notes: Protobuf vs JSON*](https://andrewjdawson2016.medium.com/learning-notes-protobuf-vs-json-serialization-2dcc26b063dd); [daminibansal, Moving from JSON to Protobuf](https://daminibansal.medium.com/moving-from-json-to-protocol-buffers-protobuf-when-and-why-ea61701072eb)). But for a debuggable homelab daemon with KB-scale model state, **JSON with an explicit `"schema_version": N` field and a documented migration path** wins on debuggability: `cat`, `jq`, `git diff` all work. FlatBuffers' zero-copy is irrelevant here.

**Writes must be atomic.** Temp-file + `rename(2)` (which is atomic on Linux within a filesystem) is the standard pattern. Pure-Go libraries: `natefinch/atomic` ([github.com/natefinch/atomic](https://github.com/natefinch/atomic); already a direct dep of fan2go, confirming ecosystem alignment: [fan2go/go.mod](https://github.com/markusressel/fan2go/blob/master/go.mod)), `google/renameio` ([pkg.go.dev/github.com/google/renameio](https://pkg.go.dev/github.com/google/renameio)), `facebookgo/atomicfile`. Use one; don't roll your own.

### 4.2 Location

For a **system** daemon running under systemd, the XDG Base Directory spec says state belongs analogous to `/var/lib` ([freedesktop.org XDG BDS](https://specifications.freedesktop.org/basedir/latest/); Arch wiki notes `$XDG_STATE_HOME` defaults to `$HOME/.local/state` *for user apps*, analogous to `/var/lib` for system apps: [ArchWiki XDG Base Directory](https://wiki.archlinux.org/title/XDG_Base_Directory)).

**Recommendation:**
- System daemon (default): `/var/lib/ventd/` (model, calibration, fingerprint cache) + `/etc/ventd/` (user config) + `/run/ventd/` (pid, socket, live metrics).
- User-mode (`--user` for desktop users): `$XDG_STATE_HOME/ventd/` (default `~/.local/state/ventd/`), `$XDG_CONFIG_HOME/ventd/`, `$XDG_RUNTIME_DIR/ventd/`.

### 4.3 Layout

    /var/lib/ventd/
      fingerprint.json              # DMI+hwmon hash, ventd version that wrote it
      platform/<fingerprint>/
        model.json                  # learned thermal model (versioned)
        model.json.bak              # previous generation, on rollback
        fan-calibration.json        # RPM-vs-PWM curve, min-spin, stall hysteresis
        workloads.json              # learned workload signatures
        motifs.json                 # scheduled events from matrix-profile pass
        telemetry/
          recent.bin                # 1 Hz ring buffer, 7 days, for offline motif mining

### 4.4 Migration

Each file has `"schema_version": N`. At daemon start, load. If `N < current`, run the chain of `migrate_N_to_N+1` pure functions; atomically write back. If `N > current` (downgrade), refuse to touch and fall back to safe defaults with a warning — never corrupt newer state.

### 4.5 Export/import

`ventd model export > node.json` / `ventd model import node.json` produces a single file keyed by fingerprint with the learned model + calibration. Homelab users running identical hardware can warm-start every node; if the target fingerprint differs, import as "seed" rather than "cache" (model is used only until drift detection kicks in).

---

## 5. Prior art — what is actually shipped vs. academic

### thermald (Intel)
Runs as a user-space daemon on Intel Sandy-Bridge-and-newer CPUs. Monitors `/sys/class/thermal` and coretemp, binds sensors to cooling devices (P-state, power clamp, RAPL, T-state), activates cooling actions when temperatures cross trip points defined in `thermal-conf.xml(.auto)` ([Intel thermal_daemon on GitHub](https://github.com/intel/thermal_daemon); [Arch Linux man page](https://man.archlinux.org/man/thermald.8)). Phoronix benchmarks demonstrate it materially improves out-of-box thermal/power behaviour on Tiger Lake ([*The Importance of Thermald on Linux for Modern Intel Tiger Lake Laptops*, Phoronix 2021](https://www.phoronix.com/review/intel-thermald-tgl)). **Design verdict:** thermald is "proactive" only in the weak sense that it acts before the BIOS hard-throttles — it is fundamentally **trip-point reactive** driven by a static XML table. No learned model, no prediction beyond a short look-ahead embedded in Intel DPTF. It controls CPU knobs, not case fans. Intel-only. Written in C++.

### fancontrol (lm-sensors)
Static PWM tables in `/etc/fancontrol`, linear interpolation between temperature points. Zero prediction ([ArchWiki Fan speed control](https://wiki.archlinux.org/title/Fan_speed_control)).

### fan2go (markusressel/fan2go)
The closest existing Go analog. 264 stars, pure Go, YAML config, hwmon + NVML sensors, per-fan user-defined curves, rolling-average-sized smoothing windows, control-algorithm per fan, auto-detect stall RPM, Prometheus exporter, REST API, atomic config writes via `natefinch/atomic` ([fan2go README on GitHub](https://github.com/markusressel/fan2go); [go.mod](https://github.com/markusressel/fan2go/blob/master/go.mod)). Uses proportional-style closed-loop-per-fan to track a curve target. **No thermal model, no workload awareness, no prediction** — it is a well-engineered reactive curve controller. This is the bar ventd aims to surpass; fan2go's hwmon/NVML/fan-characterisation code is an excellent reference for the *non-predictive* layers of ventd.

### CoolerControl (codifryed/coolercontrol)
Written in Rust daemon (`coolercontrold`) plus TS/Vue UI. System + web UI, auto-detects hwmon/liquidctl/NVIDIA/AMD. Fixed / Graph / Mix / Overlay profiles, hysteresis, thresholds, directionality, response-time tuning ([docs.coolercontrol.org: Profiles](https://docs.coolercontrol.org/config-basic/profiles.html); [Hardware Support](https://docs.coolercontrol.org/hardware-support.html); [GamingOnLinux 2026 release coverage](https://www.gamingonlinux.com/2026/04/coolercontrol-4-2-adds-auto-detection-of-new-devices-stress-testing-and-more/)). Wide feature surface, but still a **reactive curve controller with shaping functions** — no thermal model, no learning, no workload prediction. Strong reference for features users expect.

### liquidctl
Python userspace for AIO coolers and RGB — set curves, set colours, initialise devices ([liquidctl(8) man page](https://www.mankier.com/8/liquidctl); [github.com/liquidctl/liquidctl](https://github.com/liquidctl/liquidctl)). Pure fan/pump curve control. No learning.

### auto-cpufreq
Observes battery/load/thermal to pick a cpufreq governor. **Reactive, not predictive** ([ostechnix guide](https://ostechnix.com/optimize-performance-and-battery-life-with-auto-cpufreq/)).

### NVIDIA Dynamic Boost / AMD SmartShift
Hardware-firmware features that redistribute power budget between CPU and GPU on shared-cooling-solution laptops. Dynamic Boost 2.0 is described by NVIDIA as "AI to balance power" but the per-frame adjustments are documented to be **generic signal-driven, not per-game profiled** ([AnandTech, *NVIDIA Details Dynamic Boost Tech*, 2020](https://www.anandtech.com/show/15692/nvidia-details-dynamic-boost-tech-and-advanced-optimus); PCWorld measured ~15 W GPU budget shift: [*Up close with Nvidia's Dynamic Boost*](https://www.pcworld.com/article/393737/up-close-with-nvidias-dynamic-boost-feature-for-gaming-laptops.html)). Linux-side: `nvidia-powerd` daemon ships with driver 510+ ([NVIDIA Dynamic Boost on Linux, README](https://download.nvidia.com/XFree86/Linux-x86_64/510.47.03/README/dynamicboost.html)). AMD SmartShift works at the Infinity-Fabric level between Ryzen + Radeon; patches exposing it via sysfs landed in 2021 ([Tom's Hardware coverage](https://www.tomshardware.com/news/amd-smartshift-linux-support)). Both use **short-horizon feedback on instantaneous load**, not learned workload signatures. Flag: NVIDIA's marketing calls this "AI" but the technical docs describe a signal-responsive controller, not a learned-workload model.

### Academic / industrial predictive thermal control
- **MPC for multicore thermal management.** [Bartolini *et al.*, *Thermal Control of Manycore and Multicore Processors*, IEEE CSS book chapter](https://ieeecss.org/sites/ieeecss/files/2019-06/IoCT2-RC-Bartolini-1.pdf); [Zanini et al., *Multicore Thermal Management with MPC*, ECCTD 2009](https://si2.epfl.ch/~demichel/publications/archive/2009/C2L-C3-9245.pdf); [Bambini et al., *A two-layer distributed MPC approach*, Control Engineering Practice 2022](https://www.sciencedirect.com/science/article/abs/pii/S0967066122000235); [*Hybrid DTM with MPC*, IEEE 2015](https://ieeexplore.ieee.org/document/7032888/).
- **Proactive workload-based thermal management.** Coskun, Rosing & Gross, *Proactive Temperature Management in MPSoCs*, 2008 — direct intellectual ancestor of ventd's long-horizon idea.
- **Feedback thermal control on real-time systems.** [Fu *et al.*, *Feedback Thermal Control of Real-time Systems on Multicore Processors*, EMSOFT 2012](https://www.cse.wustl.edu/~lu/papers/emsoft12.pdf).
- **Google DeepMind 2016 data-centre cooling**; Microsoft Resource Central workload prediction.
- **Learning-based predictive HVAC control.** [Terzi *et al.*, *Learning-based predictive control of cooling system of a large business centre*, Control Engineering Practice 2020](https://katalog.lib.cas.cz/KNAV/EdsRecord/edselp,S0967066120300307); industrial HVAC MPC (gray-box RC + MPC) is mature — [review in *A predictive control approach for thermal energy management in buildings*, Energy Reports 2022](https://www.sciencedirect.com/science/article/pii/S2352484722013038) — and the *same* gray-box-RC-plus-MPC recipe transfers 1:1 to CPU-cooler thermals at a *shorter* time-scale. Flag: many of these papers are simulation-validated; the ones with physical deployment (Terzi; DeepMind; Microsoft RC) are consistent with what ventd proposes.

**Gap analysis.** In open-source Linux fan control, there is literally nothing today between "static curve" and "academic paper simulation". A predictive, learning, workload-aware daemon is genuinely novel territory at ship scale — the techniques are not novel, the *packaging for homelab Linux* is.

---

## 6. Go (CGO_ENABLED=0) ecosystem for ML / signal / control

| Need | Library | CGO-free? | Status |
|---|---|---|---|
| Dense linear algebra | **`gonum.org/v1/gonum/mat`** | Yes (native path) | Mature, production; fan2go-ecosystem-grade ([gonum.org](https://www.gonum.org/)) |
| Statistics | **`gonum.org/v1/gonum/stat`** | Yes | Mature |
| Optimisation (nonlinear) | `gonum.org/v1/gonum/optimize` | Yes | Mature — usable for offline PI tuning if IMC closed-form insufficient |
| Kalman filter | **`rosshemsley/kalman`** | Yes | Simple, non-uniform time, Brownian model ([pkg.go.dev](https://pkg.go.dev/github.com/rosshemsley/kalman)) |
| Kalman filter (explicit A/B/C/D) | **`konimarti/kalman`** | Yes | Adaptive, production-used ([konimarti/kalman](https://github.com/konimarti/kalman)) |
| K-means / DBSCAN / OPTICS | **`mpraski/clusters`** | Yes | Includes online k-means++ ([pkg.go.dev](https://pkg.go.dev/github.com/mpraski/clusters)) |
| Online distance clustering | `wearelumenai/distclus` | Yes | MCMC + streaming |
| Matrix Profile / motifs | **`matrix-profile-foundation/go-matrixprofile`** | Yes | STOMP/SCRIMP for scheduled-pattern mining ([GitHub](https://github.com/matrix-profile-foundation/go-matrixprofile)) |
| Gaussian process (probabilistic) | `bitbucket.org/dtolpin/gogp` (Infergo) | Yes | Research-grade — skip for v1 |
| Neural nets | Gorgonia / gorgonia-tensor | Partial (BLAS optional; CUDA requires CGO) | Overkill for v1 |
| Neural nets | `born-ml/born`, `zerfoo/zerfoo` | Yes | LLM-focused, overkill for v1 |
| Tiny MLP from scratch | `gonum/mat` + hand-rolled | Yes | Correct fit if we need MLP |
| eBPF | **`cilium/ebpf`** | **Yes** (zero C dependencies at runtime) | Canonical ([github.com/cilium/ebpf](https://github.com/cilium/ebpf)) |
| Netlink (proc connector, generic) | **`vishvananda/netlink`** | Yes | Production ([proc_event_linux.go](https://github.com/vishvananda/netlink/blob/main/proc_event_linux.go)) |
| NVML (NVIDIA GPUs) | **`NVIDIA/go-nvml`** requires CGO | **No** — must load via `purego` | Use `jwijenbergh/purego` or `ebitengine/purego` to dlopen `libnvidia-ml.so.1` (the library the user already ships with the driver); this is explicitly the pattern the question specifies and matches the constraint ([NVIDIA/go-nvml](https://github.com/NVIDIA/go-nvml); [purego docs](https://pkg.go.dev/github.com/ebitengine/purego)) |
| SMBIOS/DMI | `mdlayher/go-smbios` | Yes | Pure-Go DMI decoder ([blog.gopheracademy.com](https://blog.gopheracademy.com/advent-2017/accessing-smbios-information-with-go/)) |
| Atomic file write | **`natefinch/atomic`** (fan2go uses this) / `google/renameio` | Yes | Trivial, correct |
| Hardware sensors | `md14454/gosensors` (fan2go uses it) | Yes | Wraps hwmon sysfs |
| PSI / pressure | stdlib `/proc/pressure/*` read | Yes | Kernel 4.20+ ([docs.kernel.org/accounting/psi.html](https://docs.kernel.org/accounting/psi.html)) |
| RAPL / powercap | stdlib `/sys/class/powercap/intel-rapl:*/energy_uj` read (root required post-CVE-2020-8694: [Intel RAPL advisory](https://www.intel.com/content/www/us/en/developer/articles/technical/software-security-guidance/advisory-guidance/running-average-power-limit-energy-reporting.html)) | Yes | ventd already runs root |

**Signal processing.** There is no single dominant Savitzky-Golay or IIR filter package in Go; standard practice is to implement directly on gonum (SG coefficients are a small precomputed table; Wikipedia and Eigenvector tech notes document the derivation fully: [Savitzky–Golay filter on Wikipedia](https://en.wikipedia.org/wiki/Savitzky%E2%80%93Golay_filter)). This is ~100 lines in Go. An ordinary EWMA works fine for most of what ventd needs; SG is only useful if we want accurate first-derivative estimates.

---

## 7. Safety and correctness patterns from adjacent industries

### 7.1 Shadow control
The dominant pattern in safety-critical online-learning software. New controller runs in parallel, produces outputs logged but not applied, statistics are compared over a confidence window, promotion is explicit and auditable. Shipped in autonomous-driving stacks, used in continuous safety-assessment literature ([Guissouma et al. 2023, IEEE](https://ieeexplore.ieee.org/document/10092729/)), and formalised for RL-in-shadow-mode in robotics/CPS ([*Stepping Out of the Shadows*, arXiv 2410.23419](https://arxiv.org/abs/2410.23419); [TUM proposal](https://www.ce.cit.tum.de/fileadmin/w00cgn/air/_my_direct_uploads/Proposal_safe_SM.pdf)).

### 7.2 Hard envelope + soft controller
Cars (ESC/ABS), HVAC (high/low-temp cutouts), process control (alarm trip logic) all wrap the learned/tuned control loop in a non-learned safety layer that physically cannot be disabled by the controller. ventd's version: **dumb temperature→PWM clamp runs in a separate goroutine, updates PWM if the learned controller underperforms the clamp, and a `safety_floor` pwm minimum is enforced at write time**.

### 7.3 Bounded exploration
In industrial autotuning (relay feedback tuning, Åström-Hägglund) oscillation amplitude is *bounded* by construction. The PID-autotuner community has multiple such ports ([jackw01/arduino-pid-autotuner](https://github.com/jackw01/arduino-pid-autotuner) as a simple readable reference). For ventd's fan-characterisation bootstrap, sweep PWM only within a pre-agreed thermal band and abort on deviation.

### 7.4 Failsafe state
On any of: NaN/Inf in model, residual SD > 3× historical, no sensor update for N seconds, systemd reload, SIGHUP — **jump straight to the conservative default curve** and log loudly. This is standard "bumpless transfer + hardcoded fallback" ECU/PLC practice.

### 7.5 Trust thresholds
Don't enable predictive mode until calibration has seen ≥ X distinct thermal regimes (idle, light, heavy, sustained) and shadow residual is ≤ baseline. Expose per-feature "trust" flags to the user and to Prometheus.

---

## 8. Recommended architecture for ventd

### 8.1 Signal acquisition layer

A single goroutine per "source family" publishes to a typed channel:

* **hwmon** (coretemp, k10temp, mobo sensors, PWM/tacho): 1 Hz sysfs read.
* **cpufreq** per-core scaling_cur_freq: 1 Hz.
* **utilisation** from `/proc/stat`: 1 Hz delta.
* **RAPL** from `/sys/class/powercap/intel-rapl:*/energy_uj`: 1 Hz delta → power in watts ([kernel-internals.org RAPL cheatsheet](https://kernel-internals.org/power/power-capping/)); falls back gracefully on AMD without RAPL.
* **PSI** `/proc/pressure/{cpu,memory,io}`: 1 Hz read of avg10 ([kubernetes.io PSI doc](https://kubernetes.io/docs/reference/instrumentation/understand-psi-metrics/)).
* **GPU**: NVML via purego dlopen of `libnvidia-ml.so.1` for NVIDIA; `/sys/class/drm/cardN/device/hwmon/` and AMDGPU sysfs for AMD.
* **disk/net**: `/proc/diskstats`, `/proc/net/dev` deltas at 1 Hz.
* **process events**: netlink proc connector (fork/exec/exit) → event channel, with eBPF `sched_process_exec` as higher-fidelity alternative when available.
* **DMI fingerprint**: read once at startup.

A central **sample aggregator** emits a consolidated `Sample{t, per_core_T, per_core_freq, per_core_util, package_power, psi, gpu_*, disk_*, net_*, pwm_*, rpm_*}` at 1 Hz (configurable to 2–5 Hz for short-horizon accuracy).

### 8.2 Short-horizon prediction layer — **ARX + RLS with feedforward**

* **Model structure per fan-zone:** second-order ARX with inputs (package_power, Σutilisation, ambient proxy, fan_pwm_k-1), output (representative zone temperature). Regressor dimension 8–12.
* **Online estimator:** RLS with variable forgetting factor (VFF-RLS, Paleologu 2008), covariance trace clamp, skip-on-low-excitation rule.
* **Equivalently implementable as a 4-state Kalman filter** (state = [T, Ṫ, R_est, C_est]); keep both code paths behind an interface and pick by benchmark.
* **Feedforward contribution** to fan PWM: `k_fp · dPdt + k_fu · d(util)/dt`, with k_fp, k_fu learned by relating step-response overshoot to feedforward magnitude.
* **Controller:** PI whose gains come from IMC tuning on the identified FOPDT approximation, with output bounded to `[pwm_floor(T), 100]`. λ (closed-loop time constant) exposed as the single user-facing "aggressiveness" knob.

### 8.3 Long-horizon layer — **signatures + motifs**

* On every `exec` event, log `(t, comm, basename(exec_path), uid, cgroup, argv_hash_optional)` in a ring buffer.
* Every N seconds after exec, compute the realised Δpower/Δtemp/duration vs. pre-exec baseline. Update a Bayesian per-signature estimator.
* When a known-heavy signature execs again, **pre-spin fans** by injecting a temporary additive into the PI setpoint error, shaped as `expected_Δpower × learned_k_ff`, decayed over `expected_duration`.
* **Matrix-profile motif mining** runs offline (e.g. nightly) over the past 7 days of 1 Hz telemetry using `go-matrixprofile` — discovered motifs become "scheduled events" pre-warming the cooling system at their learned trigger times.
* No user configuration required. The signature set exports/imports cleanly.

### 8.4 Safety envelope

Three layers, checked at PWM write time:

1. **Hard cap**: `if temp > T_crit − 5 ⇒ PWM = 100`. Non-overrideable.
2. **Per-platform conservative fallback curve**: used when model not trusted or residuals spike; shipped as static data keyed by vendor/family.
3. **Parameter box-clamp** on R/C/feedforward gains from physics-derived priors; clamp after every RLS step.

Drift detector (Page-Hinkley on residuals) demotes the learned controller to the fallback curve on trip; triggers recalibration.

### 8.5 Persistence

* `/var/lib/ventd/platform/<fingerprint>/model.json` with `schema_version` field, atomic-written via `natefinch/atomic`.
* Telemetry ring buffer in a compact binary format (gonum's encoding or a simple per-field packed layout) — 7 days × 1 Hz × ~30 scalars × 8 B ≈ 145 MB max, gzip-compressed to ~20 MB in practice; configurable down.
* `ventd model export/import` for sharing across identical nodes.

### 8.6 Phased implementation

**Phase 0 — "fan2go+": deliver a reactive-but-better-than-fan2go baseline.**
- hwmon/NVML(purego)/AMDGPU sysfs acquisition.
- Auto-detection and calibration of fans (min-spin, stall hysteresis, RPM-vs-PWM curve) via bounded relay sweep.
- Feed-forward on dP/dt and d(util)/dt atop a conservative static curve.
- Hard safety cap.
- JSON state file, atomic writes.
- Prometheus metrics, Unix socket, systemd unit.
Shipping this alone already surpasses reactive curve tools because of the feed-forward term.

**Phase 1 — short-horizon predictive.**
- ARX model + RLS with VFF.
- IMC-tuned PI.
- Shadow-mode evaluation vs. Phase-0 controller; promote only after residual/overshoot criteria met.
- Page-Hinkley drift detector with auto-demote to fallback curve.

**Phase 2 — long-horizon predictive.**
- Netlink proc-connector (pure Go via `vishvananda/netlink`).
- Per-signature thermal-event statistics.
- Pre-warm PI setpoint on recognised exec.
- `ventd profile export/import`.

**Phase 3 — motif mining and advanced control.**
- Offline matrix-profile scheduled-event discovery via `go-matrixprofile`.
- Optional eBPF exec tracer via `cilium/ebpf` for per-thread granularity.
- Optional EKF for joint state+ambient estimation.
- Evaluate explicit MPC (offline QP solution, lookup at runtime) as a v3 controller upgrade.

### 8.7 Libraries to use, not build

- `gonum.org/v1/gonum/mat` and `/stat` (linear algebra, stats)
- `konimarti/kalman` *or* `rosshemsley/kalman` (Kalman, if we go that route)
- `cilium/ebpf` (optional phase-3 exec tracer)
- `vishvananda/netlink` (proc-connector, hwmon enumeration helpers)
- `ebitengine/purego` (NVML dlopen — exactly the project's stated constraint)
- `mdlayher/go-smbios` (DMI fingerprint)
- `natefinch/atomic` (atomic file write; same choice fan2go made)
- `md14454/gosensors` or direct sysfs (hwmon)
- `prometheus/client_golang` (metrics, matching fan2go/CoolerControl convention)
- `matrix-profile-foundation/go-matrixprofile` (offline motif mining, phase 3)
- `mpraski/clusters` (if/when signature clustering needed, phase 2–3)

### 8.8 Libraries/approaches to reject

- **Gorgonia / Zerfoo / Born** for v1–v2: too heavy, wrong abstraction for a small regression problem.
- **LSTM/GRU/Transformer** predictors: mismatched to the task; linear ARX is within 1–2 % of what nonlinear models achieve on thermal plants at these horizons while costing 1000× less.
- **Full Gaussian Process**: O(N²) per step, no pure-Go production lib, uncertainty approximable by RLS covariance.
- **NVIDIA/go-nvml** directly (CGO). Use the dlopen+purego pattern.
- **Reimplementing Kalman from scratch** unless the existing libs' APIs are inadequate.
- **Protobuf for v1 state**: unjustified opacity; JSON with `schema_version` is enough.

### 8.9 What is genuinely novel vs. good engineering

**Not novel (well-engineered application of existing techniques):**
- Pure-Go implementation on Linux (CoolerControl is Rust; thermald is C++; fan2go Go-but-reactive — but the *techniques* ventd composes have decades of literature).
- RC thermal model + RLS / IMC / PI — textbook.
- Shadow control, hard envelope, drift detection — textbook safety.
- Process-launch-triggered profiles — Windows gaming software has had them for a decade.
- Exec detection via proc-connector or eBPF — standard Linux tracing.

**Genuinely novel for an end-user product:**
- **Autolearning workload-signature anticipation applied to fan control, zero-config.** Windows tools require the user to map executables to profiles; ventd learns the mapping from observed thermal effect.
- **Cross-node homelab profile sharing keyed by DMI fingerprint.** Nobody ships this; it leverages the specific homelab/NAS pattern of identical-hardware clusters.
- **True feedforward pre-spin on dP/dt before temperature moves**, in a consumer-facing Linux daemon.
- **Matrix-profile-based scheduled-event anticipation** (pre-warm fans for 03:00 backup). I could not find any deployed fan controller that does this.
- **The packaging**: zero-config calibration + learning + safety envelope + shadow-mode rollout, all in a single systemd unit, is itself the novel contribution relative to the state of the art in open source.

The "world's first truly predictive fan control" claim is defensible **with precise framing**: *first shipping Linux fan controller that learns a thermal plant model online, anticipates short-horizon thermal spikes via feed-forward on power derivatives, anticipates long-horizon events via learned workload signatures and motif discovery, and does so with no user configuration.* Each individual technique has academic or industrial precedent; their open-source integration into a pure-Go daemon for homelab Linux does not.

---

## Proposed spec outline (spec-05-predictive-thermal.md)

> **SPEC-05: Predictive Thermal Control**
>
> **Status:** Draft
> **Depends on:** spec-01 (architecture), spec-02 (signal acquisition), spec-03 (fan calibration), spec-04 (control loop)
> **License:** GPL-3.0
>
> ### 1. Goals
> - 1.1 Anticipate short-horizon thermal spikes (5–30 s) via learned plant + feedforward.
> - 1.2 Anticipate long-horizon thermally-heavy workloads (30 s–5 min) via exec signatures + motif discovery.
> - 1.3 Preserve the zero-config, lightweight, pure-Go, safe-by-default ventd ethos.
>
> ### 2. Non-goals
> - 2.1 Not a general-purpose ML framework.
> - 2.2 Not a replacement for thermald (we complement it).
> - 2.3 No DVFS control (we manage fans and pumps only).
>
> ### 3. Signal acquisition contract
> - 3.1 Sample cadence: 1 Hz default, 2 Hz high-resolution mode.
> - 3.2 Mandatory signals: per-core T, per-core util, PWM, RPM, RAPL power (x86) OR AMD equivalent, ambient proxy.
> - 3.3 Optional: PSI {cpu,mem,io}, NVML via purego, AMDGPU sysfs, /proc/diskstats, /proc/net/dev, process events via netlink proc-connector (fallback: /proc polling), DMI fingerprint.
> - 3.4 All sysfs reads wrapped behind typed interfaces; fakes for testing.
>
> ### 4. Short-horizon model
> - 4.1 Second-order ARX, regressor dimension ≤ 12, one model per fan-zone.
> - 4.2 Online estimator: RLS with variable forgetting factor 0.95–0.999; covariance trace clamp.
> - 4.3 Feed-forward on smoothed dP/dt, d(util)/dt using Savitzky-Golay derivative on a 5-sample window.
> - 4.4 Controller: PI tuned by IMC rule on the ARX-derived FOPDT approximation; λ exposed as `aggressiveness ∈ {quiet, balanced, responsive}`.
> - 4.5 Numerically bounded parameters (R, C, feed-forward gains) with physics-informed box constraints.
>
> ### 5. Long-horizon model
> - 5.1 Process-launch source: netlink proc-connector (primary), eBPF sched_process_exec (optional), /proc polling (fallback).
> - 5.2 Signature key: `sha256(basename(exec_path) || cgroup_leaf || uid)`; optional argv-template token bag.
> - 5.3 Per-signature Bayesian stats (count, mean/var Δpower, mean/var Δtemp, mean/var duration, last_seen).
> - 5.4 "Heavy workload" predicate: `mean_Δpower > platform_heavy_threshold AND count ≥ 3 AND last_seen < 30 days`.
> - 5.5 On recognised heavy exec: inject additive to setpoint-error signal, decaying over `min(expected_duration, 5 min)`.
> - 5.6 Motif discovery: offline STOMP via go-matrixprofile on past 7 days of telemetry; "scheduled events" with learned trigger times.
>
> ### 6. Safety envelope (non-overrideable)
> - 6.1 Hard cap: `T > T_crit − 5 ⇒ PWM = 100`.
> - 6.2 Fallback conservative curve per platform family, shipped as static data.
> - 6.3 Drift detector: Page-Hinkley on prediction residual; on trip, demote to fallback curve, log, attempt recalibration.
> - 6.4 Shadow-mode promotion: new model runs in parallel for ≥ 24 h with at least one observed idle→load→idle cycle; promoted only if residual-SD ≤ live controller and overshoot ≤ live controller.
> - 6.5 NaN/Inf/staleness detector → instant fallback curve.
>
> ### 7. Persistence
> - 7.1 JSON with `schema_version`; versioned migration functions.
> - 7.2 Path: `/var/lib/ventd/platform/<dmi_fingerprint>/{model,fan-calibration,workloads,motifs}.json`; user-mode falls back to `$XDG_STATE_HOME/ventd/`.
> - 7.3 Atomic write via `natefinch/atomic` (tempfile + rename).
> - 7.4 `ventd model export | import` for cross-node profile sharing keyed by fingerprint.
>
> ### 8. Observability
> - 8.1 Prometheus metrics: prediction residual, forgetting factor, parameter vector, feedforward contribution, shadow-vs-live deltas, drift events, time-since-last-update, fallback activations.
> - 8.2 Unix socket debug API: dump current model JSON.
> - 8.3 Structured logs at INFO, DEBUG, TRACE.
>
> ### 9. Phased rollout
> - Phase 0: reactive + feedforward + safety envelope.
> - Phase 1: short-horizon ARX/RLS + IMC-PI + shadow mode + drift detection.
> - Phase 2: exec signatures + pre-warm setpoint bias + export/import.
> - Phase 3: matrix-profile scheduled events + optional eBPF tracer + optional explicit MPC controller.
>
> ### 10. Testing
> - 10.1 Offline replay of recorded telemetry against known workloads (compile, game, backup, VM boot) — assert overshoot < X °C, RMS error < Y °C, fan-spin lead time > Z seconds vs. pure-curve baseline.
> - 10.2 Property tests on safety envelope: hard cap always fires when temp > limit, regardless of model outputs (including fuzzed NaN/Inf/negative gains).
> - 10.3 Fault injection: sensor dropout, RAPL access revoked, hwmon chip disappearing, DMI fingerprint change → daemon must degrade gracefully.
> - 10.4 Shadow-mode correctness tests: promotion never occurs when shadow-controller residuals exceed live controller on the evaluation window.

---

## Summary recommendation

Build ventd's predictive layer as **ARX+RLS with IMC-PI and derivative feedforward for the short horizon**, **learned per-signature workload statistics with matrix-profile-discovered motifs for the long horizon**, behind a three-tier safety envelope (hard cap, conservative fallback curve, drift-detected demotion), shadow-promoted from the reactive baseline, persisted as versioned JSON keyed by DMI fingerprint, using `gonum`, `cilium/ebpf`, `vishvananda/netlink`, `ebitengine/purego`, `natefinch/atomic`, and `matrix-profile-foundation/go-matrixprofile`. Reject LSTM/GPR/Gorgonia for v1 as mismatched to the problem scale. The techniques are all textbook; the *integration* into a zero-config pure-Go homelab daemon is the novel contribution.