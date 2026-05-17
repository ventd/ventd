# Pass 3 — Rule↔Binding Integrity (RULE-CPL-* sub-pass)

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, third pass
**Baseline commit**: `e49ac20` (main after #1067/#1068/#1069/#1070 + pass-2 + pass-3 smart-mode-wiring sub-pass)
**Scope**: every `RULE-CPL-*` heading in `docs/rules/coupling.md` (Layer-B coupling) — 15 rules. The 16th RULE-CPL match (`RULE-CPL-IDENT-WIRING-04` in `docs/rules/smart-mode-wiring-1035.md`) was already audited in the previous sub-pass and filed as WEAK under #1075.

## Method (recap)

For each rule, read the rule text and extract the load-bearing behavioural claim. Then read the bound subtest and verify a regression that **removed the claim's load-bearing dispatch / invariant** would fail the test. `tools/rulelint` covers the structural side (file exists, subtest name matches `t.Run`); Pass 3 covers the semantic side.

## Findings

### SOLID (13/15) — load-bearing claim directly exercised

| rule | claim | how the test exercises it |
|---|---|---|
| RULE-CPL-SHARD-01 | NCoupled ≤ 16 | `New(DefaultConfig("ch", 17))` errors; 16 and 0 succeed. Boundary check. |
| RULE-CPL-SHARD-03 | tr(P) ≤ 100 via post-update rescale | 1000 ticks of constant phi (unidentifiable); asserts `mat.Trace(s.p) ≤ TrPCap`. |
| RULE-CPL-IDENT-01 | Window capacity = 60 | `NewWindow(3, 60)`; adds 30 → Count=30; adds 60+ → Count clamps to 60. |
| RULE-CPL-IDENT-02 | κ thresholds 100/10000 with three-way classification | Table-driven: 50, 100, 100.01, 1000, 10000, 10000.01, 1e6, +Inf, NaN → exhaustive boundary check including degenerate values. |
| RULE-CPL-IDENT-03 | ρ > 0.999 SIGNED (not absolute) | Synthetic Window with pwm1==pwm2 (perfect positive) AND pwm1↔pwm3 (anti-correlated). Asserts (1,2) detected AND (1,3) NOT flagged. Directly tests the signed-not-absolute clause. |
| RULE-CPL-WARMUP-01 | AND-gate of three conditions | 44 samples + SetKind(Healthy, κ=50) — κ-gate clears, tr(P) likely clear, n_samples gate fails → asserts still warming. Tests AND semantics (one failing gate keeps WarmingUp=true). |
| RULE-CPL-RUNTIME-02 | Read() lock-free via atomic.Pointer | Holds `s.mu`; spawns goroutine that calls Read; asserts return within 100 ms. |
| RULE-CPL-PERSIST-01 | hwmon-fingerprint mismatch discards | Save with fpOld, Load with fpNew → asserts loaded=false AND NSamples=0. |
| RULE-CPL-PERSIST-02 | schema-version mismatch discards (no migrate) | Patches on-disk SchemaVersion=99, asserts Load returns loaded=false. |
| RULE-CPL-PERSIST-03 | Restored tr(P) clamped to cap | Inflates pre-save tr(P) to 100×TrPCap, saves, loads, asserts restored tr(P) ≤ TrPCap. |
| RULE-CPL-WIRING-01 | buildCouplingRuntime returns nil on empty channels | Invokes real `buildCouplingRuntime(nil, …)` AND `(…, []*probe.ControllableChannel{}, …)`; asserts both return nil. |
| RULE-CPL-WIRING-02 | exactly one shard per channel | Invokes real `buildCouplingRuntime` with 3 channels, asserts `rt.Shard(path)` non-nil for each, AND `SnapshotAll()` length matches. |
| RULE-CPL-WIRING-03 | ctx.Done stops the runtime within 1 second | Starts Run in a goroutine, cancels ctx, asserts Run returns within 1 s. |
| RULE-CPL-WIRING-04 | `Shard.Update` once per controller tick per channel; first tick skipped | Drives real `buildSmartObsBridge` closure with 3 records on the same channel — asserts NSamples=2 (= second-tick + third-tick; first-tick skipped). Plus `MultiChannelIndependence` verifies per-channel state. |

### BORDERLINE (2/15) — load-bearing claim partially exercised

| rule | claim | gap |
|---|---|---|
| RULE-CPL-SHARD-02 | RLS update MUST use `mat.SymRankOne` rank-1 Sherman-Morrison form; **never invert P directly** | `TestRLS_RankOneUpdate_MatchesAnalytical` feeds 500 random observations and asserts θ converges to ground truth ±0.05. This tests the **behavioural half** (convergence) but does not enforce the **structural half** (form used). A refactor that swapped `SymRankOne` for `mat.Inverse(P) · P_new` would still produce convergence (just numerically slower / less stable on ill-conditioned φ) and the test would still pass. Practical risk low — there's only one canonical RLS update, and convergence + tr(P) clamp behaviour are what operators see. Flagging for completeness, not for fix. |
| RULE-CPL-RUNTIME-01 | One estimator goroutine per channel; total bounded by `len(controllableChannels)` | `TestRuntime_OneGoroutinePerChannel` adds 3 shards, runs Run, asserts goroutine delta ∈ [3, 10]. Tolerance window is wide enough that a "2 per shard" regression (delta=6) passes. Catches the goroutine-explosion class (delta=100+) but not the doubling class. Practical risk low — Run's goroutine count is observable in production via pprof, and the "per channel" claim is structurally enforced at the `addLoop` call site, not the test. Flagging for completeness. |

### WEAK / GHOST

**None in this family.** Every rule's load-bearing claim is exercised by the bound test either directly or with a minor caveat.

## Comparison vs the smart-mode-wiring sub-pass

| sub-pass | rules | SOLID | BORDERLINE | WEAK |
|---|---|---|---|---|
| smart-mode-wiring (5 rules) | 5 | 2 | 0 | 3 |
| RULE-CPL-* (15 rules) | 15 | 13 | 2 | 0 |

The RULE-CPL-* family is in materially better shape. Reasons:
1. The Layer-B coupling rules describe **mathematical invariants** (κ thresholds, tr(P) clamp, AND-gate semantics) that are testable by direct invocation of pure functions or `Shard.Update` — no "drive Manager.run end-to-end" plumbing needed.
2. The wiring rules in this family (`WIRING-01..04`) all directly invoke the production helper (`buildCouplingRuntime`, `buildSmartObsBridge`) — same pattern as the SOLID rules in the smart-mode-wiring sub-pass (`RULE-CONFA-WIRING-01/02`).
3. The smart-mode-wiring rules with WEAK bindings (`AGG-WIRING-01`, `SIG-WIRING-01`) make claims about call sites buried deep in `Manager.run` / `runDaemonInternal` — production callers that are hard to drive in isolation. None of the RULE-CPL-* rules make claims that buried.

This suggests a heuristic for future Pass-3 work: **rules that claim "X is called from Y" where Y is a deeply-nested production entry point (Manager.run, runDaemonInternal, the controller tick loop) are high-risk for weak binding**. Rules that claim "X exists and behaves Y way" are low-risk.

## Filing

No fileable issues from this sub-pass. The two BORDERLINE entries are noted in this doc but do not warrant a fix in their own right — both have low practical risk and would be caught by separate operator-visible signals (numerical instability in production for SHARD-02; goroutine leak detection in `-race` runs for RUNTIME-01).

## Next

Pass 3 continues with RULE-CMB-* (Layer-C marginal) — 15+ rules, recently amended for group-aware OAT (#1031). By the heuristic above, this family's WIRING rules (which claim `Runtime.OnObservation` is called from `smart_obs_bridge` — a production helper) should be SOLID, but the algebraic invariants on `Saturation` / warmup gate / OAT admission could surface narrower-than-rule tests like the borderline cases in this sub-pass.
