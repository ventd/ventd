# Pass 6: internal/polarity/ deep read

**Files audited:**
- `internal/polarity/polarity.go` (115 LOC)
- `internal/polarity/hwmon.go` (188 LOC)
- `internal/polarity/nvml.go` (220 LOC)
- `internal/polarity/ipmi.go` (188 LOC)
- `internal/polarity/persist.go` (147 LOC)
- `internal/polarity/start.go` (69 LOC)

**LOC:** 927 (non-test)

**Files NOT audited:** `polarity_test.go`, `fixtures/fixtures.go` —
test scaffolding only, per scope.

**Time on task:** ~30 min (read + cross-ref against controller +
wizard + persist callers + writeup).

**Baseline commit:** `b46c1a5` (Pass 1-5 audit doc) on top of
`da67bd1` (#1034 Layer-B/C feed). Verified against `cmd/ventd/main.go`
@ #1027 (SetChannelResolver wiring) and #1028 (SetPolarityProber wiring).

## Critical findings

### C1. The `polarity` package's persistence + start-time apply chain is entirely unwired in production — every restart re-classifies polarity from scratch, and the wizard's polarity result never reaches the controller

Zero production callers for:

| symbol | file | role |
|---|---|---|
| `polarity.Persist` | `persist.go:26` | saves wizard polarity results to `calibration` KV |
| `polarity.Load` | `persist.go:46` | restores `PolarityStore` from `calibration` KV |
| `polarity.ApplyOnStart` | `start.go:22` | RULE-POLARITY-08: daemon-start match-by-PWMPath |
| `polarity.ApplyPersisted` | `persist.go:104` | helper used by `ApplyOnStart` |
| `polarity.ApplyToChannel` | `polarity.go:79` | writes `ch.Polarity` from a `ChannelResult` |
| `polarity.HwmonProber.ProbeAll` | `hwmon.go:173` | RULE-POLARITY-08: sweep-all orchestration |

The wizard at `internal/setup/setup.go:1097-1161` calls
`prober.ProbeChannel` per channel (so `ProbeAll` is unused) and stores
the result on its **own** `FanState.PolarityPhase` field as a string —
which is only consumed locally to mark `"phantom"` channels for skip
during calibration (lines 1119, 1170). The wizard never:

- calls `polarity.Persist(db, results)` to save the classifications;
- propagates `"inverted"` polarity into the generated `config.Fan`
  or the persisted `hwdb.ChannelCalibration`;
- writes `ch.Polarity` back onto a persisted `ControllableChannel`.

`polarity.ApplyOnStart` exists in the rule catalogue (RULE-POLARITY-08)
as the daemon-start authority — load PolarityStore, match by PWMPath,
set `ch.Polarity` + `ch.PhantomReason`. There is no caller. The
post-restart contract — "match persisted polarity to live channels by
PWMPath; unmatched stay 'unknown'" — is structurally inert.

**Aggregate effect**: every channel in production runs with whatever
`Polarity` value `probe.NewControllableChannel` initialised. That
default is `"unknown"`. `polarity.WritePWM` at `polarity.go:97-98`
refuses `"unknown"` with `ErrPolarityNotResolved`. So the only paths
that call `polarity.WritePWM` (envelope prober, opportunistic prober —
see C2 below) would refuse every write on every channel after a
restart, until the wizard re-runs.

This is the C1 cluster. Severity: HIGH (structural). Same class as
pass-1 #1035 (smart-mode wiring gaps): the documented contract is the
rule catalogue's description, not the running behaviour.

### C2. The controller's primary write path bypasses `polarity.WritePWM` entirely — the polarity package's RULE-POLARITY-05 refusal contract is enforced on the wrong code paths

`internal/controller/controller.go:316-322`:

```go
func (c *Controller) writeWithRetry(ch hal.Channel, pwm uint8, ...) error {
    if _, err := hwdb.ShouldApplyCurve(c.calCh); err != nil {
        ...
        return err
    }
    adjusted := uint8(hwdb.InvertPWM(c.calCh, int(pwm), c.pwmUnitMax))
    writeErr := c.backend.Write(ch, adjusted)
    ...
}
```

The controller's main PWM write path:
- inverts via `hwdb.InvertPWM(c.calCh, ...)` — reads
  `ChannelCalibration.PolarityInverted`, **not** `ch.Polarity`;
- gates phantom-write via `hwdb.ShouldApplyCurve(c.calCh)` — reads
  `ChannelCalibration.Phantom` (validity-probe field), **not**
  `ch.Polarity == "phantom"`.

`polarity.WritePWM` (the rule-catalogue refusal contract) is only
called by:
- `internal/envelope/envelope.go:39` (envelope prober — calibration-time);
- `internal/probe/opportunistic/prober.go:115,176` (opportunistic probe).

The controller's hot path **never** consults the polarity package's
classification. Two distinct polarity state systems exist in parallel:

| state field | source | consumers |
|---|---|---|
| `ChannelCalibration.PolarityInverted` (bool) | `internal/validity/polarity.go` (zero prod callers — see C4) | controller `hwdb.InvertPWM` |
| `ControllableChannel.Polarity` (string) | `polarity.HwmonProber.ProbeChannel` (wizard Phase 5b) | `polarity.WritePWM` (envelope + opportunistic) |

These two systems never reconcile. A wizard that classifies a channel
as `"inverted"` writes it to `fans[i].PolarityPhase = "inverted"` in
the FanState but neither:
- propagates that to a persisted `ChannelCalibration` with
  `PolarityInverted: true`, nor
- persists the `ControllableChannel.Polarity` value so a daemon
  restart's `ApplyOnStart` (which doesn't run anyway, per C1) could
  consume it.

