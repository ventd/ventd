# NVML helper rules — SUID-root write-helper for unprivileged ventd

These invariants govern `cmd/ventd-nvml-helper/` and the dispatch
logic in `internal/nvidia/helper.go`. Issue #770 motivates the design:
the unprivileged `ventd` daemon cannot call NVIDIA's NVML write APIs
(`nvmlDeviceSetFanSpeed_v2`, `nvmlDeviceSetDefaultFanSpeed_v2`,
`nvmlDeviceSetFanControlPolicy`) because they require root euid. The
helper is installed SUID-root by the .deb / .rpm postinst and proxies
the write subcommands behind a strict input whitelist.

Each rule binds 1:1 to a subtest in `internal/nvidia/helper_test.go`.
A change to any rule requires updating the bound test in the same PR;
`tools/rulelint` blocks the merge otherwise.

## RULE-NVML-HELPER-RECURSION-01: needsHelper() returns false when euid==0, even with a helper binary present.

Without this guard, the SUID helper invocation chain would recurse:
helper runs (SUID → euid==0) → calls `nvidia.WriteFanSpeed` →
`needsHelper()` returns true → helper invokes itself → infinite loop
exhausting the process table. The guard tests `os.Geteuid() == 0`
first; only when running as a non-root euid does the dispatch
consider the binary's presence.

Bound: internal/nvidia/helper_test.go:TestNeedsHelper_RootBypasses

## RULE-NVML-HELPER-PRESENCE-01: needsHelper() returns false when the helper binary is absent.

Hosts that haven't been upgraded to a helper-shipping ventd version
must continue to function with the pre-helper direct-NVML behaviour.
When `os.Stat(helperPath())` returns an error (typically ENOENT),
`needsHelper()` returns false and the dispatch falls through to the
direct call — which will then return `Insufficient Permissions` on
non-root, surfacing #770 cleanly to the daemon log without silently
hanging.

Bound: internal/nvidia/helper_test.go:TestNeedsHelper_NoBinarySkips

## RULE-NVML-HELPER-PRESENCE-02: needsHelper() returns true when both euid != 0 AND the helper binary is present.

The positive path. Non-root euid + binary on disk → use the helper.
Tested with a no-op shell script at the path pointed to by
`VENTD_NVML_HELPER` env override.

Bound: internal/nvidia/helper_test.go:TestNeedsHelper_NonRootWithHelperPresent

## RULE-NVML-HELPER-ARGS-01: runHelper() passes args through to the helper subprocess in argv order, no shell expansion.

The helper expects positional args: `<subcommand> <gpu_idx>
<fan_idx?> <pct_or_policy?>`. runHelper invokes via `exec.Command`
with each argument as a separate argv entry — no shell escapes, no
glob expansion, no environment-variable substitution. A stub helper
that echoes its argv to a temp file confirms the exact args reach
the helper binary in the order the dispatch sent them.

Bound: internal/nvidia/helper_test.go:TestRunHelper_PassesArgsThroughEnv

## RULE-NVML-HELPER-ERR-01: runHelper() wraps the helper's stderr text into the returned error.

When the helper exits non-zero, the caller's error must include the
helper's stderr (NVML error string, "out of range" diagnostic, etc.)
so the daemon log captures the failure cause without scraping a
separate stream. Tested with a stub helper that prints a known
string to stderr and exits 1; the returned error must contain that
string.

Bound: internal/nvidia/helper_test.go:TestRunHelper_ErrorPropagation

## RULE-NVML-HELPER-EXIT-01: setFanControlPolicyViaHelper translates exit code 4 to (false, nil) — preserves the (supported, err) contract of the direct NVML path.

The direct `SetFanControlPolicy` returns `(true, nil)` on success
and `(false, nil)` when the driver doesn't expose the
`nvmlDeviceSetFanControlPolicy` symbol (R515-/R470 → rw_quirk cap).
The helper communicates the same outcome via exit code 4. The
dispatcher must unwrap the wrapped `*exec.ExitError`, check for
exit code 4, and translate to the `(false, nil)` form.

`runHelper` MUST wrap the underlying `*exec.ExitError` via `%w`
(not `%s`) so the unwrap path can find it. A regression that uses
`%s` instead of `%w` will silently turn "unsupported on this driver"
into a hard error, breaking forward-compat with older NVML drivers.

Bound: internal/nvidia/helper_test.go:TestExitCode_UnwrapsExitError

## RULE-NVML-HELPER-EXEC-01: errorsAs() unwraps wrapped *exec.ExitError chains via the Unwrap() interface.

The dispatcher avoids importing `errors` here for tight import
discipline; instead it uses a hand-rolled errorsAs helper that
walks the Unwrap() chain. The walker MUST handle:
(a) the direct `*exec.ExitError` case — returns true with the
    pointer assigned;
(b) wrapped chains via `fmt.Errorf("...: %w", exitErr)` — the
    Unwrap() interface walk recovers the original;
(c) non-matching errors — returns false without panic.

A regression that fails to unwrap chained ExitErrors breaks
RULE-NVML-HELPER-EXIT-01.

Bound: internal/nvidia/helper_test.go:TestErrorsAs_ManualUnwrap
