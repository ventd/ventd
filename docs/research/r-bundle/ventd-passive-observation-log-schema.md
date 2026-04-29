# Passive Observation Log Schema (v0.5.4)

**Target patch:** `spec-v0_5_4-passive-observation.md` (drafted in a future chat).
**Status of inputs:** R-bundle 15/15 complete. Spec-16 (v0.5.0.1) is the storage substrate. R11 sets sample rate. R7 sets privacy boundary. R9/R10/R12/R13 are the consumers.
**Why this exists separately from the patch spec:** the schema is the contract every smart-mode patch from v0.5.4 onward depends on. Writing it once, locking it before v0.5.4 ships, prevents the v0.5.7/v0.5.8/v0.5.10 patches each having to re-litigate field-level decisions in their own CC sessions.

---

## 1. What the log is, what it isn't

The passive observation log is the **raw signal capture** of every controller tick: what ventd wrote, what the system showed, what classifier state was active, and what events fired. It is the training corpus for Layer A/B/C estimators when they cold-start, the historical evidence behind two of R13 doctor's recovery items, and the audit trail that demonstrates smart-mode is doing what it claims.

It is **not** authoritative state. Layer A's converged PWM→RPM curves, Layer B's coupling matrix β, Layer C's RLS θ̂ and P matrices, R7's signature library, and R12's confidence-formula inputs all live in spec-16 KV/Blob with their own persistence and update cadence. The log is the corpus those models were *built from*; once a model is persisted, the log entries that produced it are no longer load-bearing for steady-state operation. The log matters at three points in the system's life cycle:

1. **Cold-start after state loss.** Spec-16 Blob corruption, manual `ventd state reset`, or first install after a v0.5.0 → v0.5.4 upgrade where prior calibration data is structurally incompatible. The log is the only path back to a non-empty model.
2. **R13 doctor recovery items.** RECOVER-007 ("Layer C never converged on signature") needs ≥6 days of observation history to fire. "Recent envelope aborts (24h)" and "Recent Layer A activations (24h)" need 24h windows. These are the only doctor surfaces that read the log.
3. **Offline forensic analysis** (developer / advanced user). When something looks wrong in the live system, the log is the breadcrumb trail. Diag bundles capture a separate, smaller trace ring (cc-prompt-spec03-pr2c §15.5); the observation log is the longer-horizon companion.

The log explicitly does NOT serve:

- **Live UI state.** Doctor's live metrics surface (R13 §2) reads in-memory snapshots, never disk.
- **Drift detection.** Per R12 §Q8 and R15 Drift-5, drift detector consumes R12's live residual stream maintained in memory, not the log.
- **Controller hot path.** Controller reads in-memory state for its 0.5 Hz / 60 s tick. The log is append-only side effect, never queried by the controller itself.
- **Diag bundle contents.** Diag bundle has its own trace ring with its own retention and redaction. Observation log is excluded from default bundles per privacy threat model §2.6.

The log's job is therefore narrow: be the **deterministic, replayable, privacy-safe, retention-bounded record stream** that future model rebuilds can consume.

---

## 2. Sample shape — record schema

One record per **controller tick on a single channel**. Multi-channel systems produce multiple records per wall-clock tick, one per channel. Records are msgpack-encoded payloads framed by spec-16's append-only log primitive (§6.1: 4-byte length + payload + 4-byte CRC32).

### 2.1 Field-by-field

```
Record {
  // Identity
  ts                    int64       // Unix microseconds
  channel_id            uint16      // stable, matches spec-16 KV channel metadata key

  // Actuation — what the controller did this tick
  pwm_written           uint8       // 0..255, value written to pwmN this tick
  pwm_enable            uint8       // 1=manual, 2=BIOS-auto, 5=SmartFan, etc.
  controller_state      uint8       // enum, see §2.2

  // Observation — what the system showed
  rpm                   int32       // observed fanN_input; -1 if tach-less
  tach_tier             uint8       // R8 tier: 0=tach, 1=peer-coupled, 2=BMC-IPMI,
                                    // 3=EC-stepped, 4=thermal-inversion,
                                    // 5=RAPL-proxy, 6=AIO-readonly
  sensor_readings       map[uint16]int16  // sensor_id -> milli-degrees C
                                          // (1 mC resolution, signed; -32768 = read-failed)
  polarity              uint8       // 0=normal, 1=inverted, 2=indeterminate

  // Signature (R7 input for Layer C)
  signature_label       string      // hex-joined top-K=4 hashes, max 80 chars; "" if none
  signature_promoted    bool        // true on the tick R7 K-stable promotion completed

  // R12 confidence reconstruction inputs
  r12_residual          float32     // one-step prediction error e_k (Layer C innovation)

  // Event annotations — bitmask, cardinality matters
  event_flags           uint32      // see §2.3
}
```

