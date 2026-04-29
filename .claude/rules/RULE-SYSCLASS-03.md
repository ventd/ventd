# RULE-SYSCLASS-03: Ambient sensor identification uses a three-step fallback chain: labeled → lowest-at-idle → 25°C constant.

`identifyAmbient(sources []probe.ThermalSource) float64` applies three steps in order:
(1) Return the reading from any sensor whose label matches an ambient keyword (`"ambient"`,
`"inlet"`, `"room"`, `"case"`, etc.) — the labeled-sensor step.
(2) If no labeled sensor is found, return the reading from the non-CPU, non-GPU sensor
with the lowest value at idle — the lowest-at-idle step.
(3) If no admissible sensor is found (all sensors blocked by the admissibility blocklist,
or the list is empty), return the constant 25.0°C — the fallback step.
A system without any ambient sensor still gets a sensible ambient for Envelope C curve
parameterisation; a constant is preferable to an error that aborts the calibration run.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_03_AmbientFallbackChain
