# RULE-DIAG-PR2C-04: Redactor mapping file is created with mode 0600.

The persistent mapping store at `/var/lib/ventd/redactor-mapping.json` (root-mode) or
`$XDG_STATE_HOME/ventd/redactor-mapping.json` (user-mode) MUST be created with
`os.OpenFile(..., os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)` and the mode MUST be
verified via `os.Stat` after the file is written and closed. A mapping file created with
a permissive umask (e.g. 0644) exposes the cleartext-to-obfuscated mapping — which is
the de-redaction key — to any user on the system. The stat-verify step catches the
failure class where a caller creates the file first (0644 by default) and then writes to
it; `OpenFile` with explicit mode must be used from the start, not chmod-after-write.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_04/mapping_file_mode_0600
<!-- rulelint:allow-orphan -->
