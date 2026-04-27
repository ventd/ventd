# Canonical data — Stage 1 reconciliation

All four mockups must match these values. The dashboard had 14 fans
but the devices page summary said 8 chips and 14 fans, while the nav
badge originally read "14" on dashboard and "8" on devices. Reconciled.

## Identity
- **Host:** `homelab-01`
- **Fingerprint:** `a4f2c8d1`
- **Daemon uptime:** `4d 12h`
- **Version:** `0.4.1`

## Hardware totals
- **Chips:** 8 (5 hwmon · 2 hidraw · 1 nvml)
- **Fans:** 14 (all calibrated)
- **Temps:** 21 (3 sources active)
- **Writable channels:** 12 / 14 (2 read-only — corsair write disabled)
- **Issues:** 1 (corsair write disabled)

## Nav badges
- **Devices:** `8` (chips count, not fans)
- All other nav items: no badge

## Canonical fan list (14)

| # | Name                | Source                  | Mode    |
|---|---------------------|-------------------------|---------|
| 1 | CPU fan header      | nct6798d / pwm1         | auto    |
| 2 | Front intake top    | nct6798d / pwm2         | auto    |
| 3 | Front intake mid    | nct6798d / pwm3         | auto    |
| 4 | Front intake bottom | nct6798d / pwm4         | auto    |
| 5 | Rear exhaust        | nct6798d / pwm5         | manual  |
| 6 | Top exhaust front   | nct6798d / pwm6         | auto    |
| 7 | Top exhaust rear    | nct6798d / pwm7         | auto    |
| 8 | GPU 0 fan 0         | nvidia / gpu0/fan0      | auto    |
| 9 | GPU 0 fan 1         | nvidia / gpu0/fan1      | auto    |
|10 | AIO pump            | corsair-h150i / pump    | auto    |
|11 | AIO fan 1           | corsair-h150i / fan1    | auto    |
|12 | AIO fan 2           | corsair-h150i / fan2    | auto    |
|13 | AIO fan 3           | corsair-h150i / fan3    | auto    |
|14 | PSU fan             | corsair-psu / fan1      | auto    |

## Live readings (consistent across all screens)
- CPU package: 52 °C
- GPU: 61 °C (RTX 4090 · 178 W)
- Coolant: 34 °C (H150i Elite)
- Front intake mid: 52 °C → 38% (drives the curve editor cursor)

## Calibration data — Front intake mid (curve editor reference)
- Stop PWM: 28 (11% duty)
- Start PWM: 42 (16% duty)
- Max RPM: 1820
- Min stable RPM: 540
- Calibrated: 2 days ago
