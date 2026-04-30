# Observation Log Rules

These invariants govern the passive observation log infrastructure in
`internal/observation/`. Every rule is 1:1 with a subtest. `tools/rulelint`
fails the build if a rule lacks a corresponding subtest.

## Schema

## RULE-OBS-SCHEMA-01: MarshalRecord + UnmarshalPayload round-trips a Record field-for-field.

`MarshalRecord(*Record)` prepends frame byte `0x01` and encodes the Record with
msgpack. `UnmarshalPayload(payload)` inspects the first byte, decodes the msgpack
body, and returns a non-nil `*Record` and nil `*Header`. All named fields must
survive the round-trip byte-equal. A field that silently truncates or omits during
encode/decode would produce silent data loss in any downstream consumer (analysis,
doctor, diagnostic bundle).

Bound: internal/observation/record_test.go:TestRecord_RoundTrip_ByteEqual

## RULE-OBS-SCHEMA-02: Exactly one Header precedes all Records in each log file.

After appending n records via a fresh Writer, the log file starts with exactly one
Header payload (frame byte `0x02`) followed by n Record payloads (frame byte `0x01`).
No second Header may appear within the same file. A header emitted per-record would
inflate log size and corrupt the frame layout assumed by any Reader.

Bound: internal/observation/record_test.go:TestHeader_OnePerFile_PrecedesRecords

## RULE-OBS-SCHEMA-03: Reader.Stream returns an error for Header.SchemaVersion != 1.

When `UnmarshalPayload` decodes a Header whose `SchemaVersion` field is not equal to
`schemaVersion` (1), `Reader.Stream` returns a diagnostic error and the callback `fn`
is never called. Forward schema versions (future writers on older readers) must be
refused rather than silently ignored so that stale readers report a clear diagnostic
instead of producing corrupt output.

Bound: internal/observation/record_test.go:TestSchemaVersion_RejectsUnknownFuture

## RULE-OBS-SCHEMA-04: ControllerState enum constants have the exact values from §2.2.

The seven `ControllerState_*` constants must have the exact integer values:
`COLD_START=0`, `WARMING=1`, `CONVERGED=2`, `DRIFTING=3`, `ABORTED=4`,
`MANUAL_OVERRIDE=5`, `MONITOR_ONLY=6`. A renumbering would corrupt any log file
read by a binary with a different constant layout.

Bound: internal/observation/record_test.go:TestControllerState_EnumValuesLocked

## RULE-OBS-SCHEMA-05: EventFlag bitmask constants occupy bits 0–12; bits 13–31 are reserved.

The 13 `EventFlag_*` constants must occupy exactly bits 0–12 with no collisions.
`eventFlagReservedMask` must cover exactly bits 13–31 and no bit in 0–12.
Bits outside the defined set are masked to zero by `Append` before persisting,
preventing forward-compat bits from an old writer from being silently propagated
into a new log file.

Bound: internal/observation/record_test.go:TestEventFlags_Bits0Through12Locked

## Rate / Class

## RULE-OBS-RATE-01: Writer emits exactly one Record per Append call for fast-class (hwmon) channels.

Each call to `w.Append(r)` stores exactly one msgpack-encoded Record payload in the
log store. Headers are emitted once per file, not per record. `ChannelClassMap[id]`
in the Header must be `0` (fast) for channels whose driver is not in `slowClassDrivers`.

Bound: internal/observation/writer_test.go:TestWriter_FastClass_EmitsExactlyOneRecordPerTick

## RULE-OBS-RATE-02: Writer emits exactly one Record per Append call for slow-class (drivetemp) channels.

Same as RULE-OBS-RATE-01 but for slow-class drivers (`drivetemp`, `bmc`, `ipmi`).
`ChannelClassMap[id]` in the Header must be `1` (slow). The class difference is
metadata only — the write rate is still one Record per Append call regardless of class.

Bound: internal/observation/writer_test.go:TestWriter_SlowClass_EmitsExactlyOneRecordPerTick

## RULE-OBS-RATE-04: When channel class is absent from KV at construction, class is inferred from driver name.

`loadOrInferClassMap` checks the KV store for each channel's class key. When the key is
absent, it falls back to driver-name inference using `slowClassDrivers`:
`drivetemp`, `bmc`, `ipmi` → slow (1); all other driver names → fast (0). Construction
always succeeds. This avoids a hard dependency on spec-16 KV state being pre-populated
and handles fresh installs where the v0.5.1 probe result is not yet present.

Bound: internal/observation/writer_test.go:TestWriter_ClassInferredFromDriver_WhenAbsentFromKV

## RULE-OBS-RATE-03: Channel class is loaded from KV at construction time and never re-derived per tick.

The `classMap` is populated once inside `newWithClock` from the KV store (falling back
to driver-name inference when the key is absent). After construction, mutating the KV
store must not change `classMap`. The `Header.ChannelClassMap` written by the first
Append must reflect the class resolved at construction, not the current KV value.

Bound: internal/observation/writer_test.go:TestWriter_ClassReadFromKV_NotRederivedPerTick

## Privacy

## RULE-OBS-PRIVACY-01: Writer construction fails when the Record struct has a msgpack field tag matching an excluded category.

