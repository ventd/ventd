# spec-v0_5_2 — Polarity disambiguation across all controllable channel types

**Status:** DESIGN. Drafted 2026-04-28.
**Ships as:** v0.5.2 (second smart-mode behaviour patch).
**Depends on:** v0.5.1 spec-v0_5_1 catalog-less probe must be merged
first. NVML symbol loader (`internal/nvidia/`) and NVML probe scaffold
(`internal/hal/gpu/nvml/probe.go`) already on main from earlier work.
**Consumed by:** v0.5.3 Envelope C/D probe — operates on the
polarity-resolved channel set this patch produces.
**References:** `spec-smart-mode.md` §5.2 (procedure of record),
`specs/spec-v0_5_1-catalog-less-probe.md` (probe layer consumed),
`specs/spec-16-persistent-state.md` (KV store consumed).

---

## 1. Why this patch exists

v0.5.1 produces a `ProbeResult` with every `ControllableChannel.Polarity`
set to `"unknown"`. Downstream code cannot safely write PWM values
without knowing whether the channel is conventionally mapped (high
PWM = high RPM) or inverted (high PWM = low RPM).

v0.5.2 is the first patch that writes to PWM hardware in the
smart-mode era. Its sole job is to resolve polarity per channel
across **all** controllable channel types — hwmon, NVML, IPMI, EC.
No deferrals, no vendor classes left for a future patch to handle.

This patch does not calibrate. It does not learn. It produces three
classifications per channel — `normal`, `inverted`, `phantom` — and
persists them. Envelope C/D probe (v0.5.3) consumes the classified
channel set.

## 1.1 Ground-up principle

Every controllable channel type must reach a resolved polarity
classification by end of v0.5.2. There is no "deferred" state.
Channels for which polarity cannot be determined (firmware refuses
write, no tach to observe) are classified permanently as `phantom`
with a documented reason — not deferred, not pending, not "fix
later." Phantom is the honest answer when the hardware does not
admit polarity probing.

The rule statement (RULE-PROBE-06) that v0.5.1 shipped saying
"polarity is `unknown`" was incomplete. v0.5.2 corrects it as the
first commit of this PR. See §7.

---

## 2. Scope

### 2.1 In scope

- Polarity probe procedure for hwmon channels: PWM=128 midpoint
  write, 3-second hold, RPM observation, classification.
- Polarity probe procedure for NVML channels: target speed = 50%
  via NVML write API, 3-second hold, observed fan-speed comparison,
  classification.
- Polarity probe procedure for IPMI channels: per-vendor backend
  probe via existing vendor command interfaces. Each vendor backend
  exposes a `ProbePolarity(channel)` method.
- Polarity probe procedure for EC channels (ThinkPad
  `/proc/acpi/ibm/fan` etc.): identical to hwmon procedure since EC
  presents writable PWM and readable tach to userspace.
- Phantom channel detection: classify as phantom when no measurable
  RPM/speed change after midpoint/50% write, or when backend refuses
  the write.
- Per-channel classification persisted to spec-16 KV store under
  `calibration` namespace.
- Update `ControllableChannel.Polarity` from `"unknown"` to resolved
  value on probe completion.
- Phantom channels demoted to monitor-only at runtime — daemon does
  not write to them.
- Inverted channels: PWM-write callers receive a polarity-aware write
  helper that inverts the value before backend write.
- Resumability: per-channel polarity state survives daemon restart;
  daemon does not re-probe channels with valid persisted polarity
  unless reset path triggers.
- RULE-PROBE-06 correction (chore commit, first in PR).

### 2.2 Out of scope

- **User idle gate.** §5.3 of smart-mode. The polarity probe is brief
  enough (3 seconds per channel, sequential) that v0.5.2 runs it
  without an idle gate. v0.5.3 introduces the gate primitive.
- **Runtime load monitor.** §5.4 of smart-mode. v0.5.3.
- **Envelope C/D calibration.** v0.5.3.
- **Phantom reclassification on extended observation.** Once
  classified phantom, v0.5.2 does not re-probe. Reset path
  (spec-v0_5_1 §5.3) is the only way to re-probe.
- **Polarity drift detection.** v0.5.4+ Layer A response curve drift
  may catch polarity changes from hardware swaps; v0.5.2 does not.

---

## 3. The polarity probe procedure (per channel type)

### 3.1 hwmon channels (and EC channels presenting as hwmon-like)

Sequential, one channel at a time:

