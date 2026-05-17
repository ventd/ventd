# Experimental-feature flag rules

These invariants govern `internal/experimental/` (the flag
parser + diag publication) and the catalog schema's
`experimental:` block. The flags gate operator-opt-in
capabilities — AMD OverDrive bit, NVIDIA Coolbits, HPE iLO4
unlocked-fan, iDRAC9 legacy raw — that require a non-default
kernel cmdline or BIOS setting to take effect.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## Schema validation (loader-side gates)

## RULE-EXPERIMENTAL-SCHEMA-01: Recognized experimental key with bool value is accepted and parsed into ExperimentalBlock.

When an `experimental:` block in a driver or board profile
contains a key from the recognized set (`ilo4_unlocked`,
`amd_overdrive`, `nvidia_coolbits`, `idrac9_legacy_raw`) with a
boolean value (`true` or `false`), `validateExperimental` MUST
accept the entry and set the corresponding field on the
returned `ExperimentalBlock`. The test fixture provides a YAML
driver profile with `experimental: {amd_overdrive: true}`
loaded via `LoadCatalogFromFS` on an inline `fstest.MapFS` and
asserts that `cat.Drivers["amdgpu"].Experimental.AMDOverdrive == true`.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys

## RULE-EXPERIMENTAL-SCHEMA-02: Recognized experimental key with non-bool value is rejected with a typed error.

When an `experimental:` block contains a recognized key paired
with a non-boolean value (e.g. a string or integer),
`validateExperimental` MUST return a non-nil error containing
both the key name and the Go type of the bad value (e.g.
`"experimental.amd_overdrive: expected bool, got string"`).
The catalog load MUST fail and return that error. A string
where a bool is expected indicates a YAML authoring error;
silently coercing would mask the mistake and leave the field
at its zero value.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue

## RULE-EXPERIMENTAL-SCHEMA-03: Unknown experimental key with Levenshtein distance ≤ 2 from a known key is rejected as a likely typo with a suggestion.

When an `experimental:` block contains an unrecognized key
whose Damerau-Levenshtein distance to the nearest recognized
key is ≤ 2, `validateExperimental` MUST return a non-nil error
containing the text `"Did you mean:"` followed by the closest
recognized key. The catalog load MUST fail. Distance ≤ 2
covers one-character substitutions, insertions, deletions, and
transpositions — the full typo space for short identifiers.
Accepting such a key silently would leave the intended feature
disabled with no indication to the catalog author.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion

## RULE-EXPERIMENTAL-SCHEMA-04: Unknown experimental key with Levenshtein distance > 2 is accepted with a one-shot WARN; subsequent occurrences of the same key are silently ignored.

When an `experimental:` block contains an unrecognized key
whose Damerau-Levenshtein distance to every recognized key is
> 2, `validateExperimental` MUST accept the entry (not fail),
log exactly one `slog.LevelWarn` message containing the text
`"unknown key ignored"` for that key, and suppress all
subsequent WARN emissions for the same key within the process
lifetime. This is the forward-compat shim for v1.3+ catalog
entries loaded on a v1.2 ventd binary. The test injects a
`slog.Handler` via `slog.SetDefault` to capture the WARN,
calls the loader twice with the same unknown key, and asserts
exactly one WARN and no error.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce

## RULE-EXPERIMENTAL-SCHEMA-05: Absent experimental block behaves identically to an all-false ExperimentalBlock (v1.1 behavior preserved).

When a driver or board profile has no `experimental:` key, the
loaded profile's `Experimental` field MUST be the zero value of
`ExperimentalBlock` (all four fields false). No error is
returned. This preserves exact v1.1 matching behavior for all
existing catalog entries that pre-date the v1.2 schema
amendment. A catalog that adds `experimental:` support for new
entries MUST NOT break loading of older entries that never set
the field.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1

## Merge (board + driver overlay)

## RULE-EXPERIMENTAL-MERGE-01: CatalogMatch.ExperimentalEligibility OR-merges experimental flags from board and driver profiles.

`CatalogMatch.ExperimentalEligibility()` MUST return an
`ExperimentalBlock` where each field is `true` if and only if
the corresponding field is `true` in EITHER
`m.Board.Experimental` OR `m.Driver.Experimental`. A feature
asserted true by the board profile alone, by the driver
profile alone, or by both is eligible; a feature false in both
is not eligible. Either pointer may be nil (absent match); a
nil pointer contributes all-false. The test fixture constructs
a `CatalogMatch` with a board profile asserting
`ilo4_unlocked: true` and a GPU driver profile asserting
`nvidia_coolbits: true`, calls `ExperimentalEligibility()`, and
asserts that both fields are true while `amd_overdrive` and
`idrac9_legacy_raw` remain false.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_ExperimentalEligibility_OrsBoardAndGPU

## Framework (CLI/config plumbing)

## RULE-EXPERIMENTAL-FLAG-PRECEDENCE: CLI flags override config-file values; OR-merge satisfies CLI > config > default for additive boolean flags.

`experimental.Merge(cli, cfg Flags) Flags` computes each
output field as `cli.Field || cfg.Field`. Because `flag.Bool`
cannot distinguish an explicit `--enable-*=false` from the
default false, OR-merge is the correct rule: a flag enabled by
either source is active; a flag disabled by both sources is
inactive. This satisfies CLI > config > default for all four
experimental flags. The test fixture verifies: CLI-true beats
config-false, config-true propagates when CLI is false,
both-true yields true, both-false yields false, and multiple
flags are merged independently.

Bound: internal/experimental/parse_test.go:TestMerge_PrecedenceCLIOverConfig

## RULE-EXPERIMENTAL-HWDIAG-PUBLISHED: Publish sets one hwdiag entry per active flag under ComponentExperimental.

