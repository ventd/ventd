# Configuration

`ventd` generates its config automatically during the setup wizard. You should never have to edit YAML by hand. This document describes the schema for users who want to inspect or customise the generated file.

Config lives at `/etc/ventd/config.yaml`. Edits take effect on `SIGHUP` or on daemon restart — the web UI's "Apply" button sends the signal for you. A full, annotated reference lives at `config.example.yaml` in the repo root; that file is the source of truth for field names.

## Top-level structure

```yaml
version: 1
poll_interval: 2s

web:
  listen: 0.0.0.0:9999
  password_hash: ""          # set by the web UI on first boot; do not edit manually
  # tls_cert: /etc/ventd/cert.pem
  # tls_key:  /etc/ventd/key.pem
  session_ttl: 24h

sensors:
  - name: cpu_package
    type: hwmon
    path: /sys/class/hwmon/hwmon4/temp1_input

  - name: gpu_0
    type: nvidia
    path: "0"                 # GPU index, string
    # metric: temp            # temp (default) | util | mem_util | power | clock_gpu | clock_mem | fan_pct

fans:
  - name: cpu_fan
    type: hwmon
    pwm_path: /sys/class/hwmon/hwmon6/pwm1
    min_pwm: 30
    max_pwm: 255

curves:
  - name: cpu_linear
    type: linear
    sensor: cpu_package
    min_temp: 40.0
    max_temp: 80.0
    min_pwm: 30
    max_pwm: 255

controls:
  - fan: cpu_fan
    curve: cpu_linear
```

## Fields

### `version`

Schema version for the on-disk config. Currently `1`. Future migrations will bump this and rewrite the file in place.

### `poll_interval`

How often the controller reads sensors, evaluates curves, and writes PWM. Expressed as a Go duration string (`500ms`, `1s`, `2s`). Default `2s`. Going below `500ms` wastes CPU for no gain on most thermal workloads.

### `web.listen`

Bind address for the web UI. Default `0.0.0.0:9999` (all interfaces). Loopback-only: `127.0.0.1:9999`. For TLS, set `tls_cert` / `tls_key`; `ventd` also auto-generates a self-signed pair on first boot when both fields are blank and the listener is non-loopback.

### `web.session_ttl`

How long an authenticated browser session stays valid. Duration string; default 24h.

### `web.trust_proxy`

List of CIDRs (e.g. `["127.0.0.1/32"]`) whose requests are allowed to set `X-Forwarded-For`. When the peer address is inside a trusted CIDR, the rate limiter and access logs use the left-most XFF entry as the client IP; otherwise XFF is ignored. Empty (default) disables XFF entirely. Set this when fronting `ventd` with Nginx, Caddy, or a reverse-proxy sidecar.

### `web.login_fail_threshold` / `web.login_lockout_cooldown`

Consecutive failed logins from the same peer IP before a lockout (`login_fail_threshold`) and how long that lockout lasts (`login_lockout_cooldown`, duration string). Zero means "use the built-in defaults".

### `sensors[]`

Each sensor has a `name` (referenced by curves), a `type` (`hwmon` or `nvidia`), and a `path`.

- `hwmon` — `path` is the full sysfs path to a `temp*_input` file.
- `nvidia` — `path` is the GPU index as a string (`"0"`, `"1"`). `metric` selects what is read: `temp` (default), `util`, `mem_util`, `power`, `clock_gpu`, `clock_mem`, `fan_pct`.

Optional `hwmon_device` — stable `/sys/devices/...` path used to re-resolve the `path` after hwmon renumbering (`hwmon3 → hwmon4` across reboots). Set by the setup wizard when it detects the device.

### `fans[]`

Each fan entry describes a writable control:

- `name` — referenced by `controls` entries
- `type` — `hwmon` or `nvidia`
- `pwm_path` — for `hwmon`, the sysfs `pwm*` file; for `nvidia`, the GPU index as a string
- `rpm_path` — optional override for auto-derived `fan*_input`
- `hwmon_device` — stable `/sys/devices/...` path for path re-resolution across reboots
- `control_kind` — `""`/`"pwm"` (default, standard PWM file) or `"rpm_target"` (pre-RDNA AMD, which exposes `fan*_target` instead of `pwm*`)
- `min_pwm` — runtime clamp floor (0–255). Every write is raised to at least this value before hitting sysfs.
- `max_pwm` — runtime clamp ceiling (usually 255)
- `is_pump` — when true, `min_pwm` is raised to `pump_minimum` regardless of what the curve asks for
- `pump_minimum` — hard PWM floor for this pump (only meaningful with `is_pump: true`)

Calibration results — start PWM, stop PWM, max RPM, the PWM→RPM curve — are stored separately in `/etc/ventd/calibration.json` and are not part of `config.yaml`. Rerun calibration from the web UI rather than editing that file by hand.

### `curves[]`

Three curve types are supported.

**Linear** — single-segment interpolation from temperature to PWM. Below `min_temp` returns `min_pwm`; above `max_temp` returns `max_pwm`; in between is linearly interpolated.

```yaml
- name: cpu_linear
  type: linear
  sensor: cpu_package
  min_temp: 40.0
  max_temp: 80.0
  min_pwm: 30
  max_pwm: 255
```

PWM values are raw 0–255, the same units hwmon uses. If you want "30 %", write `77` (≈ 0.30 × 255).

**Fixed** — constant PWM regardless of temperature. Useful for pumps or noise-sensitive presets.

```yaml
- name: pump_fixed
  type: fixed
  value: 100
```

**Mix** — evaluates several source curves and aggregates their outputs. Case fans usually want `function: max` over CPU and GPU curves so the loudest source wins.

```yaml
- name: case_mix
  type: mix
  function: max           # max | min | average
  sources: [cpu_linear, gpu_linear]
```

`sources` is a list of **curve names**, not sensor names.

### `controls[]`

Binds a fan to a curve. Every fan you want `ventd` to drive needs a `controls` entry; fans without one are left alone (other than the watchdog's `pwm_enable` restore on exit).

```yaml
controls:
  - fan: cpu_fan
    curve: cpu_linear
  - fan: sys_fan1
    curve: case_mix
```

Optional `manual_pwm: <0–255>` pins the fan to a fixed duty and bypasses the curve — useful for one-off diagnostics from the web UI.

## Hot reload

`ventd` reloads config on `SIGHUP`:

```
sudo systemctl reload ventd
```

The systemd unit wires `ExecReload=/bin/kill -HUP $MAINPID` so `systemctl reload` is the recommended entry point. Curve definitions, sensor assignments, and control bindings take effect on the next poll. Adding or removing fans requires a full restart because watchdog registrations are set at startup.

## Validation

`ventd` validates the config on load. Common errors:

- Unknown sensor name referenced from a curve
- Unknown curve name referenced from a `controls` entry
- Cyclic curve dependencies (mix curve references itself)
- `min_pwm > max_pwm`
- `is_pump: true` with `min_pwm` below `pump_minimum` (auto-raised with a warning)

On a failed reload the daemon logs the error and keeps running with the previously loaded config — it never crashes the process on bad YAML. The current load state is visible in the web UI.
