# RULE-DIAG-PR2C-06: Architecturally-excluded paths are never captured, even with --redact=off.

The paths listed in the capture denylist MUST never be read or included in any bundle,
regardless of redaction profile or CLI flags. The denylist is hardcoded (not configurable)
and includes: `/etc/shadow`, `/etc/sudoers`, `/etc/sudoers.d/`, SSH key files
(`/root/.ssh/`, `/home/*/.ssh/`), TLS private key files (`*.key`, `*.pem` containing
`PRIVATE KEY`), `/proc/<pid>/environ` for any process, shell history files
(`~/.bash_history`, `~/.zsh_history`), D-Bus session credentials, and kernel keyring
contents (`/proc/keys`). The enforcement is architectural: the bundle generator's capture
allowlist does not include these paths. No redactor primitive is asked to scrub them
because they are never collected. The test fixture verifies that an attempt to add a
denylist path via `detection.AddFile` returns `ErrDenied` regardless of the redaction
profile in effect.

Bound: internal/diag/bundle_test.go:TestRuleDiagPR2C_06/denylist_paths_never_captured
<!-- rulelint:allow-orphan -->
