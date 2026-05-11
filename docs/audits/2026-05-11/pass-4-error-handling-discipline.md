# Pass 4: Error-handling discipline

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, Pass 4
**Baseline commit**: post #1088 (M21 swap-monitor merged)
**Scope**: error-wrapping, error-path test coverage, sentinel-vs-string discipline across representative `internal/` packages.

## 1. Methodology

For each audited package: enumerate exported funcs returning `error` via `grep -nE '^func.*\) [A-Z].*error'`, count `fmt.Errorf("...: %w", err)` wrappings vs raw `return err` returns, enumerate `var Err...` sentinels, then `grep -rnE 'errors\.Is.*Err<Name>'` across the tree to find production callers. Error-path test coverage is measured by counting tests that assert non-nil `err` on a wrong-input or fault-injected call. Per-package text below summarises the resulting numbers. No code is modified by this pass; fix-in-place candidates land in the same PR; larger gaps are filed as issues.

## 2. Per-package findings

### `internal/state/`

21 exported funcs across `blob.go`, `kv.go`, `log.go`, `state.go`, `pidfile.go`, `version.go`; 11 of them return `error`. **Wrapping**: 30 `fmt.Errorf` sites vs ~7 raw `return err` (mostly forwarding internal helpers). **Coverage**: every public path-bearing surface has a bound RULE-STATE-* test exercising both happy and failure paths (atomicWrite ENOSPC, downgrade refusal, torn-record skip, transaction rollback, free-space gate). **Sentinels**: `ErrDowngrade`, `ErrCorruptState`, `ErrTransactionPersistFailed`, `ErrAlreadyRunning`, `iox.ErrInsufficientFreeSpace` (via state-pkg gate). All five are checked with `errors.Is` only in tests; **zero production call sites** type-check them (see Systemic Gap S1). Discipline is strong inside the package; the gap is downstream callers in `cmd/ventd/main.go` not branching on the sentinels.

### `internal/hal/hwmon/`

9 exported funcs; 5 return `error` (`Enumerate`, `Read`, `Write`, `Restore`, plus `Close` which always returns nil). **Wrapping**: 4 of 5 error returns wrap with `fmt.Errorf("hal/hwmon: ...: %w", err)`; one raw `return err` in `Read` forwards a sysfs read error without wrapping the channel ID — accepted because the caller already logs the channel context. **Coverage**: M17's new EBUSY rate tracker has 5 dedicated tests (`TestEBUSYRate_*`) covering happy, expiry, isolation, locked-thresholds. `TestWrite_EBUSY_ReacquiresAndRetries` and `TestWrite_PersistentEBUSY_FailsAfterOneRetry` exercise the retry-once contract. **Sentinels**: package consumes `syscall.EBUSY`, `fs.ErrNotExist`, `os.ErrPermission` via `errors.Is`, plus wraps `hal.ErrNotPermitted`. No package-local sentinels declared; discipline is excellent.

### `internal/coupling/`

22 exported funcs; 6 return `error` (`Shard.Update`, `Window.Add`, `Runtime.AddShard`, `Runtime.Run`, `Shard.Save`, `Shard.Load`, plus `New` constructor). **Wrapping**: persistence layer is 100% wrapped (`coupling: marshal: %w`, `coupling: persist: %w`, `coupling: corrupt bucket %s: %w`). Constructors and validators use bare `errors.New` for static messages — acceptable because no recovery branches exist (callers log and continue). **Coverage**: `TestShard_NCoupledCappedAt16`, `TestShard_HwmonFingerprintInvalidation`, `TestShard_SchemaVersionMismatchDiscards`, plus rejection assertions on bad-dimension `Update` and `AddShard` duplicate detection. **Sentinels**: none declared.

### `internal/marginal/`

19 exported funcs; 5 return `error` (`New`, `Shard.Update`, `Shard.Save`, `Shard.Load`, `Runtime.Run`). **Wrapping**: 4 of 5 wrap with `fmt.Errorf("marginal: ...: %w", err)`. Validation errors in `New` and `Update` use `errors.New` for static dimension-mismatch detection. **Coverage**: `TestShard_DimensionFixedAt2`, `TestShard_HwmonFingerprintInvalidation`, `TestShard_SchemaVersionMismatchDiscards`, plus `TestShard_RestoredReWarms`. The `persistence.go::decodeP` helper has internal `return nil, err` raw forwards that lose source-byte offset — minor (S4 below). **Sentinels**: none declared.

