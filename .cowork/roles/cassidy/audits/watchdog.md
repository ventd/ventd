# Audit checklist: `internal/watchdog/`

The watchdog is ventd's last line of defence. When the daemon exits for any reason — graceful SIGTERM, panic, error-return from main — the watchdog runs `Restore` and hands the fan back to firmware-auto. Every exit path without a paired Restore is a real safety bug.

Cross-reference: `.claude/rules/watchdog-safety.md` binds 7 `RULE-WD-*` invariants to subtests in `internal/watchdog/safety_test.go`. Caught bugs in this directory: none so far, but #287 was a rule-test semantic binding gap in this area. Fix landed in PR #300.

---

## Scope

PRs touching `internal/watchdog/watchdog.go`, `internal/watchdog/safety_test.go`, or any code in `cmd/ventd/main.go` that wires watchdog into the daemon lifecycle.

## Always-run checks (priority order)

### 1. Do all 7 `RULE-WD-*` invariants still bind to subtests?

The seven invariants (as of 2026-04-18 post-#300):

- `RULE-WD-RESTORE-EXIT` — every exit path (ctx cancel, error, panic) restores
- `RULE-WD-RESTORE-IDEMPOTENT` — Restore safe to call multiple times
- `RULE-WD-RESTOREONE-NO-DEREGISTER` — RestoreOne does not remove entry from watchdog's list
- `RULE-WD-RESTORE-CAPTURES-ORIGINAL` — Restore writes back the captured origEnable (or hardware fallback)
- `RULE-WD-REGISTER-LIFO` — Register pushes onto the stack; Restore pops LIFO
- `RULE-WD-REGISTER-DEDUP` — re-Registering the same path doesn't duplicate entries
- `RULE-WD-RESTORE-PANIC-RECOVER` — the inner restore for each entry runs inside a `recover()` so one bad entry doesn't block the others

**How:** `grep -rn '^Bound:' .claude/rules/watchdog-safety.md` — each line names a subtest in `safety_test.go`. Verify each named subtest exists.

CI catches this via `tools/rulelint`. If rulelint is green, skip to check 2.

**Fail if:** rulelint red, OR a new PR adds a `RULE-WD-*` without a Bound: line, OR a `Bound:` line names a subtest that doesn't exist.

### 2. Are the bound subtests semantically aligned with the rule prose? (This is what #287 was)

Rulelint verifies the Bound: subtest EXISTS. It does NOT verify the subtest's assertions actually test what the rule's prose claims.

**How:** for each rule touched by the PR, open the rule prose, then open the bound subtest, then verify the subtest's assertions match the prose. Examples:

- Rule says "RestoreOne writes back captured origEnable" → subtest MUST read the enable file after RestoreOne and assert `readback == origEnable` (not just "doesn't panic")
- Rule says "Restore runs on every exit path" → subtest MUST exercise each exit path (not just the happy case)

**Fail if:** bound subtest exists but doesn't actually test the rule's claim. File via TEMPLATES.md (no direct template — it's a rule-binding-drift issue; write custom).

### 3. Every `Register` call has a paired exit path that ends with `Restore` or `RestoreOne`?

Semi-automated: for every `w.Register(pwmPath, enable)` in the tree, trace whether the same goroutine's exit paths restore that entry.

**How:** `grep -rn 'wd\.Register\|watchdog\.Register' internal/ cmd/` — for each hit, walk up to the enclosing function and verify every `return` path leads (directly or via defer) to Restore.

**Fail if:** a new Register call has an exit path that doesn't restore. This is the main `RULE-WD-RESTORE-EXIT` semantic concern.

### 4. Is the panic-recover envelope complete?

Current design: panic during tick is recovered by a deferred `recover()` in `Controller.Run`; the recover path calls `wd.Restore()` before the goroutine exits.

**How:** on any PR that adds a new goroutine in the controller/watchdog/calibrate path, verify the new goroutine has `defer func() { if r := recover(); r != nil { wd.Restore(); panic(r) } }()` or equivalent pattern.

**Fail if:** new long-lived goroutine in safety-critical path without panic-recover that restores.

### 5. RestoreOne doesn't deregister entries (RULE-WD-RESTOREONE-NO-DEREGISTER)

Specific subtle invariant: `RestoreOne(pwmPath)` writes the restore value but leaves the entry in `w.entries`. Subsequent full `Restore()` still sees it and re-restores. This matters because RestoreOne is called on per-fan write failure (#263) — if it deregistered, a subsequent full Restore would miss the fan.

**How:** for any PR that modifies RestoreOne, verify `len(w.entries)` is unchanged before vs after the call.

**Fail if:** RestoreOne mutates the entries slice (append / remove / reorder).

### 6. No `log.Fatal` / `os.Exit` / `panic(` inside watchdog code

Watchdog is the SAFETY layer. It must not terminate the process — it must always return cleanly so the caller can do cleanup before exit. The daemon's `main()` is the only place that can decide to exit.

**How:** `grep -rn 'log\.Fatal\|os\.Exit\|panic(' internal/watchdog/` — should return zero hits.

**Fail if:** any hit outside a `TestXxx` function in a _test.go file.

### 7. Register is idempotent (same path twice = single entry)

Operator might load a config with a fan that's already registered (config reload, dynamic rebind). Register MUST deduplicate, not create two entries with different captured origEnable values.

**How:** test `safety_test.go:wd_register_dedup` covers this — verify it exists and actually asserts `len(w.entries)` is 1 after two Register calls with the same path.

**Fail if:** subtest removed or assertion weakened.

### 8. Shutdown order: watchdog restore happens AFTER controller shutdown

In `cmd/ventd/main.go`:

1. ctx.Cancel() or SIGTERM
2. Controller.Run returns (tick loop exits, restores entries it owned)
3. `defer wd.Restore()` in main fires (defence in depth — restores anything the controller missed)
4. os.Exit or normal return

**How:** verify `cmd/ventd/main.go`'s defer ordering. The `defer wd.Restore()` should be registered BEFORE the controller starts, so it fires AFTER the controller returns.

**Fail if:** defer order flipped, or a new code path exits via os.Exit bypassing deferred restore.

---

## Skim-pass (low budget)

1. Rulelint green? (check 1 automated)
2. Any new Register without matching Restore on exit? (check 3)
3. Any new goroutine in safety-critical path without panic-recover? (check 4)

---

## Not-audited

- Test verbosity (as long as assertions are present)
- Performance of Restore — it runs once per daemon lifetime; μs don't matter
- slog key naming (only if operator-facing)

## Related files

- `.claude/rules/watchdog-safety.md` — the rules (read when the PR touches them)
- `internal/watchdog/safety_test.go` — the bound subtests
- `cmd/ventd/main.go` — where watchdog wires into daemon lifecycle
- `tools/rulelint/main.go` — the CI check that binds prose to subtests (syntactic)
