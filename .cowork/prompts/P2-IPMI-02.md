You are Claude Code, working on the ventd repository.

## Task
ID: P2-IPMI-02
Track: IPMI
Goal: Socket-activated IPMI sidecar. The Wave 1 IPMI backend (P2-IPMI-01) opens `/dev/ipmi0` directly from the main daemon, which requires ventd's process to have the CAP_SYS_RAWIO capability. This task extracts the IPMI I/O into a tiny privileged sidecar, communicates with it over a Unix socket, and drops the capability requirement from the main daemon. This is the security discipline that makes ventd audit-defensible vs. CoolerControl (which runs root).

## Care level
HIGH. Capability scoping. Failure modes include: sidecar crashes silently while daemon continues to think IPMI is working; privilege escalation if the socket is world-writable; use-after-free if daemon and sidecar disagree on an IPMI channel's state.

## Context you should read first

- `internal/hal/ipmi/backend.go` — Wave 1 backend. This task refactors the I/O path, not the protocol.
- `deploy/ventd.service` — systemd unit for main daemon; the sidecar adds a paired unit.
- `internal/hal/usbbase/` — pattern reference for "minimal Go HID primitives"; sidecar is a similarly small layer.

## What to do

1. Create `cmd/ventd-ipmi-sidecar/main.go` — a tiny binary that:
   - Opens `/dev/ipmi0` with CAP_SYS_RAWIO.
   - Listens on `/run/ventd/ipmi.sock` (mode 0660, group `ventd`).
   - Accepts IPMI request envelopes (NetFn, Cmd, Data, channel) as JSON lines.
   - Returns responses as JSON lines.
   - Drops privileges after opening the socket (capabilities pared to CAP_SYS_RAWIO only; UID/GID change to `ventd:ventd`).

2. Create `deploy/ventd-ipmi-sidecar.service` — systemd unit with:
   - `Type=simple`.
   - `User=ventd Group=ventd`.
   - `AmbientCapabilities=CAP_SYS_RAWIO`.
   - `NoNewPrivileges=true`.
   - `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `PrivateDevices=false` (needs /dev/ipmi0), `ReadWritePaths=/run/ventd`.
   - `BindsTo=ventd.service` so the sidecar stops when main does.

3. Create `deploy/ventd-ipmi.socket` for socket activation:
   - `ListenStream=/run/ventd/ipmi.sock`.
   - `SocketMode=0660`, `SocketGroup=ventd`.

4. Refactor `internal/hal/ipmi/backend.go`:
   - Replace direct ioctl calls with `sidecarClient` that sends JSON envelopes over the socket.
   - Dial `/run/ventd/ipmi.sock` at construction; if the socket is absent, the backend gracefully disables itself (silent no-op on systems without the sidecar installed).
   - Protocol version byte in the first request so future sidecar changes don't break older daemons silently.

5. Update `cmd/ventd/main.go` registration to reflect that the IPMI backend no longer requires CAP_SYS_RAWIO; document this in a comment.

6. Unit tests `cmd/ventd-ipmi-sidecar/main_test.go`:
   - `TestSidecar_HappyPath` — start sidecar with a fake ipmi device path, send a valid request, get expected response.
   - `TestSidecar_MalformedRequest_Rejected` — junk JSON → structured error response, sidecar stays alive.
   - `TestSidecar_UnauthorisedUID_Rejected` — if socket is connected from a non-ventd UID, request is dropped.

7. Integration test `internal/hal/ipmi/backend_sidecar_test.go`:
   - Stands up the sidecar binary as a subprocess with a fake ipmi path.
   - Exercises Enumerate / Read / Write / Restore against the real socket.

8. Update `deploy/*` README/docs to mention the new sidecar service; note it must be enabled separately (`systemctl enable ventd-ipmi-sidecar.socket`).

## Definition of done

- Sidecar binary exists at `cmd/ventd-ipmi-sidecar/`.
- systemd units (.service + .socket) in `deploy/`.
- IPMI backend no longer opens /dev/ipmi0 directly; only dials the socket.
- Capability drop on sidecar verified via test.
- Main daemon can run with CAP_SYS_RAWIO removed from its unit (update deploy/ventd.service accordingly).
- Integration test exercises the full socket path.
- CHANGELOG entry.

## Out of scope

- Moving other backends behind sidecars (e.g., NVML, hwmon). IPMI is uniquely privileged; the others don't benefit from this treatment.
- LAN IPMI (lanplus) — still device-only.
- HIL tests.

## Branch and PR

- Branch: `claude/P2-IPMI-02-sidecar`
- Title: `feat(ipmi): socket-activated sidecar; drop CAP_SYS_RAWIO from main daemon (P2-IPMI-02)`

## Constraints

- Depends on P2-IPMI-01 being merged.
- Files: `cmd/ventd-ipmi-sidecar/**`, `internal/hal/ipmi/**`, `deploy/*.service`, `deploy/*.socket`, `cmd/ventd/main.go`, `CHANGELOG.md`.
- No new dependencies beyond `golang.org/x/sys`.
- CGO_ENABLED=0 for both binaries.
- All safety guarantees from P2-IPMI-01 preserved (vendor gating, DMI check).

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional field: CAPABILITY_DELTA — show before/after capability sets for main daemon.
