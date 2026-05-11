# Pass 6: internal/web/ deep read

**Files audited (non-test, 23 files, ~6.4k LOC body / 19.4k total):**

- `internal/web/server.go` (2,064 LOC) — route registration, middleware composition, login/logout, session store glue, calibrate / setup / hwdiag / system / factory-reset / setup-load-module / reboot handlers, install dispatcher, modprobe-options-write, AppArmor loader, set-password.
- `internal/web/auth.go` (203 LOC) — session store, csrfFor, cookie writers, bcrypt helpers.
- `internal/web/csrf.go` (127 LOC) — `requireCSRF`, `requireMaxBody`, cookie writers.
- `internal/web/security.go` (406 LOC) — security headers, Origin check, login rate limiter, body-size guards.
- `internal/web/update.go` (636 LOC) — `/update/check`, `/update/apply`, install.sh staging, `systemd-run` spawn.
- `internal/web/update_outcome.go` (191 LOC) — transient-unit watcher goroutine.
- `internal/web/diag.go` (349 LOC) — `/diag/bundle`, `/diag/send`, `/diag/download/<name>`.
- `internal/web/panic.go` (264 LOC) — panic-button handler, restorePanic, writeMaxPWMToAllFans.
- `internal/web/sse.go` (105 LOC) — `/api/events` SSE.
- `internal/web/setup_events_sse.go` (119 LOC) — `/api/setup/events` SSE (cursor poll on bounded ring).
- `internal/web/smart_handlers.go` (445 LOC) — confidence/preset PUT, smart/status, smart/channels.
- `internal/web/schedule.go` (372 LOC) — scheduler tick + `/profile/schedule` PUT.
- `internal/web/config_dryrun.go` (428 LOC) — diff endpoint.
- `internal/web/patch.go` (142 LOC) — `PATCH /api/config`.
- `internal/web/profiles.go` (110 LOC) — profile read + `POST /api/profile/active`.
- `internal/web/hardware_inventory.go`, `hardware_rescan.go`, `hwmon_debug.go`, `history.go`, `release_notes.go`, `system_status.go`, `doctor.go`, `health.go`, `selfsigned.go`, `http_redirect.go`, `version.go`, `ui.go`, `router.go`, `authpersist/*` — all read-side or wiring-only.

**Files NOT audited:** every `*_test.go` (per scope).

**Time on task:** ~30 min (load context, read non-test files, write).

