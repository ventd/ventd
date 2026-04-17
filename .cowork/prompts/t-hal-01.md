You are Claude Code, working on the ventd repository.

## Task
ID: T-HAL-01
Track: HAL
Goal: Lock the HAL backend contract as a table-driven invariant test bound 1:1 to a new `.claude/rules/hal-contract.md` rule file.

## Model
Opus 4.7 (rule-file authoring + invariant binding is safety-critical — do not downgrade).

## Context you should read first
- `ventdtestmasterplan.mkd` §2 (invariant binding pattern) and §17 (T-HAL-01 entry).
- `ventdmasterplan.mkd` §8 P1-HAL-01 entry (HAL interface just merged as #247).
- `.claude/rules/hwmon-safety.md` — reference implementation of the rule-file pattern.
- `internal/controller/safety_test.go` — reference for how rule subtests are structured.
- `internal/hal/backend.go` — the `FanBackend` interface.
- `internal/hal/registry.go` — backend registration.
- `internal/hal/hwmon/*.go` — first concrete backend, exercised in the contract test.
- `internal/hal/nvml/*.go` (or wherever the NVML backend landed in #247) — second concrete backend.
- `tools/rulelint/` — meta-lint that verifies every `Bound:` line resolves to a real subtest (will fail CI if you miss one).

## What to do

1. Create `.claude/rules/hal-contract.md` following the format in `.claude/rules/hwmon-safety.md`. Enumerate the HAL contract invariants each backend must satisfy. From testplan §2:
   - Enumerate is idempotent (calling it twice returns the same set of channels, modulo hot-plug).
   - Read never mutates observable state — no PWM writes, no pwm_enable flips.
   - Write clamps output to `[MinPWM, MaxPWM]` from channel Caps.
   - Restore is safe on channels that were never opened (no-op or clean error, no panic).
   - Caps is stable across the channel's lifetime (MinPWM, MaxPWM, supported ChannelRole don't change).
   - ChannelRole classification is deterministic for a given channel ID.
   - Close is idempotent.
   - Open on an already-open channel is either a no-op or a clean error, never a double-acquire.
   Each invariant gets a `## RULE-HAL-<NNN>` heading, a short paragraph explaining why, and a `Bound: internal/hal/contract_test.go:TestHAL_Contract/<subtest_name>` line.

2. Create `internal/hal/contract_test.go` with a table-driven `TestHAL_Contract` test. The test registers every backend that ships today (hwmon, nvml) via the `hal.Registry` and runs each invariant as a subtest against each backend. Use the existing fakehwmon fixture for hwmon; use fakenvml (or the equivalent from #247) for NVML. Each subtest's name must match the `Bound:` line in the rule file — the meta-lint will fail CI if any mismatch exists.

3. Add a stub for the first subtest that the NVML backend is expected to fail or skip (GPUs don't implement Restore the same way). Document the skip in the rule file: a backend may declare it doesn't support an invariant via a `Caps` bit; the contract test then skips that invariant for that backend rather than failing. This is a real design choice — do NOT invent a fictional caps bit; look at `internal/hal/backend.go` and use what exists. If no suitable caps bit exists, skip with a `t.Skipf("backend <X> does not implement invariant <Y>: <reason>")` and note the followup.

4. Run locally:
   - `go test ./internal/hal/... -run TestHAL_Contract -v` — all subtests pass.
   - `go vet ./...`, `golangci-lint run`, `gofmt` — all clean.
   - `go run ./tools/rulelint` — zero orphan rules, zero unclaimed subtests in bound files.

5. Update `CHANGELOG.md` under `## Unreleased`:
   - `test(hal): contract test T-HAL-01 binds backend invariants to .claude/rules/hal-contract.md`

## Definition of done
- `.claude/rules/hal-contract.md` exists with at least 7 invariants, each with a `Bound:` line.
- `internal/hal/contract_test.go` exists with a table-driven `TestHAL_Contract` exercising hwmon + nvml backends.
- Every `Bound:` line resolves to a real subtest (meta-lint green).
- `go test -race ./internal/hal/...` passes.
- CHANGELOG.md entry present.

## Out of scope
- Tests outside the scope this task targets per the testplan catalogue.
- Modifying the `FanBackend` interface or adding a new Caps bit (if one is needed, document it as a followup — it becomes a new P-task).
- Testing backends not yet merged (IPMI, liquid, crosec, pwmsys, asahi).
- Performance benchmarks (that's T-HOT-01's scope).

## Branch and PR
- Branch: `claude/hal-contract-T-HAL-01-<rand5>`
- Commit style: conventional commits
- Open a DRAFT PR with title: `test(hal): T-HAL-01 — lock backend contract invariants`
- PR body (max 15 lines):
  - Goal (one line).
  - Files touched (bullet list).
  - "How I verified" (2–3 lines covering: local test run, lint run, rulelint run).
  - Task-ID: T-HAL-01.

## Constraints
- Allowlist: `internal/hal/contract_test.go`, `.claude/rules/hal-contract.md`, `CHANGELOG.md`. Only these files. If you find you need another, STOP and report in `CONCERNS`.
- No new dependencies.
- Keep the main binary CGO_ENABLED=0 compatible (test files don't ship in the binary, but keep the test CGO-free for cross-compile lanes).
- Preserve all existing safety guarantees — this is a test-only PR, touching no production code.
- If blocked, push WIP, draft PR with `[BLOCKED]` prefix, "Blocker" section in the description.

## Reporting
On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <=200 words
- CONCERNS: anything second-guessed
- FOLLOWUPS: any scope edge you noticed (e.g. "Caps needs a `SupportsRestore` bit")
