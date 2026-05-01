# spec-v0_5_5 — Opportunistic active probing for Layer A gaps

**Status:** DESIGN. Drafted 2026-05-01.
**Ships as:** v0.5.5 (fifth smart-mode behaviour patch).
**Depends on:**
- v0.5.3 envelope C/D + idle gate (PR #685, shipped) — supplies
  `internal/idle/StartupGate`, `internal/envelope/holdLoop`,
  `internal/envelope/thresholds.LookupThresholds`,
  `internal/sysclass.Detect`.
- v0.5.4 passive observation log (shipped) — supplies
  `internal/observation/Reader.Stream` and the schema-versioned record
  stream that v0.5.5 reads from (gap detection) and writes to (probe
  events).
- v0.5.2 polarity disambiguation (shipped) — supplies
  `internal/polarity/WritePWM` gateway.

**Consumed by:**
- v0.5.7 Layer B coupling (consumes Layer A coverage data
  opportunistic probing fills in).
- v0.5.8 Layer C RLS (consumes converged Layer A curves seeded by
  passive + opportunistic data).
- v0.5.9 confidence-gated controller (drift detector retires the need
  for opportunistic re-probe of stable hardware).
- v0.5.10 doctor (consumes "last 20 opportunistic probes" + abort
  history from observation log).

**References:**
- `specs/spec-smart-mode.md` §6.4 Shape 2 (opportunistic active
  probing mechanics — the design of record), §6.1 (Layer A scope),
  §7.4 (manual mode disables opportunistic probing).
- `specs/spec-12-amendment-smart-mode-rework.md` §3.5 (Settings
  toggle UX), §7 (`RULE-UI-SMART-07`).
- `specs/spec-v0_5_3-envelope-cd.md` (idle gate, holdLoop,
  thresholds — primitives this patch reuses).
- `specs/spec-v0_5_4-passive-observation.md` and
  `docs/research/r-bundle/ventd-passive-observation-log-schema.md`
  (record schema and reader API; this patch bumps schema 1 → 2).
- `docs/research/r-bundle/R5-defining-idle-multi-signal-predicate.md`
  (idle predicate research — `OpportunisticGate` reuses R5 hard
  preconditions verbatim, adds two signals, replaces durability).
- `docs/research/r-bundle/envelope-c-abort-thresholds.md` (R4 abort
  thresholds — reused unchanged).

---

## 1. Why this patch exists

Passive observation (v0.5.4) records every PWM the controller actually
writes. The controller's natural operating range typically spans only
the middle of the PWM space — a CPU fan in normal use may visit PWM
80–180 and never touch PWM 30 or PWM 240. Layer A's most valuable
learning lives in the unvisited tails: stall-PWM and min-spin sit at
the low end; saturation behaviour sits at the high end. Without a
mechanism to visit those PWM bins deliberately, Layer A converges
slowly or never on the most diagnostic regions.

v0.5.5 fills the gaps. During genuinely-idle windows ventd writes a
specific gap PWM value to one channel for ~30 seconds, observes the
response, logs it to the v0.5.4 observation stream with a probe-event
flag, and returns to the controller-managed value. Over a few weeks
of normal use every channel has full Layer A coverage without the
user noticing.

The patch is purposefully narrow: it consumes the v0.5.4 reader API
and the v0.5.3 abort/hold primitives. It does not modify the schema
beyond adding one event-flag bit, does not introduce a new storage
backend, and does not surface analysis on the doctor page (that's
v0.5.10's job per spec-smart-mode §11).

## 1.1 Ground-up principle

Reuse beats rebuild. Five existing packages contribute primitives;
v0.5.5 adds the orchestration and gap-detection layer between them.
Specifically:

- `internal/idle/StartupGate` predicate logic is reused unchanged.
  The new `OpportunisticGate` differs only in (a) durability window,
  (b) two extra signal sources (`/proc/interrupts` deltas + active
  SSH session presence), (c) refusal reasons.
- `internal/envelope/thresholds.LookupThresholds(class)` returns the
  same per-class abort thresholds the v0.5.3 envelope probe uses.
  Opportunistic probes are short (30 s, single PWM) but the dT/dt
  and T_abs ceilings remain the floor of safety.
- `internal/envelope/holdLoop` (private; promoted to package-internal
  helper for v0.5.5) is the write-and-observe primitive.
- `internal/polarity.WritePWM` is the only path PWM writes take.
- `internal/observation.Reader.Stream` walks active + rotated log
  files to compute per-channel PWM bins visited in the cool-down
  window.

---

## 2. Scope

### 2.1 In scope

**Idle gate (`internal/idle/`):**

- New entry point `OpportunisticGate(ctx, GateConfig) (bool, Reason,
  *Snapshot)` wrapping the same predicate stack as `StartupGate` with
  three differences:
  1. Durability window: **600 s (10 min)** vs StartupGate's 300 s.
     Justification: opportunistic probing is opt-in for autonomous
     fans; user expectation is "system has been quiet for a while,"
     not "system has just satisfied PSI's avg300 cycle." 10 min
     survives @hourly cron noise (R5 §5) without admitting brief
     load lulls between user activity. Independent of M (thermal
     stability) which is checked separately.
  2. Two additional signals (both must report no recent activity):
     - **Input-IRQ delta:** parse `/proc/interrupts`, classify each
       IRQ via `/sys/kernel/irq/<n>/actions` matching `i8042`,
       `xhci_hcd`/`ehci_hcd`/`uhci_hcd` AND `hid`/`usbhid`/`kbd`/
       `mouse`. Any non-zero delta on any input IRQ in the last 60 s
       window resets the durability counter.
     - **Active SSH session:** `loginctl list-sessions
       --output=short` parsed; any session with `Remote=yes AND
       Active=yes AND IdleSinceHint <= 60 s` resets durability.
       Long-idle SSH (`tmux attach` left running) does NOT reset.
  3. Hard preconditions inherited unchanged from `StartupGate`:
     battery, container, scrub-active, blocked process, boot warmup,
     post-resume warmup. R5 §7.1 verbatim.

- New `Reason` enum members: `ReasonRecentInputIRQ`,
  `ReasonActiveSSHSession`, `ReasonOpportunisticDisabled`,
  `ReasonOpportunisticBootWindow` (the 24-hour install delay).

**Gap detector (`internal/probe/opportunistic/`):**

- `Detector` reads observation log via `Reader.Stream(since=now-7d,
  fn)` once per scheduling tick. Builds per-channel set of "PWM bins
  visited in last 7 days" from records where `event_flags &
  ENVELOPE_C_ABORT == 0` (i.e., natural controller writes and
  successful opportunistic probes count; aborted opportunistic probes
  do NOT count, so the bin remains "unvisited" for retry purposes).
- Probe grid is the union of:
  - Low half (PWM 0–96): every 8 raw units. 13 bins.
  - High half (PWM 97–255): every 16 raw units. 10 bins.
  - Stall-PWM and min-spin from the channel's `last_calibration` KV
    record, when available.
- Gap = grid bin with no record in the last 7 days. Detector returns
  `map[ChannelID][]uint8` of channel-to-sorted-PWM-gaps, lowest PWM
  first (low end is highest-value learning).

**Scheduler (`internal/probe/opportunistic/`):**

- Single long-running goroutine launched from `cmd/ventd/main.go`
  alongside the controller goroutines.
- Tick cadence: 60 s. Each tick:
  1. Refuse if `Config.NeverActivelyProbeAfterInstall == true`.
  2. Refuse if first-install timestamp (read from
     `/var/lib/ventd/.first-install-ts`, written on first daemon
     start if missing) is < 24 h ago. Reason:
     `ReasonOpportunisticBootWindow`.
  3. Call `OpportunisticGate(ctx, cfg)`. If false, sleep next tick.
  4. Call `Detector.Gaps(now)`. If empty, sleep next tick.
  5. Pick lowest-PWM gap on the channel with the largest gap set
     (tie-break: longest time since last opportunistic probe on this
     channel, persisted in spec-16 KV at
     `opportunistic.last_probe.<channel_id>`).
  6. Refuse if channel is in manual mode (per
     `Config.Controls[].Mode == "manual"`).
  7. Fire single probe via `Prober.FireOne(ctx, channel, gapPWM,
     deps)`.
  8. Persist `opportunistic.last_probe.<channel_id> = now` in
     spec-16 KV regardless of probe outcome.

- Status struct exposes: `Running bool`, `ChannelID uint16`,
  `GapPWM uint8`, `StartedAt time.Time`, `LastReason Reason`.

**Prober (`internal/probe/opportunistic/`):**

- `FireOne(ctx, ch, gapPWM, deps)`:
  1. Read controller's current PWM via `readPWM(ch.PWMPath)`.
     This is the restoration baseline.
  2. Call `polarity.WritePWM(ch, gapPWM, sysfsWriteFn)`.
  3. Run `holdLoop` for 30 s (5 % jitter). On dT/dt or T_abs trip
     per `LookupThresholds(class)`: abort, log with
     `EventFlag_OPPORTUNISTIC_PROBE | EventFlag_ENVELOPE_C_ABORT`,
     restore baseline.
  4. On normal completion: emit observation record with
     `EventFlag_OPPORTUNISTIC_PROBE` set, restore baseline via
     `polarity.WritePWM(ch, baseline, sysfsWriteFn)`.
- `defer`-based restore on every exit path (success, abort, ctx
  cancel, panic). Same discipline as `internal/envelope/envelope.go`.

**Schema bump (`internal/observation/`):**

- `SchemaVersion` const: 1 → 2.
- New `EventFlag_OPPORTUNISTIC_PROBE = 1 << 13` (bit 13 was reserved
  per spec-v0_5_4 §3 RULE-OBS-SCHEMA-05).
- Reader handles both v1 and v2 records; writer always emits v2.
- Migration: spec-16 §7.3 forward-migration entry registers v1 -> v2
  as additive (no field changes, just new event-flag bit; v1 readers
  ignore unknown bits in `event_flags` per existing semantics).

**Settings toggle:**

- New `Config.NeverActivelyProbeAfterInstall bool` with yaml tag
  `never_actively_probe_after_install` and JSON tag
  `never_actively_probe_after_install`.
- Default: `false` (probing enabled).
- Persisted via existing PUT `/api/v1/config` flow.

**Web surface:**

- New endpoint `GET /api/v1/probe/opportunistic/status` returning
  the scheduler's `Status` struct as JSON. Mirrors
  `handleCalibrateStatus` pattern at `internal/web/server.go:1112`.
- Dashboard pill (PR-B): polls endpoint at 5 s cadence, renders
  pill matching v0.5.3 envelope-probe style when `Running == true`.
- Settings checkbox (PR-B): rendered in a "Smart mode" section.

**Tests (synthetic, all CI):**

- Idle: `TestOpportunisticGate_DurabilityIs600s`,
  `TestOpportunisticGate_RefusesOnInputIRQDelta`,
  `TestOpportunisticGate_RefusesOnActiveSSH`,
  `TestOpportunisticGate_AcceptsLongIdleSSH`,
  `TestOpportunisticGate_HardPreconditionsInherited`.
- Detector: `TestDetector_BuildsGapSetFromLog`,
  `TestDetector_ExcludesBinsWithin7Days`,
  `TestDetector_LowHighGridSpacing`,
  `TestDetector_AnchorsStallAndMinSpin`,
  `TestDetector_AbortedOpportunisticDoesNotCount`.
- Scheduler: `TestScheduler_FirstProbeDelayedBy24h`,
  `TestScheduler_OneChannelAtATime`,
  `TestScheduler_HonoursToggleOff`,
  `TestScheduler_RefusesManualModeChannels`,
  `TestScheduler_PicksLowestPWMOnLargestGap`.
- Prober: `TestProber_FullCycle_RestoresController`,
  `TestProber_AbortPath_RestoresController`,
  `TestProber_CtxCancel_RestoresController`,
  `TestProber_EmitsRecordWithProbeFlag`.
- Schema: `TestSchemaV2_BackwardCompatibleRead`,
  `TestSchemaV2_WriterEmitsV2`.

### 2.2 Out of scope

- **Adaptive probe scheduling.** First version is fixed cadence + 7
  day cool-down. Per-channel adaptive intervals based on Layer A
  confidence are v0.5.9 territory.
- **Multi-channel parallel probes.** Strictly serial system-wide in
  v0.5.5. Cross-channel parallelism is unsafe until Layer B coupling
  map (v0.5.7) tells us which channels' thermal sources don't
  overlap.
- **Probe duration sweeps.** 30 s is fixed. Adaptive duration based
  on sensor latency class is deferred.
- **Doctor surface.** "Last 20 opportunistic probes," "next
  scheduled probe," "abort statistics" are all v0.5.10 work. v0.5.5
  emits the records; v0.5.10 reads them.
- **Telemetry to upstream.** Diag bundle still excludes the
  observation log by default per spec-v0_5_4 §6.2. Opportunistic
  probe events live in the same log; same exclusion applies.
- **CLI surface.** No `ventd opportunistic-probe` command. Manual
  invocation is via the existing `ventdctl calibrate --force` path,
  out of scope for v0.5.5.
- **`opportunistic_max_age_days` tunable.** Default is "re-probe
  only on drift detection" (no upper bound). User-facing tunable
  deferred until field telemetry says it matters.
- **Probe-event analytics.** `Detector` returns the gap set; it does
  not report coverage statistics or per-channel learning progress.

---

## 3. Invariant bindings

`.claude/rules/opportunistic.md` binds 1:1 to subtests in
`internal/probe/opportunistic/` and `internal/idle/`. Enforced by
`tools/rulelint`.

| Rule | Binding |
|---|---|
| `RULE-OPP-PROBE-01` | Probe MUST fire only when `OpportunisticGate` returns true. |
| `RULE-OPP-PROBE-02` | Probe duration MUST be 30 ± 5 seconds; no PWM sweep within a single probe. |
| `RULE-OPP-PROBE-03` | At most one opportunistic probe in flight system-wide. Concurrent fires MUST be rejected. |
| `RULE-OPP-PROBE-04` | All PWM writes MUST route through `polarity.WritePWM`; direct sysfs writes are forbidden. |
| `RULE-OPP-PROBE-05` | Probe MUST abort on `envelope.LookupThresholds(class)` thresholds being exceeded; restoration MUST complete before the function returns. |
| `RULE-OPP-PROBE-06` | A PWM bin with a non-aborted observation record (any `event_flags` bit) within 7 days MUST NOT be re-probed. |
| `RULE-OPP-PROBE-07` | First probe MUST NOT fire within 24 hours of `/var/lib/ventd/.first-install-ts` mtime. |
| `RULE-OPP-PROBE-08` | Probe MUST refuse when `Config.NeverActivelyProbeAfterInstall == true`. |
| `RULE-OPP-PROBE-09` | Probe MUST refuse on channels where `Config.Controls[].Mode == "manual"`. |
| `RULE-OPP-PROBE-10` | On every exit path (normal completion, abort, ctx cancel, panic) the probe MUST restore the controller-managed PWM value via `polarity.WritePWM`. |
| `RULE-OPP-PROBE-11` | Each probe emits exactly one observation record with `EventFlag_OPPORTUNISTIC_PROBE` bit set. Aborts emit with `EventFlag_OPPORTUNISTIC_PROBE \| EventFlag_ENVELOPE_C_ABORT`. |
| `RULE-OPP-PROBE-12` | Probe grid MUST be 8 raw PWM units between 0 and 96 inclusive, 16 raw PWM units between 97 and 255 inclusive. Stall-PWM and min-spin MUST be probed when in a gap, regardless of grid spacing. |
| `RULE-OPP-IDLE-01` | `OpportunisticGate` durability MUST be 600 seconds. |
| `RULE-OPP-IDLE-02` | `OpportunisticGate` MUST refuse when any input IRQ has non-zero delta in the last 60 seconds. |
| `RULE-OPP-IDLE-03` | `OpportunisticGate` MUST refuse when any `Remote=yes Active=yes IdleSinceHint<=60s` session is present. |
| `RULE-OPP-IDLE-04` | `OpportunisticGate` MUST inherit all hard preconditions from `StartupGate` unchanged (battery, container, storage maintenance, blocked process, boot warmup, post-resume warmup). |
| `RULE-OPP-OBS-01` | `SchemaVersion` constant MUST be 2 once this patch ships. Reader MUST accept v1 records as forward-compatible (no field changes). |
| `RULE-OPP-OBS-02` | `EventFlag_OPPORTUNISTIC_PROBE = 1 << 13`. The bit MUST NOT collide with any v0.5.4 reserved bit. |

---

## 4. Subtest mapping

Tests live in `internal/probe/opportunistic/`,
`internal/idle/opportunistic_test.go`, and
`internal/observation/record_test.go`.

| Rule | Subtest |
|---|---|
| RULE-OPP-PROBE-01 | `TestScheduler_FiresOnlyAfterGatePasses` |
| RULE-OPP-PROBE-02 | `TestProber_DurationWithinTolerance` |
| RULE-OPP-PROBE-03 | `TestScheduler_OneChannelAtATime` |
| RULE-OPP-PROBE-04 | `TestProber_RoutesViaPolarityWrite` |
| RULE-OPP-PROBE-05 | `TestProber_AbortPath_RestoresController` |
| RULE-OPP-PROBE-06 | `TestDetector_ExcludesBinsWithin7Days` |
| RULE-OPP-PROBE-07 | `TestScheduler_FirstProbeDelayedBy24h` |
| RULE-OPP-PROBE-08 | `TestScheduler_HonoursToggleOff` |
| RULE-OPP-PROBE-09 | `TestScheduler_RefusesManualModeChannels` |
| RULE-OPP-PROBE-10 | `TestProber_FullCycle_RestoresController`, `TestProber_CtxCancel_RestoresController` |
| RULE-OPP-PROBE-11 | `TestProber_EmitsRecordWithProbeFlag` |
| RULE-OPP-PROBE-12 | `TestDetector_LowHighGridSpacing`, `TestDetector_AnchorsStallAndMinSpin` |
| RULE-OPP-IDLE-01 | `TestOpportunisticGate_DurabilityIs600s` |
| RULE-OPP-IDLE-02 | `TestOpportunisticGate_RefusesOnInputIRQDelta` |
| RULE-OPP-IDLE-03 | `TestOpportunisticGate_RefusesOnActiveSSH`, `TestOpportunisticGate_AcceptsLongIdleSSH` |
| RULE-OPP-IDLE-04 | `TestOpportunisticGate_HardPreconditionsInherited` |
| RULE-OPP-OBS-01 | `TestSchemaV2_BackwardCompatibleRead`, `TestSchemaV2_WriterEmitsV2` |
| RULE-OPP-OBS-02 | `TestEventFlag_ProbeBitDoesNotCollide` |

---

## 5. Success criteria

### 5.1 Synthetic CI tests

All 22 subtests pass on every PR. `tools/rulelint` reports zero
unbound rules, zero unused subtests.

### 5.2 Behavioural HIL

**Primary fleet member: Proxmox host (192.168.7.10, 5800X + RTX
3060).** 72-hour soak with normal homelab workload:

- ≥3 successful opportunistic probes per controllable channel logged
  with `EventFlag_OPPORTUNISTIC_PROBE` bit set.
- Every probe followed within 35 s by a controller-managed PWM write
  to the same channel (verified by tail of observation log).
- Zero `Got notification message from PID X` lines in `journalctl
  -u ventd` (regression check after PR #723).
- Average opportunistic probe duration in [25, 35] seconds.
- `htop` shows scheduler goroutine consumes <0.1 % CPU averaged over
  72 h.

**Negative fleet members:**

- **MiniPC (192.168.7.222), when online:** monitor-only mode (no
  controllable fans). Daemon log MUST contain
  `opportunistic.skipped_no_controllable_channels` once per scheduler
  tick. No probes ever fire.
- **Steam Deck:** `firmware_owns_fans: true` capability detected.
  Settings page renders the toggle. Daemon never fires a probe.

### 5.3 Time-bound metric

**Not applicable.** Opportunistic probing is a passive learning
mechanism; it neither speeds nor slows calibration or controller
convergence in the 72-hour soak. Long-term (months) Layer A
convergence is a v0.5.9 confidence-gated controller measurement, not
a v0.5.5 success criterion.

---

## 6. Privacy contract

Inherited from spec-v0_5_4 §6 unchanged. Opportunistic probe records
go through the same writer with the same exclusion list. No new
sensitive fields introduced. The `EventFlag_OPPORTUNISTIC_PROBE` bit
is opaque metadata, not user data.

`/proc/interrupts` deltas and `loginctl` session presence are
consumed by `OpportunisticGate` and reduced to boolean refusal
reasons. They are NOT persisted to the observation log or the
diagnostic bundle.

---

## 7. Failure modes enumerated

1. **Idle gate never satisfies (busy NAS, scrub running daily).**
   Scheduler keeps polling at 60 s cadence. No probes fire. No log
   churn (refusal reason logged at debug level only). Layer A
   coverage gaps persist; v0.5.9 drift detector eventually requests
   a focused recalibration.

2. **Probe aborts on thermal slope.** Restoration completes,
   observation record emitted with both probe and abort bits.
   Cool-down logic still applies — the bin counts as "visited" for
   7 days even though no useful data was learned. Acceptable: the
   bin is genuinely unsafe at the current ambient/load profile;
   waiting 7 days for retry is right.

3. **Daemon restart mid-probe.** `defer`-based restore did NOT run
   (panic / SIGKILL). Next daemon start: controller's first tick
   writes its own PWM to the channel, overriding whatever
   opportunistic value was left. Worst-case window: ~2 s of
   "opportunistic value persists into post-restart." Acceptable.

4. **Schema v1 file present after upgrade.** Reader handles both v1
   and v2 transparently. Records written before v0.5.5 lack
   `EventFlag_OPPORTUNISTIC_PROBE` (correct — no opportunistic
   probes happened) and the gap detector treats every visited bin as
   eligible for the 7-day cool-down (correct).

5. **Channel removed mid-probe (hot-unplug).** `holdLoop` returns
   `ErrChannelGone`; restore step fails silently (channel is gone,
   nothing to restore to). Observation record is still emitted with
   abort flag and reason `channel_removed`. Scheduler removes the
   channel from its working set on next detector pass.

6. **Polarity unknown / phantom.** `polarity.WritePWM` refuses;
   `FireOne` returns immediately without firing. Detector excludes
   non-controllable channels at construction time per
   `polarity.IsControllable`.

7. **Spec-16 KV write failure on `last_probe.<channel_id>`
   persistence.** Treated as advisory: scheduler logs a warning and
   continues. Loss of one timestamp means the channel may be re-
   probed sooner than 7 days for one cycle. Self-corrects on next
   successful KV write.

8. **`/var/lib/ventd/.first-install-ts` missing or unreadable on
   daemon start.** Created with current mtime; this restarts the
   24 h delay clock once. Acceptable: a fresh install correctly
   waits 24 h, an upgrade with a wiped state file gets one extra
   24 h delay (one-time cost).

9. **Toggle flipped to ON mid-probe.** Probe in progress completes
   and restores normally. Next scheduler tick refuses with
   `ReasonOpportunisticDisabled`. No abort needed mid-probe — the
   toggle is "enroll/un-enroll for future probes," not a kill switch
   for the current one.

10. **`/proc/interrupts` parse failure (kernel format change).**
    `OpportunisticGate` treats unreadable file as "input activity
    unknown" → refuse. Surfaces as `ReasonRecentInputIRQ` with
    detail `parse_error`. Conservative; preserves the no-probe-
    during-user-activity invariant.

---

## 8. PR sequencing

### 8.1 PR-A (logic, hermetically testable)

Single PR. Files:

```
internal/idle/opportunistic.go
internal/idle/opportunistic_test.go
internal/idle/user_input.go
internal/idle/user_input_test.go
internal/probe/opportunistic/detector.go
internal/probe/opportunistic/detector_test.go
internal/probe/opportunistic/scheduler.go
internal/probe/opportunistic/scheduler_test.go
internal/probe/opportunistic/prober.go
internal/probe/opportunistic/prober_test.go
internal/probe/opportunistic/install_marker.go
internal/probe/opportunistic/install_marker_test.go
internal/observation/schema.go              (modify: bump version + flag)
internal/observation/record.go              (modify: v1/v2 read compat)
internal/observation/record_test.go         (modify: add v2 tests)
internal/config/config.go                   (modify: add toggle field)
internal/config/config_test.go              (modify: round-trip toggle)
cmd/ventd/main.go                           (modify: launch scheduler)
.claude/rules/opportunistic.md
specs/spec-v0_5_5-opportunistic-probing.md  (this file)
```

Total LOC estimate: ~900 LOC including tests.

### 8.2 PR-B (UI surface, lands after PR-A merges)

Files:

```
web/settings.html         (modify: add Smart mode section + toggle)
web/settings.js           (modify: GET/PUT toggle)
web/settings.css          (modify: section styling)
web/dashboard.html        (modify: add probe pill div)
web/dashboard.js          (modify: poll status endpoint, render pill)
web/dashboard.css         (modify: pill styling)
internal/web/server.go    (modify: add handleOpportunisticStatus)
.claude/rules/opportunistic.md (extend with RULE-OPP-UI-*)
```

Total LOC estimate: ~250 LOC.

PR-B is HIL-only verification (visual critique); no Playwright tests
land with PR-B per current ventd UI test policy.

---

## 9. Estimated cost

- Spec drafting (chat): $0 (Max plan; this document).
- PR-A CC implementation (Sonnet): **$10–18**.
- PR-B CC implementation (Sonnet): **$5–8**.
- Total: **$15–26**, within `spec-smart-mode.md` §13 projection
  ($15–25).

---

## 10. References

- `specs/spec-smart-mode.md` §6.4, §6.1, §7.4, §11, §13.
- `specs/spec-12-amendment-smart-mode-rework.md` §3.5, §7
  (`RULE-UI-SMART-07`).
- `specs/spec-v0_5_3-envelope-cd.md`.
- `specs/spec-v0_5_4-passive-observation.md`.
- `docs/research/r-bundle/R5-defining-idle-multi-signal-predicate.md`.
- `docs/research/r-bundle/envelope-c-abort-thresholds.md`.
- `docs/research/r-bundle/ventd-passive-observation-log-schema.md`.
