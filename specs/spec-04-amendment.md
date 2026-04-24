# Spec 04 — Amendment (predictive-thermal prerequisites)

**Status:** DRAFT — amends `spec-04-pi-autotune.md` to tighten scope and expose the FOPDT approximation spec-05 Phase 1 consumes for IMC tuning.
**Source:** spec-05 §4.4 uses an FOPDT (first-order plus dead-time) plant description to compute IMC-PI gains; spec-05 §4.3 needs a hook during autotune to learn feed-forward gains.
**Apply when:** merge this amendment **into spec-04 before PR 1** of spec-04 starts.
**Author note:** this is a scope tightening (relay feedback over Z-N chatter) plus two additive outputs (FOPDT record, feed-forward gain hook). Cost goes *down*, not up.

---

## Why this amendment exists

spec-04 currently specifies Ziegler-Nichols gains directly from relay feedback (Ku, Tu → Kp, Ki via Z-N formula). spec-05 needs:

1. **The underlying plant model** (FOPDT: gain K, time constant τ, dead time θ), not just the PI gains — because spec-05's IMC tuning formula derives PI gains from the plant, not from Z-N ratios, and because IMC gives the user a single `aggressiveness` knob that Z-N cannot.
2. **An autotune hook** during the step-response phase to learn feed-forward gains `k_fp` and `k_fu` (from dP/dt and d(util)/dt respectively), because ventd ships feed-forward in v0.7.0 *before* spec-05's learned model exists.
3. **Åström-Hägglund relay-only mode** as the canonical autotune path. Spec-04 already specifies the relay method; the amendment is to make the Z-N formula output optional/secondary and FOPDT output mandatory.

These are three small additive outputs. None of them change the autotune's physical behaviour — only what it records and exposes.

---

## §1 — Amendment to spec-04 §PR 3 (Ziegler-Nichols relay autotune)

### §1.1 Rename + tighten scope

Rename PR 3 from "Ziegler-Nichols relay autotune" to "**Relay-feedback autotune (Åström-Hägglund)**" and reframe Z-N as a *secondary output* rather than the primary.

Rationale: relay feedback and Z-N are orthogonal. Relay feedback extracts `(Ku, Tu)` from controlled oscillation. Z-N is one of several formulas for turning `(Ku, Tu)` into PI gains (alongside Tyreus-Luyben, IMC, Skogestad SIMC, etc.). spec-04 as written conflates them; the amendment separates the measurement from the tuning rule so spec-05 can plug in IMC without re-writing autotune.

### §1.2 Add: FOPDT identification step

Extend the algorithm with a new step between relay measurement and gain computation:

    5a. From the oscillation data, fit FOPDT plant:
        P(s) = K · exp(-θs) / (τs + 1)

    Where:
      K = steady-state gain = Δtemperature / Δpwm_amplitude
          (measured from the oscillation envelope)
      τ = time constant = approximated from phase relation of relay oscillation
          (see Åström & Hägglund 1984, eq. 6: τ ≈ Tu / (2π · tan(φ)) where φ is
           measured phase lag between PWM square wave and temperature response)
      θ = dead time = measured time between PWM step and first detectable
          dT/dt > noise_floor

    5b. Sanity-check: K > 0, τ > 0, θ ≥ 0, θ / τ < 1 (large dead-time ratio
        rejects — relay tuning degrades past θ/τ ≈ 0.5 anyway).

### §1.3 Add: autotune output record

Extend the persisted calibration record from the current `(Kp, Ki, Tu, Ku, captured_at)` to:

    type AutotuneRecord struct {
        // Measured (relay feedback):
        Ku         float64   // ultimate gain
        Tu         float64   // ultimate period, seconds
        // Identified (FOPDT fit):
        PlantK     float64   // steady-state gain, °C per PWM unit
        PlantTau   float64   // time constant, seconds
        PlantTheta float64   // dead time, seconds
        // Computed (Z-N, for backward compat and verification):
        KpZN       float64   // Z-N Kp (= 0.45 * Ku)
        KiZN       float64   // Z-N Ki (= KpZN / (Tu/1.2))
        // Learned feed-forward gains (see §2 below):
        KFeedPower float64   // feed-forward gain for dP/dt
        KFeedUtil  float64   // feed-forward gain for d(util)/dt
        // Provenance:
        CapturedAt time.Time
        FanID      string
        SensorID   string
    }

The `KpZN`/`KiZN` fields remain the *default* gains applied to the PI curve (spec-04 ships v0.6.0 with Z-N gains, unchanged from original scope). spec-05 Phase 1 (v0.8.0) adds an IMC tuning path that reads `PlantK/Tau/Theta` and computes its own gains; the autotune output is the shared input.

### §1.4 Add: JSON persistence

The autotune record is written to the platform state directory as:

    /var/lib/ventd/platform/<fingerprint>/autotune/<fan_id>.json

With `"schema_version": 1`, atomic write, same layout pattern as spec-03 amendment.

This makes the autotune record the single authoritative source for plant parameters. spec-05 Phase 1 reads from here; no re-running of autotune when predictive mode activates.

---

## §2 — Amendment to spec-04 §PR 3 autotune algorithm (feed-forward gain hook)

