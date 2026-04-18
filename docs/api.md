# HTTP API Reference

`ventd` exposes an HTTP API on the address configured by `web.listen` (default `0.0.0.0:9999`). All request and response bodies are JSON unless stated otherwise. Every `/api/<name>` route is simultaneously reachable as `/api/v1/<name>` via an alias registered at startup — see [API v1 mirror](#api-v1-mirror).

The web UI exclusively uses this API; everything the browser can do, a script can do.

## Authentication

**Session cookie flow** (used by all `session` routes):

1. `POST /login` with `{"password": "..."}` — response sets a `session` cookie.
2. Include the cookie on subsequent requests: `-H 'Cookie: session=<value>'`.
3. Session lifetime is controlled by `web.session_ttl` (default 24 h).
4. `GET /logout` clears the cookie.

**Setup-token flow** (first-boot only):

On first boot, ventd prints a one-time setup token to stdout / journald. Send it to `POST /login` with `{"setup_token": "...", "new_password": "..."}` to set the initial password and receive a session cookie.

**Rate limiting**: the login endpoint applies per-IP rate limiting. Consecutive failures trigger a lockout governed by `web.login_fail_threshold` and `web.login_lockout_cooldown`.

Routes marked **Auth: none** are accessible without a session or token.

---

## Type reference

Types used across multiple endpoints are defined once here.

```ts
type Sensor = {
  name:  string   // logical sensor name from config
  value: number   // current reading
  unit:  string   // "°C", "%" etc.
}

type Fan = {
  name:     string         // logical fan name from config
  pwm:      number         // current duty-cycle byte [0–255]
  duty_pct: number         // current duty cycle as a percentage
  rpm:      number | null  // current RPM if a correlated sensor exists
}

type HistorySample = {
  t: number  // Unix epoch second
  v: number  // metric value at that second
}

type HistoryResponse = {
  metrics:     { [metric: string]: HistorySample[] }
  interval_s:  number  // sample cadence in seconds (matches poll_interval)
  window_s:    number  // ring-buffer length in seconds
  cap_samples: number  // maximum samples retained
}

type VersionInfo = {
  version:    string
  commit:     string
  build_date: string
  go:         string
}

type Profile = object  // see config.md — profile-scoped fan bindings

type ProfileResponse = {
  active:   string                    // name of currently active profile
  profiles: { [name: string]: Profile }
}

type ScheduleStatus = {
  active_profile:  string           // name of currently active profile
  source:          "schedule" | "manual"
  next_transition: string | null    // RFC3339 timestamp or null
  next_profile:    string | null    // profile name that will activate next
}

type PanicPayload = {
  active:      boolean
  remaining_s: number        // seconds until auto-cancel; 0 if no timeout
  started_at:  string | null // RFC3339
  end_at:      string | null // RFC3339; null when no timeout was set
}

type InstallLogResponse = {
  kind:            "install_log"
  success:         boolean
  log:             string[]
  error:           string | null
  reboot_needed:   boolean | null
  reboot_message:  string | null
}

type DiagnosticEntry = {
  severity:    string   // "error" | "warning" | "info"
  summary:     string
  detail:      string | null
  fix_command: string | null
}

type ConfigDiff = {
  changed:  boolean
  sections: DiffSection[]
}

type DiffSection = {
  section: string   // top-level config section name
  kind:    "added" | "removed" | "modified"
  name:    string | null
  fields:  DiffField[] | null
}

type DiffField = {
  name: string
  from: string
  to:   string
}
```

---

## Status & events

### `GET /api/ping`

Unauthenticated health-check used by the UI to detect daemon restarts.

**Auth**: none

**Response**:
```ts
{ status: "ok" }
```

### `GET /api/status`

Snapshots all sensor readings and fan PWM/RPM values for the current controller tick.

**Auth**: session

**Response**:
```ts
{
  timestamp: string   // RFC3339
  sensors:   Sensor[]
  fans:       Fan[]
}
```

### `GET /api/events`

Server-Sent Events stream that emits a `status` event every 2 seconds (matches `poll_interval`). Each event's data field is a JSON-encoded object with the same shape as `GET /api/status`.

**Auth**: session

**Response**: `text/event-stream`

```
event: status
data: {"timestamp":"...","sensors":[...],"fans":[...]}
```

Keep-alive comments (`: keep-alive`) are emitted every 15 s. The connection stays open until the client disconnects.

### `GET /api/history`

Per-metric sparkline data from the in-process ring buffer (last hour at 2 s intervals).

**Auth**: session

**Query params**:
- `metric` (optional) — return a single metric's samples as `HistorySample[]`
- `window_s` (optional) — truncate to the last N seconds

**Response** (no `metric` param):
```ts
HistoryResponse
```

**Response** (with `metric` param):
```ts
HistorySample[]
```

### `GET /api/version`

Build metadata.

**Auth**: none

**Response**:
```ts
VersionInfo
```

---

## Configuration

### `GET /api/config`

Returns the full live configuration as a JSON object matching the `config.Config` struct. See `docs/config.md` for field descriptions.

**Auth**: session

**Response**: `config.Config` (JSON-encoded YAML struct)

### `PUT /api/config`

Validates and persists an entire replacement configuration to disk, then applies it live. The daemon does not restart; changes take effect on the next controller tick.

**Auth**: session

**Request body**: `config.Config`

**Response**:
```ts
{ status: "ok" }
```

Returns `400` with an error message if validation fails.

**Example**:
```bash
curl -X PUT http://localhost:9999/api/config \
     -H 'Cookie: session=...' \
     -H 'Content-Type: application/json' \
     -d @config.json
```

### `POST /api/config/dryrun`

Computes a semantic diff of the submitted config against the live config without persisting anything. Use this to preview what a `PUT /api/config` would change.

**Auth**: session

**Request body**: `config.Config`

**Response**:
```ts
ConfigDiff
```

---

## Profiles & scheduling

### `GET /api/profile`

Returns the active profile name and the full profile map.

**Auth**: session

**Response**:
```ts
ProfileResponse
```

### `POST /api/profile/active`

Switches the active profile immediately and sets the manual-override flag, preventing the scheduler from clobbering the choice until cleared.

**Auth**: session

**Request body**:
```ts
{ name: string }
```

**Response**:
```ts
ProfileResponse
```

**Example**:
```bash
curl -X POST http://localhost:9999/api/profile/active \
     -H 'Cookie: session=...' \
     -d '{"name": "silent"}'
```

### `PUT /api/profile/schedule`

Updates the cron schedule string for a named profile and persists it to disk.

**Auth**: session

**Request body**:
```ts
{
  name:     string  // profile name
  schedule: string  // cron expression (5-field or "@hourly"-style)
}
```

**Response**:
```ts
{ status: "ok" }
```

### `GET /api/schedule/status`

Returns the scheduler's view: which profile is active, whether it was set by the schedule or by a manual override, and when the next transition fires.

**Auth**: session

**Response**:
```ts
ScheduleStatus
```

---

## Hardware & calibration

### `GET /api/hardware`

Lists all fans and sensors detected by the hwmon tree enumeration.

**Auth**: session

**Response**: _see source (internal/web/hardware.go:handleHardware)_ — shape is the output of `monitor.Scan()`, which includes chip name, stable-device path, detected PWM channels, and temperature sensors.

### `POST /api/hardware/rescan`

Re-enumerates the hwmon tree, diffs against the previous snapshot, and stores the before/after for `GET /api/debug/hwmon`.

**Auth**: session

**Response**:
```ts
{
  new_devices:     string[]
  removed_devices: string[]
  elapsed_ms:      number
}
```

### `GET /api/debug/hwmon`

Returns the before/after snapshots from the most recent hardware rescan plus the current view. Useful for diagnosing hot-plug events.

**Auth**: session

**Response**:
```ts
{
  last_rescan_at:  string | null  // RFC3339
  rescan_trigger:  string | null  // "manual" | "uevent" | null
  before:          HwmonDevice[]
  after:           HwmonDevice[]
  current:         HwmonDevice[]
}
```

where `HwmonDevice` is:
```ts
{
  chip:          string
  dir:           string
  stable_device: string | null
  class:         string
  fans:          string[]
  sensors:       string[]
}
```

### `POST /api/calibrate/start`

Kicks off a calibration sweep for a specific fan. The sweep ramps PWM from min to max, recording RPM at each step. Runs in the background; poll `GET /api/calibrate/status` for progress.

**Auth**: session

**Query params**:
- `fan` — hwmon PWM path (e.g. `/sys/class/hwmon/hwmon3/pwm1`)

**Response**:
```ts
{ status: "started" }
```

**Example**:
```bash
curl -X POST 'http://localhost:9999/api/calibrate/start?fan=/sys/class/hwmon/hwmon3/pwm1' \
     -H 'Cookie: session=...'
```

### `GET /api/calibrate/status`

Returns in-progress calibration state for all fans currently being calibrated.

**Auth**: session

**Response**: _see source (internal/web/calibrate.go:handleCalibrateStatus)_ — output of `cal.AllStatus()`: map of PWM path → `{phase, progress_pct, elapsed_s}`.

### `GET /api/calibrate/results`

Returns the completed calibration results for all fans.

**Auth**: session

**Response**: _see source (internal/web/calibrate.go:handleCalibrateResults)_ — output of `cal.AllResults()`: map of PWM path → curve table.

### `POST /api/calibrate/abort`

Idempotently aborts an in-flight calibration and restores the fan to its pre-calibration PWM.

**Auth**: session

**Query params**:
- `fan` — hwmon PWM path

**Response**: `204 No Content`

### `POST /api/detect-rpm`

Ramps a fan's PWM briefly (~5 s) to identify which RPM sensor is correlated with it. Blocks until complete.

**Auth**: session

**Query params**:
- `fan` — hwmon PWM path

**Response**: _see source (internal/web/calibrate.go:handleDetectRPM)_ — output of `cal.DetectRPMSensor(fan)`: `{sensor_path, correlation_score}` or an error.

---

## System

### `POST /api/system/reboot`

Triggers a system reboot. Returns `409` if the daemon is running inside a container where reboot is unavailable.

**Auth**: session

**Response**:
```ts
{ status: "rebooting" }
```

### `GET /api/system/watchdog`

Reports the state of the systemd watchdog integration.

**Auth**: session

**Response**:
```ts
{
  enabled:     boolean
  interval_ms: number
  healthy:     boolean  // true if the last watchdog ping was within interval
}
```

### `GET /api/system/recovery`

Reports whether the `ventd-recover.service` (the sysfs restore unit) is installed and active. Result is cached for 5 s.

**Auth**: session

**Response**:
```ts
{
  installed:      boolean
  service_active: boolean
}
```

### `GET /api/system/security`

Reports LSM module state for SELinux and AppArmor. Result is cached for 30 s.

**Auth**: session

**Response**:
```ts
{
  selinux_module:   "loaded" | "unloaded" | "unsupported"
  apparmor_profile: "loaded" | "unloaded" | "unsupported"
}
```

### `GET /api/system/diagnostics`

Returns structured hardware diagnostic entries with severity-bucketed counts. Each entry may carry a `fix_command` the UI offers to run server-side.

**Auth**: session

**Response**:
```ts
{
  entries: DiagnosticEntry[]
  counts:  { [severity: string]: number }
}
```

---

## Panic button

The panic button immediately pins all fans to their configured `max_pwm`, overriding whatever the controller would normally write. It is the last-resort "cool the system now" control.

### `POST /api/panic`

Activates panic mode. Pass `duration_s: 0` for indefinite (until `POST /api/panic/cancel`).

**Auth**: session

**Request body**:
```ts
{ duration_s: number }
```

**Response**:
```ts
PanicPayload
```

**Example**:
```bash
curl -X POST http://localhost:9999/api/panic \
     -H 'Cookie: session=...' \
     -d '{"duration_s": 300}'
```

### `GET /api/panic/state`

Returns current panic state without modifying it.

**Auth**: session

**Response**:
```ts
PanicPayload
```

### `POST /api/panic/cancel`

Ends an active panic immediately. Idempotent — safe to call when panic is not active.

**Auth**: session

**Response**:
```ts
PanicPayload
```

---

## Hardware diagnostics

These endpoints detect common hardware configuration problems (missing kernel modules, unsigned drivers, etc.) and offer one-click remediation.

### `GET /api/hwdiag`

Returns hardware diagnostic entries, optionally filtered.

**Auth**: session

**Query params**:
- `component` (optional) — filter by component name
- `severity` (optional) — filter by severity level (`error`, `warning`, `info`)

**Response**: _see source (internal/web/hwdiag.go:handleHwdiag)_ — output of `diag.Snapshot(filter)`.

### `POST /api/hwdiag/install-kernel-headers`

Installs the distro's kernel-headers package via the detected package manager. Blocks until complete.

**Auth**: session

**Response**:
```ts
InstallLogResponse
```

### `POST /api/hwdiag/install-dkms`

Installs the distro's DKMS package via the detected package manager. Blocks until complete.

**Auth**: session

**Response**:
```ts
InstallLogResponse
```

### `POST /api/hwdiag/mok-enroll`

Returns distro-specific Machine Owner Key enrollment instructions. Does **not** execute any commands server-side — the operator runs these manually to enroll a signed DKMS module under Secure Boot.

**Auth**: session

**Response**:
```ts
{
  kind:     "instructions"
  commands: string[]   // ordered list of shell commands to run
  detail:   string     // prose explanation
}
```

---

## Setup wizard

The setup wizard is a guided flow that detects hardware, runs calibration, and writes the initial config. These endpoints are active during setup and remain accessible after setup completes (to allow re-running).

### `GET /api/setup/status`

Returns current wizard progress.

**Auth**: session

**Response**: _see source (internal/web/setup_handlers.go:handleSetupStatus)_ — output of `setup.ProgressNeeded(cfg)`: `{needed: bool, step: string, ...}`.

### `POST /api/setup/start`

Starts the setup wizard goroutine. Returns `409` if already running.

**Auth**: session

**Response**:
```ts
{ status: "started" }
```

### `POST /api/setup/apply`

Writes the wizard-generated config to disk and triggers a daemon restart to apply it.

**Auth**: session

**Response**:
```ts
{ status: "ok" }
```

### `POST /api/setup/reset`

Deletes the config file and triggers a restart, returning the daemon to first-boot / setup state.

**Auth**: session

**Response**:
```ts
{ status: "ok" }
```

### `POST /api/setup/calibrate/abort`

Idempotently cancels the setup wizard, including any in-flight calibration sweeps.

**Auth**: session

**Response**: `204 No Content`

### `POST /api/setup/load-module`

Loads a kernel module from a fixed allowlist via `modprobe` and persists it to `/etc/modules-load.d/ventd.conf` so it survives reboots.

**Auth**: session

**Request body**:
```ts
{
  module: "coretemp" | "k10temp" | "nct6683" | "nct6687" | "it87" | "drivetemp"
}
```

**Response**:
```ts
InstallLogResponse
```

---

## Auth endpoints

### `GET /api/auth/state`

Lightweight probe for the login page — returns whether this is a first-boot. Does not touch the rate limiter.

**Auth**: none

**Response**:
```ts
{ first_boot: boolean }
```

### `POST /api/set-password`

Allows an authenticated user to change the dashboard password.

**Auth**: session

**Request body**:
```ts
{
  current: string  // existing password (required)
  new:     string  // replacement password
}
```

**Response**:
```ts
{ status: "ok" }
```

---

## Login / logout (HTML endpoints)

These endpoints are used by the browser login page. They are not typically called from scripts, but they do accept JSON.

### `GET /login`

Serves the login HTML page. If the session cookie is already valid, redirects to `/`.

**Auth**: none

### `POST /login`

Authenticates the user. Two flows depending on boot state:

- **Normal login**: `{"password": "..."}`
- **First-boot**: `{"setup_token": "...", "new_password": "..."}` — sets the initial password and logs in.

**Auth**: none

**Request body**:
```ts
{ password: string }
// or on first boot:
{ setup_token: string, new_password: string }
```

**Response**: sets `session` cookie; `{"status": "ok"}` on success, `4xx` on failure.

### `GET /logout`

Clears the session cookie and redirects to `/login`.

**Auth**: none

---

## Infrastructure endpoints

### `GET /healthz`

Returns `200 ok` once the daemon has completed post-init startup; returns `503 starting` before that. Suitable for systemd `ExecStartPost=` health checks.

**Auth**: none

**Response**: `text/plain` — `ok` or `starting`

### `GET /readyz`

Returns `200 ok` when the watchdog has been pinged **and** a sensor read completed within the last 5 s. Returns `503` with a plain-text reason otherwise. Use this as a liveness/readiness probe.

**Auth**: none

**Response**: `text/plain`

---

## Static assets

### `GET /ui/*`

Embedded UI assets (JavaScript, CSS, fonts). Served with `ETag`-based conditional GET and a 1-hour `Cache-Control` ceiling. These routes are intentionally unauthenticated so the browser can load the login page resources.

**Auth**: none

### `GET /`

Dashboard root. Requires a valid session; redirects to `/login` otherwise.

**Auth**: session

**Response**: HTML (`text/html`)

---

## API v1 mirror

Every `/api/<name>` route is also registered as `/api/v1/<name>` and handled by the same function. The v1 prefix was introduced to allow a future `/api/v2/` versioning break without removing old clients. There is currently no behavioural difference between the two prefixes.

Routes with a v1 mirror (all `/api/*` routes):

| Route | v1 alias |
|---|---|
| `GET /api/ping` | `GET /api/v1/ping` |
| `GET /api/auth/state` | `GET /api/v1/auth/state` |
| `GET /api/version` | `GET /api/v1/version` |
| `GET /api/status` | `GET /api/v1/status` |
| `GET /api/events` | `GET /api/v1/events` |
| `GET /api/history` | `GET /api/v1/history` |
| `GET /api/config` | `GET /api/v1/config` |
| `PUT /api/config` | `PUT /api/v1/config` |
| `POST /api/config/dryrun` | `POST /api/v1/config/dryrun` |
| `GET /api/profile` | `GET /api/v1/profile` |
| `POST /api/profile/active` | `POST /api/v1/profile/active` |
| `PUT /api/profile/schedule` | `PUT /api/v1/profile/schedule` |
| `GET /api/schedule/status` | `GET /api/v1/schedule/status` |
| `GET /api/hardware` | `GET /api/v1/hardware` |
| `POST /api/hardware/rescan` | `POST /api/v1/hardware/rescan` |
| `GET /api/debug/hwmon` | `GET /api/v1/debug/hwmon` |
| `POST /api/calibrate/start` | `POST /api/v1/calibrate/start` |
| `GET /api/calibrate/status` | `GET /api/v1/calibrate/status` |
| `GET /api/calibrate/results` | `GET /api/v1/calibrate/results` |
| `POST /api/calibrate/abort` | `POST /api/v1/calibrate/abort` |
| `POST /api/detect-rpm` | `POST /api/v1/detect-rpm` |
| `POST /api/panic` | `POST /api/v1/panic` |
| `GET /api/panic/state` | `GET /api/v1/panic/state` |
| `POST /api/panic/cancel` | `POST /api/v1/panic/cancel` |
| `GET /api/setup/status` | `GET /api/v1/setup/status` |
| `POST /api/setup/start` | `POST /api/v1/setup/start` |
| `POST /api/setup/apply` | `POST /api/v1/setup/apply` |
| `POST /api/setup/reset` | `POST /api/v1/setup/reset` |
| `POST /api/setup/calibrate/abort` | `POST /api/v1/setup/calibrate/abort` |
| `POST /api/setup/load-module` | `POST /api/v1/setup/load-module` |
| `POST /api/system/reboot` | `POST /api/v1/system/reboot` |
| `GET /api/system/watchdog` | `GET /api/v1/system/watchdog` |
| `GET /api/system/recovery` | `GET /api/v1/system/recovery` |
| `GET /api/system/security` | `GET /api/v1/system/security` |
| `GET /api/system/diagnostics` | `GET /api/v1/system/diagnostics` |
| `GET /api/hwdiag` | `GET /api/v1/hwdiag` |
| `POST /api/hwdiag/install-kernel-headers` | `POST /api/v1/hwdiag/install-kernel-headers` |
| `POST /api/hwdiag/install-dkms` | `POST /api/v1/hwdiag/install-dkms` |
| `POST /api/hwdiag/mok-enroll` | `POST /api/v1/hwdiag/mok-enroll` |
| `POST /api/set-password` | `POST /api/v1/set-password` |
