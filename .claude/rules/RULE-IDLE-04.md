# RULE-IDLE-04: PSI is the primary load signal when /proc/pressure/ is available; /proc/loadavg is the fallback.

`Capture(deps snapshotDeps)` calls `PSIAvailable(procRoot)` which checks whether
`/proc/pressure/cpu` exists. When available, `Snapshot.PSI` is populated from
`cpu.some avg60`, `io.some avg60`, and `memory.full avg60`; `evalPredicate` uses the PSI
fields as the primary workload signal. When `/proc/pressure/` is absent (kernel < 4.20 or
CONFIG_PSI=n), `captureLoadAvg` reads `/proc/loadavg` and the first three fields are used
as the fallback signal. Using PSI as primary is correct because PSI measures actual CPU,
IO, and memory pressure rather than queue length; a system with many sleeping tasks can
show high load average but zero PSI, and must not be refused.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_04_PSIPrimaryFallback
