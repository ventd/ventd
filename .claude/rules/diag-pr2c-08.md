# RULE-DIAG-PR2C-08: Redaction mapping is consistent within a bundle (same input → same output).

Within a single bundle generation run, each primitive that uses consistent-mapping (P1
hostname, P3 MAC, P4 IP, P5 username) MUST map the same cleartext value to the same
obfuscated token every time it appears, across all files in the bundle. The consistency
is enforced by the shared mapping store passed to every primitive. The test fixture feeds
the same hostname string to the P1 primitive from two different simulated detection
outputs and asserts that both are replaced with the identical `obf_host_1` token.
Inconsistent mapping (e.g., two different tokens for the same hostname in different files)
destroys the analytical utility of the bundle — a support engineer can no longer determine
whether two references point to the same machine.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_08/mapping_consistent_within_bundle
<!-- rulelint:allow-orphan -->