### `internal/signature/`

20 exported funcs; 8 return `error`. **Wrapping**: persistence layer 100% wrapped (`marshal %q: %w`, `set %q: %w`, `delete corrupt %q: %w`). `hash.go::LoadOrCreateSalt` uses raw `return nil, err` for `os.ReadFile` and `os.WriteFile` failures, losing the salt-path context (S3). **Coverage**: `TestSalt_FilePermissionsAre0600`, `TestSalt_LengthIs32Bytes`, `TestSalt_RejectsLooseFilePermissions`, `TestSalt_RegenerationOnMissingFile`, plus the four EvictPersistedBefore error-path tests landed in #1086. **Sentinels**: `ErrSaltFilePermissionsTooLoose` declared, wrapped in one error path, **never checked via `errors.Is`** anywhere — the test asserts only `err != nil`. Classic ghost-sentinel (S2). This PR fixes S2 + S3 inline.

### `internal/web/`

~30 handlers + helpers; the public API is mostly `http.HandlerFunc` (no error return) and a handful of constructors. Error-returning publics: `HashPassword`, `EnsureSelfSignedCert`, `Server.ListenAndServe`, `VersionInfo.Print`. **Wrapping**: 41 `fmt.Errorf` sites across `server.go`, `update.go`, `auth.go`, `selfsigned.go`. **Coverage**: handler error paths well-tested (`csrf_test.go`, `update_outcome_test.go`, `auth_regression_test.go`, `hardening_test.go` etc.). **Sentinels**: none declared package-local; consumes `polarity.ErrChannelNotControllable`, `polarity.ErrPolarityNotResolved` via `errors.Is` (`panic.go:265-266`). `http.MaxBytesError` properly handled via `errors.As` with a documented string-match fallback for json.Decoder wrapping (`security.go:399-405`). Discipline good. **No control-flow string-matching** anywhere — every `err.Error()` use is for display payload only.

### `internal/watchdog/`

8 exported funcs; only 2 return `error` (`readPWMEnableWithDeadline` indirect, plus internal-state-decode forwarding). The package is structurally void-returning by design — errors become log lines + Restore continues to next entry per RULE-WD-RESTORE-PANIC. **Wrapping**: `deadline.go` wraps with `fmt.Errorf("watchdog: ...: %w", ctx.Err())` on cancel paths. **Coverage**: exemplary — 17+ subtests under `safety_test.go` cover every documented exit path including panic-continue, missing pwm_enable fallback, NVML auto, RPM-target, IPMI routing, per-syscall deadlines. The package's error-handling discipline is the cleanest in the audit; failure modes are designed to log-and-continue rather than propagate. **Sentinels**: none — appropriate given the no-error contract.

### `cmd/ventd/main.go` (`runDaemonInternal`)

~700 LOC function. **Wrapping**: 2 wrapped error returns within the function (`fmt.Errorf("re-probe: %w")`, `fmt.Errorf("resolve control: %w")`). Most failure modes log via `logger.Error/Warn` and continue (degraded-mode semantics). **Coverage**: `regression_466_test.go` runs `runDaemonInternal` end-to-end and asserts no goroutine leaks across SIGHUP. **Sentinels**: 1 production `errors.Is` use (`hwmonpkg.ErrNoPWMChannelsAppeared` in `setup.go:972`); all other state-pkg sentinels declared above flow through here without branching. The function's strategy is "log and degrade" not "structured failure recovery" — which is the correct daemon-startup posture but means sentinel-based branching is unused at the top level.

## 3. Systemic gaps

### S1 — State-package sentinels declared but never branched on at top level (FILE AS ISSUE)

`state.ErrDowngrade`, `ErrCorruptState`, `ErrTransactionPersistFailed` are declared with `errors.Is`-style API surface and tested with `errors.Is` in `state_test.go:282/1047/1103`, but **zero production callers** in `cmd/ventd/main.go` or anywhere outside `internal/state/` branch on them. The daemon-startup path catches the `state.Open` error generically and falls through to log+exit. Recommendation: file an audit issue — either wire branching at `cmd/ventd/main.go` around `state.Open` (e.g. `ErrDowngrade` → distinct exit code, `ErrCorruptState` → operator-actionable diagnostic) or document the sentinels as for-test-only.

### S2 — `signature.ErrSaltFilePermissionsTooLoose` ghost-sentinel (FIXED IN THIS PR)

