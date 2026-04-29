# RULE-IDLE-02: Battery-powered operation (AC offline or BAT discharging) is a hard refusal — AllowOverride has no effect.

`evalPredicate(snap *Snapshot, cfg GateConfig) (bool, Reason)` returns
`(false, ReasonOnBattery)` when `snap.OnBattery` is true, regardless of
`cfg.AllowOverride`. `RuntimeCheck` propagates the same refusal. Detection reads
`/sys/class/power_supply/AC*/online` (AC offline → on battery) and
`/sys/class/power_supply/BAT*/status` (value `"Discharging"` → on battery). Envelope C
calibration sweeps the fan PWM across its full range and requires mains power; running it
on battery causes premature shutdown mid-sweep, partial calibration records, and fan curves
shaped by thermal transients from battery discharge itself. The hard refusal cannot be
suppressed by any flag because the risk is physical, not operational.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_02_BatteryRefusal
