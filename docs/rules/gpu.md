# GPU HAL backend rules (spec-03 PR 2d)

These invariants govern the GPU HAL backends in
`internal/hal/gpu/` — NVIDIA via NVML (`internal/hal/gpu/nvml/`,
purego dlopen wrapper around `libnvidia-ml.so.1`), AMD amdgpu
(`internal/hal/gpu/amdgpu/`, sysfs `pwm1` + `gpu_od/fan_ctrl/`),
and Intel xe / i915 (`internal/hal/gpu/xe/` and similar — read-
only by design). The package-level write gates and graceful
degradation rules let ventd run on hosts without GPUs without
crashing or false-failing.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-GPU-PR2D-01: GPU writes are gated only by the per-device capability probe; no opt-in flag.

Default daemon mode dispatches NVIDIA GPU fan writes whenever
the per-device capability probe returns a writable capability
(`rw_full` or `rw_quirk`). The probe is the load-bearing
safety constraint — it gates on real NVIDIA driver-version
requirements (`RULE-POLARITY-06`: R515+ required for
`nvmlDeviceSetFanSpeed_v2`) — not on opt-in policy. A
capability of `ro_sensor_only` (pre-Maxwell hardware or
pre-R515 driver) returns `ErrWriteGated` because the
underlying NVML symbols are absent.

The v0.8.x sweep removed the `--enable-gpu-write` opt-in
flag, matching the precedent set by the v0.6.1 removal of
`--unsafe-corsair-writes` and `--enable-nbfc-write` (see
`RULE-LIQUID-06` + `RULE-NBFC-HAL-DEFAULT-WRITES-ON`). The
closed-set HAL catalogue + capability probe is the safety
mechanism, not an opt-in toggle — consistent with the project
invariant that "new HAL write paths ship enabled by default".

Two other safety gates remain in place because they encode
distinct constraints, not opt-in policy:

- Laptop dGPU detection (`RULE-GPU-PR2D-06`) routes writes
  through the NBFC backend rather than NVML.
- AMD writes still require `--enable-amd-overdrive`
  (`RULE-EXPERIMENTAL-AMD-OVERDRIVE-01`) because the AMD
  OverDrive interface is still experimental.

Bound: internal/hal/gpu/nvml/probe_test.go:TestGPU_WriteGatedByCapability
Bound: internal/hal/gpu/nvml/probe_test.go:write_refused_when_ro_sensor_only
Bound: internal/hal/gpu/nvml/probe_test.go:write_allowed_when_rw_full
Bound: internal/hal/gpu/nvml/probe_test.go:write_allowed_when_rw_quirk

## RULE-GPU-PR2D-02: NVML wrapper in internal/hal/gpu/nvml/ uses purego only — no CGO.

The `internal/hal/gpu/nvml/` package and all files it imports
directly MUST NOT contain `import "C"`. CGO is incompatible
with `CGO_ENABLED=0`, the project-wide invariant. NVML access
is provided via `internal/nvidia` which loads
`libnvidia-ml.so.1` at runtime using purego Dlopen/Dlsym. The
static-analysis subtest greps the `internal/hal/gpu/nvml/`
directory tree for the literal string `import "C"` and asserts
zero matches. A cgo import that slipped in during a refactor
would break the static binary on musl-based distros and
Alpine Linux, where libc SONAME assumptions in fakecgo fail at
process start.

Bound: internal/hal/gpu/nvml/loader_test.go:TestNVML_NoCGO

## RULE-GPU-PR2D-03: libnvidia-ml.so.1 absence is graceful — no panic, daemon continues.

When `libnvidia-ml.so.1` is not installed (no NVIDIA driver,
or driver installed but library absent), `nvml.Open` MUST
return a non-nil error wrapping `nvidia.ErrLibraryUnavailable`.
The GPU enumeration in `internal/hal/gpu/registry.go` treats
this error as "no NVIDIA GPUs detected" and continues
registering AMD and Intel backends. The daemon MUST NOT panic,
MUST NOT exit non-zero, and MUST log exactly one INFO-level
message ("NVIDIA driver not detected; GPU features disabled").
The subtest calls `nvml.Open` in an environment where no
`libnvidia-ml.so.1` is present and asserts: non-nil error,
errors.Is wraps `nvidia.ErrLibraryUnavailable`, no goroutine
leak, no panic.

