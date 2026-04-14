# Configuration

`ventd` generates its config automatically during the setup wizard. You should never have to edit YAML by hand. This document describes the schema for users who want to inspect or customise the generated file.

Config lives at `/etc/ventd/config.yaml`. Edits take effect on `SIGHUP` or on daemon restart — the web UI's "Apply" button sends the signal for you.

## Top-level structure

```yaml
version: 1
web:
  listen: "0.0.0.0:9999"
  password_hash: "..."          # set by the web UI; do not edit manually
  tls_cert: ""                  # path to PEM cert, optional
  tls_key: ""                   # path to PEM key, optional
poll_interval_ms: 1000
pump_minimum: 80                # PWM floor for anything classified as a pump
sensors:
  - name: cpu_temp
    type: hwmon
    path: /sys/class/hwmon/hwmon3/temp1_input
  - name: gpu_temp
    type: nvidia
    path: "0"                   # GPU index, string
    metric: temp                # temp | util | mem_util | power | clock_gpu | clock_mem | fan_pct
fans:
  - name: cpu_fan
    type: hwmon
    pwm_path: /sys/class/hwmon/hwmon3/pwm1
    rpm_path: /sys/class/hwmon/hwmon3/fan1_input
    start_pwm: 40
    stop_pwm: 30
    min_pwm: 50
    max_pwm: 255
    max_rpm: 2100
    is_pump: false
    allow_stop: false
    curve: cpu_curve
curves:
  - name: cpu_curve
    type: linear
    source: cpu_temp
    points:
      - { temp: 40, pwm: 30 }
      - { temp: 80, pwm: 100 }
```

## Fields

### `web.listen`

Default: `0.0.0.0:9999`. Binds to all interfaces on the local network. If you want localhost-only, set to `127.0.0.1:9999`. For HTTPS, front with Nginx or Caddy, or set `tls_cert` / `tls_key` for direct TLS termination.

### `poll_interval_ms`

How often the controller reads sensors, evaluates curves, and writes PWM. Default 1000 ms. Going below 500 ms wastes CPU for no gain on most thermal workloads.

### `pump_minimum`

Hard floor in PWM units (0–255) for any fan with `is_pump: true`. Pumps must never stop under load — AIOs lose flow, loops lose circulation. Default 80.

### `sensors[]`

Each sensor has a `name` (used as a key from curves), a `type` (`hwmon` or `nvidia`), and a `path`.

- `hwmon` sensors: `path` is the full sysfs path to a `temp*_input` file.
- `nvidia` sensors: `path` is the GPU index as a string (`"0"`, `"1"`). `metric` selects what is read: `temp` (default), `util`, `mem_util`, `power`, `clock_gpu`, `clock_mem`, `fan_pct`.

### `fans[]`

Each fan has a `name`, a `type` (`hwmon` or `nvidia`), paths to the PWM and RPM sysfs entries, and PWM bounds learned during calibration:

- `start_pwm` — minimum PWM at which the fan reliably starts from stopped
- `stop_pwm` — minimum PWM at which an already-spinning fan stays running
- `min_pwm` — runtime clamp floor. `start_pwm` on first write; can be driven down to `stop_pwm` once spinning.
- `max_pwm` — runtime clamp ceiling (usually 255)
- `max_rpm` — RPM observed at `max_pwm`
- `is_pump` — if true, `min_pwm` is raised to `pump_minimum` regardless
- `allow_stop` — if true, permits PWM=0 writes (fan stops completely). Default false.
- `curve` — name of the curve that drives this fan

### `curves[]`

Three curve types are supported.

**Linear** — piecewise-linear interpolation from temperature to PWM:

```yaml
- name: cpu_curve
  type: linear
  source: cpu_temp
  points:
    - { temp: 30, pwm: 20 }
    - { temp: 80, pwm: 100 }
```

Values outside the range are clamped to the endpoints. Between points, PWM is interpolated linearly. `pwm` values are percentages (0–100), converted to 0–255 internally.

**Fixed** — constant PWM regardless of temperature:

```yaml
- name: pump_curve
  type: fixed
  value: 100
```

Useful for pumps that should run at a constant speed, or for noise-sensitive deployments where you want a fixed cap.

**Mix** — combines multiple source curves with an aggregation function:

```yaml
- name: case_curve
  type: mix
  mode: max          # max | min | average
  sources:
    - cpu_curve
    - gpu_curve
```

Case fans usually want `mix` with `mode: max` over CPU and GPU curves — the loudest source wins, protecting both components.

## Hot reload

`ventd` reloads config on `SIGHUP`:

```
sudo systemctl reload ventd
```

Curves, sensor assignments, and PWM overrides take effect immediately. Adding or removing fans requires a full restart because the watchdog registrations are set at startup.

## Validation

`ventd` validates the config on load. Common errors:

- Unknown sensor name referenced from a curve
- Unknown curve name referenced from a fan
- Cyclic curve dependencies (mix curve references itself)
- `start_pwm > max_pwm` or `stop_pwm > start_pwm`
- `is_pump: true` with `min_pwm` below `pump_minimum` (auto-raised with a warning)

Errors are logged to the journal and surfaced in the web UI. The daemon refuses to apply an invalid config and keeps running with the previous one.

## Backups

Before the web UI's "Apply" writes a new config, the old one is saved to `/etc/ventd/config.yaml.bak`. Restore with:

```
sudo cp /etc/ventd/config.yaml.bak /etc/ventd/config.yaml
sudo systemctl reload ventd
```
