# Hardware database rules (PR-2 schema, fingerprint matcher, capture, overrides)

These invariants govern `internal/hwdb/` — the v1 hardware-profile
schema (drivers, chips, boards), the three-tier matcher
(DMI / device-tree / chip-probe → board → chip → driver), the
catalog overlay's experimental + `overrides.unsupported` blocks,
and the capture path that produces pending board profiles for
operator review.

Related family files:
- `hwdb-schema.md` — RULE-HWDB-01..09 (v1 top-level fields,
  monotonic curves, known PWM modules).
- `experimental.md` — `experimental:` block validation +
  AMD-OverDrive flag wiring.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## Fingerprint matcher

## RULE-FINGERPRINT-04: Matcher matches DMI `bios_version` glob when field is present on a board profile.

When a `dmi_fingerprint` entry has a non-empty `bios_version`
field (e.g. `"GKCN*"`), the tier-1 board matcher MUST evaluate
a glob match between that pattern and the live
`DMIFingerprint.BiosVersion` value. A board entry with
`bios_version: "GKCN*"` must match a live system with
`BiosVersion: "GKCN58WW"` and must NOT match a system with
`BiosVersion: "EUCN32WW"`. This field enables Lenovo Legion
family dispatch: multiple Legion generations share the same
`product_name` (machine-type code) but differ in their
4-character BIOS family prefix (GKCN, EUCN, H1CN, LPCN, etc.).
Without `bios_version` matching, all generations would collapse
to the same board profile regardless of generation-specific
fan quirks.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_BiosVersionGlob_Matches

## RULE-FINGERPRINT-05: Fingerprint without `bios_version` field matches any live BIOS version (v1 behavior unchanged).

When a `dmi_fingerprint` entry has no `bios_version` field
(absent or empty), the tier-1 board matcher MUST treat that
field as `"*"` and accept any live `BiosVersion` value,
including the empty string. This preserves exact v1.0 matching
behavior for all existing board profiles that pre-date the
v1.1 schema amendment. A catalog that adds `bios_version`
support for new entries MUST NOT break matching for older
entries that never set the field.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_BiosVersionAbsent_BehavesAsV1

## RULE-FINGERPRINT-06: Matcher matches device-tree `compatible` list glob when DMI is absent and `dt_fingerprint.compatible` is set.

When `dmiPresent` is false AND a board profile has a non-empty
`dt_fingerprint.compatible` pattern, the tier-1 matcher MUST
evaluate a glob match between that pattern and each entry in
the live `/proc/device-tree/compatible` null-separated list. A
match on ANY entry in the list is sufficient. A board with
`dt_fingerprint.compatible: "raspberrypi,5-model-b"` MUST
match a live system whose compatible list contains
`"raspberrypi,5-model-b"` (along with other entries like
`"brcm,bcm2712"`). When `dmiPresent` is true, `dt_fingerprint`
profiles are never considered — RULE-FINGERPRINT-07 covers the
model field; this rule covers the compatible list.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_DTCompatibleGlob_Matches

## RULE-FINGERPRINT-07: Matcher matches device-tree `model` string glob when DMI is absent and `dt_fingerprint.model` is set.

When `dmiPresent` is false AND a board profile has a non-empty
`dt_fingerprint.model` pattern, the tier-1 matcher MUST
evaluate a glob match between that pattern and the live
`/proc/device-tree/model` string (null-terminated, trimmed). A
board with `dt_fingerprint.model: "Raspberry Pi 5*"` MUST
match a live system with model `"Raspberry Pi 5 Model B Rev 1.0"`.
The glob wildcard `*` matches any suffix including the
revision suffix that varies across hardware batches. When
`dmiPresent` is true, `dt_fingerprint` profiles are never
considered regardless of whether `/proc/device-tree/model` exists.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_DTModelGlob_Matches

## RULE-SCHEMA-08: Board catalog loader rejects a profile with both `dmi_fingerprint` and `dt_fingerprint` set.

