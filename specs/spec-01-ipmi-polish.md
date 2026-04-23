# Spec 01 — IPMI polish for v0.3.x ship

**Masterplan IDs this covers:** T-IPMI-01, T-IPMI-02 (test coverage), plus release-blocker polish on P2-IPMI-01 / P2-IPMI-02.
**Target release:** v0.3.x (shipping now per Atlas 2026-04-19 — PR #285 merged).
**Estimated session cost:** Sonnet, ~3–4 remaining sessions, $5–15 each. No Opus required. (PR 1 already shipped.)
**Dependencies already green:** P1-HAL-01, P2-IPMI-01, P2-IPMI-02.
**Progress:** PR 1 merged 2026-04-23 (357b233).

---

## Why this ships first

IPMI is the v0.4.0 release hook per `RELEASE-PLAN.md` — the Reddit post is pre-written in your market strategy ("/r/homelab, single-binary IPMI fan control"). The code is in `main`; what's missing is the belt-and-braces polish that keeps a first-time /r/homelab user from filing an issue in the first hour. Every issue triaged is CC tokens spent. Front-load the quality now.

## Scope — what this session produces

A PR series that closes the gap between "merged" and "shippable" for IPMI. Four small PRs, not one big one. Each independently mergeable.

**Note (2026-04-23):** P2-IPMI-02 shipped the Go socket dial path but NOT
the privileged sidecar that serves it. Main daemon has zero IPMI privileges
(correct) and no sidecar to forward through (gap). PR 2 authors the sidecar;
PR 3 tests the privilege boundary.

### PR 1 — Test coverage [MERGED 2026-04-23]

Added `fakeipmi` fixture scaffolding and the 7 safety invariant subtests bound to `.claude/rules/ipmi-safety.md`. All rules landed at commit 357b233: RULE-IPMI-1 (Supermicro X11 happy path), RULE-IPMI-2 (Dell R750 happy path), RULE-IPMI-3 (HPE iLO license required), RULE-IPMI-4 (unknown vendor refuses write), RULE-IPMI-5 (BMC busy retry), RULE-IPMI-6 (ioctl timeout no goroutine leak), RULE-IPMI-7 (restore on exit all channels). Rule-lint green; no orphan subtests.

### PR 2 — Author the ventd-ipmi privileged sidecar

Rationale: P2-IPMI-02 shipped `deploy/ventd-ipmi.service` and an AF_UNIX socket path in `deploy/`, but no binary actually binds the socket. Main `ventd.service` still opens `/dev/ipmi0` directly, so the zero-capability claim is aspirational until this PR lands.

**Files:**
- `cmd/ventd-ipmi/main.go` (new)
- `internal/hal/ipmi/proto/` (new) — length-prefixed JSON wire protocol shared between main daemon and sidecar.
- `internal/hal/ipmi/client.go` (new) — main-daemon-side client that dials the sidecar socket.
- `internal/hal/ipmi/backend.go` (modify) — backend switches from direct ioctl path to `proto.Client` when sidecar socket is present.
- `deploy/ventd-ipmi.service` (verify + amend if needed):
    ```
    User=ventd-ipmi
    CapabilityBoundingSet=CAP_SYS_RAWIO
    AmbientCapabilities=CAP_SYS_RAWIO
    DeviceAllow=/dev/ipmi0 rw
    NoNewPrivileges=yes
    ProtectSystem=strict
    RestrictAddressFamilies=AF_UNIX
    Type=notify
    ```
- `deploy/ventd-ipmi.socket` (new or verify) — `ListenStream` on the well-known path.
- `deploy/ventd.service` (modify) — remove any residual `/dev/ipmi0` `DeviceAllow=` or `CAP_SYS_RAWIO` grant; add `Requires=ventd-ipmi.socket`.

**Design constraints:**
- Sidecar is always-on, NOT socket-activated on demand. Rationale: the main daemon polls BMC sensors every control tick; socket activation would churn processes. systemd `.socket` unit owns the listening fd; sidecar inherits it.
- Wire protocol: length-prefixed JSON. One request/response per frame. No streaming, no multiplexing. Keep it dumb.
- `proto` package exports: `Request` (enum: `Enumerate`, `ReadSensor`, `SetMode`, `WriteDuty`, `Restore`), `Response`, `Codec`.
- Sidecar binary stays tiny — no third-party deps beyond what's already in `go.mod`. Pure stdlib `net` + `encoding/json` + `syscall`.

**Definition of done:**
- [ ] `cmd/ventd-ipmi` builds with `CGO_ENABLED=0`.
- [ ] `internal/hal/ipmi/proto` has round-trip tests for every `Request` type.
- [ ] `internal/hal/ipmi/client_test.go` uses a fake sidecar to verify the backend speaks proto correctly (pure in-process, no real socket).
- [ ] `deploy/ventd-ipmi.service` parses cleanly under `systemd-analyze verify`.
- [ ] PR does NOT yet remove the direct-ioctl fallback in `backend.go` — leave a build tag or runtime flag so PR 3 can flip the switch.

### PR 3 — Verify the privilege boundary

**Files:**
- `internal/hal/ipmi/socket_test.go` (new)
- `deploy/ventd.service` (final strip)
- `internal/hal/ipmi/backend.go` (remove direct-ioctl fallback)

**Tests:**
1. Main unit `ventd.service`: parse the unit file, assert `DeviceAllow=` does not include `/dev/ipmi0` AND `CapabilityBoundingSet=` excludes `CAP_SYS_RAWIO`.
2. Sidecar `ventd-ipmi.service`: assert `DeviceAllow=/dev/ipmi0 rw` appears exactly once, `CapabilityBoundingSet=CAP_SYS_RAWIO`, `NoNewPrivileges=yes`, `ProtectSystem=strict`.
3. Integration under `systemd-run` (containerised): main daemon attempts direct ioctl on `/dev/ipmi0` → EPERM. Forwarded via sidecar → succeeds. Gate behind `//go:build ipmi_integration` if `systemd-run` unavailable in the test container; document the gap in `TESTING.md`.

**Definition of done:**
- [ ] Direct-ioctl fallback removed from `backend.go`.
- [ ] Integration test passes OR is explicitly gated with docs.
- [ ] `go test -race ./internal/hal/ipmi/...` green.

### PR 4 — Vendor gating + CHANGELOG

**Files:**
- `internal/hal/ipmi/backend.go` (minor)
- `internal/hwdb/profiles.yaml` (add IPMI-relevant entries)
- `CHANGELOG.md`

**Behaviour:**
1. DMI gate: on systems where chassis_type ≠ 23 AND vendor is not in `{Supermicro, Dell Inc., Hewlett Packard Enterprise, Lenovo}`, `Enumerate()` returns zero channels and logs a single debug-level event. Do not probe `/dev/ipmi0` — that device may not even exist, and probing can generate spurious kernel messages.
2. Add 6 IPMI-relevant fingerprint entries to `internal/hwdb/profiles.yaml`: 2× Supermicro (X11, X13), 2× Dell PowerEdge (R740, R750), 1× HPE ProLiant DL380 Gen10 (for the "iLO Advanced required" error path test), 1× Lenovo ThinkSystem.
3. `CHANGELOG.md` under v0.3.0: one bullet per vendor, explicit about what works and what doesn't. Honest: "HPE iLO detected — informational only; writes require iLO Advanced license" not "HPE supported."

## Definition of done

- [ ] `go test -race ./internal/hal/ipmi/...` passes locally and in CI.
- [ ] `go test -run TestIPMISafety_Invariants ./internal/hal/ipmi/...` passes; every subtest maps 1:1 to a `RULE-IPMI-<N>` in `.claude/rules/ipmi-safety.md`. ✓ PR 1 done.
- [ ] `.github/workflows/rule-lint.yml` (or whatever T-META-01 lints are called) still green — no orphan rules, no orphan subtests. ✓ PR 1 done.
- [ ] PR 2: `cmd/ventd-ipmi` builds with `CGO_ENABLED=0`; proto round-trip tests pass; `deploy/ventd-ipmi.service` parses under `systemd-analyze verify`.
- [ ] PR 3: direct-ioctl fallback removed from `backend.go`; privilege-boundary tests pass or gated with docs.
- [ ] PR 4: DMI gate landed; `CHANGELOG.md` updated with honest vendor-coverage statement.
- [ ] No HARDWARE-REQUIRED work — everything in this spec is pure-Go / fixture / cross-compile, no Proxmox VM or Desktop HIL needed.

## Explicit non-goals (do not let CC scope-creep into these)

- No new vendors beyond Supermicro + Dell + HPE-error-path + Lenovo-detection. LIQUID backend is a separate spec.
- No IPMI LAN or IPMI-over-KCS — in-band `/dev/ipmi0` only.
- No fleet management. That's P8.
- No UI changes.

## CC session prompt — copy/paste this

PRs 2, 3, and 4 are the remaining work. Run in order; each session gated on the prior PR being merged.

See separate CC session prompts:
- `cc-prompt-spec01-pr2.md`  (sidecar author — PR 2)
- `cc-prompt-spec01-pr3.md`  (privilege boundary — PR 3)
- `cc-prompt-spec01-pr4.md`  (DMI gate + profiles + CHANGELOG — PR 4)

Total estimated cost: $20–40 across the three remaining sessions.

## Why this is cheap

- Pure Go, no hardware, no network.
- Fixtures already specified in `ventdtestmasterplan.md §3`.
- Pattern already proven with `hwmon-safety.md` invariant binding — CC just mirrors it.
- Rule-binding lint (T-META-01 if it exists) catches drift automatically — no manual review needed for the mapping itself.
