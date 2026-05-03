# Modprobe-options-write endpoint rules — v0.5.11 R28 Stage 1

These invariants govern the `/api/hwdiag/modprobe-options-write`
endpoint and its supporting helpers in `internal/hwmon/modprobe_options.go`.
The endpoint applies a kernel-module option fix (e.g. `thinkpad_acpi
fan_control=1`) by writing a one-line drop-in to
`/etc/modprobe.d/ventd-<module>.conf` and reloading the module.

The endpoint is wired into the wizard-recovery classifier as the
`action_url` for `ClassThinkpadACPIDisabled` (RULE-WIZARD-RECOVERY-10).
The classifier card surfaces the failure; this endpoint applies the
fix the operator clicks.

Each rule below binds 1:1 to a subtest. `tools/rulelint` blocks the
merge if a rule lacks its bound subtest.

## RULE-MODPROBE-OPTIONS-01: IsAllowedModprobeOption gates the closed allowlist; arbitrary (module, options) pairs are refused.

`hwmon.IsAllowedModprobeOption(module, options string) bool` is the
single authoritative gate over the modprobe-options drop-in writer.
The allowlist is keyed by module name; each module maps to a closed
set of literal option strings. Stage 1A ships exactly one entry:
`thinkpad_acpi → fan_control=1`. The match is byte-exact: stray
whitespace, semicolons, or differing values (e.g. `fan_control=0`)
do NOT match. The `/api/hwdiag/modprobe-options-write` handler MUST
call `IsAllowedModprobeOption` BEFORE invoking the writer; future
Stage-1 entries (it87 `ignore_resource_conflict=1`, it87
`force_id=0xNNNN`) extend the allowlist alongside their catalog
rows in their own PRs.

Bound: internal/hwmon/modprobe_options_test.go:TestIsAllowedModprobeOption

## RULE-MODPROBE-OPTIONS-02: WriteModprobeOptionsDropIn is idempotent on identical content; rewrites only on option change.

`hwmon.WriteModprobeOptionsDropIn(path, module, options string)
error` MUST write `options <module> <options>\n` exactly when the
existing file content differs. If the file already contains the
target line byte-exact, the writer MUST NOT touch the file (no
mtime bump, no inode change). Re-clicking the wizard's "Enable
thinkpad_acpi fan_control" button on a system where the drop-in
is already correct must be a no-op rather than a state mutation.

Bound: internal/hwmon/modprobe_options_test.go:TestWriteModprobeOptionsDropIn

## RULE-MODPROBE-OPTIONS-03: Endpoint refuses non-POST requests AND non-allowlisted (module, options) pairs with HTTP 400.

`/api/hwdiag/modprobe-options-write` MUST reject:

- Any non-POST method with HTTP 405 (matches the rest of the
  hwdiag-install endpoint contract).
- A POST with empty body, malformed JSON, an unknown module, an
  empty module/options pair, an option value not on the allowlist,
  or shell-injection bait (`;rm -rf /`) embedded in the options
  string with HTTP 400.

The 400 path is reachable BEFORE any filesystem write, so a
crafted request from a hijacked session cannot create or rewrite
arbitrary modprobe.d files outside the allowlist's narrow scope.

Bound: internal/web/hwdiag_install_test.go:TestModprobeOptionsWrite_AllowlistEnforced
Bound: internal/web/hwdiag_install_test.go:TestInstallEndpointsRejectGET
