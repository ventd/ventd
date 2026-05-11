# Pass 6: internal/hal/hwmon/ deep read

**Files audited:**
- `/root/ventd-work/internal/hal/hwmon/backend.go` (410 LOC)
- `/root/ventd-work/internal/hal/hwmon/sentinel.go` (131 LOC)
- `/root/ventd-work/internal/hal/hwmon/export_test.go` — read for context, not in scope (test seam only)

**LOC:** 541 (production); export_test.go excluded
**Time on task:** ~30 min
**Baseline commit:** `b46c1a5` (post pass-1..5 audit doc merge)

Scope is narrow: this is the lightest hwmon-adjacent package by LOC, but the
load-bearing one — it owns the sentinel filter constants AND the EBUSY-retry
primitive AND the per-channel `acquired` state. Every other read/write path
in the daemon (`internal/hwmon/*`, `internal/monitor/*`, `internal/controller/*`,
`internal/web/server.go`, `internal/calibrate/*`) is downstream of one of the
two files audited here.

There is no mode-reacquire helper that lives in a separate file — the entire
EBUSY recovery primitive is inline in `Backend.Write`. Likewise, there is no
separate "pwm_enable enum probe / cache / fallback" file — that lives in
`internal/setup/restore_excluded.go` and is OUT of scope for this pass (covers
the wizard's `restoreExcludedChannels` code path, not the controller's hot loop).

## Critical findings (sentinel leak, hardware damage class)

None. The sentinel filter contract holds at every backend-internal serialisation
boundary. The previously-caught gap at `monitor.Scan()` (RULE-HWMON-SENTINEL-STATUS-BOUNDARY)
has been closed: `internal/monitor/monitor.go:201` calls `isSentinelMonitorVal`
before appending to `Device.Readings`. Spot-checked four downstream consumers
(`internal/controller/controller.go:759`, `internal/web/server.go:951`,
`internal/web/server.go:988`, `internal/calibrate/calibrate.go:1329`) — each
calls `halhwmon.IsSentinelRPM` / `halhwmon.IsSentinelSensorVal` before either
populating a Reading struct or recording a calibration sample.

## High findings (correctness bug, race, EBUSY storm)

### H1 — `monitor.isSentinelMonitorVal` is asymmetric with `IsSentinelSensorVal`; the absolute-zero floor is NOT enforced at the monitor scan boundary.

**File:** `internal/monitor/monitor.go:297-307`

`IsSentinelSensorVal` (sentinel.go:120-131) rejects temperatures via:
`val >= PlausibleTempMaxCelsius || val <= PlausibleTempMinCelsius` — i.e. both
high cap (150°C) AND absolute-zero floor (−273.15°C). The monitor-side variant
`isSentinelMonitorVal` (monitor.go:297) only checks the high cap:
`val >= halhwmon.PlausibleTempMaxCelsius`. A driver underflow producing
−2147483.648°C (the canonical int32-divided-by-1000 case named in
RULE-SENTINEL-TEMP-FLOOR) WOULD pass through `monitor.Scan()` and reach the
`Device.Readings` slice / `GET /api/hardware` JSON. The R28 audit text in
sentinel.go:51-60 explicitly cites this as the motivating failure class.

Per RULE-HWMON-SENTINEL-STATUS-BOUNDARY, "every serialization boundary" must
enforce the same filter. The monitor.go variant is the surface this rule
explicitly names as the boundary that previously leaked. The current
asymmetry resurrects the same class of leak for the absolute-zero floor
specifically.

Two consistency fixes are possible: (a) make `isSentinelMonitorVal` call
the same `IsSentinelSensorVal` it imports, OR (b) inline the floor check
in `isSentinelMonitorVal`. Either way the comment at monitor.go:295-296
("Mirrors the thresholds in internal/hal/hwmon/sentinel.go") is currently
misleading — the mirror is incomplete.

### H2 — `Backend.Read` returns Reading{OK=false, RPM=valid} on PWM-read failure paths; consumers that ignore OK get a misleading half-populated reading.

**File:** `internal/hal/hwmon/backend.go:146-178`

The flow is: `reading.OK = true` at line 146. If `ReadPWM` fails, line 151
sets OK=false but the subsequent RPM read (lines 154-177) still populates
`reading.RPM` on success. A consumer that reads `reading.RPM` without
checking `reading.OK` (canonical case: any future code that pattern-matches
the existing `b.Read(ch)` calls in `internal/calibrate/calibrate.go:638,897,1334`)
gets a Reading where RPM is real but OK is false because PWM was unavailable.

