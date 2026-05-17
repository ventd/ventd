# Pass 3 — Rule↔Binding Integrity (smart-mode-wiring sub-pass)

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, third pass
**Baseline commit**: `39e77a7` (main after all pass-1 fix PRs merged)
**Scope**: every `## RULE-*` heading in `docs/rules/*.md` cross-referenced against the bound subtest's actual assertions. Verifies that the test exercises the rule's **semantic load-bearing claim**, not just the API contract.

## Method

1. Total rule headings: **358** across **37** rule files (`rg -c '^## RULE-' docs/rules/`).
2. `tools/rulelint` already verifies structural binding (the bound file exists, the subtest name is matched by a `t.Run(...)` literal or top-level test name). Pass-3 starts where rulelint stops.
3. For each rule, read the rule text — extract the **load-bearing behavioural claim** (the verb-phrase about a specific call site, ordering, or invariant). Then read the bound subtest and verify that a regression which **removed the claim's load-bearing dispatch** would fail the test.
4. Full Pass-3 across 358 rules is a multi-day effort. This sub-pass scopes to the **5 RULE-*-WIRING-* rules added by #1068** because they are the freshest wiring work and the highest-risk class — they make claims about specific call sites in `Manager.run`, `runShardLoop`, `runDaemonInternal`, and `buildSmartObsBridge`. A regression in any of those callers regenerates the same RFC #1024 "smart-mode doesn't advance" symptom #1068 was supposed to fix.

## Findings — 3 of 5 RULE-*-WIRING-* rules have weak bindings

### SOLID (2/5)

| rule | rule's load-bearing claim | bound test reaches the call site? |
|---|---|---|
| RULE-CONFA-WIRING-01 | `buildSmartObsBridge` invokes `layer_a.Estimator.Observe` per tick | **Yes** — `TestSmartObsBridge_LayerAObserveFiresEveryTick` constructs the real bridge, invokes the closure 3×, asserts `Snapshot.Coverage = 1/NumBins` (a value reachable only if Observe was actually called by the bridge). |
| RULE-CONFA-WIRING-02 | `buildLayerAEstimator` calls `Estimator.LoadChannel` per channel after `Admit` | **Yes** — `TestBuildLayerA_LoadChannelRestoresHistogram` does the full Save → buildLayerAEstimator → Read round-trip and asserts `SeenFirstContact` is restored (only LoadChannel restores this flag). |

### WEAK (3/5) — Pass-3 findings

| rule | rule's load-bearing claim | what the bound test actually does |
|---|---|---|
| RULE-AGG-WIRING-01 | `Manager.run` invokes `calibrationCompleteFn(time.Now())` **after `runAcousticGate` and before `setPhase("finalizing", ...)`** | `TestManager_CalibrationCompleteCallbackFires` constructs a `Manager` directly, sets the callback via `SetCalibrationCompleteFn`, **pulls the callback out under the manager's mutex and invokes it manually**. Does not call `Manager.run`. Verifies only that the field is settable and the callback fires when invoked. A regression that deletes the call site from `Manager.run` silently passes the test. |
| RULE-CPL-IDENT-WIRING-04 | `runShardLoop`'s `identTick.C` case reads `RegressorWindow()`, calls `Window.Kappa()`, `ClassifyKappa`, and writes via `Shard.SetKind` | `TestRuntime_IdentifiabilityTickClassifiesKappa` manually performs the same sequence of calls (`win := s.RegressorWindow(); kappa := win.Kappa(); kind := ClassifyKappa(kappa); s.SetKind(kind, kappa)`). The test comment is honest about this — `// Same code shape as the production identTick branch.` A regression that deletes the `identTick.C` arm from `runShardLoop` silently passes — every API contract still holds in isolation. |
| RULE-SIG-WIRING-01 | Daemon start calls `signature.Library.LoadLabels` after `LoadManifest` | `TestSignatureLoadLabels_RestoresPersistedBuckets` manually invokes `LoadManifest(...)` then `LoadLabels(...)` on a fresh library and checks the bucket round-trips. Does not call `runDaemonInternal`. A regression that deletes the LoadLabels call from `runDaemonInternal` passes. |

