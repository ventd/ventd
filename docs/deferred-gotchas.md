# Deferred hwmon gotchas

From hwmon-research.md §17 gap analysis 2026-04-24.

## Post-v1.0 (laptop hardware)
- §17.12 ThinkPad watchdog periodic re-assert (thinkpad-acpi)
- §17.13 Dell consumer laptop BIOS SMM rewrite (Inspiron/Latitude/Vostro)

## Deferred pending feature
- §17.17 pwm_auto_point 0- vs 1-based indexing — revisit when hardware-curve upload ships (post-v0.5.0)
- §17.21 step_wise thermal governor hysteresis — revisit if ventd ever registers DT cooling-maps
