# RULE-GPU-PR2D-01: All GPU writes are gated behind --enable-gpu-write flag AND per-device capability probe success.

Default daemon mode is read-only enumeration and RPM reporting. A GPU fan write is only
dispatched when (a) the `--enable-gpu-write` runtime flag is set on the ventd command line
AND (b) the per-device capability probe returns a writable capability (`rw_full` or
`rw_quirk`). Either condition false causes the backend to return `ErrWriteGated` with a
message identifying which gate failed. The capability probe is the load-bearing safety
constraint — it gates on real NVIDIA driver-version requirements (`RULE-POLARITY-06`:
R515+ required for `nvmlDeviceSetFanSpeed_v2`) — not on HIL evidence accumulation. The
v0.6.1 sweep removed the HIL-style `--unsafe-corsair-writes` and `--enable-nbfc-write`
gates (see `RULE-LIQUID-06` + `RULE-NBFC-HAL-DEFAULT-WRITES-ON`) but left this gate in
place because the underlying driver-version constraint is genuine.

Bound: internal/hal/gpu/nvml/probe_test.go:TestGPU_WriteGated
