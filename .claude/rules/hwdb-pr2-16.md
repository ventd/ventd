# RULE-HWDB-PR2-16: Driver profile `blacklist_before_install` rejects empty entries and duplicates.

Schema v1.3 introduces the optional `blacklist_before_install: [<module>, ...]`
field on driver profiles. Phoenix's MS-7D25 IT8688E→NCT6687D incident proved
that the install path sometimes needs to blacklist a conflicting in-tree
driver before the OOT module can bind. Generalising that pattern: when a
driver profile lists `blacklist_before_install: [nct6683]`, the install path
writes `blacklist nct6683` to `/etc/modprobe.d/ventd-<driver>.conf` and runs
`modprobe -r nct6683` before invoking the target driver's modprobe.

`validateDriverProfile` rejects:
- An empty entry in the slice (a whitespace-only or zero-length module name).
- Duplicate entries (the same module listed twice on the same driver).

The slice may be absent or empty without error; both indicate "no conflicting
modules to blacklist".

The test fixture exercises the happy path (two distinct modules load
cleanly), plus the empty-entry and duplicate-entry rejection cases.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_16
