# RULE-GPU-PR2D-01: All GPU writes are gated behind --enable-gpu-write flag AND per-device capability probe success.

Default daemon mode is read-only enumeration and RPM reporting. A GPU fan write is only
dispatched when (a) the `--enable-gpu-write` runtime flag is set on the ventd command line
AND (b) the per-device capability probe returns a writable capability (`rw_full` or
`rw_quirk`). Either condition false causes the backend to return `ErrWriteGated` with a
message identifying which gate failed. This mirrors the `--unsafe-corsair-writes` gate from
spec-02 (RULE-LIQUID-06) so operators apply the same mental model: explicit opt-in, per
backend, with firmware allowlist as the second gate.

Bound: internal/hal/gpu/nvml/probe_test.go:TestGPU_WriteGated