```
For each hwmon/EC channel C in ProbeResult.ControllableChannels:
  if C.TachPath == "":
    C.Polarity = "phantom"
    C.PhantomReason = "no_tach"
    persist
    continue

  baseline_pwm  = read(C.PWMPath)
  baseline_rpm  = read(C.TachPath) over 1 second, mean
  
  write(C.PWMPath, 128)
  sleep 3 seconds
  observed_rpm  = read(C.TachPath) over last 500ms, mean
  
  write(C.PWMPath, baseline_pwm)  // restore
  sleep 500ms
  
  delta = observed_rpm - baseline_rpm
  
  if abs(delta) < THRESHOLD_RPM:
    C.Polarity = "phantom"
    C.PhantomReason = "no_response"
  elif delta > 0:
    C.Polarity = "normal"
  else:
    C.Polarity = "inverted"
  
  persist C
```

`THRESHOLD_RPM` is **150 RPM**. Conservative bias toward phantom is
preferred — phantom channels are safe (no writes); a misclassified
controllable channel could oscillate the system. Quiet server fans
that produce <150 RPM swing between idle and midpoint will be
classified phantom; v0.5.4+ Layer A drift detection may reclassify
them.

### 3.2 NVML channels

NVML's API contract permits inversion in principle even though the
overwhelming majority of NVIDIA driver implementations map "speed
percent" monotonically to fan duty cycle. Probe is the same shape:

```
For each NVML channel C:
  baseline_speed = nvmlDeviceGetFanSpeed(C.Device, C.FanIndex)
  baseline_policy = nvmlDeviceGetFanControlPolicy_v2(C.Device, C.FanIndex)
  
  nvmlDeviceSetFanControlPolicy(C.Device, C.FanIndex, NVML_FAN_POLICY_MANUAL)
  nvmlDeviceSetFanSpeed_v2(C.Device, C.FanIndex, 50)
  sleep 3 seconds
  observed_speed = nvmlDeviceGetFanSpeed(C.Device, C.FanIndex)
  
  // Restore in deferred handler (always, even on error path)
  nvmlDeviceSetFanSpeed_v2(C.Device, C.FanIndex, baseline_speed)
  nvmlDeviceSetFanControlPolicy(C.Device, C.FanIndex, baseline_policy)
  sleep 500ms
  
  delta = observed_speed - baseline_speed
  
  if abs(delta) < THRESHOLD_PCT:
    C.Polarity = "phantom"
    C.PhantomReason = "no_response"
  elif delta > 0:
    C.Polarity = "normal"
  else:
    C.Polarity = "inverted"
  
  persist C
```

`THRESHOLD_PCT` is **10 percentage points** (NVML reports speed in
0-100 percent integer scale, not RPM). 50% midpoint should produce
≥20 percentage points swing on any working fan; 10pp threshold is
conservative.

NVML driver minimum: **R515**. Prerequisite check: probe verifies
driver version via `nvmlSystemGetDriverVersion` before attempting
write. Driver <R515 → channel marked phantom with reason
`"driver_too_old"` and diagnostic surfaces.

### 3.3 IPMI channels (per-vendor)

Each IPMI vendor backend (Supermicro, Dell, HPE) implements:

```go
type PolarityProbe interface {
    ProbePolarity(ctx context.Context, channel ChannelID) (PolarityResult, error)
}
```

Per-vendor implementation:

- **Supermicro AST (two-zone):** existing `0x30 0x70 0x66` mode set
  to manual, write zone target = 50%, 3-second hold, observe fan
  RPM via SDR, restore, classify. Real probe.
- **Dell PE 14G (iDRAC9 ≥3.34):** firmware refuses arbitrary fan
  writes. Probe attempts vendor manual-fan-set command. Refusal is
  the result — channel classified phantom with reason
  `"firmware_locked"`. This is the correct outcome; the firmware
  owns the fans.
