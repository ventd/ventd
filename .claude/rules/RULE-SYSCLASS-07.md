# RULE-SYSCLASS-07: Every system class produces at least one evidence string in Detection.Evidence.

`Detection.Evidence` MUST be a non-empty slice for every non-Unknown class returned by
`detectWithDeps`. Each class has at least one canonical evidence string: ClassNASHDD has
`"rotational_disk"` and `"pool_detected"`, ClassMiniPC has `"no_controllable_channels"`,
ClassLaptop has `"battery_detected"`, ClassServer has `"bmc_detected"`, ClassHEDTAIO has
`"liquid_cooler_channel"`, ClassHEDTAir has `"hedt_cpu"`, ClassMidDesktop has
`"controllable_channels"`. Evidence strings are logged at INFO level on daemon start and
surfaced in `ventd doctor` output; an empty slice prevents the operator from understanding
why a class was chosen, making misclassification debugging impossible.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_07_EvidenceCompleteness