Bound: internal/hal/gpu/nvml/loader_test.go:TestNVML_GracefulMissingLib

## RULE-GPU-PR2D-04: Schema v1.0 unchanged — new GPU driver YAMLs validate against existing profile_v1.go schema with no new fields.

All GPU driver catalog entries added in spec-03 PR 2d MUST
validate against the existing `internal/hwdb/profile_v1.go`
schema (schema_version "1.0") without requiring any new struct
fields, new YAML keys, or changes to `validateDriverProfile`.
Every GPU driver profile (`nvidia`, `amdgpu`, `amdgpu_rdna3`,
`i915`, `xe`, `nouveau`, `radeon`) must pass
RULE-HWDB-PR2-01..05 validation. The subtest
`TestHWDB_GPUEntriesV1Compatible` calls `LoadCatalog()` on the
embedded filesystem and asserts that all GPU driver module
names are present in `cat.Drivers` with non-nil profiles, and
that the catalog load returns nil error. A GPU profile that
introduces a new required field would silently break all
existing board profiles that do not set the new field.

Bound: internal/hwdb/profile_v1_test.go:TestHWDB_GPUEntriesV1Compatible

## RULE-GPU-PR2D-05: hwmon path resolution by name only — no hwmonN number literals in non-test code under internal/hal/gpu/.

All hwmon path resolution in `internal/hal/gpu/` MUST discover
the active `hwmonN` directory by reading the `name` attribute
(e.g. matching "amdgpu", "xe", "i915") rather than hard-coding
any `hwmon0`, `hwmon1`, etc. literal. The same pattern is
enforced for motherboard chip drivers by
RULE-HWMON-INDEX-UNSTABLE. `hwmonN` numbers are kernel-assigned
at boot, change across reboots and module reloads, and are not
stable across PCIe slot changes. The static-analysis subtest
greps all non-test `.go` files under `internal/hal/gpu/` for
the regular expression `hwmon[0-9]` and asserts zero matches.
A hard-coded path that worked in dev may silently control the
wrong GPU fan after a kernel update changes hwmon numbering.

Bound: internal/hal/gpu/amdgpu/sysfs_test.go:TestGPU_NoHwmonNumbersHardcoded

## RULE-GPU-PR2D-06: Laptop dGPU detection is conservative — DMI chassis_type in laptop set marks dGPU as requires_userspace_ec.

When `nvml.LaptopDGPU(dmiRoot)` detects that the chassis type
is one of: Portable (8), Laptop (9), Notebook (10), Hand Held
(11), Sub Notebook (14), or Convertible (31) — as read from
`<dmiRoot>/class/dmi/id/chassis_type` — AND at least one NVML
GPU is visible, the function returns `(true, nil)`. The GPU
registry then sets `RequiresUserspacEC: true` on the
corresponding channel, and any write attempt returns
`ErrLaptopDgpuRequiresEC` with a message pointing to the
spec-09 NBFC backend. The subtest exercises the detection with
a synthetic `testing/fstest.MapFS` rooted at a temp dir,
injecting chassis_type=9 (Laptop) and asserting the returned
bool is true and the write attempt returns a non-nil error
containing "requires_userspace_ec". Non-laptop chassis types
must return false.

Bound: internal/hal/gpu/nvml/probe_test.go:TestNVML_LaptopDgpuRequiresEC
Bound: internal/hal/gpu/nvml/probe_test.go:laptop_chassis_detected
Bound: internal/hal/gpu/nvml/probe_test.go:desktop_chassis_not_flagged
Bound: internal/hal/gpu/nvml/probe_test.go:laptop_write_returns_ec_error

## RULE-GPU-PR2D-07: RDNA3+ AMD GPU writes use gpu_od/fan_ctrl/fan_curve interface only — direct pwm1 writes are refused.

