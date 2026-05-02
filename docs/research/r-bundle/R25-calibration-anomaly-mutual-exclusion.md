# R25 — Calibration ↔ anomaly-detection mutual exclusion

**Status:** OPEN. Surfaced 2026-05-01 by the post-v0.5.6 smart-mode
audit (`/root/ventd-walkthrough/smart-mode-smarter.md` §"Open
research questions" #5).

**Question.** R14 specifies the calibration time budget — Envelope
C/D probes inject step-function PWM changes that produce
predictable thermal step responses. R16 specifies the anomaly
detector — Page-Hinkley + cross-shard aggregation that fires on
statistical anomalies in the residual stream.

**These two overlap.** An R14 calibration sweep injects exactly
the kind of step changes that R16's `RULE-ANOMALY-06` ("wrong-
header reseat" detector) treats as anomalous. Today the spec
papers over this with `not in_envelope_abort` predicates, but
that's load-bearing in a way that's not formalised:

- What if calibration is paused mid-sweep (R14 §6.3 user idle
  gate trips)? Is anomaly detection paused, armed, or armed-but-
  silenced?
- What if a real fault happens *during* calibration? The signal
  is present but the detector is masked.
- What if anomaly detection is wedged in "alarm" state when
  calibration starts? Does calibration override / clear the
  alarm, or does the alarm block calibration?

**Why R1-R20 don't answer this.**
- R14 mentions anomaly suppression but doesn't formalise the
  state machine.
- R16 inherits R14's `in_envelope` predicate but doesn't define
  the state-machine semantics on overlap.
- The two were designed independently; their interaction is
  emergent, not specified.

**What needs answering.**
1. What's the state machine for `(calibration_state, anomaly_state)`?
   Candidates:
   - **Strict mutex.** Calibration aborts on alarm; alarm cleared
     on calibration completion.
   - **Soft mutex.** Calibration silences alarms but reports them
     in doctor; on completion, suppressed alarms are surfaced.
   - **Cooperative.** Calibration provides anomaly detector with
     a "step changes are expected" hint; detector gates Page-
     Hinkley accordingly.
2. What's the operator-facing UX? "Don't recalibrate while a
   fault is active" is one position; "always allow recalibration,
   it might fix things" is another.
3. Are there other calibration paths beyond Envelope C/D that
   need similar mutex contracts? (v0.5.7's Layer-B re-warm? v0.5.5
   opportunistic probes — those are per-bin and short, but they
   DO inject steps.)
4. How does the v0.5.10 doctor surface ("active recovery items")
   render the overlap state? "Calibration in progress; alarms
   suppressed for X seconds remaining."

**Pre-requisite for.** Robust v0.5.10 doctor surface where R16
anomalies and R14 calibrations may both be in-flight.

**Recommended target.** v0.5.10 doctor patch — the surface is
where this becomes user-visible; this is the right tag to land
the formalisation alongside.

**Effort estimate.** 1 R-item (state-machine derivation), 1 spec
patch (`spec-v0_5_10-doctor.md` extension), ~2-3 days
implementation once design lands.
