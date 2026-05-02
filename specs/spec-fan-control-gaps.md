# spec-fan-control-gaps — Competitive gap analysis and patch proposals

**Status:** draft, gap-survey artefact for v0.6.0–v0.7.x roadmap input.
**Scope:** Linux only. No microphones. No custom hardware. Excludes features
locked in ventd specs (R7, R12, R16, R17, R18, R19; spec-05 phases 0–3).

---

## §1 Per-category competitive matrix

**N** = no evidence. **P** = partial / single-axis. **Y** = explicit feature.

| Category | fan2go | CoolerControl | fancontrol | thinkfan | nbfc-linux | asusctl | TuxClocker | liquidctl | FanControl (Win) | Argus | OpenBMC | iDRAC | iLO | ventd locked |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1. Psychoacoustic noise | N | N | N | N | N | N | N | N | N | N | N | N | N | dither (R18) |
| 2. Predictive ramp-ahead | P (D-term) | N | N | N | N | N | N | N | N | N | N | N | N | **Y** spec-05 |
| 3. Bearing wear / dust | N | N | N | N | N | N | N | N | N | P (alert) | P (alert) | P (alert) | P (alert) | partial R16 |
| 4. Multi-machine fleet | N | N | N | N | N | N | N | N | N | N | P (chassis) | P (chassis) | P (chassis) | Pro-tier |
| 5. Workload-aware auto | N | P (trigger) | N | N | N | P (perf) | N | N | P (plugin) | P (hotkey) | N | N | N | spec-05 Ph2 |
| 6. Energy / power-aware | N | N | N | N | N | P | N | N | N | N | P (budget) | P (budget) | P (budget) | N |
| 7. Datasheet priors | N | N | N | N | N | N | N | N | N | N | N | N | N | N |
| 8. Multi-fan choreography | N | P (max-mix) | N | N | N | N | N | N | P (max-mix) | P (max-mix) | N | N | N | N |
| 9. User-feedback loudness | N | N | N | N | N | N | N | N | N | N | N | N | N | N |
| 10. Auto fan-fail rebalance | N | N | N | N | N | N | N | N | N | P (alert) | N | **Y** | **Y** | watchdog only |