When the AMD backend detects an RDNA3 or RDNA4 device (via the
`fan_curve` file present at
`<card>/device/gpu_od/fan_ctrl/fan_curve`), any call to the
RDNA1/2 `pwm1` write path MUST return `ErrRDNA3UseFanCurve`
without writing to `pwm1`. Writes MUST go only to the
`fan_curve` sysfs interface (5-anchor-point format: `<idx>
<temp_c> <pct>` followed by `c` to commit). The subtest
`TestAMD_RDNA3UsesFanCurve` provides a synthetic
`testing/fstest` fixture containing both `pwm1` and
`gpu_od/fan_ctrl/fan_curve`, calls the RDNA3 write path with a
test speed, and asserts: `pwm1` is unmodified, `fan_curve`
received the correct anchor-point bytes followed by a commit.
This prevents silent no-ops when a kernel that removed RDNA3
`pwm1` writability is encountered on a card that boots in
firmware mode.

Bound: internal/hal/gpu/amdgpu/sysfs_test.go:TestAMD_RDNA3UsesFanCurve
Bound: internal/hal/gpu/amdgpu/sysfs_test.go:direct_pwm1_write_refused
Bound: internal/hal/gpu/amdgpu/sysfs_test.go:fan_curve_write_accepted
Bound: internal/hal/gpu/amdgpu/sysfs_test.go:rdna12_card_accepts_pwm1_write

## RULE-GPU-PR2D-09: nvidia.InitWithDeadline returns wrapped ErrLibraryUnavailable on timeout; the in-flight dlopen goroutine is orphaned (uncancellable).

`InitWithDeadline(ctx, logger, timeout)` wraps `Init` in a
goroutine and selects between the goroutine's done-channel,
`time.After(timeout)`, and `ctx.Done()`. The four branches:

- **Loader returns within deadline**: error (or nil) is
  returned verbatim so callers' `errors.Is(err, ErrLibraryUnavailable)`
  and `errors.Is(err, ErrInitFailed)` checks continue to work
  as if `Init` had been called directly.
- **Timeout fires before loader returns**: an error wrapping
  `ErrLibraryUnavailable` whose message contains `"timed out"`
  and the deadline. A WARN line is logged once ("NVML init
  timed out; GPU features disabled for process lifetime").
- **ctx cancelled before loader returns**: an error wrapping
  `ErrLibraryUnavailable` with the cancellation cause
  embedded. No log line — cancellation is operator-driven.
- **timeout <= 0**: timeout is disabled (equivalent to plain
  `Init`). A pre-cancelled ctx still short-circuits before any
  loader call.

The in-flight goroutine is orphaned on timeout/cancel because
`purego.Dlopen` is uncancellable — Linux dlopen has no per-call
timeout primitive, and goroutines cannot be killed externally
without coopt. The goroutine eventually completes when Dlopen
returns, which may be never on a truly wedged driver.
Subsequent callers of `Init` or `InitWithDeadline` within the
same process will block on the same `loadOnce.Do` that the
orphan owns — so once a timeout fires, NVML is permanently
disabled for this process. By design: the daemon proceeds
without GPU features rather than hang past systemd's
`TimeoutStartSec` with no diagnostic the operator can act on.

The testable inner core `initWithDeadline(ctx, logger, timeout, fn)`
accepts an arbitrary loader function so tests can exercise
every branch without touching the package-level `loadOnce` /
`loadErr` / `loadLibraryFn` state. Production calls always
pass `Init` as the fn.

Bound: internal/nvidia/init_deadline_test.go:RULE-GPU-PR2D-09_timeout_returns_wrapped_unavailable
Bound: internal/nvidia/init_deadline_test.go:RULE-GPU-PR2D-09_fast_path_passes_through
Bound: internal/nvidia/init_deadline_test.go:RULE-GPU-PR2D-09_ctx_cancel_returns_wrapped_unavailable
Bound: internal/nvidia/init_deadline_test.go:RULE-GPU-PR2D-09_zero_timeout_disables_deadline
Bound: internal/nvidia/init_deadline_test.go:RULE-GPU-PR2D-09_nil_logger_uses_default
