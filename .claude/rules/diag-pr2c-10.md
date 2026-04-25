# RULE-DIAG-PR2C-10: Bundle output directory has mode 0o700; bundle file has mode 0o600. Both verified post-write.

The bundle output directory (resolved per §15.5 output-dir precedence) MUST be created
with `os.MkdirAll(dir, 0o700)`. The bundle file MUST be created with
`os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)`. After the file is
written and closed, BOTH the directory mode (via `os.Stat(dir).Mode().Perm()`) and the
file mode (via `os.Stat(path).Mode().Perm()`) MUST be verified to equal 0o700 and 0o600
respectively. A mismatch — possible when a restrictive or permissive umask overrides the
requested mode on some filesystems — is treated as a fatal error and the bundle file is
removed. The test fixture creates the output in a temp dir and asserts both stat results
exactly.

Bound: internal/diag/bundle_test.go:TestRuleDiagPR2C_10/output_dir_and_file_modes
<!-- rulelint:allow-orphan -->