A board profile MUST have exactly one of `dmi_fingerprint` or
`dt_fingerprint`. Setting both is a schema error: the
matcher's DMI-first / DT-fallback dispatch logic requires each
profile to commit to one fingerprint type. A profile with both
set produces ambiguous match semantics (which takes
precedence? what if only DT is live?).
`validateBoardCatalogEntry` MUST return a non-nil error
containing `"exactly one is required"` for any profile where
both fields are non-nil, causing the entire board catalog load
to abort. Similarly, a profile with neither field set is
rejected with the same error — an un-matchable profile is a
catalog defect, not a warning.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_RejectsBothFingerprintTypes

## PR-2 profile schema

## RULE-HWDB-PR2-01: Every driver_profile MUST declare all fields in §2-§12. Missing field = matcher refuses to load profile DB.

Every entry in the driver catalog MUST have all required
top-level fields: `module`, `family`, `description`,
`capability`, `pwm_unit`, `pwm_enable_modes`, `off_behaviour`,
`polling_latency_ms_hint`, `recommended_alternative_driver`,
`conflicts_with_userspace`, `fan_control_capable`,
`required_modprobe_args`, `pwm_polarity_reservation`,
`exit_behaviour`, `runtime_conflict_detection_supported`, and
`citations`. A driver profile with any of these fields absent
is rejected at load time with a human-readable error that
names the missing field and the offending module name.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_01

## RULE-HWDB-PR2-02: chip_profile.inherits_driver MUST resolve to a known driver_profile.module.

Every chip profile's `inherits_driver` field must reference a
`module` value that exists in the loaded driver catalog. A
chip profile referencing an unknown driver is rejected at load
time. The error message names the failing chip `name` and the
unresolved `inherits_driver` value. This ensures the
inheritance chain is always complete — a chip with no driver
parent cannot be used in the three-tier resolver.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_02

## RULE-HWDB-PR2-03: board_profile.primary_controller.chip MUST resolve to a known chip_profile.name.

Every board profile's `primary_controller.chip` field must
reference a `name` value that exists in the loaded chip
catalog. A board profile referencing an unknown chip is
rejected at load time. The error message names the failing
board `id` and the unresolved chip name. This ensures the full
three-tier chain (driver → chip → board) is always resolvable
when a board match is found.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_03

## RULE-HWDB-PR2-04: pwm_unit_max MUST be set when pwm_unit ∈ {step_0_N, cooling_level}.

When a driver profile declares `pwm_unit: step_0_N` or
`pwm_unit: cooling_level`, the companion `pwm_unit_max` field
MUST be a non-null positive integer. A profile with these
pwm_unit values and a null or absent `pwm_unit_max` is
rejected at load time. The error names the module and the
constraint. This prevents calibration code from dispatching a
discrete-state sweep without knowing how many states exist.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_04

## RULE-HWDB-PR2-05: pwm_enable_modes MUST contain a manual entry when capability ∈ {rw_full, rw_quirk, rw_step}.

For any driver profile with `capability` in
`{rw_full, rw_quirk, rw_step}`, the `pwm_enable_modes` map
MUST contain at least one entry whose value is `"manual"`. A
writable driver with no manual-mode entry is rejected at load
time. The error names the module and the missing mode. This
ensures ventd can always take manual PWM control on a driver
it is permitted to write to — a driver without a known
manual-mode integer cannot be safely calibrated.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_05

## RULE-HWDB-PR2-06: recommended_alternative_driver MUST be non-null when capability == ro_pending_oot.

A driver profile with `capability: ro_pending_oot` declares
that the mainline driver is read-only but an out-of-tree
alternative exists. For such profiles, the
`recommended_alternative_driver` field MUST be a non-null
object with at least `module` and `source` set. A
`ro_pending_oot` profile with a null
`recommended_alternative_driver` is rejected at load time.
This invariant ensures the diagnostic bundle always has
something actionable to surface when it detects this driver.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_06