### §2.1 Add: feed-forward gain learning during autotune

During the relay oscillation phase, the autotune also records:

    For each half-cycle of the relay:
      - dP/dt_peak      — peak power derivative during the PWM step
      - d(util)/dt_peak — peak utilisation derivative during the PWM step
      - Δtemp_overshoot — temperature overshoot attributable to that half-cycle

Then compute:

    k_fp = median over half-cycles of (Δtemp_overshoot / dP/dt_peak)
         * (-pwm_per_degree_sensitivity)

    k_fu = same form, for utilisation

Both clamped to their §4.5 box constraints before persistence.

These gains are the *default* feed-forward gains shipped in spec-05 Phase 0 (v0.7.0) before any learned model exists. Without this hook, spec-05 Phase 0 would have no way to set `k_fp`, `k_fu` except hard-coded guesses, and the feed-forward baseline would be unusable.

### §2.2 Safety sentinel

New rule:

- **RULE-CAL-AUTOTUNE-06**: Feed-forward gain computation aborts if `dP/dt` data is unavailable (no RAPL, no AMD energy counter). In that case, `KFeedPower = 0` is written and the autotune continues — PI control still works, only the feed-forward baseline degrades. The DaemonLogs must explicitly surface this degradation at INFO level.

Bind to subtest. Backward-compatible: systems without RAPL are not worse off; systems with RAPL get the feed-forward benefit.

---

## §3 — Amendment to spec-04 §Definition of done

Add the following to PR 3 DoD:

- [ ] Autotune record persists to `/var/lib/ventd/platform/<fingerprint>/autotune/<fan_id>.json` with `schema_version: 1`.
- [ ] FOPDT fit produces `(PlantK, PlantTau, PlantTheta)` on synthetic plant test within 10 % of true values.
- [ ] Feed-forward gains `KFeedPower`, `KFeedUtil` are computed and persisted whenever RAPL data is present.
- [ ] RULE-CAL-AUTOTUNE-06 (feed-forward degradation path) bound to subtest.
- [ ] `.claude/rules/calibration-safety.md` extended with CAL-AUTOTUNE-06.
- [ ] Opus Consult 2 output in `docs/control-theory-notes.md` explicitly addresses: (a) Åström-Hägglund FOPDT identification rigour at θ/τ ratios ventd realistically sees on air-cooled CPUs, (b) Tyreus-Luyben vs Z-N vs SIMC for fan control plants, (c) whether 10 min timeout scales with `τ` (suggestion: timeout = max(10 min, 50·τ) once `τ` is known from first oscillation).

---

## §4 — Amendment to spec-04 §Opus Consult 2

Extend the "autotune safety" consult scope:

**Additional consult items:**
- FOPDT identification from relay data — Åström-Hägglund 1984 method vs. simpler step-response fit; which is more reliable at ventd's sampling rates (1 Hz, 2 Hz)?
- Feed-forward gain learning methodology — is the per-half-cycle median robust to outliers, or do we need a weighted least-squares fit across the oscillation window?
- Should spec-04 ship IMC-PI as an *alternative* gains-rule alongside Z-N, or defer all IMC to spec-05? (Recommendation before consult: defer to spec-05 to keep spec-04 scope tight; the amendment only adds the FOPDT *record*, not an IMC *path* in spec-04.)

---

## §5 — Files added or changed by this amendment

**New files:**
- `internal/calibrate/fopdt.go` — FOPDT identification from relay oscillation data.
- `internal/calibrate/fopdt_test.go` — synthetic plant tests (known K/τ/θ → fit within 10 %).
- `internal/calibrate/feedforward.go` — per-half-cycle feed-forward gain computation.
- `internal/calibrate/feedforward_test.go`.
- `internal/calibrate/autotune_record.go` — JSON schema + atomic write (uses spec-03 amendment storage layout).

**Modified files:**
- `internal/calibrate/autotune.go` — pipeline integration.
- `internal/calibrate/autotune_safety.go` — RULE-CAL-AUTOTUNE-06.
- `.claude/rules/calibration-safety.md` — new rule.
- `docs/control-theory-notes.md` — consult 2 output.

**Out of scope (still):**
- IMC tuning rule implementation — spec-05 Phase 1.
- Re-running autotune when predictive mode drifts — spec-05 §6.4 (recalibration after Page-Hinkley trip).
- Feed-forward gain adaptation during runtime — spec-05 Phase 0 uses the autotune-captured static gains; spec-05 Phase 1 learns them online.

---

## §6 — Cost impact

spec-04 PR 3 grows by ~10–20 %: FOPDT fit is ~80 lines of Go, feed-forward gain computation ~50 lines, persistence reuses spec-03 amendment plumbing. **Net session cost flat** — PR 3 was already the expensive one (stateful autotune, rich test scenarios), and the amendment folds additive outputs into the same session.

The real saving is downstream: spec-05 Phase 1 doesn't re-run autotune or re-identify plants; it reads the record and computes IMC gains. Estimated saving: 2–3 Sonnet sessions in spec-05 Phase 1 by avoiding re-work.

**Do not treat this amendment as a separate PR.** It folds into spec-04 PR 3 as additional outputs. The relay-feedback phase is the expensive part; recording three extra numbers is cheap.