- **HPE iLO5/6 (profile-only):** backend has no per-fan write
  capability. Probe attempts to set per-fan target via Redfish
  `Fans` resource; HPE returns 405/501. Channel classified phantom
  with reason `"profile_only"`. Backend separately exposes the
  4-bucket `ThermalConfiguration` setting at a higher API level
  (out of scope for v0.5.2's polarity probe).

The classification for Dell and HPE is **permanent**, not deferred.
The hardware semantically does not support per-channel polarity
because it does not support per-channel write. Phantom is the
honest answer. v0.5.3+ does not unlock these channels — they remain
monitor-only. Smart-mode on locked-firmware servers operates via
profile-level controls (deferred to spec-15 territory) and treats
individual fan channels as observable but not controllable.

### 3.4 Polarity-aware write helper

Once polarity is resolved, all PWM writes go through a single helper:

```go
func (c *ControllableChannel) WritePWM(ctx context.Context, value uint8) error {
    var actual uint8
    switch c.Polarity {
    case "normal":
        actual = value
    case "inverted":
        actual = 255 - value
    case "phantom":
        return ErrChannelNotControllable
    case "unknown":
        return ErrPolarityNotResolved
    default:
        return fmt.Errorf("invalid polarity: %q", c.Polarity)
    }
    return c.Backend.Write(ctx, c, actual)
}
```

Backend (`hwmon`, `nvml`, `ipmi`, `ec`) handles the actual write;
helper enforces polarity uniformly. NVML maps `value/255` to its
0-100 percent scale; IPMI maps to its vendor-defined unit. Inversion
applies at the helper layer regardless of backend.

### 3.5 Restore on every path

Every probe procedure (hwmon, NVML, IPMI) **must** restore baseline
on every exit path: success, read failure, write failure, context
cancellation, panic. Implementation uses `defer` with explicit error
handling. Subtest verifies that interrupt at any point in the
procedure leaves the channel at its baseline value.

For NVML specifically: baseline includes both the fan control policy
(`nvmlDeviceSetFanControlPolicy`) and the speed value
(`nvmlDeviceSetFanSpeed_v2`). Both must be restored.

---

## 4. Persistent state

### 4.1 KV namespace

Polarity state persists in spec-16 KV store under `calibration`
namespace:

```yaml
calibration:
  polarity:
    schema_version: 1
    channels:
      - source_id: "hwmon3"
        backend: "hwmon"
        identity:
          pwm_path: "/sys/class/hwmon/hwmon3/pwm1"
          tach_path: "/sys/class/hwmon/hwmon3/fan1_input"
        polarity: "normal" | "inverted" | "phantom"
        phantom_reason: "no_tach" | "no_response" | "firmware_locked" | "profile_only" | "driver_too_old" | "write_failed" | ""
        baseline: 128
        observed: 128
        delta: 0
        unit: "rpm" | "pct" | "vendor"
        probed_at: "2026-04-28T..."
```

Per-backend identity field captures whatever uniquely identifies the
channel within the backend:
- hwmon: `pwm_path` + `tach_path`
- NVML: `pci_address` + `fan_index`
- IPMI: `bmc_address` + `vendor` + `channel_id`
- EC: `proc_path` + `fan_index`

### 4.2 Daemon start sequence

1. Read `calibration.polarity.channels` from KV.
2. Match each persisted entry against current
   `ProbeResult.ControllableChannels` by `(backend, identity)` tuple.
3. On match: apply persisted polarity to channel.
4. On miss (channel newly appeared, hardware change): probe required.
   Daemon refuses to enter control mode until missing channels are
   probed. Surface in doctor.
5. On orphan persisted entry (channel disappeared): drop entry. Diag
   bundle notes the removal.

### 4.3 Reset path

Spec-v0_5_1 §5.3 "Reset to initial setup" wipes `wizard` and `probe`
namespaces. v0.5.2 extends the reset to also wipe `calibration`
namespace. RULE-POLARITY-09 covers.

---

## 5. Wizard integration

### 5.1 Polarity probe runs at wizard time

After v0.5.1's three-state classifier returns `OutcomeControl`, the
wizard now runs polarity probe before reaching the existing post-probe
flow.

```
ProbeResult → ClassifyOutcome → OutcomeControl →
  Polarity probe (this patch) →
    All channels classified phantom → demote to OutcomeMonitorOnly,
      surface monitor-only fork (per v0.5.1 §5.1)
    Some channels controllable → continue to existing wizard flow,
      with phantom channels marked monitor-only on dashboard
```

If the polarity probe demotes the system to monitor-only (every
channel phantom — common on Dell PE 14G + HPE iLO5/6 firmware-locked
hosts), the wizard re-enters v0.5.1's monitor-only fork with
appropriate diagnostic.

### 5.2 Wizard UI during probe

Single screen during polarity probe:

> "Testing fan response on N channel(s). This takes about 3 seconds
> per channel."

Per-channel state pill: pending / testing / normal / inverted /
phantom. No user interaction required mid-probe. Probe is brief
enough that no abort/cancel UI is needed; if the user closes the
wizard, daemon doesn't start, and channels are left at baseline (per
RULE-POLARITY-04 restore-on-exit).