**Net effect**: an inverted-polarity fan classified by the wizard
runs in the wrong direction under the controller's main writeback
loop. The controller calls `hwdb.InvertPWM` with `c.calCh.PolarityInverted ==
false` (no production code ever flips that bit — see C4), the call
is a no-op, and `c.backend.Write(ch, raw_pwm)` writes the
un-inverted byte to sysfs.

Severity: HIGH. This is the structural pump-the-wrong-direction class
the polarity probe was designed to prevent, and the rule catalogue
(RULE-POLARITY-05) presents `WritePWM` as the universal gateway — but
the universal gateway only fires on probe paths, never on the control
loop.

### C3. `controller.tick` sentinel-carry-forward path bypasses every polarity contract — same finding as `pass-6-controller.md` C1, repeated here because it's also a polarity violation

`internal/controller/controller.go:541-548`:

```go
if c.hasLastPWM {
    ch, buildErr := c.channelFor(fan)
    if buildErr == nil {
        _ = c.backend.Write(ch, c.lastPWM)
    }
}
```

On a sensor-sentinel tick within the 30-second carry-forward window,
the controller calls `c.backend.Write` directly — skipping both
`writeWithRetry` (which at least invokes `hwdb.InvertPWM`) AND
`polarity.WritePWM`. Even if C2 were fixed by routing the main write
through `polarity.WritePWM`, this branch would still emit raw,
un-inverted bytes. Cross-referenced from `pass-6-controller.md` C1;
flagged here for the polarity-package audit's completeness.

### C4. `MatchKey`/`channelKey` produce systematically-mismatched keys for NVML and IPMI channels — even if `ApplyOnStart` were wired, it would never match these backends

`internal/polarity/persist.go:70-91`:

```go
func MatchKey(r ChannelResult) string {
    switch r.Backend {
    case "nvml":
        return fmt.Sprintf("nvml:%s:%d", r.Identity.PCIAddress, r.Identity.FanIndex)
    case "ipmi":
        return fmt.Sprintf("ipmi:%s:%s", r.Identity.Vendor, r.Identity.ChannelID)
    default:
        return fmt.Sprintf("hwmon:%s", r.Identity.PWMPath)
    }
}

func channelKey(ch *probe.ControllableChannel) string {
    switch ch.Driver {
    case "nvml":
        return fmt.Sprintf("nvml::%s", ch.PWMPath) // pci address not in channel yet
    case "ipmi":
        return fmt.Sprintf("ipmi::%s", ch.SourceID)
    default:
        return fmt.Sprintf("hwmon:%s", ch.PWMPath)
    }
}
```

