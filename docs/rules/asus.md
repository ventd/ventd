# ASUS WMI custom fan curve — HAL backend + g-helper corpus rules

ASUS ROG/TUF/Strix/Scar/Flow/Zephyrus/Zenbook/Vivobook/ProArt/ROG Ally notebooks
expose an eight-point custom fan curve through the mainline `asus-wmi` driver
(hwmon name `asus_custom_fan_curve`, kernel 6.4+ — see the `asus-wmi.yaml` driver
catalog row). The write surface is a curve, not a per-tick duty register:
`pwmN_auto_point1..8_{temp,pwm}` plus `pwmN_enable` (1=manual/apply,
2=factory-auto, 3=factory-auto+reset). The firmware runs the control loop itself
once the curve is programmed, so ventd drives it through the `internal/hal/asuswmi`
backend as a `hal.CurveSink` (CapWriteCurve) — program the curve once, re-program
only on change — exactly the amdgpu RDNA3/4 model (spec-17 PR-1b).

ventd also vendors `seerge/g-helper`'s default fan-curve presets
(github.com/seerge/g-helper, GPL-3.0) as a Mode-C corpus under
`internal/hwdb/asus/`. g-helper has no per-model curve dictionary (ASUS keeps
per-model curves in the BIOS); the corpus carries g-helper's model-agnostic
silent/balanced/turbo fallback curves only. These are reference curves an
operator can adopt — ventd talks to the kernel asus-wmi sysfs directly and never
shells out to g-helper or asusctl, so the corpus carries no EC-register/ACPI map
and there is no register allowlist to gate. spec-17 PR-3.

## RULE-HAL-ASUS-01: Enumerate is idempotent and keys on the asus_custom_fan_curve hwmon name.

`asuswmi.Backend.Enumerate` MUST scan the hwmon class for the device whose
`name` is `asus_custom_fan_curve` and emit one CurveSink channel per fan whose
`pwmN_auto_point1_pwm` attribute exists (pwm1 → RoleCPU, pwm2 → RoleGPU), each
advertising `CapRead | CapWriteCurve | CapRestore` and NOT `CapWritePWM`. A host
without the device — every non-ASUS machine, and ASUS hosts on a kernel without
the feature — returns an empty slice, not an error. Repeated calls return the
same channel set / caps / roles.

Bound: internal/hal/asuswmi/backend_test.go:TestEnumerate

## RULE-HAL-ASUS-02: WriteCurve programs eight monotonic anchors and applies the curve via pwm_enable=1.

`asuswmi.Backend.WriteCurve` MUST resample the caller's points to exactly eight
anchors with strictly-increasing temperatures and non-decreasing 0-255 PWM
bytes, write each `pwmN_auto_pointM_{temp,pwm}` node, then set `pwmN_enable=1` to
apply the curve. A firmware rejection (EIO/ENODEV — the "BIOS rejected fan curve"
failure) MUST surface as `ErrFanCurveRefused`; an EPERM/EACCES MUST surface as
`hal.ErrNotPermitted`. Both wraps preserve the underlying syscall error in the
chain.

Bound: internal/hal/asuswmi/backend_test.go:TestWriteCurve

## RULE-HAL-ASUS-03: Restore hands the fan back to factory auto (pwm_enable=2) and is safe on un-programmed channels.

`asuswmi.Backend.Restore` MUST write `pwmN_enable=2` (factory auto, curve
retained) — the asus_custom_fan_curve analogue of thinkpad's "level auto" /
legion's "balanced". It MUST be a clean operation on a channel that was
enumerated but never programmed (no panic, RULE-HAL-004).

Bound: internal/hal/asuswmi/backend_test.go:TestRestore

## RULE-HAL-ASUS-04: Read never mutates and reports a duty only when the kernel exposes one.

