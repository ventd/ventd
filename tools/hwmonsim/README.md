# hwmonsim — synthetic hwmon tree for hardware-free end-to-end testing

`hwmonsim` materialises a faithful, **live** `/sys/class/hwmon`-shaped tree and
runs a small thermal/aerodynamic model over it. Point ventd at it with the
`VENTD_HWMON_ROOT` environment variable and the whole stack — enumeration, the
control loop, calibration sweeps, the polarity probe, the web UI, doctor — runs
against fake fans instead of real hardware.

## Why

ventd's hard parts are hardware interactions. Unit tests fake hwmon in-process,
but exercising the *whole daemon* (control loop + calibration + web) previously
needed real hardware. `hwmonsim` + `VENTD_HWMON_ROOT` close that gap.

## How it works

- **Materialiser** — writes one `hwmonN/` directory with `name` and, per fan, a
  `pwmN` / `pwmN_enable` / `fanN_input` triple plus `tempN_input` sensors —
  exactly the shape `internal/hwmon.classifyDevice` recognises as a
  controllable (`ClassPrimary`) device.
- **Live model** — each tick it reads back the `pwmN` / `pwmN_enable` files the
  daemon writes and recomputes `fanN_input` (RPM) and `tempN_input`:
  - `--model spinup` (default): a real stall threshold (`--stop-pwm`) and
    start hysteresis (`--start-pwm`), so the polarity probe and calibration see
    a genuine spin-up curve.
  - `--model linear`: RPM proportional to duty.
  - Temperature falls as average airflow rises, so smart mode sees cooling.

`VENTD_HWMON_ROOT` only steers **enumeration**; because every discovered channel
carries an absolute path rooted there, reads and writes follow automatically.
The hwmon backend logs a loud one-time WARN when the override is active so a
stray setting in production can't masquerade as real-hardware control.

## Usage

```sh
# Materialise + run the live model (3 fans, nct6687, spinup):
go run ./tools/hwmonsim --out /tmp/vsim

# In another shell, drive it with the daemon:
VENTD_HWMON_ROOT=/tmp/vsim ./ventd --config /path/to/config.yaml

# Variants:
go run ./tools/hwmonsim --out /tmp/vsim --fans 7 --chip nct6687 --model spinup
go run ./tools/hwmonsim --out /tmp/vsim --once          # static tree, no model
```

Flags: `--out` (required), `--fans`, `--temps`, `--chip`, `--max-rpm`,
`--min-rpm`, `--stop-pwm`, `--start-pwm`, `--model`, `--tick`, `--once`.

## Scope

This is dev tooling, not production code (no RULE bindings). `VENTD_HWMON_ROOT`
steers the daemon's control path; the `-list-fans-probe` diagnostic deliberately
still walks real `/sys` (it exists to compare raw sysfs against Tier-2
enumeration on real hardware).
