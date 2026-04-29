# spec-v0_5_4 — Passive observation log

**Status:** DESIGN. Drafted 2026-04-29.
**Ships as:** v0.5.4 (fourth smart-mode behaviour patch).
**Depends on:** v0.5.1 spec-16 persistent state (LogStore primitive,
`Iterate` cross-file traversal — already shipped in PR #669).
**Consumed by:** v0.5.7 Layer B coupling (cold-start re-warming),
v0.5.8 Layer C RLS (cold-start re-warming, signature replay), v0.5.10
doctor (RECOVER-007 + 24h event-flag live metrics).
**References:** `ventd-passive-observation-log-schema.md` (locked
design-of-record; this spec consumes §2 fields, §3 sampling rate,
§4 storage, §5 read API, §6 privacy verbatim per §11),
`specs/spec-16-persistent-state.md` (storage substrate),
`specs/spec-smart-mode.md` (parent design, §6.4 update mechanisms),
`specs/spec-v0_5_1-catalog-less-probe.md` (probe layer that produces
the channel set this log records).

---

## 1. Why this patch exists

The smart-mode patch sequence past v0.5.4 (Layer B v0.5.7, Layer C
v0.5.8, doctor v0.5.10) all consume historical observation data. They
need a deterministic, replayable, retention-bounded record stream of
every controller tick — what was written, what was observed, what
classifier state was active, what events fired.

Without this log, Layer B/C cannot re-warm after spec-16 Blob
corruption or `ventd state reset`; doctor's RECOVER-007 detection
cannot fire (needs ≥6 days of signature-correlated observations);
"envelope aborts in 24h" and "Layer A activations in 24h" live
metrics have no source.

v0.5.4's sole job is to ship the writer + reader infrastructure
against the locked schema. No consumer logic. No analysis. No UI
surface. The patches that consume the log are v0.5.7 / v0.5.8 /
v0.5.10.

## 1.1 Ground-up principle

The schema is the contract. Every field, every event-flag bit, every
sampling-rate decision is locked by
`ventd-passive-observation-log-schema.md`. v0.5.4 transcribes that
schema into Go and tests. It does not re-litigate field choices,
retention defaults, or privacy exclusions. Future schema changes
require a schema bump (`schema_version=2`) with a forward-migration
entry per spec-16 §7.3 — handled by the patch that introduces them,
not by v0.5.4.

---

## 2. Scope

### 2.1 In scope

- `internal/observation/` package with the file structure named in
  schema doc §7.
- `Record` struct matching schema doc §2.1 verbatim, msgpack-encoded.
- `Header` struct matching schema doc §2.4 verbatim, one per log
  file.
- `controller_state` enum matching schema doc §2.2 (7 values).
- `event_flags` bitmask matching schema doc §2.3 (bits 0–12 active,
  13–31 reserved).
- `Writer` type wrapping spec-16 `LogStore.Append`. Per-tick write
  path. Privacy-safe construction — see §6.
- `Reader` type wrapping spec-16 `LogStore.Iterate`. Two methods:
  `Stream(since, fn)` and `Latest(since, pred, n)` per schema doc §5.
- Sampling rate enforcement per schema doc §3: fast = 0.5 Hz,
  slow = 1/60 Hz. Class determined at calibration time and read from
  spec-16 KV channel metadata; not re-derived per tick.
- Header emission on log-file creation: `schema_version=1`,
  `dmi_fingerprint` (truncated SHA-256 from spec-03), `ventd_version`,
  `rotation_ts`, `channel_class_map`.
- `RotationPolicy` constants in `schema.go`: daily rotation OR 50 MB
  hard cap whichever fires first; `MaxAgeDays=7` retention default;
  `KeepCount` derived from retention; `CompressOld=true` (gzip on
  rotation when rotated file > 10 MB per spec-16 §6.2).
- Privacy invariant: the writer constructor and per-field write path
  refuse any field name in schema doc §6.1 exclusion list.
  Implementation: compile-time enforcement via static `Record` struct
  shape (no `map[string]interface{}` anywhere on the write path) plus
  a runtime sanity check at `Writer.New()` that asserts no excluded
  field name appears among the registered field set.
- `RULE-OBS-*` invariant bindings in `.claude/rules/observation.md`,
  1:1 with subtests, enforced by `tools/rulelint`.
- Synthetic CI tests per schema doc §8.1 (all 9 cases).
- HIL validation script wired to run on Proxmox host 192.168.7.10
  per schema doc §8.2 (48h soak).

### 2.2 Out of scope

- **Consumer logic.** No Layer A re-warm code, no Layer B coupling
  estimator, no Layer C RLS, no doctor recovery items. Those are
  v0.5.7 / v0.5.8 / v0.5.10.
- **Configuration exposure for rotation policy.** Schema doc §11.3
  locks rotation values as constants in `schema.go`, NOT config
  knobs. Config exposure deferred until field telemetry justifies
  tuning.
- **Fast-path read APIs (`StreamFiltered`, `CountFiltered`).** Schema
  doc §5 locks the simple `Stream` + `Latest` API at v0.5.4. Fast
  paths are post-v0.6.0 if profiling justifies.
- **Diag bundle inclusion.** Observation log is excluded from default
  diag bundles per schema doc §6.2. The opt-in flag
  `--include-observation-log` ships in the diag-bundle owner's spec,
  not here.
- **Spec-16 amendments.** The §6.3 cross-file `Iterate` contract is
  already documented and behaviourally shipped in v0.5.1 PR #669.
  v0.5.4 consumes the documented contract.
- **Schema v2.** No new fields, no precision changes (`r12_residual`
  stays float32 per schema doc §10.1). Schema bumps belong to the
  patch that introduces the new field.
- **Live UI surfaces of log data.** R13 doctor's log-consuming
  surfaces ship in v0.5.10. v0.5.4 ships the read API only.
- **Drift detector consumption.** Drift detector reads in-memory
  residual stream per R12 §Q8 / R15 Drift-5; never reads the log.
- **Per-channel log files.** One `observations` log for all channels
  per schema doc §4.1. Channel filtering is a consumer concern.

---

## 3. Invariant bindings

`.claude/rules/observation.md` binds 1:1 to subtests in
`internal/observation/`. Enforced by `tools/rulelint`.

| Rule | Binding |
|---|---|
| `RULE-OBS-SCHEMA-01` | `Record` field set, types, and order MUST match schema doc §2.1. msgpack round-trip MUST be byte-equal. |
| `RULE-OBS-SCHEMA-02` | `Header` field set MUST match schema doc §2.4. One Header per log file, written before any Record. |
| `RULE-OBS-SCHEMA-03` | `schema_version=1` is the v0.5.4 emit value. Reader MUST reject unknown future versions with a documented diagnostic. |
| `RULE-OBS-SCHEMA-04` | `controller_state` enum values MUST be exactly the 7 values in schema doc §2.2 (0=COLD_START through 6=MONITOR_ONLY). |
| `RULE-OBS-SCHEMA-05` | `event_flags` bits 0–12 MUST match schema doc §2.3 names and meanings. Bits 13–31 reserved; writer MUST NOT emit them. |
| `RULE-OBS-RATE-01` | Fast-class channels (R11 class=0) MUST emit exactly one record per controller tick at 0.5 Hz cadence. |
| `RULE-OBS-RATE-02` | Slow-class channels (R11 class=1) MUST emit exactly one record per controller tick at 1/60 Hz cadence. |
| `RULE-OBS-RATE-03` | Channel class MUST be read from spec-16 KV channel metadata at writer construction. The writer MUST NOT re-derive class per tick. |
| `RULE-OBS-PRIVACY-01` | The writer MUST refuse construction if any registered field name appears in the schema doc §6.1 exclusion list (process names, PIDs, exec paths, cmdline, usernames, hostnames, IP/MAC, `/home` paths, user-supplied labels). |
| `RULE-OBS-PRIVACY-02` | The `Record` struct MUST NOT contain `map[string]interface{}`, `map[string]string`, or any field whose value is user-controlled string content. Channel and sensor identity MUST be opaque integer IDs only. |
| `RULE-OBS-PRIVACY-03` | `signature_label` MUST be the hex-encoded SipHash-2-4 output supplied by R7. The writer MUST NOT compute or transform the label; it accepts the opaque string from the controller. |
| `RULE-OBS-ROTATE-01` | Active log rotates on the first append after midnight UTC. Rotated file is named with the date stamp of its mtime, not the rotation moment. |
| `RULE-OBS-ROTATE-02` | Active log rotates on the first append after the file reaches 50 MB. Rotation MUST complete before the triggering append commits. |
| `RULE-OBS-ROTATE-03` | Rotated files larger than 10 MB MUST be gzipped per spec-16 §6.2. Reader MUST transparently decompress. |
| `RULE-OBS-ROTATE-04` | Retention enforces `MaxAgeDays=7` and `KeepCount` derived from retention. Files outside retention MUST be deleted on the next rotation event. |
| `RULE-OBS-READ-01` | `Stream(since, fn)` MUST iterate records in append order across active and rotated files within retention. Iteration stops when `fn` returns false. |
| `RULE-OBS-READ-02` | `Latest(since, pred, n)` MUST return at most `n` matching records by forward iteration with bounded ring; it MUST NOT load full retention into memory. |
| `RULE-OBS-CRASH-01` | Per spec-16 §6.4: torn records (CRC mismatch or length-overrun) MUST be skipped silently on read. Iteration MUST continue past skipped records. |

---

## 4. Subtest mapping

Tests live in `internal/observation/`. Filenames per schema doc §7.

| Rule | Subtest |
|---|---|
| RULE-OBS-SCHEMA-01 | `TestRecord_RoundTrip_ByteEqual` |
| RULE-OBS-SCHEMA-02 | `TestHeader_OnePerFile_PrecedesRecords` |
| RULE-OBS-SCHEMA-03 | `TestSchemaVersion_RejectsUnknownFuture` |
| RULE-OBS-SCHEMA-04 | `TestControllerState_EnumValuesLocked` |
| RULE-OBS-SCHEMA-05 | `TestEventFlags_Bits0Through12Locked` |
| RULE-OBS-RATE-01 | `TestWriter_FastClass_EmitsExactlyOneRecordPerTick` |
| RULE-OBS-RATE-02 | `TestWriter_SlowClass_EmitsExactlyOneRecordPerTick` |
| RULE-OBS-RATE-03 | `TestWriter_ClassReadFromKV_NotRederivedPerTick` |
| RULE-OBS-PRIVACY-01 | `TestWriter_New_RefusesConstructionWithExcludedField` |
| RULE-OBS-PRIVACY-02 | `TestRecord_StructHasNoUserControlledStrings` |
| RULE-OBS-PRIVACY-03 | `TestWriter_SignatureLabel_AcceptedOpaque_NotTransformed` |
| RULE-OBS-ROTATE-01 | `TestRotation_AcrossMidnight_CorrectDateStamp` |
| RULE-OBS-ROTATE-02 | `TestRotation_At50MB_BeforeAppendCommits` |
| RULE-OBS-ROTATE-03 | `TestRotation_GzipAbove10MB_ReaderTransparentlyDecompresses` |
| RULE-OBS-ROTATE-04 | `TestRotation_RetentionEnforced_OldFilesDeleted` |
| RULE-OBS-READ-01 | `TestReader_Stream_TraversesActiveAndRotated_InOrder` |
| RULE-OBS-READ-02 | `TestReader_Latest_BoundedRing_NotFullLoad` |
| RULE-OBS-CRASH-01 | `TestReader_TornRecord_SkippedSilently_IterationContinues` |

Plus the schema doc §8.1 case `TestWriter_DaemonRestartAcrossMidnight_RotatesActiveByMtime`
binds to RULE-OBS-ROTATE-01 as a second subtest.

---

## 5. Success criteria

### 5.1 Synthetic CI tests

All 18 subtests above pass on every PR. `tools/rulelint` reports
zero unbound rules, zero unused subtests.

### 5.2 Behavioural HIL — Proxmox host (192.168.7.10)

48h soak with synthetic controller-state cycling:

- 1 active file + 1 rotated `.gz` file at hour 24.
- 2 rotated `.gz` files + 1 active at hour 48.
- File sizes within 20% of computed §3 schema-doc budget for the
  configured channel set (8-channel desktop class).
- `Reader.Stream(since=time.Time{}, fn)` from cold start replays the
  full 48h in <30 s wall time on the Proxmox host's storage.
- `htop` shows the writer goroutine consumes <0.5% CPU averaged over
  the 48h window. No measurable controller hot-loop slowdown vs. a
  build with logging compiled out.

### 5.3 Time-bound metric

**Not applicable.** Per schema doc §8.3: observation logging is
passive; it neither speeds nor slows calibration or controller
convergence. Explicit not-applicable declared per spec-smart-mode
§12.

---

## 6. Privacy contract — implementation

Schema doc §6 is the contract. v0.5.4 implements it via three
defenses:

1. **Compile-time:** `Record` struct shape is fixed and contains no
   `map[string]interface{}` or user-controlled strings. The Go type
   system makes it impossible for a caller to attach process names,
   PIDs, hostnames, etc. to a record.
2. **Construction-time:** `Writer.New()` runs a sanity check that
   reflects over the registered field set and refuses construction
   if any field name matches the schema doc §6.1 exclusion list.
   The check fires at daemon startup, before any record is written.
3. **Test-time:** `TestWriter_New_RefusesConstructionWithExcludedField`
   synthesises a malicious `Record` variant for each of the 9
   exclusion categories and asserts construction fails.

The `signature_label` field is the only string in the record. It is
the hex-encoded SipHash-2-4 output produced upstream by R7 with the
per-install salt at `/var/lib/ventd/.signature_salt`. v0.5.4 accepts
the label opaquely; it does not touch the salt or the hash. The
salt's mode-0600 protection is R7's responsibility.

---

## 7. Out-of-scope (extended)

In addition to §2.2:

- **`pwm_readback` field.** Schema doc §10.1 lists this as a future
  candidate. Not in v0.5.4. If R6 polarity-field experience justifies
  it, a future schema bump adds it.
- **Bazel/Buck2 signature-label cardinality validation.** Schema doc
  §10.2 notes none of the fleet runs these toolchains. Not a v0.5.4
  blocker.
- **Reader replay performance on HDD-backed `/var/lib`.** Schema doc
  §10.2 flags this as an F2-210 HIL concern when smart-mode is
  enabled there. v0.5.4 verifies on Proxmox host's storage class
  (typical homelab SSD); HDD-NAS validation is a follow-up.
- **Active probing.** v0.5.5 territory. v0.5.4 is purely passive.

---

## 8. Failure modes enumerated

1. **`/var/lib/ventd/logs/` does not exist on first start.**
   spec-16's directory creation handles this. Writer construction
   succeeds against the empty directory; first append creates the
   active file with a header.

2. **Active log file CRC-corrupted by power loss mid-record.** Per
   spec-16 §6.4 + RULE-OBS-CRASH-01: torn last record skipped on
   read. Statistically insignificant for Layer B/C learning over the
   retention window.

3. **Disk full during append.** spec-16 `LogStore.Append` returns
   error; writer surfaces it to the caller (controller). Controller
   continues without logging this tick. Retention rotation will
   reclaim space on the next rotation event.

4. **Channel class missing from spec-16 KV at writer construction.**
   Class is set during v0.5.1 probe; if absent, v0.5.4 writer refuses
   construction with a diagnostic naming the channel ID. This is a
   spec-16 / spec-v0_5_1 contract violation, not a v0.5.4 logic bug.

5. **Schema-version mismatch on read (file written by future v0.5.x
   with `schema_version=2`).** Reader rejects with a documented
   diagnostic per RULE-OBS-SCHEMA-03. Consumer (Layer B/C re-warm,
   doctor) treats the file as missing for re-warming purposes; the
   later patch's forward-migration registry handles the upgrade.

6. **Daemon restart across midnight UTC.** Active file's mtime is in
   the previous day. Per RULE-OBS-ROTATE-01: on first append after
   restart, active file is rotated to its mtime's date stamp (not
   restart-day), fresh active file created.

