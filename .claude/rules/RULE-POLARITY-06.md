# RULE-POLARITY-06: NVML polarity probe refuses channels whose driver version is below R515 (major < 515).

`NVMLProber.ProbeChannel` calls `NVMLFuncs.DriverVersion()` and parses the major version
component (e.g. "570.211.01" → 570). When `major < 515`, the probe returns a `ChannelResult`
with `Polarity = "phantom"` and `PhantomReason = PhantomReasonDriverTooOld` without
attempting any fan speed write. NVML `nvmlDeviceSetFanSpeed_v2` and
`nvmlDeviceGetFanControlPolicy_v2` are available from driver R515 onward; calling them on
older drivers produces an NVML_ERROR_FUNCTION_NOT_FOUND that cannot be cleanly recovered.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-06_nvml_driver_version_gate
