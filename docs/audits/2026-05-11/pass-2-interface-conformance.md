# Pass 2 — Interface-Conformance Sweep

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, second pass
**Baseline commit**: `39e77a7` (main after #1065/#1066/#1067/#1068/#1069/#1070 — all pass-1 fix PRs landed)
**Scope**: every named `interface { ... }` declaration in `internal/` + `cmd/` (non-test code), cross-referenced against production dispatch sites and concrete implementations.

## Method

1. Enumerate: `rg -nP '^type\s+[A-Z][A-Za-z0-9_]*\s+interface\s*\{' internal/ cmd/ --glob '!*_test.go' -g '*.go'` — **35 interfaces** found across 17 packages.
2. For each interface T, count "strict prod use sites" — every `\bT\b` hit in prod code minus comment lines (`//`) minus the declaration line itself. Strict counts ≤ 2 are candidates.
3. For each candidate, inspect the actual references to distinguish:
   - **Live but narrow**: field declaration + factory parameter / option function (normal pattern).
   - **Ghost**: declaration only; no field, no parameter, no return type, no type assertion.
4. For each ghost, check whether concrete implementations are instantiated in production. If implementations exist only in tests, the entire subsystem is dead.

## Inventory by package

35 interfaces across 17 packages: hal (1), hal/usbbase (3), controller (2), curve (2), watchdog (1), polarity (3), probe (1), probe/opportunistic (1), marginal (3), validity (1), preflight (1), observation (1), idle (1), calibrate (2), diag/redactor (1), doctor (1), doctor/detectors (10).

## Findings

### GHOST — confirmed dispatch surface dead in production

| package | interface | impls | impls instantiated in prod? | rule |
|---|---|---|---|---|
| `polarity` | `IPMIVendorProbe` | `SupermicroIPMIProbe`, `DellIPMIProbe`, `HPEIPMIProbe` | **No** — only `_test.go` references | RULE-POLARITY-07 |

**`polarity.IPMIVendorProbe` is dead in production.** The interface is declared at `internal/polarity/ipmi.go:12` with three concrete implementations (Supermicro / Dell / HPE), but production code never:
- Takes a parameter of type `IPMIVendorProbe`.
- Stores a `IPMIVendorProbe` field.
- Returns an `IPMIVendorProbe`.
- Type-asserts against `IPMIVendorProbe`.
- Instantiates any of the three concrete vendor probes.

The only references outside the declaration are two `_test.go` lines:
- `internal/polarity/polarity_test.go:317` — the RULE-POLARITY-07 binding subtest constructs each impl directly.
- The test fixture `polarity_test.go:93/323/358/396/413` exercises the impls via direct struct instantiation, never via interface dispatch.

The rule (RULE-POLARITY-07) claims:
> `IPMIVendorProbe` is an interface with `ProbeIPMIPolarity(ctx, ch) (ChannelResult, error)`. Three implementations are shipped: `SupermicroIPMIProbe` [...] `DellIPMIProbe` [...] `HPEIPMIProbe` [...]. Permanent phantom channels are excluded from the polarity-aware write path for the lifetime of the daemon without re-probing.

The dispatch surface required to make any of this real does not exist. A server with IPMI fan-control hardware that ventd polarity-probes today goes through the hwmon path or the polarity is set to `"unknown"` and stays there.

**Cross-reference**: pass-1 listed `SupermicroIPMIProbe.ProbeIPMIPolarity`, `DellIPMIProbe.ProbeIPMIPolarity`, `HPEIPMIProbe.ProbeIPMIPolarity` under Category B (HARDWARE_PATH_UNWIRED) — same finding at the method-call level. Pass 2 corroborates at the interface-dispatch level: not only is the method never called, the *interface itself* is never used as a type.

**Filing**: this is a new, separately-actionable issue distinct from #1037 (which was the polarity *write* path, already fixed by #1067). Filing follow-up issue post-pass.

### LIVE — interfaces with low strict-use count but real wiring

The following interfaces had strict-use counts of 1–4 but on inspection are normal struct-field + constructor-parameter patterns. Not ghost:

| interface | wiring shape |
|---|---|
| `controller.PanicChecker` | field on `Controller`; set via `WithPanicChecker` option. |
| `curve.StatefulCurve` | type-asserted at `controller.go:678` against `compiledCurve`; `PICurve` implements it. |
| `marginal.ShardLookup` / `ShardSnapshotReader` / `SignguardLookup` | constructor parameters to `marginal.NewRuntime`; wired in `cmd/ventd/smart_builders.go`. |
| `polarity.NVMLInterface` | field on `NVMLProber`; getter `nvml()`. |
| `probe/opportunistic.LastProbeStore` | field on `Scheduler.Config`; wired via `opportunistic.NewKVLastProbeStore(st.KV)` in `cmd/ventd/smart_builders.go:157`. |
| `validity.ChannelProber` | (3 strict uses; live). |
| `watchdog.LastKnownStore` | field on `Watchdog`; set via `NewWithStore`. |
| every `doctor/detectors.*FS` and `*Snapshotter` interface | field on the detector struct; set via the detector's `New*` constructor; wired in `cmd/ventd/doctor.go`. |

These have narrow dispatch but the rules-they-bind to are satisfied because the single field+constructor is the entire wiring contract.

### CORROBORATIONS — Pass-1 findings now visible at the interface layer

Pass-1 listed several methods as having zero production callers. Pass-2 confirms which ones are interface-dispatch dead (and therefore the whole abstraction is unused) vs which are zero-prod-caller methods on a still-live struct surface:

| pass-1 entry | pass-2 verdict |
|---|---|
| `confidence/layer_a.Estimator.Observe` | NOT interface-bound — direct method; pass-1 finding stands. Fixed by #1068. |
| `confidence/aggregator.Aggregator.SetEnvelopeCDoneAt` | NOT interface-bound — direct method; fixed by #1068. |
| `marginal.Shard.IsSaturated` / `PredictDT` | NOT interface-bound — direct method; #1068 added the call-site for IsSaturated; `PredictDT` is still inline-derived (deferred future cleanup, not load-bearing). |
| `coupling.Shard.SetGroups` / `SetKind` | NOT interface-bound — direct method on a concrete type. SetGroups is fixed by #1031. SetKind still unwired — Pass 1 noted this; filing as deferred follow-up. |
| `coupling.Window.FindCoVaryingPairs` / `Kappa` | NOT interface-bound — concrete struct methods; #1068 wired Kappa via the identifiability tick. FindCoVaryingPairs not yet wired; deferred. |
| `polarity.IPMIVendorProbe` impls (Supermicro / Dell / HPE) | **Pass-2 GHOST** — the interface itself is unused. Distinct from the polarity write path #1037 fixed via #1067. |
| `polarity.HwmonProber.ProbeAll` | NOT interface-bound — concrete method. Still unwired. |
| `hal/gpu/amdgpu.CardInfo.WriteFanCurveGated` / `RestoreAuto` / `ReadFanRPM(s)` | NOT interface-bound — concrete methods. The whole amdgpu helper surface is unwired through the HAL FanBackend dispatch; the live amdgpu backend bypasses these helpers. Deferred follow-up. |

## Summary

- **35 interfaces** in production.
- **1 confirmed GHOST**: `polarity.IPMIVendorProbe`. Distinct, fileable finding.
- **0 surprises** in the rest. Every other low-use interface is either wired at construction time or genuinely narrow but live.
- **Pass-1 method-level findings** still need their own fix work where not already addressed by the four merged fix PRs (#1067, #1068, #1069, #1070); the unaddressed residue is enumerated above.

## Net new fileable issues

1. **`polarity.IPMIVendorProbe` dispatch surface is dead** — the interface + three concrete implementations exist; no production code constructs any of them or dispatches via the interface. RULE-POLARITY-07 is documentation, not behaviour. Either wire the IPMI polarity path (probably gated by an IPMI hardware-detection step in the wizard) or delete the dead surface.

Additional residue from pass-1 that wasn't covered by the merged fix PRs:
2. `coupling.Shard.SetKind` still unwired — runShardLoop comment says "Caller passes via SetKind" but no caller does. Layer-B's identifiability classification field is still uninitialised in production.
3. `coupling.Window.FindCoVaryingPairs` unwired — co-varying-fan auto-merge (RULE-CPL-IDENT-03) is dead.
4. `polarity.HwmonProber.ProbeAll` unwired — the hwmon polarity-probe sweep API exists but the wizard never calls it; current per-channel probe is fine for live use but the bulk-probe API is dead.
5. `hal/gpu/amdgpu.CardInfo.{WriteFanCurveGated,RestoreAuto,ReadFanRPM,ReadFanRPMs}` unwired — RDNA3+ fan-curve dispatch helpers exist; the live amdgpu backend bypasses them via direct sysfs writes. Either re-route the backend through the helpers (and gain the RDNA4 kernel-gate guard) or delete the helpers.

## Next

Pass 3 — rule↔binding integrity sweep. Read every RULE in `docs/rules/` and verify the bound subtest exercises the invariant the rule text describes (not just that the subtest exists). Pass 2 only checks declared dispatch surfaces; Pass 3 closes the gap where a test passes by exercising a narrower path than the rule promises.
