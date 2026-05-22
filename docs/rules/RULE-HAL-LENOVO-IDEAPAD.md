# HAL lenovoideapad backend rules — platform_profile state-switcher for IdeaPad-class hosts

These invariants govern `internal/hal/lenovoideapad/`, the HAL backend
that drives Lenovo IdeaPad-class laptops via the standard ACPI
`platform_profile` sysfs surface exposed by the `ideapad_laptop` kernel
module.

The backend is the state-switcher path — per-tick PWM bytes are bucketed
into one of three platform_profile states (`low-power` / `balanced` /
`performance`) using the same thresholds Legion uses (84 / 170). IdeaPads
expose no powermode or fancurve secondaries — the platform_profile node
is the only writable fan-control surface available to userspace on this
firmware family (live-tested on IdeaPad Flex 5 14ITL05 / 82HS / BIOS
FXCN28WW; full survey in `/home/phoenix/ventd-7280-fan-rev/lenovo-ideapad-flex-5-driver-notes.md`).

Discovery is exclusive: a host must have `ideapad_laptop` loaded AND must
NOT have `legion_laptop` loaded for this backend to enumerate a channel.
This prevents both the legion and lenovoideapad backends from enumerating
the same platform_profile path on hybrid hosts.

The backend is wired into the HAL registry at daemon start by
`cmd/ventd/calresolver.go::registerHALBackends`. Writes proceed
unconditionally once `Enumerate` returns a channel — there is no
per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair / thinkpad /
legion posture (see `feedback-dont-default-writes-off`). Safety is
enforced by:

