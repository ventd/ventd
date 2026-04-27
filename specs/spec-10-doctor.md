# spec-10 — `ventd doctor` preflight diagnostic command

**Status:** draft, target v0.6.0 (slipped from v0.5.0; v0.5.0 shipped 2026-04-26 without doctor).
**Predecessor:** spec-06 (install-contract), spec-03 PR 2a/2b (catalog + calibration).
**Successor consumer:** spec-11 (first-run web UI wizard) calls `ventd doctor --json` as its first step.
**Memory anchor:** desktop pre-flight gap surfaced in chat 2026-04-26 (Phoenix's 13900K + RTX 4090 + Phanteks 14-fan + Arctic LFII 420 dual-boot install pending). spec-10 is the install-time + first-run answer to "will I see errors clearly enough to debug this on my own hardware".

---

## Why this ships v0.5.0

ventd is currently install-and-pray. Package install + service start either work or fail silently into the journal. There is no preflight that says "this hardware is supported, BIOS is configured correctly, no conflicting daemons are running, your kernel has the right modules loaded, you are clear to start the service".

Three concrete failure modes from the homelab/NAS audience that doctor closes:

1. **BIOS Q-Fan/Smart Fan reasserts curves on Gigabyte/MSI Z690/Z790 boards.** Currently detected post-hoc by calibration probe writing `bios_overridden: true`. User has no idea this will happen until after first calibration runs and possibly after fans hit unsafe RPM during the probe. Doctor warns up-front by reading BIOS strings + DMI vendor and matching against a known-bad list.

2. **Conflicting userspace daemons (lm_sensors `fancontrol`, `thinkfan`, CoolerControl with EC drivers, `nbfc_service`).** spec-03 amendment §3.5 introduced `conflicts_with_userspace`. ventd refuses to start if a conflict is detected, but the error is "service failed, see journal" rather than "fancontrol.service is running, stop it first or use --force-start". Doctor fronts this as a separate command users can run before `systemctl start ventd`.

3. **Hardware not catalogued → tier-3 fallback silently downgrades feature set.** Currently a journal warning. Users on uncatalogued boards (likely on first-light hardware like Phoenix's specific Z790 SKU) don't know they're getting a degraded experience. Doctor explicitly calls out tier-3 fallback as a yellow-flag with "your board is not in the catalog; ventd will run in generic mode with reduced feature confidence; here is how to contribute a board profile".

Plus a fourth that doctor enables but does not solve:
4. **Permission/AppArmor sanity.** spec-06 PR 2 ships AppArmor enforce-mode. Doctor verifies the profile is loaded and in the expected state; if it's complain-mode or unloaded, doctor flags it.

Doctor is read-only. It does not modify state. It runs as user or root (different output detail). Exit code drives spec-11 wizard branching.

This belongs in v0.5.0 alongside spec-03 PR 3 because: (a) PR 3 catalog seeding lands ~25 board YAMLs, doctor's match prediction needs the seeded catalog to be useful; (b) v0.5.0 is the first release where ventd is "real" enough to install on enthusiast hardware, doctor is the install-experience polish for that audience; (c) spec-11 wizard depends on doctor's `--json` output, both specs ship together so the wizard is not stranded waiting for doctor.

---

## Scope — what this session produces

Single PR. Mostly read-only code paths reusing existing hwdb + calibration store + spec-03 amendment `conflicts_with_userspace` field. Cost target Sonnet $25-40.

### PR 1 — `ventd doctor` subcommand + invariants

**Files (new):**
- `cmd/ventd/doctor.go` — subcommand entry, flag parsing, output formatting (text + JSON modes).
- `internal/doctor/runner.go` — orchestrator. Runs each check in sequence, collects results, returns a `Report`.
- `internal/doctor/report.go` — `Report` struct + `Check` struct + severity enum (`OK | Warning | Blocker`) + JSON serialisation.
- `internal/doctor/checks/dmi.go` — read DMI, fingerprint, predict catalog tier match using `internal/hwdb` matcher in dry-run mode.
- `internal/doctor/checks/bios.go` — Q-Fan/Smart Fan detection. Read DMI vendor + BIOS strings, match against known-bad list (`internal/doctor/bios_known_bad.go` — 15-25 entries seeded from controllability map + community reports).
- `internal/doctor/checks/conflicts.go` — query systemd for known-conflicting units (`fancontrol.service`, `thinkfan.service`, `nbfc_service.service`, `coolercontrold.service`). Reuse spec-03 amendment `conflicts_with_userspace` resolution logic — do not fork.
- `internal/doctor/checks/kernel.go` — verify required modules loaded (`nct6775` family, `coretemp`, `k10temp`, `amdgpu`, `drivetemp` etc.) by walking `/sys/class/hwmon/*/name`. Cross-reference against the matched catalog's `requires_modules` field.
- `internal/doctor/checks/permissions.go` — verify ventd user/group, `/var/lib/ventd/` ownership + mode, AppArmor profile state via `aa-status` (graceful skip if AppArmor not installed).
- `internal/doctor/checks/calibration.go` — check whether `/var/lib/ventd/calibration/<fp>-<bios>.json` exists for current fingerprint+BIOS; if stale (BIOS changed), recommend recalibration.
- `internal/doctor/checks/gpu.go` — NVML present? amdgpu controllable? Check driver versions against minimum supported.
- `internal/doctor/runner_test.go` — synthetic-fixture subtests, one per check. Bind to RULE-DOCTOR-* invariants.
- `internal/doctor/bios_known_bad.go` — known-bad BIOS regex/string list with `(vendor, BIOS string regex, severity, message)` tuples. Maintained alongside catalog.
- `.claude/rules/doctor.md` — RULE-DOCTOR-01..10 invariants.
- `docs/doctor.md` — user-facing reference: how to read the output, severity meanings, exit codes, JSON schema.

**Files (modified):**
- `cmd/ventd/main.go` — register `doctor` subcommand.
- `internal/hwdb/matcher_v1.go` — expose `MatchDryRun(dmi DMI) MatchDiagnostics` if it isn't already exported by spec-03 PR 2a (likely already there, verify before duplicating).
- `Makefile` or release build — ensure doctor invocation works in distro packaging context (no shared-library regressions).
- `CHANGELOG.md` — v0.6.0 entry.

**Files (out of scope, not touched):**
- `internal/calibration/*` — frozen post-PR-2b. Doctor reads on-disk JSON via `*hwdb.CalibrationRun`, never imports the calibration package.
- `internal/diag/*` — PR 2c artefact, unrelated. Doctor and diag are separate commands.
- `web/*` — spec-11 consumes doctor output, no doctor-side web work.
- BIOS-write paths — doctor is read-only.

### Invariant bindings (`.claude/rules/doctor.md`)

1. `RULE-DOCTOR-01` — Doctor is read-only. No code path may write to `/sys`, `/dev`, or `/var/lib/ventd/` from `internal/doctor/`. Bound by static analysis subtest that greps the package for `os.OpenFile` with write flags. **Binds to:** `TestDoctor_ReadOnly`.

2. `RULE-DOCTOR-02` — Doctor exit codes are stable: `0 = all OK`, `1 = warnings only`, `2 = at least one blocker`, `3 = doctor itself errored (could not complete checks)`. **Binds to:** `TestDoctor_ExitCodes`.

3. `RULE-DOCTOR-03` — Every `Check` produces structured output regardless of success. No silent passes. JSON output always lists every check ran. **Binds to:** `TestDoctor_AllChecksReported`.

4. `RULE-DOCTOR-04` — Doctor runs as unprivileged user without crashing. Permission-gated checks (e.g. AppArmor enforce state, `/var/lib/ventd/` mode) gracefully degrade with severity `Warning` and a message stating "rerun as root for full check". **Binds to:** `TestDoctor_UnprivilegedRun`.

5. `RULE-DOCTOR-05` — DMI fingerprint match prediction MUST use the same `hwdb.Fingerprint(dmi)` and `hwdb.Match()` code paths that ventd itself uses at runtime. No parallel implementation. **Binds to:** `TestDoctor_FingerprintParity`.

6. `RULE-DOCTOR-06` — Conflict detection MUST reuse spec-03 amendment `conflicts_with_userspace` resolution. No fork. **Binds to:** `TestDoctor_ConflictsResolverShared`.

7. `RULE-DOCTOR-07` — `bios_known_bad.go` entries are validated at compile time: every entry has non-empty `vendor`, valid regex in `bios_string`, severity in {`Warning`, `Blocker`}, non-empty `message`. **Binds to:** `TestDoctor_KnownBadValid`.

8. `RULE-DOCTOR-08` — Doctor's JSON output schema is versioned. spec-11 wizard pins `schema_version: "1"`. Schema bump is breaking change requiring spec amendment. **Binds to:** `TestDoctor_JSONSchemaVersioned`.

9. `RULE-DOCTOR-09` — Doctor runs in <2 seconds wall-clock on the dev container with no hardware, executing checks serially. Every check has a 200ms timeout; checks that need longer must be opt-in via flag. Rationale: 10 checks × 200ms = 2.0s worst case fits the 2s budget without goroutine fan-out (which would double test surface for marginal speedup). All current checks are sub-50ms in practice (sysfs reads, systemctl is-active, DMI parse) — 200ms is 4× headroom. **Binds to:** `TestDoctor_LatencyBudget`.

10. `RULE-DOCTOR-10` — Doctor's experimental-flags check MUST read from `hwdiag.Store` via `ComponentExperimental`, not by parsing config or re-implementing flag resolution. **Binds to:** `TestDoctor_ExperimentalFlagsFromHwdiag`.

### Pre-existing hwdiag.Store consumers

`internal/hwdiag` already ships `ComponentExperimental` (spec-15 PR 1).
`ventd doctor` MUST reuse `hwdiag.Store.Snapshot(hwdiag.FilterComponent(hwdiag.ComponentExperimental))`
to read the live experimental-flags entries rather than re-implementing the lookup.

This is an integration point, not an ownership boundary: doctor reads the store,
spec-15 writes to it. The coupling is intentional and documented here so the impl
session does not accidentally introduce a parallel lookup path.

### Output design

**Text mode (default, human-readable):**
```
$ ventd doctor

Hardware
  ✓ DMI fingerprint:   abc123def456
  ✓ Catalog match:     tier-1 (board: ASUS ROG STRIX Z790-E)
  ⚠ BIOS Q-Fan:        DETECTED on this board family — disable in BIOS or
                       expect bios_overridden flags after calibration.
                       See: https://ventd.dev/q-fan

Kernel
  ✓ nct6775:           loaded (provides hwmon0)
  ✓ coretemp:          loaded (provides hwmon1)
  ✗ k10temp:           not applicable (Intel CPU)
  ✓ amdgpu:            n/a (no AMD GPU)
  ⚠ NVIDIA driver:     R510, ventd recommends R515+ for fan control.
                       See: https://ventd.dev/nvidia-driver

Userspace conflicts
  ✓ fancontrol:        not running
  ✓ thinkfan:          not running
  ✗ nbfc_service:      RUNNING — stop with `systemctl stop nbfc_service`
                       before starting ventd.

Permissions
  ✓ ventd user:        exists (uid=998, gid=998)
  ✓ /var/lib/ventd:    mode 0700, owner ventd:ventd
  ⚠ AppArmor:          rerun as root to verify enforce-mode

Calibration state
  ⚠ No calibration found for fingerprint abc123/BIOS 1.2.3.
                       Run `ventd calibrate` after starting the service.

Summary: 1 blocker, 4 warnings, 9 OK.
Exit code 2.
```

**JSON mode (`--json`, machine-readable, consumed by spec-11):**
```json
{
  "schema_version": "1",
  "ventd_version": "0.5.0",
  "timestamp": "2026-05-15T10:23:00Z",
  "summary": {
    "blockers": 1,
    "warnings": 4,
    "ok": 9,
    "exit_code": 2
  },
  "checks": [
    {
      "id": "dmi-fingerprint",
      "category": "hardware",
      "severity": "ok",
      "title": "DMI fingerprint",
      "value": "abc123def456",
      "message": null,
      "remediation_url": null
    },
    {
      "id": "bios-qfan",
      "category": "hardware",
      "severity": "warning",
      "title": "BIOS Q-Fan",
      "value": "detected",
      "message": "Q-Fan detected on this board family — disable in BIOS or expect bios_overridden flags after calibration.",
      "remediation_url": "https://ventd.dev/q-fan"
    },
    {
      "id": "conflict-nbfc",
      "category": "userspace_conflicts",
      "severity": "blocker",
      "title": "nbfc_service",
      "value": "running",
      "message": "Stop nbfc_service before starting ventd.",
      "remediation_url": null,
      "remediation_command": "systemctl stop nbfc_service"
    }
  ]
}
```

### Flag surface

```
ventd doctor [flags]

  --json                  Output JSON instead of human text.
  --skip CATEGORY[,CAT…]  Skip categories (hardware, kernel, userspace_conflicts,
                          permissions, calibration, gpu).
  --only CATEGORY[,CAT…]  Run only listed categories.
  --no-color              Disable ANSI colour in text output.
  --quiet                 Suppress OK lines, show warnings + blockers only.
  --verbose               Include diagnostic detail (raw DMI strings, etc.).
  --schema                Print JSON schema to stdout, exit 0.
```

### Out-of-scope (explicit)

- **No remediation actions.** Doctor reports, does not fix. "Suggest a fix" is in the message field; user runs the suggested command separately. Auto-fix is a footgun in a fan controller and we don't want it.
- **No network.** Doctor never makes HTTP calls. `remediation_url` is for the user to visit, not for doctor to fetch.
- **No long-running checks.** Per RULE-DOCTOR-09, each check ≤200ms. Live RPM probing, calibration probing, etc. are not doctor's job — those belong to `ventd calibrate`.
- **No write permissions.** No `--fix` flag exists.
- **No GUI.** Web UI consumption is spec-11.
- **No daemon mode.** Doctor is one-shot CLI. It does not run continuously.

---

## Definition of done

- [ ] `cmd/ventd/doctor.go` registers the subcommand. `ventd doctor --help` prints usage.
- [ ] All 10 RULE-DOCTOR-* rules bound to subtests under `internal/doctor/`. `tools/rulelint` returns 0.
- [ ] `bios_known_bad.go` seeded with at least 15 entries covering Gigabyte Z690/Z790, MSI Z690/Z790, ASUS ROG (Q-Fan), known ThinkPad EC overrides, and Lenovo Legion firmware curve cases (cross-ref hwmon-research §17 and controllability map).
- [ ] Text output renders correctly on a TTY without ANSI support (test with `--no-color`).
- [ ] JSON output validates against `docs/doctor-schema.json` (a JSON-schema file shipped in the repo).
- [ ] `ventd doctor --json` runs in <2 seconds wall-clock on the dev container.
- [ ] `ventd doctor` runs as unprivileged user without panicking.
- [ ] `ventd doctor` returns correct exit codes per RULE-DOCTOR-02. Subtest verifies all four exit-code cases (0, 1, 2, 3).
- [ ] Conflict-detect logic shared with spec-03 amendment `conflicts_with_userspace` resolver — confirmed by `go list -deps ./internal/doctor | grep <shared-package>`.
- [ ] DMI fingerprint logic shared with hwdb — RULE-DOCTOR-05 subtest confirms identical output for identical input.
- [ ] `docs/doctor.md` covers: synopsis, all flag meanings, output format with examples, exit codes, JSON schema reference, "how to contribute a known-bad BIOS entry" workflow.
- [ ] CHANGELOG entry under v0.6.0 `### Added`: `ventd doctor` preflight diagnostic command.
- [ ] Conventional commit at boundaries: `feat(doctor): preflight subcommand with 10 read-only checks`.
- [ ] PR description explicitly notes: "spec-11 first-run wizard depends on `--json` schema_version 1; bumping schema is a breaking change requiring spec amendment."

---

## Explicit non-goals

- No active hardware probing. Doctor reads `/sys`, runs `aa-status`, reads DMI, queries systemd, reads ventd state files. It does NOT write PWM, does NOT run calibration, does NOT toggle modules.
- No network checks. Doctor does not test internet connectivity, does not check for ventd updates, does not phone home.
- No config validation beyond what hwdb does internally. Validating user-edited `/etc/ventd/config.yaml` is out of scope; that's a separate `ventd config validate` subcommand if/when needed.
- No diagnostic bundle generation. That's `ventd diag bundle` (pr2c). Doctor and diag are siblings, not parent/child.
- No first-run wizard logic. Doctor produces JSON; spec-11 wizard consumes it. Branching, redirects, UI flow are spec-11's problem.
- No installer integration. Doctor does not run during `apt install ventd`. Users invoke it manually or via the wizard.

---

## Red flags — stop and page Phoenix

- A check needs more than 500ms — surface for redesign. Doctor latency budget is non-negotiable; spec-11 wizard expects sub-2s response and we don't want users staring at a spinner.
- A check measures >100ms in dev — surface, likely indicates wrong approach (shelling out instead of reading /sys directly, or unnecessary subprocess invocation).
- A check would need write permissions — surface, RULE-DOCTOR-01 violation, redesign needed.
- A check produces output that depends on the order of execution — surface, checks must be independent. Reorderability is what makes `--skip`/`--only` work.
- BIOS known-bad entries balloon past 50 — surface, this is becoming a maintenance burden and we should consider whether the catalog itself should carry per-board BIOS-warning fields.
- Total CC spend crosses $35 — surface progress, request continuation.

---

## CC session prompt — copy/paste this

```
spec-10 implementation. Read /mnt/project/spec-10-doctor.md (full spec) and /mnt/project/spec-06-install-contract.md (predecessor pattern reference). 

Sonnet only. No subagents. Single PR. Conventional commits.

Branch: spec-10/doctor

Files to create per the spec, files to modify per the spec, no ghost code (verify with `go list -deps ./cmd/ventd | grep internal/doctor` post-impl).

Key invariants:
- RULE-DOCTOR-01 read-only (static analysis subtest)
- RULE-DOCTOR-02 stable exit codes (0/1/2/3)
- RULE-DOCTOR-05 fingerprint parity with runtime hwdb (no parallel impl)
- RULE-DOCTOR-06 reuse spec-03 amendment conflicts_with_userspace resolver
- RULE-DOCTOR-08 JSON schema_version "1" pinned (spec-11 wizard consumes)
- RULE-DOCTOR-09 <2s wall-clock total, 200ms per-check timeout, serial execution (no goroutine fan-out)
- RULE-DOCTOR-10 experimental-flags check reads from hwdiag.Store ComponentExperimental (no parallel impl)

Stop and surface to Phoenix if:
- BIOS known-bad list grows past 50 entries
- Any check needs >200ms (or measures >100ms in dev — likely wrong approach)
- Total spend crosses $35

Verification before marking done:
1. go test ./internal/doctor/... -v -count=1
2. go test ./... -count=1 (no PR 2a/2b/2c regressions)
3. golangci-lint run ./...
4. tools/rulelint
5. ventd doctor on dev container — all four exit codes reachable via fixtures
6. ventd doctor --json | jq '.schema_version' == "1"

PR body must call out: "spec-11 wizard pins schema_version 1; bumping is breaking."

Estimate: $25-40, 35-55 minutes.
```

---

## Cost discipline notes

Doctor is cheap because:
- Mostly read-only, no novel algorithms.
- Reuses hwdb matcher, calibration store reader, spec-03 amendment conflict resolver — substantial code reuse.
- 9 invariants is moderate (vs spec-03 PR 2a's 14, spec-06's 4).
- Output formatting is straightforward; both text and JSON are stdlib-only.
- Synthetic fixtures only — no HIL needed for the PR. Phoenix's desktop is the first real-hardware test post-merge, but that's manual not CC work.

Risks that could inflate cost:
- BIOS known-bad seeding is research-heavy. Mitigation: keep the v0.5.0 list minimum-viable (15 entries), expand in v0.5.x patches as community reports come in.
- AppArmor check varies by distro. Mitigation: graceful-skip-if-not-installed, don't try to handle every AA edge case.
- JSON schema versioning ceremony. Mitigation: ship v1 frozen, do not bikeshed minor variants.

Total target: $25-40 Sonnet. spec-11 wizard ($40-60) is the heavier sibling.

---

**End of spec-10-doctor.md.**
