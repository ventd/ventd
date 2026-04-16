# Hardware Safety Rules

These are non-negotiable. Violating these can damage hardware.

- NEVER write PWM=0 to any fan unless the config explicitly sets min_pwm=0 AND the fan is marked allow_stop: true
- ALWAYS clamp PWM writes to [min_pwm, max_pwm] range defined in the fan config
- ALWAYS check pwm_enable mode before writing PWM values — if pwm_enable != 1 (manual), set it first
- Watchdog.Restore() must be called on ANY exit path — signal, panic, error, context cancellation
- When reading sysfs values, handle ENOENT (device disappeared) and EIO (driver error) gracefully — log and skip, don't crash
- Pump fans (is_pump: true) have a hard floor at pump_minimum — never write below it
- Calibration must be interruptible — if the user sends SIGINT during calibration, restore original PWM immediately
- hwmon indices (hwmon0, hwmon1...) are NOT stable across reboots — always resolve via hwmon_device path
