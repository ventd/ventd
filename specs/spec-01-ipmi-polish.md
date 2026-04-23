# Spec 01 — IPMI polish for v0.3.x ship

**Masterplan IDs this covers:** T-IPMI-01, T-IPMI-02 (test coverage), plus release-blocker polish on P2-IPMI-01 / P2-IPMI-02.
**Target release:** v0.3.x (shipping now per Atlas 2026-04-19 — PR #285 merged).
**Estimated session cost:** Sonnet, ~3–5 focused sessions, $5–15 each. No Opus required.
**Dependencies already green:** P1-HAL-01, P2-IPMI-01, P2-IPMI-02.

---

## Why this ships first

IPMI is the v0.4.0 release hook per `RELEASE-PLAN.md` — the Reddit post is pre-written in your market strategy ("/r/homelab, single-binary IPMI fan control"). The code is in `main`; what's missing is the belt-and-braces polish that keeps a first-time /r/homelab user from filing an issue in the first hour. Every issue triaged is CC tokens spent. Front-load the quality now.

## Scope — what this session produces

A PR series that closes the gap between "merged" and "shippable" for IPMI. Four small PRs, not one big one. Each independently mergeable.

**Note (2026-04-23):** P2-IPMI-02 shipped the Go socket dial path but NOT
the privileged sidecar that serves it. Main daemon has zero IPMI privileges
(correct) and no sidecar to forward through (gap). PR 2 is therefore split:
PR 2a authors the sidecar; PR 2b tests the privilege boundary.

### PR 1 — Test coverage against the `fakeipmi` fixture (T-IPMI-01)

**Files:**
- `internal/hal/ipmi/backend_test.go` (extend)
- `internal/hal/ipmi/safety_test.go` (new) — bound to `.claude/rules/ipmi-safety.md`
- `internal/testfixture/fakeipmi/**` (new if missing; extend if present)
- `.claude/rules/ipmi-safety.md` (new)

**Coverage required:**
1. Happy path — Supermicro X11 vendor path: enumerate → read sensors → set manual mode → write fan duty → restore on exit.
2. Happy path — Dell PowerEdge R750 vendor path: same sequence with Dell-specific command bytes (0x30/0x30).
3. HPE error path — vendor detected, backend returns clear "iLO Advanced license required" error, does not attempt writes.
4. Unknown vendor error path — refuses to enter manual mode, logs structured event, returns empty channel list.
5. BMC-busy retry — first ioctl returns `IPMI_CC_NODE_BUSY`, retry with backoff succeeds on second attempt.
6. ioctl timeout — BMC unresponsive for >2s → surface as structured error, no goroutine leak (verify with `go.uber.org/goleak`).
7. Fallback on daemon exit — watchdog calls `backend.Restore()`, verify every channel gets the pre-ventd `pwm_enable` restored (or manual-mode-off equivalent for IPMI).

**Invariant file contents (`.claude/rules/ipmi-safety.md`):**
Follow the exact format established in `.claude/rules/hwmon-safety.md`. Write 7 `RULE-IPMI-<N>:` entries corresponding to the 7 tests above. Each rule's `Bound:` line points to a specific subtest name.

### PR 2 — Socket-activated sidecar verification (T-IPMI-02)

**Files:**
- `internal/hal/ipmi/socket_test.go` (new)
- `deploy/ventd-ipmi.service` (verify exists and correct)
- `deploy/ventd.service` (verify main unit has zero IPMI device grant)

**Tests:**
1. Main unit `ventd.service`: parse the unit file, assert `DeviceAllow=` does not include `/dev/ipmi0`.
2. Sidecar `ventd-ipmi.service`: assert `DeviceAllow=/dev/ipmi0 rw` appears exactly once, `CapabilityBoundingSet=` is empty, `NoNewPrivileges=yes`, `ProtectSystem=strict`.
3. Integration under systemd-run (containerised test): main daemon attempts ioctl on `/dev/ipmi0` directly → EPERM. Forwarded via sidecar → succeeds.

### PR 2a — Author the ventd-ipmi sidecar (spec-gap fix)

Ship the missing privileged process. Always-on (not socket-activated —
main daemon polls constantly, spawn latency adds no value). Privilege-
separated: sidecar holds `CAP_SYS_RAWIO` + `DeviceAllow=/dev/ipmi0 rw`,
main daemon holds neither.

**Files (new):** `cmd/ventd-ipmi/main.go`, `internal/hal/ipmi/proto/`,
`deploy/ventd-ipmi.service`, `deploy/tmpfiles.d-ventd.conf`,
`deploy/sysusers.d-ventd.conf`.

**Files (modified):** `internal/hal/ipmi/backend.go` (default socket path),
`deploy/README.md` (install steps), Makefile/goreleaser (build artifact).

**Wire protocol:** length-prefixed JSON, ops ENUMERATE / READ_SENSORS /
SET_MANUAL_MODE / WRITE_DUTY / RESTORE. Per-request timeout 2s.
Frame cap 64KB.

**See:** `cc-prompt-spec01-pr2a.md` for full CC session prompt.

### PR 2b — Sidecar privilege-boundary verification (T-IPMI-02)

Original PR 2 test plan, now that the sidecar exists.

**Files:**
- `internal/hal/ipmi/socket_test.go` (new)
- `TESTING.md` (one line if integration test build-tagged)

**Tests:**
1. Main unit `ventd.service`: no `DeviceAllow=/dev/ipmi*`, empty
   `CapabilityBoundingSet=`, no `CAP_SYS_RAWIO` in `AmbientCapabilities=`.
2. Sidecar `ventd-ipmi.service`: exactly-one `DeviceAllow=/dev/ipmi0 rw`,
   `CapabilityBoundingSet=CAP_SYS_RAWIO` (only that one cap),
   `NoNewPrivileges=yes`, `ProtectSystem=strict`, `User=ventd-ipmi`,
   `RestrictAddressFamilies=AF_UNIX`, `Type=notify`.
3. Integration under systemd-run (containerised test): sidecar responds
   to proto ENUMERATE roundtrip; process without `CAP_SYS_RAWIO` gets
   EPERM on direct `/dev/ipmi0` ioctl.

**If systemd-run unavailable in CI**, gate (3) behind `//go:build ipmi_integration`
and document the missing coverage in `TESTING.md` under the hardware-gated matrix.

### PR 3 — Vendor gating sanity + CHANGELOG

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
- [ ] `go test -run TestIPMISafety_Invariants ./internal/hal/ipmi/...` passes; every subtest maps 1:1 to a `RULE-IPMI-<N>` in `.claude/rules/ipmi-safety.md`.
- [ ] `.github/workflows/rule-lint.yml` (or whatever T-META-01 lints are called) still green — no orphan rules, no orphan subtests.
- [ ] `CHANGELOG.md` updated with honest vendor-coverage statement.
- [ ] No HARDWARE-REQUIRED work — everything in this spec is pure-Go / fixture / cross-compile, no Proxmox VM or Desktop HIL needed.

## Explicit non-goals (do not let CC scope-creep into these)

- No new vendors beyond Supermicro + Dell + HPE-error-path + Lenovo-detection. LIQUID backend is a separate spec.
- No IPMI LAN or IPMI-over-KCS — in-band `/dev/ipmi0` only.
- No fleet management. That's P8.
- No UI changes.

See separate CC session prompts:
- cc-prompt-spec01-pr1.md  (T-IPMI-01 coverage + invariants)
- cc-prompt-spec01-pr2a.md (sidecar author)
- cc-prompt-spec01-pr2b.md (sidecar verification)
- cc-prompt-spec01-pr3.md  (DMI gate + profiles + CHANGELOG)

Run in order. Each session gated on prior PR being merged.
Total estimated cost: $25–48 across all four sessions.

## Why this is cheap

- Pure Go, no hardware, no network.
- Fixtures already specified in `ventdtestmasterplan.md §3`.
- Pattern already proven with `hwmon-safety.md` invariant binding — CC just mirrors it.
- Rule-binding lint (T-META-01 if it exists) catches drift automatically — no manual review needed for the mapping itself.