---

## 6. Invariant bindings

| Rule ID | Statement |
|---|---|
| `RULE-POLARITY-01` | hwmon and EC polarity probe MUST write PWM=128 exactly. NVML probe MUST write speed=50%. IPMI probe MUST use the per-vendor midpoint command. Subtest verifies write capture per backend. |
| `RULE-POLARITY-02` | Polarity probe hold time MUST be 3 seconds ± 200ms across all backends. Subtest verifies via injected clock. |
| `RULE-POLARITY-03` | Phantom classification thresholds MUST be: hwmon `abs(delta_rpm) < 150`, NVML `abs(delta_pct) < 10`. IPMI thresholds defined per-vendor in their backend. Subtest verifies boundary cases. |
| `RULE-POLARITY-04` | Polarity probe MUST restore baseline on every exit path: success, read failure, write failure, context cancellation, panic. NVML restoration MUST include both fan control policy and speed value. Subtest injects faults at each step. |
| `RULE-POLARITY-05` | Polarity-aware write helper MUST refuse writes to channels where `Polarity ∈ {phantom, unknown}`. Subtest verifies error returns. |
| `RULE-POLARITY-06` | NVML probe MUST verify driver version ≥R515 via `nvmlSystemGetDriverVersion` before attempting write. Older driver → channel classified phantom with reason `"driver_too_old"`. Subtest covers via fake NVML returning <R515. |
| `RULE-POLARITY-07` | IPMI per-vendor backend MUST implement the `PolarityProbe` interface. Backends without write capability (Dell PE 14G locked firmware, HPE profile-only) MUST classify their channels phantom with the appropriate reason. No "deferred" classification. Subtest covers Supermicro (real probe), Dell (firmware refusal), HPE (405/501 response). |
| `RULE-POLARITY-08` | On daemon start, polarity state from KV MUST be applied to matching channels by `(backend, identity)` tuple. Mismatch refuses control mode start. Subtest covers match, miss-newchannel, orphan cases. |
| `RULE-POLARITY-09` | Reset-to-initial-setup MUST wipe `calibration.polarity` KV entries in addition to `wizard` and `probe` namespaces. Subtest verifies KV state post-reset. |
| `RULE-POLARITY-10` | Phantom channels MUST NOT be writable via the polarity-aware helper. Daemon control loop MUST treat phantom channels as monitor-only. Subtest verifies control loop excludes phantom from write set across all four backends. |

Each rule maps 1:1 to a Go subtest. `tools/rulelint` enforces the
binding.

---

## 7. RULE-PROBE-06 correction (chore commit, first in PR)

Spec-v0_5_1's `RULE-PROBE-06` was shipped as:

> All `ControllableChannel.Polarity` MUST be `"unknown"` in v0.5.1
> output.

This was an incomplete statement of intent. The correct invariant is:

> `ControllableChannel.Polarity` MUST be drawn from the closed set
> `{"unknown", "normal", "inverted", "phantom"}`. Probe layer
> (spec-v0_5_1) sets `"unknown"`. Polarity probe (spec-v0_5_2)
> resolves to one of the other three values. No code path may
> produce a value outside this set.

The fix lands as the **first commit** in this PR:

```
chore(probe): correct RULE-PROBE-06 to closed-set invariant
```

This commit:

- Edits `.claude/rules/RULE-PROBE-06.md` with the corrected statement.
- Edits the bound subtest to assert closed-set membership rather
  than equality to `"unknown"`.
- Adds a comment in the rule file noting the correction reason.

The chore commit ships before any v0.5.2 code so the rule is
correct on disk before any subtest needs to bind to the new
statement.

---

## 8. Failure modes enumerated

1. **hwmon sysfs write fails (EACCES, EBUSY).** Channel marked
   phantom with reason `"write_failed"`. Diagnostic surfaces.
   Continue with remaining channels.

2. **hwmon tach read fails after midpoint write.** Channel marked
   phantom with reason `"no_response"`. Restore baseline. Continue.

3. **NVML driver returns NVML_ERROR_NOT_SUPPORTED on
   SetFanControlPolicy.** Channel marked phantom with reason
   `"write_failed"`. Restore any partial state (baseline policy
   if it was successfully read first).

4. **NVML driver version <R515.** Per RULE-POLARITY-06, channel
   classified phantom with reason `"driver_too_old"`. No write
   attempted.