## RULE-HWDB-PR2-07: fan_control_capable: false profiles MUST install in monitor-only mode (no calibration probe runs).

When the resolved `EffectiveControllerProfile` has
`FanControlCapable: false`, the install and runtime paths must
not invoke the calibration probe for any channel backed by
this profile. The test fixture verifies that
`ShouldCalibrate(ecp)` returns false when `FanControlCapable`
is false, regardless of what `Capability` is set to. A
monitor-only driver that runs calibration would attempt PWM
writes to a driver that either returns EPERM or silently
ignores them.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_07

## RULE-HWDB-PR2-08: Calibration result bios_overridden: true MUST cause apply path to refuse curve writes for that channel.

When a `ChannelCalibration` loaded from disk has
`BIOSOverridden: true`, the controller apply path MUST return
`hwdb.ErrBIOSOverridden` and skip writing any PWM value to the
associated channel. The test fixture verifies that
`writeWithRetry` returns `hwdb.ErrBIOSOverridden` and that
`backend.Write` is never called when the controller's `calCh`
has `BIOSOverridden: true`. This prevents silent no-op writes
to channels where the BIOS firmware actively overrides ventd's
PWM values — the correct response is to surface the issue in
the diagnostic bundle and mark the channel as monitor-only.

Bound: internal/controller/controller_test.go:TestWriteWithRetry_RefusesBIOSOverridden
Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_08

## RULE-HWDB-PR2-09: DMI BIOS version mismatch between calibration record and current firmware MUST trigger recalibration.

When ventd starts and loads a `CalibrationRun` from disk, it
compares the `BIOSVersion` field in that record against the
current BIOS version read from
`/sys/class/dmi/id/bios_version`. A mismatch MUST cause
`NeedsRecalibration(run, currentBIOS)` to return true. The
test fixture verifies this with a synthetic mismatch case and
a synthetic match case (which must return false). Stale
calibration after a BIOS upgrade can produce incorrect PWM
polarity detection, wrong stall_pwm, and miscalibrated fan
curves that damage hardware.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_09

## RULE-HWDB-PR2-10: Layer precedence (board > chip > driver, calibration > all for runtime fields) MUST be enforced by the resolver.

The `ResolveEffectiveProfile(driver, chip, board, cal)`
function MUST apply layer precedence: board overrides chip,
chip overrides driver; calibration fields override all three
for the fields they populate (PWMPolarity, MinResponsivePWM,
StallPWM, PhantomChannel). The test fixture uses a synthetic
triple: driver=nct6775 (defaults), chip=nct6798 (overrides
OffBehaviour to bios_dependent), board=asus_z790_a (overrides
PollingLatencyHint to 75ms and CPUTINFloats=true). The
resolved profile must reflect all three overrides: chip beats
driver on OffBehaviour; board beats chip on latency and quirk.
A calibration result with PWMPolarity=inverted must further
override the resolved profile's calibration fields. Each
assertion is labeled with the layer that should win.

Bound: internal/hwdb/effective_profile_test.go:TestRuleHwdbPR2_10

## RULE-HWDB-PR2-11: PR 1 → PR 2 migration: a PR 1 pwm_control: <string> MUST resolve via the chip-name fallback path with a logged warning if the string doesn't match a chip profile.

The `ModuleProfile.ToEffectiveControllerProfile()` migration
helper (added to `module_match.go`) MUST attempt to resolve
the PR 1 `hardware.pwm_control` string first as a
chip_profile.name, then as a driver_profile.module. In both
cases it returns an EffectiveControllerProfile. When the
string matches a driver module but not a chip name, the
migration logs a warning and synthesises an anonymous chip
profile with no overrides. The test fixture verifies both
resolution paths: a string that matches a known chip name
("nct6798") and a string that matches only a driver module
("nct6775"). The warning must be observable via the test's
slog handler.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_11

## RULE-HWDB-PR2-12: The matcher MUST refuse to match a profile that violates any of RULE-HWDB-PR2-01..05.

