# Calibration Result Persistence Rules

These invariants govern `Manager.RemapKey`, the routine that migrates a
calibration result from one sysfs path to another when hwmonN indices change
across reboots. Violating them risks losing calibration data or applying a
curve shaped for the wrong fan.

Each rule below is bound to one subtest in `internal/calibrate/calibrate_test.go`.
If a rule text is edited, update the corresponding subtest in the same PR;
if a new rule lands, it must ship with a matching subtest or the rule-lint
in `tools/rulelint` blocks the merge.

## RULE-CAL-REMAP-MOVES: RemapKey atomically moves oldPath to newPath and rewrites the inner PWMPath

When oldPath exists in the calibration results map, `Manager.RemapKey(oldPath,
newPath)` must transfer the entry to newPath AND rewrite the `PWMPath` field
inside the result record to match. A remap that moves the map key but leaves
`PWMPath` pointing to the stale path produces a record that identifies itself
incorrectly, breaking any code that validates the stored path against the live
sysfs layout.

Bound: internal/calibrate/calibrate_test.go:remaps existing key

## RULE-CAL-REMAP-NOOP: RemapKey is a no-op when oldPath is absent

When oldPath is not present in the calibration results map,
`Manager.RemapKey(oldPath, newPath)` must return without mutating any entry.
An erroneous remap triggered by an unrelated hot-plug event could silently
overwrite or discard calibration data for a fan that was not renumbered.

Bound: internal/calibrate/calibrate_test.go:missing oldPath is a no-op

## RULE-CAL-REMAP-OVERWRITE: RemapKey overwrites newPath when both keys are present

When both oldPath and newPath exist in the calibration results map,
`Manager.RemapKey(oldPath, newPath)` must overwrite the newPath entry with
the oldPath record. After a reboot renumber, a daemon restart may have
started a fresh partial sweep at newPath before detecting the rename; the
previously-completed data stored under oldPath is more valuable and must win.

Bound: internal/calibrate/calibrate_test.go:both keys present: new overwritten with old
