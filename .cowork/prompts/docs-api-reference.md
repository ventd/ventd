# docs-api-reference — create docs/api.md HTTP API reference (#269)

You are Claude Code, working on the ventd repository.

## Task

ID: issue-269-docs-api
Track: DOCS
Goal: Create `docs/api.md` — a comprehensive HTTP API reference for ventd's 54+ registered routes, organised by functional area, so operators and integrators can script ventd without reading source.

Files Cassidy cited:
- `internal/web/server.go` — route registrations (primary source)
- `internal/web/api.go` — request/response struct shapes
- `internal/web/profiles.go`, `internal/web/schedule.go`, `internal/web/history.go`, `internal/web/hardware.go`, etc. — per-area handlers
- `.cowork/FEATURE-IDEAS.md` FP-08 — why this matters (hardware-neutral remote-control API depends on spec)

## Model

Sonnet 4.6. Docs-only, single-file, isolated allowlist.

## Care level

Standard. No code touched; no safety surfaces. Reviewer checks the `docs/api.md` contents against the actual route registrations in source, not against any golden or test.

## Branch-base preamble — MANDATORY (lesson #18)

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/docs-api-reference-$(openssl rand -hex 3) origin/main
# Sanity check: main must not contain cowork/state prompts
test ! -f .cowork/prompts/docs-api-reference.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

If the sanity check fails, CC halts and reports rather than committing polluted history.

## Context you should read first (keep under 15)

1. `internal/web/server.go` — the full file; grep for `HandleFunc` and `Handle(` to enumerate every route
2. `internal/web/api.go` — request and response struct definitions
3. `internal/web/profiles.go` — profile endpoints
4. `internal/web/schedule.go` — scheduler endpoints
5. `internal/web/history.go` — history endpoints
6. `internal/web/hardware.go` (or wherever the `/api/hardware*` handlers live)
7. `internal/web/setup_handlers.go` (or the equivalent file for `/api/setup/*` + `/api/set-password` endpoints)
8. `internal/web/panic.go` (or wherever `/api/panic*` lives)
9. `internal/web/calibrate.go` (or the equivalent for `/api/calibrate/*` + `/api/detect-rpm`)
10. `internal/web/hwdiag.go` (or equivalent for `/api/hwdiag*`)
11. `docs/config.md` — format and tone template; match it
12. `README.md` — high-level project tone; keep docs consistent

Do NOT read the whole `internal/web/` tree — it's large. Read the route-registration site in `server.go` and use that index to find specific handlers.

## What to do

1. Grep `internal/web/*.go` for `HandleFunc\|Handle(` to enumerate every registered route. Record method (GET/POST/PUT/DELETE), path (including any `/api/v1/` mirror), and handler function name.

