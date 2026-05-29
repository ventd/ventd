# Web UI Rules

- All UI is server-side rendered HTML/JS embedded in ui.go — no build step, no npm, no node
- Static assets served via Go embed directive
- API endpoints under /api/ return JSON
- Setup wizard at /api/setup/* — only active when no config exists or daemon is in setup mode
- Auth handled in auth.go — check authentication before adding new endpoints
- Listen address defaults to 0.0.0.0:9999 — accessible on the local network out of the box
- Authentication is required for all routes except /login, /logout, /api/ping — enforced in auth.go middleware
- First-boot: no config + no auth.json → browser shows the password-set form, then logs in directly. Setup-token bootstrap was eliminated in v0.5.8.1 (#765, #794) when the daemon flipped to `User=root`.
- For HTTPS: set web.tls_cert and web.tls_key in config, or front with Nginx/Caddy (recommended for Let's Encrypt)
- Keep JS minimal and vanilla — no frameworks, no transpilation

## RULE-WEB-UPDATE-STAGE-PATH-OUTSIDE-PRIVATETMP: in-UI update stages install.sh outside the daemon's PrivateTmp namespace.

`writeInstallShBytes` MUST stage the install.sh it writes (whether
fetched from the GitHub release or unpacked from the embedded copy)
under a host-shared, non-namespaced path that the transient
`ventd-update.service` spawned via `systemd-run` can read in its own
namespace.

The default staging directory is `/run/ventd` — host-visible
(`PrivateTmp=yes` does not namespace `/run`), already in
`ventd.service`'s `ReadWritePaths`, ephemeral (cleared on reboot so
no orphan litter), mode 0700 (no world-readable script bytes).

Why not `/tmp`: ventd.service ships `PrivateTmp=yes`. The daemon's
view of `/tmp` is a per-unit kernel namespace; a script staged there
from the daemon does not exist at that path on the host. The
transient `ventd-update.service` spawned via `systemd-run` runs in
the host namespace; `bash /tmp/<staged>.sh` returns exit 127 / ENOENT
and the unit fails. The API caller sees a successful 202 because
`realUpdateRun`'s `cmd.Run()` observed a successful systemd-run queue,
not the unit's runtime exit. Diagnosed end-to-end on Phoenix's MSI
Z690-A desktop on 2026-05-08; latent since the systemd-run pattern
landed.

The package-level `installStagingDir` seam holds the staging path.
Production sets it to `/run/ventd`; tests override it to a
`t.TempDir()` so the assertions don't need root. An empty seam value
means "use the default tmp dir" — no shipping code uses this today,
but the seam preserves the legacy behaviour as an escape hatch.

The fallback branch lands when `installStagingDir` is non-empty but
the directory cannot be created or proven writable (dev-tree
invocation, sandbox-hardened env that doesn't grant `/run/ventd`,
non-systemd hosts). In that case the function falls through to
`os.CreateTemp("", ...)` so existing dev workflows + non-systemd
hosts keep working — but on production systemd hosts the staging
dir is always reachable and the fallback never fires.

The default-being-/run/ventd assertion is pinned independently of
the seam-override tests so a regression that defaults the seam back
to `""` (Go's `os.TempDir()` resolution) or to `"/tmp"` reintroduces
the silent-fail bug — even if the seam-override tests still pass.

Bound: internal/web/update_staging_test.go:staging_dir_default_is_run_ventd
Bound: internal/web/update_staging_test.go:happy_path_stages_under_configured_dir
Bound: internal/web/update_staging_test.go:falls_back_to_default_tmp_when_staging_dir_unwritable
Bound: internal/web/update_staging_test.go:empty_staging_dir_seam_uses_default_tmp

## RULE-WEB-UPDATE-STATUS-FIDELITY: failed transient unit surfaces via /api/v1/update/check.last_apply_error.

When `POST /api/v1/update/apply` spawns the transient
`ventd-update.service` via systemd-run AND that unit subsequently
fails (script ENOENT, exec error, install.sh exit non-zero before
binary swap), the daemon MUST surface that failure to the in-UI
operator on the next `GET /api/v1/update/check`.

The mechanism: a bounded watcher goroutine (`watchUpdateApplyOutcome`
in `update_outcome.go`) polls the transient unit for up to
`updateOutcomeWatchTimeout` (60s default) at
`updateOutcomePollInterval` (1s default). When systemd reports the
unit `Result != "success"` and `SubState ∈ {exited, dead, failed}`,
the watcher captures the result + last 30 journal lines into
`lastApplyOutcomePtr` (an `atomic.Pointer[LastApplyOutcome]`). The
next `/update/check` includes the captured state under the
`last_apply_error` JSON field.

Three locked behaviours:

- **Failure capture**: a unit that finishes with non-success result
  produces a non-nil `LastApplyOutcome` containing the version, RFC3339Nano
  timestamp, status string ("failed"), Detail naming the unit + result +
  script path, and the journal tail. The same daemon's subsequent
  /update/check responses include `last_apply_error` until the daemon
  restarts.
- **Success silence**: a unit that finishes with `Result=success`
  records NO outcome. The success surface is the daemon's own restart
  (operator polls /update/check, sees `current` updated to the new
  version). Storing a success outcome would persist a stale "last
  attempt failed" indicator after a successful subsequent install.
- **Timeout silence**: a unit that never finishes within
  updateOutcomeWatchTimeout records NO outcome. The watcher returns;
  the operator can re-poll /update/check after the next state change.

The `omitempty` tag on `LastApplyError` is load-bearing: when no
failure has been observed in the daemon's lifetime, the JSON response
MUST NOT include the field so older UIs that don't know about it see
no behaviour change.

The watcher is only spawned when `systemd-run` is the spawn primitive
(systemd available + systemd-run on PATH). The nohup fallback path
has no transient unit to watch.

Transient query errors (systemd reloading, dbus busy) on a single
poll MUST NOT terminate the watcher — the next tick re-queries.

Bound: internal/web/update_outcome_test.go:failed_unit_captures_outcome_with_journal_tail
Bound: internal/web/update_outcome_test.go:successful_unit_records_no_outcome
Bound: internal/web/update_outcome_test.go:never_finished_within_timeout_records_no_outcome
Bound: internal/web/update_outcome_test.go:transient_query_error_does_not_terminate_watcher
Bound: internal/web/update_outcome_test.go:update_check_surfaces_captured_outcome_via_json_response
Bound: internal/web/update_outcome_test.go:update_check_omits_field_when_outcome_unset

## RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE: every state-changing API request requires a valid X-CSRF-Token header.

Authenticated POST / PUT / PATCH / DELETE requests through any
`/api/` or `/api/v1/` route MUST carry an `X-CSRF-Token` header
matching the session's bound CSRF token. The `requireCSRF` middleware
in `internal/web/csrf.go`:

- Bypasses the check on safe methods (GET / HEAD / OPTIONS) — those
  don't mutate state and don't need CSRF.
- Reads the session token from the `ventd_session` cookie, looks up
  the session's bound CSRF token via `sessionStore.csrfFor`. Missing
  or expired session → 401 (auth gate fires before CSRF gate).
- Reads the `X-CSRF-Token` header. Missing → 403 with body
  `"X-CSRF-Token header required"`.
- Constant-time compares the header against the session's bound
  token via `subtle.ConstantTimeCompare`. Mismatch → 403 with body
  `"CSRF token mismatch"`.
- On success, calls the wrapped handler.

Token lifecycle:

- Generated alongside the session token in `sessionStore.create`.
- Returned to the JS layer in two places: the login response JSON
  (`csrf_token` field) and a non-HttpOnly `ventd_csrf` cookie. The
  cookie is set with `SameSite=Strict` to match the session cookie's
  posture (RULE-WEB-COOKIE-SAMESITE-STRICT) and is read by the JS
  layer via `document.cookie`.
- Cleared on logout via `clearCSRFCookie` paired with the existing
  `clearSessionCookie`.

JS layer wiring lives in `web/shared/brand.js` (loaded by every
HTML page): a fetch monkey-patch reads the `ventd_csrf` cookie and
injects `X-CSRF-Token` on every `POST` / `PUT` / `PATCH` / `DELETE`
fetch. Pages don't need per-request changes; existing `fetch()`
calls work transparently.

Cross-origin attackers cannot construct a valid request because they
cannot read the per-session CSRF token (cookies are SOP-isolated +
SameSite=Strict refuses to attach the cookie to cross-site
navigations). The Origin allow-list (existing `originAllowed`) is
the third layer of CSRF defence; SameSite=Strict is the first;
the per-request token is the second.

Tests construct authenticated POST requests via the `authAndCSRF`
helper in `csrf_test_helpers_test.go` — sets the session cookie +
the X-CSRF-Token header in one call.

Bound: internal/web/csrf_test.go:post_without_csrf_header_is_rejected_403
Bound: internal/web/csrf_test.go:post_with_wrong_csrf_token_is_rejected_403
Bound: internal/web/csrf_test.go:post_with_correct_csrf_token_passes_csrf_gate
Bound: internal/web/csrf_test.go:get_request_bypasses_csrf_check_entirely
Bound: internal/web/csrf_test.go:post_without_session_cookie_is_rejected_401_before_csrf_check

## RULE-WEB-COOKIE-SAMESITE-STRICT: session and CSRF cookies are SameSite=Strict; session is HttpOnly, CSRF is not.

`setSessionCookie` writes the `ventd_session` cookie with
`SameSite=http.SameSiteStrictMode` and `HttpOnly=true`.
`setCSRFCookie` writes the `ventd_csrf` cookie with
`SameSite=http.SameSiteStrictMode` and `HttpOnly=false`.

The flip from `SameSiteLaxMode` (pre-v0.5.31) closes the residual
cross-site form-POST vector that Lax permitted. Strict refuses to
attach the cookie to ANY cross-site navigation — including
top-level link clicks, form submits from another tab, and
`window.open` redirects. Combined with the per-request CSRF token
(RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE) and the Origin
allow-list, a forged cross-origin request cannot:

1. Carry the session cookie (SameSite=Strict refusal), AND
2. Carry the CSRF token (cross-origin script can't read it), AND
3. Pass the Origin allow-list check.

All three layers must fail for a CSRF attack to succeed; v0.5.31
ships all three.

The CSRF cookie's `HttpOnly=false` is load-bearing — the JS layer
reads it via `document.cookie` to inject the X-CSRF-Token header.
A regression that flips it to HttpOnly silently breaks every
authenticated state-changing fetch in the UI; the bound subtest
catches that flip.

Bound: internal/web/csrf_test.go:session_cookie_samesite_strict
Bound: internal/web/csrf_test.go:csrf_cookie_samesite_strict_and_not_httponly

## RULE-WEB-BODY-SIZE-CAP-1MIB: every authed state-changing request body is capped at 1 MiB.

`registerAPIRoutes` wraps every entry in the route table with
`requireMaxBody(defaultMaxBody, ...)` where
`defaultMaxBody = 1 << 20` (1 MiB). The middleware sets
`r.Body = http.MaxBytesReader(w, r.Body, max)` on POST / PUT /
PATCH / DELETE requests; safe methods bypass.

Oversized payloads surface as `*http.MaxBytesError` on the next
read. Handlers that decode JSON via `json.Decoder.Decode`, parse
forms via `r.ParseForm`, or read raw bytes see the error and emit
413 Request Entity Too Large via the `isMaxBytesErr` check that
already lives in `internal/web/security.go` and is reused by every
JSON-accepting handler.

The 1 MiB cap dwarfs every realistic state-change payload (config
push: ~10 KiB; password set: ~200 bytes; version string: <100
bytes; setup wizard apply: ~50 KiB worst case) and still blocks
trivial OOM attempts on an authenticated upload.

The `/login` endpoint applies its own narrower 64 KiB cap directly
in the handler (`limitBody(w, r, 64<<10)`) before form parsing
because the login form arrives unauthenticated and a smaller cap
is appropriate for that surface.

Bound: internal/web/csrf_test.go:oversized_post_to_authed_route_returns_413
Bound: internal/web/csrf_test.go:undersized_post_passes_body_cap

## RULE-WEB-METRICS-EXPOSITION: /metrics renders live state in Prometheus text format, sourced from buildStatus(), with sentinel/unknown readings omitted.

`GET /metrics` exposes the daemon's live state in the Prometheus text
exposition format (version 0.0.4) so a homelab Prometheus / Grafana-Agent
scrape works out of the box. It is registered directly on the mux next to
`/healthz` and `/readyz` and is **unauthenticated** by design — operational
telemetry with the same posture as the health probes, carrying no secrets — so
a scrape config needs no credentials. The handler is GET-only (405 otherwise).

Every sample is sourced from `buildStatus()` — the same live snapshot
`/api/v1/status` serves — rather than a parallel read path, so a metric can
never report a value the daemon isn't actually reading. Honesty of the surface
is enforced the same way the dashboard is: a sensor whose read returned a
sentinel/implausible value (nil `Value`) and a fan with no tachometer (nil
`RPM`) are **omitted** from the exposition rather than reported as `0` — a
scraped series is always a value the daemon trusts. Each metric family emits its
`# HELP`/`# TYPE` header exactly once and only when it has at least one sample.
Label values are escaped per the exposition spec (backslash, double-quote,
newline only).

Bound: internal/web/metrics_test.go:TestWriteMetrics_Format
Bound: internal/web/metrics_test.go:TestWriteMetrics_OmitsEmptyFamiliesAndBlankVersion
Bound: internal/web/metrics_test.go:TestHandleMetrics_GETOnly
Bound: internal/web/metrics_test.go:TestEscapeLabelValue

## RULE-WEB-CALIBRATION-SCOPE-REALDATA: the calibration signal scope is driven by the real sweep payload, never a synthetic waveform.

The calibration page's signal scope (`web/calibration.js` `paintScope`) once drew
its TX/RX traces and its PWM-duty / tach-freq / ADC-noise readouts (and the
CAPTURING state) from `Math.sin(Date.now())` — fabricated numbers presented as
live instrument readings, which violates the no-theatre contract the rest of the
UI follows ("show — rather than invent").

`paintScope` now reads the real Progress payload (`lastProgress.fans`): the TX
trace is the active fan's real `current_pwm` level, the RX trace is its real RPM
history (the rolling buffer `paintAllSparks` maintains), the duty readout is the
real `current_pwm` %, and the tach readout is the real `current_rpm` / 60 Hz.
Before any duty/tach exists (the detect / driver / enumerate phases) it renders a
flat baseline with "—" readouts rather than a synthetic waveform. The ADC-noise
readout has **no** real source in the payload, so it is always "—" — never
fabricated. The live-sweep motion is real: fresh tach samples scroll the RX trace
as each poll arrives.

Bound: internal/web/e2e_test.go:TestE2E_CalibrationScopeUsesRealData

## RULE-WEB-MONITOR-ONLY-CTA: a daemon with no fans under control shows a setup call-to-action, not a passive "No fan data" dead-end.

When the daemon controls no fans (monitor-only: BIOS/EC-locked hardware, or
setup not yet run), the dashboard's fans grid renders an actionable CTA —
"ventd isn't controlling any fans yet … run setup to detect and calibrate your
fans" linking to `/calibration` — instead of a bare "No fan data yet…"
placeholder. `/` meta-refreshes a no-controls daemon to `/health` (#784), but
the dashboard stays reachable from the nav, so the dead-end is real.

The CTA renders only on a genuine empty-fans payload: `renderFanTiles` runs only
from `applyStatus` (a real poll/SSE frame), and demo mode always carries
synthetic fans — so it never flashes during the pre-first-poll window (the
static HTML placeholder covers that).

Bound: internal/web/e2e_test.go:TestE2E_MonitorOnlyDashboardShowsSetupCTA

## RULE-WEB-DOCTOR-FIX-THIS: each Doctor finding carries its class's actionable remediation, so the page can offer "Apply fix" / "Learn more", not just describe the problem.

The Doctor page once only listed findings + a class label. The /api/v1/doctor
response now attaches `recovery.RemediationFor(fact.Class)` to each fact — the
**same** catalogue the calibration recovery cards use — as an additive
`remediation` field. The response is a superset of `doctor.Report` (the cache is
untouched; the per-request view embeds each `doctor.Fact` and appends
remediation), so CLI/diff consumers that unmarshal into `doctor.Report` ignore
the extra field and no schema bump is needed.

`doctor.js` renders each remediation as an **Apply fix** button (POST to the
real backed `hwdiag/*` / `setup/*` endpoint — CSRF auto-injected by the shared
`brand.js` fetch wrapper) and/or a **Learn more** link to the wiki page. Buttons
appear only for entries with a real `action_url`; doc-only classes get the link.
A finding whose class has no catalogue entry (`ClassUnknown`) gets only the
generic diagnostic-bundle action — no invented handlers.

Bound: internal/web/doctor_test.go:TestDoctorReportWithRemediation
Bound: internal/web/e2e_test.go:TestE2E_DoctorShowsFixThisAction