7. **Daemon offline >7 days.** Retention reclaim deletes all files
   beyond `MaxAgeDays`. On restart, log effectively cold-empty.
   Layer B/C re-warm sees no history; consumers must tolerate this
   as their own contract (v0.5.7 / v0.5.8 design responsibility).

8. **Reader iteration during active write.** spec-16 §4.3 single-
   process model + §5.1 schema-doc concurrency model: reader sees a
   consistent snapshot up to the last fully-written record at
   iteration start; concurrent appends may or may not be observed,
   no consumer cares.

9. **Concurrent multiple `Writer` instances.** Forbidden. Writer
   construction acquires a per-log-name lock from spec-16 KV; second
   construction on the same log name fails with diagnostic. Test:
   `TestWriter_New_SecondInstance_Refused` — additional subtest
   under RULE-OBS-PRIVACY-01's umbrella (writer construction
   discipline).

10. **Privacy exclusion list expansion.** If a future patch adds a
    new excluded category, that patch updates schema doc §6.1 AND
    `.claude/rules/observation.md` AND the runtime sanity check in
    one PR. v0.5.4 does not pre-bake categories beyond the schema
    doc §6.1 list.

---

## 9. PR sequencing

Single PR. Files:

```
internal/observation/record.go
internal/observation/record_test.go
internal/observation/writer.go
internal/observation/writer_test.go
internal/observation/reader.go
internal/observation/reader_test.go
internal/observation/rotation.go
internal/observation/rotation_test.go
internal/observation/schema.go
.claude/rules/observation.md
specs/spec-v0_5_4-passive-observation.md   (this file)
validation/observation-hil.sh              (Proxmox 48h soak script)
```

