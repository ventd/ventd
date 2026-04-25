# RULE-DIAG-PR2C-05: --redact=off requires interactive confirmation or --i-understand-this-is-not-redacted.

When `--redact=off` is passed without `--i-understand-this-is-not-redacted`, the CLI MUST
prompt the user interactively: print a warning message to stderr and read from stdin. The
bundle proceeds only if the user types the exact word `confirm` (case-sensitive). Any
other input (including empty, ^C, or a mistyped word) MUST abort bundle generation with
exit code 1. When both `--redact=off` AND `--i-understand-this-is-not-redacted` are
present together, the confirmation step is skipped and bundle generation proceeds without
any prompt. This is not a security gate — a determined user can trivially bypass it — but
a deliberate-action ratchet that prevents accidentally-unredacted bundles from being
produced when the user simply forgot to remove `--redact=off` from a debug session.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_05/off_requires_confirm_or_flag
<!-- rulelint:allow-orphan -->