**Total typical size:** 100–150 bytes msgpack-encoded with 4 sensors per channel and a 40-char active signature label. Empty signature labels and tach-less channels (`rpm=-1`) reduce this. Worst-case (8 sensors, 80-char label) approaches 200 bytes.

### 2.2 `controller_state` enum

```
0 = COLD_START          // hard-pinned w_pred=0, no Layer C contribution
1 = WARMING             // model converging, Layer A primary
2 = CONVERGED           // model stable, predictive blending active per R12
3 = DRIFTING            // R12 drift response active, half-life decay engaged
4 = ABORTED             // envelope abort or stall, reactive only this channel
5 = MANUAL_OVERRIDE     // user has pinned this channel; no learning
6 = MONITOR_ONLY        // R3 hardware-refused or Tier-2 BLOCK; read-only
```

The state is a property of the channel, not the system. A single tick can have channels in different states.

### 2.3 `event_flags` bitmask

Bits set by the controller when a notable event fires *during this tick*. Most ticks have `event_flags == 0`.

```
bit 0  = LAYER_A_HARD_CAP            // Layer A safety envelope clamped output
bit 1  = ENVELOPE_C_ABORT            // R4 abort fired
bit 2  = ENVELOPE_D_FALLBACK         // dropped from Envelope C to D mid-calibration
bit 3  = DRIFT_TRIPPED               // R12 drift detector trip this tick
bit 4  = SATURATION_DETECTED         // R11 dual-condition saturation gate fired
bit 5  = STALL_WATCHDOG_FIRED        // R8 stall watchdog escalation
bit 6  = IDLE_GATE_REFUSED           // R5 refused a calibration window opportunity
bit 7  = R12_GLOBAL_GATE_OFF         // global w_pred_system gate flipped this tick
bit 8  = LAYER_C_SHARD_ACTIVATED     // R10 overlay shard created for (channel, sig)
bit 9  = LAYER_C_SHARD_EVICTED       // R10 overlay shard evicted
bit 10 = R9_IDENT_CLASS_CHANGED      // R9 identifiability class transitioned
bit 11 = SIGNATURE_PROMOTED          // mirrors signature_promoted bool, kept for fast filtering
bit 12 = SIGNATURE_RETIRED           // R7 retired a signature this tick
bits 13-31 reserved
```

The doctor's "envelope aborts in 24h" and "Layer A activations in 24h" live metrics resolve to `event_flags & bitN != 0` counts over the relevant window. The bitmask form means a single forward iteration over the log produces all per-flag counts simultaneously, no decoding-by-flag overhead.

### 2.4 Header (per log file, not per record)

A spec-16 log file consists of records prefixed by a single header record:

```
Header {
  schema_version        uint16      // starts at 1
  dmi_fingerprint       [16]byte    // truncated SHA-256 of board fingerprint per spec-03
  ventd_version         string      // "0.5.4" etc. — for forensic correlation
  rotation_ts           int64       // Unix seconds the file was opened
  channel_class_map     map[uint16]uint8  // channel_id -> R11 class (0=fast, 1=slow)
}
```

The header is decoded once per file and cached. Consumers use `channel_class_map` to interpret the per-channel record cadence — the log itself does not record cadence per tick.

### 2.5 What is deliberately absent