`asuswmi.Backend.Read` MUST NOT mutate observable state (RULE-HAL-002), and MUST
report `OK=true` with the duty only when the bare `pwmN` node is present and
parses to 0-255; otherwise `OK=false` ("skip this tick"), never an error. The
asus_custom_fan_curve hwmon carries no tachometer, so RPM is always zero — the
controller drives these channels via CurveSink and does not Read per tick.

Bound: internal/hal/asuswmi/backend_test.go:TestRead

## RULE-HAL-ASUS-05: per-tick Write is refused — the surface is curve-only.

`asuswmi.Backend.Write` MUST return a descriptive error (the surface has no
per-tick duty register) and MUST NOT write any sysfs node. Channels advertise
`CapWriteCurve`, not `CapWritePWM`, so the controller routes them through
WriteCurve; the error guards a mis-wired caller.

Bound: internal/hal/asuswmi/backend_test.go:TestWritePerTickRefused

## RULE-HAL-ASUS-06: invalid channel state is rejected and Close is idempotent.

`asuswmi.stateFrom` MUST reject a channel whose state has an empty `HwmonDir`
(`ErrNoFanCurveHwmon`) or a FanIndex that is not 1 (CPU) or 2 (GPU). `Close` MUST
be idempotent (RULE-HAL-007).

Bound: internal/hal/asuswmi/backend_test.go:TestStateValidation

## RULE-HAL-ASUS-07: resampleCurve yields exactly eight monotonic anchors with percent→byte conversion.

`asuswmi.resampleCurve` MUST produce exactly `fanCurvePoints` (8) anchors with
strictly-increasing temperatures and non-decreasing PWM bytes, converting each
interpolated percentage to the kernel's 0-255 PWM byte (0→0, 100→255, round-half-
up). A flat/degenerate input MUST still yield eight strictly-increasing
temperatures; an empty input is an error.

Bound: internal/hal/asuswmi/backend_test.go:TestResampleCurve

## RULE-ASUS-CATALOG-01: a malformed or invalid vendored preset aborts the load with the offending file named.

`asus.LoadCatalogFS` MUST fail closed: a config file that is not valid JSON, or
that fails the corpus invariants, aborts the entire load with an error naming the
offending file — never a silently half-loaded catalogue. The embedded corpus
(`internal/hwdb/asus/configs/*.json`) MUST therefore parse and validate cleanly.

Bound: internal/hwdb/asus/embed_test.go:TestLoadCatalog_EmbeddedFS_ParsesAllPresets
Bound: internal/hwdb/asus/embed_test.go:TestLoadCatalogFS_RejectsMalformedJSON

## RULE-ASUS-CATALOG-02: every vendored preset satisfies the curve invariants.

`asus.validate` MUST reject a config that defines no presets, a preset with an
empty or duplicate mode, a preset missing its CPU or GPU curve, or any curve
whose anchors carry a duty outside `[0,100]` or a temperature below the previous
anchor (curves are ascending by temperature). A preset with an inverted curve is
a sync error, not a curve to offer an operator.

Bound: internal/hwdb/asus/embed_test.go:TestValidate_RejectsBadConfigs

## RULE-ASUS-CATALOG-03: the ASUS matcher is deterministic and keyed on the ASUS sys_vendor.

`asus.Match` MUST be a pure function of the catalog + DMI: an ASUS host (DMI
`sys_vendor` containing "asus", case-folded) resolves to the canonical g-helper
preset entry, and a non-ASUS host resolves to no match. Repeated calls with the
same input MUST return the same result.

Bound: internal/hwdb/asus/match_test.go:TestMatch_ASUSReturnsGHelper

## RULE-ASUS-DOCTOR-01: the ASUS doctor card is corpus-backed and fires only on ASUS hosts.

The `asus_fan_curves` detector MUST emit exactly one card on an ASUS host that
names the available g-helper presets (read from the vendored corpus, not
hard-coded) and the `asus_custom_fan_curve` kernel interface, and MUST stay
silent on a non-ASUS host (and on a DMI-read error).

Bound: internal/doctor/detectors/asus_fan_curves_d_test.go:TestASUSFanCurves_ASUSHostEmitsCard
