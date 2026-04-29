# RULE-SYSCLASS-01: System class precedence order is NAS > MiniPC > Laptop > Server > HEDT > MidDesktop > Unknown.

`detectWithDeps(d deps, r *probe.ProbeResult)` evaluates system class signals in a fixed
priority chain: NAS (rotational drive + pool) is checked first, MiniPC (no controllable
channels and N-series CPU) second, Laptop (battery present or chassis DMI type) third,
Server (BMC present or server-class CPU) fourth, HEDT-AIO/HEDT-Air fifth,
MidDesktop (any controllable channels) sixth, Unknown last. A result that matches multiple
classes resolves to the highest-priority class — e.g. a NAS with a battery (docking station)
is ClassNASHDD, not ClassLaptop. Incorrect ordering would misclassify machines whose
hardware straddles two categories, producing the wrong Envelope C tuning.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_01_PrecedenceOrder
