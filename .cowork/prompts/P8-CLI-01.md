# P8-CLI-01 — ventdctl CLI over unix socket

**Care level: MEDIUM.** A separate binary that talks to the daemon
through a unix socket. Privilege boundary matters: the socket is
root-only or matched to the daemon's user. Accidental world-readable
socket = fleet-wide privilege escalation. Treat socket permissions as a
security-critical path.

## Task

- **ID:** P8-CLI-01
- **Track:** CLI (Phase 8)
- **Goal:** `ventdctl` command-line tool that talks to the daemon over
  a unix-domain socket at `/run/ventd/ventdctl.sock`. Subcommands:
  `list`, `get`, `set`, `calibrate`, `profile`, `apply`, `diff`.

## Context you should read first

- `cmd/ventd/main.go` — daemon bootstrap; you'll add a socket listener.
- `internal/web/api.go` — existing HTTP endpoints; the CLI will
  largely mirror the HTTP API over the socket with a simpler JSON-RPC
  envelope.
- `deploy/ventd.service` — systemd unit, to understand file-system
  restrictions (ProtectSystem, RuntimeDirectory, etc).
- `docs/` — existing operator docs, to place the new CLI reference.

## Design — read carefully, do not deviate

### Socket contract

- Path: `/run/ventd/ventdctl.sock`
- Owner: same uid as ventd daemon process.
- Mode: `0660`. Group-readable so a `ventdctl` group can be used for
  non-root access (documented but not configured by default).
- Created on daemon startup under `RuntimeDirectory=ventd` (systemd
  creates /run/ventd with correct perms).
- Removed on daemon shutdown (defer os.Remove).

### Wire format

JSON-RPC-lite. Request:

```json
{"method": "list", "params": {}, "id": 1}
```

Response:

```json
{"result": {...}, "id": 1}
// or
{"error": {"code": 404, "message": "channel not found: fan99"}, "id": 1}
```

One request per connection, one response, then close. No pipelining,
no keepalive. Small, simple, auditable.

### Methods

- `list` — returns `[{id, backend, role, pwm, rpm}]` for all channels.
- `get {channel}` — single-channel detail including temp, curve, recent
  samples.
- `set {channel, pwm}` — manual PWM override (same path as web UI's
  manual-mode). Validates channel exists, PWM 0-255. Marks manual
  override.
- `calibrate {channel?}` — runs calibration; channel optional (default
  all). Returns result summary.
- `profile` — shows current hardware profile match (fingerprint ID +
  source).