5. **IPMI vendor backend not in catalog (unknown BMC).** Channel
   classified phantom with reason `"write_failed"`. Diagnostic
   surfaces, asking user to contribute a profile.

6. **Dell PE 14G iDRAC9 ≥3.34.** Per RULE-POLARITY-07, vendor
   backend's `ProbePolarity` returns firmware-refusal sentinel.
   Channel phantom with reason `"firmware_locked"`. Permanent.

7. **HPE iLO5/6 Redfish 405 on per-fan write.** Per RULE-POLARITY-07,
   channel phantom with reason `"profile_only"`. Permanent.

8. **Daemon receives shutdown signal mid-probe.** Restore baseline on
   the in-flight channel via deferred handler. Persist all completed
   channels. Mark in-flight channel as not-yet-probed (no entry in
   KV). Next start re-probes.

9. **Channel polarity flips between probes (hardware swap).** Match
   by `(backend, identity)` stays valid; persisted polarity applies
   to new fan. If this misclassifies, user invokes reset. v0.5.2
   does not auto-detect polarity drift.

10. **Quiet server fan, midpoint produces <150 RPM delta.**
    Classified phantom. Doctor surfaces with hint to retry probe via
    reset. Future patches may extend probe to multiple PWM values.

11. **NVML fan with broken driver report (rare R515+ bug, e.g.
    consumer card with custom vendor BIOS).** Probe writes 50%,
    driver returns immediately without applying. Observed delta is 0
    → channel classified phantom with reason `"no_response"`.
    Acceptable; safer than misclassifying.

12. **Two hwmon channels share a single fan (parallel wiring).** Both
    probed sequentially. First probe affects RPM seen on second.
    Mitigation: 500ms restore delay between channels gives RPM time
    to settle.

13. **Inverted polarity on tach-less hwmon channel.** Cannot detect —
    channel classified phantom (no_tach). Acceptable: tach-less
    inverted channels are exotic and the safe default (no writes) is
    correct.

14. **Polarity state KV write fails (disk full).** Probe completes
    in-memory. Daemon refuses to start in control mode (per
    RULE-STATE-01 in spec-16). User must resolve storage.

15. **Concurrent probe attempts (race).** spec-16 PID file detection
    prevents multi-daemon. Single-daemon, single-probe is the only
    valid state. Subtest covers via fake KV with concurrent-write
    sentinel.

---

## 9. Validation criteria

### 9.1 Synthetic CI tests

Required, all must pass on every PR:

- **hwmon polarity classification:** synthetic fixtures with deltas
  at +620, -620, +149, -149, 0 → produce normal, inverted, phantom,
  phantom, phantom respectively.
- **NVML polarity classification:** fake NVML returning speed deltas
  +30, -30, +9, -9, 0 percentage points → produce normal, inverted,
  phantom, phantom, phantom.
- **NVML driver version gate:** fake NVML returning driver 510 →
  channel phantom with reason `"driver_too_old"`, no write
  attempted.
- **IPMI Supermicro:** fake Supermicro backend returns successful
  midpoint set + delta on SDR read → classified per delta.
- **IPMI Dell firmware refusal:** fake Dell backend returns
  iDRAC9 firmware-refusal sentinel → channel phantom with reason
  `"firmware_locked"`, no further write attempted.
- **IPMI HPE 405:** fake HPE Redfish backend returns 405 on per-fan
  write → channel phantom with reason `"profile_only"`.
- **Restore on every path:** inject failure at each step (write,
  read, sleep, restore-write, NVML policy restore) → verify baseline
  fully restored after each.
- **Polarity-aware write helper:** normal channel writes value
  unchanged; inverted channel writes `255 - value`; phantom/unknown
  channels return error. Verified across all four backends.
- **KV persistence:** probe runs, `calibration.polarity.channels`
  populated correctly with backend-specific identity field.
- **Daemon start match:** persisted polarity applied to matching
  channels; mismatch refuses control-mode start.
- **Reset path:** `calibration` namespace wiped alongside `wizard`
  and `probe`.
- **RULE-PROBE-06 closed-set:** subtest asserts every produced
  polarity value is in `{"unknown", "normal", "inverted", "phantom"}`.

### 9.2 Behavioural HIL

**Fleet members required, in priority order:**