`validateFieldExclusion(v any)` inspects every exported struct field's `msgpack` tag and
returns a non-nil error if any tag matches an excluded category: `hostname`, `username`,
`pid`, `comm`, `exe`, `cmdline`, `mac_addr`, `ip_addr`, `home`, `nickname`. Legitimate
field names (`ts`, `signature_label`, etc.) must not be rejected. The real `Record`
struct must always pass the exclusion check.

Bound: internal/observation/writer_test.go:TestWriter_New_RefusesConstructionWithExcludedField

## RULE-OBS-PRIVACY-02: Record struct contains no map[string]interface{} or unconstrained string fields beyond signature_label.

The structural guarantee: the only string field on `Record` is `signature_label`, which
holds an opaque SipHash-2-4 hex digest from R7. `sensor_readings` is `map[uint16]int16`,
not `map[string]anything`. A free-form string field would allow user-controlled content
to enter the log under a key that is not validated by `validateFieldExclusion`.

Bound: internal/observation/record_test.go:TestRecord_StructHasNoUserControlledStrings

## RULE-OBS-PRIVACY-03: signature_label is stored verbatim — no hashing or transformation.

`Writer.Append` stores the `SignatureLabel` field from the caller's Record unchanged.
`UnmarshalPayload` returns the same bytes on read-back. The label is an opaque
SipHash-2-4 output produced by R7; re-hashing or transforming it would break the
cross-reference between the observation log and R7's signature store.

Bound: internal/observation/writer_test.go:TestWriter_SignatureLabel_AcceptedOpaque_NotTransformed

## Read

## RULE-OBS-READ-01: Reader.Stream traverses active and rotated files in append order.

`Reader.Stream(since, fn)` calls `logStore.Iterate` which visits all files in order.
Headers are consumed transparently. Records are delivered to `fn` in the order they
were appended, across all files. Returning false from `fn` stops iteration cleanly.

Bound: internal/observation/reader_test.go:TestReader_Stream_TraversesActiveAndRotated_InOrder

## RULE-OBS-READ-02: Reader.Latest returns at most n records without loading the full history into memory.

`Reader.Latest(since, pred, n)` uses a bounded ring buffer of capacity n. Only n
record pointers are held in memory at any point; full retention is never loaded.
With 10 records and n=3, the last 3 records (highest Ts) are returned in append order.

Bound: internal/observation/reader_test.go:TestReader_Latest_BoundedRing_NotFullLoad

## Crash

## RULE-OBS-CRASH-01: Reader silently skips corrupt payloads and continues iteration.

When `UnmarshalPayload` returns a non-nil error for a payload (e.g. a torn write
with a valid frame byte but truncated msgpack body), `Reader.Stream` returns nil
from the Iterate callback (skip) and continues with the next payload. A single
corrupt record must not prevent delivery of the records that follow it.

Bound: internal/observation/reader_test.go:TestReader_TornRecord_SkippedSilently_IterationContinues

## Rotation

## RULE-OBS-ROTATE-01: Append triggers rotation when the clock crosses midnight since the active-file day.

`Writer.Append` compares `truncateToDay(now)` against `w.activeDay`. When they differ,
`w.Rotate()` is called before the record is written, creating a new file and emitting a
fresh Header. Two consecutive Appends on different calendar days produce two log files,
each starting with a Header. A fresh Writer constructed on the same calendar day as the
persisted `kvActiveDayKey` does not rotate and does not emit a duplicate Header.

Bound: internal/observation/rotation_test.go:TestWriter_Rotate_MidnightCrossing
Bound: internal/observation/rotation_test.go:TestWriter_Rotate_DaemonRestart_SameDay

## RULE-OBS-ROTATE-02: Append triggers rotation when bytesWritten reaches the 50 MiB size cap.

`Writer.Append` checks `w.bytesWritten >= maxActiveSize` before each write. When the
cap is reached, `w.Rotate()` is called, a new file is opened, and the byte counter
resets. The caller can directly set `w.bytesWritten = maxActiveSize` in tests to
exercise the cap without writing 50 MiB of data.

Bound: internal/observation/rotation_test.go:TestWriter_Rotate_SizeLimit

## RULE-OBS-ROTATE-03: Rotation, header persistence, and Reader.Stream work end-to-end against the real state store.

With real `state.LogDB` and `state.KVDB` (opened on a tempdir), two records written on
day1 plus one on day2 (via clock injection) produce two log files. `Reader.Stream`
returns all 3 records in timestamp order via the real Iterate path.

Bound: internal/observation/rotation_test.go:TestWriter_Rotate_IntegrationWithState

## RULE-OBS-ROTATE-04: Writer calls SetRotationPolicy at construction with KeepCount=8 and CompressOld=true; MaxSizeMB=0 and MaxAgeDays=0 disable LogDB auto-rotation.

The observation Writer handles rotation itself (midnight + 50 MiB cap). LogDB
auto-rotation triggers are disabled by setting MaxSizeMB=0 and MaxAgeDays=0 in
`writerPolicy`. KeepCount=8 and CompressOld=true from `DefaultRotationPolicy` are
preserved so LogDB still prunes and compresses old files after the Writer calls
`log.Rotate`.

Bound: internal/observation/rotation_test.go:TestWriter_Rotate_RetentionPolicyApplied
