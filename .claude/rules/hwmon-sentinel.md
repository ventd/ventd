# hwmon Sentinel Acceptance Invariants

These rules complement the sentinel-rejection rules in hwmon-safety.md by
pinning the acceptance side: what values must pass through the sentinel filter
unchanged, and what additional values beyond the exact 0xFFFF sentinel must
be rejected. Together with the rejection rules they fully specify the
`IsSentinelSensorVal` and `IsSentinelRPM` contracts.

Each rule below is bound to one subtest in `internal/hal/hwmon/safety_test.go`.
If a rule text is edited, update the corresponding subtest in the same PR;
if a new rule lands, it must ship with a matching subtest or the rule-lint
in `tools/rulelint` blocks the merge.

## RULE-SENTINEL-FAN-IMPLAUSIBLE: fan RPM above the plausible cap is rejected even when not the exact 0xFFFF sentinel

`IsSentinelRPM` must reject any RPM value above `PlausibleRPMMax`
(25 000 since 2026-05-03; previously 10 000), not only the exact 65 535
(0xFFFF) nct6687 sentinel. The cap is set above any real-world fan
shipped today (consumer 120/140 mm ≤ 4 000 RPM, AIO pumps ≤ 6 500,
Delta/Sanyo Denki industrial server fans 12 000–22 000) and below the
0x7FFF / 0xFFFF mid-latch glitches some chips emit. A reading strictly
above the cap is treated as sentinel regardless of its exact value.

The 2026-05-03 raise (10 000 → 25 000) was an R28 audit P0 fix:
servers with Sanyo Denki fans had legitimate 18 000 RPM readings
silently rejected, surfacing as "stopped" in the dashboard.

Bound: internal/hal/hwmon/safety_test.go:sentinel/fan_rejects_implausible_rpm

## RULE-SENTINEL-FAN-VALID: a normal fan RPM reading passes through the sentinel filter unchanged

`IsSentinelRPM` must return false for RPM values within the plausible range
(≤ 25 000 RPM since 2026-05-03), and `Backend.Read` must return OK=true with
the correct RPM value. A filter that rejects legitimate readings would cause
calibration to report "no valid RPM data" and leave the fan under open-loop
control. The bound subtest covers both consumer-class (1 200 RPM) and
server-class (18 000 RPM) values to pin the new cap.

Bound: internal/hal/hwmon/safety_test.go:sentinel/fan_accepts_normal_rpm

## RULE-SENTINEL-TEMP-CAP: temperature at or above the 150°C plausibility cap is rejected

`IsSentinelSensorVal` must reject temperature readings at or above
`PlausibleTempMaxCelsius` (150°C), not only the exact 255.5°C (0xFFFF)
sentinel. Consumer silicon cannot operate at 150°C; a reading at or above
this threshold indicates a chip latch error or a driver sentinel in a
non-standard position. Letting such a value reach the curve would drive the
fan to MaxPWM on every tick.

Bound: internal/hal/hwmon/safety_test.go:sentinel/temp_rejects_above_plausible_cap

## RULE-SENTINEL-TEMP-FLOOR: temperature at or below absolute zero is rejected as a sensor latch / driver underflow

`IsSentinelSensorVal` must reject temperature readings at or below
`PlausibleTempMinCelsius` (−273.15°C). A reading at or below the absolute-
zero floor is physically impossible — it indicates a sensor latch error or a
signed/unsigned underflow in the driver (e.g. a driver returning the int32
sentinel `-2147483648` divided by 1000 = −2147483.648°C). Drivers historically
have no canonical "value unavailable" signal, so a sub-absolute-zero filter
is the defensive complement to the high-end `PlausibleTempMaxCelsius` cap.
Real-world degraded readings such as the Framework 13 AMD 7040 EC's −17°C
I2C bus underflow remain above this floor and pass through to the operator
UI for triage; only physically impossible values are filtered here.

Bound: internal/hal/hwmon/safety_test.go:sentinel/temp_rejects_sub_absolute_zero

## RULE-SENTINEL-TEMP-VALID: a normal temperature reading passes through the sentinel filter unchanged

`IsSentinelSensorVal` must return false for temperature readings in the
normal operating range (below 150°C and not matching the 0xFFFF sentinel).
A 45°C CPU temperature must reach the controller's sensor map intact. A
filter that produces false positives would cause the controller to carry
forward the last good PWM indefinitely, severing the thermal control loop.

Bound: internal/hal/hwmon/safety_test.go:sentinel/temp_accepts_normal_reading

## RULE-SENTINEL-VOLTAGE-VALID: a normal voltage reading passes through the sentinel filter unchanged

`IsSentinelSensorVal` must return false for voltage readings in the normal
PSU rail range (≤ 20 V). A 12 V reading (ATX 12V rail) must not be rejected.
A filter with a threshold set too aggressively would suppress legitimate PSU
rail monitoring data and blind the daemon to voltage anomalies that can
indicate hardware instability.

Bound: internal/hal/hwmon/safety_test.go:sentinel/voltage_accepts_normal_reading