For NVML: `MatchKey` = `"nvml:<pci>:<fanIndex>"`; `channelKey` =
`"nvml::<PWMPath>"` (empty pci slot, PWMPath in the fan-index slot).
These cannot collide except by accident — there is no live channel
identity that could roundtrip persist → load.

For IPMI: `MatchKey` = `"ipmi:<vendor>:<channelID>"`; `channelKey` =
`"ipmi::<sourceID>"` (empty vendor slot). Misses by `vendor` field.

The hwmon path matches because both forms use the PWMPath, which is
stable. NVML and IPMI would silently produce `MatchMissing` for every
persisted entry. The trailing comment "// pci address not in channel
yet" acknowledges the gap but the code ships.

Severity: HIGH (latent — only fires if C1 is fixed; until then no
GPU/IPMI host even reaches this code).

## High findings

### H1. RULE-POLARITY-07: all three `IPMIVendorProbe` implementations have zero production callers — confirmed from pass-1

`SupermicroIPMIProbe.ProbeIPMIPolarity` (`ipmi.go:39`)
`DellIPMIProbe.ProbeIPMIPolarity` (`ipmi.go:130`)
`HPEIPMIProbe.ProbeIPMIPolarity` (`ipmi.go:175`)

Production wiring sets `setupMgr.SetPolarityProber(&polarity.HwmonProber{})`
at `cmd/ventd/main.go:887` and `cmd/ventd/runsetup.go:99`. The
`IPMIVendorProbe` interface is never instantiated outside tests. The
wizard's Phase 5b polarity probe loop at
`internal/setup/setup.go:1103-1142` only calls `prober.ProbeChannel`
on the single registered prober, so even with an IPMI vendor instance
wired, the wizard would route IPMI channels through `HwmonProber.ProbeChannel`
— which would phantom-classify them (no `ch.TachPath`).

The probe dispatch by `ch.Backend` does not exist anywhere. Per
RULE-POLARITY-07 (Dell firmware-locked, HPE profile-only,
Supermicro OEM-write paths), the dispatch should happen in the wizard
based on channel backend type. Where it should fire: the wizard's
Phase 5b loop. The wiring gap.

### H2. RULE-POLARITY-08: `HwmonProber.ProbeAll` is dead — confirmed from pass-1

Pass-1 finding confirmed by source read. The wizard at
`internal/setup/setup.go:1106-1142` iterates channels itself and
calls `prober.ProbeChannel` per channel. `ProbeAll` (`hwmon.go:173`)
is unreachable in production.

Per RULE-POLARITY-08 the persistence side (`ApplyOnStart`) is the
load-bearing daemon-start path. The probe-ALL sweep is what should
fire when `ApplyOnStart` reports `NeedsProbe=true` for unmatched
channels. Both halves of the rule are dead:

- `ApplyOnStart` has no caller (C1).
- `ProbeAll` has no caller (here).

The wizard's per-channel loop substitutes for `ProbeAll` only in the
first-boot flow. The "restart and re-probe" flow described in the
rule (load persisted, fall through to ProbeAll for unmatched) has no
implementation.

### H3. `SupermicroIPMIProbe.ProbeIPMIPolarity` violates RULE-POLARITY-04 — no `defer` to restore on ctx-cancel mid-hold

`internal/polarity/ipmi.go:68-80`:

```go
select {
case <-ctx.Done():
    return res, ctx.Err()        // <-- returns WITHOUT restore
default:
}

s.clock()(HoldDuration)

select {
case <-ctx.Done():
    return res, ctx.Err()        // <-- returns WITHOUT restore
default:
}

observed := s.readSDRFan(ch.SourceID)
...
restore := []byte{0x30, 0x45, 0x00}
restoreResp := make([]byte, 4)
_ = s.SendRecv(restore, restoreResp)   // <-- only reached on success
```

