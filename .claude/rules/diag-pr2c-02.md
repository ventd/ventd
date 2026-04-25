# RULE-DIAG-PR2C-02: Self-check pass detects un-redacted hostname strings in the final bundle.

After the bundle tarball is fully assembled, `SelfCheck` MUST scan every file in the
archive for the literal hostname string (as returned by `os.Hostname()` at bundle-start
time). If ANY occurrence of the cleartext hostname is found in any file (regardless of
whether the redactor reported redacting it), `SelfCheck` MUST return a non-nil error
listing the file(s) and byte offsets. The self-check is performed on the assembled
tarball content before the file handle is closed. A self-check that passes when the
hostname is present — e.g. because the redactor missed a file, or a detection script
added content after the redactor ran — produces a bundle with a false `redaction_consistent`
flag and violates user trust.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_02/self_check_detects_hostname_leak
<!-- rulelint:allow-orphan -->
