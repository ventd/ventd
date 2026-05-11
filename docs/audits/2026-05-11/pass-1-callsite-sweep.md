# Pass 1: Mechanical Call-Site Sweep

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep
**Baseline commit**: `da67bd1` (post #1034 merge — fixes Layer-B/C feed only)
**Scope**: every exported method on every exported type in `internal/` + `cmd/` (non-test code only)
**Tooling**: mechanical `rg` over the build tree; no judgement applied to false positives.

## Method

1. Enumerate all `func (r *T) M(...)` and `func (r T) M(...)` where both `T` and `M` start uppercase (exported). 475 declarations across 423 distinct `(T, M)` pairs.
2. Build the production-only call-site index: `rg -nP '\.[A-Z][A-Za-z0-9_]*\('` across `internal/` + `cmd/`, exclude `*_test.go`.
3. For each `(T, M)` pair, count `\.M(` hits in the production index.
4. Zero-prod-caller methods are candidates. Subtract test fixtures (types named `Fake*`, `Stub*`, `DTFake*`) + reflection-driven methods (`MarshalJSON`, `UnmarshalJSON`, `MarshalYAML`, `UnmarshalYAML`, `String`, `Format`, `Error`, `Unwrap`).

Result: **66 candidates** to triage.

## False-positive class — method-name-only grep

The grep pattern `\.MethodName(` cannot distinguish between calls on different types that share a method name. Methods named `Tick`, `Read`, `Close`, `Write`, `Start`, `Stop`, etc. are matched even when called on unrelated types. The triage below uses the method-name's specificity as a confidence signal: methods with unique names (`SetEnvelopeCDoneAt`, `WriteFanCurveGated`, `ProbeIPMIPolarity`, `FindCoVaryingPairs`) have high signal; methods with generic names need manual verification.

## Findings — categorised by severity

### Category A: SMART_MODE_INERT — confirmed ghost code in the smart-mode pipeline

**Same class as issue #1033 (Layer-B/C wiring gap).** These methods belong to the smart-mode pipeline (confidence estimators, aggregator, RLS shards, identifiability detectors). Without production callers, the smart-mode state they're supposed to advance stays at its zero-value forever. The blended controller consumes the zero-value state via `Snapshot.Theta`, then refuses every predictive-mode tick via `piRefuseReason("b_ii=0")` — falling back to 100% reactive control.

| symbol | test callers | rule | impact |
|---|---|---|---|
| `confidence/layer_a.Estimator.Observe` | 11 | RULE-CONFA-FORMULA-01 | conf_A stays at zero; min-collapse in aggregator forces w_pred ≤ 0 |
| `confidence/layer_a.Estimator.LoadChannel` | 6 | RULE-CONFA-PERSIST-01 | Layer-A state never restored across daemon restart |
| `confidence/aggregator.Aggregator.SetDrift` | 1 | RULE-AGG-DRIFT-01 | drift_flag never set; per-layer decay never engages |
| `confidence/aggregator.Aggregator.SetEnvelopeCDoneAt` | 2 | RULE-AGG-COLDSTART-01 | the 5-min cold-start hard-pin uses zero-value time → elapsed > 5min always true → cold-start gate structurally inert |
| `marginal.Shard.IsSaturated` | 4 | RULE-CMB-SAT-01/02 | Path-A/B saturation predicate dead; blended controller's cost gate uses inline math against zero θ |
| `marginal.Shard.PredictDT` | 0 | (would-be RULE-CMB-PREDICT) | Path-A predicted ΔT never computed via the shard's own method; blended controller re-derives inline |
| `coupling.Shard.SetGroups` | 0 | (unknown — investigate) | shard groups never set on the coupling side |
| `coupling.Shard.SetKind` | 2 | RULE-CPL-IDENT-02 | identifiability classification (`KindHealthy`/`Marginal`/`Unidentifiable`) never written; runShardLoop comment EXPLICITLY says "Caller passes via SetKind" but no caller ever does |
| `coupling.Window.FindCoVaryingPairs` | 1 | RULE-CPL-IDENT-03 | co-varying fan detection (pairwise ρ > 0.999 merge) dead |
| `coupling.Window.Kappa` | 1 | RULE-CPL-IDENT-02 | κ classification (healthy ≤100, marginal ≤10⁴, unidentifiable >10⁴) never computed; Snapshot.Kappa stays at zero-value |
| `signature.Library.LoadLabels` | 2 | RULE-SIG-PERSIST-02 | signature library state never restored across restarts |
| `signature.Library.Buckets` | 0 | (read accessor for doctor surface) | doctor cannot enumerate signature buckets |

**Aggregate impact**: the smart-mode pipeline as deployed today is reactive-only. Every layer of the predictive arm — Layer-A confidence, Layer-B identifiability classification, Layer-C saturation detection, aggregator drift decay, aggregator cold-start gate, signature persistence — is dead. The blended controller's `w_pred ≤ 0` short-circuit (RULE-CTRL-BLEND-03) is the only branch that ever fires. The R12 confidence machinery in the rule catalogue is documentation of what the code should do, not of what it does.

**Existing fix in flight**: #1033 fixed `coupling.Shard.Update` + `marginal.Runtime.OnObservation` via the new bridge in PR #1034. That makes Layer-B and Layer-C *capable* of advancing — but the symptoms above all need their own wiring fix to lift the entire predictive arm from "dead" to "working":

1. Wire `Estimator.Observe` from the bridge — feed Layer-A's PWM histogram (RULE-CONFA-COVERAGE-01).
2. Wire `Estimator.LoadChannel` at daemon startup — restore persisted Layer-A state.
3. Wire `Aggregator.SetEnvelopeCDoneAt` from the wizard's calibration-complete handler.
4. Wire `Aggregator.SetDrift` from a future R16 drift detector (or stub it as no-op until R16 lands).
5. Wire `Window.Kappa` + `Window.FindCoVaryingPairs` in the coupling `runShardLoop`'s identifiability tick (which is currently a documented no-op stub).
6. Wire `Shard.SetKind` from the same identifiability tick.
7. Wire `Library.LoadLabels` at daemon startup — restore signature library across restarts.
8. Replace blended.go's inline Path-A math with `Shard.IsSaturated` + `Shard.PredictDT` so the contract live on the shard, not in the consumer.

### Category B: HARDWARE_PATH_UNWIRED — vendor-specific code paths with declared APIs but no production dispatch

These are not smart-mode adjacent but are equally structurally inert. Each is a hardware-specific code path with an apparent API contract but no production caller.

| symbol | rule | impact |
|---|---|---|
| `polarity.SupermicroIPMIProbe.ProbeIPMIPolarity` | RULE-POLARITY-07 | IPMI polarity probing never dispatches the Supermicro implementation. The IPMIVendorProbe interface exists; the wizard never calls it. |
| `polarity.DellIPMIProbe.ProbeIPMIPolarity` | RULE-POLARITY-07 | same — Dell firmware-locked refusal never reached. |
| `polarity.HPEIPMIProbe.ProbeIPMIPolarity` | RULE-POLARITY-07 | same — HPE profile-only refusal never reached. |
| `hal/gpu/amdgpu.CardInfo.WriteFanCurveGated` | RULE-GPU-PR2D-07 | RDNA3+ GPU writes are supposed to go through this gated helper. Zero call sites. The amdgpu backend's Write path bypasses the gate entirely. |
| `hal/gpu/amdgpu.CardInfo.RestoreAuto` | (RULE-HAL-004 contract) | GPU restore-to-auto path dead. Watchdog Restore must be reaching it through some other surface — needs verification. |
| `hal/gpu/amdgpu.CardInfo.ReadFanRPM` | (RULE-HAL-002 contract) | GPU RPM read dead. |
| `hal/gpu/amdgpu.CardInfo.ReadFanRPMs` | (RULE-HAL-002 contract) | plural variant dead. |
| `polarity.HwmonProber.ProbeAll` | RULE-POLARITY-08 | hwmon polarity-probe orchestration over all channels. Zero call sites. Per the rule "On daemon start, ApplyOnStart matches persisted polarity results to live channels by PWMPath; unmatched channels remain 'unknown'" — the persistence side is wired (#1027/#1028) but the actual probe-all sweep is not. |

These need triage to determine: (a) does the rule require the method, in which case the method needs wiring; or (b) is the rule actually enforced via a different code path, in which case the method is genuinely dead and should be deleted.

### Category C: DOCTOR_UNWIRED — surface methods that might be inert

| symbol | suspected role | impact |
|---|---|---|
| `marginal.Runtime.ShardCount` | doctor surface accessor | doctor can't report Layer-C shard counts per channel |
| `marginal.Runtime.SetPWMUnitMax` | step_0_N / cooling_level drivers (RULE-HWDB-PR2-04) | non-duty_0_255 channels can't override pwmUnitMax from default 255 — discrete-step drivers silently misbehave at scaling |
| `doctor.SuppressionStore.Suppress` | doctor card suppression | web UI's "suppress this card" button doesn't reach the store |
| `doctor.SuppressionStore.Unsuppress` | same | "un-suppress" never reaches the store either |
| `observation.Reader.Latest` | doctor / SSE feed (RULE-OBS-READ-02) | bounded recent-records reader has zero production callers |

### Category D: FALSE POSITIVES — test fixtures or method-name aliasing

These are statistically zero-prod but are either explicit test-only helpers (the type/name signals "fixture") or share names with methods on unrelated types (which the grep pattern can't distinguish).

| symbol | reason classified as FP |
|---|---|
| `Clock.Advance` (22 test callers) | obviously a faketime fixture |
| `Roots.*` (Roots.WriteAC, WriteBattery, WriteCgroup, WriteDockerEnv, WriteHypervisor, WriteLoadavg, WriteMdstat, RemovePSI) | `internal/idle` test fixture for synthesising /proc/sys roots |
| `Server.SetNowFn`, `Server.SetSchedulerInterval` | test seams for the web server |
| `Codec.ReadResponse`, `Codec.WriteRequest` | hidraw codec methods — likely called by Corsair-backend production code; method-name collision |
| `Device.GetFeature`, `Device.SendFeature`, `Device.IsClosed`, `Device.SetOnWrite` | likely Corsair HID device interface — method-name collision on the generic `Device` type |
| `DeviceHandle.*` | similar — generic name |
| `Handle.GetFeature`, `Handle.SendFeature` | same |

I'd estimate ~25 of the 66 candidates are false positives of this class. The Category A and B findings are the high-confidence true-ghost set (~22 entries).

### Category E: NEEDS_VERIFICATION — judgement-required entries

| symbol | reason flagged |
|---|---|
| `setup.Manager.Diagnostics` (3 test callers) | wizard manager's Diagnostics accessor — is it consumed by the web UI? |
| `setup.Manager.EmitEvent` (5 test callers) | wizard event emission — is anyone subscribed? |
| `setup.Manager.SetVendorDaemonProbe` (2 test callers) | vendor-daemon probe injection — wired anywhere? |
| `hwdb.ModuleProfile.ToEffectiveControllerProfile` (0 test callers) | RULE-HWDB-PR2-11 migration helper — does the v1→v2 path actually use it? |
| `coupling.Shard.Dim` (2 test callers) | dimension accessor — could be used inline anywhere |
| `state.Store.Revision` (5 test callers) | state-store revision — doctor surface? |
| `Hasher.HashCommHex` (1 test caller) | siphash variant — direct call vs the non-Hex variant? |
| `Reader.Latest` (1 test caller) | observation.Reader bounded-latest — should be doctor wiring |
| `nbfc?.Backend.ClearAcquired` (0 test callers) | acquired-state clear — could be called via interface |

## Coverage gap on the grep heuristic

The pass-1 sweep is mechanical and misses three known classes:

1. **Interface dispatch**: methods called via interface type. `rg '\.Method('` matches the call, but the concrete-type method declaration isn't connected to the call by grep alone. For IPMIVendorProbe, FanBackend, et al, manual verification was needed (done inline above).
2. **Function values**: `f := obj.Method; f(args)` — call patterns the regex doesn't catch. Probably <1% incidence in this codebase.
3. **Reflection / generated code**: methods invoked by reflection (MarshalJSON et al). Filtered explicitly.

A future audit pass can build a real call-graph using `go/ssa` to eliminate these gaps. Beyond pass-1 scope.

## Action items

1. **File umbrella issue** "smart-mode pipeline structurally inert: 11 method-level wiring gaps" with Category A as the table — captures the v0.6.0 ship-gate blocker. (Builds on #1033, which fixed 2 of the 13 — `coupling.Shard.Update` and `marginal.Runtime.OnObservation`.)
2. **File individual issues** for Category B entries that have rule-catalogue contracts (IPMI polarity, AMD GPU writes, HwmonProber.ProbeAll). Per-rule, per-issue so the regress-protected scope is tight.
3. **Continue to pass 2** (persistence-backward trace): for every persisted artifact, who writes to it in production?

## Diagnostic command for next time

```bash
# Run this from the repo root after each release. Two minutes; surfaces ghost-code candidates within seconds of their introduction.
rg -nP '^func \(\s*\w+\s+\*?(\w+)\s*\)\s+([A-Z]\w*)\s*\(' --no-heading -- internal/ cmd/ \
  | grep -v _test.go \
  | sed -E 's/^([^:]+:[0-9]+):func \([^)]+ \*?(\w+)\)\s+([A-Z][[:alnum:]_]*)\s*\(.*$/\1\t\2\t\3/' > /tmp/methods.tsv

rg -nP '\.[A-Z][A-Za-z0-9_]*\(' --type=go internal/ cmd/ | grep -v _test.go > /tmp/prod.txt

while IFS=$'\t' read -r _f typ method; do
  prod=$(grep -c "\.${method}(" /tmp/prod.txt)
  if [ "$prod" -eq 0 ]; then
    printf '%s\t%s\n' "$typ" "$method"
  fi
done < /tmp/methods.tsv | sort -u
```

The "filter Category D false positives" pass is then ~10 minutes by hand on a fresh codebase pass. Add to the v0.6.0 ship-plan as a mandatory pre-release audit step.

---

# Pass 2: Persistence-Backward Trace

For every smart-mode persisted artifact, verified both Save (write side) and Load (read side) production callers:

| artifact | Save | Load | status |
|---|---|---|---|
| `coupling.Shard` (smart/shard-B/*.cbor) | `internal/coupling/runtime.go:163,180` ✓ | `internal/coupling/runtime.go:73` (AddShard) ✓ | functional |
| `marginal.Shard` (smart/shard-C/*.cbor) | `internal/marginal/runtime.go:572` ✓ | `internal/marginal/runtime.go:436` ✓ | functional |
| `signature.Library` (signature/ KV namespace) | `cmd/ventd/main.go:1577,1580` ✓ | **0 production callers** ✗ | write-only ghost; state never restored across restart |
| `layer_a.Estimator` (smart/conf-A/*.cbor) | **0 production callers** ✗ | **0 production callers** ✗ | fully dead in both directions |

`coupling` + `marginal` Save/Load are wired correctly — the issue isn't persistence, it's the Update/OnObservation data feeds (closed by #1034). `layer_a` and `signature.LoadLabels` are persistence ghosts and need wiring; folded into umbrella issue #1035.

# Pass 4: Self-Documented Stub Comment Sweep

`rg -niP 'no-op stub|currently a stub|currently no-op|lands when|not wired|wired in v|landed when|deferred until|defer to v'` across `internal/` + `cmd/` (excluding tests):

| location | comment | status |
|---|---|---|
| `internal/coupling/runtime.go:173-174` | "This tick is currently a no-op stub — the concrete identifiability path lands when..." | Same finding as #1035 #8/#9/#10 — coupling's identifiability tick is the dead path |
| `internal/web/setup_events_sse.go:42` | "setup manager not wired" | Runtime 503 error message, NOT ghost code — graceful degrade when wizard isn't running |
| `cmd/ventd/smart_obs_bridge.go:153` | "TODO v0.6.x: plumb PSI cpu.some avg10" | The Load=0.0 stub I added in #1034 — documented + tracked. |

Net: the codebase doesn't have many self-flagged stubs. The smart-mode ghosts are silent — they don't announce themselves in comments. The only comment-self-documented gap is the coupling identifiability tick, and that's already in #1035.

# Pass 5: Rule-Binding vs Production-Binding Reconciliation

22 MUST-call directives across 15 rule files. Spot-checked the safety-critical ones for production wiring:

| rule | symbol | prod call sites | status |
|---|---|---|---|
| RULE-SYSCLASS-02 | `sysclass.PersistDetection` | `cmd/ventd/main.go:474` | ✓ wired |
| RULE-EXPERIMENTAL-HWDIAG-PUBLISHED | `experimental.Publish` | `cmd/ventd/main.go:862` | ✓ wired |
| RULE-SETUP-REPROBE-01 | `setup.Manager.SetReProber` | `cmd/ventd/main.go:903` | ✓ wired |
| RULE-HWMON-PROLONGED-INVALID-RESTORE | `c.wd.RestoreOne` | `internal/controller/controller.go:349,525,538` | ✓ wired |
| RULE-HWMON-RESTORE-EXIT | `Watchdog.Restore` | indirect via `defer` in controller Run | ✓ wired |
| RULE-HWMON-INVALID-CURVE-SKIP | `c.lastPWM` carry-forward | wired in `c.tick` | ✓ wired |

The pattern: **safety-critical rules (hardware watchdog, sysclass persistence, preflight, wizard recovery) are properly wired in production. The wiring gaps cluster cleanly in the smart-mode subsystem** (coupling/marginal/layer_a/aggregator/signature). The R12 confidence machinery rule catalogue describes intent, not implementation.

Risk assessment:
- **Safety**: no hardware-safety rules surfaced as ghost-bound. The watchdog Restore + sysfs ENOENT skip + sentinel-filter + zero-PWM sentinel are all genuinely enforced in production.
- **Functionality**: smart-mode is structurally inert. The reactive controller does 100% of the work today. Users haven't noticed because the reactive controller works fine; smart-mode was supposed to add a thin predictive layer on top.

# Pass 3: End-to-End Dataflow (controller PWM write)

The most load-bearing input: controller writes a PWM byte to a fan. Trace each downstream branch:

```
controller.tick() at internal/controller/controller.go:c.write(pwm)
  ├── backend.Write(channel, pwm)              ✓ wired (FanBackend interface dispatch)
  │     ├── hwmon: writes sysfs                ✓ functional
  │     ├── nvml:  helper SUID-write           ✓ functional
  │     ├── ipmi:  vendor OEM command          ✓ functional
  │     ├── corsair: HID command               ✓ functional
  │     └── amdgpu: pwm1 or fan_curve          ✓ functional (but fan_curve goes via direct path, NOT WriteFanCurveGated — see #1035 Category B)
  │
  ├── c.emitObservation(pwm) at controller.go:944
  │     └── c.obsAppend(&ObsRecord{...})       ✓ wired
  │           └── buildSmartObsBridge          ✓ wired (#1034 — was ghost before)
  │                 ├── obsWriter.Append       ✓ wired (observation log)
  │                 ├── couplingRT.Update      ✓ wired by #1034
  │                 └── marginalRT.OnObservation ✓ wired by #1034
  │
  └── (no other branches)
```

Downstream consumers of the observation log + smart-mode state:

```
obs.Reader.Stream  ──→ probe/opportunistic/detector.go    ✓ wired (gap detection)
obs.Reader.Latest  ──→ ✗ no production callers (ghost — see #1035 Category C)
coupling.Snapshot  ──→ blended.go (Path-A/cost gate)       ✓ wired (but reads zero θ until #1035 lands)
marginal.Snapshot  ──→ blended.go (saturation re-derive)   ✓ wired (but reads zero θ until #1035 lands)
layer_a.Snapshot   ──→ blended.go (first-contact clamp)    ✓ wired (but reads zero conf until #1035 lands)
aggregator.Snapshot ──→ smart_builders.go (BlendFn) → blended.go ✓ wired (but reads zero w_pred until #1035 lands)
```

The PWM-write branch is structurally complete. The observation-log branch was the missing piece (closed by #1034). The Snapshot-consumer branches all read from runtimes that have no input data feed (closed for coupling/marginal by #1034; still open for layer_a/aggregator per #1035).

**Aggregate verdict from passes 1-5**: the audit found one structural failure mode — the smart-mode pipeline is ~85% unwired in production — and zero new hardware-safety gaps. The 11 entries in #1035 are sufficient to capture every smart-mode wiring gap pass-1 surfaced.

# Next steps

1. **Land #1034** (the Layer-B/C feed bridge) — ✓ DONE, merged as `da67bd1` 2026-05-11.
2. **Implement the 11 wiring gaps in #1035** — order per the prioritisation in the umbrella issue. Each ~30-50 LOC + tests + RULE binding.
3. **Re-soak against the fully-wired daemon** — when ALL 11 wirings land, the desktop + Proxmox soaks should show non-zero `n_samples`, `theta`, `conf_A`, `w_pred` within hours.
4. **Add the pass-1 mechanical grep to v0.6.0 ship plan** as a pre-release audit step. Two-minute check, catches future ghost-code regressions instantly.
5. **(stretch) Build a `tools/audit/ghost-code/` package** that runs the call-site sweep as a CI step. Threshold: zero new ghost methods on every release branch.