The Supermicro path has no `defer` for SET_FAN_MODE=auto restore.
After issuing the OEM SET_FAN_SPEED at 50%, if the context cancels
during the hold window (lines 70 or 78), the fan stays at OEM-50%
mode indefinitely. The deferred-restore pattern that `HwmonProber`
and `NVMLProber` both implement is missing here.

RULE-POLARITY-04: "Baseline PWM is restored on every exit path —
write failure, context cancel, and normal return". Supermicro IPMI
violates the context-cancel branch.

(`DellIPMIProbe` has no hold-window write at all on the
firmware-locked path; HPE always-phantom is a no-op. Only
Supermicro has a real write that needs restore.)

### H4. `polarity.PolarityStore` has no concurrency primitives — if any future `ApplyOnStart` wiring lands alongside concurrent persist (e.g. mid-probe Save), races are unguarded

`internal/polarity/persist.go` exposes `Persist` and `Load` against
`*state.KVDB`. The KVDB itself is transactional, but the in-memory
flow has no mutex: a hypothetical wizard that calls `Persist` while
the daemon-start `ApplyOnStart` loop walks the channel slice would
race on `ch.Polarity` writes from `ApplyToChannel`. Today this is
moot (C1: neither side fires), but the rule-catalogue design assumes
both sides will eventually fire concurrently — e.g. a wizard re-run
while smart-mode is live.

Severity: MEDIUM. Latent until C1 lands. Flagged because the fix is
trivial (read-modify-write under a sync.Mutex on PolarityStore, or
keep the channel-mutation purely single-threaded post-load).

## Medium findings

### M1. NVML probe's `SetFanControlPolicy` ignores the `(bool, error)` "supported" return — drivers that report `(false, nil)` ("not supported on this driver/version") proceed to `SetFanSpeed` anyway

`internal/polarity/nvml.go:149`:

```go
if _, err := p.nvml().SetFanControlPolicy(id.GPUIndex, id.FanIndex,
        nvidia.FanPolicyTemperatureDiscrete); err != nil {
    res.Polarity = "phantom"
    res.PhantomReason = PhantomReasonWriteFailed
    return res, nil
}
```

