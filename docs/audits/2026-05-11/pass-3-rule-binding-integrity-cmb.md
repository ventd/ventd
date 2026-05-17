# Pass 3 — Rule↔Binding Integrity (RULE-CMB-* sub-pass)

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, third pass
**Baseline commit**: `e49ac20` (main after pass-3 smart-mode-wiring + pass-3-cpl)
**Scope**: every `RULE-CMB-*` heading in `docs/rules/marginal.md` (Layer-C marginal-benefit) — 26 rules.

## Findings

### SOLID (22/26)

| rule | claim | how exercised |
|---|---|---|
| RULE-CMB-SHARD-01 | d_C = 2 fixed | `New` errors on dim != 2 |
| RULE-CMB-SHARD-03 | tr(P) ≤ 100 clamp | 1000 constant-phi ticks; trace stays clamped |
| RULE-CMB-SAT-01 | Path-A: β₀ × ΔPWM < 2°C → saturated | boundary 0.5°C saturated, 3°C not |
| RULE-CMB-SAT-02 | Path-B: 20 consecutive sub-2°C writes → saturated | exactly tests streak count + streak break |
| RULE-CMB-SAT-03 | saturation forced false during warmup | wrong-direction prior + warmup → not saturated (wrong-prior guard verified) |
| RULE-CMB-WARMUP-01 | three-condition gate + parent Layer-B clearance | 60 samples + SetParentOutOfWarmup transitions WarmingUp=false |
| RULE-CMB-PRIOR-01 | β₀ from b_ii/pwm_unit_max, β₁=0 | exact value check; un-confirmed path tested separately |
| RULE-CMB-LIB-02 | fallback labels never create shards | three filtered labels exercised |
| RULE-CMB-RUNTIME-01 | Run starts ≤5 goroutines | tight cap (vs the looser CPL-RUNTIME-01 borderline) |
| RULE-CMB-RUNTIME-02 | OnObservation min-over-N < 1ms | exactly tests min over 50 samples |
| RULE-CMB-RUNTIME-03 | Snapshot.Read() lock-free | holds shard mu; verifies Read returns |
| RULE-CMB-PERSIST-01 | hwmon fingerprint mismatch discards | full save→load cycle with different fp |
| RULE-CMB-PERSIST-02 | schema version mismatch discards | patches on-disk version=99, asserts !loaded |
| RULE-CMB-PERSIST-03 | restored tr(P) clamped + re-warmup | inflate pre-save trace, assert post-load clamp + WarmingUp=true |
| RULE-CMB-R11-01 | three locked constants | exact value pins (2.0 / 20 / 3) |
| RULE-CMB-OAT-01 | group-aware OAT (6 bindings) | each clause has a dedicated test: reject cross-channel, admit intra-group, reject extra-group, ungrouped=size-1 behaviour, size-1 dropped, idempotent replace |
| RULE-CMB-CONF-01 | Confidence struct exposes R12 §Q1 inputs | each term checked for valid range |
| RULE-CMB-NAMESPACE-01 | KV at smart/shard-C/<channel>-<sig>.cbor | full save → verifies directory + filename containing sig |
| RULE-CMB-WIRING-01 | buildMarginalRuntime returns nil on empty | invokes the real production helper |
| RULE-CMB-WIRING-03 | construction is lazy (no shards until OnObservation) | invokes the real production helper + asserts ShardCount=0 |
| RULE-CMB-WIRING-04 | OnObservation per tick, first tick skipped | drives the real buildSmartObsBridge closure |
| RULE-CMB-SHARD-02 | RLS uses SymRankOne (convergence half) | 500 random observations, θ converges to ground truth ±0.05 — same caveat as CPL-SHARD-02 (structural "never invert P" untested), folded under SOLID because the operator-visible behaviour is convergence |

### BORDERLINE (4/26) — claim partially exercised; low practical risk

| rule | gap | risk |
|---|---|---|
| RULE-CMB-LIB-01 | Rule says "weighted-LRU eviction" with score = `HitCount × exp(-(age/τ))`. Test verifies the **cap** (`ShardCount ≤ MaxShardsPerChannel`) but not the **eviction policy**. A regression that switched to FIFO eviction would still pass. | Low. The cap is the operator-visible signal; eviction policy is observable only by inspecting evicted shards' IDs. |
| RULE-CMB-PRIOR-02 | Rule promises "atomic.Pointer load at admission, not from the live shard". Test verifies β_0 reflects the prior value at admission (the **outcome**); does not verify the **mechanism** (atomic.Pointer specifically). A regression to direct pointer deref produces the same outcome. | Low. The race-avoidance reason for atomic.Pointer is correctness-under-concurrency; the test runs single-goroutine so the race condition is not exercised. A separate `-race` test driving the parent's b_ii from a goroutine while the marginal runtime admits would close this gap; not present today. |
| RULE-CMB-DISABLE-01 | Rule promises "R1/R3 disable inheritance + nil-parents/signguard graceful degrade". Test verifies the bottom half (nil parents → shard still admits cleanly). Does NOT exercise R1 Tier-2 BLOCK or R3 hardware-refused signal propagation — those are upstream signals from daemon-startup detection. | Low. The disable signals are exercised by separate tests at the daemon-startup layer; the rule binding here is the inheritance side, which would naturally compose. |
| RULE-CMB-IDENT-01 | Rule says "deferred when parent κ > 10⁴; **τ_retry = 1h**". Test verifies the deferred-admission outcome on parent κ-bad. Does NOT verify the 1h re-attempt floor — a regression to per-tick re-attempt would still pass. | Low. The 1h re-attempt floor is a performance / log-spam concern, not correctness. Operator-visible signal is journald log line cadence; not a thermal bug. |

### WEAK / GHOST

**None in this family.** As with RULE-CPL-*, the Layer-C rules are dominated by mathematical-invariant claims and pure-function helpers, both of which are directly testable.

## Running tally

| sub-pass | total | SOLID | BORDERLINE | WEAK |
|---|---|---|---|---|
| smart-mode-wiring (5) | 5 | 2 | 0 | 3 |
| RULE-CPL-* (15) | 15 | 13 | 2 | 0 |
| RULE-CMB-* (26) | 26 | 22 | 4 | 0 |
| **total so far** | 46 | 37 | 6 | 3 |

The heuristic from pass-3-cpl holds: rules that claim mathematical invariants or call buildXxxRuntime production helpers audit cleanly. The 3 WEAK rules are all from the smart-mode-wiring-1035.md family — they claim "X is called from Manager.run / runDaemonInternal" but test the helper, not the caller.

## Filing

No fileable issues from this sub-pass. The 4 BORDERLINE entries are noted but do not warrant individual fixes — they trade off test setup complexity (drive R1/R3 detection end-to-end; spawn a concurrent goroutine for atomic.Pointer; advance time 1h) against low-risk corner cases. If Phoenix wants any of them tightened, rolling them into a single follow-up test PR is the cleanest path.

## Next

Pass 3 continues with RULE-POLARITY-* — 11 rules, recently overhauled in #1067. Per the heuristic, the production-wiring claims around the controller hot path (RULE-POLARITY-11 in particular) are the highest-risk and should be sampled first.