1. **Proxmox VM 103 (Plex, RTX 3060, NVIDIA driver 570.211.01,
   accessible via `ssh plex@192.168.7.5`):** primary NVML HIL. Probe
   runs against the 3060. Verify polarity classifies as `normal`
   (expected for any well-behaved NVIDIA consumer card). Verify
   baseline restore by checking GPU fan speed reverts to
   driver-default policy after probe. Run during low-Plex-traffic
   window (probe writes briefly affect fan during any active
   transcode).
2. **Proxmox host (5800X, hwmon channels):** primary hwmon HIL.
   Verify polarity classifies correctly on motherboard PWM
   channels. Verify wizard reaches post-probe flow.
3. **MiniPC (Celeron, minimal hwmon, ssh phoenix@192.168.7.222):**
   smoke test. Verify probe completes without crash on minimal
   hardware. Whatever PWM exposes classifies correctly or as
   phantom.
4. **At least one laptop (when Phoenix's fleet has one online):** EC
   polarity edge case. ThinkPad or similar with `/proc/acpi/ibm/fan`
   exposed. Verify classification (likely `normal` for ThinkPad EC).
5. **9900K rig (when online):** supersedes Proxmox host as primary
   hwmon HIL. DIY mainboard with multiple PWM headers exercises
   broader hardware variety.

**Not required (no fleet member, but synthetic CI covers the path):**

- IPMI Supermicro: no fleet member with Supermicro BMC. Synthetic
  fixture only. v1.0 may surface as field issue if synthetic fixture
  is wrong; acceptable risk given absence of HIL.
- IPMI Dell PE 14G locked: no fleet member. Synthetic fixture only.
- IPMI HPE: no fleet member. Synthetic fixture only.

The IPMI HIL gap is documented in TESTING.md and surfaces as a known
v1.0 risk. Future fleet expansion (acquiring even one second-hand
Supermicro BMC board) closes it.

### 9.3 Time-bound metric

**Per-channel probe time:** 4.0 seconds ± 0.5s across all backends
(3s hold + 500ms baseline read + 500ms restore).

**Total probe time for system with N channels:** ≤ 4.5 × N seconds.
For typical desktop (4-6 channels): under 30 seconds. For server
with 12+ fans: up to a minute. Wizard UI displays estimated time on
entry.

---

## 10. PR sequencing

Single PR. Three commits in order:

1. `chore(probe): correct RULE-PROBE-06 to closed-set invariant`
   (per §7).
2. `feat(polarity): cross-backend polarity probe + phantom detection`
   (the bulk).
3. `feat(wizard): polarity probe screen + monitor-only demotion`
   (UI integration).

Splitting these into separate PRs introduces empty-shipping problems
identical to v0.5.1's reasoning. The chore commit is small enough to
ride along.

---

## 11. Estimated cost

CC implementation (Sonnet, single PR): **$13-23 estimate**.

Decomposition:

- RULE-PROBE-06 chore commit: $1.
- hwmon polarity probe + threshold logic + restore handling: $3-4.
- NVML polarity probe (consumes existing `internal/nvidia/`
  symbol loader, builds the probe procedure on top): $3-5.
- IPMI per-vendor `PolarityProbe` interface + Supermicro/Dell/HPE
  implementations: $3-5.
- Polarity-aware write helper: $1.
- KV persistence: $1.
- Wizard integration: $1-2.
- RULE-POLARITY-* bindings (10 rules, 1:1 subtests) + RULE-PROBE-06
  amendment subtest: $1-2.
- Synthetic test fixtures (~12 fixtures across hwmon, NVML, IPMI):
  $1-2.

Pad +50% if NVML write API integration runs into purego signature
bugs (the existing `internal/nvidia/` loader has the symbols but
v0.5.2 is the first patch to actually call them in writing).

Hard stop: $40.

---

## 12. References

- `spec-smart-mode.md` §5.2 — procedure of record.
- `specs/spec-v0_5_1-catalog-less-probe.md` — probe layer consumed.
  RULE-PROBE-06 corrected here.
- `specs/spec-16-persistent-state.md` — KV store consumed.
- `specs/spec-12-amendment-smart-mode-rework.md` §3.1 — wizard UI
  conventions.
- `Firmware-Locked_Fan_Control_on_Linux__Vendor_Capabilities_and_Limits_for_ventd.md`
  — Dell/HPE firmware behaviour informing IPMI permanent
  classifications.
- `internal/nvidia/nvidia.go` — NVML symbol loader consumed by §3.2.
- `internal/hal/gpu/nvml/probe.go` — existing NVML probe scaffold.

---

**End of spec.**