Calibrate's existing usage at calibrate.go:1334 (`r, _ := b.Read(ch)`; then
`if !r.OK { continue }`) correctly skips. But the contract is murky:
RULE-HAL-002 only specifies "Read never mutates observable state". There's
no explicit rule on what OK means when only one of {PWM, RPM} is readable.
The safer contract is "OK == true iff every populated field is valid"; the
current code returns OK=false but still populates RPM. Either tighten the
contract (zero RPM when OK=false) or document the half-populated state
explicitly so consumers know to gate on OK before reading any field.

## Medium findings (robustness issue, dead branch)

### M1 — Negative RPM clamp at `backend.go:163-165` silently rewrites a driver sentinel into a passing value.

**File:** `internal/hal/hwmon/backend.go:163-165`

After `ReadRPMPath`, the code does `if rpm < 0 { rpm = 0 }`. The IsSentinelRPM
check then runs on the clamped value. A driver returning −1 (a common
"reading unavailable" signal on some embedded hwmon drivers — kernel
hwmon-sysfs docs note there's no canonical convention) becomes 0, which
passes IsSentinelRPM (`0 != 65535 && 0 <= 25000`). The backend then reports
`reading.RPM = 0` with `OK = true`. A downstream curve / calibration treats
this as "fan stopped" rather than "reading invalid".

Calibrate's separate path (`internal/calibrate/calibrate.go:1329`) reads via
`readSysfsInt` directly and passes the raw value (including −1) to
`halhwmon.IsSentinelRPM`, which returns false for −1. So calibrate would also
accept −1 as legit. The two paths AGREE in their (lenient) behaviour, but
neither rejects a negative driver-error signal. Whether this matters depends
on which drivers in the wild use negative values for "unavailable" — the
sentinel filter only catches the 0xFFFF / >25k cases.

Suggested action: either reject `rpm < 0` as an invalid reading (set OK=false),
or document explicitly that negative reads are treated as zero. Current behaviour
is undefined and inconsistent with how the package treats other sentinels.

### M2 — `ensureManualMode` reads `b.writePWMEnable` and `b.writePWMEnablePath` without synchronisation; concurrent first-time writes to different paths see a partial seam swap.

**File:** `internal/hal/hwmon/backend.go:341-348`

The Backend has no mutex protecting reads of `b.writePWMEnable` /
`b.writePWMEnablePath`. In production these are nil and the code falls
through to `hwmon.WritePWMEnable`. In tests they're set once at
construction via `NewBackendForTest`. The test seam is set BEFORE any
goroutine could read it (testing's single-thread setup), so this is
benign in practice.

But the `b.acquired` sync.Map IS designed to be touched by multiple
goroutines (one Backend instance is shared across all hwmon channels —
see `cmd/ventd/calresolver.go:60` register-once pattern). The Backend's
struct only holds `logger`, `acquired`, and the three test-injectable
function fields. The function fields are read-only after construction;
sync.Map handles the read/write race itself. So in practice this is fine
— but the data-race-free property depends on "the test seams are never
mutated post-construction", which isn't documented as a rule.

Suggested action: comment block explaining that `writePWMEnable*` /
`writeDutyFn` are read-only after `NewBackend*` returns; or formalise it
by making them constructor-only (no exported setter). Currently relies on
"tests don't do this" by convention.

### M3 — `Backend.Write`'s `acquired` cache is not atomically tied to the chip's actual `pwm_enable` state across multiple `Backend` instances sharing the same channel.

**File:** `internal/hal/hwmon/backend.go:57-59`, `382-384`

The `b.acquired` sync.Map is per-Backend-instance. Multiple Backends can
exist (controller + watchdog construct separate ones — see
`internal/controller/controller.go:294` and `internal/watchdog/watchdog.go:93`).
Each Backend tracks its OWN view of "have we set pwm_enable=1 yet" without
coordinating with the others.

In practice this is benign because:
- Watchdog only calls `Restore`, never `Write` — so it never populates `acquired`.
- The controller is the sole `Write` caller per channel.

But the contract bound to RULE-HAL-008 says "Writing to an already-acquired
channel is a no-op or clean error". If a hypothetical future call path
constructed a second Backend with same logger and called `Write` on the
same channel, the second Backend would re-issue `pwm_enable=1` because its
sync.Map is empty. RULE-HAL-008 would be violated structurally — though
no current call site triggers it.

Suggested action: either document the "one Backend per process" assumption
(it's effectively enforced by the watchdog/controller layering today), or
move `acquired` to a package-level shared sync.Map keyed by pwm_path.

### M4 — `restoreRPMTarget` reads `hwmon.ReadFanMaxRPM` (line 295) on every Restore call rather than using `st.MaxRPM` cache from `State`.

**File:** `internal/hal/hwmon/backend.go:293-296`

The `State` struct comment at backend.go:42-46 says "Opt-4: the controller
reads fan*_max once at startup and embeds it here so Write can skip the
per-tick sysfs round-trip". `writeDuty` (line 240-243) honours this and
falls back to `ReadFanMaxRPM` only when `st.MaxRPM <= 0`.

But `restoreRPMTarget` (line 295) unconditionally calls `ReadFanMaxRPM`
ignoring `st.MaxRPM`. Restore runs once per channel on daemon exit, so the
cost is bounded (N sysfs reads, not N-per-tick). Functional correctness
is unaffected. The inconsistency is a minor code-quality issue: a future
refactor that drops the fan*_max file would break Restore on RPM-target
channels even though the cache held a valid value.

Suggested action: prefer `st.MaxRPM` with `ReadFanMaxRPM` fallback to match
the `writeDuty` pattern. Single-line change.

## Low findings (style)

### L1 — Comment at `backend.go:137-139` ("Temperature is left zero — hwmon temp* files are exposed as sensors") is correct but the Reading struct has a `.Temp` field that's structurally unused for hwmon. A grep `reading\.Temp` in `internal/hal/hwmon/` returns zero hits, confirming the doc.

### L2 — `stateFrom` (backend.go:389-401) accepts both `State` and `*State` shapes. The error path for `*State{nil}` returns a plain `errors.New` while the type-mismatch path uses `fmt.Errorf` — minor inconsistency in error wrapping. Both paths are tested in `backend_test.go`; the inconsistency is invisible to callers.

### L3 — `rpmPathFromPWM` at backend.go:406-410 duplicates the private `rpmPath` helper in `internal/hwmon/hwmon.go:142`. The comment acknowledges this ("reimplemented here to avoid widening the hwmon package API"). Minor: a `hwmon.RPMPathFromPWM` exported helper would deduplicate without widening any other surface.

### L4 — `sentinel.go:124` switch reads `val >= PlausibleTempMaxCelsius || val <= PlausibleTempMinCelsius` with no else; the function returns false for any non-{temp,in,fan} prefix. A `power` file (hwmon power* sysfs) gets no sentinel check at all. In practice this is unreachable from current callers (Reading.Temp isn't populated from power* paths), but the silent fall-through is worth a comment if power monitoring lands later.

## Verified-correct (invariants confirmed)

- **RULE-HWMON-MODE-REACQUIRE single-retry contract** (backend.go:212-225): the EBUSY path checks `errors.Is(writeErr, syscall.EBUSY)`, deletes the `acquired` cache, re-runs `ensureManualMode`, retries `writeDuty` once. A second EBUSY surfaces verbatim to the caller. No goroutine spawn, no loop. Bound subtests cited in the rule both exist in `internal/hal/hwmon/backend_test.go`.

- **RULE-HAL-008 (write-to-acquired is no-op)** (backend.go:337-340): `ensureManualMode` short-circuits when `b.acquired.Load(st.PWMPath)` returns ok=true. The acquired flag is set ONLY after a successful write (or documented ENOENT) at backend.go:357/366. A failed write leaves the flag unset so the next call retries the mode write rather than masking.

- **Sentinel filter coverage across read paths**: every Backend Read path that emits a Reading goes through `IsSentinelRPM` (backend.go:168). Every downstream RPM-emitting path (calibrate.go:1329, server.go:988, monitor.go:201 via mirror) gates similarly. Every temperature serialization point (controller.go:759, server.go:951, monitor.go:201 via mirror) gates via `IsSentinelSensorVal` or its monitor-side mirror — H1 is the one asymmetry.

- **RULE-HAL-001 (Enumerate idempotent)** (backend.go:101-135): pure read of `/sys/class/hwmon` via `hwmon.EnumerateDevices`. No mutation, no state retained between calls beyond what's pulled fresh from sysfs. Deterministic iteration order via the `internal/hwmon` enumeration helper.

- **RULE-HAL-002 (Read never mutates)** (backend.go:140-179): only file-read syscalls (`ReadPWM`, `ReadRPMPath`). No `pwm_enable` write, no PWM write.

- **RULE-HAL-003 (Write faithful)** (backend.go:232-248): no clamping in the backend; pwm byte forwarded as-is to `hwmon.WritePWM`. RPM-target conversion is the documented divisor; not a "remap".

- **RULE-HAL-004 (Restore safe on never-opened)** (backend.go:267-291): `OrigEnable == -1` → fallback to `WritePWM(path, 255)`. No panic on un-opened channel; `stateFrom` returns a clean error on bad Opaque shape rather than panicking.

- **RULE-HAL-005 (Caps stable)** (backend.go:108-132): caps derived from sysfs file class (`dev.PWM` vs `dev.RPMTargets`); deterministic per chip topology snapshot.

- **RULE-HAL-007 (Close idempotent)** (backend.go:90): no-op `return nil`, called any number of times.

- **Negative-RPM clamp** (backend.go:163-165): defensive but see M1 above for the sentinel-bypass concern.

- **`ensureManualMode` rate-limiting on failure**: backend.go:357 stores `acquired` only on success or documented ENOENT. A failed `WritePWMEnable` does NOT set the flag, so the next tick retries rather than silently masking. Recovery from transient EPERM / EBUSY mode-writes is built in.

- **Sentinel constants R28 raise** (sentinel.go:24-41): `PlausibleRPMMax = 25000`, matching the 2026-05-03 audit text; `SentinelRPMRaw = 65535` exact match for 0xFFFF; `PlausibleTempMinCelsius = -273.15` absolute-zero floor present.

- **Sysfs path resolution at startup, not lazy** (verified via `rebind_trigger.go:25-43`): a topology-change event for a configured device triggers a full daemon restart via `restartCh`, which re-runs `ResolveHwmonPaths` from main.go. The Backend's State struct holds resolved paths from the start; there's no per-tick re-resolution. The "stale path across kernel module reload" risk surfaced in the audit scope is closed by this restart-on-rebind pattern. The Backend itself doesn't cache hwmonN numbers — it stores the resolved sysfs path string and lets the kernel-side rebind trigger handle reconciliation.

## Files NOT audited and why

- `backend_test.go` (7109 bytes) — test file, out of scope for "non-test .go" sweep.
- `safety_test.go` (9772 bytes) — test file, ditto.
- `export_test.go` (1817 bytes) — test seam file; out of scope per task ("non-test .go" excludes `_test.go` files).
- `internal/hwmon/*.go` — the low-level sysfs helpers consumed by this package. Out of scope ("internal/hal/hwmon/" only) but worth a separate pass if drift suspected. Spot-checked `hwmon.go` for the `ReadValue` / `ReadRPMPath` / `WritePWMEnable` shapes referenced from this package; the empty-file handling (`strings.TrimSpace` then `ParseInt`) returns a parse error rather than zero, which `Backend.Read` then treats as a failed read (OK=false). No sentinel-leak concern from empty / trailing-newline input.
- `internal/monitor/monitor.go` — out of scope ("internal/hal/hwmon/" only) but flagged in H1 for the cross-boundary inconsistency the audit scope explicitly named.
- `internal/setup/restore_excluded.go` — owns the pwm_enable enum probe / cache / fallback chain (RULE-HWMON-ENABLE-EINVAL-FALLBACK). Out of scope for this pass; the rule text confirms the wizard-side probe is the canonical home, not the controller-hot-path Backend.

---

## Summary

**Finding count:** 10 (1 high, 4 medium, 4 low + 1 deferred-style)
**Severity breakdown:** 0 critical / 2 high / 4 medium / 4 low

**Most important finding (1 sentence):** `monitor.isSentinelMonitorVal` only enforces the high temperature cap but not the absolute-zero floor that `IsSentinelSensorVal` rejects, re-opening the very serialization-boundary leak class that RULE-HWMON-SENTINEL-STATUS-BOUNDARY exists to prevent.