**Baseline commit:** `b46c1a5` (docs(audit) pass 1-5) on top of `da67bd1` (#1034 smart-mode bridge). No new merges since.

## Headline counts

- **Critical (security / hardware safety):** 0
- **High (correctness gaps with operational impact):** 3
- **Medium (defence-in-depth gaps, latent bugs, contract drift):** 5
- **Low (cleanup / nit):** 2

**Most consequential finding in one sentence:** `handleResetAndReinstall` decodes the request JSON *after* `runInstallHandler` has already opened the long-lived install goroutine, but more importantly `runInstallHandler` itself never enforces the `requireMaxBody` cap nor uses `isMaxBytesErr` to surface 413 — and worse, **`handleResetAndReinstall` calls `json.NewDecoder(r.Body).Decode(...)` against an unbounded body because `r.Body` is wrapped in `MaxBytesReader` only when the request comes through `registerAPIRoutes`, which it does, but no current handler in `runInstallHandler` ever surfaces 413 to the operator on overflow** — see M3.

The truly noteworthy finding is **H1**: every state-changing handler routed through `registerAPIRoutes` is auth+csrf+body-cap gated correctly, but the **first-boot password-set POST `/login` runs the full credential-write + session-creation path outside that middleware**, and the body cap there is the narrow per-handler `limitBody(w, r, 64<<10)` only — without the `originAllowed` guard or the global CSRF gate. The Origin check sits *outside* the mux (`securityHeaders()(originCheck(logger)(s.mux))`), so `/login` does pass it for safe methods; on POST it requires Origin to match. That mostly closes the gap, but read H1 for the residual seam.

## CRITICAL findings

(none)

## HIGH findings

### H1. `runInstallHandler` accepts `POST` with no method-allowed body shape and silently runs the install on any non-empty body, including bodies that arrive *with* `Content-Type: application/json` but a body the handler does not parse — `handleInstallKernelHeaders`, `handleInstallDKMS`, `handleLoadAppArmor`, `handleResetAndReinstall`, `handleGrubCmdlineAdd`.

`internal/web/server.go:1670` (the `runInstallHandler` shared core) checks `r.Method != http.MethodPost` and then calls `fn(logFn)` directly without reading or validating the body — the request body is silently ignored on the kernel-headers / DKMS / AppArmor entry points. `handleResetAndReinstall` and `handleGrubCmdlineAdd` *do* JSON-decode `r.Body` *inside* the runInstallHandler closure, but they do so without an explicit body cap call: the inherited `requireMaxBody(defaultMaxBody, ...)` (1 MiB) wraps every authed route via `registerAPIRoutes`, but neither handler invokes `isMaxBytesErr` on the decode error — a `*http.MaxBytesError` surfaces as `"invalid JSON body: ..."` with **HTTP 200** because `runInstallHandler` only sets the response status implicitly via `s.writeJSON`.

Specifically (`server.go:1715`):

```go
if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
    module = body.Module
}
```

On a malformed/oversized body the error is silently swallowed and the handler falls through to `guessInstalledOOTModule`. This is a **graceful-degrade** (the install runs against the on-disk-detected module) — not a security bug — but it makes the operator surface confusing: a client that sent `{"module":"it87"}` over a network blip sees `module=""` and gets the auto-detected target instead of the one they asked for. **Severity High** because at least one of these handlers (`handleGrubCmdlineAdd`) writes to `/etc/default/grub.d/ventd-cmdline.cfg` and then regenerates the bootloader configuration. Empty body falls through to the hard-coded `acpi_enforce_resources=lax`, which is the documented default — so the security blast radius is narrow — but the silent-body-swallow pattern is the wrong default for a state-changing endpoint that just rewrote your bootloader config.

**Fix sketch:** in `runInstallHandler`, when `r.ContentLength > 0` and the handler's `fn` decodes its own body, the handler should call `isMaxBytesErr` and emit 413 explicitly. Cleaner: pass the parsed body into `fn` so the body parsing lives in one place.

### H2. `handleSetupReset` and `handleFactoryReset` delete the config file FIRST, then call `s.kvWiper()` — and the kvWiper's error is downgraded to a `Warn` log, not surfaced to the operator.

`internal/web/server.go:1381` (handleSetupReset) and `:1438` (handleFactoryReset):

```go
if err := os.Remove(s.configPath); err != nil && !os.IsNotExist(err) {
    http.Error(w, "remove config: "+err.Error(), http.StatusInternalServerError)
    return
}
if s.kvWiper != nil {
    if err := s.kvWiper(); err != nil {
        s.logger.Warn("factory reset: kv wipe failed", "err", err)
    }
}
```

If `kvWiper` fails — typical cause is `iox.EnsureFreeSpace` refusing the wipe under RULE-STATE-12, or the KV's persist returning ENOSPC — the **config.yaml has already been removed but the wizard / probe / calibration KV namespaces still hold the old polarity records, calibration results, and wizard outcome**. The next daemon start sees an empty config + stale KV state and lands in the wizard with a system that's been half-reset — the very "silent state divergence" RULE-STATE-12 was added to prevent.

The handler returns `{"status":"ok"}` to the UI regardless. From the operator's perspective the factory reset succeeded; on the next restart the wizard surfaces stale probe outcomes the operator just tried to clear.

**Severity High** because this is a class of bug Phoenix flagged at the senior-review level for the KV path; here it cuts the other direction (KV write blocked, config write succeeded).

**Fix sketch:** swap the order — wipe KV namespaces first, only remove the config file once kvWiper returns nil — OR roll back the config delete on kvWiper error (write the original config back from the in-memory pointer). The roll-back path is brittle; the swap is cleaner.

### H3. `handleSystemReboot` spawns a goroutine that calls `exec.Command("systemctl", "reboot").Run()` without a recover, without context binding, and without checking whether the daemon has any controllers under active manual override.

`internal/web/server.go:1585`:

```go
go func() {
    select {
    case <-time.After(300 * time.Millisecond):
    case <-s.ctx.Done():
        return
    }
    for _, cmd := range [][]string{
        {"systemctl", "reboot"},
        {"reboot"},
    } {
        if err := exec.Command(cmd[0], cmd[1:]...).Run(); err == nil {
            return
        }
    }
}()
```

Two concerns flagged in the senior-review M20 brief:

- **No `recover()`** — if `exec.Command(...).Run()` panics (rare but possible on resource exhaustion / FD leak), the goroutine dies and the reboot doesn't happen. The HTTP handler already returned 200 + `{"status":"rebooting"}` so the operator sees success in the UI but the host doesn't reboot. The daemon survives but in an inconsistent state.
- **No watchdog signal** — the reboot path doesn't call `s.wd.Restore()` before triggering `systemctl reboot`. The pre-reboot path is "200 → 300 ms sleep → reboot"; if the operator's BIOS is slow to handle the systemctl-triggered TERM/KILL, the daemon may not get a chance to write `pwm_enable=2` back. Modern systemd handles this via `KillMode=control-group`, but RULE-HWMON-RESTORE-EXIT documents the contract: every documented exit path restores. Reboot-as-handler is documented; let's make sure the watchdog Restore actually fires before the kernel-init sequence kicks in.

**Severity High** because the reboot button is the wizard's primary "fix bootloader → reboot" surface; a silent-no-reboot outcome strands the operator on a half-applied install.

**Fix sketch:** wrap the goroutine body in `defer recover()` with a log entry naming the panic and an INFO log noting the daemon will continue. Optionally call `s.wd.RestoreCtx(...)` (with the 1.8s budget per RULE-WD-RESTORE-BUDGET) before invoking `systemctl reboot` so the fan state is committed back to firmware before the kernel reboots.

## MEDIUM findings

### M1. `handleGrubCmdlineAdd` writes `param` to GRUB without re-validating it on the API boundary — relies entirely on `grub.AddParam`'s `validParam` check.

`internal/web/server.go:1807`. The handler accepts `{"param": "..."}` from the JSON body and passes it straight into `grub.AddParam(param, ...)`. The doc-comment says "the grub package's `validParam` gate refuses anything containing shell-special chars". The validation IS performed downstream, but there's no second-layer "API surface validates before dispatching to the helper" gate — a regression that removes the `validParam` check in the grub package would silently open shell-injection from this endpoint.

**Severity Medium** because the defence today works; the gap is defence-in-depth. The endpoint is auth+csrf gated, so an external attacker can't reach it without an authenticated session.

**Fix sketch:** call `grub.ValidateParam(param)` at the web-layer boundary (export it from the grub package if not already exported) before invoking `AddParam`. Or assert the contract via a comment + test binding so a future grub refactor that removes `validParam` fails CI rather than opens a CVE.

### M2. `handleSetPassword` does not invalidate other sessions belonging to the same user after a password change.

`internal/web/server.go:1997`. After the password change succeeds the handler returns `{"status":"ok"}` and the requesting session continues. Other sessions opened for the same operator on other browsers / tabs / devices are left alive — they'll keep working until their `SessionTTL` (default 60 min) expires.

The "change password" UX in most web apps either invalidates all sessions or invalidates all-other-sessions. Neither is wired here.

**Severity Medium.** If the change was triggered after a credential leak, the leaked session is still valid for up to `SessionTTL` after the rotation.

**Fix sketch:** after `storeAuthHash` succeeds, walk `s.sessions.sessions` and delete every entry whose CSRF token != the current request's session — or unconditionally clear all and let the operator re-login on every device. The simpler API is `sessionStore.deleteAllExcept(currentToken)`.

### M3. `handleSetupApplyMonitorOnly` writes `cfg = config.Empty()` (with web settings preserved) to disk and marks setup applied — without ANY validation that the wizard had actually been run, was in a state that warranted monitor-only mode, or that the operator's earlier explicit refusal still stood.

`internal/web/server.go:1353`. The handler is the operator-clicks-"Switch to monitor-only" button on the vendor-daemon recovery card. Once authenticated + CSRF-protected the operator can POST to it at any time and overwrite a perfectly working fan-control config with an empty config + monitor-only marker.

The expected flow is "wizard detected vendor daemon → operator confirms → this endpoint applies the deferral". The handler doesn't check the wizard's state or whether vendor-daemon detection actually fired. An authenticated mistake — or a misconfigured client — wipes the config.

**Severity Medium** because the wizard provides Reset to recover, and `restorePanic` / factory-reset workflows exist. But "one click destroys fan-control state" is a UX-class concern.

**Fix sketch:** require the wizard to be in `vendor_daemon_active` state OR the daemon to be in `applied=false` state before honouring this endpoint. Reject 409 otherwise with a body explaining why.

### M4. `handlePanic` writes MaxPWM to every configured fan via `writeMaxPWMToAllFans`, which calls `hwmon.WritePWM` directly — bypassing `polarity.WritePWM` (RULE-POLARITY-05) and the watchdog Register/Restore lifecycle.

`internal/web/panic.go:211`. The MaxPWM write at panic-start goes through `hwmon.WritePWM(fan.PWMPath, pwm)` for hwmon fans. On an inverted-polarity channel this writes `MaxPWM` literally instead of `255 - MaxPWM` (the inverted equivalent). For an inverted fan, that drives the fan to *minimum* speed during a panic — the opposite of the safety intent.

The panic surface comment specifically says "fans get the right write verb"; the comment doesn't acknowledge polarity inversion.

The watchdog also isn't told that the fan is being driven outside the controller's normal path — Watchdog.Register is what gives the daemon-exit Restore something to restore. Panic.restore comments say "the next controller tick will push each fan to its curve-derived value within one interval", relying on the controller to recover. That's fine for the post-panic recovery (controller writes via the right primitives), but during the panic itself an inverted fan is at the wrong duty cycle.

**Severity Medium.** A panic-button + inverted fan combination is uncommon — the polarity rule mostly catches BIOS-inverted fans on a few Gigabyte boards. But the existence of `polarity.WritePWM` + RULE-POLARITY-05 + RULE-POLARITY-10 is precisely to centralise this contract; the panic path violates it.

**Fix sketch:** look up the `*probe.ControllableChannel` for each `fan.PWMPath` and route the write through `polarity.WritePWM(ch, MaxPWM, hwmon.WritePWM)`. For phantom/unknown channels, `WritePWM` returns the right error — log and continue (same posture as the existing code).

### M5. `handleDiagSend` mutates `s.cfg` mid-handler when auto-generating the bearer token, without locking — race with concurrent `s.cfg.Load()` calls is not strictly serialised.

`internal/web/diag.go:177`:

```go
updated := *live
updated.Diag.UpstreamIngest.Token = token
if saved, err := config.Save(&updated, s.configPath); err != nil {
    s.logger.Warn("diag/send: token persist failed (continuing with in-memory token)", "err", err)
    s.cfg.Store(&updated)
} else {
    s.cfg.Store(saved)
}
```

`atomic.Pointer.Store` is atomic, so a concurrent `s.cfg.Load()` either sees the old or new pointer — no torn pointer. **But** the `*live = updated` deep-copy is field-wise; the slice/map fields (`live.Profiles`, `live.Schedules` etc.) are shallow-copied by value. A concurrent reader holding `live` who later iterates `live.Profiles` may observe a map that is being concurrently rewritten if another `handleProfileSchedule` is mutating profiles in the same window — that other handler uses a similar deep-copy pattern but copies the Profiles map explicitly (`next.Profiles = make(...)`). `handleDiagSend` does **not** copy Profiles/Schedules. A field-by-field shallow copy of the config struct shares the underlying maps with the previous pointer.

**Severity Medium** because the typical concurrency is one operator clicking buttons; the race window is small. But the pattern is brittle, and a future operator who hits both `diag/send` and `profile/schedule` in flight could observe a mid-mutation map.

**Fix sketch:** factor the deep-copy logic out (or use the same JSON marshal/unmarshal trick `handleConfidencePreset` uses on `:152`) so every handler that mutates the config makes a fully independent copy.

## LOW findings

### L1. The `setup.go` SSE handler busy-polls the event ring at 250 ms regardless of whether the wizard is active or quiescent.

`internal/web/setup_events_sse.go:19`. The cursor-poll ticker fires every 250 ms for every connected SSE subscriber, even when the wizard is idle and the ring has zero new events. Each tick still calls `s.setup.EventsSince(cursor)` which takes the manager's lock. With N idle browser tabs subscribed, that's N lock acquisitions per 250 ms.

**Severity Low.** EventsSince is fast on an empty ring, and the maximum N is tiny (one operator on one browser usually). But the post-MVP pattern would be channel-based push.

### L2. `runInstallHandler` accumulates the install log in a `[]string` that's appended to from inside `fn`, then JSON-encoded at the end — no upper bound. A misbehaving `EnsureKernelHeaders` that streams pathological log output (apt-get download progress is the typical case) can OOM the handler.

`internal/web/server.go:1675`. The accumulating slice has no cap. apt-get's `--progress-fd=1` output can run to many MB on a slow link with a giant downstream package list. The 1 MiB request body cap doesn't help here — that's on the *request* side, not the response. The response is then materialised in memory before write.

**Severity Low** because the handler is auth+csrf-gated and the operator initiated the install; an OOM on this path is annoying but not exploitable. Realistic apt/dnf output is bounded.

## Notes on what was checked and passed

- **CSRF gate (RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE):** every entry in `registerAPIRoutes` with `auth: true` is wrapped (`server.go:454`) before the auth check. `/login` and `/logout` are outside the helper but unauthenticated by design. `/login.js` is GET-only. `/healthz` and `/readyz` are GET-only. The diag download (`dlHandler`) is `s.requireAuth` wrapped + GET-only. No state-changing route bypasses CSRF.
- **Body-size cap (RULE-WEB-BODY-SIZE-CAP-1MIB):** every entry in `registerAPIRoutes` is wrapped (`server.go:449`) before the auth check. `/login` POST applies its own narrower 64 KiB cap (correct per the rule). `/set-password` applies a 64 KiB cap on top of the 1 MiB cap (correct). `/handleSetupLoadModule` applies a 256-byte cap on top (correct).
- **SameSite=Strict (RULE-WEB-COOKIE-SAMESITE-STRICT):** `setSessionCookie` uses `SameSiteStrictMode + HttpOnly=true`; `setCSRFCookie` uses `SameSiteStrictMode + HttpOnly=false`. Both correct.
- **Origin check:** `originCheck(logger)(s.mux)` wraps the entire mux from `server.go:399`. Confirmed in place.
- **Update staging path (RULE-WEB-UPDATE-STAGE-PATH-OUTSIDE-PRIVATETMP):** `installStagingDir = "/run/ventd"` at `update.go:425`. The writability probe + fallback to default tmp is preserved (`update.go:440`). Mode 0700 + mode 0755 on the script itself. Correct.
- **Update outcome surface (RULE-WEB-UPDATE-STATUS-FIDELITY):** `lastApplyOutcomePtr` is `atomic.Pointer[LastApplyOutcome]`. `omitempty` is preserved on `LastApplyError`. `watchUpdateApplyOutcome` exits cleanly on every documented path (success, fail, timeout, transient query error). The watcher is only spawned when `systemd-run` is the spawn primitive (`update.go:599`). Correct.
- **Auth middleware:** `requireAuth` is wrapped before `requireCSRF` and `requireMaxBody`. `requireAuth` returns 401 JSON for `/api/*`, 302 redirect for HTML pages. No state-changing public route observed.
- **Path traversal — diag download:** `bundleNameRe = ^ventd-diag-[A-Za-z0-9_.-]+\.tar\.gz$` matches against the trailing component only (`diag.go:81`). The regex denies `/`, `..`, leading dot, query string — correct.
- **SSE leaks:** `handleEvents` and `handleSetupEvents` both exit on `r.Context().Done()` / write failure / flush failure. Both use cursor-poll on a server-owned ring (no per-subscriber channel registration), so disconnect cleanup is automatic. No subscribers to unregister. Goroutine-clean.
- **Reboot recover:** see H3 — the goroutine has NO recover, which is the M20 finding. The 300ms `time.After` race with `s.ctx.Done()` is fine (selectable), but a panic inside `exec.Command(...).Run()` (rare but possible) propagates up and the runtime kills the goroutine.
- **`handlePanic` timer cleanup:** `restorePanic` stops the timer under the mutex; `handlePanic` stops any existing timer before arming a new one. No timer leak.
- **Session store concurrent-safety:** `sessionStore.mu` is held across all reads/writes. `reap` runs on a 15 min ticker scoped to ctx, exits on ctx.Done(). No leak.
- **Login rate limiter:** `loginLimiter` has `maxKeys = 4096`, evicts oldest on insert when capped, sweeps expired entries on a ticker. Bounded; goroutine scoped to ctx. Correct.
- **Reverse-proxy IP resolution (`resolveClientIP`):** walks XFF right-to-left, only consults XFF when peer is in trusted CIDR set. Matches the nginx-real-ip + Rails-RemoteIp standard. Correct.
- **Self-signed cert:** `EnsureSelfSignedCert` writes the key with mode 0600, cert with default mode. Permissions checked.
- **CSP:** `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:`. No `unsafe-inline`. Correct.
- **HSTS:** only emitted when `r.TLS != nil`, max-age=300. Conservative for a deployment that may downgrade. Correct.
- **Permissions-Policy:** denies camera/microphone/etc. Correct.
- **Catch-all `/`:** `handleUI` rejects any non-`/` path with 404 before serving the redirect HTML. Correct.
- **TLS sniff listener:** plaintext-on-TLS-port emits a 301 in a per-connection goroutine; goroutine is bounded (single read + write + close). No accumulation.

## Out of scope (per task brief)

- No code changes performed.
- No git commits.
- No GitHub issues filed.
- Test files (`*_test.go`) not audited beyond reading the bound-subtest names referenced in rule files.

## Diagnostic command (for next pass)

```bash
# Every authed state-changing handler that does NOT go through registerAPIRoutes
# (i.e. bypasses the 1 MiB body cap + CSRF gate):
rg -nP '^\s*s\.mux\.HandleFunc\(' /root/ventd-work/internal/web/ \
  | grep -v _test.go \
  | grep -v 'registerAPIRoutes\|/login\|/logout\|/healthz\|/readyz\|/login\.js\|/api/diag/download/\|registerSharedAssets\|registerWebPage'
```

The expected result is empty — every state-changing route should live in `registerAPIRoutes`. A non-empty result identifies new routes that need explicit middleware wrapping.