2. For each route, find the handler function. Read enough of it to determine:
   - Authentication requirement (`session required` / `setup token` / `none`)
   - Request body shape (look for `json.NewDecoder(r.Body).Decode(&X)` — X's type is the request shape)
   - Response body shape (look for `json.NewEncoder(w).Encode(X)` or `writeJSON(w, X, ...)` — X's type is the response shape)
   - A one-sentence description of what it does

3. Create `docs/api.md`. Structure:
   - **Intro** (~10 lines): ventd exposes an HTTP API on `localhost:<port>` (default 9999). Most routes require session auth (POST `/api/login`); a small set use the one-shot setup token for bootstrap. All request/response bodies are JSON unless noted.
   - **Authentication** section: session cookie flow, setup-token flow, rate limits.
   - **Status & events** section: `/api/status`, `/api/events` (SSE).
   - **Configuration** section: `/api/config*`, `/api/profile*` (incl. schedule).
   - **Hardware & calibration** section: `/api/hardware*`, `/api/calibrate*`, `/api/detect-rpm`.
   - **System** section: `/api/system/*`, `/api/panic*`.
   - **Setup wizard** section: `/api/setup/*`.
   - **Auth** section: `/api/set-password`, `/api/login`, related.
   - **Diagnostics** section: `/api/hwdiag*`, `/api/debug/*`.
   - **History** section: `/api/history`.
   - **`/api/v1/` mirror** note at the end if any routes have a v1 mirror — list them.

4. For each route, use this template:

   ```markdown
   ### `METHOD /api/path`

   One-sentence description.

   **Auth**: session | setup-token | none

   **Request body** (if applicable):
   ```ts
   { field: type, ... }  // TypeScript-style notation
   ```

   **Response**:
   ```ts
   { field: type, ... }
   ```

   **Example**:
   ```bash
   curl -X METHOD https://localhost:9999/api/path \
        -H 'Cookie: session=...' \
        -d '{"field": "value"}'
   ```
   ```

   Omit the Example for trivial GETs with no body (just the top two parts).

5. Use TypeScript-style type notation for shapes. `string`, `number`, `boolean`, `string[]`, `{ field: type }`, etc. Name types that repeat (e.g., `Sensor`, `Fan`, `Profile`) and define them once at the top of the file under a `## Type reference` section.

6. Do NOT invent endpoints or response shapes. If a handler's response shape is ambiguous from source, mark it `**Response**: _see source (internal/web/foo.go:handlerName)_` rather than fabricating.

7. Add a CHANGELOG entry under `## [Unreleased] / ### Added`:
   `- docs: HTTP API reference at docs/api.md (Cassidy finding, #269)`.

8. Verify:
   ```bash
   # Markdown lints cleanly; no broken internal links
   gofmt -l .  # should be empty (no Go files changed)
   git diff --stat origin/main..HEAD | tail -1  # should be: 2 files changed (docs/api.md + CHANGELOG.md)
   ```

## Definition of done

- `docs/api.md` exists. At least 50 routes documented (goal: all 54+). Each route has auth, request shape, response shape at minimum.
- `docs/api.md` organised into the 9 functional-area sections listed above.
- `## Type reference` section defines shared types.
- No source files modified except `CHANGELOG.md`.
- `git diff --stat origin/main..HEAD | tail -1` shows 2 files changed.
- `git log --oneline origin/main..HEAD` shows 1 commit.

## Out of scope for this task

- Modifying any `internal/web/*.go` source code. This is docs-only.
- Adding tests (R19 doesn't apply — no source changed, and #269 has no `Fixes:` requiring a regression test).
- Updating `docs/config.md` or other existing docs except CHANGELOG.
- Implementing any new endpoint or changing any existing handler.
- Touching `.cowork/`, `.claude/`, `.github/`, `deploy/`, or any directory outside `docs/` + `CHANGELOG.md`.

## Branch and PR

- Work on branch: `claude/docs-api-reference-<rand6>`
- Commit style: conventional commits
- Open a PR (ready-for-review, not draft) with title: `docs: HTTP API reference (closes #269)`
- PR body must include:
   - Goal verbatim from this prompt
   - Route count: how many routes documented / how many registered (grep-based count)
   - `BRANCH_CLEANLINESS`: paste `git log --oneline origin/main..HEAD` and `git diff --stat origin/main..HEAD | tail -1`
   - `How I verified`: gofmt, diff-stat, spot-check of 3 routes against source
   - `Closes #269`

## Constraints

- Files touched must be exactly: `docs/api.md` (new), `CHANGELOG.md` (one-line append). Nothing else.
- Do not add new dependencies.
- Keep the main binary `CGO_ENABLED=0` compatible (no source touched anyway; this is belt-and-suspenders).
- Preserve all existing safety guarantees (no source touched; safety unchanged).
- If you find a handler whose registration exists but whose implementation is stub / unreachable / returns 501, document it as `**Status**: stub, not yet implemented` but still include it for completeness.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- BEFORE-SHA: <base sha>
- AFTER-SHA: <head sha>
- POST-PUSH GIT LOG: `git log --oneline origin/main..HEAD`
- BRANCH_CLEANLINESS: paste `git log --oneline origin/main..HEAD` + `git diff --stat origin/main..HEAD | tail -1`
- ROUTE_COUNT: documented / total
- SUMMARY: <= 150 words
- CONCERNS: ambiguities, stub endpoints, routes you couldn't find shapes for
- FOLLOWUPS: work adjacent to scope — e.g., OpenAPI yaml generation, Prometheus integration doc, SDK bindings
