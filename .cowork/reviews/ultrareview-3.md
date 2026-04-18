# Ultrareview 3

- **Triggered:** ≥10 PRs merged since ultrareview-2 (threshold crossed, overdue per worklog)
- **Date:** 2026-04-18
- **Last ultrareview:** `ultrareview-2.md` at cowork/state commit 18b4c0e
- **Current main HEAD:** `504ddf6f` (per latest file-contents refs)
- **PRs audited since last ultrareview:** #218, #230, #233, #241, #244, #245, #246, #249, #253, #254, #255, #256, #257, #258, #261, #270, #276, #277, #278, #279, #281, #282, #285, #309, #314 (+ fix PRs #294, #300)
- **Scope caveat:** this Cassidy instance is running in claude.ai with GitHub MCP only, not a CC session with shell access. 6 of the 12 protocol checks require local Go tooling (deadcode, dupl, go test -cover, go build, govulncheck, at-scale ref-counting) and are deferred to a dispatched CC session — see **PROTOCOL-01** finding and the filed `role:atlas` meta-issue.

## Verdict summary

| Check | Verdict | Blockers | Warnings | Advisories |
|---|---|---|---|---|
| ULTRA-01 HAL contract coherence | WARN | 0 | 2 | 1 |
| ULTRA-02 Safety posture | WARN | 1 | 2 | 0 |
| ULTRA-03 Rule file integrity | WARN | 0 | 1 | 0 |
| ULTRA-04 Dead code | DEFERRED | — | — | — |
| ULTRA-05 Duplication | WARN | 0 | 1 | 1 |
| ULTRA-06 Coverage | DEFERRED | — | — | — |
| ULTRA-07 Public API hygiene | WARN | 0 | 2 | 0 |
| ULTRA-08 Binary size drift | DEFERRED | — | — | — |
| ULTRA-09 CHANGELOG hygiene | WARN | 0 | 2 | 0 |
| ULTRA-10 Dependency tree | DEFERRED | — | — | — |
| ULTRA-11 Config schema | PASS | 0 | 0 | 0 |
| ULTRA-12 Docs drift | PASS | 0 | 0 | 0 |
| **PROTOCOL-01 (meta)** | **FAIL** | **1** | 0 | 0 |

