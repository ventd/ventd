# Pass 3 — Rule↔Binding Integrity (RULE-WD-* sub-pass)

**Date**: 2026-05-11
**Baseline commit**: `f95ba76` (main after pass-3-cmb)
**Scope**: every `RULE-WD-*` heading in `docs/rules/watchdog-safety.md` — **11 rules / 16 bindings** (3 new families from #1070).

## Findings

### SOLID (11/11) — every rule's load-bearing claim is directly exercised

| rule | bindings | how exercised |
|---|---|---|
| RULE-WD-RESTORE-EXIT | 1 | 2 hwmon channels seeded enable=2, flipped to manual, Restore called, both files asserted back at 2 |
| RULE-WD-RESTORE-PANIC | 1 | synthetic slog panic injected on call #1 via `countingPanicHandler`; second entry still receives restore — proves per-entry recover frame fires |
| RULE-WD-FALLBACK-MISSING-PWMENABLE | 1 | channel A with absent pwm_enable + channel B with valid enable; B asserted restored after A's log-and-continue fallback |
| RULE-WD-NVIDIA-RESET | 1 | nvidia entry with non-parseable index → logged-and-skipped; valid index dispatches into nvidia pkg, no PWM=0 written |
| RULE-WD-RPM-TARGET | 1 | seeded fan1_max with non-default value; pwm_enable absent; restore writes max RPM (not "255") to fan1_target |
| RULE-WD-DEREGISTER | 1 | unknown path no-op + double-Deregister of same path removes one entry (LIFO) |
| RULE-WD-RESTORE-BUDGET | 3 | (a) 3 fast channels under 500 ms budget all restore, no WARN, wall-clock < budget; (b) hung channel under 100 ms budget — function returns within budget+grace, WARN names hung path, others restored; (c) pre-cancelled ctx skips backend, _enable stays perturbed, WARN names cancellation |
| RULE-WD-REGISTER-IDEMPOTENT | 1 | Register + Deregister cycle preserves startup entry's origEnable |
| RULE-WD-PER-SYSCALL-DEADLINE | 5 | (a) pre-cancelled ctx makes read goroutine race-lose to ctx.Done — read abandoned; (b) writeWithDeadline returns bounded wall-clock regardless of underlying write completion; (c-e) NVML reset wrapper abandoned on deadline; success passthrough; backend-integration |
| RULE-WD-PRIOR-CRASH-FALLBACK | 3 | (a) live enable=1 + no store → SafePreDaemonEnable=2; (b) live enable=1 + store has 2 → recovers persisted 2; (c) live enable=legitimate → persists to store |
| RULE-WD-IPMI-ROUTING | 1 | RegisterIPMI binds a callback recorded via atomic.Int32; watchdog's Restore loop asserts callback was invoked |

### BORDERLINE / WEAK / GHOST

**None.** Every test names the specific issue number (#1038, #1039, #1041, #1042, #1043) it pins, exercises the production helper directly with a recording stub, and asserts the operator-visible signal. The watchdog family is the cleanest sub-pass to date.

## Why this family audits so cleanly

The RULE-WD-* family is dominated by:
1. Pure functions / methods on `*Watchdog` callable in isolation (Restore, RestoreCtx, RegisterIPMI, restoreOne)
2. Synthetic backend stubs (`fakehwmon`, hand-rolled IPMI callbacks) that record dispatch
3. Wall-clock and goroutine-count assertions that exercise the budget contracts directly

The heuristic-failure class from pass-3-smart-mode-wiring (claims about call sites buried in long-running entry-points like `Manager.run` or `runDaemonInternal`) is absent here — every watchdog entry-point is testable in O(10) lines of fixture.

## Running tally (5 sub-passes complete)

| sub-pass | total | SOLID | BORDERLINE | WEAK | GHOST |
|---|---|---|---|---|---|
| smart-mode-wiring | 5 | 2 | 0 | 3 | 0 |
| RULE-CPL-* | 15 | 13 | 2 | 0 | 0 |
| RULE-CMB-* | 26 | 22 | 4 | 0 | 0 |
| RULE-POLARITY-* | 11 | 9 | 1 | 0 | 1 (filed) |
| RULE-WD-* | 11 | 11 | 0 | 0 | 0 |
| **total** | **68** | **57** | **7** | **3** | **1** |

## Filing

No fileable issues from this sub-pass.

## Next

RULE-STATE-* (12 rules, recently overhauled by #1066). Same shape: pure functions on `*KVDB` / `*LogDB` / `*BlobDB`, all testable in isolation. Per the heuristic, expect predominantly SOLID.

After RULE-STATE-*, the audit pivots to higher-volume / lower-risk families (RULE-HWMON-*, RULE-HAL-*, RULE-PROBE-* etc.). Phase plan: ~6 more sub-passes to cover the catalogue, no individual sub-pass running more than ~30 rules.