- **No process names, comm strings, or raw signature hashes.** R7's privacy contract requires signatures to be hex-encoded labels of SipHash-2-4 output under a per-install salt. The salt at `/var/lib/ventd/.signature_salt` is `0600 ventd:ventd` and is excluded from diag bundles. Logging anything that could rebuild the SipHash inputs (PIDs, parent-comms, exe paths, cmdlines) would defeat the salt and is forbidden.
- **No PWM-effective-value or readback.** The writer logs what it *wrote*; readback noise is a Layer A modeling concern, not a logging concern.
- **No `tr(P)`, RLS state, β, or w_pred.** These are derived from history or live in spec-16 Blob as authoritative state. Logging them duplicates state and forces consumers to choose between log-derived and persisted-state versions of the same number — class of bugs we don't want.
- **No timer state** (LPF window, Lipschitz cap, idle-since timestamps). Derived from history, not observation.
- **No sensor names** in records — only sensor_ids resolved against channel metadata in spec-16 KV. Saves bytes and makes redaction trivial (the names live in metadata, not the high-volume log).
- **No raw-signature-hash bitmap** for R12 conf_B's `workload_variety` term. R12 §conf_B persists distinct-signature-visited bitmaps in spec-16 KV directly; the log records signature *labels* (the hex-encoded top-K join) and consumers reconstruct variety from the stream of labels seen.
- **No calibration state.** First-run wizard checkpoints (R14) live in spec-16 KV under `calibration:`, not in the observation log. Calibration is *what ventd is doing to the hardware*; the observation log is *what happened next*. Mixing them complicates retention semantics.

---

## 3. Sampling rate

**Locked: per-channel, one record per controller tick on this channel, where the tick rate is a property of the channel's R11 class.**

Two classes from R11 §1:

- **`class=fast`** — CPU/GPU/Super-IO sensors (coretemp, k10temp, nct67xx, it87, asus-ec). Tick rate **0.5 Hz** (1 record per 2 s).
- **`class=slow`** — HDD/drivetemp, BMC/IPMI sensors. Tick rate **1/60 Hz** (1 record per 60 s). BMC SDR refresh is hardware-capped at ≤ 1 Hz per R8 §Tier-2; logging at 0.5 Hz is wasted resolution. drivetemp's SCT cadence is ~1 min per R11 §4; same reasoning.

The class is determined at calibration time per R11's latency-vs-τ admissibility rule, recorded once in spec-16 KV channel metadata, and surfaced to the log writer through the per-file header's `channel_class_map`. The log writer does not re-derive class per tick.

**No oversampling, no adaptive sampling.** Rationale:

- Sensor dynamics are slower than the fastest tick rate. Oversampling captures noise, not signal.
- Adaptive sampling adds a control loop *inside the observation layer*, which breaks reproducibility — replay isn't deterministic if sampling decisions depend on runtime state. This matters when Layer A/B/C are seeded from log replay during cold-start re-warming.
- Storage budget at fixed rate is bounded and predictable. Adaptive sampling saves storage we don't need to save.
- "One record per controller tick on this channel" is a clean invariant for tests: synthetic N-tick input produces exactly N records per channel.

**Storage budget at locked rates:**

- Fast channel: 0.5 Hz × ~125 bytes × 86400 s = 5.4 MB/day
- Slow channel: 1/60 Hz × ~125 bytes × 86400 s = 0.18 MB/day
- 8-channel desktop (all fast): 43 MB/day, ~300 MB/week
- 8-bay NAS (mixed: 2 fast CPU + 8 slow drives): 12 MB/day, ~85 MB/week
- After gzip-on-rotation (~3:1 typical for mixed integer/string data): ~100 MB/week desktop, ~30 MB/week NAS

These figures fit comfortably within the spec-16 §6.2 default storage envelope and the homelab admin's tolerance for daemon disk footprint.

---

## 4. Storage — spec-16 framing

### 4.1 Log name and path

Single log named `observations` under spec-16's log namespace. Resolved path:

- **System mode (root install):** `/var/lib/ventd/logs/observations.log` (active), `/var/lib/ventd/logs/observations.log.YYYYMMDD.gz` (rotated)
- **User mode (non-root):** `$XDG_STATE_HOME/ventd/logs/observations.log`, mirroring spec-16 §1.1 fallback rules

One log file for all channels. Channel filtering is a consumer concern (forward iterate + predicate on `channel_id`), not a storage concern. Per-channel files would multiply file descriptor and rotation complexity without consumer benefit — Layer B coupling estimation needs all channels' sensors in the same record window anyway.

