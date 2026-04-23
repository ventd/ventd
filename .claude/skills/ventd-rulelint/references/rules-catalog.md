# Rules Catalog

One-paragraph summary per `.claude/rules/*.md` file, listing the RULE-* IDs each file owns.

---

## attribution.md

Governs git identity, commit messages, and all public-facing content. No AI attribution (`Co-Authored-By: Claude`, "Generated with" footers, or any mention of AI/LLM) anywhere in commits, PRs, issues, docs, or code. All commits must be authored as `phoenixdnb`. PR bodies, README, and release notes must never reference how the code was produced. Remote is `github.com/ventd/ventd`. **No RULE-* bindings** — violations here are process/policy, not checked by rulelint.

---

## calibration-safety.md

Owns the `RULE-CAL-*` family. Governs the calibration sweep layer that probes fan hardware. Rules cover: `RULE-CAL-ZERO-FIRES` (sentinel escalates after exactly 2 s at PWM=0), `RULE-CAL-ZERO-CANCEL` (non-zero Set before deadline stops the timer), `RULE-CAL-ZERO-REARM` (second Set(0) after cancel starts a fresh 2 s clock), `RULE-CAL-ZERO-STOP` (Stop permanently disables the sentinel), `RULE-CAL-ZERO-RACE` (concurrent Sets are race-safe), `RULE-CAL-ZERO-STOP-IDEMPOTENT` (double-Stop must not panic), `RULE-CAL-ZERO-DURATION` (ZeroPWMMaxDuration=2s, SafePWMFloor in [20,80]). Detection rules: `RULE-CAL-DETECT-HAPPY` (sensor that rises with PWM wins), `RULE-CAL-DETECT-NO-WINNER` (no correlating sensor → empty path + nil error), `RULE-CAL-DETECT-NVIDIA-REJECT` (nvidia fans refused at entry), `RULE-CAL-DETECT-NO-FILES` (no fan*_input files → non-nil error), `RULE-CAL-DETECT-CONCURRENT` (second concurrent sweep on same path → "already running" error). All bound in `internal/calibrate/safety_test.go` and `internal/calibrate/detect_test.go`.

---

## collaboration.md

Defines how Phoenix and Claude sessions work together: tone (hard truth, no filler), decision-making (pick and execute, don't offer A/B choices), Phoenix-only actions (git tag, goreleaser release, force-push, PAT rotation, firewall changes), merge discipline (branch from origin/main, squash-merge via PR), issue-filing standards, and parallel-session etiquette. **No RULE-* bindings** — process rules, not enforced by rulelint.

---

## go-conventions.md

Ventd-specific Go style: `log/slog` only (no fmt.Println/log.Printf), `fmt.Errorf("context: %w", err)` wrapping, table-driven tests with `t.Run()`, no `init()` functions, `internal/` boundary enforcement, `context.Context` propagation for goroutines, and pre-commit gates (`go vet ./...`, `go test -race ./...`). **No RULE-* bindings** — enforced by the linter and reviewer, not rulelint.

---

## hal-contract.md

Owns the `RULE-HAL-*` family. Defines the contract every `hal.FanBackend` implementation must satisfy. Rules: `RULE-HAL-001` (Enumerate is idempotent), `RULE-HAL-002` (Read never mutates state), `RULE-HAL-003` (Write faithfully delivers requested duty cycle without re-clamping), `RULE-HAL-004` (Restore is safe on channels never opened), `RULE-HAL-005` (Caps stable across channel lifetime), `RULE-HAL-006` (ChannelRole deterministic across Enumerate calls), `RULE-HAL-007` (Close is idempotent), `RULE-HAL-008` (second Write to same channel does not re-issue mode command). All bound in `internal/hal/contract_test.go`.

---

## hwmon-safety.md

Owns the `RULE-HWMON-*` family — the largest rule family. Hardware safety invariants for the controller layer: `RULE-HWMON-STOP-GATED` (PWM=0 requires allow_stop+min_pwm=0), `RULE-HWMON-CLAMP` (duty cycle clamped to [min_pwm, max_pwm]), `RULE-HWMON-ENABLE-MODE` (pwm_enable=1 before first write), `RULE-HWMON-RESTORE-EXIT` (Watchdog.Restore on every exit path), `RULE-HWMON-SYSFS-ENOENT` (ENOENT/EIO logged and skipped, not fatal), `RULE-HWMON-PUMP-FLOOR` (pump fans never below pump_minimum), `RULE-HWMON-CAL-INTERRUPTIBLE` (calibration restores original PWM on abort), `RULE-HWMON-INDEX-UNSTABLE` (hwmon paths via device path, not index). Sentinel rules: `RULE-HWMON-SENTINEL-TEMP`, `RULE-HWMON-SENTINEL-FAN`, `RULE-HWMON-SENTINEL-VOLTAGE` (0xFFFF and implausible values rejected at read boundary), `RULE-HWMON-INVALID-CURVE-SKIP` (sentinel reading carries forward lastPWM), `RULE-HWMON-PROLONGED-INVALID-RESTORE` (30 s of consecutive sentinels → RestoreOne), `RULE-HWMON-SENTINEL-FIRST-TICK-IMMEDIATE-RESTORE` (sentinel on first tick → immediate RestoreOne), `RULE-HWMON-SENTINEL-STATUS-BOUNDARY` (sentinel filter applied at every serialization boundary including monitor.Scan). Bound in `internal/controller/safety_test.go`, `internal/hal/hwmon/safety_test.go`, and `internal/monitor/monitor_test.go`.

---

## usability.md

Defines the universal Linux compatibility contract: support all major distros and init systems, single static binary, curl-pipe-bash install, zero terminal use after install (all config via web UI), first-run wizard, human-readable error messages, and fan/sensor naming conventions. **No RULE-* bindings** — design guidelines enforced through code review.

---

## watchdog-safety.md

Owns the `RULE-WD-*` family. Governs the last-line-of-defence restore layer: `RULE-WD-RESTORE-EXIT` (every documented exit path writes pwm_enable back for all registered channels), `RULE-WD-RESTORE-PANIC` (per-entry panic recovery so one failing fan doesn't abort the rest), `RULE-WD-FALLBACK-MISSING-PWMENABLE` (missing pwm_enable file → log + write full-speed safety net, no early return), `RULE-WD-NVIDIA-RESET` (nvidia channels call nvidia.ResetFanSpeed, never write PWM=0), `RULE-WD-RPM-TARGET` (fan*_target channels restore via fan*_max, not raw PWM 255), `RULE-WD-DEREGISTER` (Deregister on unknown or already-removed path is a no-op), `RULE-WD-REGISTER-IDEMPOTENT` (startup origEnable survives re-registration). All bound in `internal/watchdog/safety_test.go`.

---

## web-ui.md

Server-side rendered HTML/JS embedded via Go `embed`, no build step, no npm. API endpoints under `/api/`, setup wizard at `/api/setup/*`, auth middleware in `auth.go` wrapping all routes except `/login`, `/logout`, `/api/ping`. First-boot prints one-time setup token. Vanilla JS only. Listen on `0.0.0.0:9999`. **No RULE-* bindings** — enforced by code review.