The catalog loader `LoadCatalog()` MUST validate all driver
and chip profiles against RULE-HWDB-PR2-01..05 before
returning any catalog. If any profile fails validation, the
entire catalog load fails with a structured error that names
the violating profile and the rule. The test fixture loads an
invalid driver profile (missing `capability`) and asserts that
LoadCatalog returns a non-nil error containing "required
field". A valid catalog with no violations must load cleanly.
This prevents silent acceptance of a malformed catalog entry
that could produce undefined matcher behaviour at runtime.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_12

## RULE-HWDB-PR2-13: Every driver_profile MUST declare exit_behaviour from the §12.1 enum.

Every driver profile MUST declare `exit_behaviour` as one of
`force_max`, `restore_auto`, `preserve`, or `bios_dependent`.
A profile with a missing or unrecognised `exit_behaviour`
value is rejected at catalog load time with an error that
names the module and the invalid value. The test fixture
verifies: (1) a valid profile with each enum value loads
cleanly; (2) a profile with an invalid value `"unknown_mode"`
is rejected. The `ExitBehaviour` field is exposed on
`EffectiveControllerProfile` so the apply path can dispatch
the correct shutdown action per channel.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_13

## RULE-HWDB-PR2-14: Every driver_profile MUST declare runtime_conflict_detection_supported boolean.

Every driver profile MUST declare
`runtime_conflict_detection_supported` as an explicit boolean
(`true` or `false`). A profile where this field is absent (not
just false — Go's zero value cannot be distinguished from
explicit false) is rejected at catalog load time. The catalog
loader uses a pointer type (`*bool`) internally to detect
absence. The test fixture verifies that an explicit `false`
loads cleanly and that an absent field is rejected. The field
is exposed on
`EffectiveControllerProfile.RuntimeConflictDetectionSupported`
for use by the post-PR-2 sanity-check path.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_14

## RULE-HWDB-PR2-15: Board profile `pwm_groups` validates that each entry's channel is non-empty, fans is non-empty, and fan ids are unique.

Schema v1.3 introduces the optional `pwm_groups: [{channel:
<pwm-leaf>, fans: [<fan-id>, ...]}]` field on board profiles.
The field exists because R29 §4 found that Phoenix's MSI
Z690-A drives Cpu_Fan + Pump_Fan + Sys_Fan_1 + Sys_Fan_2 with
**identical PWM values across all 2479 captured status
samples** — one PWM channel, four fans. Without this grouping
data the v0.5.11+ cost gate computes per-fan loudness
independently, missing the +10·log10(N) energetic-sum penalty
that real grouped fans exhibit.

`validateBoardCatalogEntry` rejects:
- An entry whose `channel` is empty / whitespace-only.
- An entry whose `fans` slice is empty (a group with zero
  fans is meaningless).
- An entry whose `fans` slice contains an empty fan id, OR
  contains a duplicate fan id (the same fan listed twice on
  the same channel).

A board profile without `pwm_groups` is unchanged in
behaviour — the field is opt-in and defaults to "no grouping
known", which means the cost gate treats each fan
independently (the pre-v1.3 behaviour).

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_15

## RULE-HWDB-PR2-16: Driver profile `blacklist_before_install` rejects empty entries and duplicates.

Schema v1.3 introduces the optional `blacklist_before_install:
[<module>, ...]` field on driver profiles. Phoenix's MS-7D25
IT8688E→NCT6687D incident proved that the install path
sometimes needs to blacklist a conflicting in-tree driver
before the OOT module can bind. Generalising that pattern:
when a driver profile lists `blacklist_before_install:
[nct6683]`, the install path writes `blacklist nct6683` to
`/etc/modprobe.d/ventd-<driver>.conf` and runs `modprobe -r
nct6683` before invoking the target driver's modprobe.

`validateDriverProfile` rejects:
- An empty entry in the slice (a whitespace-only or
  zero-length module name).
