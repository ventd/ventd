# System-class detection rules

These invariants govern `internal/sysclass/`, the per-host
classification step that selects the right Envelope C parameter
set + ambient-sensor handling + server-probe gate. Class
precedence is NAS > MiniPC > Laptop > Server > HEDT > MidDesktop
> Unknown. The classification result is persisted to KV before
Envelope C begins so the web UI and post-restart resume both see
the same class.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-SYSCLASS-01: System class precedence order is NAS > MiniPC > Laptop > Server > HEDT > MidDesktop > Unknown.

`detectWithDeps(d deps, r *probe.ProbeResult)` evaluates system
class signals in a fixed priority chain: NAS (rotational drive +
pool) is checked first, MiniPC (no controllable channels and
N-series CPU) second, Laptop (battery present or chassis DMI
type) third, Server (BMC present or server-class CPU) fourth,
HEDT-AIO/HEDT-Air fifth, MidDesktop (any controllable channels)
sixth, Unknown last. A result that matches multiple classes
resolves to the highest-priority class — e.g. a NAS with a
battery (docking station) is ClassNASHDD, not ClassLaptop.
Incorrect ordering would misclassify machines whose hardware
straddles two categories, producing the wrong Envelope C tuning.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_01_PrecedenceOrder
Bound: internal/sysclass/sysclass_test.go:nas_beats_laptop
Bound: internal/sysclass/sysclass_test.go:laptop_beats_server
Bound: internal/sysclass/sysclass_test.go:mid_desktop_fallback_with_channels
Bound: internal/sysclass/sysclass_test.go:hedt_air_beats_mid_desktop

## RULE-SYSCLASS-02: System class and evidence are written to KV store before Envelope C begins.

`PersistDetection(db *state.KVDB, d *Detection)` MUST be called
with the `Detection` returned by `Detect()` before the Envelope
C calibration loop starts. It writes four keys atomically via
`db.WithTransaction`: `sysclass.class` (string),
`sysclass.evidence` (JSON array of evidence strings),
`sysclass.detected_at` (RFC3339), and `sysclass.schema_version`.
After a successful persist, `LoadDetection(db)` MUST return a
`Detection` with the same class and evidence fields. Persisting
before Envelope C ensures the web UI can display the detected
class during calibration and that a daemon restart during
calibration inherits the correct class without re-running
detection.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_02_KVWriteBeforeEnvelopeC

## RULE-SYSCLASS-03: Ambient sensor identification uses a three-step fallback chain: labeled → lowest-at-idle → 25°C constant.

`identifyAmbient(sources []probe.ThermalSource) float64` applies
three steps in order:
(1) Return the reading from any sensor whose label matches an
ambient keyword (`"ambient"`, `"inlet"`, `"room"`, `"case"`,
etc.) — the labeled-sensor step.
(2) If no labeled sensor is found, return the reading from the
non-CPU, non-GPU sensor with the lowest value at idle — the
lowest-at-idle step.
(3) If no admissible sensor is found (all sensors blocked by
the admissibility blocklist, or the list is empty), return the
constant 25.0°C — the fallback step.
A system without any ambient sensor still gets a sensible
ambient for Envelope C curve parameterisation; a constant is
preferable to an error that aborts the calibration run.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_03_AmbientFallbackChain
Bound: internal/sysclass/sysclass_test.go:step1_labeled_sensor
Bound: internal/sysclass/sysclass_test.go:step2_lowest_at_idle
Bound: internal/sysclass/sysclass_test.go:step3_fallback_25c

## RULE-SYSCLASS-04: Ambient reading outside [10, 50]°C is rejected as implausible before Envelope C starts.

`AmbientBoundsOK(reading float64) (code string, ok bool)` MUST
return `("", true)` when `reading` is in the closed interval
[10.0, 50.0]°C. When `reading < 10.0`, it returns
`("AMBIENT-IMPLAUSIBLE-TOO-COLD", false)`. When
`reading > 50.0`, it returns
`("AMBIENT-IMPLAUSIBLE-TOO-HOT", false)`. The Envelope C
orchestrator calls `AmbientBoundsOK` on the value returned by
`identifyAmbient` before parameterising the thermal curve; an
ambient outside this range indicates a sensor wiring error, a
sentinel leak, or test-fixture pollution. Running Envelope C
on a garbage ambient would produce a curve anchored at a
physically impossible temperature and silently mis-calibrate
every fan on the system.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_04_AmbientBoundsRefusal

## RULE-SYSCLASS-05: ClassServer + BMC-present systems require --allow-server-probe to proceed with Envelope C.

`ServerProbeAllowed(cls SystemClass, bmcPresent, allowServerProbe bool) bool`
MUST return `false` when
`cls == ClassServer && bmcPresent && !allowServerProbe`. For
all other combinations — non-Server class, no BMC, or
allowServerProbe=true — it returns `true`. The Envelope C
orchestrator calls this gate before entering the calibration
loop. A server with a BMC (detected via `/dev/ipmi*` or
dmidecode type 38) may have BIOS-managed fan curves that
conflict with direct PWM writes; the operator must explicitly
pass `--allow-server-probe` to acknowledge the risk. On server
hardware without a BMC (e.g. a rack workstation without IPMI),
the gate opens automatically.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_05_ServerBMCGate

## RULE-SYSCLASS-06: Laptop EC handshake succeeds when RPM changes within 5s; fails cleanly on context cancel.

`ProbeECHandshake(ctx context.Context, pwmEnablePath, rpmPath string) (bool, error)`
polls `rpmPath` at 200ms intervals for up to
`ecHandshakeTimeout` (5s). It captures the initial RPM reading,
writes `1` to `pwmEnablePath` to enable manual mode, then waits
for the RPM to change from the initial value. When the RPM
changes, the function returns `(true, nil)`. When `ctx` is
cancelled before a change is observed, the function returns
`(false, ctx.Err())`. When the timeout elapses with no change,
it returns `(false, nil)`. A successful handshake confirms the
EC acknowledges manual PWM control. A context-cancelled return
propagates the cause without leaking goroutines or blocking the
caller.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_06_LaptopECHandshake
Bound: internal/sysclass/sysclass_test.go:success_rpm_changes
Bound: internal/sysclass/sysclass_test.go:failure_context_cancelled

## RULE-SYSCLASS-07: Every system class produces at least one evidence string in Detection.Evidence.

`Detection.Evidence` MUST be a non-empty slice for every
non-Unknown class returned by `detectWithDeps`. Each class has
at least one canonical evidence string: ClassNASHDD has
`"rotational_disk"` and `"pool_detected"`, ClassMiniPC has
`"no_controllable_channels"`, ClassLaptop has
`"battery_detected"`, ClassServer has `"bmc_detected"`,
ClassHEDTAIO has `"liquid_cooler_channel"`, ClassHEDTAir has
`"hedt_cpu"`, ClassMidDesktop has `"controllable_channels"`.
Evidence strings are logged at INFO level on daemon start and
surfaced in `ventd doctor` output; an empty slice prevents the
operator from understanding why a class was chosen, making
misclassification debugging impossible.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_07_EvidenceCompleteness
Bound: internal/sysclass/sysclass_test.go:nas
Bound: internal/sysclass/sysclass_test.go:mini_pc
Bound: internal/sysclass/sysclass_test.go:laptop
Bound: internal/sysclass/sysclass_test.go:server
Bound: internal/sysclass/sysclass_test.go:hedt_aio
Bound: internal/sysclass/sysclass_test.go:hedt_air
Bound: internal/sysclass/sysclass_test.go:mid_desktop