Total LOC estimate: ~600 LOC including tests (per schema doc §7).

No spec-16 edits in this PR — the §6.3 cross-file `Iterate`
amendment is already in main as of the in-flight spec-doc-fix branch
referenced in the schema doc.

No new dependencies. msgpack already vendored for R7.

---

## 10. Estimated cost

- Spec drafting (chat): $0 (this document, on Max plan).
- CC implementation (Sonnet, single tight PR): **$10–20** per schema
  doc §11.7. Within the $10–20 estimate already in
  `spec-smart-mode.md` §13 cost projection table.
- Bindings to `.claude/rules/observation.md`: included in PR scope.
- Synthetic CI tests: included in PR scope.
- HIL verification: post-merge, Phoenix manual on Proxmox 192.168.7.10.

Calibration note: the schema-lock document is exhaustive. Sonnet
should transcribe rather than explore. Apply the ground-truth probe
pattern (Q1–Q5 Haiku pre-flight) only if CC surfaces ambiguity in the
schema fields — none expected.

---

## 11. References

- `ventd-passive-observation-log-schema.md` — locked design-of-record.
- `specs/spec-16-persistent-state.md` — storage substrate.
- `specs/spec-smart-mode.md` — parent design.
- `specs/spec-v0_5_1-catalog-less-probe.md` — channel-class metadata
  source.
- `ventd-R12-amendment-threshold-recalibration.md` — `r12_residual`
  precision context.
- `spec-06-install-contract.md` — AppArmor profile (no v0.5.4
  changes; `/var/lib/ventd/**` write access already authorised by
  spec-16 v0.5.0.1).
