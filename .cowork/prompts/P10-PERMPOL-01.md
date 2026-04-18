# READY TO DISPATCH — no unmet dependencies; held pending CC terminal capacity

P10-PERMPOL-01 is dispatchable now (deps on P0-01 only, met). Not
released this turn because four CC terminals are already running.
Cowork dispatches when one of the current 4 merges and capacity frees
up, OR when the employer greenlights a 5th parallel stream.

---

You are Claude Code, working on the ventd repository.

## Task
ID: P10-PERMPOL-01
Track: SUPPLY
Goal: add Permissions-Policy header and ETag on the embedded UI assets served by `internal/web`.

## Model
Claude Sonnet 4.6.

## Context you should read first (≤ 15 files)
- `internal/web/security.go` — current header middleware (exists per CHANGELOG; read it first)
- `internal/web/server.go` — router + embedded asset serving
- `internal/web/security_invariants_test.go` — existing security header tests (do NOT modify, only extend/add where required by the task)
- `testplan.md` §13 (security tests) — note `TestSec_PermissionsPolicy` and cardinality guarantees
- masterplan §8 P10-PERMPOL-01 entry
- `CHANGELOG.md`

## What to do
1. **Permissions-Policy header** in `internal/web/security.go`:
   - Add `Permissions-Policy: accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()` to every HTTP response served by the daemon.
   - Deny every feature the daemon does not need. Rationale comment inline.
   - Integrate with the existing security-header middleware; do NOT create a new middleware layer.

2. **ETag for embedded UI assets**:
   - On every `GET` to `/ui/...` (or wherever embedded assets are served), compute the SHA-256 of the asset bytes at startup, cache the hex prefix as the ETag.
   - Respond `304 Not Modified` when `If-None-Match` matches.
   - Do NOT compute SHA per-request — startup-compute, request-compare.

3. **Test updates**:
   - Extend `security_invariants_test.go` with `TestSec_PermissionsPolicy` asserting the header is present on every tested route and each deny-list feature is included.
   - Add `TestEmbeddedAssets_ETag_304` asserting the 304 path and the SHA stability.
   - Keep all existing tests passing.

4. **CHANGELOG entry** under `## [Unreleased]` / `### Added`:
   `- web: Permissions-Policy header + ETag on embedded UI assets (#P10-PERMPOL-01)`

## Definition of done
- `go build ./...` clean
- `go test -race ./internal/web` clean, including new and existing tests
- `curl -I https://localhost:<port>/api/ping` (or equivalent test server probe) shows `Permissions-Policy: ...` header
- `curl -I` with `If-None-Match: "<etag>"` returns 304 on the embedded UI route
- CHANGELOG entry present

## Out of scope
- Changes to any other security header (CSP, HSTS, etc.)
- Changes to authentication/CSRF/session logic
- Embedded asset pipeline changes
- Anything outside the allowlist

## Branch and PR
- Branch: `claude/SUPPLY-permpol-<5-char-rand>`
- Commit prefix: `feat(web):` or `feat(supply):` (pick the one that reads clearest)
- Draft PR: `feat(web): Permissions-Policy header + ETag on embedded UI (P10-PERMPOL-01)`
- PR body: goal; files-touched; curl -I outputs showing the header and 304 response; verification outputs; task ID.

## Constraints
- Allowlist: `internal/web/security.go`, `internal/web/security_invariants_test.go`, OR a new sibling test file if invariants test file doesn't accommodate cleanly. Optionally `internal/web/server.go` if ETag wiring genuinely needs it (note deviation inline).
- stdlib only
- CGO_ENABLED=0 preserved

## Reporting
PR body must carry:
- STATUS / BEFORE-SHA / AFTER-SHA / POST-PUSH GIT LOG
- BUILD / TEST outputs
- HEADER-CHECK: `curl -I` output (or test-server equivalent) showing Permissions-Policy
- ETAG-CHECK: first `curl -I` capturing the ETag, second `curl -I` with `If-None-Match` returning 304
- GOFMT / CONCERNS / FOLLOWUPS

See `.cowork/TESTING.md`. This task is CI-sufficient; no HIL or VM validation.
