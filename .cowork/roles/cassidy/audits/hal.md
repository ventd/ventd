# Audit checklist: `internal/hal/`

Mechanical checks to run on every PR that touches `internal/hal/`. Each is a yes/no question; any "no" is a blocker or warning I file. Priority-ordered — if I only have budget for 5 checks, I do the first 5.

Cross-reference: bug catches at `CAUGHT.md` #4, #5, #6, #9 all live in this directory. Templates at `TEMPLATES.md` cover each.

---

## Scope

This checklist applies to PRs touching:

- `internal/hal/backend.go` — the `FanBackend` interface itself
- `internal/hal/registry.go` — backend registry + `Resolve` / `Enumerate` / `Backend`
- `internal/hal/<name>/*.go` — any individual backend (`hwmon`, `nvml`, `asahi`, `pwmsys`, `crosec`, `ipmi`, `usbbase`, future additions)
- `internal/hal/contract_test.go` — the contract test that binds HAL rules to subtests

If the PR also touches `.claude/rules/hal-contract.md`, run the rule-file checklist too (see `rules-AUDIT.md` once written).

## Always-run checks (priority order)

### 1. Does every new backend implement all 6 FanBackend methods?

Interface: `Enumerate(ctx) ([]Channel, error)`, `Read(ch) (Reading, error)`, `Write(ch, pwm) error`, `Restore(ch) error`, `Close() error`, `Name() string`.

**How:** grep the new backend file for each method signature. If any missing, Go won't compile — but look for method-value hacks (`var _ hal.FanBackend = (*Fake)(nil)` type assertion) that satisfy the compiler without actually implementing correctly.

**Fail if:** any method is a `return nil` stub without a PR-body explanation of why it's safe.

### 2. Does `Restore` lie? (SILENT-ERROR — CAUGHT.md #5)

**The single highest-value check in this directory.** Restore is the last line of defence. If it swallows errors, the caller thinks the fan is back to firmware-controlled when it's still at the daemon's last commanded PWM.

**How:** read the Restore function top-to-bottom. For every conditional branch that could fail, verify the error is returned, not logged-and-discarded.

Look for:
- `if cc != 0 { log.Warn(...); return nil }` ← **FAIL**, file via TEMPLATES.md #2
- `if err != nil { log.Warn(...); return nil }` ← **FAIL** same
- `defer func() { _ = f.Close() }()` where Close failure is genuinely unrecoverable (rare — usually Close errors matter) ← flag as advisory
- `return nil` at the end of a function that has early `return err` branches — verify the happy path actually did the restore

**Fail if:** any path from "restore attempted" to "return nil" where the restore might not have happened.

### 3. Does `Write` clamp?

PWM is uint8 so overflow is handled by the type. But semantic clamping:

- `pwm=0` on a channel whose config has `min_pwm > 0` and `allow_stop=false` → MUST refuse. Per hwmon-safety rule 1.
- Above-`max_pwm` → MUST clamp, not overwrite.

**How:** find the PWM write path. Verify it consults `config.Fan.MinPWM` / `MaxPWM` / `AllowStop` OR delegates to a clamping helper (e.g. `controller.clamp(...)`).

**Fail if:** raw PWM goes to sysfs / ioctl / ec-cmd without a visible clamp.

### 4. Is `Restore` idempotent? Safe on never-opened channels?

Contract requirement. Operator Ctrl-C's the daemon before any Write fires; the shutdown path still calls `Restore(ch)` for every enumerated channel.

**How:** in the Restore function, trace what happens when `ch.Opaque` contains the zero-value state (no origEnable captured, no prior write recorded).

**Fail if:** Restore on a never-written channel panics, returns an error, or writes something unexpected (e.g. `pwm_enable = 1` on a channel that was always at `= 2`).

### 5. Does `Read` mutate state? (It shouldn't.)

Contract requirement. Read is called from the controller tick; it must be safe to call any number of times without side-effects.

**How:** look for any write operation inside Read — sysfs writes, ioctl with a write command code, internal struct-field assignments beyond cache-fills. Cache-fills are allowed IF the cache is internal and invisible to other methods.

**Fail if:** Read takes the backend into a different mode (e.g. writes `pwm_enable=1` as part of reading RPM).