The `_` discards the `supported` bool. A driver that returns
`(false, nil)` (per the `nvml.SetFanControlPolicy` contract — "not
supported, no error") admits the probe through to the next
`SetFanSpeed` call. That call would then likely fail with
NVML_FUNCTION_NOT_FOUND — fine for the immediate operation, but the
ctx-cancel deferred restore at lines 138-146 also calls
`SetFanControlPolicy(...baselinePolicy)`. On an unsupported driver
that returned `hasPolicy=true` from the GetFanControlPolicy at
line 133, this restore is also a no-op.

Severity: MEDIUM. The driver-version gate at RULE-POLARITY-06
(`major < 515` → phantom) is supposed to filter this case upstream,
so the `(false, nil)` path should never reach line 149 on any
real driver. But the bool is informational and discarding it
silently is brittle.

### M2. `HwmonProber.ProbeChannel`: PWM baseline read failure returns `PhantomReasonWriteFailed` despite no write being attempted

`internal/polarity/hwmon.go:73-78`:

```go
baselinePWMBytes, err := p.readFile(ch.PWMPath)
if err != nil {
    res.Polarity = "phantom"
    res.PhantomReason = PhantomReasonWriteFailed
    return res, nil
}
```

The reason code is misleading — no write was attempted yet. The set
of `PhantomReason*` codes (RULE-POLARITY-10) doesn't include
`read_failed`. Adding one would be the cleanest fix; using
`PhantomReasonNoTach` semantically conflates a missing tach with a
missing PWM read.

Operator-visible: the `ventd doctor` output and wizard surface show
`write_failed` when really the PWM file is unreadable. Diagnostic
clarity hit; not a safety issue.

### M3. Threshold-boundary classification: hwmon `|delta| < 150` (strict-less-than) means a delta of exactly 150 RPM classifies as `normal`/`inverted`, not phantom

`internal/polarity/hwmon.go:135`:

```go
case math.Abs(delta) < ThresholdRPM:   // 150 RPM
    res.Polarity = "phantom"
    res.PhantomReason = PhantomReasonNoResponse
```

RULE-POLARITY-03's rule text: "When `math.Abs(delta) >= ThresholdRPM
(150)` and `delta > 0`, polarity is `"normal"`. When `delta < 0`,
polarity is `"inverted"`. When `math.Abs(delta) < ThresholdRPM`,
polarity is `"phantom"`."

Code matches rule text. The rule treats 150 RPM as not-phantom. This
is consistent — flagging only because a stricter reading (e.g.
"below the noise floor") would naturally use `<=`. The rule and
code are aligned; this is documentation of the choice, not a bug.
Same form on NVML at `nvml.go:196` with ThresholdPct (10).

### M4. `HwmonProber.ProbeChannel` `baselinePWM = 128` fallback when sysfs read returns an unparseable string

`internal/polarity/hwmon.go:79-83`:

```go
baselinePWMStr := strings.TrimSpace(string(baselinePWMBytes))
baselinePWM, err := strconv.Atoi(baselinePWMStr)
if err != nil {
    baselinePWM = 128 // safe fallback
}
```

If the sysfs file returns garbage (highly unusual but possible on a
driver that returns "ERR" or "-" on init), the prober proceeds with
baselinePWM=128 and then writes 128 as the midpoint — so the
deferred restore is a no-op write of the same value, and the fan
stays at 128 PWM until the controller's next tick. The error is
swallowed, no log. Better behaviour: log the parse failure and
classify as phantom-read-failed.

Severity: low; the failure mode is rare and the controller takes
over within a tick.

### M5. `polarity.IsControllable` returns `true` for `"normal"|"inverted"` only — but the opportunistic prober uses it as a pre-write gate, missing the `"unknown"` case the rule treats as a refusal

`internal/polarity/polarity.go:106-108`:

```go
func IsControllable(ch *probe.ControllableChannel) bool {
    return ch.Polarity == "normal" || ch.Polarity == "inverted"
}
```

Consumers:
- `cmd/ventd/main.go:606`: skips channels where `!IsControllable`
- `internal/probe/opportunistic/detector.go:67`: filters gaps for
  non-controllable channels
- `internal/probe/opportunistic/prober.go:91`: refuses to fire on a
  non-controllable channel

These all behave correctly with the closed-set semantics. The risk
is in the symmetry with `WritePWM`: `WritePWM` returns
`ErrPolarityNotResolved` (distinct from `ErrChannelNotControllable`)
for `"unknown"` channels, but `IsControllable` collapses both
phantom and unknown into "not controllable". Today this is fine
because the prober paths use both checks — the early-out via
`IsControllable` and the inner `WritePWM` call. But future code
that uses only `IsControllable` and then writes directly would
silently treat "unknown" as "phantom" for refusal purposes, losing
the distinction the rule catalogue maintains.

Severity: LOW. Latent semantic conflation; no current bug.

## Non-findings (verified clean against rule contracts)

- **RULE-POLARITY-01 (midpoint=128 hwmon, 50% NVML)**: `hwmon.go:99`
  writes `[]byte("128\n")`; `nvml.go:154` writes `50`. Confirmed.
- **RULE-POLARITY-02 (3s hold ±200ms)**: `hwmon.go:113`, `nvml.go:167`,
  `ipmi.go:74` all call `p.clock()(HoldDuration)` where `HoldDuration
  = 3 * time.Second` (`polarity.go:31`). The ±200ms tolerance is on
  the rule side; the code emits an exact 3s sleep. Confirmed.
- **RULE-POLARITY-03 (|delta| < 150 / |delta| < 10%)**: thresholds
  match constants `ThresholdRPM = 150` and `ThresholdPct = 10`. See
  M3 for boundary discussion.
- **RULE-POLARITY-04 (restore on every exit path)**: `HwmonProber`
  defer at `hwmon.go:87-92` fires on write-failure, ctx-cancel
  before/after hold, normal classify; `NVMLProber` defer at
  `nvml.go:138-146` similar with both speed + policy restore. See H3
  for Supermicro IPMI's defer gap.
- **RULE-POLARITY-06 (NVML major < 515 → phantom + DriverTooOld)**:
  `nvml.go:119-124` parses `parseDriverMajor`, refuses with
  `PhantomReasonDriverTooOld`. Confirmed.
- **RULE-POLARITY-09 (WipeNamespaces atomic across wizard/probe/
  calibration)**: `internal/probe/persist.go:97-108` uses a single
  `WithTransaction` for all three namespaces; wired in production at
  `cmd/ventd/main.go:508` via `kvWiper`. Confirmed.
- **RULE-POLARITY-10 (all phantom reasons refused by WritePWM)**: the
  refusal at `polarity.go:95-96` dispatches on
  `ch.Polarity == "phantom"` regardless of the PhantomReason code.
  Six reason codes all flow through the same refusal branch. Confirmed.
- **Sentinel-error wiring**: `ErrChannelNotControllable` and
  `ErrPolarityNotResolved` are distinct sentinels, propagated by the
  opportunistic prober via `errors.Is` (`prober.go:116`). The
  envelope prober's channelWriter.Write returns them verbatim.

## Cross-package observation — duplicate polarity state systems

The audit surfaces a structural fork that's worth raising even though
it's outside `internal/polarity/`. Three separate polarity systems
exist:

1. `internal/polarity/` — the v0.5.2 polarity probe + write helper +
   persistence chain. Used by the wizard's Phase 5b and the
   opportunistic prober; unwired from controller writes and from
   daemon-start persistence (C1, C2).
2. `internal/validity/polarity.go` — the validity-probe-era polarity
   classifier (`ProbePolarity` → `PolarityInverted` enum). Returns
   a result; that result is never persisted onto a
   `ChannelCalibration` in production (zero callers — see C4).
3. `internal/hwdb/profile_v1.go::ChannelCalibration.PolarityInverted`
   (bool field) — read by the controller's `hwdb.InvertPWM` on every
   write. Never written by any production code path.

The controller reads (3). The wizard writes (1) but only to its own
local FanState. (2) is entirely dead. The three systems do not
reconcile.

This is a strict superset of the audit scope, but it's the dominant
finding: the polarity probe machinery exists and is well-tested,
the controller's polarity-inversion machinery exists, but the
plumbing between them does not. A correctly-wired pipeline would
flow: wizard ProbeChannel → polarity.Persist → polarity.ApplyOnStart
on next boot → controller.WithCalibration reads PolarityInverted from
ChannelCalibration → InvertPWM does the right thing. None of those
arrows exist in production today.

## Files NOT audited and why

- `polarity_test.go`, `fixtures/fixtures.go` — test scaffolding,
  read for cross-reference only per task brief.

## Summary

- **Findings count**: 11 (4 critical, 4 high, 5 medium/low)
- **Severity breakdown**:
  - Critical (C1-C4): 4 — persistence chain unwired, controller
    bypasses WritePWM, sentinel-carry-forward direct write,
    NVML/IPMI MatchKey mismatch
  - High (H1-H4): 4 — IPMI vendor probes dead, ProbeAll dead,
    Supermicro IPMI restore-on-cancel missing, PolarityStore
    unprotected
  - Medium/low (M1-M5): 5 — NVML supported-bool ignored, baseline
    read mis-classified, threshold boundary documentation, 128
    baseline fallback swallows error, IsControllable conflates
    phantom and unknown

- **Single most important finding**: The controller's hot PWM write
  path at `controller.go:316-324, 338, 544` never consults
  `polarity.WritePWM` or `ch.Polarity` — a wizard-classified inverted
  fan runs in the wrong direction in production because
  `hwdb.InvertPWM` reads a `ChannelCalibration.PolarityInverted` bool
  that no production code path ever sets to `true`.
