# RULE-IDLE-09: AllowOverride=true skips storage-maintenance refusal but never skips battery or container refusal.

`CheckHardPreconditions(procRoot, sysRoot string, allowOverride bool) HardPreconditions`
evaluates three hard conditions: `OnBattery`, `InContainer`, and `StorageMaintenance`.
When `allowOverride` is true, `StorageMaintenance` is set to false regardless of whether
`/proc/mdstat` shows an active RAID rebuild — the operator has explicitly acknowledged
the maintenance window risk. However, `OnBattery` and `InContainer` are always set from
the real hardware state regardless of `allowOverride`; the `Reason()` method returns
`ReasonOnBattery` or `ReasonInContainer` for those conditions even with the override flag
active. This asymmetry is intentional: storage maintenance is recoverable (array rebuilds
complete), but battery exhaustion and container isolation are physical blockers with no
safe workaround.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_09_OverrideNeverSkipsBatteryContainer
