# RULE-CALIB-PR2B-12: Store.Filename produces "<dmi_fingerprint>-<bios_version_safe>.json"; non-alphanumeric chars in the BIOS version are replaced with hyphens.

`Store.Filename(fingerprint, biosVersion)` sanitises `biosVersion` by replacing every
character outside `[a-zA-Z0-9]` with a hyphen, collapsing consecutive hyphens, and producing
the filename `<fingerprint>-<safe>.json`. BIOS version strings from the field include spaces,
slashes, parentheses, and dots (e.g. "ASUS 0805 (04/26/2026)"); these must not appear in
filenames on any Linux filesystem. The test fixture verifies that "ASUS 0805 (04/26/2026)"
produces "ASUS-0805-04-26-2026" as the safe component. A consistent, predictable filename
format lets `Store.Load` reconstruct the path from the same inputs without a directory scan.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/store_filename_format