Declared at `/root/ventd-work/internal/signature/hash.go:46`, wrapped in the rejection branch at line ~99, but **never checked via `errors.Is`** anywhere in the tree — including the very test that exercises the failure mode (`hash_test.go:117` asserted only `err != nil`). This PR amends `TestSalt_RejectsLooseFilePermissions` to use `errors.Is(err, ErrSaltFilePermissionsTooLoose)` so the sentinel becomes the test contract.

### S3 — `signature.LoadOrCreateSalt` loses salt-path context (FIXED IN THIS PR)

`internal/signature/hash.go` `LoadOrCreateSalt` uses raw `return nil, err` for `os.ReadFile` / `os.WriteFile` failures inside, losing the salt-path context. An operator who runs the daemon and sees "permission denied" in journald has no way to know whether the salt path or some unrelated path failed. This PR wraps both with `fmt.Errorf("signature: read/write salt %s: %w", path, err)`.

### S4 — `internal/marginal/persistence.go::decodeP` loses row/col context (FILE AS ISSUE)

`decodeP` matrix helper returns `nil, err` raw on per-element decode failures, losing index context. A corrupt persisted shard surfaces as "decode: invalid msgpack type" with no row/col indication. Recommendation: file an audit issue — wrap with `fmt.Errorf("decode P[%d,%d]: %w", i, j, err)` matching the pattern already used on the same file for the non-finite check.

### S5 — `iox.ErrInsufficientFreeSpace` never branched on by production callers (FILE AS ISSUE)

`iox.ErrInsufficientFreeSpace` is declared with extensive documentation promising `errors.Is`-style use by callers (`freespace.go:43`), and the state package's `RULE-STATE-12` gate wraps it correctly, but **no production caller** branches on it with `errors.Is`. The daemon currently treats all KV write failures generically. Recommendation: file an audit issue — when the daemon-startup path eventually wires a disk-full doctor card (per RULE-DOCTOR-DETECTOR-PERMISSIONS extension), it should consume this sentinel via `errors.Is` so the surfaced cause is preserved across the wrap chain.

## 4. Closed-out items (already addressed in earlier passes)

- **RULE-WD-PER-SYSCALL-DEADLINE**: pass-6-watchdog already enforced per-syscall deadline error wrapping (`watchdog: write %s abandoned: %w`); covered by 4 bound tests. No further action.
- **RULE-HWMON-MODE-REACQUIRE EBUSY paths**: pass-6-hal-hwmon's M17 ladder bumped the EBUSY surface with full test coverage; the raw `return err` in `Read` was previously flagged and accepted as forwarding-not-wrapping since the caller logs the channel context.
- **`internal/hwdb/` sentinels (`ErrCatalog`, `ErrSchema`, `ErrNoMatch`)**: pass-3-rule-binding-integrity verified these are checked via `errors.Is` at production call sites in the matcher and validator; closed.
- **`polarity.ErrChannelNotControllable`/`ErrPolarityNotResolved`**: pass-3-rule-binding-integrity-polarity confirmed the four production `errors.Is` call sites (`controller.go:328-329`, `web/panic.go:265-266`, `opportunistic/prober.go:116`, `envelope/envelope.go:124`); closed.
- **Test bindings for raw-error returns in coupling/marginal validators**: pass-3-rule-binding-integrity-cpl and -cmb confirmed `TestShard_NCoupledCappedAt16` and `TestShard_DimensionFixedAt2` cover the bare-`errors.New` paths; closed.

## 5. Bottom line — v0.6.0 ship readiness

**Ship-readiness verdict: GO.** The error-handling story is structurally sound: wrapping is the dominant pattern, watchdog uses log-and-continue correctly, web uses `errors.As`/`errors.Is` plus one documented string-match fallback, and the smart-mode estimator packages (coupling/marginal/signature) all have explicit error-path tests bound to RULE-* rules. Of five gaps:

- **S2 and S3** are fixed inline in this PR (≤4-line changes; sentinel-Is test + salt-path wrap).
- **S1, S4, S5** are filed as audit issues for v0.6.1 follow-up. They affect operator triage (S1, S4) or future feature wiring (S5) but don't break runtime correctness.

None block v0.6.0. The v0.5.x cycle's discipline holds: rule bindings constrain the production code paths; sentinels are either properly used or surfaced for cleanup; wrapping is the default. Pass 4 closed.