### Why this matters

The smart-mode wirings shipped in #1068 fixed the RFC #1024 symptom by adding call sites in five different code paths. Three of those five rules verify the **helper functions called by the wiring** but not the **wiring call site itself**. The structural binding (`Bound:` line resolves) is correct; the semantic enforcement is partial.

A future refactor — e.g. someone simplifying `runShardLoop` and deleting the `identTick.C` case under the assumption it's dead because no test breaks — silently re-introduces the bug #1068 just fixed. The rule catalogue's "test pins production behaviour" contract is structurally satisfied but operationally violated.

## Recommended fix shape

For each weak binding, add a complementary test that drives the production caller end-to-end with a recording stub:

- **RULE-AGG-WIRING-01**: drive `Manager.run` from a test fixture with all hard preconditions cleared and a recording `calibrationCompleteFn`; assert the callback fired exactly once after the acoustic-gate phase.
- **RULE-CPL-IDENT-WIRING-04**: drive `runShardLoop` for ≥ one identTick cycle with a synthetic regressor window pre-populated to ≥ d rows; assert `Snapshot.Kappa` is non-zero on the next snapshot read.
- **RULE-SIG-WIRING-01**: spawn `runDaemonInternal` (or a test-extracted helper that contains its startup-sequence body) with a state dir that already contains a persisted manifest + bucket; assert `library.Buckets()` is non-empty after startup completes.

The cleanest path is option C from the pass-3 method note: extract the relevant block into a helper (`Manager.runCalibrationCompleteHook`, `Runtime.runIdentificationTick`, `daemonLoadSignatureState`) and bind the rule to the helper. The helper is then trivially testable in isolation AND `Manager.run` / `runShardLoop` / `runDaemonInternal` calling it is a one-liner that's hard to delete without intent.

## Scope of remaining Pass-3 work

This sub-pass covered 5 of 358 rules — focused on the smart-mode-wiring family because it's the freshest, highest-risk surface. Future Pass-3 sub-passes by rule-family (recommended cadence: one family per PR):

- **RULE-CPL-*** (Layer-B coupling) — 12 rules. Risk: high. Wirings added across v0.5.7/#1068.
- **RULE-CMB-*** (Layer-C marginal) — 15+ rules. Risk: high. Wirings recently amended for group-aware OAT (#1031).
- **RULE-POLARITY-*** — 11 rules. Risk: medium. Wirings overhauled in #1067 — the controller hot path and panic-handler changes are particularly load-bearing.
- **RULE-WD-*** (watchdog) — 9 rules. Risk: medium-low. #1070 added per-syscall deadlines + IPMI routing; the rules are well-tested via `safety_test.go` against synthetic backends but the integration-level claims (e.g. "every documented exit path") need spot-checks.
- **RULE-STATE-*** — 12 rules. Risk: medium-low. #1066 made these correctness-critical.
- **RULE-HWMON-*** — 18 rules. Risk: low. Heavily tested via fakehwmon.
- **RULE-HAL-*** — 8 rules. Risk: low. Contract-tested.
- **RULE-PROBE-*** — 11 rules. Risk: low.
- The remaining ~250 rules span doctor detectors, install pipeline, preflight, recovery, schema validation — lower risk and well-isolated tests.

## Summary

- 5 rules audited in this sub-pass.
- 2 SOLID (RULE-CONFA-WIRING-01, RULE-CONFA-WIRING-02).
- 3 WEAK (RULE-AGG-WIRING-01, RULE-CPL-IDENT-WIRING-04, RULE-SIG-WIRING-01).
- 0 broken (every bound test exists and exercises the helpers correctly; the gap is at the integration / call-site level, not the helper level).

## Filing

One tracking issue covering all three weak bindings with a recommended fix shape. Priority: med — these don't break today's behaviour, but they leave the v0.6.0-critical wiring un-protected against future regression.

## Next

Pass 3 continues with the RULE-CPL-* family in the next sub-pass.
