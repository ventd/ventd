# RULE-PKG-VALIDITY-PROBE-BOUNDARY: The three-package boundary on the calibration-adjacent surface is calibrate/ (legacy V-model sweep), validity/ (PR-2b channel-validity probe), probe/ (catalog-less primary path).

The senior review's H10 finding (the original v0.5.26 review at
`/root/.claude/plans/you-are-a-30-vivid-pascal.md`) called out a
naming collision on three calibration-adjacent packages whose names
didn't telegraph their distinct concepts:

- `internal/calibrate/` (1377 LOC) — the legacy V-model PWM sweep
  that records RPM at every duty-cycle step then curve-fits the
  result. Pre-smart-mode pipeline. Still in use as the
  fallback / recalibration path.
- `internal/calibration/` (380 LOC) — the PR-2b channel-validity
  probe (polarity, stall_pwm, BIOS-override). Originally named
  `calibration` because it shipped alongside the V-model sweep, but
  semantically distinct: this validates whether a channel CAN be
  controlled at all (it's a gate before calibration), not whether
  the V-model curve has been computed.
- `internal/probe/` (1128 LOC) — the catalog-less primary path
  (channel discovery, thermal source detection, three-state outcome).
  This is the smart-mode primary surface; v0.6.x will deprecate
  `calibrate/` in favour of signature-driven adaptive control once
  HIL field-validation (Phase C of the v0.6.0 ship plan) confirms
  convergence.

**v0.5.35 renamed `internal/calibration/` → `internal/validity/`**
to make the boundary self-documenting. The `validity` name reflects
the package's actual job: it answers "is this channel valid for
control" (polarity correct? not stalling? not BIOS-overridden?).
Tests previously bound to `internal/calibration/probe_test.go:*` are
now bound to `internal/validity/probe_test.go:*`; all 9 RULE-CALIB-PR2B-*
rule files were rewritten to reference the new path.

Concrete contract for future contributors:

- New code that detects whether a hwmon channel is **physically
  controllable** (polarity probe, stall detection, BIOS-revert
  detection) goes in `internal/validity/`.
- New code that **records V-model curve data** for a known-controllable
  channel goes in `internal/calibrate/`.
- New code that **discovers channels and routes** to either of the
  above goes in `internal/probe/`.

The boundary with `internal/calibrate/` is the v0.6.x deprecation
gate: when smart-mode field-validation completes (Phase C C5/C6) and
the catalog-less primary path can fully replace V-model sweeps,
`internal/calibrate/` shrinks to a fallback for hardware without
controllable channels (the validity probe returns
`OutcomeMonitorOnly` per RULE-PROBE-04). At that point a future
v0.7+ can fold `calibrate/` into `validity/` if the operational
boundary disappears entirely.

This rule is documentation-only (single-h1, per the existing
`.claude/rules/RULE-STATE-*.md` pattern). It has no bound subtest
because the constraint it expresses is structural / naming, not a
runtime invariant. A regression that re-introduces a fourth
calibration-adjacent package without a clear boundary is caught at
review time, not at test time.

The architectural lens lives at
`docs/research/r-bundle/smart-mode-handoff.md`; the v0.6.0 ship plan
at `/root/.claude/plans/you-are-a-30-vivid-pascal.md` Phase B item B1.
