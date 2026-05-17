# HAL thinkpad backend rules — /proc/acpi/ibm/fan procfs surface

These invariants govern `internal/hal/thinkpad/`, the HAL backend that
drives Lenovo ThinkPad fans via the kernel's `thinkpad_acpi`
procfs interface. The matching driver profile is
`internal/hwdb/catalog/drivers/thinkpad_acpi.yaml` (capability
`rw_proc`, pwm_unit `thinkpad_level`, pwm_unit_max 7, exit_behaviour
`restore_auto`); the matching board catalog is
`internal/hwdb/catalog/boards/lenovo-thinkpad.yaml`.

The backend is wired into the HAL registry at daemon start by
`cmd/ventd/calresolver.go::registerHALBackends`. Writes proceed
unconditionally once `Enumerate` returns a channel — there is no
per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair posture
(see `feedback-dont-default-writes-off`). Safety is enforced by:

- the kernel's EPERM-on-`fan_control=0` gate, which the backend
  wraps as `ErrFanControlDisabled` so the existing
  `RULE-WIZARD-RECOVERY-10` classifier + the
  `RULE-MODPROBE-OPTIONS-01` modprobe-options-write endpoint flip
  the modparam on the operator's first remediation click;
- the closed firmware level grid `[0, 7]` plus the named pseudo-
  levels `auto` / `disengaged` / `full-speed` — the kernel refuses
  every other input, so a corrupted PWM byte cannot escape into a
  rogue register write;
- the watchdog's `Restore`-on-exit (`RULE-WD-RESTORE-EXIT`)
  writing `"level auto"` on every documented shutdown path;
- `RULE-IDLE-02` (battery refusal) + `RULE-IDLE-03` (container
  refusal) closing the daemon before any write fires on a host
  where writes would be unsafe.

Each rule binds 1:1 to a subtest. `tools/rulelint` blocks the merge
if a rule lacks its bound test.

## RULE-HAL-THINKPAD-01: pwmToLevel quantises every uint8 input into the closed firmware grid [0, FirmwareLevelMax].

The round-half-up integer-arithmetic form
`(pwm * 7 + 127) / 255` places the band boundaries at
`pwm ∈ {18, 54, 90, 127, 163, 199, 235}` — symmetric around level
midpoints `{0, 36, 73, 109, 146, 182, 219, 255}`. Out-of-range
inputs cannot occur (uint8 already clamps); defensive clamps on
both ends protect against a future refactor that widens the input
type. Exhaustive sweep over all 256 input values asserts the
result is always in `[0, 7]`.

Bound: internal/hal/thinkpad/backend_test.go:TestPWMToLevel_BoundaryValues
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_QuantisesEveryPWMByteToValidLevel

## RULE-HAL-THINKPAD-02: levelToPWM is the inverse of pwmToLevel — each level's reported PWM re-quantises to the same level (no band drift).

The integer form `(level * 255 + 3) / 7` centres each level's
reported PWM in the middle of its quantisation band. A round-trip
`pwmToLevel(levelToPWM(N)) == N` for every `N` in `[0, 7]` is the
load-bearing invariant for closed-loop control: the controller
writes a PWM, reads back via `Read`, and a stable round-trip
means the read doesn't artificially flip the level on the next
tick. The pseudo-levels `auto` and `disengaged` / `full-speed`
map to fixed PWM sentinels (128 and 255 respectively) — those
don't round-trip through `pwmToLevel` and are tested via
`parseProcFan` directly.

Bound: internal/hal/thinkpad/backend_test.go:TestLevelToPWM_RoundTripsCentredBands
Bound: internal/hal/thinkpad/backend_test.go:TestLevelToPWM_OutOfRangeClampsToMax

## RULE-HAL-THINKPAD-03: parseProcFan extracts level + speed; missing-level / out-of-range level / non-numeric-non-keyword level all wrap ErrInvalidProcFanResponse.

The kernel emits a multi-line `key:\t<value>` response; the parser
is whitespace-tolerant and case-sensitive on keys ("level",
"speed"). Three named pseudo-levels are recognised: `auto` maps
PWM to the 128 midpoint sentinel (so a controller that reads
before any Write sees a plausible baseline); `disengaged` and
`full-speed` map PWM to 255 (firmware emergency cooling).
Anything else — numeric out-of-range, unknown keyword, missing
level line — wraps `ErrInvalidProcFanResponse` so `Backend.Read`
can report `Reading.OK=false` and the controller skips the tick
without daemon failure. The four negative cases (missing-level /
out-of-range / non-numeric / missing-from-minimal-kernel-output)
are pinned distinctly so a future regex regression that
silently widens acceptance fails CI.

Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_NumericLevel
Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_AutoLevelMapsToMidpoint
Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_DisengagedMapsToMax
Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_MissingLevelLineReturnsError
Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_LevelOutOfRangeReturnsError
Bound: internal/hal/thinkpad/backend_test.go:TestParseProcFan_NonNumericNonKeywordLevelReturnsError

## RULE-HAL-THINKPAD-04: Backend.Read enforces the empty-by-construction Reading invariant — OK=false on missing or malformed file zeroes every other field.

`hal.Reading` carries an OK=false → fields zero invariant
(documented in the hal package). The thinkpad Read path enforces
this via the `Reading{OK: false}` zero-value return on every
failure path: missing file (procfs disappeared between Enumerate
and Read), unparseable content, out-of-range level. Callers that
ignore OK and read a partial-populated Reading cannot see stale
PWM / RPM data from a previous tick.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Read_FileMissingReportsOKFalse
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Read_MalformedFileReportsOKFalse

