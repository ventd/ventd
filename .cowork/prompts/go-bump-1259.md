You are Claude Code, working on the ventd repository.

## Task
ID: ULTRA1-BLOCKER-01
Track: INFRA
Goal: Bump Go toolchain from 1.25.0 to 1.25.9 to close 17 Go standard library CVEs reachable from production code paths. Identified by ultrareview-1 as the only blocker on the v0.3.0 release.

## Care level
LOW. Single-line change in go.mod followed by `go mod tidy`. No code changes required. CI will catch any regression.

## Context

The ultrareview at `.cowork/reviews/ultrareview-1.md` identified 17 CVEs from `govulncheck`, all reachable from `cmd/ventd/main.go` and fixed in go1.25.9. Example traces:

- `GO-2026-4947` (crypto/x509, fixed go1.25.9)
- `GO-2025-4012` (net/http, fixed go1.25.2): traced to `web.Server.ListenAndServe` → `http.Server.ServeTLS`
- `GO-2025-4008` (crypto/tls ALPN): traced to `web.Server.ListenAndServe` → `tls.Conn.HandshakeContext`
- `GO-2025-4009` (encoding/pem): traced to `web.fingerprintCert`

**Blocking release:** ventd v0.3.0 cannot ship with known-vulnerable stdlib. Tagging is gated on this landing.

## What to do

1. Edit `go.mod`: change `go 1.25.0` to `go 1.25.9`. This is the only text change in that file.

2. Run `go mod tidy`. Commit any changes to `go.sum` it produces.

3. Verify `govulncheck ./...` reports zero reachable CVEs:
   ```
   go run golang.org/x/vuln/cmd/govulncheck@latest ./...
   ```
   If any vulnerabilities remain reachable from production, flag them in the PR body.

4. Run the full test suite under `-race`:
   ```
   CGO_ENABLED=0 go test -race -count=1 ./...
   ```

5. Confirm the binary still builds:
   ```
   CGO_ENABLED=0 go build -o /tmp/ventd-test ./cmd/ventd/
   ls -la /tmp/ventd-test   # note the size; should be similar to baseline 12.5 MB
   ```

6. Open a PR: `chore: bump Go toolchain to go1.25.9 (closes 17 stdlib CVEs)`.

7. In the PR body, include the govulncheck before/after output and a one-liner per CVE class showing which production packages were exposed.

## Definition of done

- `go.mod` line 3 reads `go 1.25.9`.
- `go.sum` reflects any transitive churn from the bump.
- `govulncheck ./...` returns zero reachable vulnerabilities.
- Full test suite passes under `-race`.
- Binary builds; size delta recorded in PR body.
- CHANGELOG.md `## Unreleased` / `### Security` section gets a one-line entry referencing the bump.

## Out of scope

- Any other dependency bumps. If `go mod tidy` wants to change non-stdlib deps, revert those changes and leave them for a separate PR.
- Code changes. This is infrastructure only.
- Any ultrareview-1 findings other than ULTRA-10 finding 1.

## Branch and PR

- Branch: `claude/chore-go-1259-bump`
- Title: `chore: bump Go toolchain to go1.25.9 (closes 17 stdlib CVEs)`

## Constraints

- Files touched: `go.mod`, `go.sum`, `CHANGELOG.md`. Nothing else.
- `CGO_ENABLED=0` must still build cleanly (baseline: 12.5 MB binary).
- All existing tests must still pass.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional field: GOVULNCHECK_AFTER with full output showing zero reachable CVEs.