- `apply {path}` — reads a YAML file, validates as a full config,
  applies atomically (same path as the web UI's config-PUT).
- `diff` — compares current in-memory config against what's on disk at
  `/etc/ventd/config.yaml`. Returns unified diff.

### CLI binary structure

`cmd/ventdctl/main.go` with subcommand dispatch (stdlib `flag` per
subcommand; no cobra dependency for a ~100-line CLI). Each subcommand:

1. Parse flags.
2. Connect to socket.
3. Marshal JSON-RPC request.
4. Read response (bounded reader with 1 MB limit — prevents a
   runaway daemon response exhausting CLI memory).
5. Pretty-print to stdout (tables for `list`, JSON for machine-readable
   mode via `-json` flag).
6. Exit code: 0 success, 1 generic failure, 2 RPC error, 3 IO error.
   Document in each subcommand's --help text.

### Daemon-side handler

New file `internal/rpc/socket.go`. Accept loop with context-aware
shutdown:

```go
func Serve(ctx context.Context, path string, router *Router) error {
    // Listen with explicit 0660 mode, chown to correct uid.
    // Accept in loop; each conn handled in its own goroutine with a
    // 5-second deadline on both read and write.
    // Graceful shutdown on ctx done: close listener, wait up to 2s
    // for in-flight goroutines.
}
```

Router dispatches to method handlers. Each handler gets a
`RequestContext{cfg *Snapshot, controller *Controller}` so it can read
current state or invoke controller actions.

### Security

- Socket mode 0660, owner == daemon uid. Enforced at creation.
- Request size limit 64 KB (bounded reader). Rejects oversized with
  RPC error.
- Response size limit 1 MB.
- Connection timeout 5s (both read and write deadlines set on accept).
- No auth beyond filesystem permissions. Rationale: unix-socket
  permissions are the standard privilege model on Linux. Anyone who
  can access the socket is trusted.

### Tests (R19-compliant)

1. `TestCLI_List_HappyPath` — spin up a mock socket server returning
   a canned list response; ventdctl list prints expected output with
   exit 0.
2. `TestCLI_ConnectionRefused_ClearError` — socket absent; ventdctl
   prints "ventd daemon not running or socket not accessible"; exit 3.
3. `TestRPC_MethodNotFound_Returns404` — router called with unknown
   method returns 404 error JSON.
4. `TestRPC_OversizedRequest_Rejected` — 64KB+1 byte request rejected
   cleanly, no daemon crash.
5. `TestRPC_MalformedJSON_Rejected` — invalid JSON rejected with 400
   error.
6. `TestRPC_ConnectionTimeout_Enforced` — stalled client gets
   disconnected at 5s.
7. `TestSocket_ModeIs0660` — after socket creation, file mode is 0660.
8. `TestSocket_RemovedOnShutdown` — after graceful shutdown, socket
   file is absent.

## Out of scope for this PR

- Fleet mode / multi-daemon aggregation (P8-FLEET-01).
- Shell completion (follow-up task).
- Man page generation (follow-up task).
- Windows / macOS equivalent (unix sockets work on macOS but P6 work
  hasn't landed cross-platform IPC yet; this is Linux-first).
- Interactive TUI mode (future, if justified).

## Definition of done

- `cmd/ventdctl/main.go` with subcommand dispatch.
- `internal/rpc/` package with Server, Router, handlers for each
  method.
- `cmd/ventd/main.go` wires rpc.Serve alongside the HTTP server under
  the same shutdown context.
- `deploy/ventd.service` updated with `RuntimeDirectory=ventd` if not
  already present.
- `docs/cli.md` new: ventdctl reference with every subcommand's
  --help text and example outputs.
- `CHANGELOG.md`: entry under `## Unreleased / ### Added`.
- All 8 tests pass; race detector clean.
- `CGO_ENABLED=0` builds for both ventd and ventdctl.
- go vet / golangci-lint / gofmt clean.
- Binary size: ventdctl should be small (<8 MB stripped). ventd binary
  gains from rpc package are R15-bounded.

## Branch and PR

- Branch: `claude/P8-CLI-01-ventdctl`
- PR title: `feat(cli): ventdctl command-line tool over unix socket (P8-CLI-01)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `cmd/ventdctl/main.go` (new binary)
  - `internal/rpc/server.go` (new)
  - `internal/rpc/router.go` (new)
  - `internal/rpc/handlers.go` (new)
  - `internal/rpc/rpc_test.go` (new)
  - `cmd/ventd/main.go` (wire rpc.Serve)
  - `cmd/ventdctl/main_test.go` (new, CLI-side tests)
  - `deploy/ventd.service`
  - `docs/cli.md` (new)
  - `CHANGELOG.md`
  - `.goreleaser.yml` (add ventdctl target)
- No new dependencies.
- `CGO_ENABLED=0` compatible.
- Preserve all safety guarantees.
- deploy/ventd.service touch: halves MAX_PARALLEL (per masterplan §6).
  Cowork should queue this task when the rest of Phase 8 isn't crowding
  the service file.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: SOCKET_SECURITY_VERIFICATION — paste the mode
  check code and its test output.
- Additional section: CLI_DEMO — paste `ventdctl list` and `ventdctl
  get fan0` sample outputs (can be synthetic).
- Additional section: BINARY_SIZE — stripped size of ventdctl binary.

## Final note

Parallelizable with P8-METRICS-01 and P8-HISTORY-01 in principle.
SHARED FILE: `cmd/ventd/main.go` adds a single-line wire-up; rebase
conflict is trivial. The deploy/ventd.service touch reduces MAX_PARALLEL;
Cowork should dispatch this task when other deploy/ edits aren't
in-flight.
