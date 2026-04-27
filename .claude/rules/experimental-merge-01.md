# RULE-EXPERIMENTAL-MERGE-01: CatalogMatch.ExperimentalEligibility OR-merges experimental flags from board and driver profiles.

`CatalogMatch.ExperimentalEligibility()` MUST return an `ExperimentalBlock` where each field
is `true` if and only if the corresponding field is `true` in EITHER `m.Board.Experimental`
OR `m.Driver.Experimental`. A feature asserted true by the board profile alone, by the driver
profile alone, or by both is eligible; a feature false in both is not eligible. Either pointer
may be nil (absent match); a nil pointer contributes all-false. The test fixture constructs a
`CatalogMatch` with a board profile asserting `ilo4_unlocked: true` and a GPU driver profile
asserting `nvidia_coolbits: true`, calls `ExperimentalEligibility()`, and asserts that both
fields are true while `amd_overdrive` and `idrac9_legacy_raw` remain false.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_ExperimentalEligibility_OrsBoardAndGPU