`experimental.Publish(store *hwdiag.Store, flags Flags)` MUST
call `store.Set` exactly once for each name in
`flags.Active()`, using `hwdiag.ComponentExperimental` as the
component, an ID of `"experimental.<name>"`, and
`hwdiag.SeverityInfo` as the severity. Inactive flags (false)
MUST NOT produce any entry. The test fixture calls Publish
with two active flags and asserts that the store snapshot for
ComponentExperimental contains exactly two entries with the
correct IDs. A zero-flag call must produce an empty snapshot.

Bound: internal/experimental/hwdiag_test.go:TestExperimental_HwdiagEntryPublished

## RULE-EXPERIMENTAL-DIAG-INCLUSION: Snapshot encodes active flags and all-flags precondition status for the diagnostic bundle.

`experimental.Snapshot(flags Flags) DiagSnapshot` MUST return
a `DiagSnapshot` where:
- `Active` contains the names of currently enabled flags in
  canonical order (matching `flags.Active()`).
- `Preconditions` is a map keyed by every name in `All()` (all
  four canonical names), with each value containing the `Met`
  and `Detail` fields from `Check(name)`.

When no flags are active, `Active` is empty and
`Preconditions` still contains all four keys.
`CollectExperimental(flags Flags)` in `internal/diag/detection`
calls `Snapshot` and encodes the result as
`experimental-flags.json` in the bundle. The
`diag.Options.ExperimentalFlags` field is threaded through
`Generate` to `CollectExperimental` so the snapshot reflects
the resolved flags.

Bound: internal/experimental/diag_test.go:TestDiag_SnapshotIncludesActiveAndPreconditions

## RULE-EXPERIMENTAL-STARTUP-LOG-ONCE: LogActiveFlagsOnce emits at most one INFO log per 24h; no log when no flags are active.

`experimental.LogActiveFlagsOnce(flags Flags, statePath string, logger *slog.Logger, now func() time.Time)`
MUST:
- Emit nothing and create no state file when `flags.Active()`
  is empty.
- On first call (no state file), emit one `slog.LevelInfo`
  log listing active flags and write the current timestamp
  (RFC3339) to `statePath`.
- Suppress the log when the state file records a timestamp
  within the last 24h (suppression window).
- Re-emit the log and update the state file when the state
  file timestamp is older than 24h.

The test fixtures cover: first-run emission, within-window
silence, after-window re-emission, and zero-flag silence. The
`now` parameter is injected for deterministic time control in
tests.

Bound: internal/experimental/startup_log_test.go:TestStartupLog_FirstRunEmits

## AMD OverDrive (specific feature)

## RULE-EXPERIMENTAL-AMD-OVERDRIVE-01: All AMD GPU HAL write paths return ErrAMDOverdriveDisabled when AMDOverdrive flag is false.

`CardInfo.WritePWM` and `CardInfo.WriteFanCurveGated` MUST
return `ErrAMDOverdriveDisabled` when `CardInfo.AMDOverdrive`
is false, regardless of any other card state (RDNA generation,
pwm1 presence, fan_curve presence). No bytes are written to
any sysfs file. The `AMDOverdrive` field mirrors the
`--enable-amd-overdrive` CLI flag and is set by the GPU
registry on every enumerated card. This gate ensures that
enabling amd_overdrive is an explicit operator decision — AMD
GPU fan control via sysfs may require the `0x4000` OverDrive
bit in `amdgpu.ppfeaturemask`, which taints the kernel on
Linux 6.14+.

Bound: internal/hal/gpu/amdgpu/overdrive_test.go:TestAMDGPU_WriteRefusesWhenOverdriveFlagFalse

## RULE-EXPERIMENTAL-AMD-OVERDRIVE-02: Precondition check parses /proc/cmdline for the OverDrive bit (0x4000) and returns an actionable detail when unset.

`checks.CheckAMDOverdrivePrecondition(cmdlinePath string)`
MUST return `(true, detail)` when `amdgpu.ppfeaturemask` is
present in `/proc/cmdline` AND bit 14 (0x4000) is set in the
parsed value. When the parameter is absent or bit 14 is unset,
it MUST return `(false, detail)` where `detail` contains: the
text `"0x4000"`, the word `"reboot"` (or equivalent
remediation guidance), and the current mask value in hex if
parseable. The detail string is surfaced verbatim in `ventd
doctor` output and in the diagnostic bundle
`experimental-flags.json`; an unactionable detail (e.g. empty
string) prevents the operator from knowing what kernel
parameter to add.

Bound: internal/experimental/checks/amd_overdrive_test.go:TestAMDOverdrive_PreconditionFailsActionableWhenBitUnset

## RULE-EXPERIMENTAL-AMD-OVERDRIVE-04: RDNA4 (Navi 48, PCI 0x7550) fan_curve writes are refused on kernel < 6.15.

`checkRDNA4KernelGate(cardPath, osReleasePath string)` MUST
return `ErrRDNA4NeedsKernel615` when `IsRDNA4(cardPath)`
returns true AND the running kernel version is below 6.15 (as
parsed from `/proc/sys/kernel/osrelease`). The gate is applied
inside `CardInfo.WriteFanCurveGated` after the `AMDOverdrive`
flag check. Non-RDNA4 cards (PCI device IDs not in the
`rdna4DeviceIDs` map) are unaffected regardless of kernel
version. RDNA4 on kernel ≥ 6.15 is permitted. This prevents
writes to the `fan_curve` interface on kernels that do not yet
expose the RDNA4 fan_curve sysfs path (merged in kernel 6.15
via drm/amdgpu commit for Navi 48).

Bound: internal/hal/gpu/amdgpu/rdna4_test.go:TestAMDGPU_RDNA4RefusesOnKernelBelow615
