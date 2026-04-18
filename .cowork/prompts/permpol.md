# permpol — P10-PERMPOL-01 Permissions-Policy + ETag on embedded UI

Alias: `permpol`. Sonnet 4.6. Depends on: P0-01 (merged).

## Goal

Ship Permissions-Policy header on every web response and ETag-based conditional GET on the embedded UI bundle.

## Task ID: P10-PERMPOL-01 (masterplan §8)

## What to do

1. Read: `internal/web/security.go`, `internal/web/server.go`, `internal/web/ui_embed.go` (whichever is the current embed module), `internal/web/headers_test.go` (if present).

2. In `internal/web/security.go`:
   - Add a `Permissions-Policy` header with at minimum: `accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()`. Set on every HTML response and every `/api/*` response. No `Permissions-Policy: *` — explicit deny list.
   - Add `Cross-Origin-Resource-Policy: same-origin` on HTML responses.

3. Add ETag middleware for static UI assets only (not `/api/*`):
   - Compute ETag as strong hex of `sha256(body)[:16]` at server boot (UI is embedded and immutable per binary).
   - On GET request for an embedded asset: if `If-None-Match` matches, return 304 with the ETag header and no body.
   - Cache-Control for embedded assets: `public, max-age=3600, must-revalidate`.
   - HTML entry page: no ETag (keep existing no-cache semantics — token flow depends on fresh HTML).

4. Tests in a new or extended `internal/web/security_invariants_test.go`:
   - Asserts Permissions-Policy header present on `/` and on one `/api/*` route with the expected deny list.
   - Asserts ETag round-trip: first GET returns ETag + 200, second GET with If-None-Match returns 304 empty body.
   - Asserts HTML page does NOT have ETag.
   - Asserts `/api/*` responses do NOT have ETag.

5. CHANGELOG under `[Unreleased]/Added`:
   - `- security: Permissions-Policy header and ETag caching on embedded UI (#P10-PERMPOL-01)`

## Definition of done

- Unit + integration tests green under `-race`.
- `curl -I https://localhost:9999/` shows Permissions-Policy and Cross-Origin-Resource-Policy.
- `curl -I` on an embedded asset shows ETag + Cache-Control.
- `go vet`, golangci-lint, gofmt all clean.
- Binary size delta ≤ +20KB.

## Out of scope

- Tests outside this task's scope.
- CSP changes (separate concern; already landed in v0.2.0).
- HSTS/TLS changes.
- Any restructuring of `internal/web/server.go` route registration.

## Branch and PR

- Branch: `claude/permpol-etag-Z3kN`
- Title: `feat(web): Permissions-Policy + ETag on embedded UI (P10-PERMPOL-01)`
- Draft PR; conventional commits.

## Constraints

- `CGO_ENABLED=0` compatible.
- No new direct dependencies (stdlib crypto/sha256 only).
- Preserve all existing security headers.

## Reporting

Standard: STATUS, PR URL, SUMMARY ≤ 200 words, CONCERNS, FOLLOWUPS.

## Model

Sonnet 4.6 — standard middleware work, no safety-critical paths.
