# RULE-EXPERIMENTAL-HWDIAG-PUBLISHED: Publish sets one hwdiag entry per active flag under ComponentExperimental.

`experimental.Publish(store *hwdiag.Store, flags Flags)` MUST call `store.Set` exactly once for
each name in `flags.Active()`, using `hwdiag.ComponentExperimental` as the component, an ID of
`"experimental.<name>"`, and `hwdiag.SeverityInfo` as the severity. Inactive flags (false) MUST
NOT produce any entry. The test fixture calls Publish with two active flags and asserts that the
store snapshot for ComponentExperimental contains exactly two entries with the correct IDs.
A zero-flag call must produce an empty snapshot.

Bound: internal/experimental/hwdiag_test.go:TestExperimental_HwdiagEntryPublished