- Duplicate entries (the same module listed twice on the same
  driver).

The slice may be absent or empty without error; both indicate
"no conflicting modules to blacklist".

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_16

## RULE-HWDB-PR2-17: Driver profile `kernel_version: {min, max}` requires dotted-numeric strings and `Min <= Max` when both set.

Schema v1.3 introduces the optional `kernel_version: {min:
"X.Y", max: "X.Y"}` field on driver profiles. R36's per-row
analysis identified eight catalog rows that gate on a
specific kernel-version window — e.g. `it87` quirks landed in
6.2 (`ignore_resource_conflict=1` + `mmio=off`), MS-01
mainline support landed in 5.14 (NCT6798D), Strix Halo support
landed in 6.13. Without this gate, ventd would attempt
drivers on kernels that can't bind them, wasting cycles +
producing misleading recovery cards.

`validateDriverProfile` rejects:
- A non-empty `min` or `max` that is not a dotted-numeric
  string. Valid: `"6.2"`, `"6.13.4"`, `"1"`. Invalid:
  `"6.2.x"`, `"v6.2"`, `"latest"`.
- A range where `min` > `max` (numeric comparison, not
  lexicographic — so `"6.10"` is treated as 6.10, not 6.1.0;
  `"6.10"` > `"6.9"` correctly).

Both `min` and `max` are optional; either or both may be
empty. An absent field block leaves the driver
kernel-version-agnostic (pre-v1.3 behaviour).

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_17

## RULE-HWDB-PR2-18: Board profile `chip_probe.hwmon_name` is the hwmon-name-based fallback fingerprint when DMI is absent / "Default string".

Schema v1.3 introduces the optional `chip_probe: {hwmon_name:
<string>}` field on board profiles. R36 §B's mini-PC EC
firmware survey identified a class of hosts — Beelink /
Minisforum / GMKtec / AceMagic mini-PCs running IT5570 or
IT8613 EC firmware — whose BIOS authors never populated DMI
fields, leaving `/sys/class/dmi/id/sys_vendor` reading
literally `"Default string"`.