### 4.2 Rotation policy

Daily rotation OR 50 MB hard cap, whichever fires first. Locked:

```
RotationPolicy {
  MaxSizeMB:    50           // hard cap, defends against pathological volume
  MaxAgeDays:   1            // primary trigger; rotates at 00:00 UTC
  KeepCount:    7            // 7 daily files = 7 days retention
  CompressOld:  true         // always gzip on rotation, no size threshold
}
```

Rationale for daily-primary, size-secondary:

- **Predictable file boundaries.** R13 doctor's "envelope aborts in last 24h" reduces to "read active file + at most one rotated file." `ls -lt` shows the timeline at a glance.
- **Defense against runaway volume.** A bug that signature-thrashes at 100 Hz could blow past the 138 MB/day worst-case modeled in §3. The 50 MB cap forces an early rotation before the active file grows unbounded between midnights.
- **Filename pattern is parseable.** `observations.log.20260429.gz` has the date in the name; retention enforcement is trivial filesystem-level (`find -mtime +7 -delete` works as a fallback if spec-16's KeepCount logic ever has a bug).

### 4.3 Retention

**Default 7 days. Configurable up to 30 days.** Locked rationale:

- 7 days matches R10 §R10.5 Layer-C overlay shard TTL (`τ_evict = 7 days of non-observation`). A signature that hasn't appeared in 7 days has its shard evicted by R10; observations beyond 7 days for that signature train no live model.
- 7 days matches R12 conf_A's recency time constant (`τ_recency_seconds: 604800`). The observation older than 7 days has decayed to <1/e of its weight in the conf_A computation already; logging it longer is consumed by the model's own forgetting.
- R13 RECOVER-007 needs ≥6 days. 7 days is just sufficient. A 30-day config option exists for users who want to investigate slow drifts or do offline forensic analysis across multi-week windows.
- Authoritative *model* state in spec-16 Blob has its own retention (effectively unbounded — the converged Layer A curve persists as long as the file isn't deleted). The log's job is corpus, not persistence.

```toml
[smart.observation_log]
retention_days = 7              # default; matches R10 TTL and R12 τ_recency
# retention_days = 30           # max; beyond this stores no information that
                                #   authoritative model state in spec-16 doesn't already encode
```

If the user reduces `retention_days` below 7, ventd warns at startup that R13 RECOVER-007 detection will be impaired. If they raise above 30, ventd warns that storage cost grows linearly without proportional model benefit.

### 4.4 Crash safety and torn records

Inherited from spec-16 §6.4. `O_APPEND | O_DSYNC` writes; CRC-mismatched or length-overrun records on read are skipped silently. Loss of one observation in a stream of millions is statistically insignificant for Layer A/B/C learning, and the log is advisory rather than authoritative.

### 4.5 Cross-file iteration (spec-16 amendment)

**Spec-16 must be amended:** `LogStore.Iterate(name, since, fn)` MUST transparently traverse rotated files within retention. This is required for `Stream(since=72h ago)` to work when `since` predates today's active file. Adding it to the `LogStore` contract rather than the wrapper API keeps consumers simple. One paragraph addition to spec-16 §6.3 covers the change; CC implementation is small (open files in date order, iterate each, advance to next on EOF).

---

## 5. Read interface

Thin wrapper package `internal/observation/` over spec-16's `LogStore`. Two methods, both locking the simplest path:

```go
package observation

type Reader struct {
  log spec16.LogStore
}

// Stream iterates records in [since, now], in append order.
// fn returns false to stop iteration early.
// Decodes every record fully before invoking fn.
func (r *Reader) Stream(since time.Time, fn func(*Record) bool) error

// Latest returns the last n records matching pred from [since, now].
// Implemented as forward Stream with a bounded ring of n records.
// Use for "last 20 envelope aborts" doctor queries.
func (r *Reader) Latest(since time.Time, pred func(*Record) bool, n int) ([]*Record, error)
```

**Why the simple API and not the fast-path one (StreamFiltered, CountFiltered):**

The locked API decodes every record fully. For doctor's 60s recovery-item poll querying "envelope aborts in last 24h," this is ~86,400 records × 8 channels = ~700,000 record decodes, ~50–100 ms wall time. That is acceptable for a 60s-cadence query. If real-hardware profiling shows it's a problem, fast paths can be added without breaking the simple API. Premature fixed-offset header optimization adds schema complexity, locks field ordering, and creates a class of evolution bugs we don't need to court before measurement says we should.

Cold-start re-warming (Layer A/B/C consumers) iterates the full retention window once at most per daemon lifetime. Wall-clock is bounded by file I/O and CPU at decode rate (~1 GB/min msgpack on a modest CPU); 7 days × 100 MB/day = 700 MB worst case, decoded in well under a minute.

### 5.1 Concurrency

Spec-16 §4.3 single-process model. Reader and writer coexist in one daemon. `Iterate` is read-only; appends continue concurrently. Reader sees a consistent snapshot up to the last fully-written record at iteration start; appends during iteration may or may not be observed, and no consumer cares. No additional locking is required at the wrapper layer.

### 5.2 Consumer access patterns (matrix)

| Consumer | Method | `since` | Filter | Cadence |
|---|---|---|---|---|
| Layer A re-warm (cold start) | `Stream` | `time.Time{}` (full retention) | filter on `channel_id` in fn | once per daemon lifetime, max |
| Layer B/C estimator warmup | `Stream` | `time.Now() - 24h` (typical) | filter on `channel_id` and `controller_state != COLD_START` | once per shard creation |
| R13 doctor — RECOVER-007 detection | `Stream` | `time.Now() - 6*24h` | filter on `signature_label == X && controller_state == CONVERGED` | every 60 s while item is candidate |
| R13 doctor — internals fold "last 20 envelope aborts" | `Latest` | `time.Now() - 24h` | `event_flags & ENVELOPE_C_ABORT != 0` | 5 Hz when section expanded, 0 Hz collapsed |
| R13 doctor — "envelope aborts in last 24h" live metric | `Stream` | `time.Now() - 24h` | counts `event_flags & ENVELOPE_C_ABORT != 0` in fn | 1 Hz |
| Drift detector | (does not read log) | — | — | — |
| Controller hot path | (does not read log) | — | — | — |

---

## 6. Privacy contract

The log is a high-volume stream that contains operational signal but must not contain personally identifying information or any input that could rebuild R7 signatures.

### 6.1 Hard exclusions

The following are NEVER permitted in observation log records:

- Process names (`/proc/PID/comm`)
- PIDs, parent-PIDs, parent-comms
- Executable paths (`/proc/PID/exe`)
- Command lines (`/proc/PID/cmdline`)
- Usernames
- Hostnames
- IP addresses, MAC addresses
- Filesystem paths under `/home`
- Any string supplied by the user (channel labels, fan nicknames, profile names)

The signature_label field is a hex-encoded SipHash-2-4 output under a per-install salt. The salt itself never leaves `/var/lib/ventd/.signature_salt` (mode 0600, owner ventd:ventd) and is explicitly excluded from diag bundles by the P9 redactor. An attacker who exfiltrates the log without the salt cannot rainbow-table comm names from the labels — the label is opaque without the salt.

### 6.2 Diag bundle interaction

Observation log files are NOT included in the default diag bundle. Per the privacy threat model §2.6 architectural exclusion, the bundle's trace ring (cc-prompt-spec03-pr2c §15.5) is the bundle's window into operational behavior — 8 MB / 10k events / 24h cap. The observation log is the longer-horizon companion intended for offline analysis on the user's own machine; sharing it requires explicit opt-in.

If a user opts in via `ventd diag bundle --include-observation-log`, the log is run through the standard P1–P10 redactor pipeline. Note: signature labels are *already* opaque hashes, so they pass through redaction as opaque tokens — no additional special-case redaction for signature labels is required. Channel metadata (sensor names, PWM names) goes through the redactor's user-supplied-label path (P9) by default.

### 6.3 What ventd writes vs what diag bundle exposes

- **Writes:** channel_id (uint16), sensor_id (uint16) — opaque integers resolved against spec-16 KV channel metadata.
- **Diag bundle:** when log is opted-in, the redactor walks the spec-16 KV channel metadata too, replacing user-supplied labels with `[REDACTED:USER_LABEL_n]` consistently. The opaque integers remain integers.

This means the log is privacy-safe by construction at write time, not retroactively at bundle time. Bundle inclusion just exposes what was already there.

---

## 7. Implementation file targets

```
internal/observation/
├── record.go              // Record struct, msgpack codec, Header struct
├── record_test.go         // round-trip, schema-version forward/back-compat
├── writer.go              // Writer type, Append() — wraps spec16.LogStore
├── writer_test.go         // synthetic ticks, rate verification, header emission
├── reader.go              // Reader type, Stream(), Latest()
├── reader_test.go         // iteration correctness, cross-file traversal, predicate
├── rotation.go            // RotationPolicy construction; daily+50MB+7day+gzip
├── rotation_test.go       // boundary spanning, daemon-restart-across-midnight
└── schema.go              // const ControllerState_*, EventFlag_*, RotationPolicy defaults
```

Plus a one-paragraph spec-16 amendment in `specs/spec-16-persistent-state.md` §6.3 making cross-file `Iterate` part of the contract.

Total LOC estimate: ~600 LOC including tests.

---

## 8. Validation criteria for v0.5.4 patch

### 8.1 Synthetic CI tests

Required, all must pass on every PR:

- Round-trip: write 1000 synthetic records, read back, assert byte-equal.
- Schema versioning: write with `schema_version=1`, read with reader expecting version 1, assert success; bump to version 2 in test, assert backward-compatible read of version 1 file with current reader.
- Rate invariant: synthetic 100-tick channel input produces exactly 100 records per channel.
- Rotation at midnight: simulate clock crossing midnight UTC, assert active file rotates with correct date stamp, KeepCount enforced.
- Rotation at size cap: write records until 50 MB, assert rotation fires before next append.
- Cross-file iterate: write records spanning 3 daily files, call `Stream(since=72h)`, assert all records returned in order.
- Torn-record skip: corrupt 1 record's CRC mid-file, iterate, assert that record is skipped and remaining records returned.
- Daemon-restart-across-midnight: simulate ventd offline 23:00 Mon → 09:00 Wed; on restart, assert active file with old mtime is rotated to its mtime's date stamp (not Wednesday's), fresh active file created.
- Privacy invariant: synthetic record-write paths reject any field name in the §6.1 exclusion list at compile time (`go vet` linter or runtime sanity check on writer construction).

### 8.2 Behavioural HIL

**Fleet member: Proxmox host (192.168.7.10).** Run ventd with synthetic controller-state cycling for 48 hours; verify:

- 1 active file + 1 rotated file at hour 24.
- 2 rotated files at hour 48.
- File sizes within 20% of computed §3 budget.
- Reader-from-cold-start replays the full 48h in <30 s wall time.

### 8.3 Time-bound metric

**Not applicable.** Observation logging is passive; it neither speeds nor slows calibration or controller convergence. Explicit not-applicable declared per spec-smart-mode §12.

---

## 9. Cross-references

- **spec-16 (v0.5.0.1)** — storage primitive. Log uses `LogStore` shape. Spec-16 amendment required: `Iterate` MUST traverse rotated files within retention (one paragraph in §6.3).
- **R7** — signature labels are the only signature artifact in records. Salt protection is R7's responsibility; observation log inherits the contract.
- **R8** — `tach_tier` field is R8's classification, recorded once at calibration and carried per record for replay determinism.
- **R9/R10** — `event_flags` carries `LAYER_C_SHARD_ACTIVATED`, `LAYER_C_SHARD_EVICTED`, `R9_IDENT_CLASS_CHANGED` for R10 shard lifecycle replay during cold-start re-warming.
- **R11** — `class=fast/slow` rate model. Channel class is determined per R11's latency-vs-τ admissibility rule and recorded in spec-16 KV channel metadata, surfaced to the log via per-file `Header.channel_class_map`.
- **R12** — `r12_residual` is the per-tick innovation feeding conf_C reconstruction. R12's authoritative state (conf scalars, RLS state) persists in spec-16 KV/Blob separately; the log carries inputs only.
- **R13** — doctor's two log-consuming surfaces (RECOVER-007 + 24h event-flag live metrics) are the only consumers reading the log on the live path. All other doctor surfaces read in-memory state.
- **R14** — calibration checkpoints live in spec-16 KV under `calibration:`, NOT in the observation log. Observation log records *what the controller did*; calibration checkpoints record *what calibration is currently doing*. Distinct namespaces.
- **R15** — spec-05 amendment confirms predictive controller telemetry uses spec-16 append-log primitive, which is what this log spec instantiates. R15 §Drift-6 names `$STATE_DIR/smart/telemetry/ring.binlog` as the path; the schema doc here defines that file's contents.

---

## 10. Open items / HIL gaps

### 10.1 Open

- **`event_flags` bit 13–31 reservation policy.** Future R-items (drift recalibration mechanics, Layer C deeper signature analysis) may want flags. Convention: a new patch that adds a flag bumps schema_version, adds a forward-migration entry in spec-16, and documents the flag in this schema doc.
- **`r12_residual` precision.** float32 is sufficient for the residual range observed in R12 §conf_C testing (typical |e_k| < 5°C, float32 gives ~6 decimal digits). If a later R-item shows residual analysis needs more precision, a schema bump to float64 is straightforward.
- **Whether to add `pwm_readback`.** Currently excluded per §2.5 — readback noise is a Layer A modeling concern, not logging. If R6 polarity field experience shows readback divergence is frequent enough to warrant logging (e.g., Dell SMM quantization revealing calibration drift), add `pwm_readback uint8` in schema v2.

### 10.2 HIL gaps

- **Storage budget validation under realistic workload.** §3 figures are computed; real homelab workloads may produce different signature-label cardinality and event-flag rates. Validate on Proxmox host across a 7-day window in v0.5.4 PR HIL.
- **Reader replay performance on slow storage.** Cold-start re-warming on a NAS with HDD-backed `/var/lib` may be slower than the modeled <30 s. F2-210 HIL validation when smart-mode is enabled there will inform.
- **Signature label cardinality on Bazel/Buck2 builds.** R7 §HIL gap notes none of the fleet runs these toolchains; if observed signature-label cardinality is much higher than expected, the 80-char label cap may need to grow (which inflates log size). Forward-looking note, not a v0.5.4 blocker.

---

## 11. Conclusions actionable for spec-v0_5_4-passive-observation.md

When that patch spec is drafted (in chat, $0):

1. **§2 sample shape is the schema. Lock the field list verbatim.** Schema-version-1 emit must match this struct. Migration registry handles future field additions.
2. **§3 sampling rate folds into spec-v0_5_4's RULE-OBS-RATE-* bindings.** One rule per class (fast, slow), each binding to a CI test that asserts "synthetic N-tick input produces exactly N records per channel."
3. **§4 rotation policy values are constants in `internal/observation/schema.go`**, not config knobs at v0.5.4. Config exposure is deferred until field telemetry says specific values need tuning.
4. **§5 read API is the v0.5.4 surface.** `Stream` and `Latest` only. `StreamFiltered`/`CountFiltered` are post-v0.6.0 additions if profiling justifies them.
5. **§6 privacy exclusions become RULE-OBS-PRIVACY-* bindings.** Compile-time linter or runtime sanity check on writer construction; the test suite includes synthetic attempts to write each excluded field, asserting rejection.
6. **§7 file targets are the PR scope.** Single PR: `internal/observation/` package, spec-16 amendment paragraph, RULE-OBS-* bindings file, test fixtures.
7. **Estimated CC cost (Sonnet, post-spec-drafting):** $10-20. Within the $10-20 estimate already in spec-smart-mode §13 cost projection table for v0.5.4.
8. **No new dependencies.** msgpack already vendored for R7. spec-16's LogStore primitive already shipping in v0.5.0.1.
9. **HIL verification on Proxmox host (192.168.7.10).** 48h soak per §8.2.

---

**Schema lock complete.** This document is the design-of-record for the v0.5.4 passive observation log. v0.5.4 patch spec consumes it; v0.5.7 (Layer B), v0.5.8 (Layer C), v0.5.10 (doctor) consume the log built against this schema without re-litigating field decisions.