### 6. Does `Close` match or exceed `Restore` semantics? Idempotent?

Close releases backend-level resources (library handles, sockets). Per contract: "Individual channel state is NOT tied to Close; callers should Restore channels before Close."

**How:** verify Close doesn't call Restore internally (that's the caller's job, not Close's). Verify calling Close twice is safe (second call returns nil, doesn't panic).

**Fail if:** Close-Close panics, or Close implicitly restores (confuses the contract).

### 7. Is `Enumerate` side-effect-free?

Enumerate is called once at startup but may be called again later (hot-plug). Each call observes current hardware state without mutating anything.

**How:** look for state mutation inside Enumerate — writing to sysfs, flipping mode bits, opening long-lived handles. Returning `Channel.Opaque` pointers that HOLD long-lived handles is legitimate; doing so as a side effect of Enumerate (e.g. opening a file descriptor per enumerated channel) is a leak risk.

**Fail if:** Enumerate opens N file descriptors that are never released if the channel is never Written to.

### 8. Does the backend's `Enumerate` overlap with another backend's? (DUPLICATE ENUMERATION — CAUGHT.md #9)

Especially when the new backend "delegates to hwmon" or matches hardware another backend already matches.

**How:** grep for `hwmonpkg.EnumerateDevices` or equivalent delegation in the new backend. If present, verify there's a filter/exclusion so the hwmon backend doesn't also return the same channels.

**Fail if:** two backends produce channels for the same physical device without coordination.

### 9. Does `Caps` bitset accurately describe the channel?

`CapRead` set → Read actually works. `CapWritePWM` set → Write accepts 0-255 and the device honours it. `CapRestore` set → Restore actually restores (see check 2).

**How:** cross-check Caps assignment in Enumerate against the method implementations. Common bug: Caps claims WritePWM but the backend only supports RPMTarget writes, or vice-versa.

**Fail if:** a Cap bit is set but the corresponding method path is a stub or a different operation.

### 10. Does the fake / test fixture match production semantics? (TEST-FIXTURE DRIFT — CAUGHT.md #4)

If the PR adds a `fake<backend>` fixture, verify it simulates real failure modes, not an idealised happy-path version.

**How:** for every production method, ask "what does the real library do when X fails?" — then verify the fake either simulates that or the fake's docstring says "does NOT simulate X."

**Fail if:** the fake's Close / Read / Write on a disconnected/busy device returns nil where production returns an error (e.g. `ENODEV`, `EAGAIN`, BMC completion code != 0).

---

## Rule-binding checks (run if the PR touches `.claude/rules/hal-contract.md` or `internal/hal/contract_test.go`)

### 11. Every `RULE-HAL-*` in the rule file has a `Bound:` line pointing at a subtest that actually exists

Rulelint catches syntactic drift (Bound: names a subtest that doesn't compile). It does NOT catch semantic drift (subtest exists but doesn't actually test what the rule claims).

**How:** for each `RULE-HAL-*` section in `hal-contract.md`, read the prose, then open `contract_test.go` at the subtest named in the Bound: line, then verify the subtest's assertions actually test the rule. If the rule says "Restore must write pwm_enable back to origEnable", the subtest must assert `readback == origEnable` at the end of Restore.

**Fail if:** prose-test semantic drift (this is what #287 was about).

### 12. Adding a new RULE-HAL-* requires adding a bound subtest in the same PR

No "I'll add the test in a follow-up" — rules without bound tests are aspirational.

**Fail if:** new rule lacks a Bound: line, or the Bound: subtest doesn't exist yet.

---

## Skim-pass checks (quick, low-budget sessions)

When I have only 2 minutes for a HAL PR:

1. Does Restore swallow errors? (check 2)
2. Does Write clamp? (check 3)
3. If new backend: does Enumerate overlap with another? (check 8)

Those three cover 3 of the 4 CAUGHT.md bug catches in this directory.

---

## Not-audited (explicitly)

- Go code style (gofmt, vet, golint run in CI — skip)
- Test coverage percentages (CI reports, skip unless PR claims a number I want to verify)
- Comment prose (read only if the comment is load-bearing — e.g. `// MUST NOT retain sensors map across ticks`)
- Error-message wording (skip unless the message gets surfaced to operators)
