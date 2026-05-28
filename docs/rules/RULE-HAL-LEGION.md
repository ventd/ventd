# HAL legion backend rules — platform_profile + powermode state-switcher

These invariants govern `internal/hal/legion/`, the HAL backend that
drives Lenovo Legion gaming-laptop fans via the legion_laptop kernel
module's control surface (johnfanv2/LenovoLegionLinux OOT DKMS).

Spec linkage: `specs/spec-17-vendor-ec-absorption.md` PR-1. The matching
driver profile is `internal/hwdb/catalog/drivers/legion_hwmon.yaml`; the
matching board catalog is `internal/hwdb/catalog/boards/lenovo-legion.yaml`.

The backend is the state-switcher path — per-tick PWM bytes are
bucketed into one of three platform_profile states (quiet / balanced /
performance). When the legion_laptop powermode node is present, the
matching powermode integer is written alongside. The curve-upload
(debugfs `/sys/kernel/debug/legion/fancurve`) is a separate spec-17
PR-1b feature via the new `hal.CurveSink` interface and is NOT covered
by this rule family.

The backend is wired into the HAL registry at daemon start by
`cmd/ventd/calresolver.go::registerHALBackends`. Writes proceed
unconditionally once `Enumerate` returns a channel — there is no
per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair / thinkpad
posture (see `feedback-dont-default-writes-off`). Safety is enforced by:

- the closed `platform_profile_choices` enum (the kernel refuses any
  write whose value isn't in the catalogue);
- the watchdog's `Restore`-on-exit (`RULE-WD-RESTORE-EXIT`) writing
  "balanced" on every documented shutdown path;
- `RULE-IDLE-02` (battery refusal) + `RULE-IDLE-03` (container refusal)
  closing the daemon before any write fires on a host where writes
  would be unsafe.

Each rule binds 1:1 to a subtest. `tools/rulelint` blocks the merge if
a rule lacks its bound test.

## RULE-HAL-LEGION-01: pwmToProfile buckets every uint8 input into one of {quiet, balanced, performance} at thresholds 84 / 170.

Bucket boundaries: PWM 0..84 → "quiet"; PWM 85..170 → "balanced";
PWM 171..255 → "performance". The thresholds are exposed as
`ThresholdQuiet` (84) and `ThresholdBalanced` (170) so future operator
tuning has a slot. Pure function; no I/O.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_01_PWMToProfileBucketBoundaries

## RULE-HAL-LEGION-02: profileToPWM is the inverse of pwmToProfile; each profile reports the centre of its band so write→read→compare is stable.

The integer form returns 42 / 127 / 213 for quiet / balanced /
performance — centred in each band so a controller writing a PWM in the
middle of any band and reading it back gets the same PWM (mod the
state quantisation). Unknown profiles return `(0, false)`.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_02_ProfileToPWMRoundTrip
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_02_ProfileToPWMUnknownReturnsFalse

## RULE-HAL-LEGION-03: Enumerate returns a single channel iff platform_profile + choices (≥2) AND /sys/module/legion_laptop are all present; absent / degenerate states return empty.

Discovery is exclusive — a host without `/sys/firmware/acpi/platform_profile`,
without a `platform_profile_choices` set of ≥ 2 values, or without
`/sys/module/legion_laptop` (the positive signal that legion_laptop is
loaded) returns an empty slice (not an error) so the registry's fan-out
Enumerate admits the absence gracefully. A degenerate
`platform_profile_choices` with < 2 values is also returned as empty —
surfacing a channel would promise control the hardware can't deliver
(mirrors `RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP`'s quiet branch).

The `legion_laptop` positive gate is the discovery boundary: the
platform_profile sysfs surface is kernel-generic and is also exposed on
Dell, HP, ASUS, Framework, and other non-Lenovo hosts via their own
vendor WMI/ACPI drivers. Without the module gate the backend used to
enumerate on any of those hosts and surface a phantom "Legion Fan" the
controller would then refuse to drive — confusing the wizard's "fans
found" tally with no actual control surface (#1410). Legion-specific
writes (powermode, debugfs fancurve) are reachable only when
legion_laptop is loaded, so the module presence is the right gate.

The powermode path is optional and is included in the channel state
only when present.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_03_EnumerateHappyPath
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_03_EnumerateAbsentReturnsEmpty
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_03_EnumerateNoPowermode
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_03_EnumerateSingleChoiceRefuses
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_03_EnumerateNoLegionModuleReturnsEmpty

## RULE-HAL-LEGION-04: Read enforces the empty-by-construction Reading invariant — OK=false on missing / malformed file zeroes every other field.

`hal.Reading` carries an OK=false → fields zero invariant. The legion
Read path enforces this via the `Reading{OK: false}` zero-value return
on every failure path: missing file (sysfs disappeared between
Enumerate and Read), unrecognised profile string (kernel exposes a new
state we don't model yet). Callers that ignore OK and read a
partial-populated Reading cannot see stale state from a previous tick.
The happy-path round-trip is the symmetric guard.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_04_ReadMissingFileReportsOKFalse
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_04_ReadUnknownProfileReportsOKFalse
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_04_ReadHappyPath

## RULE-HAL-LEGION-05: Read never mutates the platform_profile sysfs file (RULE-HAL-002).

Read uses only `readFile` (defaults to `os.ReadFile`). A regression
that introduced a write call on the same path during Read would
silently inject a state change on every tick; the file-content-
before-after assertion catches it.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_05_ReadNeverMutatesFile

## RULE-HAL-LEGION-06: Write dispatches both platform_profile and powermode when both nodes are present; powermode-absent host writes platform_profile only.

The two sysfs nodes are written in sequence: platform_profile first
(load-bearing — this is the universally-honoured ACPI surface),
powermode second (legion_laptop-specific). powermode failure is
non-fatal — platform_profile already committed the state change, and
the next write attempt re-tries the powermode side. When the
powermode path is empty (older legion_laptop builds, or non-Legion
hosts using the same backend), only platform_profile is touched.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_06_WriteDispatchesBothNodes
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_06_WriteSkipsPowermodeWhenAbsent

## RULE-HAL-LEGION-07: Write exhaustive — every uint8 input maps to one of the three valid profile strings.

A regression in `pwmToProfile`'s bucket math that emitted an empty
string or an unrecognised state for some boundary value would silently
produce an EINVAL at the kernel; the exhaustive 256-value sweep
catches it before CI ships.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_07_WriteEveryPWMByteProducesValidProfile

## RULE-HAL-LEGION-08: Write clamps to the platform_profile_choices set; a target not in choices falls back to "balanced" rather than surfacing EINVAL.

When a host's `platform_profile_choices` lacks one of the canonical
three states (some HP / Dell laptops expose only quiet + balanced),
the Write call's natural target ("performance" for a high-PWM tick)
must clamp to the closest available state ("balanced") rather than
attempt a kernel write that would fail with EINVAL. This preserves
"writes always succeed once Enumerate returned a channel" — a
contract every other HAL backend honours and downstream code expects.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_08_WriteClampsToChoicesWhenTargetUnavailable

## RULE-HAL-LEGION-09: Write EPERM wraps as ErrPlatformProfileRefused so downstream classification can branch via errors.Is.

The kernel returns EPERM when ACPI policy blocks the platform_profile
write (rare, but possible on hardened distros). The wrapped sentinel
is the only runtime signal the wizard / doctor can branch on without
string matching. Double-%w preserves the underlying syscall chain so
existing `fs.ErrPermission` classifiers still match. Unlike thinkpad,
there's no equivalent of the modprobe-options-write auto-fix for this
surface — recovery is operator-visible only.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_09_WriteEPERMWrapsAsRefused

## RULE-HAL-LEGION-10: Restore writes "balanced" to platform_profile and "1" to powermode (when present); the acquired flag is cleared on success.

The driver profile's `exit_behaviour` is `restore_auto`; for Legion
this is "balanced" rather than a literal "auto" state because the
platform_profile API has no auto keyword — the firmware curve is what
runs when platform_profile is balanced and no other override is
active. powermode "1" mirrors balanced. The acquired-flag clear lets
a subsequent SIGHUP / config reload start fresh.

Restore on an unwritten channel is a clean no-op (writing balanced to
a channel that was never written is safe — `RULE-HAL-004`).

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_10_RestoreWritesBalanced
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_10_RestoreSafeOnUnwrittenChannel

## RULE-HAL-LEGION-11: Close is idempotent (RULE-HAL-007); Name returns the stable "legion" registry tag.

Close is a no-op (the backend holds no process-level resources);
double-close must not panic and must return nil both times. `Name()`
and the `BackendName` constant must both return the literal string
"legion" — the registry tag is consumed by `hal.Resolve` to map a
`Channel.ID` like `"legion:/sys/firmware/acpi/platform_profile"` back
to this backend. A regression that changes the tag would silently
break every config that references a legion channel.

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_11_CloseIdempotent
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_11_NameStableConstant

## RULE-HAL-LEGION-12: stateFrom refuses Channel.Opaque values that are not State / *State or that carry an empty PlatformProfilePath.

The Backend's Read / Write / Restore all start by coercing
`Channel.Opaque` to `legion.State`. A wrong opaque type (a test
passing `int`, a future refactor passing a different backend's
State) or an empty PlatformProfilePath (catalogue bug, channel
constructed without a path) is refused with a typed error before any
syscall. This protects against silently writing to the empty-string
path (which `os.WriteFile` would reject anyway, but the typed
early-return makes the diagnostic actionable).

Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_12_StateFromRejectsBadOpaque
Bound: internal/hal/legion/backend_test.go:TestRULE_HAL_LEGION_12_StateFromAcceptsValueAndPointer