## RULE-HAL-THINKPAD-05: Backend.Read never mutates the procfs file (RULE-HAL-002).

Read uses only `os.ReadFile`. A regression that introduced a
`WriteFile` call on the same path during Read would silently
inject a level command on every tick; the file content
before / after assertion catches it.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Read_NeverMutatesFile

## RULE-HAL-THINKPAD-06: Backend.Write issues "enable\n" exactly once per ProcPath (across all Writes) and the level command is "level N\n" where N = pwmToLevel(pwm).

The kernel's `thinkpad_acpi` driver accepts `"level N"` directly
on every modern build, but 5.x-era kernels (Debian-stable / Ubuntu
LTS as of v0.6.1) gate the first level write behind a prior
`"enable"` command. The Backend tracks per-ProcPath acquisition
in a `sync.Map`; the first Write issues "enable\n" then the level
command, subsequent Writes skip the enable. Enable failures are
logged at DEBUG and not propagated — the subsequent level write
surfaces the canonical error if the gate really is closed.

The level command is byte-exact: `"level "` + decimal level + `"\n"`.
The exhaustive 256-value Write sweep asserts every emitted command
parses cleanly back as a level in `[0, FirmwareLevelMax]`.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_EmitsEnableThenLevel
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_SecondWriteSkipsEnable

## RULE-HAL-THINKPAD-07: Backend.Write wraps EPERM as ErrFanControlDisabled so the wizard / doctor surface dispatches the existing modprobe-options-write remediation.

The kernel's `thinkpad_acpi.c` returns `-EPERM` silently when
`fan_control=0` — no dmesg breadcrumb, no syslog. The wrapped
sentinel is the only runtime signal the wizard recovery
classifier (RULE-WIZARD-RECOVERY-10) and the doctor can branch
on without string-matching the kernel's error format.
`errors.Is(err, ErrFanControlDisabled)` and
`errors.Is(err, fs.ErrPermission)` both succeed on the wrapped
chain so existing permission-denied classifiers continue to
match.

The bound subtest uses a `chmod 0400`'d temp file to faithfully
reproduce EPERM in userspace; it skips when running as root since
the DAC mode check bypasses for euid 0.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_EPERMWrapsAsErrFanControlDisabled

## RULE-HAL-THINKPAD-08: Backend.Restore writes "level auto" first; on EPERM falls back to "disable". Acquired flag is cleared on success.

The driver profile's `exit_behaviour: restore_auto` is the
canonical exit contract for thinkpad_acpi. `"level auto"` is
the literal procfs encoding. On the rare case where `"level
auto"` itself returns EPERM (operator clobbered fan_control
between Write and Restore, or kernel update changed the gate
mid-lifetime), Restore falls back to `"disable\n"` — the
kernel command that hands the fan back to the firmware curve
without touching the manual-mode gate. If both fail, the
original error is surfaced so the watchdog logs the channel as
un-restored and continues with the remaining channels per
`RULE-WD-RESTORE-PANIC`.

On successful restore the acquired flag for that ProcPath is
cleared so a subsequent SIGHUP / config reload re-issues
`"enable"` on the first Write, preserving the byte-exact write
sequence operators expect.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Restore_WritesLevelAuto
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Restore_SafeOnUnwrittenChannel
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Restore_ClearsAcquiredFlag

## RULE-HAL-THINKPAD-09: Enumerate returns 0 or 1 channels (procfs is single-instance); idempotent (RULE-HAL-001); ctx cancellation is observed.

The thinkpad_acpi procfs surface exposes exactly one fan node
regardless of how many physical fans are populated (dual-fan
models share pwm1 — fan2 is tach-only and is not represented as
a controllable channel, see the lenovo-thinkpad.yaml catalog
notes). Enumerate stats `/proc/acpi/ibm/fan` and returns a
one-element slice on success, nil-empty on absence. Two
successive calls return the same set (RULE-HAL-001). A
pre-cancelled context short-circuits before any syscall.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Enumerate_AbsentProcfsReturnsEmpty
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Enumerate_Idempotent
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Enumerate_RespectsContextCancel

## RULE-HAL-THINKPAD-10: Backend.Close is idempotent (RULE-HAL-007); Backend.Name returns the stable "thinkpad" registry tag.

Close is a no-op (the backend holds no process-level resources);
double-close must not panic and must return nil both times.
`Name()` and the `BackendName` constant must both return the
literal string `"thinkpad"` — the registry tag is consumed by
`hal.Resolve` to map a `Channel.ID` like
`"thinkpad:/proc/acpi/ibm/fan"` back to this backend. A
regression that changes the tag would silently break every
config that references a thinkpad channel.

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Close_Idempotent
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Name_StableConstant

## RULE-HAL-THINKPAD-11: stateFrom refuses Channel.Opaque values that are not State / *State or that carry an empty ProcPath.

The Backend's Read / Write / Restore all start by coercing
`Channel.Opaque` to `thinkpad.State`. A wrong opaque type (a
test passing `int`, a future refactor passing a different
backend's State) or an empty ProcPath (catalogue bug, channel
constructed without a path) is refused with a typed error
before any syscall. This protects against silently writing to
the empty string path (which `os.WriteFile` would reject anyway,
but the typed early-return makes the diagnostic actionable).

Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_RejectsWrongOpaqueType
Bound: internal/hal/thinkpad/backend_test.go:TestBackend_Write_RejectsEmptyProcPath