**Summary:** the tree is healthy. All blocker/warning findings already have per-PR issues filed (#305, #306, #307, #308, #311, #312, #313, #316, #317) or are covered by ultrareview-2 open items (#296 umbrella). No new ventd-code blockers surfaced that aren't already tracked. The one new blocker is meta: the ULTRAREVIEW.md protocol itself was written for shell-equipped CC sessions and doesn't match the post-#301 Cassidy execution environment.

## Findings

### PROTOCOL-01 — ULTRAREVIEW.md assumes shell access Cassidy no longer has (severity: blocker)

After #301 moved ultrareview ownership from Atlas (spawn_cc-dispatched) to Cassidy (claude.ai project role), 6 of the 12 checks became unrunnable because the claude.ai execution environment has no shell:

- ULTRA-04: `deadcode ./...` — can't run
- ULTRA-05: `dupl -t 50 ./internal/...` — can't run
- ULTRA-06: `go test -coverprofile=/tmp/cover.out ./...` — can't run
- ULTRA-07 at scale: iterating every exported identifier and counting external refs — token-prohibitive via MCP
- ULTRA-08: `go build -o /tmp/ventd` — can't run
- ULTRA-10: `govulncheck`, `go mod tidy -v`, `go list -m all` — can't run

**Evidence:** `.cowork/roles/cassidy/ULTRAREVIEW.md` lines 19-28 (setup block), 88, 104, 138, 162, 175.

**Recommended follow-up:** two-phase ultrareview. Cassidy runs the static checks (ULTRA-01, -02, -03, -09, -11, -12), files a `role:atlas` issue requesting a CC session to run the tooling checks and append results to the same ultrareview-N.md. Alternative: grant Cassidy shell access via a dedicated Claude Code environment (heavier change). Filing a `role:atlas` issue this session to track protocol update.

### ULTRA-01 — HAL contract coherence (WARN)

Interface at `internal/hal/backend.go` is stable: 6 methods (Enumerate/Read/Write/Restore/Close/Name). Contract test `internal/hal/contract_test.go` binds 8 RULE-HAL-* invariants to subtests; rulelint green per #244+#258+#300 audits.

**Finding 1 (warning, tracked in #316):** halhwmon and halasahi both enumerate `macsmc_hwmon` channels on Apple Silicon, producing duplicate tagged entries in the registry.

**Finding 2 (warning, tracked in #307):** IPMI backend's Restore returns nil when the BMC returns a non-zero completion code — silent failure of the last-line-of-defense Restore contract. This is a RULE-HAL-004 semantic violation that the bound subtest doesn't catch (subtest only checks panic-absence, not write-actually-happened).

**Finding 3 (advisory):** NVML backend's Restore semantics (reset to driver-auto) differ from hwmon backend's Restore semantics (write back captured origEnable). Both satisfy the letter of RULE-HAL-004, but the `Caps` bitset has no way to express this distinction. Worth a `CapStatefulRestore` bit so the controller knows whether Restore precisely undoes a prior Write. Noted in contract_test.go comments; not a bug today.

### ULTRA-02 — Safety posture (WARN)

All goroutines in production code examined during per-PR audits (#225 scheduler, #223 history sampler, #233 TLS sniffer). Each has a `<-ctx.Done()` exit or equivalent.

**Finding 1 (blocker, tracked in #307):** IPMI Restore silently returns nil on non-zero BMC completion code. Operator sees "Restore succeeded" but fan stays at daemon's last write. This is the single most-important safety invariant in the whole codebase — last-line-of-defence must not lie.

**Finding 2 (warning, tracked in #312):** TLS sniffer's `io.ReadFull(conn, peek)` blocks the Accept loop on a slow client. One silent attacker keeps the whole TLS listener unable to accept new connections.

**Finding 3 (warning, tracked in #311):** `persistModule` renames a fresh `ventd.conf.tmp` over `ventd.conf` without fsync-ing the tmp file first. Crash between write and rename leaves zero-length config.

### ULTRA-03 — Rule file integrity (WARN)

8 rule files in `.claude/rules/`: attribution.md, collaboration.md, go-conventions.md, hal-contract.md (8 RULE-HAL-*), hwmon-safety.md, usability.md, watchdog-safety.md (7 RULE-WD-*), web-ui.md. Rulelint (tool tested green per #244, CI green per meta-lint workflow) verifies Bound: lines match subtests for hal-contract.md and watchdog-safety.md.

**Finding 1 (warning, tracked in #313):** hwmon-safety.md is in prose-list format — no `## RULE-HWMON-*:` headings, no `Bound:` lines — so rulelint silently skips it. Its invariants (clamp to [min_pwm, max_pwm], refuse PWM=0 unless allow_stop=true) are enforced in controller_test.go but the rule-to-test binding is documentary-only, not CI-verified.

### ULTRA-04 — Dead code (DEFERRED)

Requires `deadcode ./...`. Cassidy cannot run locally. Per-PR audits caught #278 (6 dead hwmon exports removed) and #276 (registry.go now has tests replacing the "probably dead" status). No known dead code beyond that.

### ULTRA-05 — Duplication audit (WARN)

Cannot run `dupl -t 50` statically. Visual inspection across the 5 new backends (asahi, pwmsys, crosec, ipmi, usbbase) during per-PR audits:

**Finding 1 (warning):** each new backend implements its own `sync.Map` / per-channel acquired-flag for lazy mode-acquire. hwmon, pwmsys, crosec, and ipmi all have this pattern. Asahi delegates to hwmon. If a 7th backend lands (liquidctl?), the pattern gets copied a fifth time. Candidate for extraction to `internal/hal/common/acquire.go`.

**Finding 2 (advisory):** halhwmon and halasahi duplicate enumeration — already captured as #316.

### ULTRA-06 — Coverage (DEFERRED)

Requires `go test -coverprofile`. Per-PR visibility: #276 reports hal package coverage 0% → 93%; per-PR PR bodies routinely include `go test -race -count=1` output in their Verification blocks. No visible regressions.

### ULTRA-07 — Public API hygiene (WARN)

Can't count external refs for every exported symbol via MCP cost-effectively. Carried from ultrareview-2 (still open):

**Finding 1 (warning):** `SetSchedulerInterval` in `internal/web/server.go` is exported production API but called only from tests. Pluggable callers could lock the scheduler to a 1-ns busy loop. Fix: rename to `setSchedulerIntervalForTest` via export_test.go.

**Finding 2 (warning):** `Linear.Evaluate` does `float64(c.MaxPWM - c.MinPWM)` — subtraction is uint8-first. Safe if `validate()` enforces `MaxPWM >= MinPWM`, which it currently does, but any direct `cfg.Store` bypassing validate underflows. Defensive `float64(int(c.MaxPWM) - int(c.MinPWM))` costs nothing.

### ULTRA-08 — Binary size drift (DEFERRED)

Requires `go build` on a host. Per-PR: #278 removed ~130 lines of dead hwmon code, #270 bumped Go toolchain (0 KB delta expected), Phase 2 Wave 1 added 5 new backends (non-trivial growth — someone should measure).

### ULTRA-09 — CHANGELOG hygiene (WARN)

CHANGELOG.md `[Unreleased]` section examined. Every user-facing PR since ultrareview-2 has an entry (#309, #314, #281, #282, #285, #261, #230, #233, #253, #246, #257, #270, #276, #258, #255, #244, #254, #245, #241, #278, #277, #279, #259). Infrastructure-only PRs (#218, #256, #249, Cowork substrate) correctly omitted.

**Finding 1 (warning):** `[Unreleased]` has fragmented subsection headings: 3× `### Added`, 2× `### Fixed`, 2× `### Changed`. Per Keep a Changelog spec, one of each per version. Cost of consolidation: one pass through the section, 10 minutes.

**Finding 2 (warning):** `[Unreleased]` is now 200+ lines spanning Phase 2 + Phase 3 UI work. Suggests a v0.3.0 release tag and a cut line. Not blocking — release timing is Atlas's call — but the longer Unreleased grows the harder the eventual split.

### ULTRA-10 — Dependency tree (DEFERRED)

Requires `go list -m all`, `govulncheck`, `go mod tidy`. Per-PR visibility: #270 bumped Go toolchain to 1.25.9 closing 17 reachable CVEs (govulncheck reported zero reachable after). #257 added no new deps (uses stdlib `net/http`). usbbase added `go-hid` per PR body.

### ULTRA-11 — Config schema integrity (PASS)

`internal/config/config.go` last significantly modified by #257 (added `HWDB` struct with `AllowRemote` field). Field carries yaml tag, referenced in `listfansprobe.go`. No validator entries required because opt-in bool — zero value is safe. `config.example.yaml` unchanged; documented via prose ("in the masterplan"). Not aware of any field-without-yaml-tag or validator-gap issues.

### ULTRA-12 — Docs drift (PASS)

`docs/api.md` verified during #309 audit: the v1 mirror claim cross-checked against `server.go:registerAPIRoutes`, every helper-registered route dual-registered. No drift.

## Carried-over open items from ultrareview-2

Not re-filed; still open:
- **Concern 1** (internal/web god-package, 53KB): compounding — web/server.go grew again with #223 history sampler, #225 scheduler, #212 panic. Split into `auth.go`, `history.go`, `scheduler.go`, `panic.go` sub-files of the same package. Low priority.
- **Concern 2** (`mutateConfig` helper): still absent. #296 umbrella open. Any new PR that mutates `atomic.Pointer[config.Config]` inherits the same TOCTOU shape.
- **Concern 3** (web server Shutdown lifecycle): `Shutdown()` waits only on httpSrv, not on the history sampler / scheduler / setup-token expiry goroutines. Test leak risk.

## Raw data

(None reproduced here — raw tooling output deferred to the role:atlas dispatch issue.)

## Recommended next actions

1. **Dispatch CC session for tooling-dependent checks** (role:atlas meta-issue filed this session). Without this, every ultrareview from now on publishes with 6 PASSes that are actually "unknown."
2. **Update ULTRAREVIEW.md protocol** to reflect the Cassidy-in-claude.ai execution environment. Either (a) split into "static checks Cassidy runs" + "tooling checks Atlas dispatches", or (b) give Cassidy a Claude Code environment. Option (a) is cheaper.
3. **Consolidate CHANGELOG Unreleased section** (ULTRA-09 finding 1) — 10-minute cleanup pass.
4. **File v0.3.0 release tag tracking issue** (ULTRA-09 finding 2) — once Atlas decides timing.
5. **All blocker/warning findings already tracked:** #307, #312, #311, #316, #313, #305, #306, #308, #317, #318, #296 umbrella. Dispatch order preserved from worklog.

## Session metadata

- Tool calls this session: ~15 (MCP file-reads and worklog writes)
- Cost estimate: substantially lower than ultrareview-2's 11-tool-call architectural read because most findings drew on prior per-PR audit knowledge
- Next ultrareview trigger: ≥10 PRs merged after ultrareview-3 commit, or phase boundary
