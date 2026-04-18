# refactor-testfixture-base — extract shared Base struct for 12 fake* packages (#271)

You are Claude Code, working on the ventd repository.

## Task

ID: issue-271-testfixture-base
Track: TEST-INFRA
Goal: Eliminate the 33-line boilerplate duplicated across 12 `internal/testfixture/fake*` packages by extracting a shared `testfixture.Base` struct in the existing `testutil/` package (or a new `internal/testfixture/base/` package). Each `fake*` package embeds `Base` and contributes only protocol-specific method bodies. Per issue #271, take the "Alternative (lower effort)" path — NOT the codegen path. Codegen is tracked separately.

## Model

Sonnet 4.6. Test infrastructure refactor; mechanical, no behaviour change.

## Care level

Standard. This touches only `internal/testfixture/*` — no production code, no safety surfaces. Every existing test must still pass unchanged; that's the primary verification.

## Branch-base preamble — MANDATORY (lesson #18)

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/refactor-testfixture-base-$(openssl rand -hex 3) origin/main
test ! -f .cowork/prompts/refactor-testfixture-base.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

## Context you should read first (keep under 15)

1. `internal/testfixture/fakehwmon/fakehwmon.go` — the ONE fixture that has real implementation; study its shape to understand what NOT to touch
2. `internal/testfixture/fakecfg/fakecfg.go` — one of the 12 boilerplate-only fixtures (typical shape)
3. `internal/testfixture/fakedmi/fakedmi.go` — another
4. `internal/testfixture/fakeipmi/fakeipmi.go` — another (has slight variation)
5. `internal/testfixture/faketime/faketime.go` — confirm it's NOT in the 12 (it's a real implementation, keep untouched)
6. `internal/testfixture/fakedt/fakedt.go` — confirm it's NOT in the 12 (landed in P2-ASAHI-01 with real implementation)
7. `internal/testfixture/fakepwmsys/fakepwmsys.go` — confirm it's NOT in the 12 (has real implementation per P2-PWMSYS-01)
8. `internal/testfixture/fakehid/fakehid.go` — confirm it's NOT in the 12 (has real implementation per P2-USB-BASE)
9. `testutil/` — look at what's already there (CallRecorder, etc.)
10. `internal/hal/contract_test.go` — shows how fakes are consumed; refactor must not break any consumer
11. issue #271 body in this prompt (already in-context above)

## What to do

### Step 1 — Enumerate the 12 boilerplate-only fakes

The issue names 12 files explicitly:
- `internal/testfixture/fakecfg/fakecfg.go`
- `internal/testfixture/fakecrosec/fakecrosec.go`
- `internal/testfixture/fakedbus/fakedbus.go`
- `internal/testfixture/fakedmi/fakedmi.go`
- `internal/testfixture/fakeipmi/fakeipmi.go`
- `internal/testfixture/fakeliquid/fakeliquid.go`
- `internal/testfixture/fakemic/fakemic.go`
- `internal/testfixture/fakenvml/fakenvml.go`
- `internal/testfixture/fakepwmsys/fakepwmsys.go` **← VERIFY: landed as real impl in #277; if yes, SKIP this one**
- `internal/testfixture/fakesmc/fakesmc.go`
- `internal/testfixture/fakeuevent/fakeuevent.go`
- `internal/testfixture/fakewmi/fakewmi.go`

Read each file. Confirm the 33-line shape (constructor returning `&Fake{rec: testutil.NewCallRecorder()}` with `t.Cleanup`). Skip any file that has real implementation beyond boilerplate — flag it in the PR body.

Since `fakecrosec` landed real content per #282, `fakepwmsys` per #277, and `fakehid` per #281 — VERIFY each of these by reading the first 50 lines before rewriting. If the file has real protocol logic, skip it (only rewrite genuinely-boilerplate files).

### Step 2 — Land the shared Base struct

Create `internal/testfixture/base/base.go`:

```go
// Package base provides the shared scaffolding embedded by every
// stub fixture in internal/testfixture/fake*. Fixtures with real
// protocol implementations (fakehwmon, faketime, fakedt, fakepwmsys,
// fakehid, fakecrosec) do NOT embed this — they have their own
// constructors.
package base

import (
    "testing"

    "github.com/ventd/ventd/testutil"
)

// Base is the common skeleton shared across stub fixtures. Each fake
// embeds Base and adds its protocol-specific methods. The t.Cleanup
// wiring lives in the constructor NewBase.
type Base struct {
    T   *testing.T
    Rec *testutil.CallRecorder
}

// NewBase returns a Base wired for teardown. Callers typically wrap
// this in a fake-specific constructor:
//
//  func New(t *testing.T) *Fake {
//      return &Fake{Base: base.NewBase(t)}
//  }
func NewBase(t *testing.T) Base {
    t.Helper()
    b := Base{T: t, Rec: testutil.NewCallRecorder()}
    t.Cleanup(func() { /* reserved for future teardown */ })
    return b
}
```

### Step 3 — Convert each of the 12 boilerplate fakes

For each file in the list above, the shape becomes:

```go
package fakeXXX

import (
    "testing"

    "github.com/ventd/ventd/internal/testfixture/base"
)

// Fake is a stub <name> fixture. Methods are no-ops today; real
// protocol logic lands with its consuming backend (see
// internal/hal/<name>/).
type Fake struct {
    base.Base
}

// New returns a Fake wired for teardown.
func New(t *testing.T) *Fake {
    t.Helper()
    return &Fake{Base: base.NewBase(t)}
}
```

Preserve any protocol-specific methods that already exist on the Fake type. If the file's method bodies are all empty, leave them empty (the struct gets smaller but the API stays the same).

### Step 4 — Update any existing consumer

If a fake's test file or a downstream test accesses `f.rec` (lowercase) as an unexported field, they'll break because `Rec` is now on `base.Base` (exported). Do a targeted find: `grep -rn 'fakeXXX\.' internal/ | grep '\.rec\b'` — only matters if consumers reach inside.

### Step 5 — Verify nothing regressed

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/testfixture/... ./internal/hal/...
gofmt -l internal/testfixture/
go vet ./internal/testfixture/...
```

All must be clean.

### Step 6 — CHANGELOG

Append under `## [Unreleased] / ### Changed`:
`- test: extract testfixture.Base to de-duplicate 12 stub fixtures (#271)`

## Definition of done

- `internal/testfixture/base/base.go` exists and compiles.
- Every boilerplate fake file (confirmed-boilerplate only) embeds `base.Base` via its `Fake` struct.
- Fakes with real implementations (fakehwmon, faketime, fakedt, fakepwmsys, fakehid, fakecrosec, or any others detected in step 1) are UNTOUCHED.
- `go build ./... && go test -race ./... && gofmt -l internal/testfixture/` all clean.
- Net diff: ~30-50 LoC added in base.go, ~200-300 LoC removed across fakes; net negative.
- `git log --oneline origin/main..HEAD` shows 1 commit.
- CHANGELOG updated.

## Out of scope

- Codegen. That's a separate follow-up task (#271 offers codegen as the primary fix; we're taking the simpler alternative).
- Adding real protocol methods to any fake. This is mechanical de-duplication only.
- Touching `internal/hal/*` — only `internal/testfixture/*` and `CHANGELOG.md`.
- Tests for `testfixture.Base` itself — it's 2 fields and a constructor; coverage comes transitively from every consumer test.

## Branch and PR

- Branch: `claude/refactor-testfixture-base-<rand6>`
- Title: `refactor(testfixture): extract shared Base struct (closes #271)`
- Body: goal, files-touched counts, BRANCH_CLEANLINESS section (`git log --oneline origin/main..HEAD` + `git diff --stat origin/main..HEAD | tail -1`), verification commands + outputs, `Closes #271`.

## Constraints

- Files touched: `internal/testfixture/base/base.go` (new) + the 12 fake files (each minus ~20 lines) + `CHANGELOG.md`. Nothing else.
- No changes to `testutil/` package.
- No changes to consumers (`internal/hal/*`, test files).
- `CGO_ENABLED=0` compatible.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- BEFORE-SHA, AFTER-SHA, POST-PUSH GIT LOG, BRANCH_CLEANLINESS
- BUILD: `CGO_ENABLED=0 go build ./...` result
- TEST: `go test -race -count=1 ./internal/testfixture/... ./internal/hal/...` result
- GOFMT: `gofmt -l internal/testfixture/` result
- CONVERTED_COUNT: how many of the 12 fakes you rewrote; which ones you skipped and why
- LINES_REMOVED: net LoC delta
- CONCERNS: any fake that broke consumers, any unusual method-body content you found, anything ambiguous
- FOLLOWUPS: codegen (mentioned in #271); future CallRecorder extensions that could move into Base