The matcher walks `/sys/class/hwmon/hwmonN/name` (already
passed as `chipName` through `MatchV1`) and binds a
chip-probe board profile when the live hwmon name matches the
catalog string (case-insensitive). The match runs as a
tier-1.5 pass — after DMI/DT board matches but before the
tier-3 chip-family fallback — so a board with a populated DMI
fingerprint always wins over a chip-probe board that happens
to share the same chip family. Confidence is 0.85 (vs.
DMI-tier-1's 0.9) because hwmon-name is a less specific
signal: many boards share the same EC chip.

`validateBoardCatalogEntry` rejects a profile that sets
`chip_probe` together with `dmi_fingerprint` and/or
`dt_fingerprint`, or that sets `chip_probe` with an empty /
whitespace-only `hwmon_name`.

Bound: internal/hwdb/profile_v1_1_test.go:TestRuleHwdbPR2_18
Bound: internal/hwdb/profile_v1_1_test.go:matcher_chip_probe_hwmon_name
Bound: internal/hwdb/profile_v1_1_test.go:matcher_chip_probe_case_insensitive
Bound: internal/hwdb/profile_v1_1_test.go:matcher_chip_probe_no_match_falls_through
Bound: internal/hwdb/profile_v1_1_test.go:validator_rejects_chip_probe_with_dmi
Bound: internal/hwdb/profile_v1_1_test.go:validator_rejects_empty_hwmon_name

## Capture (pending board profiles)

## RULE-HWDB-CAPTURE-01: Capture writes go to `/var/lib/ventd/profiles-pending/` (or `$XDG_STATE_HOME/ventd/profiles-pending/` in user mode) only.

Capture NEVER writes to the live `profiles.yaml` or
`profiles-v1.yaml` at runtime. The `Capture()` function
accepts an explicit `dir` argument and writes only to
`filepath.Join(dir, fingerprint+".yaml")`. In production,
`dir` is always the pending directory returned by
`CaptureDir()`, not any path under the embedded catalog
filesystem. A pending profile accumulates until the user
explicitly reviews and promotes it; capturing directly to the
live catalog would bypass the review step and could introduce
un-verified board data into the matcher.

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_01_PendingDirOnly

## RULE-HWDB-CAPTURE-02: Capture cannot run if the anonymiser fails. The capture function returns an error and writes nothing — fail closed.

`Capture()` calls `callAnonymise(profile)` before any write
attempt. If `callAnonymise` returns a non-nil error, `Capture`
returns that error immediately and no file is created in the
pending directory. The test verifies this by injecting a
failing anonymiser via `atomicAnonymiseFn` and asserting that
`Capture` returns a non-nil error AND that the temporary
directory remains empty. Fail-closed semantics ensure that a
broken anonymiser never produces a bundle with un-stripped
PII: the correct response is always "abort capture" rather
than "write and hope."

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_02_FailClosedOnAnonymise

## RULE-HWDB-CAPTURE-03: A captured profile YAML never contains a field outside the schema v1.0 allowlist.

`Anonymise()` enforces this via a strict YAML round-trip:
after clearing user-set text fields and applying text-level
redaction, the profile is marshalled to YAML and decoded back
using `yaml.NewDecoder` with `KnownFields(true)`. Any field
not present in the `Profile` struct causes the decode to fail,
which surfaces as a non-nil error (fail-closed per
RULE-HWDB-CAPTURE-02). The test verifies that a profile
written by `Capture()` is accepted without error by `Load()` —
which also uses `KnownFields(true)` — and that the resulting
profile has exactly one entry with a valid schema_version,
contributed_by="anonymous", and verified=false. This invariant
prevents schema drift: a future refactor that adds an untagged
struct field cannot silently produce files that fail on load.

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_03_AllowlistedFieldsOnly

## overrides.unsupported

## RULE-OVERRIDE-UNSUPPORTED-01: Matcher with `overrides.unsupported: true` emits the INFO log exactly once per ventd lifetime per board ID.

When the tier-1 matcher resolves a board profile with
`overrides.unsupported: true`, it MUST call
`LogUnsupportedOnce(boardID, log)` which emits exactly one
`slog.LevelInfo` message containing the text `"no Linux
fan-control driver"` for that board ID. All subsequent
matches of the same board ID — within the same process
lifetime — MUST NOT emit additional log entries. The
once-per-board-ID guarantee is enforced by a package-level
`sync.Map` keyed by board ID; `LoadOrStore` atomically records
the first emission and no-ops on all subsequent calls. This
ensures the "sensors-only" message appears exactly once in
journald output — not on every control tick, which would
produce log spam at polling_latency_ms_hint frequency.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_UnsupportedEmitsLogOnce

## RULE-OVERRIDE-UNSUPPORTED-02: Calibration phase skips autocurve generation when the resolved profile has `overrides.unsupported: true`.

`hwdb.ShouldSkipCalibration(ecp *EffectiveControllerProfile) bool`
MUST return `true` when `ecp.Unsupported == true`, and `false`
otherwise. The calibration orchestrator MUST call this
function before entering the probe sweep and skip the entire
calibration pipeline (polarity probe, stall sweep,
BIOS-override detection) when it returns true. Skipping is
correct because `unsupported: true` signals that no Linux
fan-control driver path exists for this board; running
calibration would attempt PWM writes that the OS either
silently ignores or returns EPERM for, wasting time and
producing garbage calibration records. Sensor reads
(telemetry-only mode) are unaffected by this flag.

Bound: internal/hwdb/profile_v1_1_test.go:TestCalibration_UnsupportedSkipsAutocurve
Bound: internal/hwdb/profile_v1_1_test.go:unsupported_true_skips
Bound: internal/hwdb/profile_v1_1_test.go:unsupported_false_does_not_skip
