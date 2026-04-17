# HAL Backend Contract

These invariants must hold for every implementation of `hal.FanBackend`.
They are enforced by `internal/hal/contract_test.go:TestHAL_Contract`.

A backend may satisfy an invariant by returning a clean error rather than
performing the operation -- the controller treats a non-nil error from a
backend operation as a skipped tick, never as a crash. A panic is never
acceptable.

When a backend genuinely cannot exercise an invariant in the current
environment (e.g. NVML unavailable in CI), the contract test calls
`t.Skipf` with a reason rather than forcing a spurious failure. All skips
must be documented here.

## RULE-HAL-001: Enumerate is idempotent

Calling Enumerate twice in quick succession without mutating hardware state
must return the same set of channel IDs in the same order. Hot-plug events
are outside this window; the invariant applies to a stable hardware snapshot.
Code that caches the output of a single Enumerate call must be able to treat
that result as authoritative until the next rescan.

Bound: internal/hal/contract_test.go:enumerate_idempotent

## RULE-HAL-002: Read never mutates observable state

Read must not write to any pwm* file, flip pwm*_enable, or issue any driver
command that changes the channel's observable state. The controller ticks
sensor reads and fan writes in distinct phases; a Read that mutates state
would corrupt the phase separation and break the watchdog's assumption that
reading a channel's current value is always safe.

Note: the NVML backend skips this test when `nvidia.Available()` is false
(no GPU driver present in CI). The invariant itself still applies; the skip
is an environment constraint, not a contract exception.

Bound: internal/hal/contract_test.go:read_no_mutation

## RULE-HAL-003: Write faithfully delivers the requested duty cycle

Write(ch, v) must forward the exact duty-cycle byte v to the underlying
hardware without remapping or silently clamping. The controller owns the
[MinPWM, MaxPWM] clamping step before calling Write; a backend that clamps
again would produce unexpected PWM values and break calibration.

Note: the NVML backend skips this test when `nvidia.Available()` is false.
The file-backed assertion (reading pwm back from sysfs) only applies to
the hwmon backend; NVML's success return is the observable proof for GPU
backends.

Bound: internal/hal/contract_test.go:write_faithful

## RULE-HAL-004: Restore is safe on channels that were never opened

Restore must not panic when called on a channel that Write was never called
for. It must return nil (no-op) or a clean error the watchdog can log and
continue from. Panicking on an un-opened channel would brick the watchdog's
shutdown path when a backend is partially initialised.

The hwmon backend satisfies this by falling back to PWM=255 when
OrigEnable=-1 (state never captured by the watchdog before control was
taken). The NVML backend returns `nvidia.ErrNotAvailable` in environments
without a GPU driver; that is a clean error and satisfies this invariant.

Note: NVML's Restore semantics differ from hwmon: it resets to the driver's
autonomous fan curve (via `nvidia.ResetFanSpeed`) rather than restoring a
specific pwm_enable integer. Both satisfy the letter of this invariant
("no-op or clean error, no panic"). No `CapRestore` variant bit exists
today to distinguish the two restore strategies; a `CapStatefulRestore` bit
would let callers know whether Restore can precisely undo a prior Write.
See FOLLOWUPS in the T-HAL-01 PR for the tracking item.

Bound: internal/hal/contract_test.go:restore_safe_on_unopened

## RULE-HAL-005: Caps are stable across a channel's lifetime

The Caps bitset for a given channel ID must not change between successive
Enumerate calls while the hardware configuration is unchanged. Code that
caches a channel's capabilities after the first Enumerate must not be
surprised by a capability appearing or disappearing mid-session.

Bound: internal/hal/contract_test.go:caps_stable

## RULE-HAL-006: ChannelRole classification is deterministic

Enumerate must return the same Role for a given channel ID on every call
within a stable hardware session. A Role that oscillates between calls would
cause the UI fan-inventory to flicker and would break config-time
classification logic that relies on role stability.

Bound: internal/hal/contract_test.go:role_deterministic

## RULE-HAL-007: Close is idempotent

Calling Close twice must not panic and must return nil on both calls.
The daemon's shutdown sequence (context cancel -> Restore all channels ->
Close all backends) may race with a deferred Close in a helper; double-close
must be a safe no-op.

Bound: internal/hal/contract_test.go:close_idempotent

## RULE-HAL-008: Writing to an already-acquired channel is a no-op or clean error

The first Write to a channel may require a mode transition (pwm_enable=1 for
hwmon) to take manual control. A second Write to the same channel must not
re-issue the mode command. Re-issuing pwm_enable=1 when the file is already
in manual mode is harmless on most drivers, but some firmware interprets the
write as a reset of the auto-curve timer, which can cause an audible speed
spike. The hwmon backend enforces this via a sync.Map; other backends must
use an equivalent mechanism or require no mode transition at all.

Bound: internal/hal/contract_test.go:write_idempotent_open
