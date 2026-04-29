# RULE-IDLE-03: Container environment is a hard refusal — AllowOverride has no effect.

`evalPredicate(snap *Snapshot, cfg GateConfig) (bool, Reason)` returns
`(false, ReasonInContainer)` when `snap.InContainer` is true, regardless of
`cfg.AllowOverride`. Container detection reads `/proc/1/cgroup` and looks for runtime
keywords (`docker`, `lxc`, `kubepods`, `garden`). Inside a container, `/sys/class/hwmon`
paths visible to ventd reflect the host kernel but may be write-protected or inaccessible
due to cgroup device permissions; a calibration sweep that writes to a PWM path in a
container either silently no-ops or panics the host kernel driver. The hard refusal cannot
be overridden because container isolation makes real fan-control verification impossible
from inside the container namespace.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_03_ContainerRefusal
