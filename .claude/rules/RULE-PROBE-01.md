# RULE-PROBE-01: Probe MUST be entirely read-only — no PWM writes, no IPMI commands, no EC commands.

`Prober.Probe()` reads hardware state from injected `fs.FS` values (SysFS, ProcFS, RootFS)
and from external commands via `ExecFn`. The only write-adjacent operation is the
`WriteChecker`, which opens a sysfs PWM path `O_WRONLY` and immediately closes it — no
data bytes are written. In tests, a stub `WriteChecker` is injected so no real file
descriptors are opened. All other I/O is `fs.ReadFile` / `fs.ReadDir` on the injected FS
or trimmed stdout from `ExecFn`. A Probe that writes a PWM value or issues an IPMI command
could alter fan state before the operator has consented to ventd taking control.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-01_read_only
