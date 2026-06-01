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

### Fault models

The pathological hardware real machines throw at ventd — the cases that
surface bugs the monotonic happy-path can't. Each is a `--model` value:

- `phantom` — tach always reads 0 (unconnected header / dead tach).
- `stuck` — tach frozen at a fixed RPM that never tracks duty (seized sensor).
- `inverted` — RPM falls as duty rises (DC-mode / inverted header); the
  polarity probe must classify this `inverted`.
- `sentinel` — a spinning fan reports the `0xFFFF` (65535) driver sentinel
  instead of a real RPM.
- `sentinelhigh` — real RPM at low duty, sentinel above ~50% duty (an
  intermittent tach glitch): a consumer that trusts raw tach computes a wildly
  false delta. Surfaced the polarity sentinel-guard gap.
- `noisy` — spin-up plus deterministic ±300 RPM tach jitter (unstable signal).
- `disconnect` — spins normally until `--fault-after` ticks, then the tach
  drops to 0 (cable yank / sudden stall) while duty stays high.

`--temp-model runaway` makes temperature climb 1 °C/tick to 105 °C regardless
of airflow, to exercise the over-temperature failsafe / critical-temp paths the
default cooling model can't reach.

The `*_test.go` scenario harness drives the **real** production code
(`internal/polarity.HwmonProber`, the `hal/hwmon` backend) against these models
and asserts ventd's verdict — fake-HIL bug-hunting with no hardware.

Value files are written atomically (temp + rename), so a concurrent reader —
the control loop, the polarity probe — never catches a file mid-write and parses
an empty/torn value, matching real `/sys` read semantics.

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

# Built-in multi-chip topologies (incl. non-controllable devices):
go run ./tools/hwmonsim --preset list
go run ./tools/hwmonsim --out /tmp/vsim --preset desktop
```

Flags: `--out` (required unless `--board list` / `--preset list`), `--preset`,
`--board`, `--fans`, `--temps`, `--chip`, `--max-rpm`, `--min-rpm`,
`--stop-pwm`, `--start-pwm`, `--model`, `--tick`, `--once`.

### `--preset`

`--preset <name>` materialises a built-in multi-chip topology that mirrors a
real machine — including the **non-controllable** devices a real
`/sys/class/hwmon` carries, so the daemon's enumeration *and classification*
(`ClassPrimary` / `ClassNoFans` / `ClassSkipNVIDIA`) are exercised, not just the
all-fans happy path:

- `desktop` — `nct6687` (6 fans) + `amdgpu` (1 fan) + `nvme` (temp-only) +
  `acpitz` (temp-only) + `nvidia` (skipped by the enumerator).
- `laptop` — `acpitz` (temp-only) + a single EC fan (`thinkpad`).
- `gpu` — `amdgpu` (1 fan) + `nvidia` (skipped).

A device with 0 fans is temp-only (sensors, no control channel). `--preset list`
prints them.

### `--board`

`--board <id>` looks the id up in ventd's hardware catalog
(`internal/hwdb/catalog/boards`, via the same `hwdb.LoadBoardCatalog` loader the
daemon uses) and seeds the device `name`(s) and controller topology from it: one
`hwmonN` per controller chip (primary + additional), using the real chip names —
so the daemon's hwdb chip-family / tier-3 matching runs against a real board's
chips. Controllers with no controllable hwmon presence (`unknown` / fanless) are
skipped. The primary controller's fan count comes from the board's
`fan_profiles` or `pwm_groups` when the catalog populates them, else `--fans`;
RPM ranges always come from the flags. `--board list` prints every id with its
chip(s).

## Scope

This is dev tooling, not production code (no RULE bindings). `VENTD_HWMON_ROOT`
steers the daemon's control path; the `-list-fans-probe` diagnostic deliberately
still walks real `/sys` (it exists to compare raw sysfs against Tier-2
enumeration on real hardware).
