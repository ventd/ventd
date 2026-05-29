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

# Seed the chip name(s) + controller topology from a real catalog board:
go run ./tools/hwmonsim --board list                    # list catalog board ids
go run ./tools/hwmonsim --out /tmp/vsim --board asrock-b650m-pg-lightning
```

Flags: `--out` (required unless `--board list`), `--board`, `--fans`, `--temps`,
`--chip`, `--max-rpm`, `--min-rpm`, `--stop-pwm`, `--start-pwm`, `--model`,
`--tick`, `--once`.

### `--board`

`--board <id>` looks the id up in ventd's hardware catalog
(`internal/hwdb/catalog/boards`, via the same `hwdb.LoadBoardCatalog` loader the
daemon uses) and seeds the device `name`(s) and controller topology from it: one
`hwmonN` per controller chip (primary + additional), using the real chip names —
so the daemon's hwdb chip-family / tier-3 matching runs against a real board's
chips. Controllers with no controllable hwmon presence (`unknown` / fanless) are
skipped. Fan counts and RPM ranges come from the flags (the catalog doesn't
carry them). `--board list` prints every id with its chip(s).

## Scope

This is dev tooling, not production code (no RULE bindings). `VENTD_HWMON_ROOT`
steers the daemon's control path; the `-list-fans-probe` diagnostic deliberately
still walks real `/sys` (it exists to compare raw sysfs against Tier-2
enumeration on real hardware).