- the closed `platform_profile_choices` enum (the kernel refuses any
  write whose value isn't in the catalogue);
- the watchdog's `Restore`-on-exit (`RULE-WD-RESTORE-EXIT`) writing
  "balanced" on every documented shutdown path;
- `RULE-IDLE-02` (battery refusal) + `RULE-IDLE-03` (container refusal)
  closing the daemon before any write fires on a host where writes
  would be unsafe.

Each rule binds 1:1 to a subtest. `tools/rulelint` blocks the merge if
a rule lacks its bound test.

## RULE-HAL-LENOVO-IDEAPAD-01: pwmToProfile buckets every uint8 input into one of {low-power, balanced, performance} at thresholds 84 / 170.

Bucket boundaries: PWM 0..84 → "low-power"; PWM 85..170 → "balanced";
PWM 171..255 → "performance". The thresholds are exposed as
`ThresholdLowPower` (84) and `ThresholdBalanced` (170) so future operator
tuning has a slot. Pure function; no I/O. The thresholds match
`internal/hal/legion` deliberately — IdeaPad and Legion share the same
underlying 3-state ACPI surface; the only difference is the lowest
state's name (`low-power` vs `quiet`).

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_01_PWMToProfileBucketBoundaries

## RULE-HAL-LENOVO-IDEAPAD-02: profileToPWM is the inverse of pwmToProfile; each profile reports the centre of its band so write→read→compare is stable.

The integer form returns 42 / 127 / 213 for low-power / balanced /
performance — centred in each band so a controller writing a PWM in the
middle of any band and reading it back gets the same PWM (mod the state
quantisation). Unknown profiles return `(0, false)`. Notably "quiet"
(legion's name) returns false here — the two backends do not share
profile vocabularies even though they share thresholds.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_02_ProfileToPWMRoundTrip
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_02_ProfileToPWMUnknownReturnsFalse

## RULE-HAL-LENOVO-IDEAPAD-03: Enumerate returns a single channel iff platform_profile + choices (≥2) + ideapad_laptop module are present AND legion_laptop module is absent.

Discovery is exclusive — a host without ideapad_laptop loaded, or with
legion_laptop also loaded, returns an empty slice. Hosts without
platform_profile return empty too (kernel doesn't expose the surface).
A degenerate `platform_profile_choices` with < 2 values is also returned
as empty — surfacing a channel would promise control the hardware can't
deliver (mirrors `RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP`'s quiet branch
and the matching legion gate).

The legion-exclusion check is defence-in-depth: legion's own backend
already enumerates platform_profile without DMI gating, so without the
check both backends would expose the same path on hybrid hosts. In
practice no shipping Lenovo loads both modules at once, but the guard
makes the discovery contract self-documenting.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateHappyPath
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateAbsentPlatformProfileReturnsEmpty
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateNoIdeapadModuleReturnsEmpty
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateLegionPresentReturnsEmpty
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_03_EnumerateSingleChoiceRefuses

## RULE-HAL-LENOVO-IDEAPAD-04: Read enforces the empty-by-construction Reading invariant — OK=false on missing / malformed file zeroes every other field, and OK=true paths leave RPM at 0 (no RPM source available on IdeaPad).

`hal.Reading` carries an OK=false → fields zero invariant. The
lenovoideapad Read path enforces this via the `Reading{OK: false}`
zero-value return on every failure path: missing file (sysfs disappeared
between Enumerate and Read), unrecognised profile string (kernel exposes
a new state we don't model yet). Callers that ignore OK and read a
partial-populated Reading cannot see stale state from a previous tick.

RPM telemetry is not available on IdeaPad hosts — the modern kernel does
not expose `ec_sys` (removed upstream as unsafe), and there is no ACPI
FRSP equivalent in the IdeaPad DSDT. Reading.RPM stays 0 on the
happy-path return; the controller treats this as "no telemetry" rather
than "fan stopped". The happy-path subtest asserts this explicitly so a
future regression that fabricates a fake RPM (or mis-reads from a stale
buffer) is caught at CI time.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_04_ReadMissingFileReportsOKFalse
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_04_ReadUnknownProfileReportsOKFalse
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_04_ReadHappyPath

## RULE-HAL-LENOVO-IDEAPAD-05: Read never mutates the platform_profile sysfs file (RULE-HAL-002).

Read uses only `readFile` (defaults to `os.ReadFile`). A regression that
introduced a write call on the same path during Read would silently
inject a state change on every tick; the file-content-before-after
assertion catches it.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_05_ReadNeverMutatesFile

## RULE-HAL-LENOVO-IDEAPAD-06: Write dispatches the bucketed profile string to platform_profile.

The Write path bucketes the PWM byte via pwmToProfile, clamps against
the channel's Choices set, then writes the resulting profile string to
the platform_profile sysfs node. Unlike legion, there is no powermode or
fancurve secondary node — IdeaPads expose only the one writable surface.
The subtest sweeps the canonical low/mid/high PWM values and asserts the
exact bytes written.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_06_WriteWritesProfileString

## RULE-HAL-LENOVO-IDEAPAD-07: Write exhaustive — every uint8 input maps to one of the three valid profile strings.

A regression in `pwmToProfile`'s bucket math that emitted an empty
string or an unrecognised state for some boundary value would silently
produce an EINVAL at the kernel; the exhaustive 256-value sweep catches
it before CI ships.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_07_WriteEveryPWMByteProducesValidProfile

## RULE-HAL-LENOVO-IDEAPAD-08: Write clamps to the platform_profile_choices set; a target not in choices falls back to "balanced" rather than surfacing EINVAL.

When a host's `platform_profile_choices` lacks one of the canonical three
states (some IdeaPad SKUs expose only low-power + balanced), the Write
call's natural target ("performance" for a high-PWM tick) must clamp to
the closest available state ("balanced") rather than attempt a kernel
write that would fail with EINVAL. This preserves "writes always succeed
once Enumerate returned a channel" — a contract every other HAL backend
honours and downstream code expects.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_08_WriteClampsToChoicesWhenTargetUnavailable

## RULE-HAL-LENOVO-IDEAPAD-09: Write EPERM wraps as ErrPlatformProfileRefused so downstream classification can branch via errors.Is.

The kernel returns EPERM when ACPI policy blocks the platform_profile
write (rare, but possible on hardened distros). The wrapped sentinel is
the only runtime signal the wizard / doctor can branch on without string
matching. Double-%w preserves the underlying syscall chain so existing
`fs.ErrPermission` classifiers still match. Unlike thinkpad, there's no
equivalent of the modprobe-options-write auto-fix for this surface —
recovery is operator-visible only.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_09_WriteEPERMWrapsAsRefused

## RULE-HAL-LENOVO-IDEAPAD-10: Restore writes "balanced" to platform_profile; the acquired flag is cleared on success.

The driver profile's `exit_behaviour` is `restore_auto`; for IdeaPad this
is "balanced" rather than a literal "auto" state because the
platform_profile API has no auto keyword — the firmware curve is what
runs when platform_profile is balanced. The acquired-flag clear lets a
subsequent SIGHUP / config reload start fresh.

Restore on an unwritten channel is a clean no-op (writing balanced to a
channel that was never written is safe — `RULE-HAL-004`).

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_10_RestoreWritesBalanced
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_10_RestoreSafeOnUnwrittenChannel

## RULE-HAL-LENOVO-IDEAPAD-11: Close is idempotent (RULE-HAL-007); Name returns the stable "lenovoideapad" registry tag.

Close is a no-op (the backend holds no process-level resources);
double-close must not panic and must return nil both times. `Name()` and
the `BackendName` constant must both return the literal string
"lenovoideapad" — the registry tag is consumed by `hal.Resolve` to map a
`Channel.ID` like `"lenovoideapad:/sys/firmware/acpi/platform_profile"`
back to this backend. A regression that changes the tag would silently
break every config that references a lenovoideapad channel.

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_11_CloseIdempotent
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_11_NameStableConstant

## RULE-HAL-LENOVO-IDEAPAD-12: stateFrom refuses Channel.Opaque values that are not State / *State or that carry an empty PlatformProfilePath.

The Backend's Read / Write / Restore all start by coercing
`Channel.Opaque` to `lenovoideapad.State`. A wrong opaque type (a test
passing `int`, a future refactor passing a different backend's State) or
an empty PlatformProfilePath (catalogue bug, channel constructed without
a path) is refused with a typed error before any syscall. This protects
against silently writing to the empty-string path (which `os.WriteFile`
would reject anyway, but the typed early-return makes the diagnostic
actionable).

Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_12_StateFromRejectsBadOpaque
Bound: internal/hal/lenovoideapad/backend_test.go:TestRULE_HAL_LENOVO_IDEAPAD_12_StateFromAcceptsValueAndPointer