**Source notes (per cell):**
- fan2go: PID + linear/aggregator curves, no acoustic/health/fleet
  ([fan2go](https://github.com/markusressel/fan2go), [issue #247 noise complaint](https://github.com/markusressel/fan2go/issues/247)).
- CoolerControl: "Fixed, Graph, Mix, Overlay" + hysteresis Functions; reactive
  ([CoolerControl](https://github.com/codifryed/coolercontrol)).
- fancontrol(8): linear-only, reactive
  ([manpage](https://man.archlinux.org/man/extra/lm_sensors/fancontrol.8.en)).
- thinkfan / nbfc-linux: reactive curves with avg/min/max
  ([thinkfan](https://github.com/vmatare/thinkfan), [nbfc-linux](https://github.com/nbfc-linux/nbfc-linux)).
- asusctl: laptop perf profiles + dGPU MUX, no choreography
  ([asusctl](https://gitlab.com/asus-linux/asusctl)).
- TuxClocker: writable→readable bindings + profiles
  ([TuxClocker](https://github.com/Lurkki14/tuxclocker)).
- liquidctl: device CLI, fixed/custom profiles only
  ([liquidctl](https://github.com/liquidctl/liquidctl)).
- FanControl (Rem0o): graph editor, mix/max, plugin sensors
  ([FanControl Plugins wiki](https://github.com/Rem0o/FanControl.Releases/wiki/Plugins)).
- Argus Monitor: multi-controller max, hysteresis, averaging, rate-limit,
  S.M.A.R.T., fan-fail warning — no wear prediction
  ([Argus Fan Control](https://www.argusmonitor.com/help/FanControl.php),
  [Argus Warnings](https://help.argusmonitor.com/Warnings1.html)).
- OpenBMC phosphor-fan-presence: presence/monitor/control via D-Bus, no
  prediction
  ([phosphor-fan-presence](https://github.com/openbmc/phosphor-fan-presence)).
- Dell iDRAC: "If a fan failure is detected, the remaining fans automatically
  increase their speeds to compensate"
  ([Dell PowerEdge KB](https://www.dell.com/support/kbdoc/en-us/000257346/poweredge-how-to-change-the-fan-speed-offset)).
- HPE iLO: chassis-level rebalance documented per ProLiant generation
  ([HPE iLO 6 cooling](https://support.hpe.com/hpesc/public/docDisplay?docId=sd00002007en_us)).

---

## §2 Gaps no surveyed competitor ships

Eight categories show universal absence. Three offer the highest leverage on
ventd's existing spec surface (fandb catalog, RLS state, watchdog).

- **G1 — Psychoacoustic / frequency-weighted noise objective.** Datacentre
  acoustics literature is explicit that A-weighting *misses* the low-frequency
  tones that dominate annoyance, and narrow-band frequency analysis is "a
  surgeon's scalpel" relative to the dB(A) "bludgeon"
  ([INVC](https://invc.com/noise-control/data-center-noise-attenuation/)). No
  daemon ingests an audibility weighting.

- **G2 — Datasheet-derived informed priors (highest leverage).** Noctua, Delta,
  Sanyo Denki, Sunon publish per-PWM RPM and dB(A) curves on every product
  page ([Noctua NF-A12x25 spec](https://noctua.at/en/nf-a12x25-pwm/specification)).
  No tool consumes these as priors for calibration or health checks. Every
  install re-derives the curve from scratch, and an under-spec or worn fan
  is invisible until catastrophic.

- **G3 — Multi-fan choreography for fixed airflow at minimum loudness
  (highest leverage).** CoolerControl, FanControl, Argus all support
  `max(a, b)` mixers — the *opposite* of choreography. None solve "to remove
  X watts at lowest perceived loudness, ramp which fans by how much?".
  Cloudflare handles this in chassis design ([Gen-12 thermal](https://blog.cloudflare.com/thermal-design-supporting-gen-12-hardware-cool-efficient-and-reliable/)),
  not at runtime. Cloudflare's Gen-13 documents that fan power scales with
  the *cube* of RPM ([Gen-13](https://blog.cloudflare.com/gen13-config/)) —
  a 25% RPM cut is a 58% power cut, often without measurable thermal cost.

- **G4 — Bearing-wear / dust prediction without a microphone.** Spectral
  kurtosis on tach pulse trains achieves 95%+ bearing-fault accuracy in the
  literature ([envelope spectrum + SK](https://www.sciencedirect.com/science/article/pii/S2590123025029627)).
  No surveyed tool extracts higher-moment statistics from tach.

- **G5 — Workload-aware fan-curve auto-switch.** CoreCtrl auto-switches
  *GPU* profiles when an associated app launches
  ([CoreCtrl AMD](https://www.musabase.com/2026/04/msi-afterburner-alternative-on-linux.html));
  no Linux daemon does this for *fan curves* across the system. ventd
  spec-05 Phase 2 covers signature pre-warm but doesn't switch *curves*.

- **G6 — Active fan-failure compensation in Linux user-space (highest
  leverage).** Dell iDRAC and HPE iLO ship this as standard chassis
  firmware. Every Linux user-space daemon at best *alerts*; none re-target
  surviving fans during nominal operation. ventd's watchdog is the closest
  analogue but only restores firmware-auto on exit.

- **G7 — User-feedback loudness loop.** Zero competitors capture a binary
  user signal during operation. ventd's web UI per spec-12 already provides
  the surface.

- **G8 — Energy-cost-aware control.** Server BMCs offer power caps that
  *side-effect* fan duty. No Linux user-space daemon treats fan power as
  a first-class objective despite the cubic-RPM relationship being well
  documented.

The three highest-leverage gaps reuse ventd's locked architecture: **G2**
(fan database catalog, parallel to spec-03 hwdb), **G3** (consumes RLS state
from spec-05), **G6** (consumes envelope-D probe data and watchdog plumbing).

---

## §3 Spec draft — three patches

### Patch A — `fandb` Datasheet-derived informed priors

**Tagline:** Noctua already published your fan curve. Use it.

**Concrete behaviour.** Ship `internal/fandb/catalog/*.yaml` keyed on
manufacturer + part number (`noctua/NF-A12x25-PWM`,
`delta/AFB1212H`), holding tabulated `(pwm, rpm_p50, rpm_p95, dB_A_p50,
mech_lifetime_h, datasheet_url, datasheet_sha256)`. Wizard adds an optional
"match a fan to its datasheet" step (skippable). Three runtime consumers:
(a) calibration uses the datasheet stall PWM as a prior, then snaps to
measured ±2 LSB; (b) spec-05 RLS covariance initialises from the datasheet
curve, shortening warmup; (c) doctor surfaces
`measured_rpm / datasheet_rpm @ same_pwm` — under-spec by >15% is a soft
"check for dust / worn bearing" advisory. The G1 loudness objective is
constructed by reading the datasheet's dB(A) column at chosen RPM — no
microphone, no inference.

**Locked decisions.**
- YAML, schema-versioned, validated by `fandb-lint` reusing
  `KnownFields(true)` (per spec-03 RULE-HWDB-06 pattern).
- No PII: manufacturer + part number only; capture is anonymous.
- PDFs not shipped (licensing); URL + SHA256 + extracted points only.
- Crowd-sourced contribution path mirrors spec-14a.
- Unknown fan remains first-class; ventd is not a membership product.

**Roadmap slot.** v0.6.0, before predictive Phase 0 (v0.7.0). Two PRs:
PR1 = catalog format + 30 seed entries (Noctua A-series, Arctic P-series,
Delta AFB1212 family); PR2 = runtime consumption.

**Invariants.**
- `RULE-FANDB-01`: catalog MUST validate `KnownFields(true)`; unknown
  field = refuse to load.
  Bound: `internal/fandb/catalog_test.go:TestCatalog_KnownFieldsStrict`
- `RULE-FANDB-02`: datasheet curve points MUST be monotonic non-decreasing
  in PWM and in RPM (constant-PWM rest-band excepted).
  Bound: `internal/fandb/catalog_test.go:TestCatalog_MonotonicCurve`
- `RULE-FANDB-03`: a catalog match is a *prior*, never an *override*. When
  measured midpoint RPM differs from datasheet by >25%, ventd logs WARN,
  falls back to measurement-only calibration, continues.
  Bound: `internal/fandb/prior_test.go:TestPrior_FallsBackOnMismatch`
- `RULE-FANDB-04`: schema explicitly omits any field for chassis serial,
  hostname, MAC, or user handle.
  Bound: `internal/fandb/catalog_test.go:TestCatalog_NoPIIFields`
- `RULE-FANDB-05`: datasheet URL + SHA256 MUST both be present; attribution
  is a load-time requirement, not a warning.
  Bound: `internal/fandb/catalog_test.go:TestCatalog_AttributionRequired`

**Cost.** ~$80 (catalog format $20, 30 seed YAMLs $30, prior consumption
$20, wizard step $10).

**Open questions.**
1. Manufacturer retraction policy if a vendor objects to extracted curves?
2. Catalog ships with ventd or loads from opt-in network endpoint?
3. Confidence-interval policy when only typical-curve is published?
4. Fingerprinting "this physical fan is the part number you say it is"
   without trusting the user?
5. Multi-revision parts (Noctua re-tunes within a model name) —
   disambiguation strategy?

---

### Patch B — `compose` Multi-fan choreography for minimum loudness

**Tagline:** Three fans at 40% are quieter than one at 80%, same heat.

**Concrete behaviour.** When ventd has ≥2 controllable fans whose airflow
paths overlap (same case, same heat-source pair), it solves a small
constrained optimisation each tick: minimise total predicted loudness
L(pwm₁, pwm₂, …) subject to predicted ΔT ≤ target. L comes from Patch A's
datasheet dB(A) curves; predicted ΔT comes from spec-05 RLS. Unmatched fans
contribute their measured RPM × a generic Noctua-A-median prior. Solver is
a 3-fan-or-fewer 1-D grid search at 8% PWM resolution = 4096 evals,
microseconds. >3 fans are partitioned by overlap group. Group membership
is calibration output: at envelope-D probe time ventd already perturbs
each fan and records per-fan-per-sensor dT/dt; fans with non-trivial
response on the same sensor join the same group.

**Locked decisions.**
- Advisory by default: solver produces a UI suggestion, not an auto-apply,
  in v0.6.x. Auto-apply gated on `--enable-choreography-auto-apply`.
- Objective is dB(A), not RPM, not PWM. No mic: dB(A) is datasheet-derived.
- Without datasheet matches, feature degrades silently to existing curve —
  no extrapolation gambling.
- Per-fan curves remain source of truth; solver writes per-fan overrides,
  not a global "smart mode" hiding fan identity.
- Pump channels (RULE-LIQUID-01, RULE-HWMON-PUMP-FLOOR) excluded from
  domain.

**Roadmap slot.** v0.7.x, after spec-05 Phase 1 shadow-promotion (v0.8.0).
Solver consumes RLS state. Two PRs: PR1 = overlap-group capture during
envelope-D; PR2 = solver + advisory UI.

**Invariants.**
- `RULE-COMPOSE-01`: solver MUST NOT propose any per-fan PWM below
  calibrated min_responsive_pwm or above measured pwm_unit_max.
  Bound: `internal/compose/solver_test.go:TestSolver_RespectsCalibratedBounds`
- `RULE-COMPOSE-02`: a run that cannot meet the headroom constraint MUST
  return `ErrInfeasible`, NOT "max all fans". UI displays infeasibility;
  controller continues with current per-fan curves.
  Bound: `internal/compose/solver_test.go:TestSolver_InfeasibleReturnsError`
- `RULE-COMPOSE-03`: pump channels MUST be excluded from solver domain.
  Solver MUST refuse to load any config including `is_pump: true`.
  Bound: `internal/compose/solver_test.go:TestSolver_PumpChannelsExcluded`
- `RULE-COMPOSE-04`: overlap groups MUST be derived from envelope-D
  measured response, NEVER from cabinet-position heuristics. Unknown
  topology produces single-fan groups (no choreography).
  Bound: `internal/compose/group_test.go:TestGroup_UnknownTopologyIsSingleton`
- `RULE-COMPOSE-05`: advisory mode is default; auto-apply requires explicit
  flag gating every write. Default-deny.
  Bound: `internal/compose/runtime_test.go:TestRuntime_AdvisoryByDefault`

**Cost.** ~$140 (overlap-group capture $40, solver $50, advisory UI $30,
acceptance tests $20).

**Open questions.**
1. dB(A) prior choice for unmatched fans — Noctua-median or class-derived?
2. Solver re-runs every tick (cheap, noisy) or every envelope-D-cadence
   (slow, correct, may lag transients)?
3. How are users informed that their non-Noctua fan is modelled with a
   Noctua prior?
4. Multi-zone solver — when groups share an exhaust path, do they couple?
5. Overlap-group capture cost on systems with 8+ fans — single-perturb
   sufficient or pairwise needed?

---

### Patch C — `failover` Active fan-failure compensation

**Tagline:** What iDRAC does for $400, ventd does on a Raspberry Pi.

**Concrete behaviour.** When a fan's tach drops to 0 RPM under non-zero PWM
for >30s (existing sentinel logic) and the same chassis has ≥1 sibling
with a non-zero overlap-group response (Patch B's group capture), ventd
auto-bumps the sibling PWM by the predicted-airflow-deficit amount,
clamped to `pwm_unit_max - 10%`. Predicted-deficit is a per-overlap-group
constant captured at envelope-D probe time. Compensation persists while
the failure persists; on recovery (RPM > stall for 60s) compensation
ramps down. Web UI + slog WARN on first detection; per-incident
suppression available.

**Locked decisions.**
- Failover is gated on calibration data — never on inferred topology.
- Compensation bounded: total surviving PWM ≤ `pwm_unit_max - 10%`. If the
  deficit cannot be met within this bound, ventd raises `LevelError` and
  falls back to watchdog-restore (firmware auto). Never silently overdrives.
- Pump channels exempt as both source (pump fail = critical alert) and
  target (RULE-LIQUID-01 governs pump duty).
- Events logged via observation-log schema; no new state primitive.
- Noisy-neighbour guard: ≥3 compensation events on the same channel in 24h
  disables auto-comp for that channel and surfaces operator advisory.

**Roadmap slot.** v0.7.x, paired with Patch B (shares overlap-group data).
One PR after Patch B PR1.

**Invariants.**
- `RULE-FAILOVER-01`: compensation MUST NOT exceed `pwm_unit_max - 10%`
  on any sibling. Excess deficit → `LevelError` + watchdog restore.
  Bound: `internal/failover/compensate_test.go:TestCompensate_RespectsHeadroom`
- `RULE-FAILOVER-02`: pump-channel failure MUST raise `LevelCritical`
  and MUST NOT trigger fan-side compensation.
  Bound: `internal/failover/compensate_test.go:TestCompensate_PumpFailureIsCritical`
- `RULE-FAILOVER-03`: compensation MUST be removed within 60s of the
  failed fan's RPM returning above stall + safety. Sibling ramp-down rate
  MUST NOT exceed configured ramp limit.
  Bound: `internal/failover/recovery_test.go:TestRecovery_GracefulRampDown`
- `RULE-FAILOVER-04`: ≥3 compensation events on the same channel within
  24h MUST disable auto-compensation for that channel and surface a
  "hardware persistent" doctor advisory.
  Bound: `internal/failover/policy_test.go:TestPolicy_NoisyNeighbourGuard`
- `RULE-FAILOVER-05`: failover MUST only operate on overlap groups
  established by envelope-D probe data. No data → no auto-comp.
  Bound: `internal/failover/group_test.go:TestGroup_NoCompensationWithoutData`

**Cost.** ~$110 (compensation $40, recovery ramp $30, noisy-neighbour
$20, observation-log integration $20).

**Open questions.**
1. Is auto-comp permitted on battery? (Probably no, per RULE-IDLE-02.)
2. Minimum number of healthy siblings required (e.g. ≥2)?
3. Operator opt-out granularity: per-channel, per-chassis, per-host?
4. Does Patch B's solver re-run after failover bumps, or is failover
   one-shot subsumed by next solver tick?
5. Expose `ventdctl failover history` for fleet operators tracking
   fan-MTBF empirically?

---

## Summary

ventd already has more invariant-pinned safety scaffolding than any
surveyed competitor. The leverage points are the (proposed) fan database,
the predictive RLS state, and the watchdog. Patches A, B, C each consume
and amplify locked work, fill universal gaps, and require no
microphone, no custom hardware, no non-Linux platform.

Total: **$80 + $140 + $110 = $330** across v0.6.0 and v0.7.x.
