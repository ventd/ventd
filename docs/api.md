# HTTP API Reference

`ventd` exposes an HTTP API on the address configured by `web.listen` (default `127.0.0.1:9999`). All request and response bodies are JSON unless stated otherwise. Every `/api/<name>` route is simultaneously reachable as `/api/v1/<name>` via an alias registered at startup — see [API v1 mirror](#api-v1-mirror).

The web UI exclusively uses this API; everything the browser can do, a script can do.

## Authentication

**Session cookie flow** (used by all `session` routes):

1. `POST /login` with the form field `password=...` — `POST /login` is **form-encoded** (`application/x-www-form-urlencoded`), not JSON. The response sets a `ventd_session` cookie (HttpOnly) and a `ventd_csrf` cookie, and returns `{"status": "ok", "csrf_token": "..."}`.
2. Include the cookie on subsequent requests: `-H 'Cookie: ventd_session=<value>'`. State-changing requests must also present the CSRF token (the `ventd_csrf` cookie, or the `csrf_token` from the login response).
3. Session lifetime is controlled by `web.session_ttl` (default 24 h).
4. `GET /logout` clears the cookie.

`POST /login` requires a same-origin `Origin` header; cross-origin requests are rejected with `403 forbidden: origin mismatch`.

**First-boot flow** (initial password set):

Before an admin password is configured, `POST /login` with the form field `new_password=...` sets the initial password and starts a session. Issue #765 removed the setup-token gate, so no token is required.

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
     -H 'Cookie: ventd_session=...' \
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
     -H 'Cookie: ventd_session=...' \
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

### `GET /api/hardware/inventory`

Composes `monitor.Scan()` with the live config (aliases + curve coupling) and a per-sensor rolling history ring into the feed the redesigned Hardware page consumes. The web UI polls this at ~1.5 s; this is also the source `/sensors`, `/devices`, and `/health` agree on (unlike `GET /api/status`, which only returns curve-bound sensors). Side-effect-free apart from the in-process history ring (reset on daemon restart).

**Auth**: session

**Query params**:
- `include_phantoms` (optional) — when present, includes mirror / phantom `fan*_input` rows. Default-off: a host with one physical fan shows one row.

**Response**:
```ts
{
  chips: {
    id:     string
    bus:    "hwmon" | "nvml" | "acpi"
    name:   string          // friendly chip-family name
    path:   string          // sysfs / device path
    model:  string          // friendly + chip-code line
    status: "ok" | "offline"
    sensors: {
      id:    string                       // stable unique key (sensor path)
      label: string                       // raw driver label, e.g. "temp1"
      alias?: string                      // config-supplied friendly name
      kind:  "temp" | "fan" | "volt" | "power"
      value: number
      unit:  string
      history: number[]                   // chronological, oldest first, ≤ 60 samples
      position?: { x: number, y: number } // operator-supplied heatmap coords
      used_by: string[]                   // curve IDs consuming this sensor
      likely_disconnected?: boolean       // suspicious-low temp band
    }[]
  }[]
  curves: {
    id:       string
    name:     string
    consumes: string[]   // sensor IDs with a live matching alias
    drives:   string[]   // fan PWM IDs bound to this curve
  }[]
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
     -H 'Cookie: ventd_session=...'
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

### `POST /api/calibrate/reset`

Wipes all persisted calibration state (the learned-curve store and `calibration.json`) so the next run starts from scratch. Idempotent.

**Auth**: session

**Response**:
```ts
{ status: "ok" }
```

### `GET /api/platform-profile`

Reads the kernel `platform_profile` interface (`/sys/firmware/acpi/platform_profile`) — the firmware power/thermal selector ventd's zero-config controller drives on hosts that expose it.

**Auth**: session

**Response**:
```ts
{
  present:   boolean    // false when the kernel/firmware exposes no interface
  current:   string     // active profile, e.g. "balanced"; empty if unset
  available: string[]   // allowed values, e.g. ["cool","quiet","balanced","performance"]
  path:      string     // sysfs directory read
}
```

---

## Smart mode & confidence

ventd's predictive controller blends a learned model with the reactive curve. These read-only endpoints expose what it has learned and why predictive control is (or isn't) engaged. See `docs/architecture/` and the `RULE-AGG-*` / `RULE-CPL-*` / `RULE-CMB-*` rule families for the underlying model.

### `GET /api/smart/status`

One-object summary of the worst-case global state, the active preset, and channel counts — the dashboard renders it as a single status pill or banner.

**Auth**: session

**Response**:
```ts
{
  enabled:        boolean
  preset:         string         // "silent" | "balanced" | "performance"
  global_state:   string         // worst per-channel UI state across the fleet
  channels:       number
  warming_up:     number         // channels still warming Layer B/C
  converged:      number         // fully converged channels
  not_started?:   number         // channels that have never seen first contact
  confidence_min: number | null  // min w_pred across channels (0..1); null pre-warmup
  confidence_max: number | null  // max w_pred; null pre-warmup
  acoustic: {
    enabled:        boolean
    target_dba?:    number       // resolved dBA cap
    current_dba?:   number       // host loudness; dBA if mic-calibrated, else "au" scale
    mic_calibrated: boolean
    mode?:          "measured" | "estimated"
  }
  cooling: {                     // chassis cooling-capacity estimate (#1285)
    capacity_w?: number          // watts at 70 °C ΔT
    cpu_tdp_w?:  number          // CPU package power limit (RAPL)
    adequate:    boolean
    has_signal:  boolean         // false when unavailable; UI hides the panel
  }
}
```

### `GET /api/smart/channels`

Deep per-channel snapshot array — the learned coupling/marginal shards and the controller's most-recent decision for each channel. Nested shard objects are omitted when the corresponding runtime is absent.

**Auth**: session

**Response**: a JSON array of
```ts
{
  channel_id:       string
  name?:            string   // operator-friendly fan name from config
  ui_state:         string   // converged | warming | cold-start | drifting | refused
  w_pred:           number   // 0..1 final blend weight
  signature_label?: string
  coupling?:        object   // per-channel coupling shard (theta, kappa, lambda, ewma_residual, n_samples, …)
  marginal?:        object[] // per-PWM marginal shards
  decision?: {               // the BlendedResult the next tick will write
    output_pwm:        number
    reactive_pwm:      number
    predictive_pwm:    number
    ui_state:          string  // reactive | blended | refused-pi | refused-pathA | refused-cost | refused-dba
    predicted_dba?:    number
    diagnostic_reason?: string
    // plus path_a_refused / cost_refused / dba_budget_refused / pi_refused / integrator_frozen booleans
  }
}
```

### `GET /api/confidence/status`

Per-channel confidence snapshot (the 5-state pill grid) plus the `w_pred_system` gate verdict. Read-only; never blocks the controller hot loop.

**Auth**: session

**Response**:
```ts
{
  enabled:      boolean
  global_state: string   // worst-of-channels collapse; "idle" when none
  preset:       string
  gate?: {               // w_pred_system gate; omitted in monitor-only mode
    open:             boolean
    reason?:          string
    detail?:          string
    schema_loaded:    boolean
    preconditions_ok: boolean
    wizard_control:   boolean
    mass_stalled:     boolean
    smart_disabled:   boolean
  }
  channels: {
    channel_id:         string
    name?:              string
    w_pred:             number
    ui_state:           string
    conf_a:             number
    conf_b:             number
    conf_c:             number
    tier:               number
    coverage:           number
    seen_first_contact: boolean
    age_seconds:        number
    drift_active:       boolean
    drift_a?:           DriftLayer   // omitted until the layer converges
    drift_b?:           DriftLayer
    drift_c?:           DriftLayer
  }[]
}

// DriftLayer: { drifting: boolean, residual: number, baseline: number, control_limit: number }
```

### `GET /api/confidence/preset`, `PUT /api/confidence/preset`

GET returns the active preset; PUT changes it (mutates the live config and persists). Recognised values: `silent`, `balanced`, `performance`; anything else is `400`.

**Auth**: session

**Request body** (PUT, JSON):
```ts
{ preset: "silent" | "balanced" | "performance" }
```

**Response** (both):
```ts
{ preset: string }
```

### `GET /api/probe/opportunistic/status`

Status of the opportunistic probe scheduler — the background process that excites an idle channel during quiet moments so smart mode can learn its coupling without disturbing a busy system.

**Auth**: session

**Response**:
```ts
{
  running:           boolean
  channel_id?:       number    // channel currently being probed
  gap_pwm?:          number    // PWM step applied for the probe
  started_at?:       string    // RFC3339
  last_reason?:      string    // machine reason the last probe ended/was skipped
  last_reason_human?: string   // operator-readable form of last_reason
  tick_count:        number
}
```

---

## Doctor

### `GET /api/doctor`

Runs the diagnostic detector suite (cached briefly between calls) and returns a structured report — the data behind the `/doctor` page. Each fact is a card with a severity, an operator-facing title/detail, and a stable `entity_hash` used for per-fact suppression.

**Auth**: session

**Response**:
```ts
{
  schema_version: string        // pins the JSON shape (RULE-DOCTOR-08)
  generated:      string        // RFC3339
  severity:       "ok" | "warning" | "blocker"   // worst-case rollup
  facts: {
    detector:    string
    severity:    "ok" | "warning" | "blocker"
    class:       string         // FailureClass; routes the recovery card
    title:       string
    detail?:     string
    entity_hash: string         // 16 hex chars; stable across restarts
    observed:    string         // RFC3339
    journal?:    string[]
  }[]
  detector_errors?: { detector: string, err: string }[]   // detectors that themselves failed
}
```

---

## Updates & release notes

### `GET /api/update/check`

Checks GitHub for a newer release. Network errors are surfaced in `error` rather than failing the request.

**Auth**: session

**Response**:
```ts
{
  current:        string    // running version
  latest:         string    // latest release tag
  available:      boolean    // latest is strictly newer than current
  published_at?:  string    // RFC3339 from GitHub
  url?:           string    // release page URL
  error?:         string    // populated on fetch failure
  last_apply_error?: {       // details of a previous failed in-UI update, if any
    at:           string
    version:      string
    status:       "failed" | "timed_out"
    detail?:      string
    journal_tail?: string
  }
}
```

### `POST /api/update/apply`

Spawns `install.sh` to roll the daemon binary forward to the requested version (binary swap only — never builds or loads a kernel module). Returns immediately; the install runs detached.

**Auth**: session

**Request body** (JSON):
```ts
{ version: string }   // must look like vX.Y.Z
```

**Response**: `202 Accepted`
```ts
{ status: "scheduled", version: string, message?: string }
```

Returns `400` if the version is malformed and `503` if the updater is unavailable.

### `GET /api/release-notes`

Returns CHANGELOG sections (newest-first) so the UI can show what changed.

**Auth**: session

**Query params**:
- `since` (optional) — only return sections newer than this version.

**Response**:
```ts
{
  current:  string
  since?:   string                                          // echoed query param
  sections: { version: string, date?: string, markdown: string }[]
  error?:   string                                          // populated when CHANGELOG can't be read
}
```

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
     -H 'Cookie: ventd_session=...' \
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

### `POST /api/hwdiag/load-apparmor`

Loads ventd's AppArmor profile into the kernel. Streams progress as an install log.

**Auth**: session

**Response**: `InstallLogResponse`

### `POST /api/hwdiag/grub-cmdline-add`

Adds a kernel command-line parameter to the GRUB config and regenerates it (e.g. `acpi_enforce_resources=lax` to unblock a Super-I/O chip). Takes effect on next boot.

**Auth**: session

**Request body** (JSON):
```ts
{ param: string }   // a malformed/empty param is rejected with 400
```

**Response**: `InstallLogResponse` (with `reboot_needed: true`)

### `POST /api/hwdiag/modprobe-options-write`

Writes a `/etc/modprobe.d` drop-in for a kernel module and reloads it. The `(module, options)` pair must be in ventd's allowlist, else `400`.

**Auth**: session

**Request body** (JSON):
```ts
{ module: string, options: string }
```

**Response**: `InstallLogResponse`

### `POST /api/hwdiag/reset-and-reinstall`

Cleans up the installed out-of-tree driver and reinstalls it from scratch — the recovery path when a driver install is in a wedged state. `400` if no installed OOT driver is found to clean up.

**Auth**: session

**Response**: `InstallLogResponse`

### `POST /api/diag/bundle`

Generates a redacted diagnostic bundle (logs, config, hwmon topology) on disk and returns a download handle. Redaction profile is governed by config.

**Auth**: session

**Response**:
```ts
{
  filename:         string
  download_url:     string   // GET /api/diag/download/<filename>
  redaction_profile: string
}
```

### `POST /api/diag/send`

Generates a diagnostic bundle and uploads it to the configured `diag.upstream_ingest.url` (must be a valid `https://` URL).

**Auth**: session

**Response**:
```ts
{
  reference:       string   // upstream-assigned reference id
  bytes:           number   // bundle size uploaded
  redactor_profile: string
  url:             string   // upstream URL the bundle landed at
}
```

Returns `412 Precondition Failed` when no valid upstream ingest URL is configured, and `502 Bad Gateway` when the upload is rejected.

---

## Setup wizard

The setup wizard is a guided flow that detects hardware, runs calibration, and writes the initial config. These endpoints are active during setup and remain accessible after setup completes (to allow re-running).

### `GET /api/setup/status`

Returns current wizard progress.

**Auth**: session

**Response**: _see source (internal/web/setup_handlers.go:handleSetupStatus)_ — output of `setup.ProgressNeeded(cfg)`: `{needed: bool, step: string, ...}`.

### `GET /api/setup/events`

Server-Sent Events stream of the setup/calibration activity feed — the structured rows the wizard renders live. Backed by a bounded in-memory ring + per-connection cursor.

**Auth**: session

**Query params**:
- `since` (optional) — Unix-ms cursor; only events newer than this are sent (resume without re-receiving the full log).

**Response**: `text/event-stream`, each event's data a JSON object:
```ts
{ ts: number, level: string, tag: string, text: string }
```

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

### `POST /api/setup/apply-monitor-only`

Completes setup in monitor-only mode — ventd reads sensors but drives no fans. Writes a minimal config and starts the daemon in that mode.

**Auth**: session

**Response**:
```ts
{ status: "ok", mode: "monitor_only" }
```

### `POST /api/admin/factory-reset`

Wipes the KV state and config, then uninstalls ventd — the full teardown the setup UI offers. Responds immediately, then performs the uninstall in the background, so the response confirms the reset has begun rather than completed.

**Auth**: session

**Response**:
```ts
{ status: "uninstalling" }
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

These endpoints are used by the browser login page. They are not typically called from scripts. Unlike the `/api/*` routes, `POST /login` takes **form-encoded** fields (`application/x-www-form-urlencoded`), not JSON, and requires a same-origin `Origin` header.

### `GET /login`

Serves the login HTML page. If the session cookie is already valid, redirects to `/`.

**Auth**: none

### `POST /login`

Authenticates the user. Two flows depending on boot state:

- **Normal login**: form field `password=...`
- **First-boot**: form field `new_password=...` — sets the initial password and logs in. No setup token is required (#765 removed the gate).

**Auth**: none

**Request body** (`application/x-www-form-urlencoded`):
```
password=<password>
# or on first boot:
new_password=<new password>
```

Example:
```sh
curl -k -X POST https://host:9999/login \
  -H 'Origin: https://host:9999' \
  --data-urlencode 'password=...'
```

**Response**: sets the `ventd_session` (HttpOnly) and `ventd_csrf` cookies; `{"status": "ok", "csrf_token": "..."}` on success, `4xx` on failure.

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
