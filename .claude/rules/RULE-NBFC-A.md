# NBFC catalog + matcher + doctor card rules — spec-09 PR A

These invariants govern the read-only catalogue surface introduced
by spec-09 PR A: the vendored upstream nbfc-linux JSON corpus + the
DMI matcher + the doctor detector. No EC writes, no privileged code
paths, no behaviour change on hosts that don't match a catalog entry.

Each rule binds 1:1 to a subtest. `tools/rulelint` blocks the merge
if a rule lacks its bound test.

## RULE-NBFC-CATALOG-01: A malformed embedded config aborts daemon start with the offending filename named.

`nbfc.LoadCatalogFS` parses every `configs/*.json` under the embedded
filesystem and returns an error wrapping the first failing file
path. A skip-on-error catalog half-load would leave the doctor
surface silently incomplete and the operator with no signal that
a config file shipped corrupted. The bound test feeds a two-file
fixture (one good, one with a JSON syntax error) and asserts the
error mentions the failing filename verbatim.

The loader also rejects parsed configs whose `NotebookModel` is
empty after whitespace trim; that case is bound separately and
treated structurally identically (catalogue load fails, daemon
proceeds to log the breadcrumb without starting fan control).

Bound: internal/hwdb/nbfc/embed_test.go:TestLoadCatalogFS_RejectsMalformedJSON
Bound: internal/hwdb/nbfc/embed_test.go:TestLoadCatalogFS_RejectsEmptyNotebookModel

## RULE-NBFC-CATALOG-02: `Match(catalog, dmi)` is pure — same inputs always produce the same `(*Entry, MatchTier)`.

The matcher's three-tier dispatch (Exact → Prefix → Substring →
None) is byte-deterministic. No package-level state, no time-of-
day branches, no randomness. The bound subtest invokes Match 100
times against an identical DMI tuple and asserts every result is
identical to the first.

Purity matters because `Match` is consumed by the doctor detector
(which runs every 60s on the daemon's periodic ticker per
spec-10's runner cadence) and by future spec-09 PR B2 `Probe`
which runs once at backend construction; both need the catalog
match to resolve consistently regardless of when called.

Bound: internal/hwdb/nbfc/match_test.go:TestMatch_DeterministicPure
Bound: internal/hwdb/nbfc/match_test.go:TestMatch_ExactProductName
Bound: internal/hwdb/nbfc/match_test.go:TestMatch_PrefixGlob
Bound: internal/hwdb/nbfc/match_test.go:TestMatch_CaseFoldedExact
Bound: internal/hwdb/nbfc/match_test.go:TestMatch_NoMatchReturnsNone
Bound: internal/hwdb/nbfc/match_test.go:TestMatch_NilCatalogReturnsNone

## RULE-NBFC-CATALOG-03: Control-mode classification is exhaustive — every embedded config classifies into one of the four `ControlMode` constants.

`classifyControlMode` inspects both the parsed config (typed) and
the raw JSON (substring scan) so a config that introduces a Lua
or ACPI field via a key the schema doesn't model yet still
classifies into the right bucket. The forward-compat fence: a new
upstream config that requires a fifth control mode fails the
bound subtest before it can silently land in a catalogue sync.

Lua takes precedence over ACPI when a config mixes both — Lua is
the most-blocking refusal in v0.8.0 (no runtime); the operator
needs to see that first.

Bound: internal/hwdb/nbfc/embed_test.go:TestCatalog_AllControlModesAccountedFor
Bound: internal/hwdb/nbfc/embed_test.go:TestClassifyControlMode_Register
Bound: internal/hwdb/nbfc/embed_test.go:TestClassifyControlMode_Register16
Bound: internal/hwdb/nbfc/embed_test.go:TestClassifyControlMode_ACPI
Bound: internal/hwdb/nbfc/embed_test.go:TestClassifyControlMode_LuaBeatsACPI

## RULE-NBFC-DOCTOR-01: The NBFC match detector emits exactly one Fact per matched DMI; the severity and detail change with the resolved control mode + match tier.

`NBFCMatchDetector.Probe` emits:

- **Severity OK** when the match resolved to a register-only or
  ACPI control mode — both are in the spec-09 v0.8.0 scope. The
  Detail names the upstream `NotebookModel`, the source filename,
  the control mode, and (when match tier is non-Exact) the
  upstream stem vs the live ProductName.
- **Severity Warning** when the match resolved to a Lua-driven
  config (0/311 catalogue entries today; the slot exists for
  forward-compat). Lua is structurally refused in v0.8.0; the
  doctor card names the refusal explicitly so the operator's
  expectation is set.
- **Severity Warning** when no catalog entry matches the live DMI.
  Detail names the live ProductName / BoardName / SysVendor and
  the upstream-contribution URL (the `nbfc-linux/nbfc-linux`
  Configuration HowTo).

Quiet (zero facts) on hosts where `ControllableChannelCount > 0`
— smart-mode applies to that case, and the doctor surface there
is covered by other detectors (hwmon_swap, dkms_status, etc.).

Graceful-degrade Warning fact when DMI read fails (RULE-DOCTOR-04
pattern) or the catalogue itself fails to load. Neither path can
crash doctor; both surface actionable Detail.

Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestRULE_NBFC_DOCTOR_01_RegisterMatchEmitsOK
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestRULE_NBFC_DOCTOR_01_ACPIMatchEmitsOKWithACPIDetail
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestRULE_NBFC_DOCTOR_01_LuaMatchEmitsWarning
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestRULE_NBFC_DOCTOR_01_NoMatchEmitsContributionInvite
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestNBFCMatch_DesktopWithChannelsNoFact
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestNBFCMatch_DMIReadErrorGracefullyDegrades
Bound: internal/doctor/detectors/nbfc_match_d_test.go:TestNBFCMatch_RespectsContextCancel

## RULE-NBFC-CATALOG-JSONC-01: The loader normalises JSON5-style constructs upstream nbfc-linux's C parser accepts: line / block comments, trailing commas, hex literals (`0xNN`), and leading-zero numbers.

`stripJSONComments` (with `stripTrailingCommas` + `rewriteHexLiterals`
+ `stripLeadingZeros` pipeline) makes the upstream corpus parseable
under Go's strict `encoding/json`. The pipeline is string-aware —
every transform walks the bytes tracking quote / escape state, so
literal sequences inside string values survive intact (an Author
named `"O'Neill"` with an apostrophe, a Description containing
`"// not a comment"`, etc.).

Phoenix's PR A spike caught four catalogue files needing this:
Acer Nitro V15-41 (line comments), Acer Predator PH315-54 (block
comments + trailing comma post-strip), Asus ROG G75VX (line
comments), Alienware m15 R3 (hex literals), HP Pavilion dk15
(leading-zero numbers). The full embedded `Catalog.Size()` test
fails fast if a future upstream sync introduces a fifth quirk
this pipeline doesn't handle.

Bound: internal/hwdb/nbfc/embed_test.go:TestLoadCatalog_EmbeddedFS_ParsesAllConfigs
Bound: internal/hwdb/nbfc/embed_test.go:TestRawStringOrArray_Unmarshal
